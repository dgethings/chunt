package server_test

// End-to-end JSON-RPC wire test: drives chunt exactly the way an editor
// does — a real jrpc2 server (the same assigner, options, and channel.Header
// framing cmd/serve.go uses) over an in-memory pipe, talked to with a real
// jrpc2 client. Unlike server_test.go, which calls server.Server methods
// directly, every request and notification here crosses the wire, so a bad
// struct tag or a framing/push bug in the protocol layer fails here even
// though it would pass every direct-call test.
//
// The session helpers (WireSession, NewWireSession, nthPositionOf, the
// fixture constants) are deliberately reusable: chunt-ded extends them for
// protocol edge-case coverage.

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/server"
)

// wireTimeout bounds every wait in the harness (diagnostics push, server
// termination, per-session request context) so a lost push or a hung server
// produces a clear timeout failure instead of a deadlock.
const wireTimeout = 10 * time.Second

// wireURI is the document URI used by the wire fixtures.
const wireURI = "file:///wire.ios.j2"

// wireFixtureInitial is opened over the wire. It contains exactly one known
// diagnostic — the undefined ACL reference "MISSING" — while FOO is defined
// (and referenced twice) so the definition/references paths resolve cleanly.
const wireFixtureInitial = `! wire-test fixture
hostname r1
!
interface GigabitEthernet0/0
 ip access-group MISSING in
!
interface GigabitEthernet1/0
 ip access-group FOO in
!
ip access-list standard FOO
 permit 10.0.0.0 0.0.0.255
!
route-map RM permit 10
!
`

// wireFixtureFixed is wireFixtureInitial with the undefined reference
// resolved (MISSING -> FOO). Sent as a full-text didChange, it must clear
// the pushed diagnostics.
const wireFixtureFixed = `! wire-test fixture
hostname r1
!
interface GigabitEthernet0/0
 ip access-group FOO in
!
interface GigabitEthernet1/0
 ip access-group FOO in
!
ip access-list standard FOO
 permit 10.0.0.0 0.0.0.255
!
route-map RM permit 10
!
`

// wireSettle is how long the diagnostics stream must stay quiet before
// AwaitSettledDiagnostics declares it stable. The feature publishes
// diagnostics in two tiers (tree-only, then +refs; see
// DESIGN-chunt-cfz-progressive-diagnostics.md), so one editor action can
// push up to two notifications back-to-back.
const wireSettle = 250 * time.Millisecond

// WireSession is an in-process chunt server plus a jrpc2 client connected
// over an in-memory Content-Length-framed channel. It mirrors cmd/serve.go's
// wiring (server.New -> RegisterFeature -> Assigner -> jrpc2.NewServer with
// AllowPush+Concurrency 1 over channel.Header("")), so anything that holds
// on the real transport holds here.
type WireSession struct {
	t       *testing.T
	client  *jrpc2.Client
	jrpcSrv *jrpc2.Server
	ctx     context.Context
	cancel  context.CancelFunc

	// diags receives every textDocument/publishDiagnostics notification the
	// server pushes, decoded through the real protocol DTO.
	diags chan protocol.PublishDiagnosticsParams

	shutdownDone bool
}

