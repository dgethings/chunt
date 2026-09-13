package cisco_ios_jinja2_test

import (
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
)

// TestProtocolMismatchDiagnostics covers the cross-protocol wrong-section pass
// (chunt-pwz). A command owned by one routing protocol (resolved via the
// hybrid node-kind / keyword-text registry) and placed inside another
// protocol's router or address-family section yields exactly one Error with
// Code "protocol-mismatch"; same-protocol usage, shared commands, top-level
// placement, and a clean multi-protocol config produce none.
func TestProtocolMismatchDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantHit bool
		// wantSubs are substrings required in the Message of the single hit.
		wantSubs []string
	}{
		// --- positive: cross-protocol ---
		{
			name:     "area (ospf) inside router bgp -> Error",
			src:      "router bgp 100\n area 0 range 10.0.0.0 255.0.0.0\n!\n",
			wantHit:  true,
			wantSubs: []string{`"area"`, "OSPF", "router bgp"},
		},
		{
			name:     "aggregate-address (bgp) inside router ospf -> Error",
			src:      "router ospf 1\n aggregate-address 10.0.0.0 255.0.0.0\n!\n",
			wantHit:  true,
			wantSubs: []string{"aggregate-address", "BGP", "router ospf"},
		},
		{
			name:     "auto-cost (ospf) inside router bgp -> Error (node-kind signal)",
			src:      "router bgp 100\n auto-cost reference-bandwidth 100000\n!\n",
			wantHit:  true,
			wantSubs: []string{"OSPF", "router bgp"},
		},
		{
			name:     "graceful-restart (bgp) inside router ospf -> Error (node-kind signal)",
			src:      "router ospf 1\n graceful-restart\n!\n",
			wantHit:  true,
			wantSubs: []string{"BGP", "router ospf"},
		},
		{
			name:     "no area (ospf) inside router bgp still flagged (negated descends)",
			src:      "router bgp 100\n no area 0 range 10.0.0.0 255.0.0.0\n!\n",
			wantHit:  true,
			wantSubs: []string{`"area"`, "OSPF", "router bgp"},
		},
		{
			name:     "area inside address-family inherits bgp from enclosing router",
			src:      "router bgp 100\n address-family ipv4\n  area 0 range 10.0.0.0 255.0.0.0\n !\n!\n",
			wantHit:  true,
			wantSubs: []string{`"area"`, "OSPF", "router bgp"},
		},

		// --- negative: same protocol (no signal) ---
		{name: "area inside router ospf -> none", src: "router ospf 1\n area 0 range 10.0.0.0 255.0.0.0\n!\n"},
		{name: "aggregate-address inside router bgp -> none", src: "router bgp 100\n aggregate-address 10.0.0.0 255.0.0.0\n!\n"},
		{name: "auto-cost inside router ospf -> none", src: "router ospf 1\n auto-cost reference-bandwidth 100000\n!\n"},
		{name: "graceful-restart inside router bgp -> none", src: "router bgp 100\n graceful-restart\n!\n"},

		// --- negative: shared commands valid in both protocols ---
		{name: "network inside router bgp -> none", src: "router bgp 100\n network 10.0.0.0\n!\n"},
		{name: "network inside router ospf -> none", src: "router ospf 1\n network 10.0.0.0 0.0.0.255\n!\n"},
		{name: "neighbor inside router bgp -> none", src: "router bgp 100\n neighbor 10.0.0.1 remote-as 200\n!\n"},
		{name: "neighbor inside router ospf -> none", src: "router ospf 1\n neighbor 10.0.0.1\n!\n"},
		{name: "redistribute inside router bgp -> none", src: "router bgp 100\n redistribute connected\n!\n"},
		{name: "redistribute inside router ospf -> none", src: "router ospf 1\n redistribute connected\n!\n"},

		// --- negative: no enclosing router section ---
		{name: "area at top level -> none (no protocol)", src: "area 0 range 10.0.0.0 255.0.0.0\n!\n"},
		{name: "aggregate-address at top level -> none", src: "aggregate-address 10.0.0.0 255.0.0.0\n!\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			hits := diagsByCode(diags, "protocol-mismatch")
			if tc.wantHit {
				if len(hits) != 1 {
					t.Fatalf("want exactly 1 protocol-mismatch diagnostic, got %d: %+v", len(hits), hits)
				}
				d := hits[0]
				if d.Severity != protocol.SeverityError {
					t.Errorf("severity: got %d, want Error (1)", d.Severity)
				}
				if d.Source != "chunt" {
					t.Errorf("source: got %q, want \"chunt\"", d.Source)
				}
				for _, sub := range tc.wantSubs {
					if !strings.Contains(d.Message, sub) {
						t.Errorf("message %q missing substring %q", d.Message, sub)
					}
				}
			} else {
				if len(hits) != 0 {
					t.Errorf("want no protocol-mismatch diagnostic, got %d: %+v", len(hits), hits)
				}
			}
		})
	}
}

// TestProtocolMismatchDiagnostics_CleanMultiProtocol is the false-positive
// guard: a config that correctly uses router bgp (bgp commands) and router
// ospf (ospf commands) side by side must produce zero protocol-mismatch
// diagnostics.
func TestProtocolMismatchDiagnostics_CleanMultiProtocol(t *testing.T) {
	src := `router bgp 100
 aggregate-address 10.0.0.0 255.0.0.0
 graceful-restart
 neighbor 10.0.0.2 remote-as 200
 network 10.0.0.0
!
router ospf 1
 area 0 range 10.0.0.0 255.0.0.0
 auto-cost reference-bandwidth 100000
 network 10.0.0.0 0.0.0.255
!
`
	diags := openDiags(t, src)
	if hits := diagsByCode(diags, "protocol-mismatch"); len(hits) != 0 {
		t.Errorf("clean multi-protocol config must not produce protocol-mismatch diagnostics; got %d: %+v", len(hits), hits)
	}
}

func diagsByCode(diags []protocol.Diagnostic, code string) []protocol.Diagnostic {
	var out []protocol.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}
