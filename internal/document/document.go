package document

import "bytes"

// Document is a single text document known to the server: its identity,
// sync version, and content.
//
// Content is stored with line endings NORMALIZED to LF (see New). Every
// downstream consumer — tree-sitter parse ranges, node-text slices, the
// per-line position math in encoding.go — then runs on one canonical byte
// stream. LSP positions are (line, character) pairs in which a line's
// terminator occupies no addressable slot, so normalized columns stay
// consistent with the client's original CRLF buffer (chunt-ro9).
type Document struct {
	URI        string
	LanguageID string
	Version    int
	Content    []byte
	Lines      []string
}

// New builds a Document, normalizing CRLF line endings in content to LF.
func New(uri, languageID string, version int, content []byte) *Document {
	return &Document{
		URI:        uri,
		LanguageID: languageID,
		Version:    version,
		Content:    normalizeLineEndings(content),
	}
}

// SetContent replaces the document's text and sync version, normalizing
// line endings exactly like New. Handlers that mutate an existing Document
// in place (textDocument/didChange) must go through this instead of
// assigning Content directly: a CRLF buffer re-sent on every change would
// otherwise re-introduce raw CR bytes mid-session and shift parse ranges
// again.
func (d *Document) SetContent(content []byte, version int) {
	d.Content = normalizeLineEndings(content)
	d.Version = version
}

// normalizeLineEndings returns content with CRLF line endings rewritten to
// LF. Content without a CR (the common case) is returned unchanged — same
// slice, no allocation.
//
// CRLF must be normalized before parsing because tree-sitter absorbs the
// trailing CR into error regions and end-of-line tokens: on CRLF input a
// missing-"}}" ERROR node reports 0:12-13 instead of the LF input's 0:12-12,
// and every golden comparison breaks. Positions need no back-translation:
// LSP coordinates are per-line, a CR in a CRLF file only ever occupies the
// terminator slot at the end of a line, and no editor places a cursor or
// selection boundary between the CR and the LF — so a line's characters
// address identical text in both encodings.
func normalizeLineEndings(content []byte) []byte {
	if bytes.IndexByte(content, '\r') < 0 {
		return content
	}
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}
