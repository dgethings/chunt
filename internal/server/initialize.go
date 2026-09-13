package server

import (
	"context"
	"log/slog"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/dgethings/chunt/internal/protocol"
)

type InitializeParams struct {
	ClientInfo *protocol.ClientInfo `json:"clientInfo"`
}

func (s *Server) Initialize(ctx context.Context, params InitializeParams) (protocol.InitializeResult, error) {
	s.setState(stateInitialized)
	if params.ClientInfo != nil {
		slog.Info("connected to client", "name", params.ClientInfo.Name, "version", params.ClientInfo.Version)
	}
	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			PositionEncoding:       "utf-8",
			TextDocumentSync:       1,
			HoverProvider:          true,
			DefinitionProvider:     true,
			ReferencesProvider:     true,
			DocumentSymbolProvider: true,
			CompletionProvider:     &protocol.CompletionOptions{},
		},
		ServerInfo: protocol.ServerInfo{
			Name:    "chunt",
			Version: s.version,
		},
	}, nil
}

func (s *Server) Initialized(ctx context.Context) error {
	slog.Info("client initialized")
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.setState(stateShutDown); err != nil {
		return err
	}
	return s.features.Close()
}

func (s *Server) Assigner() (jrpc2.Assigner, error) {
	return handler.Map{
		"initialize":                  handler.New(s.Initialize),
		"initialized":                 handler.New(s.Initialized),
		"shutdown":                    handler.New(s.Shutdown),
		"textDocument/didOpen":        handler.New(s.DidOpen),
		"textDocument/didChange":      handler.New(s.DidChange),
		"textDocument/didClose":       handler.New(s.DidClose),
		"textDocument/completion":     handler.New(s.Completion),
		"textDocument/hover":          handler.New(s.Hover),
		"textDocument/definition":     handler.New(s.Definition),
		"textDocument/references":     handler.New(s.References),
		"textDocument/documentSymbol": handler.New(s.DocumentSymbol),
	}, nil
}
