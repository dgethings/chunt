package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Prefix-list undefined-reference diagnostics (chunt-otg.10).

func TestUndefinedPrefixListFlagged(t *testing.T) {
	src := `route-map RM permit 10
 match ip address prefix-list MISSING
!
router bgp 100
 neighbor 1.1.1.1 prefix-list GONE out
!
`
	diags := openDiags(t, src)
	var flagged []string
	for _, d := range diags {
		if d.Code == "undefined-prefix-list" {
			flagged = append(flagged, d.Message)
		}
	}
	if len(flagged) != 2 {
		t.Fatalf("got %d undefined-prefix-list diagnostics (%v), want 2", len(flagged), diags)
	}
	if !contains(flagged[0], "MISSING") || !contains(flagged[1], "GONE") {
		t.Fatalf("wrong names flagged: %v", flagged)
	}
}

// Varargs match clauses flag EACH missing list independently; defined lists
// are never flagged.
func TestPrefixListVarargsFlaggedPerName(t *testing.T) {
	src := `ip prefix-list EDGE seq 10 permit 10.0.0.0/8
!
route-map RM permit 10
 match ip address prefix-list EDGE MISSING1 MISSING2
!
`
	var count int
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-prefix-list" {
			count++
			if strings.Contains(d.Message, "EDGE") {
				t.Fatalf("defined list flagged: %q", d.Message)
			}
		}
	}
	if count != 2 {
		t.Fatalf("got %d undefined-prefix-list diagnostics, want 2 (MISSING1, MISSING2)", count)
	}
}

// A defined list satisfies references from every form (match, neighbor,
// distribute-list), including across if/else branches (chunt-otg.21 rule 2).
func TestDefinedPrefixListSatisfiesAllForms(t *testing.T) {
	src := `ip prefix-list EDGE-IN seq 10 permit 10.0.0.0/8
!
route-map RM permit 10
 match ip address prefix-list EDGE-IN
!
router bgp 100
 neighbor 1.1.1.1 prefix-list EDGE-IN out
 distribute-list prefix EDGE-IN in
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-prefix-list" {
			t.Fatalf("defined list still flagged: %q", d.Message)
		}
	}
}
