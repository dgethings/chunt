package ast_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/ast"
	ts_ci "github.com/dgethings/tree-sitter-cisco-ios-jinja2/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

const fullConfig = "!\n! version 26.1.0\n!\nversion 26.2.0\n!\narchive\n" +
	"   path disk0:someconfig\n   end\n!\n" +
	"async-bootp bootfile :172.30.1.1 \"pcboot\"\n!\n" +
	"line console\n   activation-character 127\n!\n" +
	"hostname test\n!\n" +
	"alias exec siib show ip interface brief\n" +
	"alias exec sibnb show ip bgp neighbor brief\n!\n"

// interfaceSection exercises an interface_section block (new parser).
const interfaceSection = "!\ninterface GigabitEthernet0/0\n ip address 10.0.0.1 255.255.255.0\n!\n"

// routerSection exercises a router_section block (new parser).
const routerSection = "!\nrouter bgp 100\n network 10.0.0.0 0.0.0.255 area 0\n!\n"

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

func TestFindNodeAtPosition_NilRoot(t *testing.T) {
	if got := ast.FindNodeAtPosition(nil, 0, 0); got != nil {
		t.Fatalf("want nil for nil root, got %q", got.GrammarName())
	}
}

func TestFindNodeAtPosition(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line uint
		col  uint
		want string // expected GrammarName of the deepest node
	}{
		// The motivating example: an incomplete hostname line with no trailing "!".
		// The hostname node spans only "hostname bob" (cols 0-12), so positions at
		// or past its end fall back to the config root.
		{"user/bang", "!\nhostname bob\n", 0, 0, "eos"},
		{"user/hostname_keyword", "!\nhostname bob\n", 1, 0, "hostname"},
		{"user/hostname_mid", "!\nhostname bob\n", 1, 4, "hostname"},
		{"user/value", "!\nhostname bob\n", 1, 9, "value"},
		// Cursor just past the trailing space of "hostname ": resolves into the
		// empty value the user is about to type.
		{"user/completion_accepted", "!\nhostname ", 1, 10, "value"},
		{"user/eol_falls_back_to_root", "!\nhostname bob\n", 1, 12, "config"},
		{"user/trailing_line_falls_back_to_root", "!\nhostname bob\n", 2, 0, "config"},

		// hostname section (system.txt)
		{"hostname/keyword", "!\n! version 26.1.0\n!\nhostname test\n!\n", 3, 0, "hostname"},
		{"hostname/value", "!\n! version 26.1.0\n!\nhostname test\n!\n", 3, 9, "value"},
		// Trailing "!" parses as a named eos node under the new grammar.
		{"hostname/trailing_eos", "!\n! version 26.1.0\n!\nhostname test\n!\n", 4, 0, "eos"},

		// service section (service.txt): the literal `version` keyword token.
		{"service/version_keyword", "!\n! version 26.1.0\n!\nversion 26.2.0\n!\n", 3, 0, "version"},

		// line section (line.txt) — `line` is now a prec-2 keyword token
		// (`line_kw`) because line_section is a hierarchical section. The body
		// sub-commands (e.g. activation-character) parse inside the section.
		{"line/keyword", "!\n! version 26.1.0\n!\nline console\n   activation-character 127\n!\n", 3, 0, "line_kw"},
		{"line/ascii_value", "!\n! version 26.1.0\n!\nline console\n   activation-character 127\n!\n", 4, 24, "value"},

		// archive section (archive.txt) — `archive` is a command_line identifier.
		{"archive/keyword", "!\n! version 26.1.0\n!\narchive\n   path disk0:someconfig\n   end\n!\n", 3, 0, "identifier"},

		// async-bootp section (async-bootp.txt) — the leading "async" token is a
		// command_line identifier; the remainder lands in a `text` node whose
		// parent is the config root.
		{"async/keyword", "!\n! version 26.1.0\n!\nasync-bootp bootfile :172.30.1.1 \"pcboot\"\n!\n", 3, 0, "identifier"},

		// interface_section (interface.txt) — newly covered.
		// The literal `interface` keyword is an anonymous token under interface_header.
		{"interface/keyword", interfaceSection, 1, 0, "interface"},
		// The device name lives in a named interface_name field of interface_header.
		{"interface/name", interfaceSection, 1, 10, "interface_name"},
		// Body command `ip address ...` is a command_line; its first identifier "ip".
		{"interface/sub_command_name", interfaceSection, 2, 1, "identifier"},
		// The first argument "address" sits inside a value leaf.
		{"interface/sub_command_arg", interfaceSection, 2, 6, "value"},

		// router_section (router.txt) — newly covered.
		// The literal `router` keyword is an anonymous token under router_header.
		{"router/protocol", routerSection, 1, 0, "router"},
		// The process-id ("100") is the value field of router_header.
		{"router/process_id", routerSection, 1, 11, "value"},
		// Body `network ...` statement's first argument is a value leaf.
		{"router/network_arg", routerSection, 2, 9, "value"},

		// full config (full-config.txt) - cross-section checks
		{"full/file_path", fullConfig, 6, 8, "value"},
		{"full/ascii_value", fullConfig, 12, 24, "value"},
		{"full/hostname", fullConfig, 14, 0, "hostname"},
		{"full/alias_mode", fullConfig, 16, 6, "value"},
		{"full/alias_command", fullConfig, 17, 16, "command_line"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := parseRoot(t, tc.src)
			got := ast.FindNodeAtPosition(root, tc.line, tc.col)
			if got == nil {
				t.Fatalf("FindNodeAtPosition(%d,%d) returned nil; want %q", tc.line, tc.col, tc.want)
			}
			if name := got.GrammarName(); name != tc.want {
				t.Errorf("FindNodeAtPosition(%d,%d) = %q, want %q", tc.line, tc.col, name, tc.want)
			}
		})
	}
}

// TestFindNodeAtPosition_OutOfRangeNoPanic guards B1 (chunt-mpc): an
// out-of-range position must not panic. The original code dereferenced the
// node (GrammarName) before the nil check, so a nil return from
// DescendantForPointRange would have panicked. (This binding returns a node
// rather than nil for out-of-range points, so the assertion is that the call
// completes and yields a usable node — the nil path itself is defensive.)
func TestFindNodeAtPosition_OutOfRangeNoPanic(t *testing.T) {
	root := parseRoot(t, "!\nhostname test\n!\n")
	for _, p := range []struct{ l, c uint }{{9999, 9999}, {500, 0}, {1, 99999}} {
		// Must not panic; a non-nil node (the root / enclosing node) is fine.
		got := ast.FindNodeAtPosition(root, p.l, p.c)
		if got == nil {
			t.Errorf("FindNodeAtPosition(%d,%d) returned nil for a non-nil root", p.l, p.c)
		}
	}
}
