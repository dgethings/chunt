package cisco_ios_jinja2

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Tests for the snippet-shape machinery behind the invalid-mask pass
// (chunt-at0): parseSnippetSlots (snippet -> ordered slots), alignSnippet
// (slots x document tokens -> placeholder bindings), and isSubnetMask.

func TestParseSnippetSlots(t *testing.T) {
	cases := []struct {
		name    string
		snippet string
		want    []snippetSlot
	}{
		{
			name:    "ip address: choices stripped, values kept",
			snippet: "ip address ${1:ip-address} ${2:mask} ${3|secondary vrf vrf-name,secondary|}",
			want: []snippetSlot{
				{kind: slotLiteral, literal: "ip"},
				{kind: slotLiteral, literal: "address"},
				{kind: slotValue, name: "ip-address"},
				{kind: slotValue, name: "mask"},
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
	ipRouteSlots := parseSnippetSlots("ip route [vrf ${1:vrf-name}] ${2:prefix} ${3:mask} ${4|ip-address,interface-type interface-number ip-address,interface-type interface-number|}")
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
		want  []argBinding // (name, text) pairs in order; nil = no bindings expected
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
			name:  "trailing keyword after filled slots is ignored",
			slots: ipAddressSlots,
			toks:  toks("ip", "address", "10.0.0.1", "255.255.255.0", "secondary"),
			want: []argBinding{
				{name: "ip-address", token: docToken{text: "10.0.0.1"}},
				{name: "mask", token: docToken{text: "255.255.255.0"}},
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
			name:  "ip route with vrf group present",
			slots: ipRouteSlots,
			toks:  toks("ip", "route", "vrf", "FOO", "10.0.0.0", "wibble", "10.0.0.2"),
			want: []argBinding{
				{name: "vrf-name", token: docToken{text: "FOO"}},
				{name: "prefix", token: docToken{text: "10.0.0.0"}},
				{name: "mask", token: docToken{text: "wibble"}},
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
				if got[i].name != tc.want[i].name || got[i].token != tc.want[i].token {
					t.Errorf("binding %d: got (%s, %q, jinja=%v), want (%s, %q, jinja=%v)",
						i, got[i].name, got[i].token.text, got[i].token.jinja,
						tc.want[i].name, tc.want[i].token.text, tc.want[i].token.jinja)
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

// TestArgValidatorsRegistry pins the conservative v1 registry: only
// unambiguous subnet-mask placeholder names are validated; wildcard and
// non-IP "mask" names are deliberately absent (false negatives preferred).
func TestArgValidatorsRegistry(t *testing.T) {
	for _, name := range []string{"mask", "subnet-mask", "network-mask"} {
		if _, ok := argValidators[name]; !ok {
			t.Errorf("argValidators[%q] missing, want present", name)
		}
	}
	for _, name := range []string{"wildcard-mask", "hash-mask-length", "bit-mask-pattern", "mask-value", "ip-address", "number", ""} {
		if _, ok := argValidators[name]; ok {
			t.Errorf("argValidators[%q] present, want absent (conservative v1)", name)
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
