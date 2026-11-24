// Package symbols extracts named Cisco IOS definition sites (interface,
// router, route-map, class-map, policy-map, vlan, line, redundancy, ACL)
// from a parsed tree-sitter AST and indexes them per-URI for downstream
// LSP features (Definition, References, DocumentSymbol, diagnostics).
package symbols

import (
	"strings"
	"sync"
	"unicode"

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
	KindACLv6          Kind = "ipv6-acl"
	KindMACACL         Kind = "mac-acl"
	KindARPACL         Kind = "arp-acl"
	KindHostname       Kind = "hostname"

	// Routing policy objects (chunt-otg.6). All are referenced by name from
	// route-maps and routing protocols (see the undefined-ref refSpecs); the
	// kinds also give go-to-definition / find-references / outline support.
	KindPrefixList    Kind = "prefix-list"
	KindCommunityList Kind = "community-list"
	KindAsPathACL     Kind = "as-path-acl"

	// Infrastructure objects (chunt-otg.7). The grammar (v0.3.1) parses
	// every one of these headers as FLAT lines, so extraction is
	// command_line-based (see extractInfrastructure); duplicate-definition
	// warnings follow from the shared pass. KindVRF folds the legacy
	// `ip vrf NAME` namespace into `vrf definition NAME`, and KindSLA folds
	// `rtr N` into `ip sla N` — the legacy and modern forms cannot coexist
	// for the same object in IOS, so conflicts must compare.
	KindVRF            Kind = "vrf"
	KindTrack          Kind = "track"
	KindKeyChain       Kind = "key-chain"
	KindDHCPPool       Kind = "dhcp-pool"
	KindSLA            Kind = "sla"
	KindMonitorSession Kind = "monitor-session"
	KindTimeRange      Kind = "time-range"
)

// Symbol is a named definition site extracted from a parsed document.
type Symbol struct {
	Kind      Kind
	Name      string
	URI       string
	Range     protocol.Range // full section / line range
	NameRange protocol.Range // just the name token (Definition / highlight)
	// Seq carries the explicit `seq N` entry number for kinds whose entries
	// are seq-addressed (currently prefix-lists). "" when the command form
	// has no seq clause. The duplicate-definition pass keys seq-carrying
	// entries on (kind, name, seq) so a second entry in the same list is not
	// a whole-list duplicate; a REPEATED seq is.
	Seq string
	// Guarded reports whether the definition sits inside a Jinja block that
	// renders conditionally or repeatedly ({% if %}/{% elif %}/{% else %}/
	// {% for %}). Guarded definitions satisfy references from any branch
	// (chunt-otg.21 rule 2) and are exempt from duplicate-definition
	// warnings, because mutually exclusive branches legitimately repeat a
	// definition and static analysis cannot prove which branches render.
	Guarded bool
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
	// Guarded mirrors Symbol.Guarded for references inside Jinja blocks.
	Guarded bool
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
		// A negated command REMOVES a definition (`no ip prefix-list PL2
		// seq 10 ...`, `no hostname R9`); it can never introduce one, so
		// prune the whole subtree (chunt-otg.6). Extraction previously
		// descended into negated_statement bodies and registered the negated
		// definition as a real symbol — flagging remove-then-re-add patterns
		// as duplicates.
		if n.Kind() == "negated_statement" {
			return false
		}
		if sym, ok := extractSection(uri, n, content); ok {
			finishSymbol(&sym, n)
			out = append(out, sym)
			// Section bodies do not themselves contain nested section
			// definitions in IOS, but descending is harmless; keep going
			// so jinja loops containing sections (e.g. `{% for %}` emitting
			// interfaces) are still indexed.
		}
		return true
	})
	walkNamed(root, func(n *sitter.Node) bool {
		if n.Kind() == "negated_statement" {
			return false // same prune as the section walk above
		}
		if sym, ok := extractFlat(uri, n, content); ok {
			finishSymbol(&sym, n)
			out = append(out, sym)
		}
		return true
	})
	return out
}

