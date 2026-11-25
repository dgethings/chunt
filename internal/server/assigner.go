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
// gets through (assign time for the pre-initialize boundary, dispatch time
// for the post-shutdown one — see Assign):
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
//
// The gate runs in two phases (chunt-5ks):
//
//   - ASSIGN time: jrpc2 resolves Assign when a message batch is dequeued,
//     strictly in stream order (batches are dequeued FIFO and assigned
//     under the server's queue lock). initialize marks itself "seen" on the
//     Server here; anything assigned before it is genuinely pre-handshake
//     traffic and is rejected. An assign-time STATE check cannot make this
//     distinction: a client that pipelines many messages into a single
//     write (initialize, initialized, didOpen, ...) has everything after
//     initialize assigned while the initialize handler has not yet set the
//     state, silently dropping the pipelined notifications and failing the
//     requests with misleading errors.
//   - DISPATCH time: jrpc2's ServerOptions.Concurrency "does not constrain
//     order of issue", so a pipelined message's handler can run while the
//     initialize (or shutdown) handler is still in flight. The wrapper
//     checks the shutdown boundary when the handler actually runs, which
//     is both more permissive (messages following initialize in stream
//     order pass while the handshake is in flight) and more strict (a
//     request pipelined after shutdown is rejected once shutdown has
//     landed, instead of racing the teardown).
//
// A pipelined message may therefore run before the initialize handler
// completes; that is safe because the only state Initialize writes
// (posEncoding, state) is mutex-guarded. Notifications still hit the gate
// — jrpc2 discards errors reported for notification handlers — preserving
// the spec's drop rules.
func (g stateGate) Assign(ctx context.Context, method string) jrpc2.Handler {
	h := g.methods.Assign(ctx, method)
	if h == nil {
		return nil
	}
	if method == "initialize" {
		g.srv.markInitializeSeen()
	} else if !g.srv.initializeSeen() {
		return reject(codeServerNotInitialized, "method %q requested before initialize", method)
	}
	return func(ctx context.Context, req *jrpc2.Request) (any, error) {
		if g.srv.getState() == stateShutDown {
			return nil, jrpc2.Errorf(jrpc2.InvalidRequest, "method %q requested after shutdown", method)
		}
		return h(ctx, req)
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
