package cisco_ios_jinja2

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Measurement harness for chunt-cfz (progressive/streamed diagnostics).
//
// This is NOT part of the normal test suite: it is gated behind CHUNT_PERF=1
// so `make test-lsp` is unaffected. Run explicitly:
//
//	CHUNT_PERF=1 CGO_ENABLED=1 go test ./internal/features/cisco_ios_jinja2/ \
//	  -run '^TestPipelineTiming$' -v -count=1
//
// It instruments the current didOpen/didChange pipeline (parse -> symbols.Index
// -> every diagnostic pass) on a synthesized large multi-section config and
// prints a per-stage breakdown. The numbers answer chunt-cfz Q1: does parse
// dominate, or is there meaningful post-parse pass time that progressive
// publishing could surface to the user sooner?
//
// IMPORTANT CONTEXT for interpreting the numbers: DidChange (feature.go:86)
// calls parser.Parse(content, oldTree) but NEVER calls tree.Edit() with change
// ranges, because the LSP TextDocumentContentChangeEvent DTO carries only the
// new Text (no Range) — see internal/protocol/diagnostics.go. Without an edit,
// tree-sitter cannot reuse unchanged subtrees, so per-keystroke parse cost is
// ~= a full re-parse. parse-cold below is therefore representative of BOTH the
// didOpen path AND the per-keystroke didChange path. parse-incremental-no-edit
// measures Parse(newContent, oldTree) after a 1-char edit to confirm the old
// tree hint buys nothing without an edit.
func TestPipelineTiming(t *testing.T) {
	if os.Getenv("CHUNT_PERF") != "1" {
		t.Skip("set CHUNT_PERF=1 to run the pipeline-timing measurement (chunt-cfz)")
	}

	for _, n := range []int{50, 200, 1000} {
		content := genLargeConfig(n)
		uri := "file:///perf.cfg"
		doc := document.New(uri, "cisco_ios_jinja2", 1, content)
		lines := strings.Count(string(content), "\n")

		f := New()
		// Warm up: fill keyword/symbol caches and prime the cgo path.
		{
			tr := f.parser.Parse(content, nil)
			f.symbols.Index(uri, tr.RootNode(), content)
			f.runDiagnostics(doc, tr)
			tr.Close()
		}

		const iters = 20
		// measure returns the min of `iters` runs — the most stable estimator
		// for a CPU-bound hot path (filters out GC/scheduler noise).
		measure := func(name string, fn func()) time.Duration {
			best := time.Duration(1 << 62)
			for i := 0; i < iters; i++ {
				t0 := time.Now()
				fn()
				if d := time.Since(t0); d < best {
					best = d
				}
			}
			return best
		}

		// Stage 1a: cold parse (didOpen; ~= per-keystroke given the no-Edit
		// reality noted above).
		var tree *sitter.Tree
		parseCold := measure("parse-cold", func() {
			tree = f.parser.Parse(content, nil)
		})

		// Stage 1b: Parse(newContent, oldTree) with NO tree.Edit, 1-char edit.
		ins := bytes.Index(content, []byte("hostname"))
		edited := make([]byte, 0, len(content)+1)
		edited = append(edited, content[:ins]...)
		edited = append(edited, ' ')
		edited = append(edited, content[ins:]...)
		parseIncrNoEdit := measure("parse-incremental(no-Edit)", func() {
			t2 := f.parser.Parse(edited, tree)
			t2.Close()
		})

		// Re-establish a clean tree + symbol index for the pass timing below.
		tree = f.parser.Parse(content, nil)
		f.symbols.Index(uri, tree.RootNode(), content)

		idxTime := measure("symbols.Index", func() {
			f.symbols.Index(uri, tree.RootNode(), content)
		})

		// Per-pass timing. Closures return len() so we avoid importing the
		// protocol type purely for the measurement.
		type pass struct {
			name string
			fn   func() int
		}
		passes := []pass{
			// tree-collector is the merged single-pass walk (chunt-zob) folding
			// syntax + command-version + wrong-section + protocol-mismatch.
			{"tree-collector", func() int { return len(f.collectTreeDiags(doc, tree)) }},
			{"version-mismatch", func() int { return len(f.runVersionMismatchDiagnostics(doc, tree)) }},
			{"undefined-refs", func() int { return len(f.runUndefinedReferenceDiagnostics(doc)) }},
			{"duplicate-defs", func() int { return len(f.runDuplicateDefinitionDiagnostics(doc)) }},
		}
		passTotals := map[string]time.Duration{}
		var passSum time.Duration
		for _, p := range passes {
			d := measure(p.name, func() { p.fn() })
			passTotals[p.name] = d
			passSum += d
		}
		diagCount := len(f.runDiagnostics(doc, tree))

		tree.Close()
		f.Close()

		// --- report ---
		t.Logf("")
		t.Logf("=== scale n=%d  (%d lines, %d bytes, %d diagnostics) ===", n, lines, len(content), diagCount)
		t.Logf("  parse-cold               %12s   (didOpen, ~= per-keystroke: DidChange passes oldTree w/o tree.Edit)", parseCold)
		t.Logf("  parse-incremental(noEd)  %12s   (Parse(new, oldTree) no tree.Edit, 1-char edit)", parseIncrNoEdit)
		t.Logf("  symbols.Index            %12s", idxTime)
		t.Logf("  --- diagnostic passes (min of %d) ---", iters)
		for _, p := range passes {
			t.Logf("  %-24s %12s", p.name, passTotals[p.name])
		}
		t.Logf("  %-24s %12s   (sum of passes)", "PASSES TOTAL", passSum)
		t.Logf("")
		t.Logf("  parse share of (parse+passes)  = %5.1f%%", pct(parseCold, parseCold+passSum))
		t.Logf("  passes share                   = %5.1f%%", pct(passSum, parseCold+passSum))
		t.Logf("  symbols.Index share of passes  = %5.1f%%", pct(idxTime, passSum))
		t.Logf("  full pipeline (parse+index+passes) = %s", parseCold+idxTime+passSum)
	}
}