// extractFlat dispatches a flat *_statement node (one that is not a
// hierarchical section) to the appropriate symbol extractor. Currently
// handles access_list_statement (numbered ACL form),
// hostname_statement, and command_line (named-ACL fallback plus the
// chunt-otg.6 routing policy objects and the chunt-otg.7 infrastructure
// objects). Add new flat statement kinds here.
func extractFlat(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	switch n.Kind() {
	case "access_list_statement":
		return extractACL(uri, n, content)
	case "hostname_statement":
		return extractHostname(uri, n, content)
	case "rtr_statement", "text":
		// text: the `-range NAME` catch-all after a bare `time` command_line
		// (grammar v0.3.1 gap — see extractInfrastructure).
		return extractInfrastructure(uri, n, content)
	case "command_line":
		if sym, ok := extractRoutingPolicy(uri, n, content); ok {
			return sym, true
		}
		if sym, ok := extractFamilyACLCommandLine(uri, n, content); ok {
			return sym, true
		}
		if sym, ok := extractInfrastructure(uri, n, content); ok {
			return sym, true
		}
		return extractNamedACLCommandLine(uri, n, content)
	}
	return Symbol{}, false
}

// extractFamilyACLCommandLine recovers the chunt-otg.9 ACL families that the
// grammar parses as FLAT command lines: `ipv6 access-list NAME` and
// `arp access-list NAME` (neither has the standard|extended type word that
// promotes the line into an ip_access_list_section, and the section header's
// leading bare identifier only accepts `ip`/`mac`). Numbered-name
// normalization matches the v4 path: names compare as written, string form.
func extractFamilyACLCommandLine(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	leading, args, argNodes := leadingAndArgs(n, content)
	if leading != "ipv6" && leading != "arp" {
		return Symbol{}, false
	}
	if len(args) < 2 || args[0] != "access-list" {
		return Symbol{}, false
	}
	kind := KindARPACL
	if leading == "ipv6" {
		kind = KindACLv6
	}
	nameNode := argNodes[1]
	return Symbol{
		Kind:      kind,
		Name:      textOf(nameNode, content),
		URI:       uri,
		Range:     nodeRange(n),
		NameRange: nodeRange(nameNode),
	}, true
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
	// resolveKind, when non-nil, overrides kind from the header node (used
	// for section nodes shared by several families — ip_access_list_section
	// carries both `ip` and `mac` access-lists; chunt-otg.9).
	resolveKind func(header *sitter.Node, content []byte) Kind
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
	//
	// The SAME section node also carries `mac access-list extended NAME`
	// (the header rule starts with a bare identifier, so `mac` lexes into
	// it too — chunt-otg.9). resolveKind picks the family from the header's
	// leading identifier so a MAC ACL never satisfies an `ip access-group`
	// reference and vice versa.
	{
		sectionKind: "ip_access_list_section", kind: KindACL, headerKind: "ip_access_list_header", nameField: "name",
		resolveKind: aclFamilyKind,
	},
}

