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
// jrpc2 Concurrency=1), so the mutable fields below (trees, parser) need no
// synchronization. See cmd/serve.go.
type CiscoIOSFeature struct {
	parser  *sitter.Parser
	trees   map[string]*sitter.Tree
	keyword *keyword.Set
	symbols *symbols.Table
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
	}
}

// GrammarModule is the tree-sitter grammar dependency, pulled from go.mod at
// build time. Keep in sync with the require line in go.mod.
const GrammarModule = "github.com/dgethings/tree-sitter-cisco-ios-jinja2"

// GrammarVersion reads the resolved version of the grammar module from the
// binary's build info (populated at `go build`). Returns "" when build info is
// unavailable (e.g. a stripped binary that lacks it).
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
// publishes the full set (adds undefined-refs + duplicate-defs) after Index. A
// nil publish skips mid-pipeline publishing and just returns the full set (used
// by cmd/check and unit tests). Tier 2 is omitted when it would equal tier 1
// (no ref diagnostics), avoiding a redundant publish on clean configs —
// publishDiagnostics is full-replacement, so tier 1 already carried the full
// set in that case.
func (f *CiscoIOSFeature) DidOpen(ctx context.Context, doc *document.Document, publish func([]protocol.Diagnostic)) ([]protocol.Diagnostic, error) {
	tree := f.parser.Parse(doc.Content, nil)
	if tree != nil {
		f.trees[doc.URI] = tree
	}
	treeDiags := f.runTreeDiagnostics(doc, tree)
	if publish != nil {
		publish(treeDiags)
	}
	if tree != nil {
		f.symbols.Index(doc.URI, tree.RootNode(), doc.Content)
	}
	return f.finishRefDiagnostics(doc, treeDiags, publish), nil
}

// DidChange re-parses (incrementally where the old tree is known) and returns
// diagnostics. See DidOpen for the tiered-publishing contract of `publish`.
func (f *CiscoIOSFeature) DidChange(ctx context.Context, doc *document.Document, publish func([]protocol.Diagnostic)) ([]protocol.Diagnostic, error) {
	oldTree := f.trees[doc.URI]
	newTree := f.parser.Parse(doc.Content, oldTree)
	if oldTree != nil {
		oldTree.Close()
	}
	f.trees[doc.URI] = newTree
	treeDiags := f.runTreeDiagnostics(doc, newTree)
	if publish != nil {
		publish(treeDiags)
	}
	f.symbols.Index(doc.URI, newTree.RootNode(), doc.Content)
	return f.finishRefDiagnostics(doc, treeDiags, publish), nil
}

// finishRefDiagnostics runs the symbol-table-dependent passes (tier 2) and
// returns the full accumulated set. It builds `final` as a fresh slice (not
// aliasing treeDiags's backing array) so an async-queued tier-2 publish cannot
// observe a later append. When publish is non-nil it publishes the full set —
// unless there are no ref diagnostics, in which case tier 1 already published
// the complete set and a tier-2 publish would be a redundant no-op.
func (f *CiscoIOSFeature) finishRefDiagnostics(doc *document.Document, treeDiags []protocol.Diagnostic, publish func([]protocol.Diagnostic)) []protocol.Diagnostic {
	refDiags := f.runRefDiagnostics(doc)
	final := make([]protocol.Diagnostic, 0, len(treeDiags)+len(refDiags))
	final = append(final, treeDiags...)
	final = append(final, refDiags...)
	if publish != nil && len(refDiags) > 0 {
		publish(final)
	}
	return final
}

func (f *CiscoIOSFeature) DidClose(ctx context.Context, doc *document.Document) error {
	if t, ok := f.trees[doc.URI]; ok {
		t.Close()
		delete(f.trees, doc.URI)
	}
	f.symbols.Clear(doc.URI)
	return nil
}
