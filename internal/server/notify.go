package server

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/dgethings/chunt/internal/protocol"
)

// jrpcServer safely retrieves the jrpc2 server from ctx. jrpc2.ServerFromContext
// panics when the context has no server attached (its key is unexported, so the
// value cannot be injected by callers). In production the server is always
// present (handlers run inside jrpc2's dispatch loop); the recover only fires
// in tests or misuse, yielding nil so the caller's nil-check skips the notify.
func jrpcServer(ctx context.Context) (srv *jrpc2.Server) {
	defer func() { recover() }()
	return jrpc2.ServerFromContext(ctx)
}

// publishDiagnostics notifies the client of diagnostics for uri, swallowing
// the notification when no jrpc2 server is available (e.g. in tests).
func publishDiagnostics(ctx context.Context, uri string, diags []protocol.Diagnostic) {
	if srv := jrpcServer(ctx); srv != nil {
		srv.Notify(ctx, "textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: diags,
		})
	}
}
