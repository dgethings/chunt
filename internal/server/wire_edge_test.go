package server_test

// Wire-level protocol edge cases, building on the WireSession harness from
// wire_test.go. Where wire_test.go drives the happy editor session, this
// file drives the unhappy paths an LSP server must survive: exit without
// shutdown, traffic before initialize and after shutdown, unknown methods,
// duplicate/unknown document sync notifications, and malformed payloads.
//
// Every test here locks in ACTUAL behavior as regression protection. The
// lifecycle cases assert the stateGate assigner (internal/server/assigner.go):
// requests before initialize -> -32002 ServerNotInitialized, requests after
// shutdown -> -32600 InvalidRequest, notifications in those states dropped.
// The remaining error shapes (-32601, -32602, framing) are jrpc2 defaults,
// asserted here so a wiring change that loses them fails loudly.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/server"
)

// codeServerNotInitialized mirrors the LSP constant in internal/server
// (kept local so the tests fail on the wire behavior, not on a refactor).
const codeServerNotInitialized = jrpc2.Code(-32002)

// callErr performs a JSON-RPC request that is expected to fail and returns
// the wire error, failing the test if the request unexpectedly succeeds.
func (s *WireSession) callErr(method string, params any) *jrpc2.Error {
	s.t.Helper()
	_, err := s.client.Call(s.ctx, method, params)
	if err == nil {
		s.t.Fatalf("Call %q: unexpectedly succeeded, want error", method)
	}
	var jerr *jrpc2.Error
	if !errors.As(err, &jerr) {
		s.t.Fatalf("Call %q: got error %v (%T), want *jrpc2.Error", method, err, err)
	}
	return jerr
}

// expectCode asserts a wire error carries the given JSON-RPC code.
func expectCode(t *testing.T, context string, err *jrpc2.Error, want jrpc2.Code) {
	t.Helper()
	if got := jrpc2.ErrorCode(err); got != want {
		t.Errorf("%s: error code = %d, want %d; full error: %v", context, got, want, err)
	}
	if err.Message == "" {
		t.Errorf("%s: error message is empty, want non-empty", context)
	}
}

// assertNoPush asserts the diagnostics stream stays quiet for wireSettle:
// a dropped or rejected notification must not publish diagnostics.
func (s *WireSession) assertNoPush(context string) {
	s.t.Helper()
	select {
	case p := <-s.diags:
		s.t.Errorf("%s: unexpected publishDiagnostics push for %s (%d diagnostics)",
			context, p.URI, len(p.Diagnostics))
	case <-time.After(wireSettle):
	}
}

// waitServer asserts the server terminates within wireTimeout and returns
// its Wait() error (nil meaning a clean stop).
func (s *WireSession) waitServer() error {
	s.t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.jrpcSrv.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(wireTimeout):
		s.t.Fatalf("server did not terminate within %v", wireTimeout)
		return nil
	}
}

// TestWire_ExitWithoutShutdown locks in case: an exit notification with no
// prior shutdown request. The server must terminate cleanly — Wait() returns
// nil without hanging. chunt has no in-band exit handler: termination is
// driven by the transport closing (stdio EOF), which is what editors do
// right after sending exit. The spec's distinct process exit codes
// (0 after shutdown, 1 otherwise) are not observable over an in-memory
// channel and are not implemented; that deviation is documented in the PR.
func TestWire_ExitWithoutShutdown(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureInitial) // a realistic mid-session exit

	s.Notify("exit", nil)
	if err := s.client.Close(); err != nil {
		t.Logf("client close: %v", err)
	}
	if err := s.waitServer(); err != nil {
		t.Errorf("server Wait() after exit-without-shutdown: %v, want nil", err)
	}
	s.cancel()
}

