package server

import (
	"context"

	"github.com/dgethings/chunt/internal/protocol"
)

// DocumentSymbol dispatches textDocument/documentSymbol to the registered
// feature.
func (s *Server) DocumentSymbol(ctx context.Context, params protocol.DocumentSymbolParams) ([]protocol.DocumentSymbol, error) {
	doc, err := s.documents.Get(params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	f, err := s.features.Route(doc.LanguageID)
	if err != nil {
		return nil, err
	}
	syms, err := f.DocumentSymbol(ctx, doc)
	if err != nil {
		return nil, err
	}
	return s.wireDocumentSymbols(doc, syms), nil
}
