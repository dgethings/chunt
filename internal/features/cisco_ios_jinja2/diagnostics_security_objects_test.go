package cisco_ios_jinja2_test

import (
	"testing"
)

// Security object duplicate-definition coverage (chunt-otg.8).

func TestDuplicateSecurityDefinitions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "crypto map repeated SEQ is the conflict",
			src:  "crypto map CM1 10 ipsec-isakmp\n!\ncrypto map CM1 10 ipsec-manual\n!\n",
			code: "duplicate-crypto-map",
		},
		{
			name: "crypto map distinct SEQs are additive",
			src:  "", // handled by the non-duplicates test below
			code: "",
		},
		{
			name: "transform-set repeated",
			src:  "crypto ipsec transform-set TS1 esp-aes esp-sha-hmac\n!\ncrypto ipsec transform-set TS1 esp-3des esp-sha-hmac\n!\n",
			code: "duplicate-transform-set",
		},
		{
			name: "isakmp policy repeated",
			src:  "crypto isakmp policy 10\n encryption aes\n!\ncrypto isakmp policy 10\n encryption 3des\n!\n",
			code: "duplicate-isakmp-policy",
		},
		{
			name: "isakmp profile repeated",
			src:  "crypto isakmp profile PROF1\n!\ncrypto isakmp profile PROF1\n!\n",
			code: "duplicate-isakmp-profile",
		},
		{
			name: "ipsec profile repeated",
			src:  "crypto ipsec profile IPP1\n!\ncrypto ipsec profile IPP1\n!\n",
			code: "duplicate-ipsec-profile",
		},
		{
			name: "zone repeated",
			src:  "zone security INSIDE\n!\nzone security INSIDE\n!\n",
			code: "duplicate-zone",
		},
		{
			name: "parameter-map repeated",
			src:  "parameter-map type inspect PM1\n!\nparameter-map type inspect PM1\n!\n",
			code: "duplicate-parameter-map",
		},
	}
	for _, tc := range cases {
		if tc.src == "" {
			continue // additive case exercised in TestSecurityNonDuplicates
		}
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, d := range openDiags(t, tc.src) {
				if d.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("no %s diagnostic in %v", tc.code, openDiags(t, tc.src))
			}
		})
	}
}

func TestSecurityNonDuplicates(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "crypto map distinct SEQs coexist",
			src:  "crypto map CM1 10 ipsec-isakmp\n!\ncrypto map CM1 20 ipsec-isakmp\n!\n",
		},
		{
			name: "distinct crypto maps coexist",
			src:  "crypto map CM1 10 ipsec-isakmp\n!\ncrypto map CM2 10 ipsec-isakmp\n!\n",
		},
		{
			name: "distinct isakmp policies coexist",
			src:  "crypto isakmp policy 10\n!\ncrypto isakmp policy 20\n!\n",
		},
		{
			name: "distinct zones coexist",
			src:  "zone security INSIDE\n!\nzone security OUTSIDE\n!\n",
		},
		{
			name: "nameless singleton parameter-map does not flag",
			src:  "parameter-map type inspect-global\n!\n",
		},
		{
			name: "negation clears",
			src:  "zone security INSIDE\n!\nno zone security INSIDE\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				switch d.Code {
				case "duplicate-crypto-map", "duplicate-transform-set", "duplicate-isakmp-policy",
					"duplicate-isakmp-profile", "duplicate-ipsec-profile", "duplicate-zone",
					"duplicate-parameter-map":
					t.Fatalf("unexpected duplicate diagnostic: %q: %s", d.Code, d.Message)
				}
			}
		})
	}
}
