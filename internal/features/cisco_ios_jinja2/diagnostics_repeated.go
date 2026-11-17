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

// Repeated-command and redefined-setting diagnostics (chunt-4ih).
//
// These flag conflicting config practice:
//
//   - duplicate-command (Warning): identical command text appearing 2+ times
//     in one section. An exact repeat of a whole line is almost always a
//     copy-paste error — duplicate static routes, neighbors, ACL entries,
//     descriptions — so no curated registry is needed; every repeat after
//     the first is flagged. Non-identical variants (e.g. floating static
//     routes: same prefix, different next-hop/distance) are exempt because
//     the text differs.
//
//   - redefined-setting (Error): scalar settings whose later definition
//     REPLACES the earlier one — a hostname value replacement, multiple
//     primary `ip address` definitions on one interface, conflicting
//     `router-id` values in a routing process. The last definition wins at
//     merge time, so a conflict is almost never what the author intended;
//     these are Errors whether the two definitions share a file or live in
//     different template files of the same device directory.
//
// Scope: one section (or the top-level config) — the same command in
// different sections is fine (one address per interface). In SINGLE-FILE
// mode a scope is a section node in one document; in DIRECTORY mode
// (workspace.go) a scope is the LOGICAL section: every file's instance of
// e.g. `router bgp 65001` merged in (filename, position) order, plus the
// root scopes of all files merged into one. Negated statements (`no ...`)
// CLEAR the tracked state for the command they negate: remove-then-re-add
// is a normal pattern, so the re-add is not a duplicate (and `no hostname`
// / `no ip address ...` / `no router-id` reset the scalar trackers).
// Commands inside Jinja blocks (`{% if %}` branches) are excluded in v1:
// mutually exclusive branches legitimately repeat a command, so only a
// scope's DIRECT command children are examined.
//
// The hostname rule stays out of the tier-2 duplicate-hostname pass's way:
// SAME-value hostname duplicates keep their existing `duplicate-hostname`
// Warning (with symbol-table relatedInformation); only VALUE REPLACEMENTS
// are flagged here. The duplicate-command code carries relatedInformation
// back to the first definition, mirroring the duplicate-definition pass.

// runRepeatedCommandDiagnostics scans every scope (the root config plus each
// section node) for repeated command text and curated scalar redefinitions.
// Tier 1: tree + keyword DB only, no symbol table.
func (f *CiscoIOSFeature) runRepeatedCommandDiagnostics(doc *document.Document, tree *sitter.Tree) []protocol.Diagnostic {
	if tree == nil || tree.RootNode() == nil {
		return nil
	}
	root := tree.RootNode()
	var diags []protocol.Diagnostic
	f.appendRepeatedScope(&diags, doc, root)
	ast.WalkNamed(root, func(n *sitter.Node) bool {
		if n != root && section.IsKnownSectionKind(n.Kind()) {
			f.appendRepeatedScope(&diags, doc, n)
		}
		return true
	})
	return diags
}

// appendRepeatedScope checks one scope's direct command-like children
// (command_line and *_statement, excluding negated_statement) for the two
// rules above. Nested sections inside the scope are separate scopes and are
// skipped here; their contents are visited when the walk reaches them.
func (f *CiscoIOSFeature) appendRepeatedScope(diags *[]protocol.Diagnostic, doc *document.Document, scope *sitter.Node) {
	t := newRepeatedTracker(f.keyword, false)
	f.feedScope(t, diags, doc, scope)
}

// feedScope feeds a tracker every direct command-like child of scope (in
// document order), appending the diagnostics it emits. Shared by the
// single-file pass (one scope) and the workspace pass (several section
// instances of one logical scope, potentially from different documents).
func (f *CiscoIOSFeature) feedScope(t *repeatedTracker, diags *[]protocol.Diagnostic, doc *document.Document, scope *sitter.Node) {
	for i := uint(0); i < scope.ChildCount(); i++ {
		c := scope.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		kind := c.Kind()
		if kind == "negated_statement" {
			// The negation removes the command's effect: clear the recorded
			// state for what it negates, so a later re-add is not a duplicate.
			t.clearNegated(scopeCmd{node: c, doc: doc})
			continue
		}
		if kind != "command_line" && !strings.HasSuffix(kind, "_statement") {
			continue // sections, headers, eos, jinja blocks (v1)
		}
		*diags = append(*diags, t.feed(scopeCmd{node: c, doc: doc})...)
	}
}

// scopeCmd is one command-like occurrence in a scope, paired with the
// document it was parsed from. The single-file pass always feeds one
// document; the workspace pass feeds a merged stream where consecutive
// commands may come from different files, so every emitted diagnostic must
// be anchored (and its relatedInformation targeted) via the occurrence's
// own document.
type scopeCmd struct {
	node *sitter.Node
	doc  *document.Document
}

func (c scopeCmd) content() []byte { return c.doc.Content }

