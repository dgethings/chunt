package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// ACL family diagnostics (chunt-otg.9): family cross-satisfaction is
// impossible, duplicates flag per family, numbered names round-trip.

// A v4 definition must not satisfy a v6 reference (this currently has no
// v6 reference form until chunt-otg.13; here the isolation is asserted in
// the direction that exists today: a mac/v6/arp definition does not satisfy
// an `ip access-group` v4 reference).
func TestACLFamilyCrossSatisfactionImpossible(t *testing.T) {
	src := `mac access-list extended SAME-NAME
 permit any any
!
ipv6 access-list SAME-NAME
 permit ipv6 any any
!
arp access-list SAME-NAME
 permit ip any mac any
!
interface Gi0/0
 ip access-group SAME-NAME in
!
`
	diag, ok := findDiagByCode(openDiags(t, src), "undefined-acl")
	if !ok {
		t.Fatalf("v4 reference was satisfied by a non-v4 family definition")
	}
	if !strings.Contains(diag.Message, "SAME-NAME") {
		t.Fatalf("flagged wrong name: %q", diag.Message)
	}
}

// Duplicate definitions flag per family (each family's kind keys its own
// first-seen entry in the duplicate pass).
func TestACLFamilyDuplicatesFlagged(t *testing.T) {
	src := `ipv6 access-list DUP6
 deny ipv6 any any
!
ipv6 access-list DUP6
 permit ipv6 any any
!
arp access-list DUPA
 permit ip any mac any
!
arp access-list DUPA
 deny ip any mac any
!
`
	counts := map[string]int{}
	for _, d := range openDiags(t, src) {
		switch d.Code {
		case "duplicate-ipv6-acl", "duplicate-arp-acl", "duplicate-mac-acl":
			counts[d.Code]++
		}
	}
	if counts["duplicate-ipv6-acl"] != 1 || counts["duplicate-arp-acl"] != 1 {
		t.Fatalf("family duplicates = %v, want one of each", counts)
	}
}

// Numbered-name normalization round-trips: `access-list 99 ...` definitions
// satisfy `ip access-group 99 ...` references (string form, no padding).
func TestNumberedACLNameRoundTrip(t *testing.T) {
	src := `access-list 99 permit 10.0.0.0 255.0.0.0
access-list 100 permit ip any any
!
interface Gi0/0
 ip access-group 99 in
 ip access-group 100 out
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-acl" {
			t.Fatalf("numbered ACL reference not satisfied by its definition: %q", d.Message)
		}
	}
}
