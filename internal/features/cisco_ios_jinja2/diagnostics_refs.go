package cisco_ios_jinja2

import (
	"fmt"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
)

// runUndefinedReferenceDiagnostics emits a Warning for every reference whose
// expected (Kind, Name) is not satisfied by any definition in the same
// document. This is the core Phase 4 deliverable: it catches the typos and
// missing definitions the parser itself cannot flag (because the flat
// command_line form is syntactically valid regardless of whether the named
// target exists).
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
// any, all) are suppressed to avoid false positives.
func (f *CiscoIOSFeature) runUndefinedReferenceDiagnostics(doc *document.Document) []protocol.Diagnostic {
	refs := f.symbols.ReferencesAll(doc.URI)
	if len(refs) == 0 {
		return nil
	}
	var diags []protocol.Diagnostic
	for _, r := range refs {
		if isBuiltInName(r.Kind, r.Name) {
			continue
		}
		if defs := f.symbols.Lookup(doc.URI, r.Kind, r.Name); len(defs) > 0 {
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
		if isSingletonKind(s.Kind) {
			continue
		}
		key := string(s.Kind) + "\x00" + s.Name
		if prior, ok := firstSeen[key]; !ok {
			firstSeen[key] = first{idx: i, sym: s}
		} else {
			diags = append(diags, protocol.Diagnostic{
				Range:    s.NameRange,
				Severity: protocol.SeverityWarning,
				Source:   "chunt",
				Code:     "duplicate-" + string(s.Kind),
				Message:  fmt.Sprintf("duplicate %s definition %q", s.Kind, s.Name),
				RelatedInformation: []protocol.DiagnosticRelatedInformation{{
					Location: protocol.Location{URI: prior.sym.URI, Range: prior.sym.NameRange},
					Message:  "first defined here",
				}},
			})
		}
	}
	return diags
}

// builtInNames lists identifiers that never appear as explicit definitions
// but are nonetheless valid reference targets (e.g. `class class-default`
// in a policy-map body). Suppressing these avoids false-positive undefined
// warnings.
var builtInNames = map[string]map[string]bool{
	string(symbols.KindClassMap): {
		"class-default": true,
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
