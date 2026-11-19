package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Interface-name references (chunt-otg.16).

func TestExtractInterfaceReferences(t *testing.T) {
	src := `ip route 10.0.0.0 255.0.0.0 Gi0/1
ip route 30.0.0.0 255.0.0.0 Gi0/2 100 name MYROUTE
ip route vrf CUST1 50.0.0.0 255.0.0.0 Te1/1
!
route-map RMP permit 10
 match interface Gi0/0 Gi0/1
 set interface Te0/1
!
logging source-interface Lo0
ntp source Lo1
snmp-server trap-source Lo2
ip tftp source-interface Lo3
monitor session 1 source interface Gi0/3 rx
standby 1 track Gi0/5
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	iface := refNames(refs, symbols.KindInterface)
	want := []string{"Gi0/1", "Gi0/2", "Te1/1", "Gi0/0", "Gi0/1", "Te0/1", "Lo0", "Lo1", "Lo2", "Lo3", "Gi0/3", "Gi0/5"}
	if len(iface) != len(want) {
		t.Fatalf("interface refs = %v, want %v", iface, want)
	}
	for i := range want {
		if iface[i] != want[i] {
			t.Fatalf("interface refs[%d] = %q, want %q (all: %v)", i, iface[i], want[i], iface)
		}
	}
}

// IfaceShape filter: next-hop IPs, the dhcp form, and Null0 must NOT be
// extracted as interface references; route-map keywords (name/track
// arguments) must not leak either.
func TestInterfaceShapeFilter(t *testing.T) {
	src := `ip route 20.0.0.0 255.0.0.0 1.1.1.1
ip route 40.0.0.0 255.0.0.0 dhcp
ip route 60.0.0.0 255.0.0.0 Null0
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	if got := refNames(refs, symbols.KindInterface); len(got) != 0 {
		t.Fatalf("interface refs = %v, want none (IP / dhcp / Null0 are not interfaces)", got)
	}
}
