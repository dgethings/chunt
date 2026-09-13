package server

import (
	"context"

	"github.com/dgethings/chunt/internal/protocol"
)

// References dispatches textDocument/references to the registered feature.
func (s *Server) References(ctx context.Context, params protocol.ReferenceParams) ([]protocol.Location, error) {
	doc, err := s.documents.Get(params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	f, err := s.features.Route(doc.LanguageID)
	if err != nil {
		return nil, err
	}
	return f.References(ctx, doc, params.Position, params.Context.IncludeDeclaration)
}
