package cisco_ios_jinja2_test

import (
	"testing"
)

// Track object and time-range undefined-reference coverage (chunt-otg.20).

func TestTrackUndefinedReferences(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "standby track number flags when undefined",
			src:  "interface Gi0/0\n standby 1 track 2 decrement 20\n!\n",
		},
		{
			name: "vrrp track flags when undefined",
			src:  "interface Gi0/0\n vrrp 1 track 3\n!\n",
		},
		{
			name: "glbp weighting track flags when undefined",
			src:  "interface Gi0/0\n glbp 1 weighting track 4\n!\n",
		},
		{
			name: "static route track flags when undefined",
			src:  "ip route 10.0.0.0 255.0.0.0 192.168.1.1 track 5\n!\n",
		},
		{
			name: "vrf static route track flags when undefined",
			src:  "ip route vrf BLUE 10.0.0.0 255.0.0.0 192.168.1.1 track 6\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			for _, d := range openDiags(t, tc.src) {
				if d.Code == "undefined-track" {
					found = true
				}
			}
			if !found {
				t.Fatalf("no undefined-track in %v", openDiags(t, tc.src))
			}
		})
	}
}

func TestTimeRangeUndefinedReferences(t *testing.T) {
	src := "ip access-list extended ACL1\n permit ip any any time-range TR1\n deny ip any any time-range TR2\n!\n"
	got := 0
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-time-range" {
			got++
		}
	}
	if got != 2 {
		t.Fatalf("got %d undefined-time-range diagnostics, want 2 (TR1+TR2): %v", got, openDiags(t, src))
	}
}

func TestTrackAndTimeRangeResolve(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "standby track resolves",
			src:  "track 2 interface Gi0/1 line-protocol\n!\ninterface Gi0/0\n standby 1 track 2 decrement 20\n!\n",
		},
		{
			name: "static route track resolves",
			src:  "track 5 list boolean or\n!\nip route 10.0.0.0 255.0.0.0 192.168.1.1 track 5\n!\n",
		},
		{
			name: "vrf static route keeps vrf + iface + track refs",
			src:  "vrf definition BLUE\n!\ntrack 6 interface Gi0/1 line-protocol\n!\ninterface Gi0/0\n!\nip route vrf BLUE 10.0.0.0 255.0.0.0 Gi0/0 track 6\n!\n",
		},
		{
			name: "legacy standby iface tracking still resolves",
			src:  "interface Gi0/1\n!\ninterface Gi0/0\n standby 1 track Gi0/1 decrement 10\n!\n",
		},
		{
			name: "time-range resolves",
			src:  "time-range BUSINESS\n periodic weekdays 08:00 to 17:00\n!\nip access-list extended ACL1\n permit ip any any time-range BUSINESS\n!\n",
		},
		{
			name: "negated track ref emits nothing",
			src:  "interface Gi0/0\n no standby 1 track 2\n!\n",
		},
		{
			name: "ip route next-hop ip is not a track or iface ref",
			src:  "ip route 10.0.0.0 255.0.0.0 192.168.1.1\n!\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range openDiags(t, tc.src) {
				switch d.Code {
				case "undefined-track", "undefined-time-range", "undefined-vrf", "undefined-interface":
					t.Fatalf("unexpected %s: %q", d.Code, d.Message)
				}
			}
		})
	}
}
