package cisco_ios_jinja2

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features"
	"github.com/dgethings/chunt/internal/protocol"
)

// Directory-as-one-config (workspace mode).
//
// A network device's configuration is commonly split across several Jinja2
// template files: base config, routing, ACLs, interfaces... chunt treats
// every *.ios.j2 file in the SAME DIRECTORY as ONE logical device config
// (same device type per directory). Within that model:
//
//   - Sections may be defined in multiple files (`router ospf` in one,
//     `router bgp` in another): distinct sections are simply distinct, and
//     the SAME section identity (e.g. `router bgp 65001`) appearing in
//     several files merges silently into one logical section.
//   - Duplicate/conflicting definitions inside the merged logical config
//     are still flagged (diagnostics_workspace.go): two different
//     `router-id` values under the same `router bgp` section — even in
//     different files — is an Error.
//   - References resolve across the whole group: `ip access-group MGMT in`
//     in one file is satisfied by `ip access-list ... MGMT` in another.
//
// Discovery is server-side (client-agnostic): whenever a document is opened
// or changed, chunt rescans its directory (one stat per sibling; files are
// (re)read only when new or their mtime changed). Editors keep the open
// buffer authoritative — files open in the editor are never re-read from
// disk. Sibling files that are not open are loaded as BACKGROUND documents:
// parsed, symbol-indexed, and diagnosed, with their diagnostics published
// too (editors show them in the Problems panel without opening the file).
//
// All group state is mutated only from the feature's single-threaded LSP
// dispatch (jrpc2 Concurrency=1), like the rest of the feature's caches.
//
// Out of scope (v1): subdirectories (a device is ONE directory, not a tree),
// template-variable-aware section identity (`interface {{ name }}` merges by
// literal header text), and same-URI scheme normalization beyond file://
// (untitled:/ssh: documents run in solo, single-file mode).

// publishFn names the features.PublishFunc callback for the workspace
// internals below (alias, so the feature's methods satisfy the Feature
// interface exactly).
type publishFn = features.PublishFunc

// docGroup is the state of one directory's logical device config.
type docGroup struct {
	dir string

	// order is the canonical merge order: every tracked URI sorted by base
	// filename. "Later definition wins" semantics (IOS merge order) resolve
	// deterministically against this order regardless of which file the
	// editor touched last.
	order []string

	docs  map[string]*document.Document // URI -> current doc (open or background)
	open  map[string]bool               // URIs currently open in the editor
	mtime map[string]time.Time          // disk mtime of background files

	// treeDiags caches each background URI's tree-only (tier-1)
	// diagnostics from its last disk load. Open URIs' tier-1 sets are
	// recomputed on every change and never read from here.
	treeDiags map[string][]protocol.Diagnostic

	// lastPublished fingerprints the last set published per URI, so
	// unchanged sets are not re-notified on every keystroke. Absent key =
	// never published (an empty set still publishes once, to clear).
	lastPublished map[string]string
}

// groupDir maps a file:// URI to its directory path. Non-file URIs
// (untitled:, ssh:, plain paths from before canonicalization) return
// ok=false and run in solo mode.
func groupDir(uri string) (dir string, ok bool) {
	p, ok := uriPath(uri)
	if !ok {
		return "", false
	}
	return filepath.Dir(p), true
}

// uriPath converts a file:// URI to a filesystem path. ok=false for other
// schemes.
func uriPath(uri string) (string, bool) {
	if !strings.HasPrefix(uri, "file://") {
		return "", false
	}
	return strings.TrimPrefix(uri, "file://"), true
}

// fileURI canonicalizes a filesystem path to the URI form editors use
// (VS Code, neovim: file://<abs-path>), so background documents and the
// editor's own URIs for the same file coincide — publishDiagnostics and
// relatedInformation then land on the right editor document.
func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "file://" + path
	}
	return "file://" + abs
}

// ensureGroup returns the directory group for doc, creating and rescanning
// it as needed. Returns nil when doc is not a file:// URI (solo mode: the
// feature behaves exactly as in the single-file world).
func (f *CiscoIOSFeature) ensureGroup(doc *document.Document, publish publishFn) *docGroup {
	dir, ok := groupDir(doc.URI)
	if !ok {
		return nil
	}
	g := f.groups[dir]
	if g == nil {
		g = &docGroup{
			dir:           dir,
			docs:          make(map[string]*document.Document),
			open:          make(map[string]bool),
			mtime:         make(map[string]time.Time),
			treeDiags:     make(map[string][]protocol.Diagnostic),
			lastPublished: make(map[string]string),
		}
		f.groups[dir] = g
	}
	g.rescan(f, publish)
	return g
}

// groupOf returns the existing group for doc's directory, or nil (solo mode
// or no open document in that directory anymore).
func (f *CiscoIOSFeature) groupOf(doc *document.Document) *docGroup {
	dir, ok := groupDir(doc.URI)
	if !ok {
		return nil
	}
	return f.groups[dir]
}

