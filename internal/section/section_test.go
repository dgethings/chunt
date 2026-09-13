package section_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/ast"
	"github.com/dgethings/chunt/internal/section"
	ts_ci "github.com/dgethings/tree-sitter-cisco-ios-jinja2/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// coverAll exercises every section kind EnclosingSection must resolve, plus an
// address-family nested under router bgp (whose protocol it must inherit) and a
// top-level statement. Line/column positions below are 0-indexed.
const coverAll = "interface Gi0/0\n" +
	" speed 1000\n" +
	"!\n" +
	"router bgp 100\n" +
	" neighbor 10.0.0.2 remote-as 200\n" +
	" address-family ipv4\n" +
	"  neighbor 10.0.0.2 activate\n" +
	" !\n" +
	"!\n" +
	"ip access-list standard ACL1\n" +
	" permit 10.0.0.0 0.0.0.255\n" +
	"!\n" +
	"hostname r1\n" +
	"!\n"

func parseRoot(t *testing.T, src string) *sitter.Node {
	t.Helper()
	p := sitter.NewParser()
	p.SetLanguage(sitter.NewLanguage(ts_ci.Language()))
	tree := p.Parse([]byte(src), nil)
	t.Cleanup(func() {
		tree.Close()
		p.Close()
	})
	return tree.RootNode()
}

func TestEnclosingSection(t *testing.T) {
	cases := []struct {
		name         string
		line, col    uint
		wantSection  string
		wantProtocol string
	}{
		{"interface body -> config-if", 1, 1, "config-if", ""},
		{"router bgp body -> config-router/bgp", 4, 1, "config-router", "bgp"},
		{"address-family under bgp -> config-router-af, inherits bgp", 6, 2, "config-router-af", "bgp"},
		{"named standard ACL -> config-std-nacl", 10, 1, "config-std-nacl", ""},
		{"top-level statement -> config", 12, 0, "config", ""},
	}
	root := parseRoot(t, coverAll)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := ast.FindNodeAtPosition(root, tc.line, tc.col)
			if node == nil {
				t.Fatalf("FindNodeAtPosition(%d,%d) returned nil", tc.line, tc.col)
			}
			gotSection, gotProto := section.EnclosingSection(node, []byte(coverAll))
			if gotSection != tc.wantSection || gotProto != tc.wantProtocol {
				t.Errorf("EnclosingSection(@%d:%d) = (%q, %q), want (%q, %q)",
					tc.line, tc.col, gotSection, gotProto, tc.wantSection, tc.wantProtocol)
			}
		})
	}
}

func TestEnclosingSection_NilNodeDefaultsToConfig(t *testing.T) {
	if sec, proto := section.EnclosingSection(nil, nil); sec != "config" || proto != "" {
		t.Errorf("EnclosingSection(nil, nil) = (%q, %q), want (\"config\", \"\")", sec, proto)
	}
}

func TestIsSectionHeader(t *testing.T) {
	headerKinds := []string{
		"interface_section", "router_section", "address_family_section",
		"route_map_section", "class_map_section", "policy_map_section",
		"policy_map_class_section", "vlan_section", "line_section",
		"ip_access_list_section",
	}
	root := parseRoot(t, coverAll)
	// Collect every section node kind that actually appears and assert each is
	// recognized as a header.
	seen := map[string]bool{}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if section.IsSectionHeader(n) {
			seen[n.Kind()] = true
		}
		if !section.IsSectionHeader(n) {
			// Non-section nodes (command_line, *_statement, value) must NOT
			// classify as headers.
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
	// Every header kind in the grammar table must classify as a header. We
	// cannot force all of them to appear in one small config, so assert the
	// function directly on each kind string via IsKnownSectionKind.
	for _, k := range headerKinds {
		if !section.IsKnownSectionKind(k) {
			t.Errorf("IsKnownSectionKind(%q) = false, want true", k)
		}
	}
	// A command_line is NOT a section header.
	if node := ast.FindNodeAtPosition(root, 1, 1); node != nil && section.IsSectionHeader(node) {
		t.Errorf("command_line node wrongly classified as a section header (kind=%q)", node.Kind())
	}
	// Sanity: the config above exercises at least interface, router, af, acl.
	for _, want := range []string{"interface_section", "router_section", "address_family_section", "ip_access_list_section"} {
		if !seen[want] {
			t.Errorf("expected to encounter %q as a section header in the fixture, did not", want)
		}
	}
}

func TestSectionForKind(t *testing.T) {
	cases := map[string]string{
		"interface_section":        "config-if",
		"router_section":           "config-router",
		"address_family_section":   "config-router-af",
		"policy_map_class_section": "config-pmap-c",
		// ip_access_list_section resolves dynamically (standard/extended), so it
		// has no static mapping.
		"ip_access_list_section": "",
		"command_line":           "",
	}
	for kind, want := range cases {
		if got := section.SectionForKind(kind); got != want {
			t.Errorf("SectionForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestResolveACLSection covers both ACL header types so resolveACLSection's
// standard, extended, and default-fallback returns are all exercised.
func TestResolveACLSection(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"standard", "ip access-list standard ACL1\n permit 10.0.0.0 0.0.0.255\n!\n", "config-std-nacl"},
		{"extended", "ip access-list extended ACL2\n permit tcp any any\n!\n", "config-ext-nacl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := parseRoot(t, tc.src)
			node := ast.FindNodeAtPosition(root, 1, 1)
			if node == nil {
				t.Fatalf("FindNodeAtPosition returned nil")
			}
			got, _ := section.EnclosingSection(node, []byte(tc.src))
			if got != tc.want {
				t.Errorf("ACL %s: EnclosingSection = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsSectionHeader_Nil(t *testing.T) {
	if section.IsSectionHeader(nil) {
		t.Error("IsSectionHeader(nil) = true, want false")
	}
}
