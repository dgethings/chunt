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

	// scalars carries the chunt-otg.1 curated registry state for ONE scope:
	// registry key -> first occurrence + rendered value.
	scalars map[string]scalarOccurrence

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
		scalars:       make(map[string]scalarOccurrence),
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
	// `ip address` pair yields exactly one warning. The key includes the
	// text-catch-all tail so banner/hash lines that differ only in the
	// unlexed text are not mistaken for exact duplicates.
	key := commandKeyText(c)
	if first, dup := t.seen[key]; dup {
		if t.crossFileOnly && first.doc.URI == c.doc.URI {
			return nil // same-file duplicate: owned by the single-file pass
		}
		return []protocol.Diagnostic{repeatedDiag(c, first, "duplicate-command",
			fmt.Sprintf("duplicate command %q; already defined earlier in this section", c.text()))}
	}
	t.seen[key] = c

	return t.scalarRedefinition(kw, c, toks)
}

// scalarDef is one curated scalar registry entry (chunt-otg.1/.2/.3/.4): a
// single-valued setting whose later definition silently replaces the earlier
// one. Matching is on the command's WORD PREFIX (leading token + leading
// args via leadingAndArgs), not the resolved keyword — several of these
// headers resolve to bare or truncated keywords (standby, timers, maximum,
// ip domain-name → "ip"), and several carry a variable word inside the form
// (the FHRP group number, the neighbor address), matched by "*". Several
// keyword forms can map to the same setting (the vrf forwarding pair, the
// enable secret/password pair), in which case they share one tracker key and
// redefinitions ACROSS forms are flagged too.
type scalarDef struct {
	// forms are command word sequences selecting this setting; "*" matches
	// any single word. The FIRST form is the canonical tracker key, so
	// cross-form settings share a slot regardless of which form matched.
	forms [][]string
	label string // human-readable setting name for messages
	// subKey, when set, returns the SUB-SCOPE key for one occurrence (the
	// FHRP group number, the neighbor address, the enable-secret level):
	// conflicts are tracked per (setting, sub-scope) pair, so distinct
	// groups/neighbors never interact.
	subKey func(full []string) string
}

