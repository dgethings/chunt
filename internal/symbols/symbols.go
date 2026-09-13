// Package symbols extracts named Cisco IOS definition sites (interface,
// router, route-map, class-map, policy-map, vlan, line, redundancy, ACL)
// from a parsed tree-sitter AST and indexes them per-URI for downstream
// LSP features (Definition, References, DocumentSymbol, diagnostics).
package symbols

import (
	"strings"
	"sync"

	"github.com/dgethings/chunt/internal/protocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Kind classifies a Cisco IOS named definition.
type Kind string

const (
	KindInterface      Kind = "interface"
	KindRouter         Kind = "router"
	KindAddressFamily  Kind = "address-family"
	KindRouteMap       Kind = "route-map"
	KindClassMap       Kind = "class-map"
	KindPolicyMap      Kind = "policy-map"
	KindPolicyMapClass Kind = "policy-map-class"
	KindVlan           Kind = "vlan"
	KindLine           Kind = "line"
	KindRedundancy     Kind = "redundancy"
	KindACL            Kind = "acl"
	KindHostname       Kind = "hostname"
)

// Symbol is a named definition site extracted from a parsed document.
type Symbol struct {
	Kind      Kind
	Name      string
	URI       string
	Range     protocol.Range // full section / line range
	NameRange protocol.Range // just the name token (Definition / highlight)
}

// Reference is a use of a Symbol's name somewhere in the document. The Kind
// is the expected definition kind; the Name is the referenced identifier as
// written; Range covers just the name token (so a diagnostic can be anchored
// precisely on the reference site).
type Reference struct {
	Kind  Kind
	Name  string
	URI   string
	Range protocol.Range
}

// docIndex holds the symbols and references extracted from a single
// document. Both slices are in document order.
type docIndex struct {
	Symbols    []Symbol
	References []Reference
	// O(1) lookup indices, built in Table.Index after extraction. They point
	// into Symbols/References and preserve document order, so the Lookup-
	// family methods return identical results to a linear scan (chunt-4qw).
	byKindName    map[Kind]map[string][]*Symbol    // Lookup(kind, name)
	byName        map[string][]*Symbol             // LookupAny(name)
	byKindNameRef map[Kind]map[string][]*Reference // ReferencesLookup(kind, name)
}

// Table is a per-URI index of Symbol definitions and Reference uses. Lookups
// are read-only and safe for concurrent use; Index/Clear mutate and must not
// run concurrently with each other on the same URI (the LSP server already
// serializes per document via didOpen/didChange).
type Table struct {
	mu    sync.RWMutex
	byURI map[string]*docIndex
}

func NewTable() *Table {
	return &Table{byURI: make(map[string]*docIndex)}
}

// Index walks root, extracts every definition and reference it recognizes,
// and replaces the stored entry for uri. Call with root=nil to clear.
func (t *Table) Index(uri string, root *sitter.Node, content []byte) {
	di := &docIndex{
		Symbols:    Extract(uri, root, content),
		References: ExtractReferences(uri, root, content),
	}
	di.buildIndices()
	t.mu.Lock()
	t.byURI[uri] = di
	t.mu.Unlock()
}

// buildIndices populates the O(1) lookup maps from Symbols/References in
// document order. Pointers into the backing slices keep the maps consistent;
// the slices are never mutated after Index, so the pointers stay valid for
// the life of the docIndex. Called from Index (under the write lock).
// chunt-4qw.
func (di *docIndex) buildIndices() {
	di.byKindName = make(map[Kind]map[string][]*Symbol)
	di.byName = make(map[string][]*Symbol, len(di.Symbols))
	for i := range di.Symbols {
		s := &di.Symbols[i]
		m := di.byKindName[s.Kind]
		if m == nil {
			m = make(map[string][]*Symbol)
			di.byKindName[s.Kind] = m
		}
		m[s.Name] = append(m[s.Name], s)
		di.byName[s.Name] = append(di.byName[s.Name], s)
	}
	di.byKindNameRef = make(map[Kind]map[string][]*Reference)
	for i := range di.References {
		r := &di.References[i]
		m := di.byKindNameRef[r.Kind]
		if m == nil {
			m = make(map[string][]*Reference)
			di.byKindNameRef[r.Kind] = m
		}
		m[r.Name] = append(m[r.Name], r)
	}
}

// Clear removes all symbols and references for uri.
func (t *Table) Clear(uri string) {
	t.mu.Lock()
	delete(t.byURI, uri)
	t.mu.Unlock()
}

// All returns every symbol indexed for uri, in document order. The returned
// slice is a copy and may be modified freely.
func (t *Table) All(uri string) []Symbol {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if di, ok := t.byURI[uri]; ok {
		return append([]Symbol(nil), di.Symbols...)
	}
	return nil
}

// Lookup returns symbols matching kind+name in uri, in document order. O(1)
// average via the byKindName index (chunt-4qw).
func (t *Table) Lookup(uri string, kind Kind, name string) []Symbol {
	t.mu.RLock()
	defer t.mu.RUnlock()
	di, ok := t.byURI[uri]
	if !ok {
		return nil
	}
	m := di.byKindName[kind]
	if m == nil {
		return nil
	}
	return copySymbols(m[name])
}

// LookupAny returns symbols in uri with a matching name regardless of kind,
// in document order. O(1) average via the byName index (chunt-4qw).
func (t *Table) LookupAny(uri string, name string) []Symbol {
	t.mu.RLock()
	defer t.mu.RUnlock()
	di, ok := t.byURI[uri]
	if !ok {
		return nil
	}
	return copySymbols(di.byName[name])
}

// ReferencesAll returns every reference indexed for uri, in document order.
// The returned slice is a copy and may be modified freely.
func (t *Table) ReferencesAll(uri string) []Reference {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if di, ok := t.byURI[uri]; ok {
		return append([]Reference(nil), di.References...)
	}
	return nil
}

// ReferencesLookup returns references in uri whose Kind and Name match, in
// document order. Used by the References LSP feature ("find all usages of
// this symbol") and by the unused-definition diagnostic ("any reference to
// this definition?"). O(1) average via the byKindNameRef index (chunt-4qw).
func (t *Table) ReferencesLookup(uri string, kind Kind, name string) []Reference {
	t.mu.RLock()
	defer t.mu.RUnlock()
	di, ok := t.byURI[uri]
	if !ok {
		return nil
	}
	m := di.byKindNameRef[kind]
	if m == nil {
		return nil
	}
	return copyReferences(m[name])
}

// copySymbols dereferences a slice of symbol pointers into a fresh value
// slice, returning nil for empty input so callers observe the same nil-vs-
// empty semantics as the original linear-scan implementation. chunt-4qw.
func copySymbols(src []*Symbol) []Symbol {
	if len(src) == 0 {
		return nil
	}
	out := make([]Symbol, len(src))
	for i, s := range src {
		out[i] = *s
	}
	return out
}

// copyReferences is the Reference analogue of copySymbols. chunt-4qw.
func copyReferences(src []*Reference) []Reference {
	if len(src) == 0 {
		return nil
	}
	out := make([]Reference, len(src))
	for i, r := range src {
		out[i] = *r
	}
	return out
}

// SymbolAt returns a pointer to the Symbol whose NameRange contains pos, or
// nil. Used by Go-To-Definition when the cursor is already on a definition
// site (most editors return the definition itself in that case).
func (t *Table) SymbolAt(uri string, pos protocol.Position) *Symbol {
	t.mu.RLock()
	defer t.mu.RUnlock()
	di, ok := t.byURI[uri]
	if !ok {
		return nil
	}
	for i := range di.Symbols {
		s := &di.Symbols[i]
		if rangeContains(s.NameRange, pos) {
			return s
		}
	}
	return nil
}

// ReferenceAt returns a pointer to the Reference whose Range contains pos,
// or nil. Used by Go-To-Definition to identify the reference name token the
// user clicked on and look up its target definition.
func (t *Table) ReferenceAt(uri string, pos protocol.Position) *Reference {
	t.mu.RLock()
	defer t.mu.RUnlock()
	di, ok := t.byURI[uri]
	if !ok {
		return nil
	}
	for i := range di.References {
		r := &di.References[i]
		if rangeContains(r.Range, pos) {
			return r
		}
	}
	return nil
}

// rangeContains reports whether pos lies within r (inclusive of the start,
// exclusive of the end, matching the half-open byte ranges tree-sitter
// produces and the LSP Position semantics).
func rangeContains(r protocol.Range, pos protocol.Position) bool {
	if pos.Line < r.Start.Line || pos.Line > r.End.Line {
		return false
	}
	if pos.Line == r.Start.Line && pos.Character < r.Start.Character {
		return false
	}
	if pos.Line == r.End.Line && pos.Character >= r.End.Character {
		return false
	}
	return true
}

// Extract walks root and returns every symbol it recognizes, in document
// order. Returns nil if root is nil.
//
// Symbols come from two sources:
//   - Hierarchical sections (interface_section, router_section,
//     route_map_section, class_map_section, policy_map_section, vlan_section,
//     line_section, redundancy_section, ip_access_list_section) — each
//     contributes one Symbol via the header's `name` field (or a synthesized
//     name for line/redundancy).
//   - Flat numbered ACL statements (`access-list <N> permit|deny ...`),
//     which parse as access_list_statement nodes — see extractACL.
func Extract(uri string, root *sitter.Node, content []byte) []Symbol {
	if root == nil {
		return nil
	}
	var out []Symbol
	walkNamed(root, func(n *sitter.Node) bool {
		if sym, ok := extractSection(uri, n, content); ok {
			out = append(out, sym)
			// Section bodies do not themselves contain nested section
			// definitions in IOS, but descending is harmless; keep going
			// so jinja loops containing sections (e.g. `{% for %}` emitting
			// interfaces) are still indexed.
		}
		return true
	})
	walkNamed(root, func(n *sitter.Node) bool {
		if sym, ok := extractFlat(uri, n, content); ok {
			out = append(out, sym)
		}
		return true
	})
	return out
}

// extractFlat dispatches a flat *_statement node (one that is not a
// hierarchical section) to the appropriate symbol extractor. Currently
// handles access_list_statement (numbered ACL form),
// hostname_statement, and command_line (named-ACL fallback). Add new
// flat statement kinds here.
func extractFlat(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	switch n.Kind() {
	case "access_list_statement":
		return extractACL(uri, n, content)
	case "hostname_statement":
		return extractHostname(uri, n, content)
	case "command_line":
		return extractNamedACLCommandLine(uri, n, content)
	}
	return Symbol{}, false
}

// sectionSpec describes how to extract a Symbol from a *_section node.
//
// NOTE: this table drives Symbol EXTRACTION (LSP-facing Kind + name field) —
// a different concern from the AST-kind -> keyword.Section mapping, whose
// single source of truth is internal/section (chunt-mpc). Keep the two
// tables in step when adding a section kind.
type sectionSpec struct {
	sectionKind string // AST node kind, e.g. "interface_section"
	kind        Kind   // LSP-facing symbol kind
	headerKind  string // child node kind holding the name
	nameField   string // field name on the header carrying the name token
	// synthesize, when non-nil, builds the Symbol.Name from the header node
	// (used by line_section and redundancy_section which have no single
	// `name` field).
	synthesize func(header *sitter.Node, content []byte) (name string, nameRange protocol.Range)
}

var sectionSpecs = []sectionSpec{
	{sectionKind: "interface_section", kind: KindInterface, headerKind: "interface_header", nameField: "name"},
	{sectionKind: "router_section", kind: KindRouter, headerKind: "router_header", nameField: "process_id"},
	{sectionKind: "address_family_section", kind: KindAddressFamily, headerKind: "address_family_header", nameField: "name"},
	{sectionKind: "route_map_section", kind: KindRouteMap, headerKind: "route_map_header", nameField: "name"},
	{sectionKind: "class_map_section", kind: KindClassMap, headerKind: "class_map_header", nameField: "name"},
	{sectionKind: "policy_map_section", kind: KindPolicyMap, headerKind: "policy_map_header", nameField: "name"},
	{sectionKind: "policy_map_class_section", kind: KindPolicyMapClass, headerKind: "policy_map_class_header", nameField: "name"},
	{sectionKind: "vlan_section", kind: KindVlan, headerKind: "vlan_header", nameField: "name"},
	{
		sectionKind: "line_section",
		kind:        KindLine,
		headerKind:  "line_header",
		synthesize:  synthesizeLineName,
	},
	{
		sectionKind: "redundancy_section",
		kind:        KindRedundancy,
		headerKind:  "redundancy_header",
		synthesize: func(header *sitter.Node, content []byte) (string, protocol.Range) {
			// Singleton — name is fixed; range covers the whole header so
			// Go-To-Definition lands on the `redundancy` keyword.
			return "redundancy", nodeRange(header)
		},
	},
	// Named ACLs (`ip access-list <standard|extended> NAME { ... }`). This
	// entry was deferred from Phase 1 because the grammar could not promote
	// `access-list` to a prec-2 keyword without colliding with every other
	// `ip ...` line; Phase 1 therefore routed named ACLs through flat
	// command_line extraction. Phase A in the sibling grammar repo (commit
	// ab0d95a) resolved the collision via the same `_cmd_arg` alias trick
	// used by the other six section keywords, so named ACLs now parse as a
	// real hierarchical section with a header carrying the `name` field.
	{sectionKind: "ip_access_list_section", kind: KindACL, headerKind: "ip_access_list_header", nameField: "name"},
}

func extractSection(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	for _, sp := range sectionSpecs {
		if n.Kind() != sp.sectionKind {
			continue
		}
		header := namedChildByKind(n, sp.headerKind)
		if header == nil {
			return Symbol{}, false
		}
		sym := Symbol{
			Kind:  sp.kind,
			URI:   uri,
			Range: nodeRange(n),
		}
		if sp.synthesize != nil {
			name, nameRange := sp.synthesize(header, content)
			if name == "" {
				return Symbol{}, false
			}
			sym.Name = name
			sym.NameRange = nameRange
			return sym, true
		}
		nameNode := header.ChildByFieldName(sp.nameField)
		if nameNode == nil {
			return Symbol{}, false
		}
		name := textOf(nameNode, content)
		if name == "" {
			return Symbol{}, false
		}
		sym.Name = name
		sym.NameRange = nodeRange(nameNode)
		return sym, true
	}
	return Symbol{}, false
}

// synthesizeLineName builds a stable key from `line <type> <first> [last]`,
// e.g. "vty-0-4" or "console-0". The NameRange covers the whole header
// because there is no single token that names the section.
func synthesizeLineName(header *sitter.Node, content []byte) (string, protocol.Range) {
	typeNode := header.ChildByFieldName("type")
	if typeNode == nil {
		return "", nodeRange(header)
	}
	parts := []string{textOf(typeNode, content)}
	for i := uint(0); i < header.NamedChildCount(); i++ {
		c := header.NamedChild(i)
		if c == nil {
			continue
		}
		// Skip the type field (already captured) and the line_kw keyword.
		if c.Kind() != "value" && c.Kind() != "output" {
			continue
		}
		// The type field itself is also a value/output; skip it by byte range.
		if c.StartByte() == typeNode.StartByte() {
			continue
		}
		parts = append(parts, textOf(c, content))
	}
	return strings.Join(parts, "-"), nodeRange(header)
}

// extractACL extracts a Symbol from an `access_list_statement` node (the
// numbered ACL form: `access-list <N> permit|deny ...`). The numbered ACL
// ranges are 1-99 (standard), 100-199 (extended), and historical ranges
// (200-299, etc.); only the leading number contributes the Symbol.Name
// because that is what every reference form (`ip access-group N in`,
// `access-class N in`, `match ip address N`) cites.
//
// The named ACL form (`ip access-list <standard|extended> NAME`) is handled
// by the sectionSpec table via ip_access_list_section — see that entry for
// background on the Phase A grammar promotion.
func extractACL(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	if n.Kind() != "access_list_statement" {
		return Symbol{}, false
	}
	// access_list_statement = access_list_kw + repeat(field("arg", ...)).
	// Child(0) is the leading keyword; the trailing named children are the
	// ACL number, action, protocol, source/dest, etc. Only the number
	// contributes the Symbol.Name; downstream LSP features don't need the
	// rest for symbol resolution.
	if n.NamedChildCount() < 2 {
		return Symbol{}, false
	}
	nameArg := n.NamedChild(1)
	if nameArg == nil {
		return Symbol{}, false
	}
	return Symbol{
		Kind:      KindACL,
		Name:      textOf(nameArg, content),
		URI:       uri,
		Range:     nodeRange(n),
		NameRange: nodeRange(nameArg),
	}, true
}

// extractNamedACLCommandLine recovers an ACL definition from an
// `ip access-list <standard|extended> NAME` line that the grammar parsed
// as a flat command_line rather than a hierarchical ip_access_list_section.
//
// Normally such a line opens a real sub-mode and parses as
// ip_access_list_section (handled by extractSection). But when the line is
// absorbed into an enclosing section's body — which happens whenever a
// nested section (e.g. policy_map_class_section) has consumed the `!` that
// the enclosing section still needs to terminate (the grammar's documented
// `!`-counting heuristic) — the line surfaces as a generic command_line.
// This fallback keeps ACL symbol resolution working in that case.
func extractNamedACLCommandLine(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	leading, args, argNodes := leadingAndArgs(n, content)
	if leading != "ip" || len(args) < 3 {
		return Symbol{}, false
	}
	// `ip access-list <standard|extended> NAME`.
	if args[0] != "access-list" {
		return Symbol{}, false
	}
	if args[1] != "standard" && args[1] != "extended" {
		return Symbol{}, false
	}
	nameIdx := 2
	if nameIdx >= len(argNodes) {
		return Symbol{}, false
	}
	nameNode := argNodes[nameIdx]
	name := textOf(nameNode, content)
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:      KindACL,
		Name:      name,
		URI:       uri,
		Range:     nodeRange(n),
		NameRange: nodeRange(nameNode),
	}, true
}

