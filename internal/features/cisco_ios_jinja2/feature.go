package cisco_ios_jinja2

import (
	"context"
	"log/slog"
	"runtime/debug"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/keyword"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
	ts "github.com/dgethings/tree-sitter-cisco-ios-jinja2/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// CiscoIOSFeature implements the LSP feature set for Cisco IOS + Jinja2 configs.
// All methods are called from a single goroutine (the LSP server runs with
// jrpc2 Concurrency=1), so the mutable fields below (trees, parser, groups)
// need no synchronization. See cmd/serve.go.
type CiscoIOSFeature struct {
	parser  *sitter.Parser
	trees   map[string]*sitter.Tree
	keyword *keyword.Set
	symbols *symbols.Table
	// groups tracks one docGroup per directory that has an open document
	// (workspace mode: directory = one logical device config). See
	// workspace.go.
	groups map[string]*docGroup
}

func New() *CiscoIOSFeature {
	p := sitter.NewParser()
	p.SetLanguage(sitter.NewLanguage(ts.Language()))
	// Debug, not Info: the resolved grammar module/version is troubleshooting
	// detail, not per-run operational signal. At the default --log-level=info
	// this would otherwise leak into CLI output (e.g. `chunt check`). Surface
	// it with `chunt check --log-level debug`. (chunt-lto)
	slog.Debug("grammar", "module", GrammarModule, "version", GrammarVersion())
	keywords := keyword.NewSet(Keywords)
	// Fold the curated router-command overlay into the section-validity index
	// so canonical commands the generated DB mis-registers (network,
	// router-id) are not flagged as wrong-section inside router sections
	// (chunt-vzy). Hover/completion are unaffected.
	for name, sections := range routerKeywordOverlay {
		keywords.AddValidSections(name, sections...)
	}
	return &CiscoIOSFeature{
		parser:  p,
		trees:   make(map[string]*sitter.Tree),
		keyword: keywords,
		symbols: symbols.NewTable(),
		groups:  make(map[string]*docGroup),
	}
}

// GrammarModule is the tree-sitter grammar dependency, pulled from go.mod at
// build time. Keep in sync with the require line in go.mod.
const GrammarModule = "github.com/dgethings/tree-sitter-cisco-ios-jinja2"

// GrammarVersion reads the resolved version of the grammar module from the
// binary's build info (populated at `go build`). Returns "" when build info
// is unavailable (e.g. a stripped binary that lacks it).
func GrammarVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range bi.Deps {
		if dep.Path == GrammarModule {
			return dep.Version
		}
	}
	return ""
}

func (f *CiscoIOSFeature) LanguageID() string {
	return "cisco_ios_jinja2"
}

func (f *CiscoIOSFeature) Close() error {
	for _, t := range f.trees {
		t.Close()
	}
	f.trees = make(map[string]*sitter.Tree)
	f.groups = make(map[string]*docGroup)
	if f.parser != nil {
		f.parser.Close()
		f.parser = nil
	}
	return nil
}

// DidOpen parses the document and returns its diagnostics. When publish is
// non-nil the diagnostics are streamed in two tiers (progressive publishing,
// chunt-cfz): tier 1 publishes the tree-only passes (syntax/version/section/
// protocol) immediately after parse, BEFORE the expensive symbols.Index; tier 2
// publishes the full set (adds undefined-refs + duplicate-defs + cross-file
// workspace diagnostics) after Index. A nil publish skips mid-pipeline
// publishing and just returns the full set (used by cmd/check and unit
// tests). Tier 2 is omitted when it would equal tier 1 (no ref diagnostics),
// avoiding a redundant publish on clean configs — publishDiagnostics is
// full-replacement, so tier 1 already carried the full set in that case.
//
// Workspace mode: for file:// documents, opening also loads the sibling
// *.ios.j2 templates of the same directory as background documents and
// publishes THEIR diagnostics (see workspace.go). Non-file URIs run solo.
func (f *CiscoIOSFeature) DidOpen(ctx context.Context, doc *document.Document, publish publishFn) ([]protocol.Diagnostic, error) {
	g := f.ensureGroup(doc, publish)
	tree := f.parser.Parse(doc.Content, nil)
	if tree != nil {
		f.trees[doc.URI] = tree
	}
	if g != nil {
		g.open[doc.URI] = true
		g.docs[doc.URI] = doc
		g.rebuildOrder()
	}
	treeDiags := f.runTreeDiagnostics(doc, tree)
	if g != nil {
		// Cache tier 1 for later group-wide republishes (a sibling's change
		// must be able to re-assemble THIS file's full set without
		// re-parsing it) and route the tier-1 publish through the
		// fingerprint dedup.
		g.treeDiags[doc.URI] = treeDiags
		g.publishIfChanged(publish, doc, treeDiags)
	} else if publish != nil {
		publish(doc, treeDiags)
	}
	if tree != nil {
		f.symbols.Index(doc.URI, tree.RootNode(), doc.Content)
	}
	return f.finishDiagnostics(doc, g, treeDiags, publish), nil
}

