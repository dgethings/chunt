package cisco_ios_jinja2_test

// Golden-file integration harness (chunt-0uz).
//
// This file adds an integration test tier ON TOP of the inline unit tests in
// the other *_test.go files. It feeds representative, realistic show-run-style
// configs (plus Jinja2 constructs) through the full feature pipeline
// (parse → symbols.Index → runDiagnostics, and Completion/Hover/Definition/
// References/DocumentSymbol at marked cursor positions) and compares the result
// against checked-in .golden files.
// A deliberate regression therefore surfaces as a readable diff in the test
// output, and `go test -update` regenerates the goldens so PR diffs expose
// behavioral changes.
//
// Fixtures live one-per-directory under testdata/golden/:
//
//	<name>.cfg      the input config (raw; NO inline markers, so line/column
//	                offsets are exactly what a real editor sends)
//	<name>.marks    OPTIONAL cursor positions, one per line (see parseMarks)
//	<name>.golden   the expected pipeline output (generated; do not hand-edit)
//
// Each fixture targets one diagnostic pass to keep failures localized (see the
// chunt-0uz design notes). See testdata/golden/README.md for how to add one.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
)

// updateGoldens, when set via `go test -update`, rewrites every .golden file
// instead of comparing against it. Commit the regenerated files so a PR diff
// surfaces any behavioral change.
var updateGoldens = flag.Bool("update", false, "regenerate golden files under testdata/golden")

// goldenDir is relative to the package directory (the test's working dir).
const goldenDir = "testdata/golden"

func TestGolden(t *testing.T) {
	cfgs, err := filepath.Glob(filepath.Join(goldenDir, "*.cfg"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(cfgs) == 0 {
		t.Fatalf("no *.cfg fixtures found in %s", goldenDir)
	}
	sort.Strings(cfgs)
	for _, cfgPath := range cfgs {
		cfgPath := cfgPath
		name := strings.TrimSuffix(filepath.Base(cfgPath), ".cfg")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runGolden(t, name)
		})
	}
}

// runGolden loads the <name> fixture, runs the full pipeline, and either
// regenerates <name>.golden (-update) or compares against it.
func runGolden(t *testing.T, name string) {
	t.Helper()
	cfgPath := filepath.Join(goldenDir, name+".cfg")
	src, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}

	f := cisco_ios_jinja2.New()
	defer f.Close()
	doc := document.New("file:///"+name+".cfg", "cisco_ios_jinja2", 1, src)
	diags, err := f.DidOpen(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("DidOpen(%s): %v", name, err)
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "# golden for %s.cfg — DO NOT HAND-EDIT; regenerate with:\n", name)
	fmt.Fprintf(&out, "#   go test ./internal/features/cisco_ios_jinja2/ -run '^TestGolden/%s$' -update\n", name)
	out.WriteString("\n## diagnostics\n")
	out.WriteString(formatDiags(diags))

	for _, m := range readMarks(t, name) {
		pos := protocol.Position{Line: m.line, Character: m.col}
		switch m.feature {
		case "completion":
			items, err := f.Completion(context.Background(), doc, pos)
			if err != nil {
				t.Fatalf("%s: completion @ %d:%d: %v", name, m.line, m.col, err)
			}
			fmt.Fprintf(&out, "\n## completion @ %d:%d%s\n", m.line, m.col, markNote(m.note))
			out.WriteString(formatCompletion(items))
		case "hover":
			hv, err := f.Hover(context.Background(), doc, pos)
			if err != nil {
				t.Fatalf("%s: hover @ %d:%d: %v", name, m.line, m.col, err)
			}
			fmt.Fprintf(&out, "\n## hover @ %d:%d%s\n", m.line, m.col, markNote(m.note))
			out.WriteString(formatHover(hv))
		case "definition":
			locs, err := f.Definition(context.Background(), doc, pos)
			if err != nil {
				t.Fatalf("%s: definition @ %d:%d: %v", name, m.line, m.col, err)
			}
			fmt.Fprintf(&out, "\n## definition @ %d:%d%s\n", m.line, m.col, markNote(m.note))
			out.WriteString(formatLocations(locs))
		case "references":
			locs, err := f.References(context.Background(), doc, pos, false)
			if err != nil {
				t.Fatalf("%s: references @ %d:%d: %v", name, m.line, m.col, err)
			}
			fmt.Fprintf(&out, "\n## references @ %d:%d%s\n", m.line, m.col, markNote(m.note))
			out.WriteString(formatLocations(locs))
		case "references-decl":
			// References with includeDeclaration=true: every usage plus the
			// matching definition site(s).
			locs, err := f.References(context.Background(), doc, pos, true)
			if err != nil {
				t.Fatalf("%s: references-decl @ %d:%d: %v", name, m.line, m.col, err)
			}
			fmt.Fprintf(&out, "\n## references-decl @ %d:%d%s\n", m.line, m.col, markNote(m.note))
			out.WriteString(formatLocations(locs))
		case "documentSymbol":
			// The cursor is irrelevant for documentSymbol; the `0:0` position
			// in the mark line is just the format's required placeholder, so
			// the golden header deliberately omits it.
			syms, err := f.DocumentSymbol(context.Background(), doc)
			if err != nil {
				t.Fatalf("%s: documentSymbol: %v", name, err)
			}
			fmt.Fprintf(&out, "\n## documentSymbol%s\n", markNote(m.note))
			out.WriteString(formatDocumentSymbols(syms))
		default:
			t.Fatalf("%s: unknown mark feature %q (want \"completion\", \"hover\", \"definition\", \"references\", \"references-decl\", or \"documentSymbol\")", name, m.feature)
		}
	}

	got := out.Bytes()
	goldenPath := filepath.Join(goldenDir, name+".golden")
	if *updateGoldens {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", goldenPath, err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%s: golden file missing (%v); regenerate with: go test ./internal/features/cisco_ios_jinja2/ -run '^TestGolden/%s$' -update",
			name, err, name)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("%s: golden mismatch\n%s", name, diffLines(string(want), string(got)))
	}
}