// TestTieredLatency measures the perceived-latency win of progressive
// publishing (chunt-cfz): how soon tier 1 (tree-only diagnostics) is
// published vs. how long the full pipeline (including symbols.Index + ref
// passes) takes to return. The win = tier-1 publish latency vs full-pipeline
// latency — i.e. when the user SEES the bulk of the diagnostics.
func TestTieredLatency(t *testing.T) {
	if os.Getenv("CHUNT_PERF") != "1" {
		t.Skip("set CHUNT_PERF=1 to run the tiered-latency measurement (chunt-cfz)")
	}

	for _, n := range []int{50, 200, 1000} {
		content := genLargeConfig(n)
		uri := "file:///perf-tiered.cfg"

		const iters = 20
		bestTier1 := time.Duration(1 << 62)
		bestFull := time.Duration(1 << 62)
		tier1Count, finalCount := 0, 0
		for i := 0; i < iters; i++ {
			f := New()
			doc := document.New(uri, "cisco_ios_jinja2", 1, content)
			start := time.Now()
			var tier1At time.Duration
			seenTier1 := false
			_, err := f.DidOpen(context.Background(), doc, func(diags []protocol.Diagnostic) {
				if !seenTier1 {
					tier1At = time.Since(start)
					tier1Count = len(diags)
					seenTier1 = true
				}
				finalCount = len(diags)
			})
			if err != nil {
				t.Fatalf("DidOpen: %v", err)
			}
			full := time.Since(start)
			if tier1At < bestTier1 {
				bestTier1 = tier1At
			}
			if full < bestFull {
				bestFull = full
			}
			f.Close()
		}

		t.Logf("")
		t.Logf("=== tiered latency n=%d (%d lines) ===", n, strings.Count(string(content), "\n"))
		t.Logf("  tier-1 publish (bulk of diags) %12s   (%d diags)", bestTier1, tier1Count)
		t.Logf("  full pipeline (DidOpen return) %12s   (%d diags)", bestFull, finalCount)
		t.Logf("  user sees diagnostics %5.1f%% sooner  (%s vs %s)",
			pct(bestFull-bestTier1, bestFull), bestTier1, bestFull)
	}
}

func pct(a, b time.Duration) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

// genLargeConfig synthesizes a realistic multi-section Cisco IOS config. `n`
// scales the repetition of each section kind (interfaces, ACL lines, BGP
// neighbors, OSPF networks, route-map sequences, ...). At n=200 it produces a
// few thousand lines — the range of a large real-world device config. Every
// command is drawn from the noise-free set proven by the golden fixtures so
// the wrong-section pass has real work but no spurious keyword-DB noise.
func genLargeConfig(n int) []byte {
	var b strings.Builder
	b.WriteString("! version 17.3\nversion 17.3\n!\nhostname r1\n!\n")

	// interfaces (config-if): the bulk of a real config
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "interface GigabitEthernet0/%d\n", i)
		b.WriteString(" description uplink\n")
		b.WriteString(" speed 1000\n")
		b.WriteString(" duplex full\n")
		b.WriteString("!\n")
	}

	// standard + extended ACLs with many lines
	b.WriteString("ip access-list standard ACL-STD\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, " permit 10.%d.0.0 0.0.0.255\n", i%256)
	}
	b.WriteString("!\n")
	b.WriteString("ip access-list extended ACL-EXT\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, " permit tcp any any eq %d\n", 1024+i)
	}
	b.WriteString("!\n")

	// router bgp with many neighbors + an address family
	b.WriteString("router bgp 100\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, " neighbor 10.0.%d.2 remote-as %d\n", i%256, 200+i)
	}
	b.WriteString(" address-family ipv4\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "  neighbor 10.0.%d.2 activate\n", i%256)
	}
	b.WriteString("!\n")

	// router ospf with many networks
	b.WriteString("router ospf 1\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, " network 10.%d.0.0 0.0.0.255 area 0\n", i%256)
	}
	b.WriteString("!\n")

	// route-map with many sequences
	b.WriteString("route-map RM-OUT permit 10\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, " set local-preference %d\n", 100+i)
	}
	b.WriteString("!\n")

	// class-map + policy-map, a handful of vlans, line vty
	b.WriteString("class-map match-any VOICE\n!\n")
	b.WriteString("policy-map QOS\n class VOICE\n class class-default\n!\n")
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, "vlan %d\n!\n", i)
	}
	b.WriteString("line vty 0 4\n transport input ssh\n!\n")

	return []byte(b.String())
}
