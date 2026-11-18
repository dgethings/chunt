package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Prefix-list reference extraction (chunt-otg.10).

func refNames(refs []symbols.Reference, kind symbols.Kind) []string {
	var out []string
	for _, r := range refs {
		if r.Kind == kind {
			out = append(out, r.Name)
		}
	}
	return out
}

func TestExtractPrefixListReferences(t *testing.T) {
	src := `route-map RM permit 10
 match ip address prefix-list PL1 PL2
 match ipv6 address prefix-list PL6
!
router bgp 100
 neighbor 1.1.1.1 prefix-list PL-OUT out
 neighbor 2.2.2.2 prefix-list PL-IN in
 distribute-list prefix PL-DP out
 distribute-list prefix-list PL-DPL in
 match ip address ACL1
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	pl := refNames(refs, symbols.KindPrefixList)
	want := []string{"PL1", "PL2", "PL6", "PL-OUT", "PL-IN", "PL-DP", "PL-DPL"}
	if len(pl) != len(want) {
		t.Fatalf("prefix-list refs = %v, want %v", pl, want)
	}
	for i := range want {
		if pl[i] != want[i] {
			t.Fatalf("prefix-list refs[%d] = %q, want %q (all: %v)", i, pl[i], want[i], pl)
		}
	}
	// Direction keywords must not leak as names.
	for _, n := range pl {
		if n == "in" || n == "out" {
			t.Fatalf("direction keyword leaked as reference: %v", pl)
		}
	}
}

// Longest-match-wins shadowing fix: `match ip address prefix-list PL1` must
// produce a prefix-list reference, NOT an acl reference literally named
// "prefix-list" (the pre-.10 behavior of the shorter `match ip address`
// spec), while plain `match ip address ACL1` keeps producing an ACL ref.
func TestPrefixListSpecShadowsACLSpec(t *testing.T) {
	src := `route-map RM permit 10
 match ip address prefix-list PL1
 match ip address ACL1
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	for _, r := range refs {
		if r.Kind == symbols.KindACL && r.Name == "prefix-list" {
			t.Fatalf("prefix-list keyword misread as ACL reference")
		}
	}
	if got := refNames(refs, symbols.KindPrefixList); len(got) != 1 || got[0] != "PL1" {
		t.Fatalf("prefix-list refs = %v, want [PL1]", got)
	}
	if got := refNames(refs, symbols.KindACL); len(got) != 1 || got[0] != "ACL1" {
		t.Fatalf("acl refs = %v, want [ACL1]", got)
	}
}
