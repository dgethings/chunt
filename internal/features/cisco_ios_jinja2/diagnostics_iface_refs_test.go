package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Interface-name undefined-reference diagnostics (chunt-otg.16).

func TestUndefinedInterfaceReferences(t *testing.T) {
	src := `ip route 10.0.0.0 255.0.0.0 GigabitEhternet0/1
logging source-interface MISSING-LO
monitor session 1 source interface Gi9/9 both
!
interface Gi0/1
!
interface Loopback0
!
`
	var names []string
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-interface" {
			for _, want := range []string{"GigabitEhternet0/1", "MISSING-LO", "Gi9/9"} {
				if strings.Contains(d.Message, want) {
					names = append(names, want)
				}
			}
		}
	}
	if len(names) != 3 {
		t.Fatalf("flagged %v, want the three typo'd interfaces", names)
	}
}

// Defined interfaces and exact sub-interface names satisfy references;
// next-hop IPs, dhcp, and Null0 route targets never produce
// undefined-interface noise.
func TestDefinedInterfacesSatisfyReferences(t *testing.T) {
	src := `ip route 10.0.0.0 255.0.0.0 Gi0/1
ip route 20.0.0.0 255.0.0.0 1.1.1.1
ip route 30.0.0.0 255.0.0.0 dhcp
ip route 40.0.0.0 255.0.0.0 Null0
ip route 50.0.0.0 255.0.0.0 Gi0/1.100
!
interface Gi0/1
!
interface Gi0/1.100
!
interface Loopback0
!
logging source-interface Loopback0
ntp source Loopback0
monitor session 1 destination interface Gi0/1
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-interface" {
			t.Fatalf("defined interface still flagged: %q", d.Message)
		}
	}
}