// mark is a cursor position at which a cursor-sensitive feature is exercised.
type mark struct {
	line    uint
	col     uint
	feature string // "completion", "hover", "definition", "references", "references-decl", "documentSymbol"
	note    string // optional human annotation (not compared, just echoed)
}

// readMarks parses <name>.marks if it exists. Lines:
//
//	# <line>:<col> <feature> <optional note>     (leading '# ' marks it as a comment header)
//	<line>:<col> <feature>
//
// Blank lines and lines whose first token starts with '#' are ignored. The
// position is `0:0` for documentSymbol (the only document-wide feature; the
// cursor is ignored). Tokens after the feature are treated as a free-form note
// echoed into the golden header (handy for explaining WHY a position is
// interesting).
func readMarks(t *testing.T, name string) []mark {
	t.Helper()
	marksPath := filepath.Join(goldenDir, name+".marks")
	data, err := os.ReadFile(marksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", marksPath, err)
	}
	var out []mark
	for ln, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("%s:%d: expected `<line>:<col> <feature>`, got %q", marksPath, ln+1, raw)
		}
		lc := strings.SplitN(fields[0], ":", 2)
		if len(lc) != 2 {
			t.Fatalf("%s:%d: expected `<line>:<col>`, got %q", marksPath, ln+1, fields[0])
		}
		m := mark{feature: fields[1]}
		if _, err := fmt.Sscanf(lc[0], "%d", &m.line); err != nil {
			t.Fatalf("%s:%d: bad line %q: %v", marksPath, ln+1, lc[0], err)
		}
		if _, err := fmt.Sscanf(lc[1], "%d", &m.col); err != nil {
			t.Fatalf("%s:%d: bad col %q: %v", marksPath, ln+1, lc[1], err)
		}
		if len(fields) > 2 {
			m.note = strings.Join(fields[2:], " ")
		}
		out = append(out, m)
	}
	return out
}

func markNote(note string) string {
	if note == "" {
		return ""
	}
	return "  " + note
}

// ---------------------------------------------------------------------------
// Output formatters — deterministic, human-readable, line-oriented.
// ---------------------------------------------------------------------------

