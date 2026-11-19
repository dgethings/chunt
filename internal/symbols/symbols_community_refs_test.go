package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Community-list and as-path ACL references in route-maps (chunt-otg.12).

func TestExtractCommunityAndAsPathReferences(t *testing.T) {
	src := `route-map RM permit 10
 match community COMM1
 match community 5 exact-match
 match as-path 10 20
 match as-path AS-PATHS
!
ip community-list standard COMM1 permit 65000:100
ip community-list 5 permit 65000:200
ip as-path access-list 10 permit ^65000$
ip as-path access-list 20 permit _65001_
ip as-path access-list AS-PATHS permit _65002_
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	cl := refNames(refs, symbols.KindCommunityList)
	if len(cl) != 2 || cl[0] != "COMM1" || cl[1] != "5" {
		t.Fatalf("community-list refs = %v, want [COMM1 5]", cl)
	}
	ap := refNames(refs, symbols.KindAsPathACL)
	if len(ap) != 3 || ap[0] != "10" || ap[1] != "20" || ap[2] != "AS-PATHS" {
		t.Fatalf("as-path refs = %v, want [10 20 AS-PATHS]", ap)
	}
	// exact-match must not leak as a second community-list name.
	for _, n := range cl {
		if n == "exact-match" {
			t.Fatalf("exact-match keyword leaked as reference: %v", cl)
		}
	}
}
