package server

import (
	"context"
	"log/slog"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/dgethings/chunt/internal/protocol"
)

type InitializeParams struct {
	ClientInfo *protocol.ClientInfo                `json:"clientInfo"`
	General    *protocol.GeneralClientCapabilities `json:"general,omitempty"`
}

func (s *Server) Initialize(ctx context.Context, params InitializeParams) (protocol.InitializeResult, error) {
	s.setState(stateInitialized)
	if params.ClientInfo != nil {
		slog.Info("connected to client", "name", params.ClientInfo.Name, "version", params.ClientInfo.Version)
	}
	// LSP 3.17 position-encoding negotiation (encoding.go). VS Code's
	// client offers only utf-16 and aborts the connection at
	// doInitialize if the server advertises anything else, so this must
	// be a negotiated value, never a hardcoded one.
	enc := negotiatePositionEncoding(nil)
	if params.General != nil {
		enc = negotiatePositionEncoding(params.General.PositionEncodings)
	}
	s.setPositionEncoding(enc)
	slog.Debug("negotiated position encoding", "encoding", enc)
	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			PositionEncoding:       enc,
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

// Assigner returns the jrpc2 assigner for the server's methods, wrapped in
// a stateGate that enforces the LSP lifecycle state machine (see
// assigner.go): requests before initialize get -32002, requests after
// shutdown get -32600, and notifications in those states are dropped.
func (s *Server) Assigner() (jrpc2.Assigner, error) {
	return stateGate{
		srv: s,
		methods: handler.Map{
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
		},
	}, nil
}
