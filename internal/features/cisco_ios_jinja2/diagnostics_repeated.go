package cisco_ios_jinja2

import (
	"fmt"
	"strings"

	"github.com/dgethings/chunt/internal/ast"
	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/keyword"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/section"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Repeated-command and redefined-setting warnings (chunt-4ih).
//
// These flag valid-but-smelly config practice as WARNINGS, never Errors:
// repeated definitions produce working config, but are usually unintended.
//
// Two rules:
//
//   - duplicate-command (generic): identical command text appearing 2+ times
//     in one section. An exact repeat of a whole line is almost always a
//     copy-paste error — duplicate static routes, neighbors, ACL entries,
//     descriptions — so no curated registry is needed; every repeat after
//     the first is flagged. Non-identical variants (e.g. floating static
//     routes: same prefix, different next-hop/distance) are exempt because
//     the text differs.
//
//   - redefined-setting (curated): scalar settings whose later definition
//     REPLACES the earlier one — a hostname value replacement, and multiple
//     primary `ip address` definitions on one interface (later primaries
//     replace earlier ones; `secondary` lines are additive and exempt,
//     identical lines fall to duplicate-command instead).
//
// Scope: one section (or the top-level config) — the same command in
// different sections is fine (one address per interface). Negated
// statements (`no ...`) CLEAR the tracked state for the command they
// negate: remove-then-re-add is a normal pattern, so the re-add is not a
// duplicate (and `no hostname` / `no ip address ...` reset the scalar
// trackers). Commands inside Jinja blocks (`{% if %}` branches) are
// excluded in v1: mutually exclusive branches legitimately repeat a
// command, so only a scope's DIRECT command children are examined.
//
// The hostname rule stays out of the tier-2 duplicate-hostname pass's way:
// SAME-value hostname duplicates keep their existing `duplicate-hostname`
// Warning (with symbol-table relatedInformation); only VALUE REPLACEMENTS
// are flagged here. Both new codes carry relatedInformation back to the
// first definition, mirroring the duplicate-definition pass.

// runRepeatedCommandDiagnostics scans every scope (the root config plus each
// section node) for repeated command text and curated scalar redefinitions.
// Tier 1: tree + keyword DB only, no symbol table.
func (f *CiscoIOSFeature) runRepeatedCommandDiagnostics(doc *document.Document, tree *sitter.Tree) []protocol.Diagnostic {
	if tree == nil || tree.RootNode() == nil {
		return nil
	}
	content := doc.Content
	root := tree.RootNode()
	var diags []protocol.Diagnostic
	f.appendRepeatedScope(&diags, doc, root, content)
	ast.WalkNamed(root, func(n *sitter.Node) bool {
		if n != root && section.IsKnownSectionKind(n.Kind()) {
			f.appendRepeatedScope(&diags, doc, n, content)
		}
		return true
	})
	return diags
}

// appendRepeatedScope checks one scope's direct command-like children
// (command_line and *_statement, excluding negated_statement) for the two
// rules above. Nested sections inside the scope are separate scopes and are
// skipped here; their contents are visited when the walk reaches them.
func (f *CiscoIOSFeature) appendRepeatedScope(diags *[]protocol.Diagnostic, doc *document.Document, scope *sitter.Node, content []byte) {
	seen := map[string]*sitter.Node{} // trimmed command text -> first node
	var hostNode *sitter.Node         // first hostname in this scope
	hostVal := ""
	var primaryNode *sitter.Node // first non-secondary ip address in this scope

	for i := uint(0); i < scope.ChildCount(); i++ {
		c := scope.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		kind := c.Kind()
		if kind == "negated_statement" {
			// The negation removes the command's effect: clear the recorded
			// state for what it negates, so a later re-add is not a duplicate.
			f.clearRepeatedState(seen, &hostNode, &hostVal, &primaryNode, c, content)
			continue
		}
		if kind != "command_line" && !strings.HasSuffix(kind, "_statement") {
			continue // sections, headers, eos, jinja blocks (v1)
		}

		text := strings.TrimSpace(string(content[c.StartByte():c.EndByte()]))
		toks := commandTokens(c, content)
		kw := repeatedKeyword(c, content, f.keyword)

		// Same-value hostname redefinition is owned by the tier-2
		// duplicate-hostname pass — emit nothing here for it.
		if kw == "hostname" && hostNode != nil {
			if v := lastToken(toks); v == hostVal {
				continue
			}
		}

		// Generic exact duplicate: identical command text already seen in
		// this scope. Emitted here (and no curated check) so an identical
		// primary `ip address` pair yields exactly one warning.
		if first, dup := seen[text]; dup {
			*diags = append(*diags, repeatedDiag(doc, c, first, "duplicate-command",
				fmt.Sprintf("duplicate command %q; already defined earlier in this section", text)))
			continue
		}
		seen[text] = c

		switch kw {
		case "hostname":
			if v := lastToken(toks); hostNode != nil && v != hostVal {
				*diags = append(*diags, repeatedDiag(doc, c, hostNode, "redefined-setting",
					fmt.Sprintf("hostname %q replaces %q; only the last definition takes effect", v, hostVal)))
			} else if hostNode == nil {
				hostNode, hostVal = c, v
			}
		case "ip address":
			if hasToken(toks, "secondary") {
				continue // additive secondaries are exempt
			}
			if primaryNode != nil {
				*diags = append(*diags, repeatedDiag(doc, c, primaryNode, "redefined-setting",
					fmt.Sprintf("multiple primary \"ip address\" definitions on this interface; this replaces %q", argTail(primaryNode, content, kw))))
			} else {
				primaryNode = c
			}
		}
	}
}

// clearRepeatedState processes a negated_statement: the inner command's
// tracked state is removed so a subsequent positive definition is not
// flagged. The generic duplicate map key is the inner command's trimmed
// text; the hostname and primary-address trackers reset on their no-forms.
func (f *CiscoIOSFeature) clearRepeatedState(seen map[string]*sitter.Node, hostNode **sitter.Node, hostVal *string, primaryNode **sitter.Node, neg *sitter.Node, content []byte) {
	inner := firstCommandChild(neg)
	if inner == nil {
		return
	}
	delete(seen, strings.TrimSpace(string(content[inner.StartByte():inner.EndByte()])))
	switch repeatedKeyword(inner, content, f.keyword) {
	case "hostname":
		*hostNode, *hostVal = nil, ""
	case "ip address":
		if !hasToken(commandTokens(inner, content), "secondary") {
			*primaryNode = nil
		}
	}
}

// firstCommandChild returns the negated_statement's inner command-like child
// (command_line or *_statement), or nil.
func firstCommandChild(neg *sitter.Node) *sitter.Node {
	for i := uint(0); i < neg.ChildCount(); i++ {
		c := neg.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		k := c.Kind()
		if k == "command_line" || (k != "negated_statement" && strings.HasSuffix(k, "_statement")) {
			return c
		}
	}
	return nil
}

// repeatedKeyword resolves the command keyword for a command-like node:
// command_line via the deepest longest-prefix probe; dedicated *_statement
// kinds via their node kind (hostname_statement -> hostname), since their
// named children omit the keyword text.
func repeatedKeyword(n *sitter.Node, content []byte, kw *keyword.Set) string {
	if n.Kind() == "command_line" {
		return resolveCommandKeyword(n, content, kw)
	}
	return commandFromKind(n.Kind())
}

// lastToken returns the text of the last token (the hostname VALUE for
// hostname_statement/hostname command lines), or "".
func lastToken(toks []docToken) string {
	if len(toks) == 0 {
		return ""
	}
	return toks[len(toks)-1].text
}

// hasToken reports whether any token equals s (case-insensitive) — used to
// detect the `secondary` keyword on ip address lines.
func hasToken(toks []docToken, s string) bool {
	for _, t := range toks {
		if strings.EqualFold(t.text, s) {
			return true
		}
	}
	return false
}

// argTail renders the argument tokens after the leading kw words of a
// command (e.g. "10.0.0.1 255.255.255.0" for `ip address 10.0.0.1
// 255.255.255.0` with kw "ip address") for use in redefined-setting
// messages.
func argTail(n *sitter.Node, content []byte, kw string) string {
	toks := commandTokens(n, content)
	kwWords := len(strings.Fields(kw))
	if kwWords >= len(toks) {
		return ""
	}
	parts := make([]string, 0, len(toks)-kwWords)
	for _, t := range toks[kwWords:] {
		parts = append(parts, t.text)
	}
	return strings.Join(parts, " ")
}

// repeatedDiag builds the shared Warning shape: anchored on the repeated
// command, relatedInformation back to the first definition.
func repeatedDiag(doc *document.Document, n, first *sitter.Node, code, msg string) protocol.Diagnostic {
	return protocol.Diagnostic{
		Range:    protocol.LineRange(n.StartPosition().Row, n.StartPosition().Column, n.EndPosition().Column),
		Severity: protocol.SeverityWarning,
		Source:   "chunt",
		Code:     code,
		Message:  msg,
		RelatedInformation: []protocol.DiagnosticRelatedInformation{{
			Location: protocol.Location{
				URI:   doc.URI,
				Range: protocol.LineRange(first.StartPosition().Row, first.StartPosition().Column, first.EndPosition().Column),
			},
			Message: "first defined here",
		}},
	}
}