// TestWire_RequestBeforeInitialize verifies the pre-initialize gate. LSP
// spec: requests other than initialize should reply -32002
// (ServerNotInitialized); notifications are dropped, except exit. All of
// that must leave the session healthy enough to complete the handshake
// afterwards. (Before the stateGate assigner, these requests were
// dispatched straight to their handlers and answered with jrpc2's default
// -32603 InternalError — a documented-now-fixed spec deviation.)
func TestWire_RequestBeforeInitialize(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	// Deliberately NO Initialize().

	hoverErr := s.callErr("textDocument/hover", nil)
	expectCode(t, "hover before initialize", hoverErr, codeServerNotInitialized)

	sdErr := s.callErr("shutdown", nil)
	expectCode(t, "shutdown before initialize", sdErr, codeServerNotInitialized)

	// Notifications before initialize are dropped, not errors: this didOpen
	// must not publish diagnostics.
	s.Notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        wireURI,
			LanguageID: "cisco_ios_jinja2",
			Version:    1,
			Text:       wireFixtureInitial,
		},
	})
	s.assertNoPush("didOpen before initialize")

	// None of the rejections killed the session: the handshake still works
	// and the server functions normally afterwards.
	s.Initialize()
	push := s.DidOpen(wireFixtureFixed)
	if len(push.Diagnostics) != 0 {
		t.Errorf("didOpen after late initialize: %d diagnostics, want 0: %+v",
			len(push.Diagnostics), push.Diagnostics)
	}
}

// TestWire_UnknownMethod verifies jrpc2's default handling of unregistered
// methods over chunt's wiring: requests get -32601 MethodNotFound with the
// method name echoed in error.data, and unknown notifications are dropped
// without killing the session.
func TestWire_UnknownMethod(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()

	reqErr := s.callErr("textDocument/nonesuch", nil)
	expectCode(t, "unknown method request", reqErr, jrpc2.MethodNotFound)
	if data := fmt.Sprint(reqErr.Data); !strings.Contains(data, "textDocument/nonesuch") {
		t.Errorf("unknown method error data = %q, want it to mention the method name", data)
	}

	// Unknown notification: dropped silently, session survives.
	s.Notify("chunt/completelyMadeUp", map[string]any{"whatever": true})

	// Survival proof: known traffic still works on the same session.
	s.DidOpen(wireFixtureFixed)
	var syms []protocol.DocumentSymbol
	s.Call("textDocument/documentSymbol", protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
	}, &syms)
	if len(syms) == 0 {
		t.Error("documentSymbol returned no symbols after unknown notification, want the fixture outline")
	}
}

// TestWire_DidOpenTwice locks in behavior for a didOpen on an already-open
// document (LSP says clients must not do this; the server tolerates it).
// The document store entry is replaced — content and version — and the
// feature re-parses and publishes the new content's diagnostics, so the
// last didOpen wins. No crash, no duplicate-document state.
func TestWire_DidOpenTwice(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()

	first := s.DidOpen(wireFixtureInitial)
	if len(first.Diagnostics) != 1 {
		t.Fatalf("first didOpen: %d diagnostics, want 1 (undefined acl)", len(first.Diagnostics))
	}

	// Re-open the same URI with resolved content and a bumped version.
	s.Notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        wireURI,
			LanguageID: "cisco_ios_jinja2",
			Version:    7,
			Text:       wireFixtureFixed,
		},
	})
	second := s.AwaitSettledDiagnostics()
	if len(second.Diagnostics) != 0 {
		t.Errorf("second didOpen: %d diagnostics, want 0 (store holds the replacement text): %+v",
			len(second.Diagnostics), second.Diagnostics)
	}

	// The replacement really took: a follow-up didChange is applied on top
	// of the second didOpen's content, not the first.
	third := s.DidChangeFull(8, wireFixtureInitial)
	if len(third.Diagnostics) != 1 {
		t.Errorf("didChange after re-open: %d diagnostics, want 1 (back to undefined acl): %+v",
			len(third.Diagnostics), third.Diagnostics)
	}
}