// extractHostname extracts a Symbol from a `hostname_statement` node
// (`hostname <value>`). IOS permits at most one hostname per config; a
// second occurrence is almost always a copy-paste error, so it is flagged
// by runDuplicateDefinitionDiagnostics. The hostname_value field carries
// the name token.
func extractHostname(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	if n.Kind() != "hostname_statement" {
		return Symbol{}, false
	}
	nameNode := n.ChildByFieldName("hostname_value")
	if nameNode == nil {
		return Symbol{}, false
	}
	name := textOf(nameNode, content)
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:      KindHostname,
		Name:      name,
		URI:       uri,
		Range:     nodeRange(n),
		NameRange: nodeRange(nameNode),
	}, true
}

// walkNamed depth-first traverses the named-children subtree of n, invoking
// visit on each. If visit returns false, the subtree under that node is
// skipped.
func walkNamed(n *sitter.Node, visit func(*sitter.Node) bool) {
	if n == nil {
		return
	}
	if !visit(n) {
		return
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		walkNamed(n.NamedChild(i), visit)
	}
}

func namedChildByKind(n *sitter.Node, kind string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// namedArgs returns the named children of n that carry the "arg" field
// (the trailing args of a command_line, after the leading identifier).
// The leading identifier itself (the "name" field) is excluded.
func namedArgs(n *sitter.Node) []*sitter.Node {
	var out []*sitter.Node
	nameNode := n.ChildByFieldName("name")
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		// Skip the leading identifier (the "name" field).
		if nameNode != nil && c.Id() == nameNode.Id() {
			continue
		}
		out = append(out, c)
	}
	return out
}