func (c scopeCmd) text() string {
	return strings.TrimSpace(string(c.content()[c.node.StartByte():c.node.EndByte()]))
}

// repeatedTracker carries the dedup state for ONE logical scope: the
// seen-command index plus the curated scalar trackers. feed() applies the
// rules to one command occurrence and returns the diagnostics it produces;
// clearNegated() drops state for a `no ...` line.
//
// crossFileOnly selects workspace mode: findings whose first definition is
// in the SAME document are suppressed (the single-file pass above already
// reports those), so a conflict is reported exactly once — by whichever
// pass owns its file boundary.
type repeatedTracker struct {
	kw *keyword.Set

	seen map[string]scopeCmd // trimmed command text -> first occurrence

	host      scopeCmd
	hasHost   bool
	hostVal   string
	primary   scopeCmd // first non-secondary ip address in this scope
	hasPrimry bool
	routerID  scopeCmd // first router-id in this scope
	hasRID    bool

	// conflicts records cross-file scalar conflicts (workspace mode) so
	// the workspace pass can anchor a companion superseded-setting Warning
	// on each FIRST definition afterwards — its file must show its
	// participation too, but the Warning cannot be emitted at feed time
	// (the first file was fed before the conflict existed).
	conflicts []settingConflict

	crossFileOnly bool
}

func newRepeatedTracker(kw *keyword.Set, crossFileOnly bool) *repeatedTracker {
	return &repeatedTracker{
		kw:            kw,
		seen:          make(map[string]scopeCmd),
		crossFileOnly: crossFileOnly,
	}
}

// feed applies the duplicate-command and redefined-setting rules to one
// command occurrence, updating the tracker state.
func (t *repeatedTracker) feed(c scopeCmd) []protocol.Diagnostic {
	content := c.content()
	toks := commandTokens(c.node, content)
	kw := repeatedKeyword(c.node, content, t.kw)

	// Same-value hostname redefinition is owned by the tier-2
	// duplicate-hostname pass — emit nothing here for it. That ownership is
	// per document: a same-value hostname repeated ACROSS files (workspace
	// mode) has no tier-2 owner, so it falls through to duplicate-command.
	if kw == "hostname" && t.hasHost {
		if v := lastToken(toks); v == t.hostVal && t.host.doc.URI == c.doc.URI {
			return nil
		}
	}

	// Generic exact duplicate: identical command text already seen in this
	// scope. Emitted here (and no curated check) so an identical primary
	// `ip address` pair yields exactly one warning.
	if first, dup := t.seen[c.text()]; dup {
		if t.crossFileOnly && first.doc.URI == c.doc.URI {
			return nil // same-file duplicate: owned by the single-file pass
		}
		return []protocol.Diagnostic{repeatedDiag(c, first, "duplicate-command",
			fmt.Sprintf("duplicate command %q; already defined earlier in this section", c.text()))}
	}
	t.seen[c.text()] = c

	return t.scalarRedefinition(kw, c, toks)
}

// scalarRedefinition applies the curated redefined-setting rule for kw at
// command occurrence c (whose tokens are toks), updating the scalar
// trackers. Returns the diagnostic to emit, or nil.
func (t *repeatedTracker) scalarRedefinition(kw string, c scopeCmd, toks []docToken) []protocol.Diagnostic {
	switch kw {
	case "hostname":
		v := lastToken(toks)
		if t.hasHost && v != t.hostVal {
			return t.emitRedefined(c, t.host,
				fmt.Sprintf("hostname %q replaces %q; only the last definition takes effect", v, t.hostVal),
				"hostname", t.hostVal, v)
		}
		if !t.hasHost {
			t.host, t.hostVal, t.hasHost = c, v, true
		}
	case "ip address":
		if hasToken(toks, "secondary") {
			return nil // additive secondaries are exempt
		}
		if t.hasPrimry {
			return t.emitRedefined(c, t.primary,
				fmt.Sprintf("multiple primary \"ip address\" definitions on this interface; this replaces %q", argTail(t.kw, t.primary)),
				"ip address", argTail(t.kw, t.primary), argTail(t.kw, c))
		}
		t.primary, t.hasPrimry = c, true
	case "router-id", "bgp router-id":
		v := lastToken(toks)
		if t.hasRID && v != t.routerIDVal() {
			return t.emitRedefined(c, t.routerID,
				fmt.Sprintf("router-id %q replaces %q; only the last definition takes effect", v, t.routerIDVal()),
				"router-id", t.routerIDVal(), v)
		}
		if !t.hasRID {
			t.routerID, t.hasRID = c, true
		}
	}
	return nil
}