func extractSection(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	for _, sp := range sectionSpecs {
		if n.Kind() != sp.sectionKind {
			continue
		}
		// A vrf definition's address-family sub-blocks (chunt-otg.7): the
		// grammar (v0.3.1) has no vrf section, so a `vrf definition BLUE`
		// body surfaces its `address-family ipv4` as a TOP-LEVEL sibling
		// section. Left alone, every VRF carrying the same AF name would
		// register a duplicate address-family symbol. VRF sub-modes are not
		// routing-process address-families, so they register no symbol at
		// all (cross-repo note: a real vrf_section in the grammar would
		// scope these properly).
		if sp.sectionKind == "address_family_section" && precededByVRFHeader(n, content) {
			return Symbol{}, false
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
		if sp.resolveKind != nil {
			sym.Kind = sp.resolveKind(header, content)
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

// precededByVRFHeader reports whether an address_family_section's previous
// siblings chain back to a `vrf definition`/`ip vrf` header line (allowing
// sibling AF blocks of the same VRF in between, e.g. ipv4 then ipv6). Any
// other intervening node ends the scan — the AF belongs to the config root
// or a routing process.
func precededByVRFHeader(n *sitter.Node, content []byte) bool {
	for sib := n.PrevNamedSibling(); sib != nil; sib = sib.PrevNamedSibling() {
		if sib.Kind() == "address_family_section" {
			continue // earlier AF block of the same VRF: keep scanning back
		}
		if sib.Kind() == "command_line" {
			txt := textOf(sib, content)
			return strings.HasPrefix(txt, "vrf definition ") || strings.HasPrefix(txt, "ip vrf ")
		}
		return false
	}
	return false
}

// aclFamilyKind resolves the ACL family from an ip_access_list_header's
// leading identifier: `mac access-list ...` sections carry KindMACACL;
// everything else (the bare `ip` identifier) is the IPv4 KindACL (chunt-otg.9).
func aclFamilyKind(header *sitter.Node, content []byte) Kind {
	if first := header.Child(0); first != nil && textOf(first, content) == "mac" {
		return KindMACACL
	}
	return KindACL
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

// extractRoutingPolicy extracts the chunt-otg.6 routing policy objects from
// flat command_line nodes. All three families parse flat today (the grammar
// models them as generic command lines — see the epic's grammar note), so
// extraction is hostname-style: leading token + arg-position name.
//
//	ip prefix-list NAME [seq N] permit|deny ...   (KindPrefixList)
//	ipv6 prefix-list NAME [seq N] permit|deny ...  (KindPrefixList)
//	ip community-list standard|expanded NAME ...   (KindCommunityList)
//	ip community-list N permit|deny ...            (KindCommunityList, numbered)
//	ip as-path access-list N permit|deny ...       (KindAsPathACL, numbered)
//
// Numbered forms normalize the number to its string form so definitions and
// references compare as written (no padding), mirroring extractACL.
func extractRoutingPolicy(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	leading, args, argNodes := leadingAndArgs(n, content)
	if len(args) < 2 {
		return Symbol{}, false
	}
	switch {
	case (leading == "ip" || leading == "ipv6") && args[0] == "prefix-list":
		nameNode := argNodes[1]
		sym := Symbol{
			Kind:      KindPrefixList,
			Name:      textOf(nameNode, content),
			URI:       uri,
			Range:     nodeRange(n),
			NameRange: nodeRange(nameNode),
		}
		// `seq N` clause: the explicit entry number. Entries without seq are
		// auto-numbered by IOS and carry no static identity.
		if len(args) >= 4 && args[2] == "seq" {
			sym.Seq = args[3]
		}
		return sym, true
	case leading == "ip" && args[0] == "community-list":
		// `ip community-list standard|expanded NAME ...` vs the numbered form
		// `ip community-list <1-199> permit|deny ...`.
		if len(args) >= 3 && (args[1] == "standard" || args[1] == "expanded") {
			nameNode := argNodes[2]
			return Symbol{
				Kind:      KindCommunityList,
				Name:      textOf(nameNode, content),
				URI:       uri,
				Range:     nodeRange(n),
				NameRange: nodeRange(nameNode),
			}, true
		}
		nameNode := argNodes[1]
		return Symbol{
			Kind:      KindCommunityList,
			Name:      textOf(nameNode, content),
			URI:       uri,
			Range:     nodeRange(n),
			NameRange: nodeRange(nameNode),
		}, true
	case leading == "ip" && args[0] == "as-path" && len(args) >= 3 && args[1] == "access-list":
		nameNode := argNodes[2]
		return Symbol{
			Kind:      KindAsPathACL,
			Name:      textOf(nameNode, content),
			URI:       uri,
			Range:     nodeRange(n),
			NameRange: nodeRange(nameNode),
		}, true
	}
	return Symbol{}, false
}

// extractInfrastructure extracts the chunt-otg.7 infrastructure object
// definitions from flat nodes. The grammar (v0.3.1) parses every header as
// a generic line, so this is hostname-style: leading tokens + arg-position
// name. Cross-repo note: a future grammar with real sections for these
// (vrf_section, track_section, ...) replaces the flat path via sectionSpecs.
//
//	vrf definition NAME          (KindVRF)
//	ip vrf NAME                  (KindVRF, legacy; exactly-2-args guard keeps
//	                            the interface-level `ip vrf forwarding X` out)
//	track N ...                 (KindTrack, numeric N)
//	key chain NAME              (KindKeyChain)
//	ip dhcp pool NAME           (KindDHCPPool)
//	ip sla N                    (KindSLA, numeric N)
//	rtr N                       (KindSLA, rtr_statement; legacy ip sla)
//	monitor session N ...       (KindMonitorSession, numeric N; additive —
//	                            source/destination lines configure the SAME
//	                            session, so the kind is exempt from the
//	                            duplicate pass)
//	time-range NAME             (KindTimeRange)
//
// time-range needs a workaround: the grammar has no `time-range` keyword,
// so the lexer emits command_line "time" (dropping the rest of the word)
// followed by a catch-all `text` node carrying "-range NAME". Extraction
// matches that shape (requires the preceding bare-`time` command_line) and
// is superseded once the grammar promotes the keyword.
func extractInfrastructure(uri string, n *sitter.Node, content []byte) (Symbol, bool) {
	kind, nameNode, ok := infrastructureDefinition(n, content)
	if !ok {
		return Symbol{}, false
	}
	name := textOf(nameNode, content)
	if kind == KindTimeRange {
		// The text node carries "-range NAME"; the name is its first field
		// after the prefix (time-range names have no spaces). NameRange
		// necessarily spans the whole catch-all node including the prefix —
		// a grammar keyword would give a precise range.
		if rest, cut := strings.CutPrefix(strings.TrimSpace(name), "-range "); cut {
			name = strings.Fields(rest)[0]
		}
	}
	if name == "" {
		return Symbol{}, false
	}
	return Symbol{
		Kind:      kind,
		Name:      name,
		URI:       uri,
		Range:     nodeRange(n),
		NameRange: nodeRange(nameNode),
	}, true
}

// infrastructureDefinition classifies one flat node into (kind, name node).
func infrastructureDefinition(n *sitter.Node, content []byte) (Kind, *sitter.Node, bool) {
	if n.Kind() == "text" {
		return timeRangeFromText(n, content)
	}
	if n.Kind() == "rtr_statement" {
		arg := n.ChildByFieldName("arg")
		if arg == nil || !isAllDigits(textOf(arg, content)) {
			return "", nil, false
		}
		return KindSLA, arg, true
	}
	return infrastructureCommandLine(n, content)
}

// timeRangeFromText recovers a time-range definition from the `-range NAME`
// catch-all text node that follows the bare `time` command_line (grammar
// v0.3.1 gap; see the function comment on extractInfrastructure).
func timeRangeFromText(n *sitter.Node, content []byte) (Kind, *sitter.Node, bool) {
	prev := n.PrevNamedSibling()
	if prev == nil || prev.Kind() != "command_line" || strings.TrimSpace(textOf(prev, content)) != "time" {
		return "", nil, false
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(textOf(n, content)), "-range ")
	if !ok || len(strings.Fields(rest)) == 0 {
		return "", nil, false
	}
	return KindTimeRange, n, true
}

// infraForm is one command_line shape in the chunt-otg.7 infrastructure
// registry. words is the command word prefix (leading token + leading args,
// e.g. ["ip", "dhcp", "pool"]); nameIdx is the args index carrying the
// object name; numeric requires the name to be a bare number (track/sla/
// session numbers, filtering `track timer interface`-style forms); exactArgs
// pins the total arg count (the legacy `ip vrf NAME` header has no extra
// words, so exactly-2 keeps the interface-level `ip vrf forwarding X` out).
var infraForms = []struct {
	words     []string
	nameIdx   int
	minArgs   int
	exactArgs int
	numeric   bool
	kind      Kind
}{
	{words: []string{"vrf", "definition"}, nameIdx: 1, minArgs: 2, kind: KindVRF},
	{words: []string{"ip", "vrf"}, nameIdx: 1, minArgs: 2, exactArgs: 2, kind: KindVRF},
	{words: []string{"track"}, nameIdx: 0, minArgs: 1, numeric: true, kind: KindTrack},
	{words: []string{"key", "chain"}, nameIdx: 1, minArgs: 2, kind: KindKeyChain},
	{words: []string{"ip", "dhcp", "pool"}, nameIdx: 2, minArgs: 3, kind: KindDHCPPool},
	{words: []string{"ip", "sla"}, nameIdx: 1, minArgs: 2, numeric: true, kind: KindSLA},
	{words: []string{"monitor", "session"}, nameIdx: 1, minArgs: 2, numeric: true, kind: KindMonitorSession},
}

// infrastructureCommandLine classifies a command_line into one of the
// chunt-otg.7 infrastructure kinds via the infraForms registry.
func infrastructureCommandLine(n *sitter.Node, content []byte) (Kind, *sitter.Node, bool) {
	leading, args, argNodes := leadingAndArgs(n, content)
	if len(args) == 0 {
		return "", nil, false
	}
	full := append([]string{leading}, args...)
	for i := range infraForms {
		f := &infraForms[i]
		if len(full) < len(f.words) || !wordsEqual(full[:len(f.words)], f.words) {
			continue
		}
		if len(args) < f.minArgs || (f.exactArgs > 0 && len(args) != f.exactArgs) {
			continue
		}
		if f.numeric && !isAllDigits(args[f.nameIdx]) {
			continue
		}
		return f.kind, argNodes[f.nameIdx], true
	}
	return "", nil, false
}

// wordsEqual compares two lowercase command-word slices.
func wordsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isAllDigits reports whether s is a non-empty decimal number (track/sla
// session numbers), filtering other-arg forms like `track timer interface`.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
// A `"*"` element matches any single arg token — used for `neighbor <ip>
// prefix-list NAME` where the IP is variable (chunt-otg.10). ArgIndex is the
// 0-based offset of the referenced name within the trailing args that follow
// the Leading prefix.
//
// Examples:
//
//	{["ip", "access-group"], 0, KindACL}            -> ip access-group NAME in|out
//	{["match", "ip", "address"], 0, KindACL}        -> match ip address NAME
//	{["service", "-policy"], KindPolicyMap, LastArg} -> service-policy [type ...] <dir> NAME
//	    (`service-policy` parses as service_statement(args=["-policy", dir, NAME])
//	    because `service` is a prec-2 keyword that splits the hyphenated literal)
//	{["class"], 0, KindClassMap}                    -> class NAME (inside policy-map body)
//	{["neighbor", "*", "prefix-list"], 0, KindPrefixList} -> neighbor 1.1.1.1 prefix-list NAME in
//
// When several refSpecs match one command line, the LONGEST Leading wins
// (longest-match-wins): `match ip address prefix-list PL1` must satisfy the
// 4-token prefix-list spec, not shadow into the 3-token ACL spec with a
// reference literally named "prefix-list".
type refSpec struct {
	Leading  []string
	ArgIndex int
	Kind     Kind
	// Varargs: every arg from ArgIndex onward is a referenced name
	// (`match ip address prefix-list PL1 PL2` cites two lists). Trailing
	// direction keywords (in|out) are skipped.
	Varargs bool
	// Keywords switches to keyword-scan matching (chunt-otg.11): the
	// referenced name follows the first arg equal to one of these keywords,
	// at ANY position. Needed when the name sits after a variable number of
	// tokens (`redistribute ospf 1 route-map RM` vs `redistribute static
	// route-map RM`) or when several keywords can each cite a map
	// (`neighbor 2.2.2.2 advertise-map ADV exist-map NONEX`). ArgIndex is
	// ignored when Keywords is set.
	Keywords []string
	// LastArg: the referenced name is the command's final arg — the
	// service-policy shape, where the policy-map name always comes last
	// regardless of how many type/direction keywords precede it:
	// `service-policy input PM`, `service-policy type control output CP`,
	// and the child-policy form `service-policy PM-CHILD` inside a
	// policy-map class body (chunt-otg.14). ArgIndex/Keywords ignored.
	LastArg bool
	// IfaceShape: only emit the arg when it LOOKS like an interface name
	// (leading letter, not a known non-interface word). Needed where the
	// same arg position carries either an interface or a next-hop IP
	// (`ip route <prefix> <mask> <iface-or-ip>` — chunt-otg.16).
	IfaceShape bool
}

var refSpecs = []refSpec{
	{Leading: []string{"ip", "access-group"}, ArgIndex: 0, Kind: KindACL},
	{Leading: []string{"access-class"}, ArgIndex: 0, Kind: KindACL},
	{Leading: []string{"match", "ip", "address"}, ArgIndex: 0, Kind: KindACL, Varargs: true},
	{Leading: []string{"ip", "policy", "route-map"}, ArgIndex: 0, Kind: KindRouteMap},
	// service-policy attach points (chunt-otg.14): the policy-map name is
	// always the last arg across the plain, type-qualified, and
	// child-policy-in-class-body shapes.
	{Leading: []string{"service", "-policy"}, Kind: KindPolicyMap, LastArg: true},
	{Leading: []string{"class"}, ArgIndex: 0, Kind: KindClassMap},
	{Leading: []string{"switchport", "access", "vlan"}, ArgIndex: 0, Kind: KindVlan},

	// Prefix-list references (chunt-otg.10).
	{Leading: []string{"match", "ip", "address", "prefix-list"}, ArgIndex: 0, Kind: KindPrefixList, Varargs: true},
	{Leading: []string{"match", "ipv6", "address", "prefix-list"}, ArgIndex: 0, Kind: KindPrefixList, Varargs: true},
	{Leading: []string{"neighbor", "*", "prefix-list"}, ArgIndex: 0, Kind: KindPrefixList},
	{Leading: []string{"distribute-list", "prefix"}, ArgIndex: 0, Kind: KindPrefixList},
	{Leading: []string{"distribute-list", "prefix-list"}, ArgIndex: 0, Kind: KindPrefixList},

	// Route-map references from routing protocols (chunt-otg.11). The
	// name follows a keyword at a variable position, so these scan.
	// (`table-map NAME` also cites a route-map but lexes as command_line(table)
	// + an unnamed text node — needs a grammar statement rule; see the
	// grammar-repo bead referenced from chunt-otg.11.)
	{
		Leading: []string{"neighbor"}, Kind: KindRouteMap,
		Keywords: []string{"route-map", "advertise-map", "unsuppress-map", "exist-map", "non-exist-map"},
	},
	{Leading: []string{"redistribute"}, Kind: KindRouteMap, Keywords: []string{"route-map"}},
	{Leading: []string{"distribute-list", "route-map"}, ArgIndex: 0, Kind: KindRouteMap},

	// ACL reference completion (chunt-otg.13). The bare distribute-list
	// spec also covers `neighbor <ip> distribute-list <N|NAME>` — the
	// grammar emits the distribute-list as its own statement nested in the
	// neighbor line. The longer distribute-list specs above shadow this one
	// for prefix-list / route-map forms.
	{Leading: []string{"distribute-list"}, ArgIndex: 0, Kind: KindACL},
	// IPv6 ACLs are their own family (chunt-otg.9): a v4 ACL of the same
	// name must not satisfy a traffic-filter reference.
	{Leading: []string{"ipv6", "traffic-filter"}, ArgIndex: 0, Kind: KindACLv6},
	// `snmp-server community STR [view V] RO|RW <N|NAME>` — the ACL follows
	// the RO|RW keyword when present (a trailing RO with no ACL cites
	// nothing).
	{Leading: []string{"snmp-server"}, Kind: KindACL, Keywords: []string{"RO", "RW"}},
	// NAT bindings: `ip nat inside|outside source|destination list <N|NAME> ...`.
	{Leading: []string{"ip", "nat"}, Kind: KindACL, Keywords: []string{"list"}},

	// Community-list and as-path ACL references in route-map bodies
	// (chunt-otg.12). Numbered and named forms both compare as written
	// (string normalization).
	{Leading: []string{"match", "community"}, ArgIndex: 0, Kind: KindCommunityList},
	{Leading: []string{"match", "as-path"}, ArgIndex: 0, Kind: KindAsPathACL, Varargs: true},

	// Interface-name references (chunt-otg.16). Exact spelling and exact
	// sub-interface names (Gi0/1.100) — IOS abbreviations are not valid in
	// these positions, so literal string comparison is correct.
	// Next-hop IPs / `dhcp` / the definition-less blackhole target Null0
	// are filtered by IfaceShape.
	{Leading: []string{"ip", "route"}, ArgIndex: 2, Kind: KindInterface, IfaceShape: true},
	{Leading: []string{"ip", "route", "vrf", "*"}, ArgIndex: 2, Kind: KindInterface, IfaceShape: true},
	{Leading: []string{"match", "interface"}, ArgIndex: 0, Kind: KindInterface, Varargs: true},
	{Leading: []string{"set", "interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"logging", "source-interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"ntp", "source"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"snmp-server", "trap-source"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"ip", "tftp", "source-interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"ip", "ssh", "source-interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"ip", "ftp", "source-interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"ip", "domain", "source-interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"ip", "tacacs", "source-interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"ip", "radius", "source-interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"monitor", "session", "*", "*", "interface"}, ArgIndex: 0, Kind: KindInterface},
	{Leading: []string{"standby", "*", "track"}, ArgIndex: 0, Kind: KindInterface},
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
			finishReference(&r, n)
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
		// Longest-match-wins: emit references for the single best-matching
		// spec only, so a more specific pattern shadows the shorter prefix
		// that would otherwise misread a keyword arg as a name.
		best := -1
		for i, spec := range refSpecs {
			if prefixMatchWild(fullSeq, spec.Leading) && (best < 0 || len(refSpecs[i].Leading) > len(refSpecs[best].Leading)) {
				best = i
			}
		}
		if best >= 0 {
			refs = appendSpecReferences(refs, refSpecs[best], argNodes, n, uri, content)
		}
		return true
	})
	return refs
}

// appendSpecReferences emits the reference(s) for one matched refSpec:
// keyword-scan mode (Keywords set — the name follows each keyword occurrence
// at any position), the single arg at (len(Leading)-1 + ArgIndex), or — for
// Varargs specs — every arg from that index onward, skipping trailing
// direction keywords (in|out). Each emitted reference gets the chunt-otg.21
// Jinja pass applied.
func appendSpecReferences(refs []Reference, spec refSpec, argNodes []*sitter.Node, n *sitter.Node, uri string, content []byte) []Reference {
	emit := func(nameNode *sitter.Node) []Reference {
		tok := textOf(nameNode, content)
		if spec.IfaceShape && !looksLikeIfaceName(tok) {
			return refs
		}
		ref := Reference{
			Kind:  spec.Kind,
			Name:  tok,
			URI:   uri,
			Range: nodeRange(nameNode),
		}
		finishReference(&ref, n)
		return append(refs, ref)
	}
	if spec.LastArg {
		if len(argNodes) == 0 {
			return refs
		}
		return emit(argNodes[len(argNodes)-1])
	}
	if len(spec.Keywords) > 0 {
		kwSet := make(map[string]bool, len(spec.Keywords))
		for _, k := range spec.Keywords {
			kwSet[k] = true
		}
		for i := 0; i+1 < len(argNodes); i++ {
			if kwSet[textOf(argNodes[i], content)] {
				refs = emit(argNodes[i+1])
			}
		}
		return refs
	}
	// argNodes parallels fullSeq[1:] (leading is Child(0); args are the
	// rest). Index into argNodes by (len(Leading)-1 + ArgIndex).
	idx := len(spec.Leading) - 1 + spec.ArgIndex
	if idx >= len(argNodes) {
		return refs
	}
	if !spec.Varargs {
		return emit(argNodes[idx])
	}
	for _, nameNode := range argNodes[idx:] {
		t := textOf(nameNode, content)
		if t == "in" || t == "out" {
			continue
		}
		refs = emit(nameNode)
	}
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

// prefixMatchWild reports whether a begins with every element of b in order,
// with wildcard support: a "*" element of b matches any single element of a
// (chunt-otg.10).
func prefixMatchWild(a, b []string) bool {
	if len(a) < len(b) {
		return false
	}
	for i := range b {
		if b[i] != "*" && a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Jinja-aware definition/reference resolution (chunt-otg.21)
// ---------------------------------------------------------------------------
//
// Real .ios.j2 templates compute object names at render time (`ip access-group
// {{ acl_name }} in`) and guard definitions with `{% if %}` branches. Static
// analysis cannot resolve computed names and cannot prove which branches
// render, so the diagnostics passes must gate on four rules:
//
//  1. A name containing a Jinja expression/statement tag is UNRESOLVABLE —
//     never emit undefined-* for it (HasJinjaExpression).
//  2. Definitions guarded by {% if %} still satisfy references from any
//     branch; the symbol table already indexes them, and the Guarded flag
//     lets the duplicate-definition pass skip them (mutually exclusive
//     branches legitimately repeat a definition).
//  3. {% for %}-generated definitions (name built from the loop variable)
//     register as definitions — extraction already descends into Jinja
//     blocks; the computed name is normalized for consistent comparison.
//  4. Whitespace-control and whitespace inside a name token are stripped
//     before comparison so `{{ name }}`, `{{name}}`, and `{{- name -}}`
//     compare equal (NormalizeJinjaName).

// jinjaGuardKinds are the AST node kinds that wrap conditionally or
// repeatedly rendered config: Jinja block statements from the grammar's
// template layer.
var jinjaGuardKinds = map[string]bool{
	"if_statement":       true, // {% if ... %}
	"elif_statement":     true, // {% elif ... %}
	"else_statement":     true, // {% else %}
	"for_statement":      true, // {% for ... %}
	"for_else_statement": true, // {% for %}...{% else %}
}

// HasJinjaExpression reports whether name contains a Jinja expression or
// statement tag ("{{ ... }}" or "{% ... %}"), i.e. the name is computed at
// render time and cannot be resolved statically. Such references must never
// produce undefined-* diagnostics (rule 1), and computed definition names
// must be compared only in normalized form (rule 4).
func HasJinjaExpression(name string) bool {
	return strings.Contains(name, "{{") || strings.Contains(name, "{%")
}

// NormalizeJinjaName canonicalizes a name token for comparison. Literal
// names pass through unchanged (Jinja templates are case-sensitive on
// literals, and IOS object names are too). Computed names (rule 4) have all
// whitespace removed and whitespace-control markers (`{{-`, `-}}`) trimmed,
// so `{{ vlan_id }}`, `{{vlan_id}}`, and `{{- vlan_id -}}` compare equal.
func NormalizeJinjaName(name string) string {
	if !HasJinjaExpression(name) {
		return name
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	s := b.String()
	// Trim whitespace-control markers without eating the braces.
	if strings.HasPrefix(s, "{{-") {
		s = "{{" + s[3:]
	}
	if strings.HasSuffix(s, "-}}") {
		s = s[:len(s)-3] + "}}"
	}
	return s
}

// isJinjaGuarded reports whether n sits inside a Jinja block that renders
// conditionally or repeatedly ({% if %}/{% elif %}/{% else %}/{% for %}),
// mirroring isNegated's ancestor walk.
func isJinjaGuarded(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if jinjaGuardKinds[p.Kind()] {
			return true
		}
	}
	return false
}

// finishSymbol applies the Jinja-awareness pass (chunt-otg.21) to an
// extracted symbol: Guarded ancestry and normalized computed names. Literal
// names are returned unchanged by NormalizeJinjaName.
func finishSymbol(sym *Symbol, n *sitter.Node) {
	sym.Name = NormalizeJinjaName(sym.Name)
	sym.Guarded = isJinjaGuarded(n)
}

// finishReference applies the Jinja-awareness pass (chunt-otg.21) to an
// extracted reference: Guarded ancestry and normalized computed names.
// Literal names are returned unchanged by NormalizeJinjaName.
func finishReference(ref *Reference, n *sitter.Node) {
	ref.Name = NormalizeJinjaName(ref.Name)
	ref.Guarded = isJinjaGuarded(n)
}

// ifaceShapeSkip lists words that sit where an interface name would in
// `ip route` but never name an interface: the dhcp next-hop form and the
// definition-less blackhole target Null0 (chunt-otg.16).
var ifaceShapeSkip = map[string]bool{
	"dhcp":  true,
	"null0": true,
}

// looksLikeIfaceName reports whether tok can be an interface name: a leading
// letter (next-hop IPs are digit-leading) and not a skip-listed word.
func looksLikeIfaceName(tok string) bool {
	if tok == "" {
		return false
	}
	c := tok[0]
	if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
		return false
	}
	return !ifaceShapeSkip[strings.ToLower(tok)]
}
