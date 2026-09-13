package symbols_test

import (
	"sync"
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
	ts_ci "github.com/dgethings/tree-sitter-cisco-ios-jinja2/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// parseRootForConcurrent is a local helper for the concurrency test (mirrors
// parseRoot in symbols_test.go but self-contained to avoid coupling).
func parseRootForConcurrent(t *testing.T, src string) (*sitter.Node, []byte) {
	t.Helper()
	p := sitter.NewParser()
	p.SetLanguage(sitter.NewLanguage(ts_ci.Language()))
	tree := p.Parse([]byte(src), nil)
	t.Cleanup(func() {
		tree.Close()
		p.Close()
	})
	return tree.RootNode(), []byte(src)
}

// TestTable_Concurrent verifies the symbols.Table is safe under concurrent
// Index + Lookup + ReferencesAll on the same URIs. Run with -race.
func TestTable_Concurrent(t *testing.T) {
	t.Parallel()
	tbl := symbols.NewTable()

	const workers = 8
	const iters = 100

	sources := []string{
		"interface Gi0/0\n ip access-group ACL1 in\n!\n",
		"router bgp 100\n neighbor 10.0.0.2 remote-as 200\n!\n",
		"interface Gi1/0\n switchport access vlan 99\n!\n",
	}

	// Pre-parse once so each goroutine reuses the same content; Index takes
	// the root + content, both of which are read-only during indexing.
	type parsed struct {
		root    *sitter.Node
		content []byte
	}
	parsedSources := make([]parsed, len(sources))
	for i, src := range sources {
		root, content := parseRootForConcurrent(t, src)
		parsedSources[i] = parsed{root: root, content: content}
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				uri := "file:///conc-" + string(rune('a'+(seed%3)))
				ps := parsedSources[seed%len(parsedSources)]
				switch i % 3 {
				case 0:
					tbl.Index(uri, ps.root, ps.content)
				case 1:
					_ = tbl.Lookup(uri, symbols.KindInterface, "Gi0/0")
					_ = tbl.LookupAny(uri, "ACL1")
				case 2:
					_ = tbl.ReferencesAll(uri)
					_ = tbl.All(uri)
				}
			}
		}(w)
	}
	wg.Wait()
}