// DidChange re-parses (incrementally where the old tree is known) and returns
// diagnostics. See DidOpen for the tiered-publishing contract of `publish`
// and workspace.go for the directory-group model. The group is rescanned
// (one stat per sibling) so out-of-band edits to non-open templates are
// picked up.
func (f *CiscoIOSFeature) DidChange(ctx context.Context, doc *document.Document, publish publishFn) ([]protocol.Diagnostic, error) {
	g := f.ensureGroup(doc, publish)
	// Parse without the old-tree hint: chunt does full-document sync, so the
	// LSP DTO carries no edit ranges and the old tree can never be
	// tree.Edit-ed to match the new content. Passing it unedited makes
	// tree-sitter reuse stale byte ranges whenever the content length
	// changes, corrupting node text (caught by the wire test as
	// `undefined acl "FOO in\n"` after MISSING -> FOO). The perf harness
	// (perf_pipeline_internal_test.go) measured the unedited hint as no
	// faster than a cold parse, so nothing is lost.
	oldTree := f.trees[doc.URI]
	newTree := f.parser.Parse(doc.Content, nil)
	if oldTree != nil {
		oldTree.Close()
	}
	f.trees[doc.URI] = newTree
	if g != nil {
		g.docs[doc.URI] = doc
	}
	treeDiags := f.runTreeDiagnostics(doc, newTree)
	if publish != nil {
		if g != nil {
			g.treeDiags[doc.URI] = treeDiags
			g.publishIfChanged(publish, doc, treeDiags)
		} else {
			publish(doc, treeDiags)
		}
	}
	f.symbols.Index(doc.URI, newTree.RootNode(), doc.Content)
	return f.finishDiagnostics(doc, g, treeDiags, publish), nil
}

// DidClose releases the document's tree and symbols. In workspace mode the
// directory group survives while any document in it remains open; the closed
// file is reloaded from disk as a background document (it is still part of
// the device config) and the remaining files' cross-file diagnostics are
// recomputed. When the last open document in a directory closes, the whole
// group is evicted and its background files' published sets are cleared.
func (f *CiscoIOSFeature) DidClose(ctx context.Context, doc *document.Document, publish publishFn) error {
	if t, ok := f.trees[doc.URI]; ok {
		t.Close()
		delete(f.trees, doc.URI)
	}
	f.symbols.Clear(doc.URI)
	g := f.groupOf(doc)
	if g == nil {
		return nil
	}
	delete(g.open, doc.URI)
	g.forget(doc.URI)
	delete(g.treeDiags, doc.URI)
	// The editor owns clearing the closed document's own diagnostics (solo
	// mode has never sent a clearing notification on close; parity kept).
	delete(g.lastPublished, doc.URI)
	if len(g.open) == 0 {
		g.evict(f, publish)
		return nil
	}
	g.rescan(f, publish)
	f.refreshGroup(g, nil, nil, publish)
	return nil
}

// finishDiagnostics runs the tier-2 passes and returns the (changed)
// document's full diagnostic set. Solo mode (g == nil) keeps the historical
// contract: run the symbol-table passes, publish the full set through
// `publish` only when the ref passes added diagnostics. Workspace mode
// delegates to refreshGroup, which also republishes every sibling whose set
// moved.
func (f *CiscoIOSFeature) finishDiagnostics(doc *document.Document, g *docGroup, treeDiags []protocol.Diagnostic, publish publishFn) []protocol.Diagnostic {
	if g == nil {
		refDiags := f.runRefDiagnostics(doc, nil)
		final := make([]protocol.Diagnostic, 0, len(treeDiags)+len(refDiags))
		final = append(final, treeDiags...)
		final = append(final, refDiags...)
		if publish != nil && len(refDiags) > 0 {
			publish(doc, final)
		}
		return final
	}
	return f.refreshGroup(g, doc, treeDiags, publish)
}

// refreshGroup recomputes the tier-2 (symbol-table) and cross-file sets for
// EVERY document in the group and publishes those that changed. When changed
// is non-nil (DidOpen/DidChange), its freshly computed treeDiags are used
// for its URI and its full set is returned; the cached g.treeDiags serve
// everyone else. When changed is nil (DidClose), all URIs are served from
// cache.
func (f *CiscoIOSFeature) refreshGroup(g *docGroup, changed *document.Document, treeDiags []protocol.Diagnostic, publish publishFn) []protocol.Diagnostic {
	cross := f.runWorkspaceDiagnostics(g)
	var final []protocol.Diagnostic
	for _, uri := range g.order {
		d := g.docs[uri]
		if d == nil {
			continue
		}
		t1 := g.treeDiags[uri]
		if changed != nil && uri == changed.URI {
			t1 = treeDiags
		}
		set := concatDiags(t1, f.runRefDiagnostics(d, g.order), cross[uri])
		if changed != nil && uri == changed.URI {
			final = set
		}
		g.publishIfChanged(publish, d, set)
	}
	return final
}

// concatDiags appends the non-nil rest onto head and returns the result.
func concatDiags(head []protocol.Diagnostic, rest ...[]protocol.Diagnostic) []protocol.Diagnostic {
	out := head
	for _, r := range rest {
		out = append(out, r...)
	}
	return out
}
