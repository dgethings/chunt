package cisco_ios_jinja2

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/dgethings/chunt/internal/ast"
	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/keyword"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/section"
)

// headerKeywordPats are regex fragments matching the keyword prefix of each
// section header. They power argumentPositionRe (the cursor-past-keyword
// detector below) and are a completion-specific concern: the canonical
// AST-kind -> keyword.Section mapping now lives in internal/section
// (chunt-mpc), so this list is NOT a second source of truth for section
// identity — only for the keyword-text shapes the argument-position regex must
// recognize.
var headerKeywordPats = []string{
	"interface",
	`router\s+(?:bgp|ospf)`,
	"address-family",
	"route-map",
	"class-map",
	"policy-map",
	"vlan",
	`line(?:\s+(?:console|aux|vty))?`,
	`ip\s+access-list\s+(?:standard|extended)`,
}

// valueCommandPats are non-section commands that take a single value argument
// (so completion should be suppressed past the keyword). These have no
// corresponding section node in the grammar.
var valueCommandPats = []string{
	"hostname",
	"version",
}

// of a section header or value-taking command. Used by inArgumentPosition to
// suppress keyword completion while the user is typing a value into a
// placeholder. Built once from sectionSpecs and valueCommandPats at package
// init.
//
// The pattern is structured as:
//
//	^\s*                  -- optional leading indentation
//	(no\s+)?              -- optional `no ` negation prefix
//	(<keyword>)           -- a known section header / value-taking command
//	\s                    -- at least one whitespace separator after the keyword
//
// The trailing `\s` is what discriminates "still typing the keyword" (e.g.
// `router bg`) from "past the keyword, in argument territory" (e.g.
// `router bgp ` or `router bgp 100`). MatchString only requires the regex to
// match a prefix of the input, so once the keyword + a single whitespace
// character appears anywhere in the line-up-to-cursor the match succeeds
// regardless of what follows.
//
// The `no <kw>` group is optional, so the same pattern covers both the
// positive form (`hostname <value>`) and the negated form
// (`no hostname <value>`). The carve-out for "user is still typing the
// keyword after `no`" (e.g. `no route`) falls out naturally: the regex
// requires the full keyword to appear before the trailing `\s`, so a partial
// keyword like `route` does not match `router\s+(?:bgp|ospf)`.
var argumentPositionRe = func() *regexp.Regexp {
	parts := append([]string{}, headerKeywordPats...)
	parts = append(parts, valueCommandPats...)
	pattern := `^\s*(?:no\s+)?(?:` + strings.Join(parts, "|") + `)\s`
	return regexp.MustCompile(pattern)
}()

func (f *CiscoIOSFeature) Completion(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.CompletionItem, error) {
	tree := f.trees[doc.URI]
	l := slog.With("uri", doc.URI, "language", doc.LanguageID, "message", "completion")
	if tree == nil {
		l.Error("cannot find tree")
		return nil, nil
	}
	slog.Debug("current position", "LINE", pos.Line, "COLOMN", pos.Character)

	node := ast.FindNodeAtPosition(tree.RootNode(), pos.Line, pos.Character)
	if node == nil {
		l.Error("cannot find section", "line", pos.Line, "column", pos.Character)
		return nil, nil
	}
	l.Debug("where am i?", "node", node.GrammarName(), "kind", node.Kind(), "line", pos.Line, "char", pos.Character)

	switch node.Kind() {
	case "value", "text", "eos", "comment", "ios_comment":
		return nil, nil
	}

	// Line-based safety net: catches the cases the AST-based check above
	// misses. Two scenarios in particular:
	//
	//  1. Incomplete section headers whose value field the grammar now
	//     models as `optional(...)` — the resulting `*_header` node ends
	//     at the keyword, so a cursor sitting in the empty value slot
	//     resolves to the parent (or to config) rather than to a `value`
	//     node, slipping past the early-return above. Example: `router bgp `
	//     with the ASN not yet typed.
	//
	//  2. The boundary case — cursor sits exactly at a value token's end,
	//     so ast.FindNodeAtPosition returns the value's parent (the header)
	//     rather than the value itself. Example: `router bgp 100|`.
	//
	// The check inspects the line content up to the cursor and suppresses
	// when the cursor sits past the keyword prefix of any section header
	// or value-taking command. The `no <kw>` carve-out keeps completion
	// active while the user is still typing the negated keyword (e.g.
	// `no router bgp`), since the keyword database carries `no <kw>`
	// snippets whose filter text begins with `no `.
	if inArgumentPosition(doc, pos) {
		return nil, nil
	}

	// Resolve the innermost enclosing *_section node to its keyword.Section
	// value. If the detected section has no keywords in the data, fall back
	// to the nearest ancestor section that does (via SectionTree.NearestKnown).
	rawSection, _ := section.EnclosingSection(node, doc.Content)

	// If the grammar detected a section that has no keywords in the data
	// (e.g. config-ext-nacl when only config-std-nacl keywords exist, or a
	// section the grammar models but the scraper didn't capture), fall back
	// to the nearest ancestor section that has keywords.
	section := rawSection
	if len(f.keyword.InSection(rawSection)) == 0 {
		known := f.keyword.SectionsWithKeywords()
		section = f.keyword.SectionTree().NearestKnown(rawSection, known)
	}

	items := createItems(f.keyword.InSection(section))
	return items, nil
}

