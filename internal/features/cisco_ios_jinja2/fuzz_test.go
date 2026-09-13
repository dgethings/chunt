package cisco_ios_jinja2_test

// Fuzz targets for the parse → index → diagnostics pipeline (chunt-933).
//
// chunt's front end is a tree-sitter (CGO) grammar plus a stack of custom
// heuristics, and it runs inside a long-lived LSP server: a panic anywhere in
// the pipeline — parse, symbols.Index, the diagnostic passes, or the cursor
// features (Completion/Hover/Definition/References) — takes down the editor
// session for the user. These targets feed arbitrary input through the same
// entry points the server exposes, so a panic fails the test and Go's fuzzing
// engine minimizes the crashing input automatically.
//
// Targets:
//
//	FuzzDiagnostics     DidOpen (parse + symbols.Index + every diagnostic
//	                    pass), then DocumentSymbol plus a couple of sampled
//	                    Completion/Hover pokes on the parsed tree.
//	FuzzCursorFeatures  Completion/Hover/Definition/References at a bounded
//	                    sample of cursor positions (column 0 / midpoint /
//	                    end-of-line on a strided subset of lines — never a full
//	                    O(lines×cols) sweep).
//
// Beyond "must not panic", each target checks the invariant LSP clients rely
// on: every diagnostic, symbol, hover, and location range a feature returns
// lies within the document — a valid line index, a column no greater than the
// line's byte length, and start <= end. Columns are byte offsets, matching
// chunt's internal convention (tree-sitter points are byte-based; see
// lineUpToCharacter in completion.go).
//
// Runtime: a plain `go test` (what `make test` runs) executes only the seed
// corpus — every testdata/golden/*.cfg fixture plus adversarial one-liners —
// so suite runtime is unaffected. Real fuzzing is on demand:
//
//	CGO_ENABLED=1 go test ./internal/features/cisco_ios_jinja2/ \
//	  -run '^$' -fuzz FuzzDiagnostics -fuzztime 60s
//	CGO_ENABLED=1 go test ./internal/features/cisco_ios_jinja2/ \
//	  -run '^$' -fuzz FuzzCursorFeatures -fuzztime 60s
//
// If fuzzing finds a crasher, the engine writes the (minimized) failing input
// under testdata/fuzz/<FuzzName>/ — commit it so it becomes a permanent
// regression seed, and fix the bug it exposed.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
)

// fuzzURI is the synthetic document URI shared by both targets. Each fuzz
// iteration builds a fresh feature (see the target bodies), so reusing one URI
// cannot alias parse trees or symbol state across inputs.
const fuzzURI = "file:///fuzz.ios.j2"

// adversarialSeeds are hand-written inputs chosen to stress the heuristics the
// golden fixtures' realistic configs do not: empty/truncated documents, bare
// and unclosed section headers, broken Jinja2, NUL bytes, multibyte runes
// (byte/character column skew), CRLF line endings, and duplicate definitions.
var adversarialSeeds = []string{
	"",
	"\n",
	"!",
	"interface",
	"interface\n",
	"router bgp",
	"no no no",
	"ip access-list extended",
	"{% if %}",
	"{% for x in %}",
	"{{ }}",
	"{{",
	"{% raw %}",
	"hostname \x00\n",
	"hostname rüttor\ninterface GüigabitEthernet0/0\n ip address 192.0.2.1 255.255.255.0\n",
	"\r\n\r\nhostname r1\r\ninterface Loopback0\r\n",
	"vlan 1\nvlan 1\nvlan 1\n",
	"router bgp 65000\n neighbor 10.0.0.1 remote-as 65001\n address-family ipv4\n  neighbor 10.0.0.1 activate\n",
	"route-map FOO permit 10\n match ip address FOO\n",
}

// fuzzSeeds returns the seed corpus: every golden fixture (each targets a
// different diagnostic pass — see testdata/golden/README.md) followed by the
// adversarial one-liners. Called before f.Fuzz so a plain `go test` run
// executes every seed through the fuzz body as ordinary subtests.
func fuzzSeeds(tb testing.TB) []string {
	tb.Helper()
	cfgs, err := filepath.Glob(filepath.Join("testdata", "golden", "*.cfg"))
	if err != nil {
		tb.Fatalf("glob golden fixtures: %v", err)
	}
	if len(cfgs) == 0 {
		tb.Fatalf("no *.cfg fixtures found in testdata/golden")
	}
	sort.Strings(cfgs)
	seeds := make([]string, 0, len(cfgs)+len(adversarialSeeds))
	for _, cfg := range cfgs {
		b, err := os.ReadFile(cfg)
		if err != nil {
			tb.Fatalf("read %s: %v", cfg, err)
		}
		seeds = append(seeds, string(b))
	}
	return append(seeds, adversarialSeeds...)
}

