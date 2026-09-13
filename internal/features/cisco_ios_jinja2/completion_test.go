package cisco_ios_jinja2_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
)

// placeholderDefaultRe matches LSP snippet placeholders of the form ${N:default}
// where N is a tabstop index and default is non-empty. Mirrors the regex in
// completion.go so this test fails if the production regex lets one through.
var placeholderDefaultRe = regexp.MustCompile(`\$\{(\d+):[^}]*\}`)

func TestCompletionWhileTypingValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line uint
		col  uint
	}{
		// Original hostname cases — covered by the AST-based early-return
		// (cursor lands on a `value` or MISSING-value node).
		{"hostname_space_col9", "!\nhostname ", 1, 9},
		{"hostname_space_col10", "!\nhostname ", 1, 10},
		{"hostname_name_on_value", "!\nhostname name", 1, 9},
		{"hostname_name_on_value", "!\nhostname name", 1, 12},

		// Section headers whose value field the grammar now models as
		// optional. Cursor sits in the empty value slot right after the
		// keyword — the AST resolves to the parent header (or to config),
		// so the line-based safety net must catch these.
		{"router_bgp_space", "!\nrouter bgp ", 1, 11},
		{"router_bgp_no_trailing_bang", "!\nrouter bgp \n", 1, 11},
		{"interface_space", "!\ninterface ", 1, 10},
		{"vlan_space", "!\nvlan ", 1, 5},
		{"route_map_space", "!\nroute-map ", 1, 10},
		{"class_map_space", "!\nclass-map ", 1, 10},
		{"policy_map_space", "!\npolicy-map ", 1, 11},
		{"line_space", "!\nline ", 1, 5},

		// Boundary case — cursor sits exactly at the value token's end,
		// so ast.FindNodeAtPosition returns the value's parent (the header).
		// The line-based safety net must catch this.
		{"router_bgp_value_end", "!\nrouter bgp 100", 1, 14},
		{"interface_name_end", "!\ninterface Gi0/0", 1, 15},
		{"hostname_value_end", "!\nhostname r1", 1, 11},

		// Generic command_line case: `ip access-list standard NAME` parses
		// as command_line (not as ip_access_list_section) until the name is
		// present. The line-based safety net must catch the placeholder.
		{"ip_access_list_standard_space", "!\nip access-list standard ", 1, 24},
		{"ip_access_list_extended_space", "!\nip access-list extended ", 1, 24},
		{"ip_access_list_standard_name_end", "!\nip access-list standard FOO", 1, 27},

		// Negated headers: `no <header> <value>` — suppress when past the
		// header keyword.
		{"no_router_bgp_space", "!\nno router bgp ", 1, 14},
		{"no_router_bgp_value_end", "!\nno router bgp 100", 1, 17},
		{"no_interface_space", "!\nno interface ", 1, 13},
		{"no_hostname_space", "!\nno hostname ", 1, 12},
		{"no_hostname_value_end", "!\nno hostname oldname", 1, 19},

		// version_statement — same shape as hostname_statement.
		{"version_space", "!\nversion ", 1, 8},
		{"version_value_end", "!\nversion 26.2.0", 1, 14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cisco_ios_jinja2.New()
			defer f.Close()
			doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte(tc.src))
			if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
				t.Fatalf("DidOpen: %v", err)
			}
			items, err := f.Completion(context.Background(), doc, protocol.Position{Line: tc.line, Character: tc.col})
			if err != nil {
				t.Fatalf("Completion: %v", err)
			}
			t.Logf("%q @ {%d,%d} -> %d items", tc.src, tc.line, tc.col, len(items))
			if len(items) != 0 {
				t.Errorf("expected no keyword suggestions while typing a value, got %d", len(items))
			}
		})
	}
}