// NewWireSession builds and starts a wire-connected chunt server (with the
// real Cisco IOS feature) and its client. The session is torn down in a
// test cleanup; tests that want to assert clean termination call Close.
func NewWireSession(t *testing.T) *WireSession {
	t.Helper()

	srv := server.New("wiretest")
	srv.RegisterFeature(cisco_ios_jinja2.New())

	assigner, err := srv.Assigner()
	if err != nil {
		t.Fatalf("srv.Assigner(): %v", err)
	}

	cliConn, srvConn := net.Pipe()

	// Same options and framing as cmd/serve.go.
	jrpcSrv := jrpc2.NewServer(assigner, &jrpc2.ServerOptions{
		AllowPush:   true,
		Concurrency: 1,
	})
	jrpcSrv.Start(channel.Header("")(srvConn, srvConn))

	diags := make(chan protocol.PublishDiagnosticsParams, 32)
	client := jrpc2.NewClient(channel.Header("")(cliConn, cliConn), &jrpc2.ClientOptions{
		OnNotify: func(req *jrpc2.Request) {
			if req.Method() != "textDocument/publishDiagnostics" {
				return
			}
			var p protocol.PublishDiagnosticsParams
			if err := req.UnmarshalParams(&p); err != nil {
				t.Errorf("decode publishDiagnostics params: %v", err)
				return
			}
			select {
			case diags <- p:
			default:
				t.Errorf("diagnostics channel full; push for %s dropped", p.URI)
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), wireTimeout)
	s := &WireSession{
		t:       t,
		client:  client,
		jrpcSrv: jrpcSrv,
		ctx:     ctx,
		cancel:  cancel,
		diags:   diags,
	}
	t.Cleanup(func() {
		// Best-effort teardown for tests that did not Close explicitly.
		client.Close()
		jrpcSrv.Stop()
		_ = jrpcSrv.Wait()
		cancel()
	})
	return s
}

// Call performs a JSON-RPC request and unmarshals the response result into
// result (which may be nil to ignore the payload). It fails the test on any
// transport or decode error.
func (s *WireSession) Call(method string, params, result any) {
	s.t.Helper()
	if result == nil {
		if _, err := s.client.Call(s.ctx, method, params); err != nil {
			s.t.Fatalf("Call %q: %v", method, err)
		}
		return
	}
	if err := s.client.CallResult(s.ctx, method, params, result); err != nil {
		s.t.Fatalf("Call %q: %v", method, err)
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (s *WireSession) Notify(method string, params any) {
	s.t.Helper()
	if err := s.client.Notify(s.ctx, method, params); err != nil {
		s.t.Fatalf("Notify %q: %v", method, err)
	}
}

// AwaitDiagnostics blocks until the server pushes the next
// publishDiagnostics notification, failing the test on timeout instead of
// hanging.
func (s *WireSession) AwaitDiagnostics() protocol.PublishDiagnosticsParams {
	s.t.Helper()
	select {
	case p := <-s.diags:
		return p
	case <-time.After(wireTimeout):
		s.t.Fatalf("timed out after %v waiting for textDocument/publishDiagnostics", wireTimeout)
		return protocol.PublishDiagnosticsParams{}
	}
}

// AwaitSettledDiagnostics drains diagnostics pushes until the stream has
// been quiet for wireSettle, returning the final (full-replacement) push.
// Because publishDiagnostics is full-replacement, the last push is the
// editor-visible state once the tiered stream settles.
func (s *WireSession) AwaitSettledDiagnostics() protocol.PublishDiagnosticsParams {
	s.t.Helper()
	p := s.AwaitDiagnostics()
	for {
		select {
		case next := <-s.diags:
			p = next
		case <-time.After(wireSettle):
			return p
		}
	}
}

// Initialize performs the initialize handshake, returning the decoded
// InitializeResult.
func (s *WireSession) Initialize() protocol.InitializeResult {
	s.t.Helper()
	var res protocol.InitializeResult
	s.Call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "wire-test-client", "version": "0.0"},
	}, &res)
	s.Notify("initialized", nil)
	return res
}

// DidOpen opens text as wireURI with the cisco_ios_jinja2 language ID and
// waits for the diagnostics stream to settle.
func (s *WireSession) DidOpen(text string) protocol.PublishDiagnosticsParams {
	s.t.Helper()
	s.Notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        wireURI,
			LanguageID: "cisco_ios_jinja2",
			Version:    1,
			Text:       text,
		},
	})
	return s.AwaitSettledDiagnostics()
}

// DidChangeFull sends a full-text didChange (TextDocumentSync=1) and waits
// for the diagnostics stream to settle.
func (s *WireSession) DidChangeFull(version int, text string) protocol.PublishDiagnosticsParams {
	s.t.Helper()
	s.Notify("textDocument/didChange", protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			URI:     wireURI,
			Version: version,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: text}},
	})
	return s.AwaitSettledDiagnostics()
}

// Shutdown sends the shutdown request and reports the wire error. Calling
// it twice returns the server's state-machine error on the second call.
func (s *WireSession) Shutdown() error {
	s.t.Helper()
	s.shutdownDone = true
	_, err := s.client.Call(s.ctx, "shutdown", nil)
	return err
}

