package cisco_ios_jinja2_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
)

// TestRepeatedCommandDiagnostics covers the repeated-command pass (chunt-4ih):
// valid-but-smelly config practice earns Warnings, not Errors.
//
//   - duplicate-command: identical command text appearing 2+ times in one
//     section (copy-paste errors, duplicate static routes / neighbors). Any
//     repeat of an exact line is almost always unintended, so this rule is
//     generic — no curated registry needed.
//   - redefined-setting: curated scalar redefinitions — a hostname value
//     replacement (last definition wins) and multiple primary `ip address`
//     definitions on one interface (later primaries replace earlier ones;
//     `secondary` lines are additive and exempt).
//
// Both carry relatedInformation back to the first definition. "pin" cases
// document deliberate exclusions: same-value hostname duplicates stay with
// the tier-2 duplicate-hostname pass, negated statements are ignored, and
// commands inside Jinja blocks are out of scope in v1 (mutually exclusive
// branches would false-positive).
func TestRepeatedCommandDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantCode string
		wantHit  bool
		wantSubs []string
	}{
		// --- duplicate-command (generic exact duplicate) ---
		{
			name:     "identical static route twice",
			src:      "ip route 10.0.0.0 255.0.0.0 10.0.0.2\nip route 10.0.0.0 255.0.0.0 10.0.0.2\n",
			wantCode: "duplicate-command",
			wantHit:  true,
			wantSubs: []string{`"ip route 10.0.0.0 255.0.0.0 10.0.0.2"`},
		},
		{
			name:     "identical neighbor twice (statement node)",
			src:      "router bgp 100\n neighbor 10.0.0.2 remote-as 200\n neighbor 10.0.0.2 remote-as 200\n!\n",
			wantCode: "duplicate-command",
			wantHit:  true,
			wantSubs: []string{`"neighbor 10.0.0.2 remote-as 200"`},
		},
		{
			name:     "identical description twice in one interface",
			src:      "interface Gi0/0\n description managed by ansible\n description managed by ansible\n!\n",
			wantCode: "duplicate-command",
			wantHit:  true,
		},
		{
			name:     "third occurrence earns a second warning",
			src:      "ip route 10.0.0.0 255.0.0.0 10.0.0.2\nip route 10.0.0.0 255.0.0.0 10.0.0.2\nip route 10.0.0.0 255.0.0.0 10.0.0.2\n",
			wantCode: "duplicate-command",
			wantHit:  true,
		},
		{
			name:     "floating static (same prefix, different next-hop/distance) is not a duplicate",
			src:      "ip route 10.0.0.0 255.0.0.0 10.0.0.2\nip route 10.0.0.0 255.0.0.0 10.0.0.3 25\n",
			wantCode: "duplicate-command",
			wantHit:  false,
		},
		{
			name:     "same command in different sections is fine",
			src:      "interface Gi0/0\n description core uplink\n!\ninterface Gi1/0\n description core uplink\n!\n",
			wantCode: "duplicate-command",
			wantHit:  false,
		},
		{
			name:     "negation then set is fine (pin: negated statements skipped)",
			src:      "ip route 10.0.0.0 255.0.0.0 10.0.0.2\nno ip route 10.0.0.0 255.0.0.0 10.0.0.2\nip route 10.0.0.0 255.0.0.0 10.0.0.2\n",
			wantCode: "duplicate-command",
			wantHit:  false,
		},
		{
			name:     "jinja-block-wrapped command excluded in v1 (pin)",
			src:      "{% if primary %}\nhostname a\n{% endif %}\nhostname b\n",
			wantCode: "redefined-setting",
			wantHit:  false,
		},

		// --- redefined-setting: hostname ---
		{
			name:     "hostname value replacement",
			src:      "hostname r1\nhostname r2\n",
			wantCode: "redefined-setting",
			wantHit:  true,
			wantSubs: []string{`hostname`, `"r2"`, `"r1"`, "replaces"},
		},
		{
			name:     "same hostname twice stays with tier-2 duplicate-hostname (pin)",
			src:      "hostname r1\nhostname r1\n",
			wantCode: "redefined-setting",
			wantHit:  false,
		},

		// --- redefined-setting: primary ip address ---
		{
			name:     "two primary addresses on one interface",
			src:      "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n ip address 10.0.0.2 255.255.255.0\n!\n",
			wantCode: "redefined-setting",
			wantHit:  true,
			wantSubs: []string{"ip address", `"10.0.0.1 255.255.255.0"`, "replaces"},
		},
		{
			name:     "primary plus secondary is fine",
			src:      "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n ip address 10.0.0.2 255.255.255.0 secondary\n!\n",
			wantCode: "redefined-setting",
			wantHit:  false,
		},
		{
			name:     "one address per interface across sections is fine",
			src:      "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n!\ninterface Gi1/0\n ip address 10.0.1.1 255.255.255.0\n!\n",
			wantCode: "redefined-setting",
			wantHit:  false,
		},
		{
			name:     "identical primary twice is a duplicate-command, not redefined-setting",
			src:      "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n ip address 10.0.0.1 255.255.255.0\n!\n",
			wantCode: "duplicate-command",
			wantHit:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			hits := diagsByCode(diags, tc.wantCode)
			if tc.wantHit {
				if len(hits) < 1 {
					t.Fatalf("want at least 1 %s diagnostic, got %d: %+v", tc.wantCode, len(hits), diags)
				}
				for _, d := range hits {
					if d.Severity != protocol.SeverityWarning {
						t.Errorf("severity: got %d, want Warning (2)", d.Severity)
					}
					if d.Source != "chunt" {
						t.Errorf("source: got %q, want \"chunt\"", d.Source)
					}
				}
				for _, sub := range tc.wantSubs {
					if !contains(hits[0].Message, sub) {
						t.Errorf("message %q missing substring %q", hits[0].Message, sub)
					}
				}
			} else if len(hits) != 0 {
				t.Errorf("want no %s diagnostic, got %d: %+v", tc.wantCode, len(hits), hits)
			}
		})
	}
}