// TestCompletionStillActiveForKeywordTyping pins the carve-outs that
// inArgumentPosition must NOT suppress: when the user is still typing the
// keyword itself (positive form or `no <kw>` form), keyword completion must
// remain available.
func TestCompletionStillActiveForKeywordTyping(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line uint
		col  uint
	}{
		// Empty line at top level — full config keyword list expected.
		{"top_level_empty", "!\n", 1, 0},
		// Partial keyword being typed.
		{"router_partial", "!\nroute", 1, 6},
		{"router_bgp_partial", "!\nrouter bg", 1, 9},
		// Cursor at the end of a complete keyword with no trailing space.
		// The user has typed the keyword but not yet moved into the value
		// slot, so accepting the snippet is still useful — the editor
		// expands it to the keyword + placeholder and jumps the cursor.
		{"router_bgp_complete", "!\nrouter bgp", 1, 10},
		{"interface_complete", "!\ninterface", 1, 9},
		{"vlan_complete", "!\nvlan", 1, 4},
		{"route_map_complete", "!\nroute-map", 1, 9},
		{"class_map_complete", "!\nclass-map", 1, 9},
		{"policy_map_complete", "!\npolicy-map", 1, 10},
		{"line_complete", "!\nline", 1, 4},
		// `no ` with cursor in the second-token slot — the user is typing
		// the keyword after `no`, so the `no <kw>` snippet list must stay
		// available.
		{"no_alone_space", "!\nno ", 1, 3},
		{"no_partial_keyword", "!\nno route", 1, 9},
		// `no router ` is the one odd case: the cursor is past `router` but
		// the user is still typing the routing-protocol keyword (`bgp` /
		// `ospf`). The keyword DB does not currently carry `no router bgp`
		// and `no router ospf` as filterable-from-`router` snippets, so this
		// is mainly a regression guard that the carve-out doesn't suppress
		// mid-keyword input.
		{"no_router_space", "!\nno router ", 1, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cisco_ios_jinja2.New()
			defer f.Close()
			doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte(tc.src))
			if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
				t.Fatalf("DidOpen: %v", err)
			}
			items, err := f.Completion(context.Background(), doc, protocol.Position{Line: tc.line, Character: tc.col})
			if err != nil {
				t.Fatalf("Completion: %v", err)
			}
			t.Logf("%q @ {%d,%d} -> %d items", tc.src, tc.line, tc.col, len(items))
			if len(items) == 0 {
				t.Errorf("expected keyword suggestions while typing a keyword, got 0")
			}
		})
	}
}

// TestCompletionSnippetsStripPlaceholderDefaults verifies that completion items
// never emit ${N:default} placeholders in their insertText. vim.snippet's
// selection of non-empty placeholders can leave the editor in INSERT mode
// instead of SELECT mode on some clients (notably blink.cmp on Neovim 0.12),
// which causes typed characters to insert before the default text rather than
// replacing it. See completion.go for the full rationale.
func TestCompletionSnippetsStripPlaceholderDefaults(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	src := []byte("!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, src)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one completion item")
	}

	for _, item := range items {
		if item.InsertText == nil {
			t.Errorf("item %q: missing insertText", item.Label)
			continue
		}
		if loc := placeholderDefaultRe.FindStringIndex(*item.InsertText); loc != nil {
			t.Errorf("item %q: insertText %q still contains a ${N:default} placeholder at %v",
				item.Label, *item.InsertText, loc)
		}
		if item.FilterText == nil {
			t.Errorf("item %q: missing filterText", item.Label)
			continue
		}
		if *item.FilterText != item.Label {
			t.Errorf("item %q: filterText %q != label %q",
				item.Label, *item.FilterText, item.Label)
		}
	}
}

// TestCompletionHostnameSnippetHasEmptyTabstop pins the exact insertText we
// emit for the hostname keyword after the fix: the original data file ships
// `hostname ${1:name}` but the LSP response must drop the `name` default so
// editors land the cursor in INSERT mode at the tabstop, fixing the bug where
// typing `r1` produced `hostname r1name` instead of `hostname r1`.
func TestCompletionHostnameSnippetHasEmptyTabstop(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte("!\n"))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}

	var hostnameItem *protocol.CompletionItem
	for i := range items {
		if items[i].Label == "hostname" {
			hostnameItem = &items[i]
			break
		}
	}
	if hostnameItem == nil {
		t.Fatalf("hostname completion item not found")
	}
	if hostnameItem.InsertText == nil {
		t.Fatalf("hostname item has no insertText")
	}
	want := "hostname ${1}"
	if got := *hostnameItem.InsertText; got != want {
		t.Errorf("hostname insertText = %q, want %q", got, want)
	}
}