// Close terminates the session the way an editor does: a shutdown request,
// an exit notification, then closing the channel (the stdio equivalent).
// It fails the test if the server does not stop cleanly within wireTimeout.
func (s *WireSession) Close() {
	s.t.Helper()
	if !s.shutdownDone {
		if err := s.Shutdown(); err != nil {
			s.t.Logf("shutdown call: %v (continuing teardown)", err)
		}
	}
	s.Notify("exit", nil)
	if err := s.client.Close(); err != nil {
		s.t.Logf("client close: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.jrpcSrv.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			s.t.Errorf("server Wait() after exit: %v, want nil", err)
		}
	case <-time.After(wireTimeout):
		s.t.Fatalf("server did not terminate within %v after exit", wireTimeout)
	}
	s.cancel()
}

// truncForLog shortens s for error messages so a long hover/documentation
// blob does not drown out the assertion detail.
func truncForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// nthPositionOf returns the zero-based position of the n-th (1-based)
// occurrence of needle in src. It fails the test if there are fewer than n
// occurrences.
func nthPositionOf(t *testing.T, src, needle string, n int) protocol.Position {
	t.Helper()
	off := 0
	for i := 0; i < n; i++ {
		idx := strings.Index(src[off:], needle)
		if idx < 0 {
			t.Fatalf("needle %q: only %d occurrence(s) in fixture, need %d", needle, i, n)
		}
		off += idx
		if i < n-1 {
			off += len(needle)
		}
	}
	return protocol.Position{
		Line:      uint(strings.Count(src[:off], "\n")),
		Character: uint(off - (strings.LastIndexByte(src[:off], '\n') + 1)),
	}
}

// TestWire_Initialize verifies the initialize handshake crosses the wire and
// decodes through the real DTOs: serverInfo and every advertised capability.
func TestWire_Initialize(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)

	res := s.Initialize()

	if res.ServerInfo.Name != "chunt" {
		t.Errorf("serverInfo.name = %q, want %q", res.ServerInfo.Name, "chunt")
	}
	if res.ServerInfo.Version != "wiretest" {
		t.Errorf("serverInfo.version = %q, want %q", res.ServerInfo.Version, "wiretest")
	}
	caps := res.Capabilities
	if !caps.HoverProvider || !caps.DefinitionProvider || !caps.ReferencesProvider || !caps.DocumentSymbolProvider {
		t.Errorf("capabilities: hover=%v definition=%v references=%v documentSymbol=%v, want all true",
			caps.HoverProvider, caps.DefinitionProvider, caps.ReferencesProvider, caps.DocumentSymbolProvider)
	}
	if caps.CompletionProvider == nil {
		t.Errorf("capabilities.completionProvider = nil, want non-nil")
	}
	if caps.TextDocumentSync != 1 {
		t.Errorf("capabilities.textDocumentSync = %d, want 1 (full)", caps.TextDocumentSync)
	}
	if caps.PositionEncoding != "utf-8" {
		t.Errorf("capabilities.positionEncoding = %q, want %q", caps.PositionEncoding, "utf-8")
	}
}

// TestWire_Initialize_RawShape checks the initialize result at the raw JSON
// level (decoded into generic maps). The typed asserts above cannot catch a
// field that is absent from the wire payload (e.g. an omitempty mistake on a
// value that should be present); this can.
func TestWire_Initialize_RawShape(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)

	rsp, err := s.client.Call(s.ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "wire-test-client", "version": "0.0"},
	})
	if err != nil {
		t.Fatalf("Call initialize: %v", err)
	}
	var raw map[string]any
	if err := rsp.UnmarshalResult(&raw); err != nil {
		t.Fatalf("decode raw initialize result: %v", err)
	}
	srvInfo, ok := raw["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo missing or not an object: %#v", raw["serverInfo"])
	}
	if srvInfo["name"] != "chunt" {
		t.Errorf("raw serverInfo.name = %#v, want %q", srvInfo["name"], "chunt")
	}
	caps, ok := raw["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities missing or not an object: %#v", raw["capabilities"])
	}
	for _, key := range []string{"hoverProvider", "definitionProvider", "referencesProvider", "documentSymbolProvider", "completionProvider", "textDocumentSync", "positionEncoding"} {
		if _, ok := caps[key]; !ok {
			t.Errorf("raw capabilities.%s missing from wire payload", key)
		}
	}
}

