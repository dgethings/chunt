package protocol

// DocumentSymbolParams is the parameter object for
// textDocument/documentSymbol. Editors call this once per document to build
// the breadcrumb/outline view; the server returns a flat list of top-level
// symbols (with optional nested children).
//
// LSP spec: https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textDocument_documentSymbol
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbol represents one entry in the document outline. Range is the
// full extent of the symbol (e.g. an entire section header + body);
// SelectionRange is the subrange the editor should highlight / scroll to
// when the user picks this entry (typically just the name token).
//
// The Children field supports nesting (e.g. a policy-map with one child per
// `class` block); for v1 chunt returns a flat list and leaves Children
// empty.
type DocumentSymbol struct {
	Name           string               `json:"name"`
	Detail         string               `json:"detail,omitempty"`
	Kind           int                  `json:"kind"`
	Tags           []int                `json:"tags,omitempty"`
	Range          Range                `json:"range"`
	SelectionRange Range                `json:"selectionRange"`
	Children       []DocumentSymbol     `json:"children,omitempty"`
}

// LSP SymbolKind enum values. Only the kinds chunt uses are listed; the
// full enum is at
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#symbolKind
const (
	SymbolKindNamespace = 3
	SymbolKindClass     = 5
	SymbolKindMethod    = 6
	SymbolKindField     = 8
	SymbolKindInterface = 11
	SymbolKindFunction  = 12
	SymbolKindVariable  = 13
	SymbolKindConstant  = 14
	SymbolKindNumber    = 16
)
