package cisco_ios_jinja2

import (
	"context"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
)

// References implements textDocument/references. The cursor is resolved to
// a (Kind, Name) pair via the same helpers Definition uses:
//   - cursor on a definition's NameRange -> use that symbol's (Kind, Name)
//   - cursor on a reference's name token -> use that reference's (Kind, Name)
//
// Then every reference with the same (Kind, Name) is returned. When
// includeDeclaration is true (the default most editors pass), the matching
// definition's NameRange is appended so the user can jump between all
// relevant sites including the original.
//
// Mixed-kind matching (e.g. cursor on an ACL reference that should also
// surface route-map definitions of the same name) is intentionally NOT
// done: References should be the exact inverse of "find this symbol's
// usages", and broadening the kind would surface false-positive
// coincidence matches.
func (f *CiscoIOSFeature) References(ctx context.Context, doc *document.Document, pos protocol.Position, includeDeclaration bool) ([]protocol.Location, error) {
	kind, name, ok := f.resolveSymbolOrReference(doc, pos)
	if !ok {
		return nil, nil
	}
	refs := f.symbols.ReferencesLookup(doc.URI, kind, name)
	out := make([]protocol.Location, 0, len(refs)+1)
	for _, r := range refs {
		out = append(out, protocol.Location{URI: r.URI, Range: r.Range})
	}
	if includeDeclaration {
		for _, d := range f.symbols.Lookup(doc.URI, kind, name) {
			out = append(out, protocol.Location{URI: d.URI, Range: d.NameRange})
		}
	}
	return out, nil
}

// resolveSymbolOrReference finds the (Kind, Name) the cursor is on,
// preferring a definition site over a reference site (so invoking
// References on a definition's name returns every usage of it). Returns
// ok=false if the cursor is not on any recognized name token.
func (f *CiscoIOSFeature) resolveSymbolOrReference(doc *document.Document, pos protocol.Position) (kind symbols.Kind, name string, ok bool) {
	if sym := f.symbols.SymbolAt(doc.URI, pos); sym != nil {
		return sym.Kind, sym.Name, true
	}
	if ref := f.symbols.ReferenceAt(doc.URI, pos); ref != nil {
		return ref.Kind, ref.Name, true
	}
	return "", "", false
}
