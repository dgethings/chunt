package cisco_ios_jinja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
)

func TestHoverHostname(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: !
	//       line 1: ! version 26.1.0
	//       line 2: !
	//       line 3: hostname test
	//       line 4: !
	content := []byte("!\n! version 26.1.0\n!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover on "hostname" — line 3, character 0 (zero-based)
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 3, Character: 0})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	// hostname now carries Usage/Examples/Defaults/Command-History data, so the
	// hover is a structured Markdown document whose leading text is still the
	// description (chunt-97u). Assert the description is present and that the
	// rich data promoted it to Markdown.
	desc := "To specify or modify the hostname for the network server, use the hostname command in global configuration mode."
	if !strings.Contains(result.Contents.Value, desc) {
		t.Errorf("hover value missing description %q; got prefix %q", desc, truncForLog(result.Contents.Value, 120))
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Errorf("hover kind = %q, want %q (hostname has rich data)", result.Contents.Kind, protocol.Markdown)
	}
}

func TestHoverNoKeyword(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	content := []byte("!\n! version 26.1.0\n!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover on the version line — not a documented keyword
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 1, Character: 1})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	// Currently returns a zero-value HoverResult for unknown nodes; adjust
	// if you change nodeByPosition to return nil for misses.
	if result != nil && result.Contents.Value != "" {
		t.Errorf("expected empty/nil hover for undocumented node, got %q", result.Contents.Value)
	}
}

func TestHoverHostnameKeywordEnd(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: !
	//       line 1: ! version 26.1.0
	//       line 2: !
	//       line 3: hostname test
	//       line 4: !
	content := []byte("!\n! version 26.1.0\n!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover at the END of "hostname" — line 3, character 8 (zero-based).
	// This boundary resolves to the hostname_statement node, not the leaf.
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 3, Character: 8})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	desc := "To specify or modify the hostname for the network server, use the hostname command in global configuration mode."
	if !strings.Contains(result.Contents.Value, desc) {
		t.Errorf("hover value missing description %q; got prefix %q", desc, truncForLog(result.Contents.Value, 120))
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Errorf("hover kind = %q, want %q (hostname has rich data)", result.Contents.Kind, protocol.Markdown)
	}
}

func TestHoverInterface(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: !
	//       line 1: interface GigabitEthernet0/0
	//       line 2: !
	content := []byte("!\ninterface GigabitEthernet0/0\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover on "interface" — line 1, character 0 (zero-based)
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	desc := "To configure an interface type and to enter interface configuration mode, use the interface command in the appropriate configuration mode."
	if !strings.Contains(result.Contents.Value, desc) {
		t.Errorf("hover value missing description %q; got prefix %q", desc, truncForLog(result.Contents.Value, 120))
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Errorf("hover kind = %q, want %q (interface has rich data)", result.Contents.Kind, protocol.Markdown)
	}
}

// TestHoverRichMarkdownHostname verifies the rich hover for a command that
// carries the full scraped dataset: the Markdown must include every section
// label and the description as the leading text (chunt-97u).
func TestHoverRichMarkdownHostname(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	content := []byte("!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Fatalf("hover kind = %q, want %q", result.Contents.Kind, protocol.Markdown)
	}
	for _, want := range []string{
		"**Usage Guidelines**",
		"**Examples**",
		"**Defaults**",
		"**Command History**",
		"Introduced in release 12.2",
	} {
		if !strings.Contains(result.Contents.Value, want) {
			t.Errorf("hover Markdown missing %q; got prefix %q", want, truncForLog(result.Contents.Value, 200))
		}
	}
	// Description must lead the document.
	if !strings.HasPrefix(result.Contents.Value, "To specify or modify the hostname") {
		t.Errorf("description is not the leading text; got prefix %q", truncForLog(result.Contents.Value, 80))
	}
}

// truncForLog returns the first n bytes of s (or all of it if shorter), for
// readable test-failure output without dumping a multi-thousand-char docstring.
func truncForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
