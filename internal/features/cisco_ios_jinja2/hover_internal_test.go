package cisco_ios_jinja2

import (
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/keyword"
	"github.com/dgethings/chunt/internal/protocol"
)

// TestBuildHoverContent_PlainTextFallback verifies that a keyword carrying only
// a description produces the unchanged PlainText hover (chunt-97u).
func TestBuildHoverContent_PlainTextFallback(t *testing.T) {
	kw := keyword.Keyword{
		Keyword: "sparse-cmd",
		Description: keyword.Description{
			Format: protocol.Markdown, // intentionally ignored: content decides
			Value:  "A command with no scraped extras.",
		},
	}
	got := buildHoverContent(kw)
	if got.Kind != protocol.PlainText {
		t.Errorf("kind = %q, want %q", got.Kind, protocol.PlainText)
	}
	if got.Value != kw.Description.Value {
		t.Errorf("value = %q, want the description verbatim %q", got.Value, kw.Description.Value)
	}
}

// TestBuildHoverContent_RichMarkdown verifies section construction, ordering,
// empty-section omission, and the note blockquote / code fence handling.
func TestBuildHoverContent_RichMarkdown(t *testing.T) {
	kw := keyword.Keyword{
		Keyword: "rich-cmd",
		Description: keyword.Description{
			Format: protocol.PlainText,
			Value:  "Leading description.",
		},
		Defaults:   "Disabled by default.",
		MinVersion: "15.0",
		MaxVersion: "",
		History: keyword.CommandHistory{
			Release:      "12.4",
			Modification: "Added the foo keyword.",
		},
		Usage: keyword.UsageGuideline{
			Preamble: "Use this in interface mode.",
			Note:     "Mind the gap.",
		},
		Examples: keyword.Examples{
			Preamble: "Example below:",
			Code:     "Router(config)# rich-cmd\nRouter(config)#",
		},
		DeviceTypes: []string{"router"}, // must NOT promote to a section on its own
	}
	got := buildHoverContent(kw)
	if got.Kind != protocol.Markdown {
		t.Fatalf("kind = %q, want %q", got.Kind, protocol.Markdown)
	}

	// Description leads.
	if !strings.HasPrefix(got.Value, "Leading description.") {
		t.Errorf("description must lead; got %q", trunc(got.Value, 60))
	}
	// Every populated section header is present, in order.
	wantSeq := []string{"**Usage Guidelines**", "**Examples**", "**Defaults**", "**Command History**"}
	idx := 0
	for _, want := range wantSeq {
		j := strings.Index(got.Value[idx:], want)
		if j < 0 {
			t.Errorf("missing or out-of-order section %q in: %q", want, trunc(got.Value, 400))
			continue
		}
		idx += j
	}
	// Usage note rendered as a blockquote.
	if !strings.Contains(got.Value, "> Mind the gap.") {
		t.Errorf("usage note not rendered as blockquote; got %q", trunc(got.Value, 400))
	}
	// Example code in a fenced block.
	if !strings.Contains(got.Value, "```\nRouter(config)# rich-cmd") {
		t.Errorf("example code not fenced; got %q", trunc(got.Value, 400))
	}
	// History release + modification, and the introduced version line.
	if !strings.Contains(got.Value, "Release 12.4: Added the foo keyword.") {
		t.Errorf("history release/modification line missing; got %q", trunc(got.Value, 400))
	}
	if !strings.Contains(got.Value, "Introduced in release 15.0") {
		t.Errorf("introduced-in line missing; got %q", trunc(got.Value, 400))
	}
	// MaxVersion empty -> no "Removed after" line.
	if strings.Contains(got.Value, "Removed after release") {
		t.Errorf("empty MaxVersion should omit the Removed-after line; got %q", trunc(got.Value, 400))
	}
}

// TestBuildHoverContent_FenceEscaping verifies that a code block containing
// backticks gets a longer fence so it cannot terminate early (chunt-97u).
func TestBuildHoverContent_FenceEscaping(t *testing.T) {
	kw := keyword.Keyword{
		Description: keyword.Description{Value: "d"},
		Examples: keyword.Examples{
			Code: "```nested```",
		},
	}
	got := buildHoverContent(kw)
	// The fence must be 4+ backticks (longer than the 3-run inside).
	if !strings.Contains(got.Value, "````\n```nested```\n````") {
		t.Errorf("backtick-bearing code not fenced safely; got %q", trunc(got.Value, 200))
	}
}

// TestFenceFor covers the fence-length selection directly.
func TestFenceFor(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"no backticks", "```"},
		{"one ` here", "```"},
		{"``` triple", "````"},
		{"```` quad", "`````"},
	}
	for _, tc := range cases {
		if got := fenceFor(tc.code); got != tc.want {
			t.Errorf("fenceFor(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
