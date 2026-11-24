package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Global scalar redefined-setting coverage (chunt-otg.3).

func TestGlobalScalarRedefinedSetting(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantSubs []string
	}{
		{
			name:     "banner motd replacement",
			src:      "banner motd ^Welcome to R9^\n!\nbanner motd ^Authorized access only^\n!\n",
			wantSubs: []string{`banner motd`, "Authorized access only", "Welcome to R9"},
		},
		{
			name:     "clock timezone replacement",
			src:      "clock timezone PST -8 0\n!\nclock timezone EST -5 0\n!\n",
			wantSubs: []string{`clock timezone "EST -5 0" replaces "PST -8 0"`},
		},
		{
			name:     "ip domain-name replacement",
			src:      "ip domain-name old.example.com\n!\nip domain-name new.example.com\n!\n",
			wantSubs: []string{`ip domain-name "new.example.com" replaces "old.example.com"`},
		},
		{
			name:     "enable secret and password both present is the weak-config conflict",
			src:      "enable secret 5 $1$abc$def\n!\nenable password letmein\n!\n",
			wantSubs: []string{`enable secret/password`},
		},
		{
			name:     "enable secret redefinition",
			src:      "enable secret 5 $1$abc$def\n!\nenable secret 5 $1$xyz$uvw\n!\n",
			wantSubs: []string{`enable secret/password`},
		},
		{
			name:     "snmp-server location replacement",
			src:      "snmp-server location HQ\n!\nsnmp-server location DR\n!\n",
			wantSubs: []string{`snmp-server location "DR" replaces "HQ"`},
		},
		{
			name:     "ip default-gateway replacement",
			src:      "ip default-gateway 10.0.0.1\n!\nip default-gateway 10.0.0.254\n!\n",
			wantSubs: []string{`ip default-gateway`},
		},
		{
			name:     "logging buffered size replacement",
			src:      "logging buffered 16384\n!\nlogging buffered 65536\n!\n",
			wantSubs: []string{`logging buffered`},
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

func TestGlobalScalarExemptions(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "enable secret levels do not interact",
			src:  "enable secret level 5 $1$x$y\n!\nenable secret level 10 $1$z$w\n!\n",
		},
		{
			name: "identical banner repeat falls to duplicate-command",
			src:  "banner motd ^Same^\n!\nbanner motd ^Same^\n!\n",
		},
		{
			name: "negation resets the tracker",
			src:  "ip domain-name old.example.com\n!\nno ip domain-name old.example.com\n!\nip domain-name new.example.com\n!\n",
		},
		{
			name: "ip domain list is additive",
			src:  "ip domain list corp.example.com\n!\nip domain list lab.example.com\n!\n",
		},
		{
			name: "toggle repeats are not redefined-settings",
			src:  "ip domain-name example.com\n!\nclock summer-time PDT recurring\n!\n",
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
