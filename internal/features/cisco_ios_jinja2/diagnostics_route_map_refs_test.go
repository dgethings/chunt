package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Route-map undefined-reference diagnostics from routing protocols
// (chunt-otg.11).

func TestUndefinedRouteMapFromRoutingProtocols(t *testing.T) {
	src := `router bgp 100
 neighbor 1.1.1.1 route-map MISSING-IN in
 redistribute connected route-map GONE
 neighbor 2.2.2.2 advertise-map ABSENT exist-map NOPE
 distribute-list route-map DL-MISSING in
!
`
	var names []string
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-route-map" {
			for _, want := range []string{"MISSING-IN", "GONE", "ABSENT", "NOPE", "DL-MISSING"} {
				if strings.Contains(d.Message, want) {
					names = append(names, want)
				}
			}
		}
	}
	if len(names) != 5 {
		t.Fatalf("flagged %v, want all five undefined maps", names)
	}
}

// Defined maps satisfy references from every routing-protocol form, and the
// pre-existing ip policy form keeps working.
func TestDefinedRouteMapSatisfiesAllForms(t *testing.T) {
	src := `route-map OK permit 10
!
router bgp 100
 neighbor 1.1.1.1 route-map OK in
 redistribute static route-map OK
 table-marker-placeholder no-op
!
interface Vlan1
 ip policy route-map OK
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-route-map" {
			t.Fatalf("defined map still flagged: %q", d.Message)
		}
	}
}