// TestWire_DiagnosticsLifecycle drives the push channel end to end: didOpen
// with an undefined ACL reference must arrive on the client as a
// publishDiagnostics notification with the expected message/range/severity,
// and a full-text didChange that resolves the reference must push a clear
// (empty) diagnostics list.
func TestWire_DiagnosticsLifecycle(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()

	open := s.DidOpen(wireFixtureInitial)
	if open.URI != wireURI {
		t.Errorf("push uri = %q, want %q", open.URI, wireURI)
	}
	diags := open.Diagnostics
	if len(diags) != 1 {
		t.Fatalf("didOpen push: got %d diagnostics, want 1: %+v", len(diags), diags)
	}
	d := diags[0]
	missingPos := nthPositionOf(t, wireFixtureInitial, "MISSING", 1)
	if d.Code != "undefined-acl" {
		t.Errorf("diagnostic code = %q, want %q", d.Code, "undefined-acl")
	}
	if d.Message != `undefined acl "MISSING"` {
		t.Errorf("diagnostic message = %q, want %q", d.Message, `undefined acl "MISSING"`)
	}
	if d.Severity != protocol.SeverityWarning {
		t.Errorf("diagnostic severity = %d, want %d (Warning)", d.Severity, protocol.SeverityWarning)
	}
	if d.Source != "chunt" {
		t.Errorf("diagnostic source = %q, want %q", d.Source, "chunt")
	}
	if d.Range.Start.Line != missingPos.Line {
		t.Errorf("diagnostic range line = %d, want %d", d.Range.Start.Line, missingPos.Line)
	}
	if d.Range.Start.Character != missingPos.Character || d.Range.End.Character <= d.Range.Start.Character {
		t.Errorf("diagnostic range chars = [%d,%d), want start %d and non-empty",
			d.Range.Start.Character, d.Range.End.Character, missingPos.Character)
	}

	fixed := s.DidChangeFull(2, wireFixtureFixed)
	if fixed.URI != wireURI {
		t.Errorf("didChange push uri = %q, want %q", fixed.URI, wireURI)
	}
	if len(fixed.Diagnostics) != 0 {
		t.Errorf("didChange push after fix: got %d diagnostics, want 0: %+v", len(fixed.Diagnostics), fixed.Diagnostics)
	}
}

// TestWire_Hover hovers on a known keyword over the wire and asserts the
// markup hover decodes through the HoverResult DTO.
func TestWire_Hover(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureFixed)

	pos := nthPositionOf(t, wireFixtureFixed, "hostname", 1)
	var hover protocol.HoverResult
	s.Call("textDocument/hover", protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
			Position:     pos,
		},
	}, &hover)

	if hover.Contents.Kind != protocol.Markdown {
		t.Errorf("hover kind = %q, want %q", hover.Contents.Kind, protocol.Markdown)
	}
	want := "To specify or modify the hostname for the network server, use the hostname command"
	if !strings.Contains(hover.Contents.Value, want) {
		t.Errorf("hover value missing hostname description %q; got %q", want, truncForLog(hover.Contents.Value, 120))
	}
}

// TestWire_Completion asks for completions on the trailing top-level line
// and asserts a non-empty list whose documentation is markup content.
func TestWire_Completion(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureFixed)

	// The fixture ends with "\n", so the final (empty) line is a top-level
	// completion context offering the full keyword list.
	lastLine := uint(strings.Count(wireFixtureFixed, "\n"))
	var list protocol.CompletionList
	s.Call("textDocument/completion", protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
			Position:     protocol.Position{Line: lastLine, Character: 0},
		},
	}, &list)

	if len(list.Items) == 0 {
		t.Fatal("completion returned no items, want the keyword list")
	}
	var hostname *protocol.CompletionItem
	for i := range list.Items {
		if list.Items[i].Label == "hostname" {
			hostname = &list.Items[i]
			break
		}
	}
	if hostname == nil {
		t.Fatalf("hostname completion item not found among %d items", len(list.Items))
	}
	// Documentation is a MarkupContent on the wire; the DTO field is `any`,
	// so re-marshal the decoded value to assert its shape.
	if hostname.Documentation == nil {
		t.Fatal("hostname completion Documentation = nil, want MarkupContent")
	}
	raw, err := json.Marshal(hostname.Documentation)
	if err != nil {
		t.Fatalf("re-marshal documentation: %v", err)
	}
	var mc protocol.MarkupContent
	if err := json.Unmarshal(raw, &mc); err != nil {
		t.Fatalf("documentation is not MarkupContent-shaped: %v (%s)", err, raw)
	}
	if mc.Kind != protocol.PlainText {
		t.Errorf("documentation kind = %q, want %q", mc.Kind, protocol.PlainText)
	}
	if !strings.Contains(mc.Value, "hostname") {
		t.Errorf("documentation value does not mention hostname: %q", truncForLog(mc.Value, 120))
	}
}

