package cisco_ios_jinja2

import (
	"context"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
)

// lspSymbolKind maps a Cisco IOS symbol Kind to its LSP SymbolKind enum
// value. The mapping is necessarily lossy (LSP has no concept of "vlan" or
// "route-map"); the chosen kinds drive only the gutter/outline icon in the
// editor. Detail carries the original Kind string so the user can still
// distinguish them.
var lspSymbolKind = map[symbols.Kind]int{
	symbols.KindInterface:  protocol.SymbolKindInterface, // 11 — closest semantic match
	symbols.KindRouter:     protocol.SymbolKindClass,     // 5
	symbols.KindRouteMap:   protocol.SymbolKindClass,     // 5
	symbols.KindClassMap:   protocol.SymbolKindClass,     // 5
	symbols.KindPolicyMap:  protocol.SymbolKindClass,     // 5
	symbols.KindVlan:       protocol.SymbolKindNumber,    // 16 — usually numeric
	symbols.KindLine:       protocol.SymbolKindVariable,  // 13 — synthesized "vty-0-4"
	symbols.KindRedundancy: protocol.SymbolKindNamespace, // 3
	symbols.KindACL:        protocol.SymbolKindNamespace, // 3
	symbols.KindHostname:   protocol.SymbolKindVariable,  // 13 — string identifier
}

// DocumentSymbol implements textDocument/documentSymbol. Returns one entry
// per definition site in the document, in document order. Range covers the
// full section header (or flat ACL line); SelectionRange covers just the
// name token (so the editor scrolls to and highlights the identifier when
// the user picks the outline entry). Children are intentionally empty for
// v1 — nested outlines (e.g. class blocks under a policy-map) are a future
// refinement.
func (f *CiscoIOSFeature) DocumentSymbol(ctx context.Context, doc *document.Document) ([]protocol.DocumentSymbol, error) {
	syms := f.symbols.All(doc.URI)
	if len(syms) == 0 {
		return nil, nil
	}
	out := make([]protocol.DocumentSymbol, 0, len(syms))
	for _, s := range syms {
		out = append(out, protocol.DocumentSymbol{
			Name:           s.Name,
			Detail:         string(s.Kind),
			Kind:           lspSymbolKind[s.Kind],
			Range:          s.Range,
			SelectionRange: s.NameRange,
		})
	}
	return out, nil
}