// TestWire_DidChangeUnknownDoc locks in behavior for didChange on documents
// the server does not know: never-opened URIs, and URIs whose document was
// removed by didClose. server.go logs and returns nil — the notification is
// dropped, nothing is published, and the session stays healthy.
func TestWire_DidChangeUnknownDoc(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()

	// didChange for a document that was never opened.
	s.Notify("textDocument/didChange", protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			URI:     "file:///never-opened.ios.j2",
			Version: 2,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: wireFixtureInitial}},
	})
	s.assertNoPush("didChange for never-opened document")

	// didChange after didClose removed the document from the store.
	s.DidOpen(wireFixtureInitial)
	s.Notify("textDocument/didClose", protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
	})
	s.Notify("textDocument/didChange", protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			URI:     wireURI,
			Version: 9,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: wireFixtureFixed}},
	})
	s.assertNoPush("didChange after didClose")

	// Session still healthy: reopening publishes diagnostics again.
	reopened := s.DidOpen(wireFixtureFixed)
	if len(reopened.Diagnostics) != 0 {
		t.Errorf("didOpen after dropped didChanges: %d diagnostics, want 0: %+v",
			len(reopened.Diagnostics), reopened.Diagnostics)
	}
}

// TestWire_ShutdownStateMachine locks in the post-shutdown behavior over
// the wire: a second shutdown and any further request answer -32600
// InvalidRequest, and notifications are dropped. The dropped-didChange
// assertion also regression-locks that no post-shutdown traffic touches
// the torn-down feature state (Shutdown frees the tree-sitter parsers and
// empties the router registry).
func TestWire_ShutdownStateMachine(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureInitial)

	if err := s.Shutdown(); err != nil {
		t.Fatalf("first shutdown request: %v", err)
	}

	second := s.callErr("shutdown", nil)
	expectCode(t, "second shutdown", second, jrpc2.InvalidRequest)

	hoverErr := s.callErr("textDocument/hover", nil)
	expectCode(t, "hover after shutdown", hoverErr, jrpc2.InvalidRequest)

	// Notifications after shutdown are dropped — and must not reach the
	// closed feature.
	s.Notify("textDocument/didChange", protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			URI:     wireURI,
			Version: 3,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: wireFixtureFixed}},
	})
	s.assertNoPush("didChange after shutdown")

	// And the server still exits cleanly (Close skips its own shutdown: one
	// was already sent).
	s.Close()
}

// TestWire_MalformedParams verifies that a known method invoked with params
// that cannot decode into its handler DTO answers -32602 InvalidParams as a
// clean JSON-RPC error, and the session survives.
func TestWire_MalformedParams(t *testing.T) {
	t.Parallel()
	s := NewWireSession(t)
	s.Initialize()
	s.DidOpen(wireFixtureFixed)

	// Wrong field types for HoverParams.
	bad := s.callErr("textDocument/hover", map[string]any{
		"textDocument": 42,           // want an object
		"position":     "not-at-all", // want an object
	})
	expectCode(t, "hover with mistyped params", bad, jrpc2.InvalidParams)

	// Params of the wrong shape entirely: an array where an object is
	// required.
	arr := s.callErr("textDocument/documentSymbol", []any{1, 2, 3})
	expectCode(t, "documentSymbol with array params", arr, jrpc2.InvalidParams)

	// Survival proof: a well-formed request still succeeds.
	pos := nthPositionOf(t, wireFixtureFixed, "hostname", 1)
	var hover protocol.HoverResult
	s.Call("textDocument/hover", protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: wireURI},
			Position:     pos,
		},
	}, &hover)
	if hover.Contents.Kind != protocol.Markdown {
		t.Errorf("hover after malformed requests: kind = %q, want %q", hover.Contents.Kind, protocol.Markdown)
	}
}

// rawWire is a chunt server over an in-memory Content-Length-framed pipe
// with NO jrpc2 client, so tests can write arbitrary raw bytes — invalid
// JSON bodies, broken framing — exactly like a broken or hostile transport.
type rawWire struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	wait func() error
}

