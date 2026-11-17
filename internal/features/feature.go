package features

import (
	"context"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
)

// PublishFunc publishes one document's diagnostic set to the client. It
// carries the Document (not just its URI) so the server can convert
// positions against the right content at the dispatch edge. Features may
// invoke it for documents other than the one being opened/changed — see
// the workspace (directory-as-one-config) model in the cisco_ios_jinja2
// package.
type PublishFunc func(d *document.Document, diags []protocol.Diagnostic)

type Feature interface {
	LanguageID() string
	Close() error

	// DidOpen/DidChange return the document's diagnostics. When publish is
	// non-nil it is called per tier (progressive publishing): once with the
	// tree-only passes before the symbol table is built, and once with the
	// full set after. It may also be called for OTHER documents — a feature
	// that analyzes files in context (e.g. all templates in a directory as
	// one logical config) republishes sibling files' diagnostics when one
	// file changes. The callback carries the Document each set belongs to
	// (needed for position-encoding conversion at the server edge). A nil
	// publish disables mid-pipeline publishing and the methods simply return
	// the full set (cmd/check, unit tests).
	DidOpen(ctx context.Context, doc *document.Document, publish PublishFunc) ([]protocol.Diagnostic, error)
	DidChange(ctx context.Context, doc *document.Document, publish PublishFunc) ([]protocol.Diagnostic, error)
	DidClose(ctx context.Context, doc *document.Document, publish PublishFunc) error

	Completion(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.CompletionItem, error)
	Hover(ctx context.Context, doc *document.Document, pos protocol.Position) (*protocol.HoverResult, error)
	Definition(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.Location, error)
	References(ctx context.Context, doc *document.Document, pos protocol.Position, includeDeclaration bool) ([]protocol.Location, error)
	DocumentSymbol(ctx context.Context, doc *document.Document) ([]protocol.DocumentSymbol, error)
}
