package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// ACL reference completion diagnostics (chunt-otg.13).

func TestUndefinedACLCompletionForms(t *testing.T) {
	src := `interface Vlan10
 ipv6 traffic-filter MISSING-V6 out
!
router ospf 1
 distribute-list GONE-DL in
 neighbor 1.1.1.1 distribute-list GONE-NEIGH in
!
snmp-server community public RO MISSING-SNMP
ip nat inside source list MISSING-NAT interface Gi0/0 overload
!
`
	var names []string
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-acl" || d.Code == "undefined-ipv6-acl" {
			for _, want := range []string{"MISSING-V6", "GONE-DL", "GONE-NEIGH", "MISSING-SNMP", "MISSING-NAT"} {
				if strings.Contains(d.Message, want) {
					names = append(names, want)
				}
			}
		}
	}
	if len(names) != 5 {
		t.Fatalf("flagged %v, want all five undefined ACLs", names)
	}
}

// Family isolation, end to end: a v4 ACL of the same name must NOT satisfy
// an ipv6 traffic-filter reference (and vice versa is covered by the
// .9 fixture).
func TestIPv6TrafficFilterNeedsV6Family(t *testing.T) {
	src := `ip access-list extended BOTH-NAMES
 permit ip any any
!
interface Vlan10
 ipv6 traffic-filter BOTH-NAMES out
!
`
	diag, ok := findDiagByCode(openDiags(t, src), "undefined-ipv6-acl")
	if !ok {
		t.Fatalf("v6 reference was satisfied by a v4 definition")
	}
	if !strings.Contains(diag.Message, "BOTH-NAMES") {
		t.Fatalf("flagged wrong name: %q", diag.Message)
	}
}

// Defined ACLs satisfy the new forms: v6 traffic-filter resolves only
// against a v6 definition, snmp/NAT/distribute-list against numbered or
// named v4 definitions.
func TestDefinedACLSatisfiesCompletionForms(t *testing.T) {
	src := `ipv6 access-list V6-OK
 permit ipv6 any any
!
access-list 40 permit 10.0.0.0 255.0.0.0
ip access-list standard SNMP-OK
 permit 10.1.1.1
!
ip prefix-list PL-OK seq 10 permit 10.0.0.0/8
!
interface Vlan10
 ipv6 traffic-filter V6-OK out
!
router ospf 1
 distribute-list 40 in
 neighbor 1.1.1.1 distribute-list SNMP-OK in
 distribute-list prefix-list PL-OK out
!
snmp-server community public RO 40
!
ip nat inside source list SNMP-ACL-FORWARD interface Gi0/0 overload
ip access-list extended SNMP-ACL-FORWARD
 permit ip 10.0.0.0 255.0.0.0 any
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-acl" || d.Code == "undefined-ipv6-acl" || d.Code == "undefined-prefix-list" {
			t.Fatalf("defined object still flagged: %s %q", d.Code, d.Message)
		}
	}
}
