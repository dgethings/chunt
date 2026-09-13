package server

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

// codeServerNotInitialized is ErrorCodes.ServerNotInitialized from the LSP
// specification: the recommended reply to any request that arrives before
// the initialize handshake has completed. jrpc2 has no constant for it
// because it is LSP-specific rather than JSON-RPC-standard.
const codeServerNotInitialized = jrpc2.Code(-32002)

// stateGate wraps the method map and enforces the LSP lifecycle state
// machine at the protocol layer, where editors actually exercise it. The
// Server's own handlers stay stateless about dispatch; the gate decides who
// gets through:
//
//   - Before initialize: every request except "initialize" itself is
//     rejected with -32002 ServerNotInitialized. Notifications are dropped —
//     jrpc2 discards errors reported for notification handlers — which is
//     exactly the spec's "drop notifications except exit" rule.
//   - After shutdown: every request is rejected with -32600 InvalidRequest
//     ("If a server receives requests after a shutdown request those
//     requests should error with InvalidRequest") and notifications are
//     dropped, keeping post-shutdown traffic away from state Shutdown has
//     torn down. Shutdown closes the registered features (freeing their
//     tree-sitter parsers) and empties the router registry; a didChange
//     that ran anyway would still overwrite the document store while
//     log-spamming "no feature registered", and under server options with
//     Concurrency > 1 a handler racing Close could touch a freed parser.
//
// "exit" needs no entry in the method map: chunt terminates when the client
// closes the transport (the stdio EOF that follows an editor's exit
// notification), so exit keeps working in every lifecycle state by staying
// an unknown — and therefore dropped — notification.
type stateGate struct {
	srv     *Server
	methods handler.Map
}

// Assign implements jrpc2.Assigner. Returning nil keeps jrpc2's default
// handling for unknown methods: requests get -32601 MethodNotFound and
// notifications are dropped.
func (g stateGate) Assign(ctx context.Context, method string) jrpc2.Handler {
	h := g.methods.Assign(ctx, method)
	if h == nil {
		return nil
	}
	switch g.srv.getState() {
	case stateInitialized:
		return h
	case stateCreated:
		if method == "initialize" {
			return h
		}
		return reject(codeServerNotInitialized, "method %q requested before initialize", method)
	default: // stateShutDown, including a second initialize on a dead server
		return reject(jrpc2.InvalidRequest, "method %q requested after shutdown", method)
	}
}

// Names implements the optional jrpc2.Namer interface, delegating to the
// wrapped method map.
func (g stateGate) Names() []string { return g.methods.Names() }

// reject builds a handler that fails every request with the given code.
func reject(code jrpc2.Code, msg string, args ...any) jrpc2.Handler {
	return func(context.Context, *jrpc2.Request) (any, error) {
		return nil, jrpc2.Errorf(code, msg, args...)
	}
}
