package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Interface-scoped redefined-setting coverage (chunt-otg.1).

func TestInterfaceScalarRedefinedSetting(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantSubs []string // setting label + both values
	}{
		{
			name:     "description replacement",
			src:      "interface Gi0/0\n description core uplink\n description backup uplink\n!\n",
			wantSubs: []string{`description "backup uplink" replaces "core uplink"`},
		},
		{
			name:     "mtu replacement",
			src:      "interface Gi0/0\n mtu 1500\n mtu 9000\n!\n",
			wantSubs: []string{`mtu "9000" replaces "1500"`},
		},
		{
			name:     "ip mtu and mtu are independent scalars",
			src:      "interface Gi0/0\n mtu 9000\n ip mtu 1500\n!\n",
			wantSubs: nil,
		},
		{
			name:     "speed replacement",
			src:      "interface Gi0/0\n speed 1000\n speed 100\n!\n",
			wantSubs: []string{`speed "100" replaces "1000"`},
		},
		{
			name:     "duplex replacement",
			src:      "interface Gi0/0\n duplex full\n duplex half\n!\n",
			wantSubs: []string{`duplex "half" replaces "full"`},
		},
		{
			name:     "encapsulation replacement across keyword forms",
			src:      "interface Gi0/0.10\n encapsulation dot1q 10\n encapsulation isl 10\n!\n",
			wantSubs: []string{`encapsulation "isl 10" replaces "dot1q 10"`},
		},
		{
			name:     "switchport mode replacement",
			src:      "interface Gi0/0\n switchport mode access\n switchport mode trunk\n!\n",
			wantSubs: []string{`switchport mode "trunk" replaces "access"`},
		},
		{
			name:     "vrf forwarding replacement across forms",
			src:      "interface Gi0/0\n vrf forwarding MGMT\n ip vrf forwarding PROD\n!\n",
			wantSubs: []string{`vrf forwarding "PROD" replaces "MGMT"`},
		},
		{
			name:     "ip address dhcp conflicts with static primary",
			src:      "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n ip address dhcp\n!\n",
			wantSubs: []string{"ip address"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			if tc.wantSubs == nil {
				for _, d := range diags {
					if d.Code == "redefined-setting" {
						t.Fatalf("unexpected redefined-setting: %q", d.Message)
					}
				}
				return
			}
			for _, sub := range tc.wantSubs {
				found := false
				for _, d := range diags {
					if d.Code == "redefined-setting" && strings.Contains(d.Message, sub) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("no redefined-setting containing %q in %v", sub, diags)
				}
			}
		})
	}
}

// Additive and reset patterns must not flag: secondary addresses, negation
// resets, and identical repeats (which stay duplicate-command's business).
func TestInterfaceScalarExemptions(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "secondary addresses are additive",
			src:  "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n ip address 10.0.1.1 255.255.255.0 secondary\n!\n",
		},
		{
			name: "negation resets the tracker",
			src:  "interface Gi0/0\n description old\n no description\n description new\n!\n",
		},
		{
			name: "negation resets vrf forwarding (both forms)",
			src:  "interface Gi0/0\n vrf forwarding MGMT\n no ip vrf forwarding\n vrf forwarding PROD\n!\n",
		},
		{
			name: "identical description repeat stays duplicate-command",
			src:  "interface Gi0/0\n description same text\n description same text\n!\n",
		},
		{
			name: "same setting in different interfaces is fine",
			src:  "interface Gi0/0\n description one\n!\ninterface Gi0/1\n description two\n!\n",
		},
		{
			name: "speed and duplex are independent",
			src:  "interface Gi0/0\n speed 100\n duplex full\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				if d.Code == "redefined-setting" {
					t.Fatalf("unexpected redefined-setting: %q", d.Message)
				}
			}
		})
	}
}
