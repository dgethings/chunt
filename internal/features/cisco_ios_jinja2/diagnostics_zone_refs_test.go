package cisco_ios_jinja2_test

import (
	"testing"
)

// Zone-based firewall undefined-reference coverage (chunt-otg.15).

func TestZoneUndefinedReferences(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "zone-member flags undefined zone",
			src:  "interface Gi0/0\n zone-member security INSIDE\n!\n",
			want: "INSIDE",
		},
		{
			name: "class type inspect flags undefined class-map",
			src:  "policy-map type inspect PM1\n class type inspect CM1\n  inspect\n !\n!\n",
			want: "CM1",
		},
		{
			name: "service-policy type inspect flags undefined policy-map",
			src:  "zone-pair security ZP1 source INSIDE destination OUTSIDE\n service-policy type inspect PM9\n!\n",
			want: "PM9",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, d := range openDiags(t, tc.src) {
				if contains(d.Message, `undefined class-map "type"`) {
					t.Fatalf("literal 'type' misread as a class-map name (shadowing regression): %q", d.Message)
				}
				if d.Code == "undefined-zone" && contains(d.Message, tc.want) {
					found = true
				}
				if tc.want != "INSIDE" && d.Code == "undefined-class-map" && contains(d.Message, tc.want) {
					found = true
				}
				if tc.want != "INSIDE" && d.Code == "undefined-policy-map" && contains(d.Message, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no undefined diagnostic containing %q in %v", tc.want, openDiags(t, tc.src))
			}
		})
	}
}

func TestZoneReferencesResolve(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "zone-member resolves against zone security",
			src:  "zone security INSIDE\n!\ninterface Gi0/0\n zone-member security INSIDE\n!\n",
		},
		{
			name: "negated zone-member emits nothing",
			src:  "interface Gi0/0\n no zone-member security INSIDE\n!\n",
		},
		{
			name: "class type inspect resolves against typed class-map",
			src:  "class-map type inspect match-any CM1\n match protocol tcp\n!\npolicy-map type inspect PM1\n class type inspect CM1\n  inspect\n !\n!\n",
		},
		{
			name: "service-policy type inspect resolves against typed policy-map",
			src:  "policy-map type inspect PM1\n class type inspect-self\n  pass\n !\n!\nzone-pair security ZP1 source INSIDE destination OUTSIDE\n service-policy type inspect PM1\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				switch d.Code {
				case "undefined-zone", "undefined-class-map", "undefined-policy-map":
					t.Fatalf("unexpected %s: %q", d.Code, d.Message)
				}
			}
		})
	}
}