// placeholderDefaultRe matches LSP snippet placeholders of the form ${N:default}
// and strips the default text, leaving an empty tabstop ${N}.
//
// Neovim's vim.snippet selects non-empty tabstops via a key sequence that some
// completion engines (notably blink.cmp on Neovim 0.12) leave in INSERT mode
// rather than SELECT mode, so typing at the placeholder inserts before the
// default text instead of replacing it (e.g. `hostname ${1:name}` yielded
// `hostname r1name` instead of `hostname r1`). Empty tabstops use a different
// code path that drops the cursor in INSERT mode at the tabstop position, so
// typing inserts at the correct spot.
var placeholderDefaultRe = regexp.MustCompile(`\$\{(\d+):[^}]*\}`)

// stripPlaceholderDefaults rewrites a snippet by removing the default text from
// every ${N:default} placeholder while leaving the tabstop itself intact.
func stripPlaceholderDefaults(snippet string) string {
	return placeholderDefaultRe.ReplaceAllString(snippet, "${$1}")
}

func createItems(kws keyword.Keywords) []protocol.CompletionItem {
	items := []protocol.CompletionItem{}
	format := protocol.InsertTextFormatSnippet
	kind := protocol.CompletionItemKindKeyword
	seen := map[string]bool{}
	for _, kw := range kws {
		for _, s := range kw.Snippets {
			label := snippetLabel(s)
			if seen[label] {
				continue
			}
			seen[label] = true
			snippet := stripPlaceholderDefaults(s)
			filterText := label
			var doc any
			if kw.Description.Value != "" {
				doc = protocol.MarkupContent{
					Kind:  kw.Description.Format,
					Value: kw.Description.Value,
				}
			}
			// Mode-opening commands carry EnterMode (the sub-mode they open, e.g.
			// interface -> config-if). Surface it as the completion Detail so the
			// candidate indicates where it lands. Negated forms ("no <kw>") do
			// not enter the mode, so they are excluded (chunt-fj1).
			var detail string
			if kw.EnterMode != "" && !strings.HasPrefix(s, "no ") {
				detail = "Enters " + kw.EnterMode + " mode"
			}
			items = append(items, protocol.CompletionItem{
				Label:            label,
				Detail:           detail,
				Documentation:    doc,
				FilterText:       &filterText,
				InsertText:       &snippet,
				InsertTextFormat: &format,
				Kind:             &kind,
			})
		}
	}
	return items
}

// snippetLabel derives a completion item's label from one snippet by taking
// the literal command text that precedes the first ${N:...} placeholder and
// trimming trailing whitespace. Snippets that share a keyword but differ in
// command form — e.g. `activation-character ${1:n}` and `no activation-character`
// — thus yield distinct labels (`activation-character` and
// `no activation-character`) instead of collapsing onto the same one.
func snippetLabel(snippet string) string {
	if i := strings.Index(snippet, "${"); i >= 0 {
		return strings.TrimRight(snippet[:i], " \t")
	}
	return snippet
}

// inArgumentPosition reports whether the cursor at pos sits past the keyword
// prefix of doc.Line(pos.Line) — i.e., the user is typing a value into a
// placeholder rather than entering a new keyword. Returns false when the
// cursor is still inside the keyword being typed (including the `no <kw>`
// carve-out) or when the line is empty.
func inArgumentPosition(doc *document.Document, pos protocol.Position) bool {
	line := lineUpToCharacter(doc, pos.Line, pos.Character)
	if line == "" {
		return false
	}
	return argumentPositionRe.MatchString(line)
}

// lineUpToCharacter returns the byte slice of doc.Content covering line
// `lineNum` from column 0 up to (but not including) column `char`. If `char`
// is past the end of the line, the whole line is returned. Newline bytes are
// excluded. Returns "" if the line does not exist.
//
// document.Document.Lines is documented as not populated by document.New, so
// we walk Content directly.
func lineUpToCharacter(doc *document.Document, lineNum, char uint) string {
	start := 0
	curLine := uint(0)
	lineEnd := -1
	for i, b := range doc.Content {
		if b == '\n' {
			if curLine == lineNum {
				lineEnd = i
				break
			}
			curLine++
			start = i + 1
		}
	}
	if lineEnd < 0 {
		// No newline terminated this line — either it's the last line of
		// the file (curLine == lineNum) or the line doesn't exist.
		if curLine != lineNum {
			return ""
		}
		lineEnd = len(doc.Content)
	}
	if int(char) < lineEnd-start {
		return string(doc.Content[start : start+int(char)])
	}
	return string(doc.Content[start:lineEnd])
}
