package cisco_ios_jinja2

import (
	"context"
	"strings"

	"github.com/dgethings/chunt/internal/ast"
	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/keyword"
	"github.com/dgethings/chunt/internal/protocol"
)

func (f *CiscoIOSFeature) Hover(ctx context.Context, doc *document.Document, pos protocol.Position) (*protocol.HoverResult, error) {
	tree := f.trees[doc.URI]
	if tree == nil {
		return nil, nil
	}
	node := ast.FindNodeAtPosition(tree.RootNode(), pos.Line, pos.Character)
	if node == nil {
		return nil, nil
	}
	name := node.Kind()
	if name == "identifier" {
		name = string(doc.Content[node.StartByte():node.EndByte()])
	}
	// When the cursor sits exactly at the end of a keyword token,
	// FindNodeAtPosition resolves to the enclosing *_statement / *_header
	// node rather than the anonymous keyword leaf. Recover the keyword text
	// from its first anonymous leaf child so the lookup still matches.
	if strings.HasSuffix(name, "_statement") || strings.HasSuffix(name, "_header") {
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child == nil {
				continue
			}
			if !child.IsNamed() && child.ChildCount() == 0 {
				name = string(doc.Content[child.StartByte():child.EndByte()])
				break
			}
		}
	}
	kw, ok := f.keyword.Lookup(name)
	if !ok {
		return nil, nil
	}
	return &protocol.HoverResult{
		Contents: buildHoverContent(kw),
	}, nil
}

// buildHoverContent composes the hover MarkupContent for a keyword. When the
// keyword carries only a description (no Usage/Examples/Defaults/History or
// version data), it returns the description verbatim as PlainText so behavior
// for sparse commands is unchanged. When any extra section is present it
// returns a sectioned Markdown document, omitting empty sections so a sparse
// command never shows bare headers (chunt-97u).
func buildHoverContent(kw keyword.Keyword) protocol.MarkupContent {
	if !hasHoverExtras(kw) {
		return protocol.MarkupContent{
			Kind:  protocol.PlainText,
			Value: kw.Description.Value,
		}
	}

	var b strings.Builder
	if kw.Description.Value != "" {
		b.WriteString(kw.Description.Value)
		b.WriteString("\n\n")
	}

	if kw.Usage.Preamble != "" || kw.Usage.Note != "" {
		b.WriteString("**Usage Guidelines**\n\n")
		if kw.Usage.Preamble != "" {
			b.WriteString(kw.Usage.Preamble)
			b.WriteString("\n\n")
		}
		if kw.Usage.Note != "" {
			b.WriteString(blockquote(kw.Usage.Note))
			b.WriteString("\n\n")
		}
	}

	if kw.Examples.Preamble != "" || kw.Examples.Code != "" {
		b.WriteString("**Examples**\n\n")
		if kw.Examples.Preamble != "" {
			b.WriteString(kw.Examples.Preamble)
			b.WriteString("\n\n")
		}
		if kw.Examples.Code != "" {
			fence := fenceFor(kw.Examples.Code)
			b.WriteString(fence)
			b.WriteByte('\n')
			b.WriteString(strings.TrimRight(kw.Examples.Code, "\n"))
			b.WriteByte('\n')
			b.WriteString(fence)
			b.WriteString("\n\n")
		}
	}

	if kw.Defaults != "" {
		b.WriteString("**Defaults**\n\n")
		b.WriteString(kw.Defaults)
		b.WriteString("\n\n")
	}

	if kw.History.Release != "" || kw.History.Modification != "" ||
		kw.MinVersion != "" || kw.MaxVersion != "" {
		b.WriteString("**Command History**\n\n")
		if kw.History.Release != "" || kw.History.Modification != "" {
			b.WriteString("- ")
			if kw.History.Release != "" {
				b.WriteString("Release ")
				b.WriteString(kw.History.Release)
				if kw.History.Modification != "" {
					b.WriteString(": ")
				}
			}
			if kw.History.Modification != "" {
				b.WriteString(kw.History.Modification)
			}
			b.WriteByte('\n')
		}
		if kw.MinVersion != "" {
			b.WriteString("- Introduced in release ")
			b.WriteString(kw.MinVersion)
			b.WriteByte('\n')
		}
		if kw.MaxVersion != "" {
			b.WriteString("- Removed after release ")
			b.WriteString(kw.MaxVersion)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return protocol.MarkupContent{
		Kind:  protocol.Markdown,
		Value: strings.TrimRight(b.String(), "\n"),
	}
}

// hasHoverExtras reports whether kw carries any field beyond its description
// that produces a visible hover section. DeviceTypes is intentionally not
// counted: it is platform metadata, not documentation prose, and alone should
// not promote a sparse command to Markdown.
func hasHoverExtras(kw keyword.Keyword) bool {
	return kw.Usage.Preamble != "" || kw.Usage.Note != "" ||
		kw.Examples.Preamble != "" || kw.Examples.Code != "" ||
		kw.Defaults != "" ||
		kw.History.Release != "" || kw.History.Modification != "" ||
		kw.MinVersion != "" || kw.MaxVersion != ""
}

// fenceFor returns a Markdown code fence (a run of backticks) long enough to
// safely wrap code: it is one backtick longer than the longest backtick run
// inside code (min 3), so a code block containing ``` cannot terminate the
// fence early (chunt-97u).
func fenceFor(code string) string {
	maxRun, run := 0, 0
	for i := 0; i < len(code); i++ {
		if code[i] == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	n := maxRun + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// blockquote prefixes every line of s with "> " so the rendered Markdown is a
// single blockquote rather than just the first line. Trailing newlines are
// trimmed so the caller controls spacing.
func blockquote(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n")
}