func textOf(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	return string(content[n.StartByte():n.EndByte()])
}

func nodeRange(n *sitter.Node) protocol.Range {
	if n == nil {
		return protocol.Range{}
	}
	start := n.StartPosition()
	end := n.EndPosition()
	return protocol.Range{
		Start: protocol.Position{Line: start.Row, Character: start.Column},
		End:   protocol.Position{Line: end.Row, Character: end.Column},
	}
}

// ---------------------------------------------------------------------------
// Reference extraction
// ---------------------------------------------------------------------------

// refSpec describes a command pattern that introduces a reference to a named
// definition. Leading is the prefix token sequence (starting with the
// command's first token, including any prec-2 keyword such as `match`,
// `class`, `access-class`, plus subsequent fixed tokens like `ip address`).
// ArgIndex is the 0-based offset of the referenced name within the trailing
// args that follow the Leading prefix.
//
// Examples:
//
//	{["ip", "access-group"], 0, KindACL}            -> ip access-group NAME in|out
//	{["match", "ip", "address"], 0, KindACL}        -> match ip address NAME
//	{["service", "-policy"], 1, KindPolicyMap}      -> service-policy input NAME
//	    (`service-policy` parses as service_statement(args=["-policy", dir, NAME])
//	    because `service` is a prec-2 keyword that splits the hyphenated literal)
//	{["class"], 0, KindClassMap}                    -> class NAME (inside policy-map body)
type refSpec struct {
	Leading  []string
	ArgIndex int
	Kind     Kind
}