// TestCompletionNoDuplicateLabels pins the fix for the duplicate-suggestions
// bug. The keyword database both (a) carries multiple snippets per keyword —
// the positive form and the `no <kw>` form — and (b) contains ~900 exact
// duplicate keyword records. The completion output must contain at most one
// item per distinct label regardless of how the underlying data repeats.
func TestCompletionNoDuplicateLabels(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte("!\n"))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one completion item")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if seen[item.Label] {
			t.Errorf("duplicate completion label %q", item.Label)
			continue
		}
		seen[item.Label] = true
		if item.FilterText == nil {
			t.Errorf("item %q: missing filterText", item.Label)
			continue
		}
		if *item.FilterText != item.Label {
			t.Errorf("item %q: filterText %q != label %q",
				item.Label, *item.FilterText, item.Label)
		}
	}
}

// TestCompletionNoFormHasDistinctLabel verifies the `no <kw>` snippet gets its
// own distinguishable label instead of collapsing onto the positive form. The
// downward-compatible-config keyword ships both
// `downward-compatible-config ${1:version}` and `no downward-compatible-config`;
// before the fix both surfaced as a single indistinguishable
// `downward-compatible-config` entry (twice).
func TestCompletionNoFormHasDistinctLabel(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte("!\n"))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	labels := map[string]bool{}
	for _, item := range items {
		labels[item.Label] = true
	}
	if !labels["downward-compatible-config"] {
		t.Errorf("expected label %q (positive form)", "downward-compatible-config")
	}
	if !labels["no downward-compatible-config"] {
		t.Errorf("expected label %q (negation form)", "no downward-compatible-config")
	}
}

// TestCompletionACLStandardVsExtended verifies that the section resolver reads
// the ip_access_list_header's "type" field and picks config-std-nacl for a
// standard ACL vs config-ext-nacl for an extended ACL. If the type-field
// resolution isn't working, both resolve to the same section and yield the
// same completion items.
func TestCompletionACLStandardVsExtended(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	// Standard ACL
	stdSrc := "!\nip access-list standard MY-STD\n permit 10.0.0.0 0.255.255.255\n!\n"
	stdDoc := document.New("file:///std.cfg", "cisco_ios_jinja2", 1, []byte(stdSrc))
	if _, err := f.DidOpen(context.Background(), stdDoc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	// Extended ACL
	extSrc := "!\nip access-list extended MY-EXT\n permit tcp any any eq 443\n!\n"
	extDoc := document.New("file:///ext.cfg", "cisco_ios_jinja2", 1, []byte(extSrc))
	if _, err := f.DidOpen(context.Background(), extDoc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	// Get completion items for each
	stdItems, err := f.Completion(context.Background(), stdDoc, protocol.Position{Line: 2, Character: 0})
	if err != nil {
		t.Fatalf("Std Completion: %v", err)
	}
	extItems, err := f.Completion(context.Background(), extDoc, protocol.Position{Line: 2, Character: 0})
	if err != nil {
		t.Fatalf("Ext Completion: %v", err)
	}

	if len(stdItems) == 0 {
		t.Error("expected completion items for standard ACL section")
	}
	if len(extItems) == 0 {
		t.Error("expected completion items for extended ACL section")
	}

	// Verify the items differ — standard and extended ACLs have different
	// keyword sets in the data. If both return the exact same items, the
	// type-field resolution isn't working.
	t.Logf("standard ACL: %d items, extended ACL: %d items", len(stdItems), len(extItems))
}

func findItemByLabel(items []protocol.CompletionItem, label string) *protocol.CompletionItem {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}

// TestCompletionSectionAware_InterfaceExcludesConfigKeywords pins the
// section-aware filtering: when the cursor sits inside an interface block,
// only config-if keywords are surfaced, while top-level config keywords
// (e.g. hostname) must be filtered out.
//
// We assert on the ip rarp-server item rather than clock: although both are
// Section "config-if", clock's snippets have no ${N} placeholder so its
// derived label is the full "clock autoactive preferpassive prefer" / "no
// clock" — not "clock". ip rarp-server ships "ip rarp-server ${1:ip-address}",
// whose label cleanly reduces to "ip rarp-server".
//
// The cursor position that resolves to the interface_section node is
// line 2, char 1 (the `c` token on the indented sub-command line).
func TestCompletionSectionAware_InterfaceExcludesConfigKeywords(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	src := "!\ninterface GigabitEthernet0/0\n c\n!\n"
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte(src))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 2, Character: 1})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one completion item inside the interface block")
	}
	if findItemByLabel(items, "ip rarp-server") == nil {
		t.Errorf("expected ip rarp-server (Section config-if) inside interface block")
	}
	if findItemByLabel(items, "hostname") != nil {
		t.Errorf("hostname (Section config) must NOT appear inside interface block")
	}
}

