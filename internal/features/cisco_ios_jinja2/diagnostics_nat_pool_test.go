package cisco_ios_jinja2_test

import (
	"testing"
)

// NAT pool definition and reference coverage (chunt-otg.18).

func TestNATPoolUndefinedReferences(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "inside source pool flags when undefined",
			src:  "ip nat inside source list ACL1 pool POOL1 overload\n!\n",
		},
		{
			name: "outside destination pool flags when undefined",
			src:  "ip nat outside destination list ACL1 pool POOL2\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, d := range openDiags(t, tc.src) {
				if d.Code == "undefined-nat-pool" {
					found = true
				}
			}
			if !found {
				t.Fatalf("no undefined-nat-pool in %v", openDiags(t, tc.src))
			}
		})
	}
}

func TestNATPoolResolveAndDefine(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "pool resolves against its definition",
			src:  "ip access-list extended ACL1\n permit ip 10.0.0.0 255.0.0.0 any\n!\nip nat pool POOL1 10.0.0.10 10.0.0.20 netmask 255.255.255.0\n!\nip nat inside source list ACL1 pool POOL1 overload\n!\n",
		},
		{
			name: "pool definition alone emits nothing",
			src:  "ip nat pool POOL1 10.0.0.10 10.0.0.20 netmask 255.255.255.0\n!\n",
		},
		{
			name: "prefix-length pool definition",
			src:  "ip nat pool POOL2 10.1.0.10 10.1.0.20 prefix-length\n!\n",
		},
		{
			name: "source list + interface overload form",
			src:  "interface Gi0/0\n!\nip access-list extended ACL1\n permit ip 10.0.0.0 255.0.0.0 any\n!\nip nat inside source list ACL1 interface Gi0/0 overload\n!\n",
		},
		{
			name: "static nat vrf form still resolves",
			src:  "vrf definition BLUE\n!\nip nat inside source static 10.1.1.1 200.1.1.1 vrf BLUE\n!\n",
		},
		{
			name: "negated nat line emits nothing",
			src:  "no ip nat inside source list ACL1 pool POOL1 overload\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				switch d.Code {
				case "undefined-nat-pool", "undefined-acl", "undefined-interface", "undefined-vrf":
					t.Fatalf("unexpected %s: %q", d.Code, d.Message)
				}
			}
		})
	}
}

// Pools are unique by name: a repeated definition flags through the shared
// duplicate-definition pass.
func TestDuplicateNATPoolDefinition(t *testing.T) {
	src := "ip nat pool POOL1 10.0.0.10 10.0.0.20 netmask 255.255.255.0\n!\nip nat pool POOL1 10.2.0.10 10.2.0.20 netmask 255.255.255.0\n!\n"
	found := false
	for _, d := range openDiags(t, src) {
		if d.Code == "duplicate-nat-pool" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no duplicate-nat-pool in %v", openDiags(t, src))
	}
}
