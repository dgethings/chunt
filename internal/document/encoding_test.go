package document_test

// Tests for the wire-encoding column algebra (encoding.go): UTF-16 code
// units <-> UTF-8 byte offsets on a single line, and the line extraction
// ContentLine performs internally.

import (
	"testing"

	"github.com/dgethings/chunt/internal/document"
)

// lines with one rune of each width mult, in order: 1-byte ASCII, 2-byte
// (é), 3-byte (“‐ ”), 4-byte ASTRO-planes (𝄞, 😀). Byte offsets of each
// rune and their UTF-16 widths differ, which is exactly what the
// converters translate.
const mixedRuneLine = "aé“—𝄞😀z"

// mixed lists each rune of mixedRuneLine in order with its start byte
// offset and its UTF-16 column. The two scales diverge per the rune's
// UTF-16 width: 1 unit for BMP runes (2-3 UTF-8 bytes) and 2 units for
// astral runes (4 UTF-8 bytes, a surrogate pair).
var mixed = []struct {
	what    string
	byteAt  uint
	utf16At uint
}{
	{"ASCII 'a'", 0, 0},
	{"U+00E9 é (2 bytes, 1 unit)", 1, 1},
	{"U+201C “ (3 bytes, 1 unit)", 3, 2},
	{"U+2014 — (3 bytes, 1 unit)", 6, 3},
	{"U+1D11E 𝄞 (4 bytes, 2 units)", 9, 4},
	{"U+1F600 😀 (4 bytes, 2 units)", 13, 6},
	{"ASCII 'z'", 17, 8},
	{"line end", 18, 9},
}

func TestUTF16ToByte(t *testing.T) {
	t.Parallel()
	for _, tc := range mixed {
		got := document.UTF16ToByte(mixedRuneLine, tc.utf16At)
		if got != tc.byteAt {
			t.Errorf("%s: UTF16ToByte(%q, %d) = %d, want %d", tc.what, mixedRuneLine, tc.utf16At, got, tc.byteAt)
		}
	}
}

func TestByteToUTF16(t *testing.T) {
	t.Parallel()
	for _, tc := range mixed {
		got := document.ByteToUTF16(mixedRuneLine, tc.byteAt)
		if got != tc.utf16At {
			t.Errorf("%s: ByteToUTF16(%q, %d) = %d, want %d", tc.what, mixedRuneLine, tc.byteAt, got, tc.utf16At)
		}
	}
}

func TestUTF16ToByte_PastEndClamps(t *testing.T) {
	t.Parallel()
	if got, want := document.UTF16ToByte("ab", 100), uint(2); got != want {
		t.Errorf("UTF16ToByte(\"ab\", 100) = %d, want %d (line length)", got, want)
	}
	if got, want := document.UTF16ToByte("", 7), uint(0); got != want {
		t.Errorf("UTF16ToByte(\"\", 7) = %d, want 0", got)
	}
	// 0.5 code units into a surrogate pair: clamp to the rune start.
	if got, want := document.UTF16ToByte(mixedRuneLine, 5), uint(9); got != want {
		t.Errorf("mid-surrogate UTF16ToByte = %d, want %d (start of 𝄞)", got, want)
	}
}

func TestByteToUTF16_PastEndClampsAndRoundsDown(t *testing.T) {
	t.Parallel()
	if got, want := document.ByteToUTF16("ab", 100), uint(2); got != want {
		t.Errorf("ByteToUTF16(\"ab\", 100) = %d, want %d (line length)", got, want)
	}
	// A byte offset inside a multi-byte rune reports the rune start's
	// column, the same column tree-sitter would give for that rune.
	if got, want := document.ByteToUTF16(mixedRuneLine, 2), uint(1); got != want {
		t.Errorf("ByteToUTF16 inside é = %d, want %d (é starts at byte 1)", got, want)
	}
}

func TestEncodingRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range mixed {
		if back := document.ByteToUTF16(mixedRuneLine, document.UTF16ToByte(mixedRuneLine, tc.utf16At)); back != tc.utf16At {
			t.Errorf("%s: UTF16ToByte then ByteToUTF16 = %d, want %d", tc.what, back, tc.utf16At)
		}
		if back := document.UTF16ToByte(mixedRuneLine, document.ByteToUTF16(mixedRuneLine, tc.byteAt)); back != tc.byteAt {
			t.Errorf("%s: ByteToUTF16 then UTF16ToByte = %d, want %d", tc.what, back, tc.byteAt)
		}
	}
}

func TestContentLine(t *testing.T) {
	t.Parallel()
	doc := document.New("file:///lines", "x", 1, []byte("alpha\nbeta\r\ngamma\n"))
	tests := []struct {
		n    uint
		want string
	}{
		{0, "alpha"},
		{1, "beta"}, // CRLF terminator stripped
		{2, "gamma"},
		{3, ""}, // trailing \n yields no line 3
		{9, ""},
	}
	for _, tc := range tests {
		if got := doc.ContentLine(tc.n); got != tc.want {
			t.Errorf("ContentLine(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}

	lastNoFooter := document.New("file:///n", "x", 1, []byte("one\ntwo"))
	if got := lastNoFooter.ContentLine(1); got != "two" {
		t.Errorf("filename without trailing newline: ContentLine(1) = %q, want %q", got, "two")
	}
	if got := lastNoFooter.ContentLine(2); got != "" {
		t.Errorf("ContentLine(2) past end = %q, want \"\"", got)
	}

	if got := (&document.Document{}).ContentLine(0); got != "" {
		t.Errorf("empty content: ContentLine(0) = %q, want \"\"", got)
	}
}
