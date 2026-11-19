package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// ACL reference completion (chunt-otg.13): ipv6 traffic-filter,
// distribute-list (router + neighbor forms), snmp-server community, NAT
// bindings, and varargs match ip address.

func TestExtractACLReferenceCompletion(t *testing.T) {
	src := `interface Vlan10
 ipv6 traffic-filter V6-OUT out
!
router ospf 1
 distribute-list 10 in
 distribute-list OSPF-ACL out
 neighbor 1.1.1.1 distribute-list 20 in
 distribute-list prefix-list PL out
!
snmp-server community public RO 40
snmp-server community private RW 55
snmp-server community view-only view MYVIEW RO
!
ip nat inside source list NAT-ACL interface Gi0/0 overload
ip nat outside source list 77 static 1.2.3.4
ip nat inside destination list 78 pool MYPOOL
!
route-map RM permit 10
 match ip address ACL1 ACL2
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	acl := refNames(refs, symbols.KindACL)
	want := []string{"10", "OSPF-ACL", "20", "40", "55", "NAT-ACL", "77", "78", "ACL1", "ACL2"}
	if len(acl) != len(want) {
		t.Fatalf("acl refs = %v, want %v", acl, want)
	}
	for i := range want {
		if acl[i] != want[i] {
			t.Fatalf("acl refs[%d] = %q, want %q (all: %v)", i, acl[i], want[i], acl)
		}
	}
	if got := refNames(refs, symbols.KindACLv6); len(got) != 1 || got[0] != "V6-OUT" {
		t.Fatalf("ipv6-acl refs = %v, want [V6-OUT]", got)
	}
	// The prefix-list distribute-list form keeps citing prefix-lists, and
	// the snmp line with a trailing RO (no ACL) cites nothing extra.
	if got := refNames(refs, symbols.KindPrefixList); len(got) != 1 || got[0] != "PL" {
		t.Fatalf("prefix-list refs = %v, want [PL]", got)
	}
	for _, n := range acl {
		if n == "RO" || n == "RW" || n == "list" {
			t.Fatalf("keyword leaked as ACL reference: %v", acl)
		}
	}
}