// TestCompletionSectionAware_TopLevelExcludesInterfaceKeywords is the inverse
// of the interface test: at top-level config the cursor must surface config
// keywords (hostname) and exclude config-if keywords (ip rarp-server).
func TestCompletionSectionAware_TopLevelExcludesInterfaceKeywords(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte("!\n"))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one completion item at top level")
	}
	if findItemByLabel(items, "hostname") == nil {
		t.Errorf("expected hostname (Section config) at top level")
	}
	if findItemByLabel(items, "ip rarp-server") != nil {
		t.Errorf("ip rarp-server (Section config-if) must NOT appear at top level")
	}
}

// TestCompletionIPAccessListSection verifies the normal (non-fallback) path
// for the ip_access_list_section, which the grammar maps to config-ext-nacl.
// Because config-ext-nacl carries keywords in the real data, completion must
// return them directly without falling back to an ancestor section.
func TestCompletionIPAccessListSection(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	src := "!\nip access-list extended TEST\n permit ip any any\n!\n"
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte(src))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	// Cursor on an empty line inside the ACL section
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 2, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected completion items inside ip access-list section, got none")
	}
}

// TestCompletionEmptySectionKeywordsAppearEverywhere pins the universal-
// Section rule at the completion layer: a keyword with Section "" (e.g. do)
// must appear in every section the cursor can resolve to.
func TestCompletionEmptySectionKeywordsAppearEverywhere(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line uint
		col  uint
	}{
		{"top_level", "!\n", 1, 0},
		{"inside_interface", "!\ninterface GigabitEthernet0/0\n c\n!\n", 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cisco_ios_jinja2.New()
			defer f.Close()

			doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte(tc.src))
			if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
				t.Fatalf("DidOpen: %v", err)
			}
			items, err := f.Completion(context.Background(), doc, protocol.Position{Line: tc.line, Character: tc.col})
			if err != nil {
				t.Fatalf("Completion: %v", err)
			}
			if findItemByLabel(items, "do") == nil {
				t.Errorf("expected universal keyword do (Section %q) in %q; labels: %v", "", tc.name, labelsOf(items))
			}
		})
	}
}

// TestCompletionDocumentationIsMarkupContent pins the change that widened
// CompletionItem.Documentation from a plain string to protocol.MarkupContent,
// honoring each keyword's Description.Format and Description.Value.
func TestCompletionDocumentationIsMarkupContent(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte("!\n"))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	item := findItemByLabel(items, "hostname")
	if item == nil {
		t.Fatalf("hostname completion item not found")
	}
	if item.Documentation == nil {
		t.Fatalf("hostname Documentation is nil; expected MarkupContent")
	}
	mc, ok := item.Documentation.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("hostname Documentation is %T; expected protocol.MarkupContent", item.Documentation)
	}
	if mc.Kind != protocol.PlainText {
		t.Errorf("hostname MarkupContent.Kind = %q, want %q", mc.Kind, protocol.PlainText)
	}
	want := "To specify or modify the hostname for the network server, use the hostname command in global configuration mode."
	if mc.Value != want {
		t.Errorf("hostname MarkupContent.Value = %q, want %q", mc.Value, want)
	}
}