// formatDiags renders diagnostics sorted by (line, column, code, severity) so
// the golden is stable regardless of the pass ordering inside runDiagnostics
// (the LSP client renders them sorted by location anyway). Empty input renders
// an explicit "<none>" so a clean fixture still has a visible, diffable marker.
func formatDiags(diags []protocol.Diagnostic) string {
	if len(diags) == 0 {
		return "<none>\n"
	}
	sorted := append([]protocol.Diagnostic(nil), diags...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Range.Start, sorted[j].Range.Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Character != b.Character {
			return a.Character < b.Character
		}
		if sorted[i].Code != sorted[j].Code {
			return sorted[i].Code < sorted[j].Code
		}
		return sorted[i].Severity < sorted[j].Severity
	})
	var sb strings.Builder
	for _, d := range sorted {
		fmt.Fprintf(&sb, "%-8s %-22s %s | %s", severityName(d.Severity), codeOr(d.Code, "<no-code>"), formatRange(d.Range), d.Message)
		for _, ri := range d.RelatedInformation {
			fmt.Fprintf(&sb, "  ~> %s %s %q", shortURI(ri.Location.URI), formatRange(ri.Location.Range), ri.Message)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// formatCompletion renders a compact fingerprint of the completion list. The
// keyword database is large (a single section yields thousands of items with
// long labels), so dumping every label verbatim would produce enormous, noisy
// goldens that churn on every keyword-DB edit. Instead we record the item count
// plus a sha256 of the sorted unique label set: a wiring regression (e.g. a
// cursor resolving to the wrong section) changes both the count and the
// fingerprint; a keyword-DB content change changes the fingerprint. Regenerate
// with -update and read the git diff to see what moved.
func formatCompletion(items []protocol.CompletionItem) string {
	labels := make([]string, 0, len(items))
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	sort.Strings(labels)
	h := sha256.Sum256([]byte(strings.Join(labels, "\n")))
	return fmt.Sprintf("%d items  sha256:%s\n", len(items), hex.EncodeToString(h[:8]))
}

// formatHover renders the hover result. nil renders "<none>".
//
// The value is bounded to the first hoverPreviewBytes: a sparse command's
// PlainText description is short and shown verbatim, but a rich Markdown
// hover (chunt-97u) can be several thousand bytes (interface's Usage
// Guidelines alone is ~35KB). Truncating keeps the golden diffable and focused
// on the leading structure (kind + description + first section header) while
// the full per-section rendering is covered by the inline buildHoverContent
// unit tests. A trailing marker records the truncation.
const hoverPreviewBytes = 800

func formatHover(hv *protocol.HoverResult) string {
	if hv == nil {
		return "<none>\n"
	}
	v := hv.Contents.Value
	if len(v) > hoverPreviewBytes {
		v = v[:hoverPreviewBytes] + fmt.Sprintf("\n… [truncated, %d bytes total]", len(hv.Contents.Value))
	}
	return fmt.Sprintf("kind=%s\n%s\n", hv.Contents.Kind, v)
}

// formatLocations renders Definition/References results: one `<uri> <range>`
// per line, sorted by (uri, range start, range end) so the golden is stable
// regardless of the order the symbol table yields (References appends the
// declaration after the reference sites; sorting normalizes that). Empty input
// renders an explicit "<none>" — the interesting case for an undefined name.
func formatLocations(locs []protocol.Location) string {
	if len(locs) == 0 {
		return "<none>\n"
	}
	sorted := append([]protocol.Location(nil), locs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].URI != sorted[j].URI {
			return sorted[i].URI < sorted[j].URI
		}
		a, b := sorted[i].Range.Start, sorted[j].Range.Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Character != b.Character {
			return a.Character < b.Character
		}
		ae, be := sorted[i].Range.End, sorted[j].Range.End
		if ae.Line != be.Line {
			return ae.Line < be.Line
		}
		return ae.Character < be.Character
	})
	var sb strings.Builder
	for _, l := range sorted {
		fmt.Fprintf(&sb, "%s %s\n", shortURI(l.URI), formatRange(l.Range))
	}
	return sb.String()
}

// formatDocumentSymbols renders the document outline as an indented tree, one
// symbol per line:
//
//	<indent>kind=<num>/<Name> <name> [detail=<detail>] <range> sel=<selectionRange>
//
// Siblings stay in document order (the order DocumentSymbol returns; the
// outline semantic), and children — empty today but rendered recursively so
// nested outlines (e.g. class blocks under a policy-map) golden-test cleanly
// when they arrive. Empty input renders an explicit "<none>".
func formatDocumentSymbols(syms []protocol.DocumentSymbol) string {
	if len(syms) == 0 {
		return "<none>\n"
	}
	var sb strings.Builder
	writeSymbolTree(&sb, syms, 0)
	return sb.String()
}

