package cisco_ios_jinja2_test

import (
	"testing"
)

// Subnet duplication/overlap coverage (chunt-otg.5).

func TestSubnetDiagnostics(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		code    string
		wantSev int
	}{
		{
			name: "same subnet on two interfaces is an Error",
			src: `interface Gi0/0
 ip address 10.1.1.1 255.255.255.0
!
interface Gi0/1
 ip address 10.1.1.42 255.255.255.0
!
`,
			code:    "duplicate-subnet",
			wantSev: 1,
		},
		{
			name: "contained subnet is a Warning",
			src: `interface Gi0/0
 ip address 10.1.1.1 255.255.255.0
!
interface Gi0/1
 ip address 10.1.1.129 255.255.255.128
!
`,
			code:    "overlapping-subnet",
			wantSev: 2,
		},
		{
			name: "same virtual IP on two interfaces is a Warning",
			src: `interface Gi0/0
 standby 1 ip 10.1.1.254
!
interface Gi0/1
 standby 1 ip 10.1.1.254
!
`,
			code:    "duplicate-virtual-ip",
			wantSev: 2,
		},
		{
			name: "same virtual IP twice on one interface different groups",
			src: `interface Gi0/0
 standby 1 ip 10.1.1.254
 standby 2 ip 10.1.1.254
!
`,
			code:    "duplicate-virtual-ip",
			wantSev: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, d := range openDiags(t, tc.src) {
				if d.Code == tc.code {
					found = true
					if d.Severity != tc.wantSev {
						t.Fatalf("%s severity %d, want %d: %q", tc.code, d.Severity, tc.wantSev, d.Message)
					}
				}
			}
			if !found {
				t.Fatalf("no %s diagnostic in %v", tc.code, openDiags(t, tc.src))
			}
		})
	}
}

func TestSubnetExemptions(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "distinct subnets on two interfaces are fine",
			src: `interface Gi0/0
 ip address 10.1.1.1 255.255.255.0
!
interface Gi0/1
 ip address 10.1.2.1 255.255.255.0
!
`,
		},
		{
			name: "secondary addresses are exempt",
			src: `interface Gi0/0
 ip address 10.1.1.1 255.255.255.0
 ip address 10.1.2.1 255.255.255.0 secondary
!
interface Gi0/1
 ip address 10.1.2.9 255.255.255.0
!
`,
		},
		{
			name: "same subnet in different VRFs is legitimate",
			src: `interface Gi0/0
 vrf forwarding BLUE
 ip address 10.1.1.1 255.255.255.0
!
interface Gi0/1
 vrf forwarding RED
 ip address 10.1.1.2 255.255.255.0
!
`,
		},
		{
			name: "global table vs named VRF is legitimate",
			src: `interface Gi0/0
 ip address 10.1.1.1 255.255.255.0
!
interface Vlan10
 vrf forwarding MGMT
 ip address 10.1.1.2 255.255.255.0
!
`,
		},
		{
			name: "dhcp and unnumbered are skipped",
			src: `interface Gi0/0
 ip address dhcp
!
interface Gi0/1
 ip unnumbered Loopback0
!
`,
		},
		{
			name: "jinja-computed addresses are skipped",
			src: `interface Gi0/0
 ip address {{ mgmt_ip }} 255.255.255.0
!
interface Gi0/1
 ip address {{ mgmt_ip }} 255.255.255.0
!
`,
		},
		{
			name: "negated address does not conflict",
			src: `interface Gi0/0
 ip address 10.1.1.1 255.255.255.0
!
interface Gi0/1
 no ip address 10.1.1.2 255.255.255.0
 ip address 10.1.2.1 255.255.255.0
!
`,
		},
		{
			name: "distinct FHRP virtual IPs are fine",
			src: `interface Gi0/0
 standby 1 ip 10.1.1.254
!
interface Gi0/1
 standby 1 ip 10.2.1.254
!
`,
		},
		{
			name: "same group same VIP on one interface only once is fine",
			src: `interface Gi0/0
 standby 1 ip 10.1.1.254
!
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				switch d.Code {
				case "duplicate-subnet", "overlapping-subnet", "duplicate-virtual-ip":
					t.Fatalf("unexpected %s: %q", d.Code, d.Message)
				}
			}
		})
	}
}