var scalarRegistry = []scalarDef{
	// Interface-scoped settings (chunt-otg.1).
	{forms: [][]string{{"description"}}, label: "description"},
	{forms: [][]string{{"mtu"}}, label: "mtu"},
	{forms: [][]string{{"ip", "mtu"}}, label: "ip mtu"},
	{forms: [][]string{{"ipv6", "mtu"}}, label: "ipv6 mtu"},
	{forms: [][]string{{"bandwidth"}}, label: "bandwidth"},
	{forms: [][]string{{"speed"}}, label: "speed"},
	{forms: [][]string{{"duplex"}}, label: "duplex"},
	// Re-encapsulation replaces the interface's entire frame format; every
	// dot1q/isl/ppp/hdlc sub-form folds into the bare `encapsulation`
	// tracker via word-prefix matching, keeping the type word in the value.
	{forms: [][]string{{"encapsulation"}}, label: "encapsulation"},
	{forms: [][]string{{"switchport", "mode"}}, label: "switchport mode"},
	{forms: [][]string{{"switchport", "access", "vlan"}}, label: "switchport access vlan"},
	{forms: [][]string{{"vrf", "forwarding"}, {"ip", "vrf", "forwarding"}}, label: "vrf forwarding"},

	// Global-config settings (chunt-otg.3). Banner bodies and clock offsets
	// ride the text-catch-all tail (see commandWords). enable secret/password
	// share one tracker per LEVEL — both present for the same level is the
	// weak-config conflict the issue asks to flag. Additive global commands
	// (ip name-server, logging host, ntp server, ip domain list, snmp-server
	// community) are deliberately absent: re-adds are legitimate.
	{forms: [][]string{{"banner", "motd"}}, label: "banner motd"},
	{forms: [][]string{{"banner", "exec"}}, label: "banner exec"},
	{forms: [][]string{{"banner", "login"}}, label: "banner login"},
	{forms: [][]string{{"clock", "timezone"}}, label: "clock timezone"},
	{forms: [][]string{{"clock", "summer-time"}}, label: "clock summer-time"},
	{forms: [][]string{{"ip", "domain-name"}}, label: "ip domain-name"},
	{forms: [][]string{{"enable", "secret"}, {"enable", "password"}}, label: "enable secret/password",
		subKey: func(full []string) string {
			if len(full) > 3 && full[2] == "level" {
				return full[3]
			}
			return "0"
		}},
	{forms: [][]string{{"snmp-server", "location"}}, label: "snmp-server location"},
	{forms: [][]string{{"snmp-server", "contact"}}, label: "snmp-server contact"},
	{forms: [][]string{{"ip", "default-gateway"}}, label: "ip default-gateway"},
	{forms: [][]string{{"ip", "ssh", "version"}}, label: "ip ssh version"},
	{forms: [][]string{{"logging", "buffered"}}, label: "logging buffered"},
	{forms: [][]string{{"logging", "source-interface"}}, label: "logging source-interface"},

	// FHRP per-group scalars (chunt-otg.2): scalar per (interface, group)
	// pair — the group number sub-keys the tracker, so distinct groups never
	// interact. Plain preempts carry no value and are ignored; `preempt
	// delay N` is tracked.
	{forms: [][]string{{"standby", "*", "ip"}}, label: "standby ip",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"standby", "*", "priority"}}, label: "standby priority",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"standby", "*", "preempt"}}, label: "standby preempt",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"standby", "*", "authentication"}}, label: "standby authentication",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"vrrp", "*", "ip"}}, label: "vrrp ip",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"vrrp", "*", "priority"}}, label: "vrrp priority",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"glbp", "*", "ip"}}, label: "glbp ip",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"glbp", "*", "priority"}}, label: "glbp priority",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"glbp", "*", "preempt"}}, label: "glbp preempt",
		subKey: func(full []string) string { return full[1] }},

	// Routing-process scalars (chunt-otg.4): tracked per process section (a
	// router section is the tracker scope). Toggles (bgp log-neighbor-changes,
	// auto-summary, no synchronization) have no conflicting value — a repeat
	// is duplicate-command's business — and OSPF `area N range` is additive
	// (multiple summaries per area coexist), so both are deliberately
	// absent. Neighbor scalars sub-key by the neighbor address, so different
	// neighbors never interact.
	{forms: [][]string{{"timers", "bgp"}}, label: "timers bgp"},
	{forms: [][]string{{"distance", "bgp"}}, label: "distance bgp"},
	{forms: [][]string{{"maximum"}}, label: "maximum-paths"},
	{forms: [][]string{{"neighbor", "*", "remote-as"}}, label: "neighbor remote-as",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"neighbor", "*", "description"}}, label: "neighbor description",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"neighbor", "*", "update-source"}}, label: "neighbor update-source",
		subKey: func(full []string) string { return full[1] }},
	{forms: [][]string{{"neighbor", "*", "password"}}, label: "neighbor password",
		subKey: func(full []string) string { return full[1] }},
}

// findForm returns the first form whose words prefix-match full ("*"
// matches any single word), or nil.
func (d *scalarDef) findForm(full []string) []string {
	for _, f := range d.forms {
		if wordsPrefixMatchWild(full, f) {
			return f
		}
	}
	return nil
}

// wordsPrefixMatchWild reports whether the first len(words) entries of full
// match words, where "*" matches any single word.
func wordsPrefixMatchWild(full, words []string) bool {
	if len(full) < len(words) {
		return false
	}
	for i, w := range words {
		if w != "*" && full[i] != w {
			return false
		}
	}
	return true
}