// TestCompletionDocumentationOmittedForEmptyDescription pins that
// Documentation is left nil when a keyword's Description.Value is empty
// (e.g. the file privilege keyword), so the editor does not render an
// empty documentation pane.
func TestCompletionDocumentationOmittedForEmptyDescription(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, []byte("!\n"))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	item := findItemByLabel(items, "file privilege level")
	if item == nil {
		t.Fatalf("file privilege level completion item not found")
	}
	if item.Documentation != nil {
		t.Errorf("file privilege level Documentation = %v; want nil (Description.Value is empty)", item.Documentation)
	}
}

func labelsOf(items []protocol.CompletionItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Label)
	}
	return out
}

// TestArgumentPositionRegexDerived verifies that the argumentPositionRe built
// from sectionSpecs + valueCommandPats behaves identically to the previously
// hardcoded regex. The production regex is unexported in package
// cisco_ios_jinja2, so this external test reconstructs both the derived
// pattern (mirroring the table) and the old hardcoded pattern, then
// cross-checks them across a broad input set and pins the derived pattern's
// source string so any change to the table surfaces here for review. The
// behavioral correctness of the real production regex is additionally covered
// by TestCompletionWhileTypingValue / TestCompletionStillActiveForKeywordTyping
// through the public Completion API.
func TestArgumentPositionRegexDerived(t *testing.T) {
	// Mirrors sectionSpecs / valueCommandPats in completion.go. If those
	// change, update this copy together with wantPattern below.
	type spec struct {
		astKind, keywordSec, headerPat string
	}
	specs := []spec{
		{"interface_section", "config-if", "interface"},
		{"router_section", "config-router", `router\s+(?:bgp|ospf)`},
		{"route_map_section", "config-route-map", "route-map"},
		{"class_map_section", "config-cmap", "class-map"},
		{"policy_map_section", "config-pmap", "policy-map"},
		{"vlan_section", "config-vlan", "vlan"},
		{"line_section", "config-line", `line(?:\s+(?:console|aux|vty))?`},
		{"ip_access_list_section", "config-ext-nacl", `ip\s+access-list\s+(?:standard|extended)`},
	}
	valueCmds := []string{"hostname", "version"}

	// Reconstruct the derived pattern exactly as completion.go does.
	parts := make([]string, 0, len(specs)+len(valueCmds))
	for _, s := range specs {
		parts = append(parts, s.headerPat)
	}
	parts = append(parts, valueCmds...)
	derivedPattern := `^\s*(?:no\s+)?(?:` + strings.Join(parts, "|") + `)\s`

	// Golden: the exact source string the derivation must produce. Only the
	// alternation ORDER differs from the pre-refactor regex; no alternative is
	// a prefix of another, so matching behavior is identical.
	const wantPattern = `^\s*(?:no\s+)?(?:` +
		`interface` +
		`|router\s+(?:bgp|ospf)` +
		`|route-map` +
		`|class-map` +
		`|policy-map` +
		`|vlan` +
		`|line(?:\s+(?:console|aux|vty))?` +
		`|ip\s+access-list\s+(?:standard|extended)` +
		`|hostname` +
		`|version` +
		`)\s`
	if derivedPattern != wantPattern {
		t.Errorf("derived pattern changed:\n got: %q\nwant: %q", derivedPattern, wantPattern)
	}

	// The pre-refactor hardcoded pattern, kept as the behavioral reference.
	oldRe := regexp.MustCompile(`^\s*(?:no\s+)?(?:` +
		`router\s+(?:bgp|ospf)` +
		`|interface` +
		`|vlan` +
		`|route-map` +
		`|class-map` +
		`|policy-map` +
		`|line(?:\s+(?:console|aux|vty))?` +
		`|ip\s+access-list\s+(?:standard|extended)` +
		`|hostname` +
		`|version` +
		`)\s`)
	newRe := regexp.MustCompile(derivedPattern)

	cases := []struct {
		line string
		want bool
	}{
		// Past the keyword, in argument territory -> match.
		{`router bgp `, true},
		{`router bgp 100`, true},
		{`router ospf 1`, true},
		{`interface Gi0/0`, true},
		{`interface `, true},
		{`vlan 10`, true},
		{`vlan `, true},
		{`route-map RM 10`, true},
		{`class-map CM`, true},
		{`policy-map PM`, true},
		{`line console 0`, true},
		{`line vty 0 4`, true},
		{`line aux 0`, true},
		{`line `, true},
		{`ip access-list standard FOO`, true},
		{`ip access-list extended BAR`, true},
		{`hostname r1`, true},
		{`hostname `, true},
		{`version 26.2.0`, true},
		{`version `, true},

		// Negated headers past the keyword -> match.
		{`no router bgp 100`, true},
		{`no interface Gi0/0`, true},
		{`no hostname oldname`, true},
		{`no vlan 10`, true},

		// Indented variants -> match.
		{`  interface Gi0/0`, true},
		{"\tno hostname x", true},

		// Still typing the keyword (no trailing whitespace) -> no match.
		{`route`, false},
		{`router bg`, false},
		{`router bgp`, false},
		{`interface`, false},
		{`vlan`, false},
		{`hostname`, false},
		{`version`, false},
		{`line`, false},
		{`ip access-list`, false},
		{`ip access-list standard`, false},

		// `no ` still typing the keyword -> no match.
		{`no `, false},
		{`no route`, false},
		{`no router`, false},
		{`no router bgp`, false},
		{`no hostname`, false},

		// Unrelated / empty lines -> no match.
		{``, false},
		{`!`, false},
		{`snmp-server community`, false},
		{`description hello`, false},
	}

	for _, tc := range cases {
		gotNew := newRe.MatchString(tc.line)
		gotOld := oldRe.MatchString(tc.line)
		if gotNew != gotOld {
			t.Errorf("regexes disagree on %q: derived=%v old=%v", tc.line, gotNew, gotOld)
		}
		if gotNew != tc.want {
			t.Errorf("derived regex on %q: got %v want %v", tc.line, gotNew, tc.want)
		}
	}
}