// TestWire_Definition jumps from an ACL reference to its definition over
// the wire.
func TestWire_Definition(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureFixed)

	// Cursor inside FOO on the first "ip access-group FOO in" line.
	ref := nthPositionOf(t, wireFixtureFixed, "FOO in", 1)
	ref.Character++ // middle of the token
	var locs []protocol.Location
	s.Call("textDocument/definition", protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
			Position:     ref,
		},
	}, &locs)

	if len(locs) != 1 {
		t.Fatalf("definition: got %d locations, want 1: %+v", len(locs), locs)
	}
	defPos := nthPositionOf(t, wireFixtureFixed, "ip access-list standard FOO", 1)
	if locs[0].URI != wireURI {
		t.Errorf("definition uri = %q, want %q", locs[0].URI, wireURI)
	}
	if locs[0].Range.Start.Line != defPos.Line {
		t.Errorf("definition line = %d, want %d", locs[0].Range.Start.Line, defPos.Line)
	}
}

// TestWire_References asks for all references to the defined ACL from one
// of its use sites, declaration included.
func TestWire_References(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureFixed)

	// Cursor inside FOO on the second "ip access-group FOO in" line.
	ref := nthPositionOf(t, wireFixtureFixed, "FOO in", 2)
	ref.Character++
	var locs []protocol.Location
	s.Call("textDocument/references", protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
			Position:     ref,
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	}, &locs)

	// 2 references + 1 declaration.
	if len(locs) != 3 {
		t.Fatalf("references: got %d locations, want 3: %+v", len(locs), locs)
	}
	defLine := nthPositionOf(t, wireFixtureFixed, "ip access-list standard FOO", 1).Line
	var foundDecl bool
	for _, l := range locs {
		if l.URI != wireURI {
			t.Errorf("reference uri = %q, want %q", l.URI, wireURI)
		}
		if l.Range.Start.Line == defLine {
			foundDecl = true
		}
	}
	if !foundDecl {
		t.Errorf("no reference location on declaration line %d", defLine)
	}
}

// TestWire_DocumentSymbol asserts the outline decodes over the wire with
// the expected names and LSP symbol kinds.
func TestWire_DocumentSymbol(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureFixed)

	var syms []protocol.DocumentSymbol
	s.Call("textDocument/documentSymbol", protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
	}, &syms)

	// name -> expected LSP SymbolKind (see the feature's lspSymbolKind map).
	want := map[string]int{
		"GigabitEthernet0/0": protocol.SymbolKindInterface, // 11
		"GigabitEthernet1/0": protocol.SymbolKindInterface, // 11
		"FOO":                protocol.SymbolKindNamespace, // 3 (ACL)
		"RM":                 protocol.SymbolKindClass,     // 5 (route-map)
		"r1":                 protocol.SymbolKindVariable,  // 13 (hostname)
	}
	if len(syms) != len(want) {
		t.Fatalf("documentSymbol: got %d symbols, want %d: %+v", len(syms), len(want), syms)
	}
	got := make(map[string]int, len(syms))
	for _, sym := range syms {
		got[sym.Name] = sym.Kind
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("symbol %q kind = %d, want %d (all kinds: %+v)", name, got[name], kind, got)
		}
	}
}

// TestWire_ShutdownExit performs the full lifecycle and asserts the server
// terminates cleanly once the editor closes the channel: shutdown request
// answered, exit notification accepted, Wait() returns nil.
func TestWire_ShutdownExit(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()

	if err := s.Shutdown(); err != nil {
		t.Fatalf("shutdown request: %v", err)
	}
	s.Close()
}
