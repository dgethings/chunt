package cisco_ios_jinja2_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
)

// TestArgTypeDiagnostics covers the Tier-1 value types and the curated
// ip-address layer (chunt-j9t) end to end through DidOpen. Each positive case
// uses a REAL keyword from the generated DB whose snippet declares the typed
// placeholder, so keyword resolution + snippet alignment are exercised, not
// just the predicate. "pin" cases document deliberate conservative behavior
// (bindings the pass must NOT validate) so a future regression in either
// direction surfaces.
func TestArgTypeDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantCode string
		wantHit  bool
		wantSubs []string
	}{
		// --- wildcard-mask: registered, but its only DB holders
		// ("network (EIGRP)", "distance (IP)") are paren-aliased doc pages
		// excluded from keyword probing — pin today's unreachability; flips
		// if the DB ever gains a reachable holder.
		{
			name:     "eigrp network wildcard unreachable today (pin)",
			src:      "router eigrp 100\n network 10.0.0.0 wibble\n!\n",
			wantCode: "invalid-wildcard-mask",
			wantHit:  false,
		},

		// --- ipv6-address ---
		{
			name:     "ipv6 dhcp relay destination bad address",
			src:      "interface Gi0/0\n ipv6 dhcp relay destination wibble\n!\n",
			wantCode: "invalid-ipv6-address",
			wantHit:  true,
			wantSubs: []string{"an IPv6 address", `"wibble"`},
		},
		{
			name:     "ipv6 dhcp relay destination valid",
			src:      "interface Gi0/0\n ipv6 dhcp relay destination 2001:db8::1\n!\n",
			wantCode: "invalid-ipv6-address",
			wantHit:  false,
		},
		{
			name:     "ipv6 host with port then bad address",
			src:      "ipv6 host foo 8080 wibble\n",
			wantCode: "invalid-ipv6-address",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},
		{
			name:     "ipv6 host without port: bad token lands in optional port slot, not validated (pin)",
			src:      "ipv6 host foo wibble\n",
			wantCode: "invalid-ipv6-address",
			wantHit:  false,
		},

		// --- ipv6-prefix (base ipv6 nat prefix snippet: ${1:ipv6-prefix/prefix-length}) ---
		{
			name:     "ipv6 nat prefix bad prefix",
			src:      "ipv6 nat prefix wibble\n",
			wantCode: "invalid-ipv6-prefix",
			wantHit:  true,
			wantSubs: []string{"an IPv6 prefix", `"wibble"`},
		},
		{
			name:     "ipv6 nat prefix v4 CIDR",
			src:      "ipv6 nat prefix 10.0.0.0/8\n",
			wantCode: "invalid-ipv6-prefix",
			wantHit:  true,
			wantSubs: []string{"an IPv6 prefix"},
		},
		{
			name:     "ipv6 nat prefix valid",
			src:      "ipv6 nat prefix 2001:db8::/32\n",
			wantCode: "invalid-ipv6-prefix",
			wantHit:  false,
		},

		// --- mac-address ---
		{
			name:     "mlacp system-mac bad",
			src:      "mlacp system-mac wibble\n",
			wantCode: "invalid-mac-address",
			wantHit:  true,
			wantSubs: []string{"a MAC address", `"wibble"`},
		},
		{
			name:     "mlacp system-mac valid",
			src:      "mlacp system-mac 0000.0c07.ac01\n",
			wantCode: "invalid-mac-address",
			wantHit:  false,
		},
		{
			name:     "igmp snooping static mac unreachable (pin: keyword sits behind the vlan-id token)",
			src:      "ip igmp snooping vlan 100 static wibble interface Gi0/1\n",
			wantCode: "invalid-mac-address",
			wantHit:  false,
		},
		{
			name:     "standby 1 mac-address bad (pin: structurally unreachable)",
			src:      "interface Gi0/0\n standby 1 mac-address wibble\n!\n",
			wantCode: "invalid-mac-address",
			wantHit:  false,
		},
		{
			name:     "standby mac-address group omitted (pin: optional-slot bind, not validated)",
			src:      "interface Gi0/0\n standby mac-address wibble\n!\n",
			wantCode: "invalid-mac-address",
			wantHit:  false,
		},

		// --- as-number / autonomous-system-number ---
		{
			name:     "bgp confederation identifier bad",
			src:      "router bgp 100\n bgp confederation identifier wibble\n!\n",
			wantCode: "invalid-as-number",
			wantHit:  true,
			wantSubs: []string{"an AS number", `"wibble"`},
		},
		{
			name:     "bgp confederation identifier numeric",
			src:      "router bgp 100\n bgp confederation identifier 65001\n!\n",
			wantCode: "invalid-as-number",
			wantHit:  false,
		},
		{
			name:     "bgp confederation identifier AS-dot form",
			src:      "router bgp 100\n bgp confederation identifier 1.1\n!\n",
			wantCode: "invalid-as-number",
			wantHit:  false,
		},

		// --- vlan-id ---
		{
			name:     "switchport access vlan bad",
			src:      "interface Gi0/0\n switchport access vlan wibble\n!\n",
			wantCode: "invalid-vlan-id",
			wantHit:  true,
			wantSubs: []string{"a VLAN ID", `"wibble"`},
		},
		{
			name:     "switchport access vlan out of range",
			src:      "interface Gi0/0\n switchport access vlan 5000\n!\n",
			wantCode: "invalid-vlan-id",
			wantHit:  true,
			wantSubs: []string{"a VLAN ID", `"5000"`},
		},
		{
			name:     "switchport access vlan valid upper bound",
			src:      "interface Gi0/0\n switchport access vlan 4094\n!\n",
			wantCode: "invalid-vlan-id",
			wantHit:  false,
		},

		// --- seconds ---
		{
			name:     "ageout bad",
			src:      "ageout wibble\n",
			wantCode: "invalid-seconds",
			wantHit:  true,
			wantSubs: []string{"a number of seconds", `"wibble"`},
		},
		{
			name:     "ageout valid",
			src:      "ageout 3600\n",
			wantCode: "invalid-seconds",
			wantHit:  false,
		},

		// --- distance: registered, but its slots sit inside optional [...] groups
		// on the reachable keywords (ip route family). Optional-slot bindings
		// are suppressed (greedy alignment cannot prove the token fills the
		// slot), so distance is validated nowhere reachable today — pin that
		// neither the legit nor the "permanent" trailing keyword is flagged.
		{
			name:     "ip route trailing token not validated as distance (pin)",
			src:      "ip route 10.0.0.0 255.0.0.0 10.0.0.2 wibble\n",
			wantCode: "invalid-distance",
			wantHit:  false,
		},
		{
			name:     "ip route permanent keyword must not become distance (pin)",
			src:      "ip route 10.0.0.0 255.0.0.0 10.0.0.2 permanent\n",
			wantCode: "invalid-distance",
			wantHit:  false,
		},

		// --- curated ip-address layer ---
		{
			name:     "ip address bad address slot",
			src:      "interface Gi0/0\n ip address wibble 255.255.255.0\n!\n",
			wantCode: "invalid-ip-address",
			wantHit:  true,
			wantSubs: []string{"an IPv4 address", `"wibble"`},
		},
		{
			name:     "ip route prefix bad",
			src:      "ip route wibble 255.0.0.0 10.0.0.2\n",
			wantCode: "invalid-ip-address",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},
		{
			name:     "ip route vrf prefix bad",
			src:      "ip route vrf FOO wibble 255.0.0.0 10.0.0.2\n",
			wantCode: "invalid-ip-address",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},
		{
			name:     "ip nat outside source static bad global-ip",
			src:      "ip nat outside source static wibble 10.0.0.2\n",
			wantCode: "invalid-ip-address",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},
		{
			name:     "ip nat outside source static bad local-ip",
			src:      "ip nat outside source static 10.0.0.1 wibble\n",
			wantCode: "invalid-ip-address",
			wantHit:  true,
			wantSubs: []string{`"wibble"`},
		},
		{
			name:     "ip address negotiated is allowed (documented form absent from DB)",
			src:      "interface Gi0/0\n ip address negotiated\n!\n",
			wantCode: "invalid-ip-address",
			wantHit:  false,
		},
		{
			name:     "ip address dhcp resolves to its own keyword",
			src:      "interface Gi0/0\n ip address dhcp\n!\n",
			wantCode: "invalid-ip-address",
			wantHit:  false,
		},
		{
			name:     "ip address valid",
			src:      "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n!\n",
			wantCode: "invalid-ip-address",
			wantHit:  false,
		},
		{
			name:     "ip route valid",
			src:      "ip route 10.0.0.0 255.0.0.0 10.0.0.2\n",
			wantCode: "invalid-ip-address",
			wantHit:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			hits := diagsByCode(diags, tc.wantCode)
			if tc.wantHit {
				if len(hits) != 1 {
					t.Fatalf("want exactly 1 %s diagnostic, got %d: %+v", tc.wantCode, len(hits), hits)
				}
				d := hits[0]
				if d.Severity != protocol.SeverityError {
					t.Errorf("severity: got %d, want Error (1)", d.Severity)
				}
				if d.Source != "chunt" {
					t.Errorf("source: got %q, want \"chunt\"", d.Source)
				}
				for _, sub := range tc.wantSubs {
					if !contains(d.Message, sub) {
						t.Errorf("message %q missing substring %q", d.Message, sub)
					}
				}
			} else if len(hits) != 0 {
				t.Errorf("want no %s diagnostic, got %d: %+v", tc.wantCode, len(hits), hits)
			}
		})
	}
}

