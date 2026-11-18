package cisco_ios_jinja2_test

import (
	"testing"
)

// Routing policy duplicate-definition coverage (chunt-otg.6).

// Prefix-list entries are additive per list: a second `seq 20` line extends
// PL1 and must NOT be flagged. Only a REPEATED seq within one list is a
// duplicate (the later entry replaces the earlier at merge time).
func TestDuplicatePrefixListSeqCollision(t *testing.T) {
	src := `ip prefix-list PL1 seq 10 permit 10.0.0.0/8
ip prefix-list PL1 seq 20 permit 20.0.0.0/8
ip prefix-list PL1 seq 10 deny 10.0.0.0/8
ip prefix-list PL2 seq 10 permit 30.0.0.0/8
!
`
	diags := openDiags(t, src)
	dupes := 0
	for _, d := range diags {
		if d.Code == "duplicate-prefix-list" {
			dupes++
			if !contains(d.Message, `PL1`) || !contains(d.Message, "seq 10") {
				t.Fatalf("wrong seq-collision diagnostic: %q", d.Message)
			}
		}
	}
	if dupes != 1 {
		t.Fatalf("got %d duplicate-prefix-list diagnostics, want exactly 1 (the repeated seq 10): %v", dupes, diags)
	}
}

// Community-list and as-path ACL entries are additive (multiple
// permit/deny lines per list) and carry no seq, so no duplicate-definition
// condition exists for them; exact-text repeats are owned by the
// duplicate-command pass instead.
func TestCommunityAndAsPathEntriesAreAdditive(t *testing.T) {
	src := `ip community-list standard C1 permit 65000:100
ip community-list standard C1 permit 65000:200
ip as-path access-list 10 permit ^65000$
ip as-path access-list 10 deny _65001$
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "duplicate-community-list" || d.Code == "duplicate-as-path-acl" {
			t.Fatalf("additive entry flagged as duplicate: %q", d.Message)
		}
	}
}

// A negated entry line removes a prefix-list entry and must not register a
// definition (chunt-otg.6 negation skip): a positive definition plus a
// later `no ip prefix-list` of the SAME entry leaves one registration — no
// duplicate. (The symbol-table pass is order-insensitive; a remove-then-
// re-add pattern leaves two positive definitions and is flagged — that is
// the repeatedTracker's concern, not this pass's.)
func TestNegatedPrefixListEntryNotADefinition(t *testing.T) {
	src := `ip prefix-list PL1 seq 10 permit 10.0.0.0/8
no ip prefix-list PL1 seq 10 permit 10.0.0.0/8
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "duplicate-prefix-list" {
			t.Fatalf("negated entry registered as a definition: %q", d.Message)
		}
	}
}
