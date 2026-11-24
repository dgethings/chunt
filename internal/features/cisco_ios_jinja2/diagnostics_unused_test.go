package cisco_ios_jinja2_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
)

// Unused-definition coverage (chunt-otg.23): Hint severity, once per
// (kind, name), conservative Jinja handling, endpoint kinds excluded.

func unusedByCode(diags []protocol.Diagnostic, code string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

func TestUnusedDefinitions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		want int
	}{
		{
			name: "unreferenced ACL flags once",
			src:  "ip access-list extended ORPHAN\n permit ip any any\n!\n",
			code: "unused-acl",
			want: 1,
		},
		{
			name: "referenced ACL does not flag",
			src:  "ip access-list extended USED\n permit ip any any\n!\ninterface Gi0/0\n ip access-group USED in\n!\n",
			code: "unused-acl",
			want: 0,
		},
		{
			name: "unreferenced route-map flags",
			src:  "route-map ORPHAN-RM permit 10\n match ip address next-hop 1.1.1.1\n!\n",
			code: "unused-route-map",
			want: 1,
		},
		{
			name: "prefix-list entries report once per object",
			src:  "ip prefix-list PL-ORPHAN seq 10 permit 10.0.0.0/8\nip prefix-list PL-ORPHAN seq 20 permit 20.0.0.0/8\n!\n",
			code: "unused-prefix-list",
			want: 1,
		},
		{
			name: "unreferenced zone flags",
			src:  "zone security DEADZONE\n!\n",
			code: "unused-zone",
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unusedByCode(openDiags(t, tc.src), tc.code)
			if got != tc.want {
				t.Fatalf("got %d %s diagnostics, want %d: %v", got, tc.code, tc.want, openDiags(t, tc.src))
			}
		})
	}
}

func TestUnusedExemptions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "guarded definition never flags",
			src:  "{% if branch %}\nip access-list extended COND\n permit ip any any\n!\n{% endif %}\n",
			code: "unused-acl",
		},
		{
			name: "computed reference counts as a use (conservative)",
			src:  "ip access-list extended LIST1\n permit ip any any\n!\ninterface Gi0/0\n ip access-group {{ acl_name }} in\n!\n",
			code: "unused-acl",
		},
		{
			name: "interfaces are endpoint kinds and never flag",
			src:  "interface Gi0/0\n description just an interface\n!\ninterface Gi0/1\n!\n",
			code: "unused-interface",
		},
		{
			name: "dhcp pools / sla sessions are excluded (external uses)",
			src:  "ip dhcp pool POOL1\n network 10.0.0.0 255.255.255.0\n!\nip sla 1\n icmp-echo 8.8.8.8\n!\n",
			code: "unused-dhcp-pool",
		},
		{
			name: "built-in names are irrelevant (class-default is never defined)",
			src:  "policy-map P1\n class class-default\n!\n",
			code: "unused-class-map",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				if d.Code == tc.code {
					t.Fatalf("unexpected %s: %q", d.Code, d.Message)
				}
			}
		})
	}
}

// The diagnostic is a Hint anchored on the name token.
func TestUnusedSeverityIsHint(t *testing.T) {
	diags := openDiags(t, "ip access-list extended ORPHAN\n permit ip any any\n!\n")
	for _, d := range diags {
		if d.Code == "unused-acl" {
			if d.Severity != protocol.SeverityHint {
				t.Fatalf("unused-acl severity %d, want Hint (4)", d.Severity)
			}
			return
		}
	}
	t.Fatalf("no unused-acl diagnostic: %v", diags)
}