var refSpecs = []refSpec{
	{Leading: []string{"ip", "access-group"}, ArgIndex: 0, Kind: KindACL},
	{Leading: []string{"access-class"}, ArgIndex: 0, Kind: KindACL},
	{Leading: []string{"match", "ip", "address"}, ArgIndex: 0, Kind: KindACL},
	{Leading: []string{"ip", "policy", "route-map"}, ArgIndex: 0, Kind: KindRouteMap},
	{Leading: []string{"service", "-policy"}, ArgIndex: 1, Kind: KindPolicyMap},
	{Leading: []string{"class"}, ArgIndex: 0, Kind: KindClassMap},
	{Leading: []string{"switchport", "access", "vlan"}, ArgIndex: 0, Kind: KindVlan},
}

// refSectionSpec describes a hierarchical section node whose header names a
// referenced entity (rather than a standalone definition). The canonical
// case is the `class NAME { ... }` sub-block inside a policy-map: it opens a
// config-pmap-c sub-mode AND cites a class-map to apply, so it is both a
// section and a reference to a class-map definition.
//
// The `class NAME` line parses as a flat class_statement when the grammar's
// `!`-counting heuristic cannot dedicate a terminator to the sub-mode, and
// as a policy_map_class_section when it can. The flat form is handled by the
// refSpec `{["class"], 0, KindClassMap}` above; this table covers the
// section form so the same reference is extracted regardless of which shape
// the grammar picks.
type refSectionSpec struct {
	sectionKind string
	headerKind  string
	kind        Kind
}