func newRawWire(t *testing.T) *rawWire {
	t.Helper()

	srv := server.New("wiretest")
	srv.RegisterFeature(cisco_ios_jinja2.New())
	assigner, err := srv.Assigner()
	if err != nil {
		t.Fatalf("srv.Assigner(): %v", err)
	}
	cliConn, srvConn := net.Pipe()
	jrpcSrv := jrpc2.NewServer(assigner, &jrpc2.ServerOptions{
		AllowPush:   true,
		Concurrency: 1,
	})
	jrpcSrv.Start(channel.Header("")(srvConn, srvConn))
	t.Cleanup(func() {
		cliConn.Close()
		jrpcSrv.Stop()
		_ = jrpcSrv.Wait()
	})
	return &rawWire{
		t:    t,
		conn: cliConn,
		r:    bufio.NewReader(cliConn),
		wait: jrpcSrv.Wait,
	}
}

// send writes one Content-Length-framed message with body as the payload.
func (w *rawWire) send(body string) {
	w.t.Helper()
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	if _, err := io.WriteString(w.conn, frame); err != nil {
		w.t.Fatalf("write raw frame: %v", err)
	}
}

// recv reads one Content-Length-framed message and returns its payload.
func (w *rawWire) recv() []byte {
	w.t.Helper()
	if err := w.conn.SetReadDeadline(time.Now().Add(wireTimeout)); err != nil {
		w.t.Fatalf("set read deadline: %v", err)
	}
	length := -1
	for {
		line, err := w.r.ReadString('\n')
		if err != nil {
			w.t.Fatalf("read frame header %q: %v", line, err)
		}
		if line = strings.TrimRight(line, "\r\n"); line == "" {
			break
		}
		const prefix = "Content-Length:"
		if strings.HasPrefix(line, prefix) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			if err != nil {
				w.t.Fatalf("parse %q: %v", line, err)
			}
			length = n
		}
	}
	if length < 0 {
		w.t.Fatal("response frame has no Content-Length header")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(w.r, buf); err != nil {
		w.t.Fatalf("read %d-byte frame body: %v", length, err)
	}
	return buf
}

// TestWire_RawMalformedBody feeds the server a frame whose body is not
// valid JSON. jrpc2 answers with a JSON-RPC error addressed to id null
// (code -32700 ParseError, message "invalid request value") and keeps the
// session alive: the very next message on the same connection is a
// successful initialize. (Only channel-level framing damage is
// unrecoverable — see TestWire_RawBrokenFraming.)
func TestWire_RawMalformedBody(t *testing.T) {
	t.Parallel()
	w := newRawWire(t)

	w.send(`{"jsonrpc": "2.0", "id": 1, "method":`) // truncated JSON body

	var er struct {
		ID    *int `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.recv(), &er); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if er.ID != nil {
		t.Errorf("error response id = %d, want null (the request id was unrecoverable)", *er.ID)
	}
	if er.Error.Code != int(jrpc2.ParseError) {
		t.Errorf("error response code = %d, want %d (ParseError for an unparseable body)", er.Error.Code, jrpc2.ParseError)
	}

	// The session survived: a valid initialize on the same connection works.
	w.send(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`)
	var ok struct {
		ID     int `json:"id"`
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.recv(), &ok); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if ok.ID != 2 {
		t.Errorf("initialize response id = %d, want 2", ok.ID)
	}
	if ok.Result.ServerInfo.Name != "chunt" {
		t.Errorf("initialize response serverInfo.name = %q, want %q", ok.Result.ServerInfo.Name, "chunt")
	}
}

// TestWire_RawBrokenFraming documents the unrecoverable case: bytes that
// violate the Content-Length framing itself. jrpc2 treats a receive failure
// as fatal and stops the server (Wait returns the framing error), which is
// the documented, desired behavior — a transport that garbles framing
// cannot be resynchronized. Asserted so it is locked in rather than
// accidental.
func TestWire_RawBrokenFraming(t *testing.T) {
	t.Parallel()
	w := newRawWire(t)

	if _, err := io.WriteString(w.conn, "this is not a Content-Length header\r\n\r\n"); err != nil {
		t.Fatalf("write unframed garbage: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- w.wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("server stopped cleanly on broken framing, want a framing error from Wait()")
		}
	case <-time.After(wireTimeout):
		t.Fatalf("server did not stop within %v after broken framing", wireTimeout)
	}
}
