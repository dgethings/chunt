package document_test

import (
	"bytes"
	"testing"

	"github.com/dgethings/chunt/internal/document"
)

// TestNew_NormalizesCRLF locks in chunt-ro9: the document layer stores
// content with CRLF line endings rewritten to LF, so tree-sitter parse
// ranges, node text, and per-line position math all run on one canonical
// byte stream no matter what line endings the client sends.
func TestNew_NormalizesCRLF(t *testing.T) {
	t.Parallel()
	cases := []struct {
		what string
		in   string
		want string
	}{
		{"LF passthrough", "a\nb\n", "a\nb\n"},
		{"CRLF to LF", "a\r\nb\r\n", "a\nb\n"},
		{"mixed endings", "a\r\nb\nc\r\n", "a\nb\nc\n"},
		{"CRLF without trailing newline", "a\r\nb", "a\nb"},
		{"empty content", "", ""},
		{"CR-only stays CR (out of scope; treated as line text)", "a\rb", "a\rb"},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			t.Parallel()
			doc := document.New("file:///x.ios.j2", "cisco_ios_jinja2", 1, []byte(tc.in))
			if got := string(doc.Content); got != tc.want {
				t.Errorf("New(%q).Content = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNew_LFIsUnallocated asserts the fast path: LF-only content is stored
// as the caller's slice, not a copy, so the common case costs nothing.
func TestNew_LFIsUnallocated(t *testing.T) {
	t.Parallel()
	src := []byte("hostname r1\n!\n")
	doc := document.New("file:///x.ios.j2", "cisco_ios_jinja2", 1, src)
	if &doc.Content[0] != &src[0] {
		t.Error("New with LF content copied the slice; want the caller's slice stored as-is")
	}
}

// TestSetContent_NormalizesCRLF locks in the didChange path: SetContent
// (which textDocument/didChange must use instead of assigning Content
// directly) normalizes exactly like New, so a CRLF buffer re-sent on every
// change cannot re-introduce raw CR bytes mid-session.
func TestSetContent_NormalizesCRLF(t *testing.T) {
	t.Parallel()
	doc := document.New("file:///x.ios.j2", "cisco_ios_jinja2", 1, []byte("a\nb\n"))
	doc.SetContent([]byte("hostname r{{\r\nip access-group A in\r\n"), 7)
	want := "hostname r{{\nip access-group A in\n"
	if got := string(doc.Content); got != want {
		t.Errorf("SetContent CRLF: Content = %q, want %q", got, want)
	}
	if doc.Version != 7 {
		t.Errorf("SetContent: Version = %d, want 7", doc.Version)
	}

	// And back to LF content: normalized is a no-op there.
	doc.SetContent([]byte("a\nb\n"), 8)
	if got := string(doc.Content); got != "a\nb\n" {
		t.Errorf("SetContent LF: Content = %q, want %q", got, "a\nb\n")
	}
}

// TestContentLine_CRLF asserts the position-conversion line accessor
// against normalized CRLF content: lines come back without terminators,
// which keeps the UTF-16 <-> byte column math identical to the LF view.
func TestContentLine_CRLF(t *testing.T) {
	t.Parallel()
	doc := document.New("file:///x.ios.j2", "cisco_ios_jinja2", 1, []byte("ip address 10.0.0.1\r\nno shutdown\r\n"))
	for line, want := range map[uint]string{
		0: "ip address 10.0.0.1",
		1: "no shutdown",
		2: "", // past EOF
	} {
		if got := doc.ContentLine(line); got != want {
			t.Errorf("ContentLine(%d) = %q, want %q", line, got, want)
		}
	}
	// Byte/UTF-16 round trip on a normalized line stays self-consistent.
	line := doc.ContentLine(0)
	if got := document.UTF16ToByte(line, 10); got != 10 {
		t.Errorf("UTF16ToByte(line, 10) = %d, want 10", got)
	}
	if got := document.ByteToUTF16(line, 10); got != 10 {
		t.Errorf("ByteToUTF16(line, 10) = %d, want 10", got)
	}
}

// TestNormalizeParseEquivalence proves the motivating behavior end to end
// at the document layer: the same logical text with CRLF and LF endings
// yields identical Content.
func TestNormalizeParseEquivalence(t *testing.T) {
	t.Parallel()
	lf := []byte("interface Gi0/0\n ip access-group A in\n!\n")
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	lfDoc := document.New("file:///lf.ios.j2", "cisco_ios_jinja2", 1, lf)
	crlfDoc := document.New("file:///crlf.ios.j2", "cisco_ios_jinja2", 1, crlf)
	if !bytes.Equal(lfDoc.Content, crlfDoc.Content) {
		t.Errorf("CRLF content %q != LF content %q after normalization", crlfDoc.Content, lfDoc.Content)
	}
}
