// Package section resolves the enclosing Cisco IOS configuration-mode section
// (and, for routing contexts, the routing protocol) for a tree-sitter AST node.
// It is the single source of truth for the AST section-kind → keyword.Section
// ID mapping, shared by completion and the diagnostic passes (chunt-mpc).
//
// Before this package existed, the same mapping table was duplicated in
// completion.go (sectionForNodeMap) and consulted inline in diagnostics_section
// .go. That duplication drifted; the helper here removes it.
//
// Note: symbols.go (internal/symbols) has its OWN sectionSpecs table that drives
// Symbol extraction (LSP Kind + name field) — a DIFFERENT concern from the
// keyword.Section mapping here. See the cross-reference comment there.
package section

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// sectionSpec maps a tree-sitter *_section node kind to its keyword.Section ID.
type sectionSpec struct {
	astKind    string
	keywordSec string
}

// sectionSpecs is the single source of truth for the AST section-kind →
// keyword.Section mapping. Add new sections here and only here. The named-ACL
// section (ip_access_list_section) is intentionally ABSENT: its keyword.Section
// depends on the per-instance header type (standard/extended), so it is
// resolved dynamically by resolveACLSection inside EnclosingSection rather than
// by a static table entry.
var sectionSpecs = []sectionSpec{
	{"interface_section", "config-if"},
	{"router_section", "config-router"},
	{"address_family_section", "config-router-af"},
	{"route_map_section", "config-route-map"},
	{"class_map_section", "config-cmap"},
	{"policy_map_section", "config-pmap"},
	{"policy_map_class_section", "config-pmap-c"},
	{"vlan_section", "config-vlan"},
	{"line_section", "config-line"},
}

// kindToSection indexes sectionSpecs by AST kind for O(1) lookup.
var kindToSection = func() map[string]string {
	m := make(map[string]string, len(sectionSpecs))
	for _, s := range sectionSpecs {
		m[s.astKind] = s.keywordSec
	}
	return m
}()

// sectionHeaderKinds is the set of every *_section kind the grammar emits,
// including ip_access_list_section (which resolveACLSection handles but which
// is not in sectionSpecs). IsSectionHeader / IsKnownSectionKind consult this so
// the syntax diagnostic's missing-eos downgrade covers ACL sections too.
var sectionHeaderKinds = func() map[string]bool {
	m := make(map[string]bool, len(sectionSpecs)+1)
	for _, s := range sectionSpecs {
		m[s.astKind] = true
	}
	m["ip_access_list_section"] = true
	return m
}()

// SectionForKind returns the keyword.Section ID for an AST section kind, or ""
// if the kind is not one of the statically mapped sections (in particular,
// ip_access_list_section returns "" here — use EnclosingSection for ACL
// resolution).
func SectionForKind(kind string) string {
	return kindToSection[kind]
}

// IsKnownSectionKind reports whether kind is a hierarchical *_section kind the
// grammar emits, including ip_access_list_section.
func IsKnownSectionKind(kind string) bool {
	return sectionHeaderKinds[kind]
}

// IsSectionHeader reports whether node is itself a section header node (one of
// the *_section kinds). This answers a different question from EnclosingSection:
// it identifies whether a node IS a section (used by the syntax diagnostic's
// missing-eos downgrade), not what section ENCLOSESES a node.
func IsSectionHeader(node *sitter.Node) bool {
	if node == nil {
		return false
	}
	return sectionHeaderKinds[node.Kind()]
}

// EnclosingSection walks node's ancestors and returns the keyword.Section ID of
// the innermost enclosing section plus, for routing contexts, the routing
// protocol. Defaults to ("config", "") when no section ancestor is found.
//
// The protocol is non-empty only inside a router_section or an
// address_family_section (which inherits its protocol from the enclosing
// router_section). It is read from the router_header's required "protocol"
// field, whose grammar rule (routing_protocol) currently matches only "bgp" and
// "ospf". Named-ACL sections resolve to config-std-nacl / config-ext-nacl based
// on the header's "type" field.
func EnclosingSection(node *sitter.Node, content []byte) (section, protocol string) {
	for n := node; n != nil; n = n.Parent() {
		switch n.Kind() {
		case "ip_access_list_section":
			return resolveACLSection(n, content), ""
		default:
			if s, ok := kindToSection[n.Kind()]; ok {
				return s, protocolFor(n, content)
			}
		}
	}
	return "config", ""
}

// protocolFor returns the routing protocol for a router_section or
// address_family_section node, or "" for any other section. An address-family
// section inherits the protocol of its enclosing router_section (IOS sub-modes
// do not redeclare the protocol).
func protocolFor(sectionNode *sitter.Node, content []byte) string {
	switch sectionNode.Kind() {
	case "router_section":
		return readRouterProtocol(sectionNode, content)
	case "address_family_section":
		for p := sectionNode.Parent(); p != nil; p = p.Parent() {
			if p.Kind() == "router_section" {
				return readRouterProtocol(p, content)
			}
		}
	}
	return ""
}

// readRouterProtocol reads the router_header's "protocol" field (a
// routing_protocol node whose text is "bgp" or "ospf"). Returns "" if the
// router_header or its protocol field is absent.
func readRouterProtocol(routerSection *sitter.Node, content []byte) string {
	header := namedChildByKind(routerSection, "router_header")
	if header == nil {
		return ""
	}
	pf := header.ChildByFieldName("protocol")
	if pf == nil {
		return ""
	}
	return string(content[pf.StartByte():pf.EndByte()])
}

// resolveACLSection reads the ip_access_list_header's "type" field to determine
// whether this is a standard or extended ACL section. Moved here from
// completion.go so both completion and the diagnostic passes share one
// implementation (chunt-mpc). Defaults to config-ext-nacl when the type field
// is absent or unrecognized.
func resolveACLSection(sectionNode *sitter.Node, content []byte) string {
	header := namedChildByKind(sectionNode, "ip_access_list_header")
	if header == nil {
		return "config-ext-nacl"
	}
	typeNode := header.ChildByFieldName("type")
	if typeNode == nil {
		return "config-ext-nacl"
	}
	switch string(content[typeNode.StartByte():typeNode.EndByte()]) {
	case "standard":
		return "config-std-nacl"
	case "extended":
		return "config-ext-nacl"
	}
	return "config-ext-nacl"
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