// rescan reconciles the group with the directory's current *.ios.j2 files:
// loads new or changed background files, drops removed ones (publishing an
// empty set to clear their stale diagnostics), and rebuilds the canonical
// order. Files open in the editor are skipped — their buffer is
// authoritative until closed.
func (g *docGroup) rescan(f *CiscoIOSFeature, publish publishFn) {
	matches, err := filepath.Glob(filepath.Join(g.dir, "*.ios.j2"))
	if err != nil {
		return
	}
	want := make(map[string]time.Time, len(matches))
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil {
			want[fileURI(m)] = st.ModTime()
		}
	}

	for uri, mt := range want {
		if g.open[uri] {
			continue
		}
		if prev, ok := g.mtime[uri]; ok && prev.Equal(mt) {
			continue
		}
		g.loadBackground(f, uri)
		g.mtime[uri] = mt
	}
	var dropped []string
	for uri := range g.mtime {
		if _, ok := want[uri]; !ok {
			dropped = append(dropped, uri)
		}
	}
	for _, uri := range dropped {
		g.dropBackground(f, uri)
		delete(g.mtime, uri)
		// The file is gone; clear whatever we last told the editor, then
		// stop tracking it (including in the merge order).
		if _, was := g.lastPublished[uri]; was {
			g.lastPublished[uri] = "[]"
			if publish != nil {
				publish(g.docs[uri], nil)
			}
		}
		g.forget(uri)
	}
	g.rebuildOrder()
}

// loadBackground reads uri from disk, parses it, symbol-indexes it, and
// caches its tree-only diagnostics. Replaces any previous background state
// for the URI (mtime-triggered reload).
func (g *docGroup) loadBackground(f *CiscoIOSFeature, uri string) {
	path, _ := uriPath(uri)
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	doc := document.New(uri, f.LanguageID(), 0, content)
	tree := f.parser.Parse(doc.Content, nil)
	if tree == nil {
		return
	}
	if old, ok := f.trees[uri]; ok {
		old.Close()
	}
	f.trees[uri] = tree
	f.symbols.Index(uri, tree.RootNode(), doc.Content)
	g.docs[uri] = doc
	g.treeDiags[uri] = f.runTreeDiagnostics(doc, tree)
}

// dropBackground releases a background URI's tree, symbols, and caches.
// The Document pointer stays in g.docs until forget() so the caller can
// still address it for a final clearing notification.
func (g *docGroup) dropBackground(f *CiscoIOSFeature, uri string) {
	if t, ok := f.trees[uri]; ok {
		t.Close()
		delete(f.trees, uri)
	}
	f.symbols.Clear(uri)
	delete(g.treeDiags, uri)
}

// forget drops the doc pointer after a clearing publish (separate from
// dropBackground so rescan can still address the doc for the final empty
// notification).
func (g *docGroup) forget(uri string) {
	delete(g.docs, uri)
}

// rebuildOrder recomputes the canonical merge order: every tracked URI
// sorted by base filename.
func (g *docGroup) rebuildOrder() {
	g.order = g.order[:0]
	for uri := range g.docs {
		g.order = append(g.order, uri)
	}
	sort.Slice(g.order, func(i, j int) bool {
		return baseName(g.order[i]) < baseName(g.order[j])
	})
}

func baseName(uri string) string {
	if p, ok := uriPath(uri); ok {
		return filepath.Base(p)
	}
	return uri
}

// evict tears the whole group down (no open documents remain in the
// directory). Background trees and symbols are released, and background
// URIs whose last published set was non-empty get an empty set published so
// the editor's Problems panel does not keep showing diagnostics for files
// chunt no longer tracks.
func (g *docGroup) evict(f *CiscoIOSFeature, publish publishFn) {
	for uri := range g.mtime {
		g.dropBackground(f, uri)
		if fp, was := g.lastPublished[uri]; was && fp != "[]" {
			if publish != nil {
				publish(g.docs[uri], nil)
			}
		}
		g.forget(uri)
	}
	delete(f.groups, g.dir)
}

// publishIfChanged publishes set for d unless it is identical (by
// fingerprint) to the last set published for its URI. This is what keeps
// workspace mode cheap on the wire: a keystroke in one file republishes
// only the files whose diagnostics actually moved.
func (g *docGroup) publishIfChanged(publish publishFn, d *document.Document, set []protocol.Diagnostic) {
	if publish == nil {
		return
	}
	fp := diagFingerprint(set)
	if g.lastPublished[d.URI] == fp {
		return
	}
	g.lastPublished[d.URI] = fp
	publish(d, set)
}

// diagFingerprint renders a diagnostic set as a stable comparison key.
// fmt's struct formatting is deterministic for these value types; only
// equality matters, not compactness.
func diagFingerprint(set []protocol.Diagnostic) string {
	return fmt.Sprintf("%v", set)
}

// runWorkspaceDiagnostics runs the cross-file passes over the whole group
// and returns the diagnostics each URI gained from OTHER files' context.
// See diagnostics_workspace.go for the logical-scope model.
func (f *CiscoIOSFeature) runWorkspaceDiagnostics(g *docGroup) map[string][]protocol.Diagnostic {
	if g == nil {
		return nil
	}
	return f.collectWorkspaceScopes(g)
}
