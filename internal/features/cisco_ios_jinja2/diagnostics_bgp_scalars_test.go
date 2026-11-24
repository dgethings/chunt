package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Routing-process and BGP neighbor scalar coverage (chunt-otg.4).

func TestBGPScalarRedefinedSetting(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantSubs []string
	}{
		{
			name:     "timers bgp conflict within one process",
			src:      "router bgp 65001\n timers bgp 10 30\n timers bgp 60 180\n!\n",
			wantSubs: []string{`timers bgp "60 180" replaces "10 30"`},
		},
		{
			name:     "distance bgp conflict",
			src:      "router bgp 65001\n distance bgp 20 200 200\n distance bgp 20 100 100\n!\n",
			wantSubs: []string{`distance bgp`},
		},
		{
			name:     "neighbor remote-as conflict for one neighbor",
			src:      "router bgp 65001\n neighbor 10.0.0.1 remote-as 65002\n neighbor 10.0.0.1 remote-as 65003\n!\n",
			wantSubs: []string{`neighbor remote-as "65003" replaces "65002"`},
		},
		{
			name:     "neighbor description conflict",
			src:      "router bgp 65001\n neighbor 10.0.0.1 description Uplink\n neighbor 10.0.0.1 description Backup\n!\n",
			wantSubs: []string{`neighbor description`},
		},
		{
			name:     "neighbor password conflict",
			src:      "router bgp 65001\n neighbor 10.0.0.1 password secret123\n neighbor 10.0.0.1 password hunter2\n!\n",
			wantSubs: []string{`neighbor password`},
		},
		{
			name:     "neighbor update-source conflict",
			src:      "router bgp 65001\n neighbor 10.0.0.1 update-source Lo0\n neighbor 10.0.0.1 update-source Lo1\n!\n",
			wantSubs: []string{`neighbor update-source`},
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

func TestBGPScalarExemptions(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "different neighbors never interact",
			src:  "router bgp 65001\n neighbor 10.0.0.1 remote-as 65002\n neighbor 10.0.0.2 remote-as 65003\n neighbor 10.0.0.1 description Uplink\n neighbor 10.0.0.2 description Other\n!\n",
		},
		{
			name: "same scalars in different processes are fine",
			src:  "router bgp 65001\n timers bgp 10 30\n!\nrouter bgp 65002\n timers bgp 10 30\n!\n",
		},
		{
			name: "negation resets the neighbor tracker",
			src:  "router bgp 65001\n neighbor 10.0.0.1 remote-as 65002\n no neighbor 10.0.0.1 remote-as 65002\n neighbor 10.0.0.1 remote-as 65003\n!\n",
		},
		{
			name: "additive OSPF area ranges are not scalars",
			src:  "router ospf 1\n area 0 range 10.0.0.0 255.0.0.0\n area 0 range 10.1.0.0 255.255.0.0\n!\n",
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

// Peer-group definitions duplicate-check and membership references resolve
// (chunt-otg.4).
func TestBPGPeerGroupDefinitionAndMembership(t *testing.T) {
	// Duplicate definition.
	src := "router bgp 65001\n neighbor PG1 peer-group\n neighbor PG1 peer-group\n!\n"
	found := false
	for _, d := range openDiags(t, src) {
		if d.Code == "duplicate-peer-group" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no duplicate-peer-group in %v", openDiags(t, src))
	}

	// Membership resolves against the definition.
	src = "router bgp 65001\n neighbor PG1 peer-group\n neighbor 10.0.0.2 peer-group PG1\n neighbor 10.0.0.2 remote-as 65003\n!\n"
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-peer-group" {
			t.Fatalf("unexpected undefined-peer-group: %q", d.Message)
		}
	}

	// Undefined membership flags.
	src = "router bgp 65001\n neighbor 10.0.0.2 peer-group PG1\n!\n"
	found = false
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-peer-group" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no undefined-peer-group in %v", openDiags(t, src))
	}
}