// TestArgTypeDiagnostics_BothSlotsBad asserts two typed errors on one line
// earn two independent diagnostics anchored on their own tokens.
func TestArgTypeDiagnostics_BothSlotsBad(t *testing.T) {
	src := "interface Gi0/0\n ip address wibble wobble\n!\n"
	diags := openDiags(t, src)
	ipHits := diagsByCode(diags, "invalid-ip-address")
	maskHits := diagsByCode(diags, "invalid-mask")
	if len(ipHits) != 1 || len(maskHits) != 1 {
		t.Fatalf("want 1 invalid-ip-address + 1 invalid-mask, got %d + %d: %+v", len(ipHits), len(maskHits), diags)
	}
	if !contains(ipHits[0].Message, "wibble") || !contains(maskHits[0].Message, "wobble") {
		t.Errorf("messages must name their own tokens: %q / %q", ipHits[0].Message, maskHits[0].Message)
	}
	// " ip address wibble wobble": wibble at 12-18, wobble at 19-25.
	if ipHits[0].Range.Start.Character != 12 || ipHits[0].Range.End.Character != 18 {
		t.Errorf("invalid-ip-address range: got %d-%d, want 12-18", ipHits[0].Range.Start.Character, ipHits[0].Range.End.Character)
	}
	if maskHits[0].Range.Start.Character != 19 || maskHits[0].Range.End.Character != 25 {
		t.Errorf("invalid-mask range: got %d-%d, want 19-25", maskHits[0].Range.Start.Character, maskHits[0].Range.End.Character)
	}
}

// TestArgTypeDiagnostics_RangeAndMessage pins the exact range and message of
// the canonical curated case.
func TestArgTypeDiagnostics_RangeAndMessage(t *testing.T) {
	src := "interface Gi0/0\n ip address wibble 255.255.255.0\n!\n"
	diags := openDiags(t, src)
	hits := diagsByCode(diags, "invalid-ip-address")
	if len(hits) != 1 {
		t.Fatalf("want exactly 1 invalid-ip-address, got %d: %+v", len(hits), hits)
	}
	d := hits[0]
	// Line 1: " ip address wibble 255.255.255.0" — wibble spans cols 12..18.
	if d.Range.Start.Line != 1 || d.Range.Start.Character != 12 || d.Range.End.Character != 18 {
		t.Errorf("range: got %d:%d-%d, want 1:12-18", d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Character)
	}
	wantMsg := `expected an IPv4 address (e.g. 10.0.0.1), got "wibble"`
	if d.Message != wantMsg {
		t.Errorf("message: got %q, want %q", d.Message, wantMsg)
	}
}