// TestRepeatedCommandDiagnostics_RelatedInfo asserts the Warning carries
// relatedInformation back to the first definition, mirroring the tier-2
// duplicate-definition pass.
func TestRepeatedCommandDiagnostics_RelatedInfo(t *testing.T) {
	src := "hostname r1\nhostname r2\n"
	diags := openDiags(t, src)
	hits := diagsByCode(diags, "redefined-setting")
	if len(hits) != 1 {
		t.Fatalf("want 1 redefined-setting, got %d: %+v", len(hits), diags)
	}
	d := hits[0]
	// Anchored on the SECOND line (the replacement).
	if d.Range.Start.Line != 1 {
		t.Errorf("range line: got %d, want 1 (the replacing definition)", d.Range.Start.Line)
	}
	if len(d.RelatedInformation) == 0 {
		t.Fatalf("expected relatedInformation pointing to the first definition")
	}
	ri := d.RelatedInformation[0]
	if ri.Location.URI != "file:///test.ios.j2" {
		t.Errorf("related URI: got %q, want file:///test.ios.j2", ri.Location.URI)
	}
	if ri.Location.Range.Start.Line != 0 {
		t.Errorf("related range line: got %d, want 0 (the first definition)", ri.Location.Range.Start.Line)
	}
	if ri.Message != "first defined here" {
		t.Errorf("related message: got %q, want \"first defined here\"", ri.Message)
	}
}

// TestRepeatedCommandDiagnostics_ThirdOccurrence pins that every occurrence
// after the first earns its own warning, all related back to the first.
func TestRepeatedCommandDiagnostics_ThirdOccurrence(t *testing.T) {
	src := "ip route 10.0.0.0 255.0.0.0 10.0.0.2\nip route 10.0.0.0 255.0.0.0 10.0.0.2\nip route 10.0.0.0 255.0.0.0 10.0.0.2\n"
	diags := openDiags(t, src)
	hits := diagsByCode(diags, "duplicate-command")
	if len(hits) != 2 {
		t.Fatalf("want 2 duplicate-command diagnostics (lines 1 and 2), got %d: %+v", len(hits), hits)
	}
	if hits[0].Range.Start.Line != 1 || hits[1].Range.Start.Line != 2 {
		t.Errorf("anchors: got lines %d and %d, want 1 and 2", hits[0].Range.Start.Line, hits[1].Range.Start.Line)
	}
	for _, d := range hits {
		if len(d.RelatedInformation) == 0 || d.RelatedInformation[0].Location.Range.Start.Line != 0 {
			t.Errorf("related info must point at the first definition (line 0)")
		}
	}
}
