package server

import (
	"context"

	"github.com/dgethings/chunt/internal/protocol"
)

func (s *Server) Definition(ctx context.Context, params protocol.DefinitionParams) ([]protocol.Location, error) {
	doc, err := s.documents.Get(params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	f, err := s.features.Route(doc.LanguageID)
	if err != nil {
		return nil, err
	}
	locs, err := f.Definition(ctx, doc, s.internalPosition(doc, params.Position))
	if err != nil {
		return nil, err
	}
	return s.wireLocations(doc, locs), nil
}
