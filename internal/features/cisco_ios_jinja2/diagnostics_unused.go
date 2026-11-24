package cisco_ios_jinja2

import (
	"fmt"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
)

// Unused-definition diagnostics (chunt-otg.23): the inverse of the
// undefined-reference pass. A named definition of a referenceable kind with
// NO reference anywhere in scope (single file, or any file of a directory
// group) is a Hint — high noise risk by nature, so severity stays at Hint
// and the kind set is conservative:
//
//   - kinds whose objects are endpoints rather than reference targets
//     (interfaces, router processes, line/redundancy/hostname, dhcp pools,
//     ip sla sessions, monitor sessions) are excluded — most of their
//     instances are legitimately unreferenced or referenced through
//     channels the ref specs do not model (key chains via routing auth,
//     SLA via track objects);
//   - definitions guarded by {% if %} branches or with computed
//     ({{ }}) names never flag (chunt-otg.21 rules 2/3);
//   - the conservative Jinja rule: any {{ }}-computed reference of a
//     matching kind counts as a use of EVERY definition of that kind, since
//     static analysis cannot disprove the match;
//   - cross-file: a reference in ANY file of the directory group counts as
//     used (workspace defScope).
//
// Seq-addressed kinds (prefix-list entries, crypto map entries) register
// many symbols per object name; the pass reports once per (kind, name).

// referenceableKinds is the v1 unused-check kind set.
var referenceableKinds = map[symbols.Kind]bool{
	symbols.KindACL:           true,
	symbols.KindACLv6:         true,
	symbols.KindMACACL:        true,
	symbols.KindARPACL:        true,
	symbols.KindRouteMap:      true,
	symbols.KindClassMap:      true,
	symbols.KindPolicyMap:     true,
	symbols.KindVlan:          true,
	symbols.KindPrefixList:    true,
	symbols.KindCommunityList: true,
	symbols.KindAsPathACL:     true,
	symbols.KindVRF:           true,
	symbols.KindTrack:         true,
	symbols.KindKeyChain:      true,
	symbols.KindTimeRange:     true,
	symbols.KindNATPool:       true,
	symbols.KindCryptoMap:     true,
	symbols.KindTransformSet:  true,
	symbols.KindIsakmpPolicy:  true,
	symbols.KindIsakmpProfile: true,
	symbols.KindIPsecProfile:  true,
	symbols.KindZone:          true,
	symbols.KindParameterMap:  true,
	symbols.KindPeerGroup:     true,
}

// runUnusedDefinitionDiagnostics emits a Hint for every referenceable
// definition in THIS document that nothing in defScope references. defScope
// mirrors the undefined pass: nil (or [doc.URI]) is single-document scope; a
// directory group's URIs make a reference in any sibling file count as used.
func (f *CiscoIOSFeature) runUnusedDefinitionDiagnostics(doc *document.Document, defScope []string) []protocol.Diagnostic {
	if len(defScope) == 0 {
		defScope = []string{doc.URI}
	}
	var diags []protocol.Diagnostic
	reported := make(map[string]bool)
	for _, s := range f.symbols.All(doc.URI) {
		if !referenceableKinds[s.Kind] {
			continue
		}
		// Jinja guards and computed names cannot be proven unused.
		if s.Guarded || symbols.HasJinjaExpression(s.Name) {
			continue
		}
		// Built-in names never have explicit definitions anyway.
		if isBuiltInName(s.Kind, s.Name) {
			continue
		}
		if f.definitionUsedSomewhere(defScope, s.Kind, s.Name) {
			continue
		}
		key := string(s.Kind) + "\x00" + s.Name
		if reported[key] {
			continue // seq-addressed entries: once per object
		}
		reported[key] = true
		diags = append(diags, protocol.Diagnostic{
			Range:    s.NameRange,
			Severity: protocol.SeverityHint,
			Source:   "chunt",
			Code:     "unused-" + string(s.Kind),
			Message:  fmt.Sprintf("%s %q is never referenced; the definition has no effect", s.Kind, s.Name),
		})
	}
	return diags
}

// definitionUsedSomewhere reports whether any file in defScope carries a
// reference of (kind, name) — or an unresolvable {{ }} reference of the
// kind, which conservatively counts as a use of every definition of that
// kind (chunt-otg.21 conservative resolution).
func (f *CiscoIOSFeature) definitionUsedSomewhere(defScope []string, kind symbols.Kind, name string) bool {
	jinjaKindUsed := false
	for _, uri := range defScope {
		if len(f.symbols.ReferencesLookup(uri, kind, name)) > 0 {
			return true
		}
		if !jinjaKindUsed {
			for _, r := range f.symbols.ReferencesAll(uri) {
				if r.Kind == kind && symbols.HasJinjaExpression(r.Name) {
					jinjaKindUsed = true
					break
				}
			}
		}
	}
	return jinjaKindUsed
}
