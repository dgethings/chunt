package cisco_ios_jinja2

import (
	"fmt"
	"strings"

	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/section"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// appendSyntaxDiag is the per-node syntax/missing check, folded into the
// single-pass tree collector (chunt-zob, see collectTreeDiagnostics in
// diagnostics.go). It surfaces the two kinds of recovery nodes the parser can
// emit:
//
//   - ERROR nodes — tokens that could not be incorporated into any rule. These
//     are reported as SeverityError anchored on the offending span.
//   - MISSING nodes — zero-width markers the parser inserts so a rule can
//     complete despite a missing required token (e.g. an unterminated `{{`
//     yields a MISSING `}}`, and a section without its terminating `!` yields a
//     MISSING `eos`). MISSING nodes are reported as SeverityError with a
//     message naming what is missing, EXCEPT for a MISSING `eos` inside a
//     section, which is a common, low-severity condition downgraded to
//     SeverityWarning and anchored on the section header.
//
// The caller (collectTreeDiags) invokes this only when inError is false — i.e.
// n is NOT inside an ERROR subtree. That replaces the old driver's "return
// false on ERROR to skip its subtree" descent rule: the merged walk still
// descends into ERROR subtrees (so the command checks can run on commands
// recovery swallowed), but suppresses the syntax re-report inside them.
// Without this, descending would re-report the recovered tokens the ERROR
// wrapped. On clean files there are no ERROR/MISSING nodes, so this is a pair
// of false boolean checks per node — a no-op.
func (f *CiscoIOSFeature) appendSyntaxDiag(diags *[]protocol.Diagnostic, n *sitter.Node, content []byte) {
	switch {
	case n.IsError():
		*diags = append(*diags, protocol.Diagnostic{
			Range:    nodeRangeOnStartRow(n, content),
			Severity: protocol.SeverityError,
			Source:   "chunt",
			Code:     "syntax-error",
			Message:  fmt.Sprintf("syntax error near %q", firstLine(content, n)),
		})
		// Error recovery can swallow an unterminated Jinja delimiter (`{{`,
		// `{%`, `{#`) as a bare anonymous token inside the ERROR node. There
		// is then no `output`/`statement`/`comment` node and no MISSING
		// closer for this pass to report, so the user sees no hint at the
		// real error line — only the generic ERROR anchored earlier
		// (chunt-9of). Recover that hint by stack-matching the Jinja
		// openers/closers among the ERROR node's tokens and emitting a
		// missing-closer diagnostic for each unmatched opener.
		*diags = append(*diags, unclosedJinjaDiagnostics(n, content)...)
	case n.IsMissing():
		*diags = append(*diags, missingDiagnostic(n, content))
	}
}

// missingDiagnostic builds the diagnostic for a MISSING node. A MISSING eos
// whose parent is a section (the only place eos is required) is downgraded to
// a Warning anchored on the section header; every other missing token is an
// Error anchored at the MISSING node's position (extended to end-of-line so
// the editor has something to render).
func missingDiagnostic(n *sitter.Node, content []byte) protocol.Diagnostic {
	if n.Kind() == "eos" {
		if parent := n.Parent(); parent != nil {
			if section.IsSectionHeader(parent) {
				if hdr := sectionHeaderNode(parent); hdr != nil {
					return protocol.Diagnostic{
						Range:    protocol.LineRange(hdr.StartPosition().Row, hdr.StartPosition().Column, hdr.EndPosition().Column),
						Severity: protocol.SeverityWarning,
						Source:   "chunt",
						Code:     "missing-eos",
						Message:  fmt.Sprintf("section %q is missing its terminating %q", nodeText(hdr, content), "!"),
					}
				}
			}
		}
	}
	kind := n.Kind()
	if kind == "" {
		kind = "token"
	}
	return protocol.Diagnostic{
		Range:    nodeRangeOnStartRow(n, content),
		Severity: protocol.SeverityError,
		Source:   "chunt",
		Code:     "missing-" + kind,
		Message:  fmt.Sprintf("missing %q", kind),
	}
}

// jinjaOpeners maps each Jinja opener token kind to the matching closer kind.
// The keys/values are the literal anonymous punctuation tree-sitter emits
// (verified against the grammar: `{{`/`}}`, `{%`/`%}`, `{#`/`#}`).
var jinjaOpeners = map[string]string{
	"{{": "}}",
	"{%": "%}",
	"{#": "#}",
}

// unclosedJinjaDiagnostics recovers missing-closer diagnostics for Jinja
// delimiters swallowed inside an ERROR node. It collects every anonymous
// opener/closer token under n (in source order) and stack-matches them per
// delimiter type; each opener left on a stack at the end is an unclosed
// delimiter and earns a missing-closer Error anchored on its line. MISSING
// tokens are ignored (neither open nor close) so a partial `output` node with
// a MISSING `}}` inside an ERROR is still flagged via its real `{{` opener.
//
// This mirrors the diagnostic an unclosed `{{` produces in isolation (a
// MISSING `}}` inside a clean `output` node) but handles the case where error
// recovery wraps the opener so no such node exists (chunt-9of).
func unclosedJinjaDiagnostics(n *sitter.Node, content []byte) []protocol.Diagnostic {
	// Per-closer stacks of currently-open, unmatched opener nodes.
	open := make(map[string][]*sitter.Node)
	walkAll(n, func(c *sitter.Node) bool {
		if c == n {
			return true
		}
		if c.IsMissing() {
			return true // a MISSING token balances nothing; skip it.
		}
		if c.IsNamed() {
			// Named nodes (e.g. a recovered `output` `{{ x }}`) are not
			// delimiters themselves, but descend so their anonymous
			// delimiter children are stack-matched below.
			return true
		}
		if closer, isOpen := jinjaOpeners[c.Kind()]; isOpen {
			open[closer] = append(open[closer], c)
			return true
		}
		// A closer ("}}", "%}", "#}") matches the most recent opener of its
		// own kind (LIFO); a stray closer with no openers is ignored.
		if stack := open[c.Kind()]; len(stack) > 0 {
			open[c.Kind()] = stack[:len(stack)-1]
		}
		return true
	})
	var diags []protocol.Diagnostic
	for closer, openers := range open {
		for _, op := range openers {
			diags = append(diags, protocol.Diagnostic{
				Range:    nodeRangeOnStartRow(op, content),
				Severity: protocol.SeverityError,
				Source:   "chunt",
				Code:     "missing-" + closer,
				Message:  fmt.Sprintf("missing %q", closer),
			})
		}
	}
	return diags
}

// nodeRangeOnStartRow returns a single-line range anchored on the node's start
// row. For a normal single-line node this is the node's own span; for a
// multi-line node (an ERROR wrapping several lines) or a zero-width node (a
// MISSING token) the range is extended to the end of the start row so the
// editor always has a non-empty region to underline.
func nodeRangeOnStartRow(n *sitter.Node, content []byte) protocol.Range {
	row := n.StartPosition().Row
	startCol := n.StartPosition().Column
	endCol := n.EndPosition().Column
	if row != n.EndPosition().Row || n.StartByte() == n.EndByte() {
		lineStart := n.StartByte() - startCol
		end := lineStart
		for end < uint(len(content)) && content[end] != '\n' {
			end++
		}
		endCol = end - lineStart
	}
	return protocol.LineRange(row, startCol, endCol)
}

// sectionHeaderNode returns the first named child of a section node, which by
// the grammar is always its *_header node (interface_header, router_header,
// ...). Returns nil if the section has no named children.
func sectionHeaderNode(section *sitter.Node) *sitter.Node {
	if section == nil || section.NamedChildCount() == 0 {
		return nil
	}
	return section.NamedChild(0)
}

// nodeText returns the source slice spanned by n.
func nodeText(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	return string(content[n.StartByte():n.EndByte()])
}

// firstLine returns the first line of the node's source text, trimmed of
// surrounding whitespace and truncated so an ERROR spanning many lines does
// not produce a huge diagnostic message.
func firstLine(content []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	s := string(content[n.StartByte():n.EndByte()])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	const maxSnippet = 40
	if len(s) <= maxSnippet {
		return s
	}
	return s[:maxSnippet-1] + "…"
}

// walkAll depth-first traverses EVERY child of n (named and anonymous),
// invoking visit on each. This differs from walkNamed: ERROR nodes and
// MISSING anonymous tokens (e.g. a MISSING `}}`) are not named children, so a
// named-only walk would skip them. If visit returns false, the subtree under
// that node is skipped.
func walkAll(n *sitter.Node, visit func(*sitter.Node) bool) {
	if n == nil {
		return
	}
	if !visit(n) {
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		walkAll(n.Child(i), visit)
	}
}
