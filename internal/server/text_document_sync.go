package server

import (
	"context"
	"log/slog"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
)

func (s *Server) DidOpen(ctx context.Context, params protocol.DidOpenTextDocumentParams) error {
	doc := document.New(
		params.TextDocument.URI,
		params.TextDocument.LanguageID,
		params.TextDocument.Version,
		[]byte(params.TextDocument.Text),
	)
	s.documents.Put(doc)
	l := slog.With("language", doc.LanguageID, "message", "didOpen")
	l.Debug("stored document", "uri", doc.URI, "version", doc.Version)

	f, err := s.features.Route(doc.LanguageID)
	if err != nil {
		l.Error("failed to find supported language", "error", err.Error())
		return nil
	}
	_, err = f.DidOpen(ctx, doc, func(diags []protocol.Diagnostic) {
		publishDiagnostics(ctx, doc.URI, diags)
	})
	if err != nil {
		l.Error("didOpen error", "error", err.Error())
	}

	l.Debug("opened", "uri", doc.URI)
	return nil
}

func (s *Server) DidChange(ctx context.Context, params protocol.DidChangeTextDocumentParams) error {
	doc, err := s.documents.Get(params.TextDocument.URI)
	if err != nil {
		slog.Error("failed to get document", "error", err.Error())
		return nil
	}
	l := slog.With("message", "didChange", "uri", doc.URI, "language", doc.LanguageID)

	for _, change := range params.ContentChanges {
		doc.Content = []byte(change.Text)
		doc.Version = params.TextDocument.Version
		s.documents.Put(doc)

		f, err := s.features.Route(doc.LanguageID)
		if err != nil {
			l.Error("failed to find supported language", "language", doc.LanguageID, "error", err)
			continue
		}

		_, err = f.DidChange(ctx, doc, func(diags []protocol.Diagnostic) {
			publishDiagnostics(ctx, doc.URI, diags)
		})
		if err != nil {
			l.Error("failed to get diagnostics", "language", doc.LanguageID, "error", err)
			continue
		}
	}
	l.Debug("successfully changed", "uri", doc.URI)
	return nil
}

func (s *Server) DidClose(ctx context.Context, params protocol.DidCloseTextDocumentParams) error {
	doc, err := s.documents.Get(params.TextDocument.URI)
	if err != nil {
		slog.Error("failed to get document", "error", err.Error())
		return nil
	}
	l := slog.With("uri", doc.URI, "language", doc.LanguageID, "message", "didClose")

	f, err := s.features.Route(doc.LanguageID)
	if err != nil {
		l.Error("failed to find supported language", "error", err.Error())
		return nil
	}
	if err := f.DidClose(ctx, doc); err != nil {
		l.Error("failed execution", "error", err.Error())
	}
	s.documents.Delete(params.TextDocument.URI)
	return nil
}
