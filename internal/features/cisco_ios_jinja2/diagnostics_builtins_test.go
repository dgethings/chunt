package cisco_ios_jinja2_test

import (
	"testing"
)

// Built-in / well-known name suppression coverage (chunt-otg.22): one test
// per registry entry.

func TestBuiltInNameSuppressions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string // the undefined-<kind> code that must NOT fire
	}{
		{
			name: "class-default in a policy-map body",
			src:  "policy-map P1\n class class-default\n  fair-queue\n !\n!\n",
			code: "undefined-class-map",
		},
		{
			name: "implicit global vrf",
			src:  "interface Gi0/0\n vrf forwarding global\n!\n",
			code: "undefined-vrf",
		},
		{
			name: "Cat9k management vrf (Mgmt-vrf)",
			src:  "interface Gi0/0\n vrf forwarding Mgmt-vrf\n!\n",
			code: "undefined-vrf",
		},
		{
			name: "management vrf lowercase",
			src:  "interface Gi0/0\n vrf forwarding mgmt-vrf\n!\n",
			code: "undefined-vrf",
		},
		{
			name: "community-list internet keyword",
			src:  "route-map R1 permit 10\n match community internet\n!\n",
			code: "undefined-community-list",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				if d.Code == tc.code {
					t.Fatalf("built-in name was flagged: %q", d.Message)
				}
			}
		})
	}
}

// Control: a genuinely undefined community-list name still flags — the
// internet suppression must not swallow arbitrary names. NUMBERED lists
// flag too (chunt-otg.12's established, tested behavior — a typo'd number
// is a real catch), so the issue's "standard numbers" bullet was
// deliberately not implemented (see builtInNames).
func TestBuiltInSuppressionsStayScoped(t *testing.T) {
	for _, name := range []string{"NOSUCHLIST", "10", "150", "200"} {
		src := "route-map R1 permit 10\n match community " + name + "\n!\n"
		found := false
		for _, d := range openDiags(t, src) {
			if d.Code == "undefined-community-list" {
				found = true
			}
		}
		if !found {
			t.Fatalf("undefined community-list %q no longer flagged: %v", name, openDiags(t, src))
		}
	}
}