// trackerKey is the scalars map key for one occurrence: the canonical (first)
// form plus the sub-scope key when the def defines one. Canonical-form keying
// is what makes cross-form settings share a slot (enable secret vs password,
// vrf forwarding vs ip vrf forwarding).
func (d *scalarDef) trackerKey(full []string) string {
	k := strings.Join(d.forms[0], "\x00")
	if d.subKey != nil {
		k += "\x00" + d.subKey(full)
	}
	return k
}

// commandWords returns the command's word sequence for registry matching:
// the first child's text (the leading keyword — including the anonymous
// keyword child of dedicated *_statement nodes) followed by the text of the
// remaining named children (the args), PLUS the catch-all text node that
// follows the command_line when present — the grammar's overflow for words
// it cannot lex (banner bodies, negative clock offsets, password hashes,
// maximum-paths's hyphenated tail). Nil when there is no leading word.
func commandWords(c scopeCmd) []string {
	n, content := c.node, c.content()
	if n.ChildCount() == 0 {
		return nil
	}
	first := n.Child(0)
	if first == nil {
		return nil
	}
	leading := string(content[first.StartByte():first.EndByte()])
	if leading == "" {
		return nil
	}
	full := []string{leading}
	for i := uint(1); i < n.ChildCount(); i++ {
		ch := n.Child(i)
		if ch == nil || !ch.IsNamed() {
			continue
		}
		full = append(full, string(content[ch.StartByte():ch.EndByte()]))
	}
	if sib := n.NextNamedSibling(); sib != nil && sib.Kind() == "text" {
		if txt := strings.TrimSpace(string(content[sib.StartByte():sib.EndByte()])); txt != "" {
			full = append(full, txt)
		}
	}
	return full
}

// commandKeyText is the duplicate-detection key: the command's own text plus
// its text-catch-all sibling, so lines that differ only in the unlexed tail
// (banner bodies, hashes) are not mistaken for exact duplicates.
func commandKeyText(c scopeCmd) string {
	txt := c.text()
	if sib := c.node.NextNamedSibling(); sib != nil && sib.Kind() == "text" {
		txt += " " + strings.TrimSpace(string(c.content()[sib.StartByte():sib.EndByte()]))
	}
	return txt
}

// value renders the setting's value tail: the words after the matched form
// (statement nodes carry the keyword as an anonymous Child(0), so their
// words are already the value; the text-catch-all tail rides in full via
// commandWords).
func (d *scalarDef) value(c scopeCmd, full []string) string {
	form := d.findForm(full)
	if form == nil {
		return ""
	}
	return strings.Join(full[len(form):], " ")
}

// scalarOccurrence records the first definition of one registry scalar in a
// scope, with its rendered value for replacement messages.
type scalarOccurrence struct {
	cmd scopeCmd
	val string
}

