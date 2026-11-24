package cisco_ios_jinja2_test

import (
	"testing"
)

// Infrastructure object duplicate-definition coverage (chunt-otg.7).

// One of each kind defines cleanly; repeated definitions flag through the
// shared duplicate-definition pass, including the folded legacy forms
// (`ip vrf NAME` counts as a vrf definition, `rtr N` as an sla).
func TestDuplicateInfrastructureDefinitions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{
			name: "vrf definition repeated",
			src:  "vrf definition BLUE\n!\nvrf definition BLUE\n!\n",
			code: "duplicate-vrf",
		},
		{
			name: "legacy ip vrf conflicts with vrf definition",
			src:  "vrf definition BLUE\n!\nip vrf BLUE\n!\n",
			code: "duplicate-vrf",
		},
		{
			name: "track repeated",
			src:  "track 1 interface Gi0/0 line-protocol\n!\ntrack 1 ip route 10.0.0.0/8\n!\n",
			code: "duplicate-track",
		},
		{
			name: "key chain repeated",
			src:  "key chain KC1\n key 1\n!\nkey chain KC1\n key 2\n!\n",
			code: "duplicate-key-chain",
		},
		{
			name: "dhcp pool repeated",
			src:  "ip dhcp pool POOL1\n network 10.0.0.0 255.255.255.0\n!\nip dhcp pool POOL1\n network 10.1.0.0 255.255.255.0\n!\n",
			code: "duplicate-dhcp-pool",
		},
		{
			name: "ip sla repeated",
			src:  "ip sla 1\n icmp-echo 8.8.8.8\n!\nip sla 1\n icmp-echo 9.9.9.9\n!\n",
			code: "duplicate-sla",
		},
		{
			name: "legacy rtr conflicts with ip sla",
			src:  "ip sla 1\n icmp-echo 8.8.8.8\n!\nrtr 1\n type echo protocol ipIcmpEcho 9.9.9.9\n!\n",
			code: "duplicate-sla",
		},
		{
			name: "time-range repeated",
			src:  "time-range TR1\n periodic weekdays 08:00 to 17:00\n!\ntime-range TR1\n absolute start 08:00 1 January 2025\n!\n",
			code: "duplicate-time-range",
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
				t.Fatalf("no %s diagnostic in %v", tc.code, openDiags(t, tc.src))
			}
		})
	}
}

// Distinct objects and additive shapes must not flag: monitor session
// source/destination lines configure the SAME session, and different
// sessions/tracks/vrfs coexist.
func TestInfrastructureNonDuplicates(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "monitor session source+destination are one session",
			src:  "monitor session 1 source interface Gi0/0\nmonitor session 1 destination interface Gi0/1\n!\n",
		},
		{
			name: "distinct sessions, tracks, vrfs coexist",
			src:  "monitor session 1 source interface Gi0/0\nmonitor session 2 destination interface Gi0/1\ntrack 1 interface Gi0/0 line-protocol\ntrack 2 interface Gi0/1 line-protocol\nvrf definition BLUE\n!\nvrf definition RED\n!\n",
		},
		{
			name: "vrf address-family sub-blocks are not routing AFs",
			src:  "vrf definition BLUE\n address-family ipv4\n!\nvrf definition RED\n address-family ipv4\n!\n",
		},
		{
			name: "negated line never registers a definition",
			src:  "vrf definition BLUE\n!\nno vrf definition BLUE\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				switch d.Code {
				case "duplicate-vrf", "duplicate-track", "duplicate-key-chain",
					"duplicate-dhcp-pool", "duplicate-sla", "duplicate-monitor-session",
					"duplicate-time-range", "duplicate-address-family":
					t.Fatalf("unexpected duplicate diagnostic: %q: %s", d.Code, d.Message)
				}
			}
		})
	}
}

// The symbol-table pass is order-insensitive (chunt-otg.6 convention): a
// remove-then-re-add pattern leaves TWO positive definitions and IS
// flagged — remove-then-re-add within one section is the repeatedTracker's
// (single-file) concern.
func TestInfrastructureRemoveThenReAddFlagged(t *testing.T) {
	src := "vrf definition BLUE\n!\nno vrf definition BLUE\n!\nvrf definition BLUE\n!\n"
	found := false
	for _, d := range openDiags(t, src) {
		if d.Code == "duplicate-vrf" {
			found = true
		}
	}
	if !found {
		t.Fatalf("remove-then-re-add must flag duplicate-vrf (order-insensitive pass): %v", openDiags(t, src))
	}
}
