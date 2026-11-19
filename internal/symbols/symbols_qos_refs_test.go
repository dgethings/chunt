package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// QoS attach points (chunt-otg.14): service-policy variants all cite the
// policy-map as their LAST arg — plain, type-qualified, and child policies.

func TestExtractServicePolicyVariants(t *testing.T) {
	src := `policy-map PM-IFACE
 class CM1
  service-policy PM-CHILD
!
policy-map PM-CHILD
!
class-map match-any CM1
!
policy-map PM-QUEUE
!
policy-map CP-PLANE
!
interface Gi0/0
 service-policy input PM-IFACE
 service-policy output PM-QUEUE
 service-policy type queueing output PM-QUEUE
!
control-plane
 service-policy input CP-PLANE
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	pm := refNames(refs, symbols.KindPolicyMap)
	want := []string{"PM-CHILD", "PM-IFACE", "PM-QUEUE", "PM-QUEUE", "CP-PLANE"}
	if len(pm) != len(want) {
		t.Fatalf("policy-map refs = %v, want %v", pm, want)
	}
	for i := range want {
		if pm[i] != want[i] {
			t.Fatalf("policy-map refs[%d] = %q, want %q (all: %v)", i, pm[i], want[i], pm)
		}
	}
	// The class body's child-policy form carries no direction word: the
	// name must not be misread as a direction or type keyword.
	for _, n := range pm {
		if n == "input" || n == "output" || n == "type" || n == "queueing" || n == "control" {
			t.Fatalf("keyword leaked as policy-map reference: %v", pm)
		}
	}
}
