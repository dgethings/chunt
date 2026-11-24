package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// FHRP per-group scalar redefined-setting coverage (chunt-otg.2).

func TestFHRPScalarRedefinedSetting(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantSubs []string
	}{
		{
			name:     "standby priority conflict within one group",
			src:      "interface Gi0/0\n standby 1 priority 110\n standby 1 priority 120\n!\n",
			wantSubs: []string{`standby priority "120" replaces "110"`},
		},
		{
			name:     "standby virtual IP conflict within one group",
			src:      "interface Gi0/0\n standby 1 ip 10.0.0.254\n standby 1 ip 10.0.0.250\n!\n",
			wantSubs: []string{`standby ip "10.0.0.250" replaces "10.0.0.254"`},
		},
		{
			name:     "standby preempt delay conflict",
			src:      "interface Gi0/0\n standby 1 preempt delay 60\n standby 1 preempt delay 30\n!\n",
			wantSubs: []string{`standby preempt`},
		},
		{
			name:     "standby authentication conflict",
			src:      "interface Gi0/0\n standby 1 authentication cisco\n standby 1 authentication other\n!\n",
			wantSubs: []string{`standby authentication`},
		},
		{
			name:     "vrrp ip conflict",
			src:      "interface Gi0/0\n vrrp 2 ip 10.1.0.254\n vrrp 2 ip 10.1.0.250\n!\n",
			wantSubs: []string{`vrrp ip`},
		},
		{
			name:     "glbp priority conflict",
			src:      "interface Gi0/0\n glbp 3 priority 100\n glbp 3 priority 150\n!\n",
			wantSubs: []string{`glbp priority`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, sub := range tc.wantSubs {
				found := false
				for _, d := range openDiags(t, tc.src) {
					if d.Code == "redefined-setting" && strings.Contains(d.Message, sub) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("no redefined-setting containing %q in %v", sub, openDiags(t, tc.src))
				}
			}
		})
	}
}

func TestFHRPScalarExemptions(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "distinct groups never interact",
			src:  "interface Gi0/0\n standby 1 priority 110\n standby 2 priority 120\n standby 1 ip 10.0.0.254\n standby 2 ip 10.1.0.254\n!\n",
		},
		{
			name: "vrrp and standby never interact",
			src:  "interface Gi0/0\n standby 1 priority 110\n vrrp 1 priority 120\n standby 1 ip 10.0.0.254\n vrrp 1 ip 10.1.0.254\n!\n",
		},
		{
			name: "plain preempt carries no value and is ignored",
			src:  "interface Gi0/0\n standby 1 preempt\n standby 1 preempt\n!\n",
		},
		{
			name: "negation resets the group tracker",
			src:  "interface Gi0/0\n standby 1 ip 10.0.0.254\n no standby 1 ip 10.0.0.254\n standby 1 ip 10.0.0.250\n!\n",
		},
		{
			name: "same group on different interfaces is fine",
			src:  "interface Gi0/0\n standby 1 priority 110\n!\ninterface Gi0/1\n standby 1 priority 120\n!\n",
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
