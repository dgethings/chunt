package server

import (
	"context"
	"log/slog"

	"github.com/dgethings/chunt/internal/protocol"
)

func (s *Server) Completion(ctx context.Context, params protocol.CompletionParams) (protocol.CompletionList, error) {
	doc, err := s.documents.Get(params.TextDocument.URI)
	if err != nil {
		slog.Error("failed to get document", "error", err.Error())
		return protocol.CompletionList{}, err
	}
	l := slog.With("uri", params.TextDocument.URI, "language", doc.LanguageID, "message", "completion")
	f, err := s.features.Route(doc.LanguageID)
	if err != nil {
		l.Debug("failed to find supported language", "error", err.Error())
		return protocol.CompletionList{}, err
	}
	items, err := f.Completion(ctx, doc, params.Position)
	if err != nil {
		slog.Debug("failed to get completion items", "error", err.Error())
		return protocol.CompletionList{}, err
	}
	return protocol.CompletionList{Items: items}, nil
}