// fuzzDoc wraps a fuzz input as the document the LSP server would hand the
// feature.
func fuzzDoc(src string) *document.Document {
	return document.New(fuzzURI, "cisco_ios_jinja2", 1, []byte(src))
}

// splitFuzzLines splits src into lines the way tree-sitter counts them: on
// '\n' only. strings.Split keeps the phantom trailing empty line when src
// ends in a newline, and column 0 of that line is a real, valid position.
func splitFuzzLines(src string) []string {
	return strings.Split(src, "\n")
}

// ---------------------------------------------------------------------------
// Bounds assertions — the LSP-client-facing invariant.
// ---------------------------------------------------------------------------

// assertPosInBounds checks that p refers to a real location in the document:
// a line that exists, and a byte column no greater than that line's length
// (end-of-line is valid; one past it is not).
func assertPosInBounds(t *testing.T, lines []string, p protocol.Position, what string) {
	t.Helper()
	if p.Line >= uint(len(lines)) {
		t.Errorf("%s: line %d out of bounds (document has %d lines)", what, p.Line, len(lines))
		return
	}
	if p.Character > uint(len(lines[p.Line])) {
		t.Errorf("%s: line %d column %d out of bounds (line is %d bytes)", what, p.Line, p.Character, len(lines[p.Line]))
	}
}

// assertRangeInBounds checks that r lies inside the document and is ordered
// (start <= end). LSP clients render these offsets verbatim; an inverted or
// out-of-bounds range corrupts the editor view.
func assertRangeInBounds(t *testing.T, lines []string, r protocol.Range, what string) {
	t.Helper()
	assertPosInBounds(t, lines, r.Start, what+" start")
	assertPosInBounds(t, lines, r.End, what+" end")
	if r.Start.Line > r.End.Line || (r.Start.Line == r.End.Line && r.Start.Character > r.End.Character) {
		t.Errorf("%s: inverted range %d:%d-%d:%d", what, r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
	}
}

// assertSymbolTreeInBounds bounds-checks a document symbol and, defensively,
// its Children: chunt returns a flat outline today, but DocumentSymbol nests
// by design, and the fuzz guard should hold the same invariant the day the
// outline starts using nesting.
func assertSymbolTreeInBounds(t *testing.T, lines []string, s protocol.DocumentSymbol) {
	t.Helper()
	assertRangeInBounds(t, lines, s.Range, "document symbol")
	assertRangeInBounds(t, lines, s.SelectionRange, "document symbol selection")
	for _, c := range s.Children {
		assertSymbolTreeInBounds(t, lines, c)
	}
}

// assertTextEditInBounds bounds-checks a completion item's TextEdit. The field
// is typed any (nil | TextEdit | InsertReplaceEdit per LSP); chunt does not
// currently populate it, so this is defensive — if it ever starts being set,
// its range is held to the same invariant from day one.
func assertTextEditInBounds(t *testing.T, lines []string, edit any, what string) {
	t.Helper()
	switch e := edit.(type) {
	case nil:
	case protocol.TextEdit:
		assertRangeInBounds(t, lines, e.Range, what)
	default:
		t.Errorf("%s: unsupported TextEdit payload type %T", what, edit)
	}
}

// ---------------------------------------------------------------------------
// Cursor sampling — bounded, deterministic.
// ---------------------------------------------------------------------------

// maxFuzzLines caps how many lines FuzzCursorFeatures visits per input, so a
// fuzzed megabyte of newlines still costs O(maxFuzzLines) feature calls.
const maxFuzzLines = 96

// sampledLineIdxs picks at most maxFuzzLines line indices to visit, always
// including the first and last lines.
func sampledLineIdxs(n int) []int {
	if n <= maxFuzzLines {
		idxs := make([]int, n)
		for i := range idxs {
			idxs[i] = i
		}
		return idxs
	}
	var idxs []int
	for i := 0; i < n-1; i += n / maxFuzzLines {
		idxs = append(idxs, i)
	}
	return append(idxs, n-1)
}

// sampledCols returns the cursor columns probed on one line: 0, the byte
// midpoint (deliberately able to land mid-rune — a great stressor for
// byte-column position math), and end-of-line. Deduplicated in order.
func sampledCols(line string) []uint {
	candidates := []uint{0, uint(len(line) / 2), uint(len(line))}
	var cols []uint
	seen := map[uint]bool{}
	for _, c := range candidates {
		if !seen[c] {
			seen[c] = true
			cols = append(cols, c)
		}
	}
	return cols
}

// fuzzPokes returns the handful of cursor positions FuzzDiagnostics uses for
// its Completion/Hover sample: start/middle of the first line and start/end
// of the last.
func fuzzPokes(lines []string) []protocol.Position {
	return []protocol.Position{
		{Line: 0, Character: 0},
		{Line: 0, Character: uint(len(lines[0]) / 2)},
		{Line: uint(len(lines) - 1), Character: 0},
		{Line: uint(len(lines) - 1), Character: uint(len(lines[len(lines)-1]))},
	}
}

// ---------------------------------------------------------------------------
// Targets.
// ---------------------------------------------------------------------------

// FuzzDiagnostics feeds arbitrary input through DidOpen — parse, symbols
// Index, and every diagnostic pass — and asserts the returned diagnostics,
// document symbols, and completion/hover results stay within document bounds.
func FuzzDiagnostics(f *testing.F) {
	for _, seed := range fuzzSeeds(f) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		feat := cisco_ios_jinja2.New()
		defer feat.Close()
		doc := fuzzDoc(src)
		ctx := context.Background()

		diags, err := feat.DidOpen(ctx, doc, nil)
		if err != nil {
			t.Fatalf("DidOpen: %v", err)
		}
		lines := splitFuzzLines(src)
		for _, d := range diags {
			assertRangeInBounds(t, lines, d.Range, "diagnostic")
			for _, ri := range d.RelatedInformation {
				assertRangeInBounds(t, lines, ri.Location.Range, "diagnostic related info")
			}
		}

		// symbols.Index output surface: the document outline must stay in
		// bounds too.
		syms, err := feat.DocumentSymbol(ctx, doc)
		if err != nil {
			t.Fatalf("DocumentSymbol: %v", err)
		}
		for _, s := range syms {
			assertSymbolTreeInBounds(t, lines, s)
		}

		// Sampled cursor pokes so the parse-consuming features run against
		// the same tree; FuzzCursorFeatures does the systematic sweep.
		for _, pos := range fuzzPokes(lines) {
			items, err := feat.Completion(ctx, doc, pos)
			if err != nil {
				t.Fatalf("Completion @ %d:%d: %v", pos.Line, pos.Character, err)
			}
			for _, it := range items {
				assertTextEditInBounds(t, lines, it.TextEdit, "completion textEdit")
				for _, e := range it.AdditionalTextEdits {
					assertRangeInBounds(t, lines, e.Range, "completion additional edit")
				}
			}
			hv, err := feat.Hover(ctx, doc, pos)
			if err != nil {
				t.Fatalf("Hover @ %d:%d: %v", pos.Line, pos.Character, err)
			}
			if hv != nil && hv.Range != nil {
				assertRangeInBounds(t, lines, *hv.Range, "hover range")
			}
		}
	})
}

