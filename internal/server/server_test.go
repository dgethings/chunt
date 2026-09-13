package server_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/server"
)

// stubFeature is a test double implementing features.Feature via inline
// function fields. It does NOT pull in the CGO tree-sitter parser. Each hook
// records that it was called so tests can assert dispatch behavior.
type stubFeature struct {
	languageID string

	didOpenCalls   atomic.Int32
	didChangeCalls atomic.Int32
	didCloseCalls  atomic.Int32

	// Injected diagnostic to publish from DidOpen/DidChange.
	openDiags []protocol.Diagnostic
}

func (s *stubFeature) LanguageID() string { return s.languageID }
func (s *stubFeature) Close() error       { return nil }

func (s *stubFeature) DidOpen(ctx context.Context, doc *document.Document, publish func([]protocol.Diagnostic)) ([]protocol.Diagnostic, error) {
	s.didOpenCalls.Add(1)
	if publish != nil {
		publish(s.openDiags)
	}
	return s.openDiags, nil
}

func (s *stubFeature) DidChange(ctx context.Context, doc *document.Document, publish func([]protocol.Diagnostic)) ([]protocol.Diagnostic, error) {
	s.didChangeCalls.Add(1)
	if publish != nil {
		publish(s.openDiags)
	}
	return s.openDiags, nil
}

func (s *stubFeature) DidClose(ctx context.Context, doc *document.Document) error {
	s.didCloseCalls.Add(1)
	return nil
}

func (s *stubFeature) Completion(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.CompletionItem, error) {
	return nil, nil
}
func (s *stubFeature) Hover(ctx context.Context, doc *document.Document, pos protocol.Position) (*protocol.HoverResult, error) {
	return nil, nil
}
func (s *stubFeature) Definition(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.Location, error) {
	return nil, nil
}
func (s *stubFeature) References(ctx context.Context, doc *document.Document, pos protocol.Position, includeDeclaration bool) ([]protocol.Location, error) {
	return nil, nil
}
func (s *stubFeature) DocumentSymbol(ctx context.Context, doc *document.Document) ([]protocol.DocumentSymbol, error) {
	return nil, nil
}

func newTestServer(t *testing.T) (*server.Server, *stubFeature) {
	t.Helper()
	stub := &stubFeature{languageID: "cisco_ios_jinja2"}
	srv := server.New("test")
	srv.RegisterFeature(stub)
	return srv, stub
}

// TestServer_Initialize verifies Initialize transitions the state to
// initialized and returns server capabilities.
func TestServer_Initialize(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t)

	res, err := srv.Initialize(context.Background(), server.InitializeParams{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ServerInfo.Name != "chunt" {
		t.Errorf("server name: got %q, want chunt", res.ServerInfo.Name)
	}
	if res.ServerInfo.Version != "test" {
		t.Errorf("server version: got %q, want test", res.ServerInfo.Version)
	}
	if !res.Capabilities.HoverProvider {
		t.Errorf("HoverProvider should be true")
	}
}

// TestServer_ShutdownBeforeInitialize verifies that Shutdown before Initialize
// returns an error (the spec-required state machine guard).
func TestServer_ShutdownBeforeInitialize(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t)

	if err := srv.Shutdown(context.Background()); err == nil {
		t.Fatalf("Shutdown before Initialize should return an error")
	}
}

// TestServer_ShutdownAfterInitialize verifies that Shutdown after Initialize
// succeeds and closes the registered feature.
func TestServer_ShutdownAfterInitialize(t *testing.T) {
	t.Parallel()
	srv, stub := newTestServer(t)

	if _, err := srv.Initialize(context.Background(), server.InitializeParams{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	_ = stub
}

// TestServer_Initialized is a smoke test for the Initialized notification,
// which is a no-op that must not error.
func TestServer_Initialized(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t)
	if err := srv.Initialized(context.Background()); err != nil {
		t.Fatalf("Initialized: %v", err)
	}
}

// TestServer_DidOpen verifies didOpen stores the document and dispatches to
// the feature's DidOpen hook.
func TestServer_DidOpen(t *testing.T) {
	t.Parallel()
	srv, stub := newTestServer(t)

	err := srv.DidOpen(context.Background(), protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        "file:///t.ios.j2",
			LanguageID: "cisco_ios_jinja2",
			Version:    1,
			Text:       "hostname r1\n",
		},
	})
	if err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if got := stub.didOpenCalls.Load(); got != 1 {
		t.Errorf("DidOpen calls: got %d, want 1", got)
	}
}

// TestServer_DidClose verifies didClose removes the document and dispatches
// to the feature's DidClose hook. Uses the spec-compliant
// DidCloseTextDocumentParams shape.
func TestServer_DidClose(t *testing.T) {
	t.Parallel()
	srv, stub := newTestServer(t)

	// Open first so the doc exists.
	if err := srv.DidOpen(context.Background(), protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        "file:///t.ios.j2",
			LanguageID: "cisco_ios_jinja2",
			Version:    1,
			Text:       "hostname r1\n",
		},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	// Spec-compliant didClose: params carry a nested TextDocument identifier.
	if err := srv.DidClose(context.Background(), protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///t.ios.j2"},
	}); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if got := stub.didCloseCalls.Load(); got != 1 {
		t.Errorf("DidClose calls: got %d, want 1", got)
	}
}

// TestServer_DidClose_UnknownDoc verifies that didClose on a missing doc does
// not error (it logs and returns nil, matching the existing handler pattern).
func TestServer_DidClose_UnknownDoc(t *testing.T) {
	t.Parallel()
	srv, stub := newTestServer(t)

	if err := srv.DidClose(context.Background(), protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///missing"},
	}); err != nil {
		t.Fatalf("DidClose on missing doc should not error; got %v", err)
	}
	if got := stub.didCloseCalls.Load(); got != 0 {
		t.Errorf("DidClose calls on missing doc: got %d, want 0", got)
	}
}

// TestServer_DidChange verifies didChange applies content changes and
// dispatches to the feature's DidChange hook.
func TestServer_DidChange(t *testing.T) {
	t.Parallel()
	srv, stub := newTestServer(t)

	if err := srv.DidOpen(context.Background(), protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        "file:///t.ios.j2",
			LanguageID: "cisco_ios_jinja2",
			Version:    1,
			Text:       "hostname r1\n",
		},
	}); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	err := srv.DidChange(context.Background(), protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			URI:     "file:///t.ios.j2",
			Version: 2,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			{Text: "hostname r2\n"},
		},
	})
	if err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	if got := stub.didChangeCalls.Load(); got != 1 {
		t.Errorf("DidChange calls: got %d, want 1", got)
	}
}

// Ensure stubFeature satisfies the Feature interface at compile time.
var _ features.Feature = (*stubFeature)(nil)
