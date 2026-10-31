package protocol

// LSP 3.17 position-encoding kinds. Clients may offer the subset they can
// honor in the initialize request's general.positionEncodings; the server
// picks one and names it in capabilities.positionEncoding (omitting that
// field means utf-16, the pre-negotiation default).
const (
	PositionEncodingUTF8  = "utf-8"  // UTF-8 byte offsets
	PositionEncodingUTF16 = "utf-16" // UTF-16 code units
)

// GeneralClientCapabilities carries the client capabilities that do not fit
// a feature-specific slot, including the position encodings the client can
// honor. VS Code's client offers only utf-16; servers must answer with one
// of the offered values (or omit positionEncoding, meaning utf-16).
type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

type InitializeParams struct {
	ClientInfo *ClientInfo                `json:"clientInfo"`
	General    *GeneralClientCapabilities `json:"general,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   ServerInfo         `json:"serverInfo"`
}

type ServerCapabilities struct {
	PositionEncoding       string             `json:"positionEncoding,omitempty"`
	TextDocumentSync       int                `json:"textDocumentSync"`
	HoverProvider          bool               `json:"hoverProvider"`
	DefinitionProvider     bool               `json:"definitionProvider"`
	ReferencesProvider     bool               `json:"referencesProvider"`
	DocumentSymbolProvider bool               `json:"documentSymbolProvider"`
	CompletionProvider     *CompletionOptions `json:"completionProvider,omitempty"`
}

type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
