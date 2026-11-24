package cisco_ios_jinja2_test

import (
	"testing"
)

// Crypto/IPsec undefined-reference coverage (chunt-otg.19).

func TestCryptoUndefinedReferences(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "interface crypto map apply flags undefined map",
			src:  "interface Gi0/0\n crypto map CM1\n!\n",
			code: "undefined-crypto-map",
		},
		{
			name: "set transform-set flags undefined",
			src:  "crypto map CM1 10 ipsec-isakmp\n set transform-set TS1 TS2\n!\n",
			code: "undefined-transform-set",
		},
		{
			name: "set isakmp-profile flags undefined",
			src:  "crypto map CM1 10 ipsec-isakmp\n set isakmp-profile PROF1\n!\n",
			code: "undefined-isakmp-profile",
		},
		{
			name: "tunnel protection flags undefined ipsec profile",
			src:  "interface Tunnel0\n tunnel protection ipsec profile IPP1 shared\n!\n",
			code: "undefined-ipsec-profile",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, d := range openDiags(t, tc.src) {
				if d.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("no %s in %v", tc.code, openDiags(t, tc.src))
			}
		})
	}
}

func TestCryptoReferencesResolve(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "crypto map apply resolves against definition",
			src:  "crypto map CM1 10 ipsec-isakmp\n match address 101\n!\ninterface Gi0/0\n crypto map CM1\n!\n",
		},
		{
			name: "definition lines never flag (self-resolving + numeric seq is not a name)",
			src:  "crypto map CM1 10 ipsec-isakmp\n!\ncrypto map CM1 20 ipsec-isakmp\n!\n",
		},
		{
			name: "set transform-set varargs resolve",
			src:  "crypto ipsec transform-set TS1 esp-aes esp-sha-hmac\n!\ncrypto ipsec transform-set TS2 esp-3des esp-sha-hmac\n!\ncrypto map CM1 10 ipsec-isakmp\n set transform-set TS1 TS2\n!\n",
		},
		{
			name: "set isakmp-profile resolves",
			src:  "crypto isakmp profile PROF1\n!\ncrypto map CM1 10 ipsec-isakmp\n set isakmp-profile PROF1\n!\n",
		},
		{
			name: "tunnel protection resolves",
			src:  "crypto ipsec profile IPP1\n set transform-set TS1\n!\ncrypto ipsec transform-set TS1 esp-aes esp-sha-hmac\n!\ninterface Tunnel0\n tunnel protection ipsec profile IPP1 shared\n!\n",
		},
		{
			name: "negated apply emits nothing",
			src:  "interface Gi0/0\n no crypto map CM1\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				switch d.Code {
				case "undefined-crypto-map", "undefined-transform-set",
					"undefined-isakmp-profile", "undefined-ipsec-profile":
					t.Fatalf("unexpected %s: %q", d.Code, d.Message)
				}
			}
		})
	}
}