// scalarRedefinition applies the curated redefined-setting rule for kw at
// command occurrence c (whose tokens are toks), updating the scalar
// trackers. Returns the diagnostic to emit, or nil.
func (t *repeatedTracker) scalarRedefinition(kw string, c scopeCmd, toks []docToken) []protocol.Diagnostic {
	switch {
	case kw == "hostname":
		v := lastToken(toks)
		if t.hasHost && v != t.hostVal {
			return t.emitRedefined(c, t.host,
				fmt.Sprintf("hostname %q replaces %q; only the last definition takes effect", v, t.hostVal),
				"hostname", t.hostVal, v)
		}
		if !t.hasHost {
			t.host, t.hostVal, t.hasHost = c, v, true
		}
	case isIPAddressKw(kw):
		if hasToken(toks, "secondary") {
			return nil // additive secondaries are exempt
		}
		if t.hasPrimry {
			// Values are rendered after the base "ip address" words (not the
			// resolved longest form), so `ip address dhcp` compares as "dhcp".
			priorVal, laterVal := argTailWords(t.primary, 2), argTailWords(c, 2)
			return t.emitRedefined(c, t.primary,
				fmt.Sprintf("multiple primary \"ip address\" definitions on this interface; this replaces %q", priorVal),
				"ip address", priorVal, laterVal)
		}
		t.primary, t.hasPrimry = c, true
	case kw == "router-id" || kw == "bgp router-id":
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

	// Curated registry scalars (chunt-otg.1/.2/.3/.4): interface, global,
	// FHRP-group, and routing-process single-value settings. A different
	// later value replaces the earlier one at merge time — an Error under
	// the redefined-setting convention.
	return t.registryRedefinition(c)
}

// registryRedefinition applies the curated scalar registry: a later
// DIFFERENT value replaces the recorded one; the first occurrence is
// recorded. Keyword-only lines carry no value and are ignored.
func (t *repeatedTracker) registryRedefinition(c scopeCmd) []protocol.Diagnostic {
	full := commandWords(c)
	if full == nil {
		return nil
	}
	for i := range scalarRegistry {
		def := &scalarRegistry[i]
		if def.findForm(full) == nil {
			continue
		}
		val := def.value(c, full)
		if val == "" {
			return nil // keyword-only line carries no value to track
		}
		key := def.trackerKey(full)
		if prior, ok := t.scalars[key]; ok {
			if val != prior.val {
				return t.emitRedefined(c, prior.cmd,
					fmt.Sprintf("%s %q replaces %q; only the last definition takes effect", def.label, val, prior.val),
					def.label, prior.val, val)
			}
			return nil // same value, different spelling: no replacement
		}
		t.scalars[key] = scalarOccurrence{cmd: c, val: val}
		return nil
	}
	return nil
}

// isIPAddressKw reports whether a resolved keyword is an "ip address ..."
// form; every such form shares the primary-address tracker, so
// `ip address dhcp` replacing a static primary is flagged too (chunt-otg.1).
func isIPAddressKw(kw string) bool {
	return kw == "ip address" || strings.HasPrefix(kw, "ip address ")
}

// registryValue was superseded by argTailWords, then by scalarDef.value.

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
// hostname, primary-address, and router-id trackers reset on their no-forms,
// as does any matching registry scalar (no description, no mtu, no vrf
// forwarding, ...).
func (t *repeatedTracker) clearNegated(neg scopeCmd) {
	inner := firstCommandChild(neg.node)
	if inner == nil {
		return
	}
	delete(t.seen, commandKeyText(scopeCmd{node: inner, doc: neg.doc}))
	kw := repeatedKeyword(inner, neg.content(), t.kw)
	if full := commandWords(scopeCmd{node: inner, doc: neg.doc}); full != nil {
		for i := range scalarRegistry {
			if scalarRegistry[i].findForm(full) != nil {
				delete(t.scalars, scalarRegistry[i].trackerKey(full))
			}
		}
	}
	switch {
	case kw == "hostname":
		t.host, t.hostVal, t.hasHost = scopeCmd{}, "", false
	case isIPAddressKw(kw):
		if !hasToken(commandTokens(inner, neg.content()), "secondary") {
			t.primary, t.hasPrimry = scopeCmd{}, false
		}
	case kw == "router-id" || kw == "bgp router-id":
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

// argTailWords renders the argument tokens after the first n leading
// tokens of a command (n = the matched keyword form's word count, e.g. 2 for
// "ip address"). Dedicated *_statement nodes carry the keyword as an
// anonymous child, so their tokens are ALREADY the value and n is ignored
// (mtu_statement "mtu 1500" yields tokens [1500]; a one-word strip would
// corrupt it to "" — chunt-otg.1).
func argTailWords(c scopeCmd, n int) string {
	toks := commandTokens(c.node, c.content())
	if c.node.Kind() != "command_line" {
		n = 0
	}
	if n >= len(toks) {
		return ""
	}
	parts := make([]string, 0, len(toks)-n)
	for _, t := range toks[n:] {
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
