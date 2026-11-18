package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Route-map references from routing protocols (chunt-otg.11).

func TestExtractRouteMapReferences(t *testing.T) {
	src := `router bgp 100
 neighbor 1.1.1.1 route-map RM-IN in
 neighbor 2.2.2.2 advertise-map ADV exist-map NONEX
 neighbor 3.3.3.3 unsuppress-map SUPP
 neighbor 4.4.4.4 default-originate route-map DEF
 redistribute connected route-map RED-CONN
 redistribute static route-map RED-STATIC
 redistribute ospf 1 route-map RED-OSPF
 distribute-list route-map DLRM in
!
router ospf 1
 redistribute bgp 65000 subnets route-map RED-BGP
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	rm := refNames(refs, symbols.KindRouteMap)
	want := []string{"RM-IN", "ADV", "NONEX", "SUPP", "DEF", "RED-CONN", "RED-STATIC", "RED-OSPF", "DLRM", "RED-BGP"}
	if len(rm) != len(want) {
		t.Fatalf("route-map refs = %v, want %v", rm, want)
	}
	for i := range want {
		if rm[i] != want[i] {
			t.Fatalf("route-map refs[%d] = %q, want %q (all: %v)", i, rm[i], want[i], rm)
		}
	}
}

// Keyword-scan must not misfire on non-keyword args: neighbor lines without
// a map keyword (remote-as, description, next-hop-self) cite nothing; the
// prefix-list neighbor form keeps citing prefix-lists (longest-match wins
// over the route-map keyword spec).
func TestRouteMapKeywordScanMisfiresGuarded(t *testing.T) {
	src := `router bgp 100
 neighbor 5.5.5.5 remote-as 65000
 neighbor 5.5.5.5 description peer
 neighbor 5.5.5.5 next-hop-self
 neighbor 6.6.6.6 prefix-list PL-NEIGH out
!
ip prefix-list PL-NEIGH seq 10 permit 10.0.0.0/8
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	if got := refNames(refs, symbols.KindRouteMap); len(got) != 0 {
		t.Fatalf("route-map refs = %v, want none", got)
	}
	if got := refNames(refs, symbols.KindPrefixList); len(got) != 1 || got[0] != "PL-NEIGH" {
		t.Fatalf("prefix-list refs = %v, want [PL-NEIGH]", got)
	}
}