func writeSymbolTree(sb *strings.Builder, syms []protocol.DocumentSymbol, depth int) {
	for _, s := range syms {
		fmt.Fprintf(sb, "%skind=%d/%s %s", strings.Repeat("  ", depth), s.Kind, symbolKindName(s.Kind), s.Name)
		if s.Detail != "" {
			fmt.Fprintf(sb, " detail=%q", s.Detail)
		}
		fmt.Fprintf(sb, " %s sel=%s\n", formatRange(s.Range), formatRange(s.SelectionRange))
		writeSymbolTree(sb, s.Children, depth+1)
	}
}

// symbolKindName maps an LSP SymbolKind number to its spec name (mirroring the
// constants in internal/protocol/document_symbol.go) so the golden reads
// `kind=11/Interface` instead of a bare number. Unknown kinds fall back to a
// stable numeric label rather than erroring — the number is still compared.
func symbolKindName(kind int) string {
	switch kind {
	case protocol.SymbolKindNamespace:
		return "Namespace"
	case protocol.SymbolKindClass:
		return "Class"
	case protocol.SymbolKindMethod:
		return "Method"
	case protocol.SymbolKindField:
		return "Field"
	case protocol.SymbolKindInterface:
		return "Interface"
	case protocol.SymbolKindFunction:
		return "Function"
	case protocol.SymbolKindVariable:
		return "Variable"
	case protocol.SymbolKindConstant:
		return "Constant"
	case protocol.SymbolKindNumber:
		return "Number"
	}
	return fmt.Sprintf("Kind%d", kind)
}

// formatRange renders an LSP range compactly: "L:C-C" for a single-line span,
// "L1:C1-L2:C2" for a multi-line span. Matches the convention used elsewhere in
// the golden output.
func formatRange(r protocol.Range) string {
	if r.Start.Line == r.End.Line {
		return fmt.Sprintf("%d:%d-%d", r.Start.Line, r.Start.Character, r.End.Character)
	}
	return fmt.Sprintf("%d:%d-%d:%d", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}

func severityName(s int) string {
	switch s {
	case protocol.SeverityError:
		return "ERROR"
	case protocol.SeverityWarning:
		return "WARNING"
	case protocol.SeverityInformation:
		return "INFO"
	case protocol.SeverityHint:
		return "HINT"
	}
	return fmt.Sprintf("SEV%d", s)
}

func codeOr(code, fallback string) string {
	if code == "" {
		return fallback
	}
	return code
}

// shortURI strips the synthetic "file:///" scheme the harness synthesizes,
// keeping the golden readable: file:///duplicates.cfg -> duplicates.cfg.
func shortURI(uri string) string {
	const prefix = "file:///"
	if strings.HasPrefix(uri, prefix) {
		return strings.TrimPrefix(uri, prefix)
	}
	return uri
}

// ---------------------------------------------------------------------------
// Minimal line diff (LCS-based) — dependency-free.
// ---------------------------------------------------------------------------

// diffLines returns a unified-style diff of want vs got, emitting only the
// changed lines prefixed with '-' / '+' (common context is omitted to keep the
// failure output short and focused on what changed). A header names the sides.
func diffLines(want, got string) string {
	a := splitLines(want)
	b := splitLines(got)
	la, lb := len(a), len(b)

	// dp[i][j] = length of the LCS of a[i:] and b[j:].
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
	}
	for i := la - 1; i >= 0; i-- {
		for j := lb - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("--- want (.golden)\n+++ got (actual)\n")
	i, j := 0, 0
	for i < la && j < lb {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			fmt.Fprintf(&sb, "- %s\n", a[i])
			i++
		default:
			fmt.Fprintf(&sb, "+ %s\n", b[j])
			j++
		}
	}
	for ; i < la; i++ {
		fmt.Fprintf(&sb, "- %s\n", a[i])
	}
	for ; j < lb; j++ {
		fmt.Fprintf(&sb, "+ %s\n", b[j])
	}
	return sb.String()
}

// splitLines splits s on newlines, dropping a trailing empty element that a
// final newline would produce so line-based comparison is on logical lines.
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