var refSectionSpecs = []refSectionSpec{
	{sectionKind: "policy_map_class_section", headerKind: "policy_map_class_header", kind: KindClassMap},
}

// ExtractReferences walks root and returns every reference it recognizes, in
// document order. Returns nil if root is nil.
//
// References come from any command-shaped node (command_line or *_statement)
// whose leading token sequence matches a refSpec. References inside a
// negated_statement are skipped because they semantically remove a binding
// rather than establish one (`no ip access-group FOO in` should NOT be
// flagged as an unresolved reference).
func ExtractReferences(uri string, root *sitter.Node, content []byte) []Reference {
	if root == nil {
		return nil
	}
	var refs []Reference
	walkNamed(root, func(n *sitter.Node) bool {
		// Section-shaped reference introducers (e.g. `class NAME` parsed as a
		// policy_map_class_section). These are not command-like, so handle them
		// before the command-line path below; keep descending either way.
		if r, ok := extractSectionReference(uri, n, content); ok {
			refs = append(refs, r)
		}
		if !isCommandLike(n) {
			return true
		}
		if isNegated(n) {
			// Skip the negated command itself but keep descending so a
			// section body containing a negated line still gets its other
			// references indexed.
			return true
		}
		leading, args, argNodes := leadingAndArgs(n, content)
		if leading == "" || len(args) == 0 {
			return true
		}
		fullSeq := append([]string{leading}, args...)
		for _, spec := range refSpecs {
			if !prefixMatch(fullSeq, spec.Leading) {
				continue
			}
			// argNodes parallels fullSeq[1:] (leading is Child(0); args are
			// the rest). Index into argNodes by (len(Leading)-1 + ArgIndex).
			idx := len(spec.Leading) - 1 + spec.ArgIndex
			if idx >= len(argNodes) {
				continue
			}
			nameNode := argNodes[idx]
			refs = append(refs, Reference{
				Kind:  spec.Kind,
				Name:  textOf(nameNode, content),
				URI:   uri,
				Range: nodeRange(nameNode),
			})
		}
		return true
	})
	return refs
}

