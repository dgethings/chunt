package server

import (
	"context"

	"github.com/dgethings/chunt/internal/protocol"
)

func (s *Server) Hover(ctx context.Context, params protocol.HoverParams) (*protocol.HoverResult, error) {
	doc, err := s.documents.Get(params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	f, err := s.features.Route(doc.LanguageID)
	if err != nil {
		return nil, err
	}
	return f.Hover(ctx, doc, params.Position)
}
