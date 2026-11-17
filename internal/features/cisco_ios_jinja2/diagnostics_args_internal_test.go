package cisco_ios_jinja2

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Tests for the snippet-shape machinery behind the argument-value passes
// (chunt-at0, chunt-j9t): parseSnippetSlots (snippet -> ordered slots),
// alignSnippet (slots x document tokens -> placeholder bindings), the value
// predicates, and the global + curated validator registries.

func TestParseSnippetSlots(t *testing.T) {
	cases := []struct {
		name    string
		snippet string
		want    []snippetSlot
	}{
		{
			name:    "ip address: choice kept as a token-consuming slot",
			snippet: "ip address ${1:ip-address} ${2:mask} ${3|secondary vrf vrf-name,secondary|}",
			want: []snippetSlot{
				{kind: slotLiteral, literal: "ip"},
				{kind: slotLiteral, literal: "address"},
				{kind: slotValue, name: "ip-address"},
				{kind: slotValue, name: "mask"},
				{kind: slotChoice},
			},
		},
		{
			name:    "optional group yields open/close markers",
			snippet: "ip route [vrf ${1:vrf-name}] ${2:prefix} ${3:mask}",
			want: []snippetSlot{
				{kind: slotLiteral, literal: "ip"},
				{kind: slotLiteral, literal: "route"},
				{kind: slotOpen},
				{kind: slotLiteral, literal: "vrf"},
				{kind: slotValue, name: "vrf-name"},
				{kind: slotClose},
				{kind: slotValue, name: "prefix"},
				{kind: slotValue, name: "mask"},
			},
		},
		{
			name:    "spaced brackets [ vrf ... ] parse the same as fused",
			snippet: "ip route [ vrf ${1:vrf-name} ] ${2:prefix}",
			want: []snippetSlot{
				{kind: slotLiteral, literal: "ip"},
				{kind: slotLiteral, literal: "route"},
				{kind: slotOpen},
				{kind: slotLiteral, literal: "vrf"},
				{kind: slotValue, name: "vrf-name"},
				{kind: slotClose},
				{kind: slotValue, name: "prefix"},
			},
		},
		{
			name:    "brackets fused to words split open/close",
			snippet: "ip address dhcp [client-id ${1:interface-type}] [hostname ${2:hostname}]",
			want: []snippetSlot{
				{kind: slotLiteral, literal: "ip"},
				{kind: slotLiteral, literal: "address"},
				{kind: slotLiteral, literal: "dhcp"},
				{kind: slotOpen},
				{kind: slotLiteral, literal: "client-id"},
				{kind: slotValue, name: "interface-type"},
				{kind: slotClose},
				{kind: slotOpen},
				{kind: slotLiteral, literal: "hostname"},
				{kind: slotValue, name: "hostname"},
				{kind: slotClose},
			},
		},
		{
			name:    "bare tabstop ${N} is a value slot with empty name",
			snippet: "shutdown ${5}",
			want: []snippetSlot{
				{kind: slotLiteral, literal: "shutdown"},
				{kind: slotValue, name: ""},
			},
		},
		{
			name:    "placeholder name with / suffix normalizes to the prefix",
			snippet: "ipv6 nat prefix ${1:ipv6-prefix/prefix-length}",
			want: []snippetSlot{
				{kind: slotLiteral, literal: "ipv6"},
				{kind: slotLiteral, literal: "nat"},
				{kind: slotLiteral, literal: "prefix"},
				{kind: slotValue, name: "ipv6-prefix"},
			},
		},
		{
			name:    "malformed trailing slash placeholder -> nil (unparseable)",
			snippet: "nat64 route ${1:ipv4-prefix}/ ${2:mask}",
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSnippetSlots(tc.snippet)
			if len(got) != len(tc.want) {
				t.Fatalf("slots: got %d, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("slot %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestAlignSnippet(t *testing.T) {
	ipAddressSlots := parseSnippetSlots("ip address ${1:ip-address} ${2:mask} ${3|secondary vrf vrf-name,secondary|}")
	ipRouteSlots := parseSnippetSlots("ip route [vrf ${1:vrf-name}] ${2:prefix} ${3:mask} ${4|ip-address,interface-type interface-number ip-address,interface-type interface-number|} [dhcp] [${5:distance}]")
	toks := func(ss ...string) []docToken {
		out := make([]docToken, len(ss))
		for i, s := range ss {
			out[i] = docToken{text: s}
		}
		return out
	}

	cases := []struct {
		name  string
		slots []snippetSlot
		toks  []docToken
		want  []argBinding // (name, text, optional) in order
		fail  bool         // alignment must bail (return nil)
	}{
		{
			name:  "canonical ip address line",
			slots: ipAddressSlots,
			toks:  toks("ip", "address", "10.0.0.1", "wibble"),
			want: []argBinding{
				{name: "ip-address", token: docToken{text: "10.0.0.1"}},
				{name: "mask", token: docToken{text: "wibble"}},
			},
		},
		{
			name:  "trailing choice keyword is consumed, not bound",
			slots: ipAddressSlots,
			toks:  toks("ip", "address", "10.0.0.1", "255.255.255.0", "secondary"),
			want: []argBinding{
				{name: "ip-address", token: docToken{text: "10.0.0.1"}},
				{name: "mask", token: docToken{text: "255.255.255.0"}},
			},
		},
		{
			name:  "ip route with vrf group present",
			slots: ipRouteSlots,
			toks:  toks("ip", "route", "vrf", "FOO", "10.0.0.0", "wibble", "10.0.0.2"),
			want: []argBinding{
				{name: "vrf-name", token: docToken{text: "FOO"}},
				{name: "prefix", token: docToken{text: "10.0.0.0"}},
				{name: "mask", token: docToken{text: "wibble"}},
				// the choice consumes the next-hop (10.0.0.2) — no binding
			},
		},
		{
			name:  "ip route with vrf group absent (skipped)",
			slots: ipRouteSlots,
			toks:  toks("ip", "route", "10.0.0.0", "wibble", "10.0.0.2"),
			want: []argBinding{
				{name: "prefix", token: docToken{text: "10.0.0.0"}},
				{name: "mask", token: docToken{text: "wibble"}},
			},
		},
		{
			name:  "ip route with distance: optional-slot binding is flagged optional",
			slots: ipRouteSlots,
			toks:  toks("ip", "route", "10.0.0.0", "255.0.0.0", "10.0.0.2", "25"),
			want: []argBinding{
				{name: "prefix", token: docToken{text: "10.0.0.0"}},
				{name: "mask", token: docToken{text: "255.0.0.0"}},
				{name: "distance", token: docToken{text: "25"}, optional: true},
			},
		},
		{
			name:  "ip route permanent: trailing keyword binds optional distance, never validated",
			slots: ipRouteSlots,
			toks:  toks("ip", "route", "10.0.0.0", "255.0.0.0", "10.0.0.2", "permanent"),
			want: []argBinding{
				{name: "prefix", token: docToken{text: "10.0.0.0"}},
				{name: "mask", token: docToken{text: "255.0.0.0"}},
				{name: "distance", token: docToken{text: "permanent"}, optional: true},
			},
		},
		{
			name:  "jinja token binds but is marked",
			slots: ipAddressSlots,
			toks: []docToken{
				{text: "ip"}, {text: "address"},
				{text: "{{ addr }}", jinja: true},
			},
			want: []argBinding{
				{name: "ip-address", token: docToken{text: "{{ addr }}", jinja: true}},
			},
		},
		{
			name:  "literal mismatch outside optional group bails",
			slots: ipAddressSlots,
			toks:  toks("ip", "unrelated", "10.0.0.1", "wibble"),
			fail:  true,
		},
		{
			name:  "doc shorter than slots: partial bindings kept",
			slots: ipAddressSlots,
			toks:  toks("ip", "address"),
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := alignSnippet(tc.slots, tc.toks)
			if tc.fail {
				if got != nil {
					t.Fatalf("want nil (bail), got %+v", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("bindings: got %d, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				w := tc.want[i]
				g := got[i]
				if g.name != w.name || g.token.text != w.token.text || g.token.jinja != w.token.jinja || g.optional != w.optional {
					t.Errorf("binding %d: got (%s, %q, jinja=%v, optional=%v), want (%s, %q, jinja=%v, optional=%v)",
						i, g.name, g.token.text, g.token.jinja, g.optional,
						w.name, w.token.text, w.token.jinja, w.optional)
				}
			}
		})
	}
}

func TestIsSubnetMask(t *testing.T) {
	valid := []string{
		"255.255.255.0",   // /24
		"255.255.255.252", // /30
		"255.255.254.0",   // /23
		"255.0.0.0",       // /8
		"128.0.0.0",       // /1
		"0.0.0.0",         // all-zero
		"255.255.255.255", // all-ones
	}
	invalid := []string{
		"wibble",
		"10.0.0.1",      // host address, not a mask
		"0.0.0.255",     // wildcard mask
		"255.0.255.0",   // non-contiguous ones
		"255.255.253.0", // 253 = 11111101, non-contiguous
		"256.0.0.0",     // octet out of range
		"1.2.3",         // too few octets
		"1.2.3.4.5",     // too many octets
		"",              // empty
		"255.255.255",   // missing octet
	}
	for _, s := range valid {
		if !isSubnetMask(s) {
			t.Errorf("isSubnetMask(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isSubnetMask(s) {
			t.Errorf("isSubnetMask(%q) = true, want false", s)
		}
	}
}

func TestIsIPv6Address(t *testing.T) {
	valid := []string{"2001:db8::1", "::1", "fe80::", "2001:0db8:0000:0000:0000:0000:0000:0001"}
	invalid := []string{
		"wibble",
		"10.0.0.1",      // IPv4
		"2001:db8:::1",  // malformed
		"2001:db8::/32", // prefix, not address
		"",
	}
	for _, s := range valid {
		if !isIPv6Address(s) {
			t.Errorf("isIPv6Address(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isIPv6Address(s) {
			t.Errorf("isIPv6Address(%q) = true, want false", s)
		}
	}
}

func TestIsIPv6Prefix(t *testing.T) {
	valid := []string{"2001:db8::/32", "::/0", "2001:db8::1/64" /* host bits: accepted, conservative */}
	invalid := []string{
		"wibble",
		"10.0.0.0/8",     // IPv4 family
		"2001:db8::",     // no length
		"2001:db8::/129", // length out of range
		"2001:db8::/32/64",
		"",
	}
	for _, s := range valid {
		if !isIPv6Prefix(s) {
			t.Errorf("isIPv6Prefix(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isIPv6Prefix(s) {
			t.Errorf("isIPv6Prefix(%q) = true, want false", s)
		}
	}
}

func TestIsMACAddress(t *testing.T) {
	valid := []string{"0000.0c07.ac01", "ffff.ffff.ffff", "0c07.ac01.0000"}
	invalid := []string{
		"wibble",
		"00:0c:07:ac:00:01", // colon form: IOS command syntax uses dotted triplets
		"0000.0c07.ac0",     // short final group
		"0000.0c07.ac01a",   // long final group
		"0g00.0c07.ac01",    // non-hex
		"0000.0c07",         // two groups
		"",
	}
	for _, s := range valid {
		if !isMACAddress(s) {
			t.Errorf("isMACAddress(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isMACAddress(s) {
			t.Errorf("isMACAddress(%q) = true, want false", s)
		}
	}
}

func TestIsASNumber(t *testing.T) {
	valid := []string{
		"65001",
		"1",
		"65535",
		"4294967295",
		"1.1",         // AS-dot
		"65535.65535", // AS-dot max
	}
	invalid := []string{
		"wibble",
		"65536.1",    // AS-dot high part out of range
		"1.65536",    // AS-dot low part out of range
		"4294967296", // plain out of range
		"1.2.3",      // too many parts
		"-1",
		"1.",
		".5",
		"",
	}
	for _, s := range valid {
		if !isASNumber(s) {
			t.Errorf("isASNumber(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isASNumber(s) {
			t.Errorf("isASNumber(%q) = true, want false", s)
		}
	}
}

func TestIsVLANID(t *testing.T) {
	valid := []string{"1", "100", "4094"}
	invalid := []string{"0", "4095", "5000", "wibble", "-1", "1.5", ""}
	for _, s := range valid {
		if !isVLANID(s) {
			t.Errorf("isVLANID(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isVLANID(s) {
			t.Errorf("isVLANID(%q) = true, want false", s)
		}
	}
}

func TestIsUnsignedNumber(t *testing.T) {
	valid := []string{"0", "1", "3600", "2592000"}
	invalid := []string{"wibble", "-1", "1.5", "1e3", "", " 5"}
	for _, s := range valid {
		if !isUnsignedNumber(s) {
			t.Errorf("isUnsignedNumber(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isUnsignedNumber(s) {
			t.Errorf("isUnsignedNumber(%q) = true, want false", s)
		}
	}
}

// TestArgValidatorFor pins the lookup order: the curated per-keyword layer
// wins over the global name registry, and names absent from both return
// not-found. The allow-set (documented IOS forms missing from the DB) must
// satisfy the validator.
func TestArgValidatorFor(t *testing.T) {
	// Curated entries.
	if v, ok := argValidatorFor("ip address", "ip-address"); !ok || v.code != "invalid-ip-address" {
		t.Errorf(`argValidatorFor("ip address","ip-address") = (%+v, %v), want curated invalid-ip-address`, v, ok)
	} else if !v.allow["negotiated"] {
		t.Errorf(`curated ip-address validator must allow "negotiated" (documented IOS form absent from the DB)`)
	}
	if v, ok := argValidatorFor("ip route", "prefix"); !ok || v.code != "invalid-ip-address" {
		t.Errorf(`argValidatorFor("ip route","prefix") = (%+v, %v), want curated invalid-ip-address`, v, ok)
	}
	// ip-address is NOT globally registered: an un-curated keyword never
	// validates it (hostname-accepting slots must not be flagged).
	if _, ok := argValidatorFor("boot system", "ip-address"); ok {
		t.Errorf(`argValidatorFor("boot system","ip-address") found, want not-found (ip-address is curated-only)`)
	}
	// Global fallback.
	if v, ok := argValidatorFor("anything", "mask"); !ok || v.code != "invalid-mask" {
		t.Errorf(`argValidatorFor("anything","mask") = (%+v, %v), want global invalid-mask`, v, ok)
	}
	if _, ok := argValidatorFor("anything", "ip-address"); ok {
		t.Errorf(`argValidatorFor("anything","ip-address") found, want not-found`)
	}
}

// TestArgValidatorsRegistry pins the conservative registries: Tier-1
// unambiguous names globally, ip-address only via the curated per-keyword
// layer, and ambiguous names (port, number, dscp-value) deliberately absent.
func TestArgValidatorsRegistry(t *testing.T) {
	globalPresent := []string{
		"mask", "subnet-mask", "network-mask",
		"wildcard-mask",
		"ipv6-address", "ipv6-prefix",
		"mac-address",
		"as-number", "autonomous-system-number",
		"vlan-id",
		"seconds", "milliseconds", "minutes",
		"distance",
	}
	for _, name := range globalPresent {
		if _, ok := argValidators[name]; !ok {
			t.Errorf("argValidators[%q] missing, want present", name)
		}
	}
	for _, name := range []string{"ip-address", "port", "port-number", "number", "dscp-value", "hash-mask-length", "bit-mask-pattern", ""} {
		if _, ok := argValidators[name]; ok {
			t.Errorf("argValidators[%q] present, want absent (conservative)", name)
		}
	}
	wantCurated := map[string][]string{
		"ip address":            {"ip-address"},
		"ip route":              {"prefix"},
		"ip route vrf":          {"prefix"},
		"ip nat outside source": {"global-ip", "local-ip"},
	}
	if len(keywordArgValidators) != len(wantCurated) {
		t.Errorf("keywordArgValidators has %d keywords, want %d: %+v", len(keywordArgValidators), len(wantCurated), keywordArgValidators)
	}
	for kw, names := range wantCurated {
		m, ok := keywordArgValidators[kw]
		if !ok {
			t.Errorf("keywordArgValidators[%q] missing, want present", kw)
			continue
		}
		for _, n := range names {
			if _, ok := m[n]; !ok {
				t.Errorf("keywordArgValidators[%q][%q] missing, want present", kw, n)
			}
		}
	}
}

// TestKeywordArgValidatorsAgainstDB is the curated-layer drift guard: every
// curated keyword must exist in the generated DB, at least one of its snippet
// forms must parse, and the curated placeholder name must appear as a value
// slot in SOME form — so a keyword-DB regeneration that renames or reshapes
// a curated command turns the silent loss of validation into a test failure.
func TestKeywordArgValidatorsAgainstDB(t *testing.T) {
	f := New()
	defer f.Close()
	for kw, names := range keywordArgValidators {
		entry, ok := f.keyword.Lookup(kw)
		if !ok {
			t.Errorf("curated keyword %q not found in DB", kw)
			continue
		}
		if len(entry.Snippets) == 0 {
			t.Errorf("curated keyword %q has no snippets", kw)
			continue
		}
		for name := range names {
			found := false
			for _, snip := range entry.Snippets {
				for _, s := range parseSnippetSlots(snip) {
					if s.kind == slotValue && s.name == name {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				t.Errorf("curated keyword %q: placeholder %q not a value slot in any snippet form", kw, name)
			}
		}
	}
}

// TestCommandTokens probes the docToken extraction from a real parse tree:
// named children in source order, identifier + value/output args, and the
// jinja flag set exactly for output nodes.
func TestCommandTokens(t *testing.T) {
	f := New()
	defer f.Close()
	src := []byte("ip address {{ addr }} wibble\n")
	tree := f.parser.Parse(src, nil)
	defer tree.Close()
	root := tree.RootNode()
	var cl *sitter.Node
	walkAll(root, func(n *sitter.Node) bool {
		if cl == nil && n.Kind() == "command_line" {
			cl = n
		}
		return cl == nil
	})
	if cl == nil {
		t.Fatal("no command_line in tree")
	}
	toks := commandTokens(cl, src)
	want := []struct {
		text  string
		jinja bool
	}{
		{"ip", false},
		{"address", false},
		{"{{ addr }}", true},
		{"wibble", false},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %+v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].text != w.text || toks[i].jinja != w.jinja {
			t.Errorf("token %d: got (%q, jinja=%v), want (%q, jinja=%v)", i, toks[i].text, toks[i].jinja, w.text, w.jinja)
		}
		if toks[i].node == nil {
			t.Errorf("token %d has no backing node", i)
		}
	}
}
