package cisco_ios_jinja2

import (
	"strings"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/section"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Cross-file diagnostics (workspace mode; see workspace.go for the
// directory-as-one-config model).
//
// The single-file repeated-command pass (diagnostics_repeated.go) scopes its
// checks to section NODES within one document. This pass lifts the same
// rules to LOGICAL scopes spanning the whole directory group:
//
//   - The root scope "" merges every file's top-level commands.
//   - Each section's identity is its header text (whitespace-collapsed),
//     CHAINED to its enclosing section's identity. `router bgp 65001` in
//     base.ios.j2 and bgp.ios.j2 is one logical section; an
//     `address-family ipv4 unicast` under `router bgp 65001` is distinct
//     from the same header under `router ospf 1`; a `class VOICE` block
//     under `policy-map PM-OUT` merges only with class VOICE blocks under
//     another file's PM-OUT.
//
// Each logical scope's command stream is the concatenation of its section
// instances' direct command-like children, in canonical group order
// (sorted filename) then document order. That merged stream feeds a
// crossFileOnly repeatedTracker, so:
//
//   - conflicting scalars across files (router bgp router-id 1.1.1.1 in one
//     file, 2.2.2.2 in another) surface as redefined-setting Errors
//     anchored on the later definition, with relatedInformation jumping to
//     the first (possibly in another file);
//   - identical duplicate lines across files surface as duplicate-command
//     Warnings;
//   - findings whose definitions live in the SAME file are suppressed here
//     — the single-file pass already owns those, so nothing is reported
//     twice.
//
// Section identity is literal header text: templates using variables
// (`interface {{ name }}`) merge only when the variable text itself
// matches. Resolving template variables is out of scope for v1.
//
// Scope ordering determinism: scopes are processed in discovery order
// (group order, then document order), so each URI's diagnostic bucket — and
// therefore what we publish — is stable across runs.

// collectWorkspaceScopes walks every document in the group, groups section
// nodes into logical scopes, and runs the merged-stream repeated-command
// rules over each. Returns the diagnostics each URI gained from other
// files' context, keyed by URI.
func (f *CiscoIOSFeature) collectWorkspaceScopes(g *docGroup) map[string][]protocol.Diagnostic {
	type member struct {
		node *sitter.Node
		doc  *document.Document
	}
	scopes := make(map[string][]member)
	var scopeOrder []string
	add := func(id string, m member) {
		if _, seen := scopes[id]; !seen {
			scopeOrder = append(scopeOrder, id)
		}
		scopes[id] = append(scopes[id], m)
	}

	for _, uri := range g.order {
		tree := f.trees[uri]
		if tree == nil || tree.RootNode() == nil {
			continue
		}
		doc := g.docs[uri]
		if doc == nil {
			continue
		}
		root := tree.RootNode()
		add("", member{node: root, doc: doc})
		collectSections(root, "", doc, func(id string, sec *sitter.Node) {
			add(id, member{node: sec, doc: doc})
		})
	}

	out := make(map[string][]protocol.Diagnostic)
	for _, id := range scopeOrder {
		t := newRepeatedTracker(f.keyword, true)
		for _, m := range scopes[id] {
			var batch []protocol.Diagnostic
			f.feedScope(t, &batch, m.doc, m.node)
			if len(batch) > 0 {
				out[m.doc.URI] = append(out[m.doc.URI], batch...)
			}
		}
		// Companion warnings: a cross-file conflict participates in BOTH
		// files. The Error above anchors on the later definition; each FIRST
		// definition now gets a superseded-setting Warning anchored on it,
		// bucketed by the first definition's URI — which differs from the
		// member being fed when the conflict was detected, hence the
		// record-then-emit split.
		for _, cf := range t.conflicts {
			out[cf.first.doc.URI] = append(out[cf.first.doc.URI], cf.companion())
		}
	}
	return out
}

// collectSections recursively walks n's named subtree, invoking add for
// every section node with its parent-chained scope ID. Sections nested
// inside Jinja blocks ({% for %} emitting interfaces) are discovered too,
// mirroring the single-file pass's named-node walk.
func collectSections(n *sitter.Node, parentID string, doc *document.Document, add func(id string, sec *sitter.Node)) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		if section.IsKnownSectionKind(c.Kind()) {
			id := scopeID(parentID, c, doc.Content)
			add(id, c)
			collectSections(c, id, doc, add)
		} else {
			collectSections(c, parentID, doc, add)
		}
	}
}

// scopeID chains a section's normalized header text onto its parent's ID.
// The empty parent ID (top-level sections) yields "\x00<header>"; nested
// sections accumulate, e.g. "\x00router bgp 65001\x00address-family ipv4
// unicast". \x00 cannot appear in header text, so the split is unambiguous.
func scopeID(parentID string, sec *sitter.Node, content []byte) string {
	return parentID + "\x00" + normHeaderText(sec, content)
}

// normHeaderText returns the section's header node text with whitespace
// collapsed to single spaces, so `router bgp  65001` and `router bgp 65001`
// share an identity. Case is preserved (IOS interface/ACL names are
// conventionally cased); empty when the header node is missing.
func normHeaderText(sec *sitter.Node, content []byte) string {
	header := firstNamedChild(sec)
	if header == nil {
		return ""
	}
	text := string(content[header.StartByte():header.EndByte()])
	return strings.Join(strings.Fields(text), " ")
}

// firstNamedChild returns n's first named child, or nil.
func firstNamedChild(n *sitter.Node) *sitter.Node {
	if n == nil || n.NamedChildCount() == 0 {
		return nil
	}
	return n.NamedChild(0)
}
