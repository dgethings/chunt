package cisco_ios_jinja2

import (
	"fmt"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
)

// runUndefinedReferenceDiagnostics emits a Warning for every reference whose
// expected (Kind, Name) is not satisfied by any definition in scope. This is
// the core Phase 4 deliverable: it catches the typos and missing definitions
// the parser itself cannot flag (because the flat command_line form is
// syntactically valid regardless of whether the named target exists).
//
// defScope lists the URIs searched for satisfying definitions: nil (or
// [doc.URI]) is the historical single-document scope; a directory group's
// order resolves references across the whole logical config, so an ACL
// defined in a sibling template satisfies an `ip access-group` reference in
// this one (workspace mode).
//
// Examples:
//
//	ip access-group FOO in       (no ip access-list ... FOO)
//	ip policy route-map BAR      (no route-map BAR permit ...)
//	class VOICE                  (no class-map ... VOICE, unless built-in)
//	service-policy input PM      (no policy-map PM)
//	switchport access vlan 99    (no vlan 99 section)
//
// References inside a negated_statement have already been filtered out by
// the reference walker (Phase 3), so e.g. `no ip access-group FOO in` does
// not produce a diagnostic.
//
// Built-in names that never have an explicit definition (class-default,
// any, all) are suppressed to avoid false positives. Names containing a
// Jinja expression ({{ ... }}) are unresolvable statically and likewise
// never produce a diagnostic — see the chunt-otg.21 rules at the top of
// internal/symbols (HasJinjaExpression) and the duplicate pass below.
// References whose name is a Jinja expression (`ip access-group {{ acl }}
// in`) are unresolvable statically (chunt-otg.21 rule 1) and never produce
// a diagnostic. Guarded definitions ({% if %}/{% for %} bodies) are indexed
// like any other and satisfy references from any branch (rule 2), so no
// extra handling is needed here — Lookup already finds them.
func (f *CiscoIOSFeature) runUndefinedReferenceDiagnostics(doc *document.Document, defScope []string) []protocol.Diagnostic {
	refs := f.symbols.ReferencesAll(doc.URI)
	if len(refs) == 0 {
		return nil
	}
	if len(defScope) == 0 {
		defScope = []string{doc.URI}
	}
	var diags []protocol.Diagnostic
	for _, r := range refs {
		if isBuiltInName(r.Kind, r.Name) {
			continue
		}
		if symbols.HasJinjaExpression(r.Name) {
			continue // computed name: unresolvable statically, rule 1
		}
		satisfied := false
		for _, u := range defScope {
			if defs := f.symbols.Lookup(u, r.Kind, r.Name); len(defs) > 0 {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		diags = append(diags, protocol.Diagnostic{
			Range:    r.Range,
			Severity: protocol.SeverityWarning,
			Source:   "chunt",
			Code:     "undefined-" + string(r.Kind),
			Message:  fmt.Sprintf("undefined %s %q", r.Kind, r.Name),
		})
	}
	return diags
}

// runDuplicateDefinitionDiagnostics emits a Warning when two definitions of
// the same (Kind, Name) appear in the same document. The diagnostic is
// anchored on the SECOND and subsequent definitions (the first is assumed
// canonical) and carries RelatedInformation pointing back to the first so
// the editor can offer a "go to original definition" jump.
//
// IOS itself accepts duplicate definitions as "edit in place" (the later
// one wins) but in templated configs they almost always indicate a
// copy-paste error.
//
// Singleton kinds (redundancy) are excluded by convention — multiple
// `redundancy` sections are unusual but valid (rare multi-chassis configs).
func (f *CiscoIOSFeature) runDuplicateDefinitionDiagnostics(doc *document.Document) []protocol.Diagnostic {
	syms := f.symbols.All(doc.URI)
	if len(syms) < 2 {
		return nil
	}
	type first struct {
		idx int
		sym symbols.Symbol
	}
	firstSeen := make(map[string]first)
	var diags []protocol.Diagnostic
	for i, s := range syms {
		if isSingletonKind(s.Kind) || isAdditiveEntryKind(s.Kind) {
			continue
		}
		// Jinja false-positive gate (chunt-otg.21): definitions guarded by
		// {% if %}/{% else %} branches may legitimately repeat a name (the
		// branches are mutually exclusive at render time), and computed
		// names (`{% for %}`-emitted `vlan {{ vlan_id }}`) cannot be proven
		// distinct or identical statically. Neither is flagged.
		if s.Guarded || symbols.HasJinjaExpression(s.Name) {
			continue
		}
		key := duplicateKey(s)
		if prior, ok := firstSeen[key]; !ok {
			firstSeen[key] = first{idx: i, sym: s}
		} else {
			msg := fmt.Sprintf("duplicate %s definition %q", s.Kind, s.Name)
			if s.Seq != "" {
				// Seq-addressed entries (prefix-lists): the repeated seq
				// REPLACES the earlier entry at merge time, so name the
				// conflicting entry in the message (chunt-otg.6).
				msg = fmt.Sprintf("duplicate %s %q entry seq %s; the later definition replaces it", s.Kind, s.Name, s.Seq)
			}
			diags = append(diags, protocol.Diagnostic{
				Range:    s.NameRange,
				Severity: protocol.SeverityWarning,
				Source:   "chunt",
				Code:     "duplicate-" + string(s.Kind),
				Message:  msg,
				RelatedInformation: []protocol.DiagnosticRelatedInformation{{
					Location: protocol.Location{URI: prior.sym.URI, Range: prior.sym.NameRange},
					Message:  "first defined here",
				}},
			})
		}
	}
	return diags
}

// duplicateKey builds the first-seen map key for one symbol in the
// duplicate-definition pass. Seq-carrying kinds (prefix-list entries) are
// keyed on (kind, name, seq) because a list's entries are additive — a
// second `ip prefix-list PL seq 20` line extends the list rather than
// duplicating it; only a repeated SEQ is a conflict (chunt-otg.6). Every
// other kind keys on (kind, name).
func duplicateKey(s symbols.Symbol) string {
	if s.Seq != "" {
		return string(s.Kind) + "\x00" + s.Name + "\x00" + s.Seq
	}
	return string(s.Kind) + "\x00" + s.Name
}

// builtInNames lists identifiers that never appear as explicit definitions
// but are nonetheless valid reference targets (e.g. `class class-default`
// in a policy-map body). Suppressing these avoids false-positive undefined
// warnings.
var builtInNames = map[string]map[string]bool{
	string(symbols.KindClassMap): {
		"class-default": true,
	},
	// VRF built-ins (chunt-otg.17): the implicit global and the automatic
	// management VRF are valid reference targets no template defines.
	string(symbols.KindVRF): {
		"global":   true,
		"Mgmt-vrf": true,
		"mgmt-vrf": true,
	},
}

func isBuiltInName(kind symbols.Kind, name string) bool {
	return builtInNames[string(kind)][name]
}

// isSingletonKind reports whether kind represents a section that exists at
// most once per document by IOS convention (so multiple definitions are not
// a duplicate-definition condition). Currently only KindRedundancy.
func isSingletonKind(kind symbols.Kind) bool {
	return kind == symbols.KindRedundancy
}

// isAdditiveEntryKind reports whether kind's definition lines are additive
// entries within one named object — community-list / as-path ACL
// permit|deny lines (chunt-otg.6) and monitor-session source/destination
// lines (chunt-otg.7; every `monitor session N ...` line configures the
// SAME session, so multiple lines per N are the normal shape). A second
// entry extends the object, so no whole-object duplicate condition exists
// for these kinds; an exact-text repeat of an entry line is already owned
// by the duplicate-command pass. (Prefix-list entries are additive too,
// but their explicit `seq N` gives the duplicate pass a real collision
// key — see duplicateKey.)
func isAdditiveEntryKind(kind symbols.Kind) bool {
	switch kind {
	case symbols.KindCommunityList, symbols.KindAsPathACL, symbols.KindMonitorSession:
		return true
	}
	return false
}
