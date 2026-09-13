package cisco_ios_jinja2

import (
	"strings"

	"github.com/dgethings/chunt/internal/keyword"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/section"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// appendWrongSection is the per-node wrong-section check, folded into the
// single-pass tree collector (chunt-zob, see collectTreeDiagnostics in
// diagnostics.go). It emits a Hint for each command whose leading keyword is
// known to the database but not valid in the enclosing config section. This
// catches common copy-paste errors (e.g. an interface command inside a router
// section). Unknown keywords are silently skipped - only commands that ARE
// documented but in the WRONG section are flagged. For command_line nodes the
// leading keyword is resolved via longest-prefix match against the keyword DB,
// so multi-word commands like "ip access-group" are validated correctly.
//
// The collector only calls this on named command-like nodes (command_line and
// *_statement); negated_statement is descended into so its inner command is
// checked. This is byte-identical to the former runWrongSectionDiagnostics,
// including inside ERROR subtrees: unreliableSectionContext suppresses the
// hint when an ERROR is an ancestor, so descending there is a harmless no-op.
func (f *CiscoIOSFeature) appendWrongSection(diags *[]protocol.Diagnostic, n *sitter.Node, content []byte) {
	enclosingSection, _ := section.EnclosingSection(n, content)
	// B5 (chunt-mpc): when the grammar detects a section the keyword DB has
	// no keywords for (or a sub-mode more precise than the DB models),
	// collapse it to the nearest known ancestor before validating — mirroring
	// completion.go. Without this, a keyword documented for a parent section
	// is wrongly flagged in the child (and IsValidInSection's ancestry check
	// alone cannot relate a section the tree does not know to its parents).
	if len(f.keyword.InSection(enclosingSection)) == 0 {
		known := f.keyword.SectionsWithKeywords()
		enclosingSection = f.keyword.SectionTree().NearestKnown(enclosingSection, known)
	}

	kw := firstKeywordFromNode(n, content, f.keyword)
	if kw == "" {
		return
	}

	if _, ok := f.keyword.Lookup(kw); !ok {
		return
	}

	if f.keyword.IsValidInSection(kw, enclosingSection) {
		return
	}

	// Suppress the hint when the parse corrupted the section context:
	// either an ERROR node wraps this command (the parser could not build a
	// clean tree, so its section membership is an artefact of recovery) or
	// the enclosing section is missing its terminating `!` (an unterminated
	// section greedily swallows following top-level commands into itself).
	// In both cases the wrong-section flag would be misleading noise, and
	// the underlying problem is already surfaced by the syntax pass
	// (chunt-9of).
	if unreliableSectionContext(n) {
		return
	}

	validSection := f.keyword.LookupSection(kw)
	*diags = append(*diags, protocol.Diagnostic{
		Range: protocol.LineRange(
			n.StartPosition().Row,
			n.StartPosition().Column,
			n.EndPosition().Column,
		),
		Severity: protocol.SeverityHint,
		Source:   "chunt",
		Message:  kw + " is valid in " + validSection + ", not in " + enclosingSection,
	})
}

// firstKeywordFromNode resolves the leading keyword for a command-like
// node. For *_statement rules this is the anonymous first child (single-token
// keyword). For command_line nodes it performs a longest-prefix probe against
// the keyword DB: tokens [name, arg1, arg2, ...] are joined with spaces and
// probed longest-first (up to maxPrefixTokens). This lets multi-word keywords
// like "ip access-group" resolve correctly. Entries containing "(" are
// excluded from probing (documentation aliases, not matchable command text).
// Falls back to the bare name token if no multi-word prefix matches.
func firstKeywordFromNode(n *sitter.Node, content []byte, kw *keyword.Set) string {
	if n == nil || n.ChildCount() == 0 {
		return ""
	}
	first := n.Child(0)
	if first == nil {
		return ""
	}
	name := string(content[first.StartByte():first.EndByte()])

	if n.Kind() != "command_line" || kw == nil {
		return name
	}

	const maxPrefixTokens = 4
	var tokens []string
	tokens = append(tokens, name)
	for i := uint(1); i < n.ChildCount() && len(tokens) < maxPrefixTokens; i++ {
		c := n.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		tokens = append(tokens, string(content[c.StartByte():c.EndByte()]))
	}

	for i := len(tokens); i >= 1; i-- {
		candidate := strings.Join(tokens[:i], " ")
		if entry, ok := kw.Lookup(candidate); ok && !strings.Contains(entry.Keyword, "(") {
			return candidate
		}
	}

	return name
}

// walkNamed was previously defined here as a private duplicate of
// ast.WalkNamed; the diagnostic passes now share the single helper in
// internal/ast (chunt-mpc).

// unreliableSectionContext reports whether n's enclosing-section context is
// too corrupted by parse recovery to trust a wrong-section hint. It returns
// true when any ancestor of n is an ERROR node (the parser could not build a
// clean tree around it) OR when the innermost enclosing section is missing its
// terminating `eos` (an unterminated section greedily swallows following
// top-level commands into itself). In both cases a wrong-section flag would be
// misleading noise, and the syntax pass already surfaces the underlying
// problem (chunt-9of).
func unreliableSectionContext(n *sitter.Node) bool {
	var innermostSection *sitter.Node
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.IsError() {
			return true
		}
		if innermostSection == nil && section.IsKnownSectionKind(p.Kind()) {
			innermostSection = p
		}
	}
	if innermostSection != nil {
		return hasMissingEos(innermostSection)
	}
	return false
}

// hasMissingEos reports whether sectionNode has a MISSING `eos` child — i.e.
// the section ran to EOF (or into the next section) without its terminating
// `!`. eos is always a direct child of its section, so a direct-children scan
// suffices.
func hasMissingEos(sectionNode *sitter.Node) bool {
	if sectionNode == nil {
		return false
	}
	for i := uint(0); i < sectionNode.ChildCount(); i++ {
		if c := sectionNode.Child(i); c != nil && c.IsMissing() && c.Kind() == "eos" {
			return true
		}
	}
	return false
}
