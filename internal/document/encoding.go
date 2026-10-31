package document

// Encoding algebra between LSP wire positions and chunt's internal columns.
//
// chunt tracks positions internally as UTF-8 byte offsets: that is what
// tree-sitter Point.Column yields and consumes, and the whole feature layer
// (keywords, symbols, diagnostics) runs on that convention. LSP 3.17 lets
// the client negotiate the wire encoding at initialize time, and VS Code's
// client only ever negotiates UTF-16 code-unit offsets (its document offsets
// are UTF-16). Wire columns that are handed to (or read from) tree-sitter
// without translation would be wrong for every position past the first
// non-ASCII byte of a line, so the server layer converts at its dispatch
// edge and keeps everything below it byte-based.
//
// Both scales reset to zero at each line start, so these helpers operate on
// a single line's text and the callers look the line up from the document.

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// UTF16ToByte converts a UTF-16 code-unit offset into a UTF-8 byte offset
// within line. Offsets past the line end clamp to the line length; an
// offset that falls inside a surrogate pair rounds down to the rune start,
// matching how LSP clients produce boundary-aligned positions.
func UTF16ToByte(line string, utf16Col uint) uint {
	var (
		i   int
		col uint
	)
	for i < len(line) {
		r, w := utf8.DecodeRuneInString(line[i:])
		u := utf16Width(r)
		if u > utf16Col-col {
			break
		}
		col += u
		i += w
	}
	return uint(i)
}

// ByteToUTF16 converts a UTF-8 byte offset within line into UTF-16 code
// units. Offsets past the line end clamp to the line length; an offset
// landing inside a multi-byte rune rounds down to that rune's start
// (tree-sitter never produces mid-rune columns, but the round-down keeps
// the two directions coherent).
func ByteToUTF16(line string, byteCol uint) uint {
	var (
		i     int
		units uint
	)
	for i < len(line) {
		r, w := utf8.DecodeRuneInString(line[i:])
		if uint(i)+uint(w) > byteCol {
			break
		}
		units += utf16Width(r)
		i += w
	}
	return units
}

// utf16Width reports how many UTF-16 code units r occupies: two for runes
// outside the Basic Multilingual Plane (a surrogate pair), one otherwise.
func utf16Width(r rune) uint {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// ContentLine returns line n (0-indexed) of Content without its terminator
// (LF, or CRLF), or "" when the document has no such line, including for an
// empty Content.
//
// Document.Lines exists but is deliberately left unpopulated by New (see
// Document), so line text is extracted from Content on demand here rather
// than falling back to an index that callers may have not filled in.
func (d *Document) ContentLine(n uint) string {
	if d == nil || len(d.Content) == 0 {
		return ""
	}
	content := d.Content
	for {
		i := bytes.IndexByte(content, '\n')
		segment := content
		if i >= 0 {
			segment = content[:i]
		}
		if n == 0 {
			return strings.TrimSuffix(string(segment), "\r")
		}
		if i < 0 {
			return ""
		}
		content = content[i+1:]
		n--
	}
}
