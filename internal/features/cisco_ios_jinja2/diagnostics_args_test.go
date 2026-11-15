package cisco_ios_jinja2_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
)

// TestInvalidMaskDiagnostics covers the snippet-driven argument-value pass
// (chunt-at0). A command whose keyword-DB snippet declares a subnet-mask
// argument (${N:mask} / ${N:subnet-mask} / ${N:network-mask}) and whose
// document token in that slot is not a dotted-quad with contiguous leading
// one-bits yields exactly one Error with Code "invalid-mask", anchored on the
// offending token. Valid masks, Jinja-templated values, unknown keywords, and
// lines that do not follow any snippet form produce none.
func TestInvalidMaskDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantHit bool
		// wantSubs are substrings required in the Message of each hit when
		// wantHit is true and exactly one hit is expected.
		wantSubs []string
	}{
		// --- positive: invalid mask values ---
		{
			name:     "arbitrary word as ip address mask -> Error",
			src:      "interface ethernet1/1\n   ip address 10.0.0.1 wibble\n!\n",
			wantHit:  true,
			wantSubs: []string{"subnet mask", `"wibble"`},
		},
		{
			name:     "non-contiguous mask 255.0.255.0 -> Error",
			src:      "interface Gi0/0\n ip address 10.0.0.1 255.0.255.0\n!\n",
			wantHit:  true,
			wantSubs: []string{"subnet mask", `"255.0.255.0"`},
		},
		{
			name:     "wildcard mask 0.0.0.255 in subnet-mask slot -> Error",
			src:      "interface Gi0/0\n ip address 10.0.0.1 0.0.0.255\n!\n",
			wantHit:  true,
			wantSubs: []string{"subnet mask"},
		},
		{
			name:     "host address 10.0.0.1 as mask -> Error",
			src:      "interface Gi0/0\n ip address 10.0.0.1 10.0.0.1\n!\n",
			wantHit:  true,
			wantSubs: []string{"subnet mask", `"10.0.0.1"`},
		},
		{
			name:     "negated command still checked (negated_statement descends)",
			src:      "interface Gi0/0\n no ip address 10.0.0.1 wibble\n!\n",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},
		{
			name:     "ip route mask slot -> Error",
			src:      "ip route 10.0.0.0 wibble 10.0.0.2\n",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},
		{
			name:     "ip route vrf optional group consumed then mask -> Error",
			src:      "ip route vrf FOO 10.0.0.0 wibble 10.0.0.2\n",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},

		// --- negative: valid mask values ---
		{name: "valid /24 mask", src: "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n!\n"},
		{name: "valid /23 mask 255.255.254.0", src: "interface Gi0/0\n ip address 10.0.0.1 255.255.254.0\n!\n"},
		{name: "valid host mask 255.255.255.255", src: "interface Gi0/0\n ip address 10.0.0.1 255.255.255.255\n!\n"},
		{name: "valid zero mask 0.0.0.0", src: "interface Gi0/0\n ip address 10.0.0.1 0.0.0.0\n!\n"},
		{name: "valid mask with trailing secondary keyword", src: "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0 secondary\n!\n"},
		{name: "valid ip route mask", src: "ip route 10.0.0.0 255.0.0.0 10.0.0.2\n"},
		{name: "valid ip route vrf mask", src: "ip route vrf FOO 10.0.0.0 255.0.0.0 10.0.0.2\n"},

		// --- negative: Jinja-templated values are not statically checkable ---
		{name: "jinja output as both args", src: "interface Gi0/0\n ip address {{ addr }} {{ mask }}\n!\n"},
		{name: "jinja output as mask only", src: "interface Gi0/0\n ip address 10.0.0.1 {{ mask }}\n!\n"},

		// --- negative: no snippet signal ---
		{name: "unknown leading keyword", src: "wibble foo bar\n"},
		{name: "keyword with no mask argument", src: "snmp-server location 1.2.3.4\n"},
		{name: "ip address dhcp sub-keyword has its own snippet", src: "interface Gi0/0\n ip address dhcp\n!\n"},
		// v1 limitation, pinned deliberately: dedicated *_statement nodes are
		// not snippet-aligned (their named children omit the keyword text).
		{name: "aggregate-address statement not validated in v1", src: "router bgp 100\n aggregate-address 10.0.0.0 wibble\n!\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			hits := diagsByCode(diags, "invalid-mask")
			if tc.wantHit {
				if len(hits) != 1 {
					t.Fatalf("want exactly 1 invalid-mask diagnostic, got %d: %+v", len(hits), hits)
				}
				d := hits[0]
				if d.Severity != protocol.SeverityError {
					t.Errorf("severity: got %d, want Error (1)", d.Severity)
				}
				if d.Source != "chunt" {
					t.Errorf("source: got %q, want \"chunt\"", d.Source)
				}
				for _, sub := range tc.wantSubs {
					if !contains(d.Message, sub) {
						t.Errorf("message %q missing substring %q", d.Message, sub)
					}
				}
			} else if len(hits) != 0 {
				t.Errorf("want no invalid-mask diagnostic, got %d: %+v", len(hits), hits)
			}
		})
	}
}

// TestInvalidMaskDiagnostics_RangeAndMessage pins the exact range (the
// offending token only, not the whole line) and message for the canonical
// example:ios.j2 case.
func TestInvalidMaskDiagnostics_RangeAndMessage(t *testing.T) {
	src := "interface ethernet1/1\n   ip address 10.0.0.1 wibble\n!\n"
	diags := openDiags(t, src)
	hits := diagsByCode(diags, "invalid-mask")
	if len(hits) != 1 {
		t.Fatalf("want exactly 1 invalid-mask diagnostic, got %d: %+v", len(hits), hits)
	}
	d := hits[0]
	// Line 1: "   ip address 10.0.0.1 wibble" — the token `wibble` spans
	// columns 23..29 (UTF-16 code units == bytes here).
	if d.Range.Start.Line != 1 || d.Range.Start.Character != 23 || d.Range.End.Line != 1 || d.Range.End.Character != 29 {
		t.Errorf("range: got %d:%d-%d:%d, want 1:23-1:29", d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Line, d.Range.End.Character)
	}
	wantMsg := `expected a subnet mask (e.g. 255.255.255.0), got "wibble"`
	if d.Message != wantMsg {
		t.Errorf("message: got %q, want %q", d.Message, wantMsg)
	}
}

// TestInvalidMaskDiagnostics_BothBadSlots is the multi-hit case: every bad
// mask slot on a line (and across lines) earns its own diagnostic.
func TestInvalidMaskDiagnostics_MultipleLines(t *testing.T) {
	src := "interface Gi0/0\n ip address 10.0.0.1 wibble\n ip address 10.0.0.2 wobble\n!\n"
	diags := openDiags(t, src)
	hits := diagsByCode(diags, "invalid-mask")
	if len(hits) != 2 {
		t.Fatalf("want 2 invalid-mask diagnostics (one per line), got %d: %+v", len(hits), hits)
	}
	if !contains(hits[0].Message, "wibble") || !contains(hits[1].Message, "wobble") {
		t.Errorf("messages must name each offending token: %q / %q", hits[0].Message, hits[1].Message)
	}
	if hits[0].Range.Start.Line == hits[1].Range.Start.Line {
		t.Errorf("expected diagnostics on different lines, both on %d", hits[0].Range.Start.Line)
	}
}

// contains is a tiny local helper so this file does not depend on strings
// merely for substring checks (the suite's other files keep the same style).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
