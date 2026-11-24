package cisco_ios_jinja2_test

import (
	"testing"
)

// VRF undefined-reference coverage (chunt-otg.17).

func TestVRFUndefinedReferences(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		undef string // expected undefined-vrf name; "" = must not flag
	}{
		{
			name:  "interface vrf forwarding flags when undefined",
			src:   "interface Gi0/0\n vrf forwarding MGMT\n!\n",
			undef: "MGMT",
		},
		{
			name:  "legacy ip vrf forwarding flags when undefined",
			src:   "interface Gi0/0\n ip vrf forwarding PROD\n!\n",
			undef: "PROD",
		},
		{
			name:  "ip route vrf flags the vrf name",
			src:   "ip route vrf BLUE 10.0.0.0 255.0.0.0 192.168.1.1\n!\n",
			undef: "BLUE",
		},
		{
			name:  "nat static vrf flags",
			src:   "ip nat inside source static 10.1.1.1 200.1.1.1 vrf BLUE\n!\n",
			undef: "BLUE",
		},
		{
			name:  "nat list+pool+vrf line flags both kinds",
			src:   "ip nat inside source list ACL1 pool POOL1 vrf BLUE overload\n!\n",
			undef: "BLUE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			found := false
			for _, d := range diags {
				if d.Code == "undefined-vrf" && contains(d.Message, tc.undef) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no undefined-vrf containing %q in %v", tc.undef, diags)
			}
		})
	}
}

func TestVRFReferencesResolve(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "vrf forwarding resolves against vrf definition",
			src:  "vrf definition MGMT\n!\ninterface Gi0/0\n vrf forwarding MGMT\n!\n",
		},
		{
			name: "vrf forwarding resolves against legacy ip vrf",
			src:  "ip vrf PROD\n!\ninterface Gi0/0\n ip vrf forwarding PROD\n!\n",
		},
		{
			name: "ip route vrf resolves",
			src:  "vrf definition BLUE\n!\nip route vrf BLUE 10.0.0.0 255.0.0.0 192.168.1.1\n!\n",
		},
		{
			name: "ip route vrf still emits the interface ref",
			src:  "vrf definition BLUE\n!\ninterface Gi0/0\n!\nip route vrf BLUE 10.0.0.0 255.0.0.0 Gi0/0\n!\n",
		},
		{
			name: "nat list + vrf both resolve",
			src:  "vrf definition BLUE\n!\nip access-list extended ACL1\n permit ip any any\n!\nip nat inside source list ACL1 pool POOL1 vrf BLUE overload\n!\n",
		},
		{
			name: "nat static vrf resolves",
			src:  "vrf definition BLUE\n!\nip nat inside source static 10.1.1.1 200.1.1.1 vrf BLUE\n!\n",
		},
		{
			name: "built-in global vrf suppressed",
			src:  "interface Gi0/0\n vrf forwarding global\n!\n",
		},
		{
			name: "built-in mgmt vrf suppressed",
			src:  "interface Gi0/0\n vrf forwarding Mgmt-vrf\n!\n",
		},
		{
			name: "negated reference emits nothing",
			src:  "interface Gi0/0\n no vrf forwarding MGMT\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				if d.Code == "undefined-vrf" {
					t.Fatalf("unexpected undefined-vrf: %q", d.Message)
				}
			}
		})
	}
}

// A vrf reference must not resolve against same-named objects of other
// kinds (family isolation is inherent to the kind-keyed symbol table): with
// only an ACL named MGMT defined, the vrf reference still flags.
func TestVRFFamilyIsolation(t *testing.T) {
	src := "ip access-list extended MGMT\n permit ip any any\n!\ninterface Gi0/0\n vrf forwarding MGMT\n!\n"
	found := false
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-vrf" && contains(d.Message, "MGMT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("vrf reference wrongly resolved against the same-named ACL; expected undefined-vrf")
	}
}