// emitRedefined emits the redefined-setting Error for a scalar conflict
// (msg is the established per-setting wording) and, in workspace mode,
// records the pair for the companion pass. Same-file findings in workspace
// mode are suppressed — the single-file pass owns those.
func (t *repeatedTracker) emitRedefined(c, first scopeCmd, msg, setting, firstVal, laterVal string) []protocol.Diagnostic {
	if t.crossFileOnly {
		if first.doc.URI == c.doc.URI {
			return nil // same-file conflict: owned by the single-file pass
		}
		t.conflicts = append(t.conflicts, settingConflict{
			first:    first,
			later:    c,
			setting:  setting,
			firstVal: firstVal,
			laterVal: laterVal,
		})
	}
	return []protocol.Diagnostic{repeatedDiag(c, first, "redefined-setting", msg)}
}

// settingConflict pairs the first and later definitions of one cross-file
// scalar conflict, with the human-readable setting label and both values.
type settingConflict struct {
	first, later       scopeCmd
	setting            string // "hostname", "ip address", "router-id"
	firstVal, laterVal string
}

// companion builds the superseded-setting Warning for a conflict's FIRST
// definition: anchored on it, naming the replacing value and the file it
// was defined later in, with relatedInformation forward to that later
// definition. Paired with the Error on the later definition, both files
// show their participation in the conflict.
func (cf settingConflict) companion() protocol.Diagnostic {
	return protocol.Diagnostic{
		Range:    protocol.LineRange(cf.first.node.StartPosition().Row, cf.first.node.StartPosition().Column, cf.first.node.EndPosition().Column),
		Severity: protocol.SeverityWarning,
		Source:   "chunt",
		Code:     "superseded-setting",
		Message:  fmt.Sprintf("%s %q is superseded by %q defined later in %s", cf.setting, cf.firstVal, cf.laterVal, baseName(cf.later.doc.URI)),
		RelatedInformation: []protocol.DiagnosticRelatedInformation{{
			Location: protocol.Location{
				URI:   cf.later.doc.URI,
				Range: protocol.LineRange(cf.later.node.StartPosition().Row, cf.later.node.StartPosition().Column, cf.later.node.EndPosition().Column),
			},
			Message: "later definition here",
		}},
	}
}

// routerIDVal reads the tracked router-id occurrence's value (its last
// token).
func (t *repeatedTracker) routerIDVal() string {
	if !t.hasRID {
		return ""
	}
	return lastToken(commandTokens(t.routerID.node, t.routerID.content()))
}

// clearNegated processes a negated_statement: the inner command's tracked
// state is removed so a subsequent positive definition is not flagged. The
// generic duplicate map key is the inner command's trimmed text; the
// hostname, primary-address, and router-id trackers reset on their no-forms.
func (t *repeatedTracker) clearNegated(neg scopeCmd) {
	inner := firstCommandChild(neg.node)
	if inner == nil {
		return
	}
	delete(t.seen, strings.TrimSpace(string(neg.content()[inner.StartByte():inner.EndByte()])))
	switch repeatedKeyword(inner, neg.content(), t.kw) {
	case "hostname":
		t.host, t.hostVal, t.hasHost = scopeCmd{}, "", false
	case "ip address":
		if !hasToken(commandTokens(inner, neg.content()), "secondary") {
			t.primary, t.hasPrimry = scopeCmd{}, false
		}
	case "router-id", "bgp router-id":
		t.routerID, t.hasRID = scopeCmd{}, false
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
func argTail(kw *keyword.Set, c scopeCmd) string {
	content := c.content()
	toks := commandTokens(c.node, content)
	kwWords := len(strings.Fields(repeatedKeyword(c.node, content, kw)))
	if kwWords >= len(toks) {
		return ""
	}
	parts := make([]string, 0, len(toks)-kwWords)
	for _, t := range toks[kwWords:] {
		parts = append(parts, t.text)
	}
	return strings.Join(parts, " ")
}

// repeatedDiag builds the shared diagnostic shape: anchored on the repeated
// command (in its own document), relatedInformation back to the first
// definition (in ITS document — possibly a different file in workspace
// mode). redefined-setting is an Error (the merged config will not behave
// as the earlier definition's author intended); duplicate-command stays a
// Warning (the config works, it is just smelly).
func repeatedDiag(c, first scopeCmd, code, msg string) protocol.Diagnostic {
	severity := protocol.SeverityWarning
	if code == "redefined-setting" {
		severity = protocol.SeverityError
	}
	return protocol.Diagnostic{
		Range:    protocol.LineRange(c.node.StartPosition().Row, c.node.StartPosition().Column, c.node.EndPosition().Column),
		Severity: severity,
		Source:   "chunt",
		Code:     code,
		Message:  msg,
		RelatedInformation: []protocol.DiagnosticRelatedInformation{{
			Location: protocol.Location{
				URI:   first.doc.URI,
				Range: protocol.LineRange(first.node.StartPosition().Row, first.node.StartPosition().Column, first.node.EndPosition().Column),
			},
			Message: "first defined here",
		}},
	}
}