// FuzzCursorFeatures exercises Completion, Hover, Definition, and References
// at a bounded, deterministic sample of cursor positions over arbitrary
// input. Any panic, error, or out-of-bounds returned range fails the target.
func FuzzCursorFeatures(f *testing.F) {
	for _, seed := range fuzzSeeds(f) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		feat := cisco_ios_jinja2.New()
		defer feat.Close()
		doc := fuzzDoc(src)
		ctx := context.Background()

		if _, err := feat.DidOpen(ctx, doc, nil); err != nil {
			t.Fatalf("DidOpen: %v", err)
		}
		lines := splitFuzzLines(src)
		for _, ln := range sampledLineIdxs(len(lines)) {
			for ci, col := range sampledCols(lines[ln]) {
				pos := protocol.Position{Line: uint(ln), Character: col}

				items, err := feat.Completion(ctx, doc, pos)
				if err != nil {
					t.Fatalf("Completion @ %d:%d: %v", ln, col, err)
				}
				for _, it := range items {
					assertTextEditInBounds(t, lines, it.TextEdit, "completion textEdit")
					for _, e := range it.AdditionalTextEdits {
						assertRangeInBounds(t, lines, e.Range, "completion additional edit")
					}
				}

				hv, err := feat.Hover(ctx, doc, pos)
				if err != nil {
					t.Fatalf("Hover @ %d:%d: %v", ln, col, err)
				}
				if hv != nil && hv.Range != nil {
					assertRangeInBounds(t, lines, *hv.Range, "hover range")
				}

				defs, err := feat.Definition(ctx, doc, pos)
				if err != nil {
					t.Fatalf("Definition @ %d:%d: %v", ln, col, err)
				}
				for _, l := range defs {
					assertRangeInBounds(t, lines, l.Range, "definition location")
				}

				// Alternate includeDeclaration across the sampled columns so
				// both References code paths see fuzzed input.
				refs, err := feat.References(ctx, doc, pos, ci == 0)
				if err != nil {
					t.Fatalf("References @ %d:%d: %v", ln, col, err)
				}
				for _, l := range refs {
					assertRangeInBounds(t, lines, l.Range, "reference location")
				}
			}
		}
	})
}
