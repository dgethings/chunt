package cisco_ios_jinja2

import (
	"context"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
)

// Definition implements textDocument/definition. Resolution rules, in order:
//
//  1. Cursor on a definition's name token (e.g. on `FOO` in
//     `route-map FOO permit 10`) -> return that definition's own location.
//     Matches the convention most editors expect when the user invokes
//     Go-To-Def from a header.
//  2. Cursor on a reference name token (e.g. on `FOO` in
//     `ip access-group FOO in`) -> look up definitions matching the
//     reference's (Kind, Name). Falls back to LookupAny (any kind with the
//     same name) if the exact kind has no definition.
//  3. Otherwise -> empty result (no locations to jump to).
//
// The returned Range is the definition's NameRange (just the name token),
// not the full section range, so the editor lands precisely on the
// identifier being defined. line/redundancy sections have a synthesized
// NameRange that spans the whole header (no single name token exists).
func (f *CiscoIOSFeature) Definition(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.Location, error) {
	if sym := f.symbols.SymbolAt(doc.URI, pos); sym != nil {
		return []protocol.Location{{URI: sym.URI, Range: sym.NameRange}}, nil
	}
	ref := f.symbols.ReferenceAt(doc.URI, pos)
	if ref == nil {
		return nil, nil
	}
	defs := f.symbols.Lookup(doc.URI, ref.Kind, ref.Name)
	if len(defs) == 0 {
		// Fall back to any kind with this name (e.g. `match ip address FOO`
		// could reference either an ACL or a prefix-list; without a
		// definition we don't know which, so try everything).
		defs = f.symbols.LookupAny(doc.URI, ref.Name)
	}
	if len(defs) == 0 {
		return nil, nil
	}
	out := make([]protocol.Location, 0, len(defs))
	for _, d := range defs {
		out = append(out, protocol.Location{URI: d.URI, Range: d.NameRange})
	}
	return out, nil
}