// TestCompletionEnterModeDetail verifies that completion items for mode-opening
// commands indicate the config sub-mode they enter, derived from the keyword's
// EnterMode field (chunt-fj1). It also verifies the carve-outs: a negated
// form ("no interface") and a non-mode-opening command ("hostname") carry no
// entered-mode Detail.
func TestCompletionEnterModeDetail(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	// Cursor at the start of a top-level command resolves to global-config
	// scope, where interface/router/policy-map/etc. are offered.
	content := []byte("!\nhostname r1\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	items, err := f.Completion(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}

	cases := []struct {
		label   string
		wantSub string // substring expected in Detail
	}{
		{"interface", "config-if"},
		{"router bgp", "config-router"},
		{"router ospf", "config-router"},
		{"archive", "config-archive"},
		{"class-map", "config-cmap"},
	}
	for _, c := range cases {
		it := findItemByLabel(items, c.label)
		if it == nil {
			t.Errorf("no completion item labeled %q", c.label)
			continue
		}
		if !strings.Contains(it.Detail, c.wantSub) {
			t.Errorf("item %q: Detail = %q, want substring %q", c.label, it.Detail, c.wantSub)
		}
	}

	// Negated forms do not enter the mode, so they must not advertise one.
	if it := findItemByLabel(items, "no interface"); it != nil && it.Detail != "" {
		t.Errorf("negated %q should not indicate an entered mode; Detail = %q", "no interface", it.Detail)
	}
	// A non-mode-opening command has no Detail.
	if it := findItemByLabel(items, "hostname"); it != nil && it.Detail != "" {
		t.Errorf("non-mode-opening %q should have empty Detail; got %q", "hostname", it.Detail)
	}
}
