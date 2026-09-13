package features

import (
	"context"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
)

type Feature interface {
	LanguageID() string
	Close() error

	// DidOpen/DidChange return the document's diagnostics. When publish is
	// non-nil it is called per tier (progressive publishing): once with the
	// tree-only passes before the symbol table is built, and once with the
	// full set after. A nil publish disables mid-pipeline publishing and the
	// methods simply return the full set (cmd/check, unit tests).
	DidOpen(ctx context.Context, doc *document.Document, publish func([]protocol.Diagnostic)) ([]protocol.Diagnostic, error)
	DidChange(ctx context.Context, doc *document.Document, publish func([]protocol.Diagnostic)) ([]protocol.Diagnostic, error)
	DidClose(ctx context.Context, doc *document.Document) error

	Completion(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.CompletionItem, error)
	Hover(ctx context.Context, doc *document.Document, pos protocol.Position) (*protocol.HoverResult, error)
	Definition(ctx context.Context, doc *document.Document, pos protocol.Position) ([]protocol.Location, error)
	References(ctx context.Context, doc *document.Document, pos protocol.Position, includeDeclaration bool) ([]protocol.Location, error)
	DocumentSymbol(ctx context.Context, doc *document.Document) ([]protocol.DocumentSymbol, error)
}