// isCommandLike reports whether n is a node whose first child is a leading
// keyword/identifier followed by trailing args. True for command_line and
// any *_statement rich rule. False for sections, headers, comments, etc.
func isCommandLike(n *sitter.Node) bool {
	if n == nil {
		return false
	}
	k := n.Kind()
	if k == "command_line" {
		return true
	}
	return strings.HasSuffix(k, "_statement")
}

// isNegated reports whether n (or any ancestor up to the document root) is
// enclosed in a negated_statement. Used to skip reference extraction for
// `no <command>` lines.
func isNegated(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "negated_statement" {
			return true
		}
	}
	return false
}

// extractSectionReference extracts a Reference from a section-shaped node
// listed in refSectionSpecs (e.g. a policy_map_class_section whose header
// names a class-map). The referenced name token comes from the header's
// `name` field, and the Reference range covers just that token so diagnostics
// and Go-To-Definition can anchor on it precisely.
func extractSectionReference(uri string, n *sitter.Node, content []byte) (Reference, bool) {
	for _, sp := range refSectionSpecs {
		if n.Kind() != sp.sectionKind {
			continue
		}
		header := namedChildByKind(n, sp.headerKind)
		if header == nil {
			return Reference{}, false
		}
		nameNode := header.ChildByFieldName("name")
		if nameNode == nil {
			return Reference{}, false
		}
		name := textOf(nameNode, content)
		if name == "" {
			return Reference{}, false
		}
		return Reference{
			Kind:  sp.kind,
			Name:  name,
			URI:   uri,
			Range: nodeRange(nameNode),
		}, true
	}
	return Reference{}, false
}

// leadingAndArgs decomposes a command-like node into (leading token text,
// trailing arg token texts, trailing arg nodes). The leading token is the
// node's first child (anonymous keyword for *_statement rules, named
// identifier for command_line). Trailing args are every named child after
// the first (anonymous tokens like the keyword literal are skipped).
func leadingAndArgs(n *sitter.Node, content []byte) (string, []string, []*sitter.Node) {
	if n == nil || n.ChildCount() == 0 {
		return "", nil, nil
	}
	first := n.Child(0)
	if first == nil {
		return "", nil, nil
	}
	leading := textOf(first, content)
	var args []string
	var argNodes []*sitter.Node
	for i := uint(1); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		args = append(args, textOf(c, content))
		argNodes = append(argNodes, c)
	}
	return leading, args, argNodes
}

// prefixMatch reports whether a begins with every element of b in order.
func prefixMatch(a, b []string) bool {
	if len(a) < len(b) {
		return false
	}
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
