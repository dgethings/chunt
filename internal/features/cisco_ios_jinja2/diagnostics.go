package cisco_ios_jinja2

import (
	"strings"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// The diagnostic passes split into two tiers for progressive publishing
// (chunt-cfz; see DESIGN-chunt-cfz-progressive-diagnostics.md):
//
//   - runTreeDiagnostics: passes that depend ONLY on the parse tree + keyword
//     DB (NOT the symbol table). These can be published as tier 1 immediately
//     after parse, before the expensive symbols.Index.
//   - runRefDiagnostics: the two passes that depend on the symbol table
//     (undefined-refs, duplicate-defs). symbols.Index must run first; these are
//     tier 2.
//
// New passes are added as separate methods (run*Diagnostics) and concatenated
// into the appropriate tier here. The ordering within a tier matters only for
// tests that assert exact diagnostic order; the LSP client renders them sorted
// by location, and goldens are sorted by (line, column, code, severity).
//
// Current passes:
//
// tree-only (tier 1):
//  A. version mismatch   (SeverityError) — diagnostics_version.go
//     Scans only top-level named children for the `! version X` comment and
//     `version Y` statement; NOT a full tree walk, so it stays its own pass.
//  B. merged tree collector (collectTreeDiags) — ONE full walk of the tree
//     (chunt-zob) folding the per-node checks that each used to walk the
//     whole tree independently:
//       - syntax / missing  (Error/Warning) — diagnostics_syntax.go
//       - command version   (SeverityHint)  — diagnostics_version.go
//       - wrong section     (SeverityHint)  — diagnostics_section.go
//       - protocol mismatch (SeverityError) — diagnostics_protocol.go
//     The tree is now traversed once per didChange instead of 4 times.
//
// symbol-dependent (tier 2):
//  C. undefined refs      (SeverityWarning) — diagnostics_refs.go
//  D. duplicate defs      (SeverityWarning) — diagnostics_refs.go

// runTreeDiagnostics runs the passes that need only the parse tree + keyword DB.
// It does NOT consult the symbol table, so it may run before symbols.Index.
func (f *CiscoIOSFeature) runTreeDiagnostics(doc *document.Document, tree *sitter.Tree) []protocol.Diagnostic {
	diags := make([]protocol.Diagnostic, 0)
	diags = append(diags, f.runVersionMismatchDiagnostics(doc, tree)...)
	diags = append(diags, f.collectTreeDiags(doc, tree)...)
	return diags
}

// collectTreeDiags is the single-pass tree-walking diagnostic collector
// (chunt-zob). It folds the per-node checks that previously each walked the
// whole tree — syntax/MISSING (appendSyntaxDiag), command-version
// (appendCommandVersion), wrong-section (appendWrongSection), and
// protocol-mismatch (appendProtocolMismatch) — into ONE traversal of the parse
// tree, so the tree is visited once per didChange instead of 4 times.
// version-mismatch is NOT here: it scans only top-level named children, not a
// full walk, and stays a separate pass (see runTreeDiagnostics).
//
// Diagnostics output is BYTE-IDENTICAL to the former multi-walk version. The
// guard is the existing golden suite (sorted by line/col/code/severity) plus
// the table-driven inline tests. Two design points preserve that identity:
//
//  1. Traversal is walkAll-style (every child, named AND anonymous). The
//     syntax check must see anonymous tokens: a MISSING `}}` is an anonymous
//     child of a named `output` node (verified), and unclosedJinjaDiagnostics
//     stack-matches anonymous Jinja openers/closers. A named-only walk
//     (ast.WalkNamed) would skip them.
//
//  2. ERROR-subtree descent. The old runSyntaxDiagnostics returned false on an
//     ERROR node to skip its subtree (descending would re-report the recovered
//     tokens it wrapped). The merged walk still needs to descend into ERROR
//     subtrees so the command checks can run on commands recovery swallowed —
//     but the syntax check must NOT re-report inside an ERROR. This is
//     reconciled with an `inError` flag threaded through the recursion:
//     appendSyntaxDiag runs only when inError is false, while the command
//     checks run on every named command-like node regardless. For
//     wrong-section/protocol this is byte-identical: wrong-section already
//     suppressed itself via unreliableSectionContext when an ERROR is an
//     ancestor, and protocol's registry only matches dedicated *_statement
//     kinds / exclusive keywords the recovery preserves.
//
// Diagnostics are collected in tree order; no consumer depends on order
// (goldens sort; tiered tests use set-equality).
func (f *CiscoIOSFeature) collectTreeDiags(doc *document.Document, tree *sitter.Tree) []protocol.Diagnostic {
	if tree == nil {
		return nil
	}
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	runVer := runningVersion(doc, tree)
	content := doc.Content
	var diags []protocol.Diagnostic

	var walk func(n *sitter.Node, inError bool)
	walk = func(n *sitter.Node, inError bool) {
		// Syntax / missing check: only outside ERROR subtrees (point 2 above).
		if !inError {
			f.appendSyntaxDiag(&diags, n, content)
		}
		// Command-level checks: named command-like nodes only. negated_statement
		// is descended into (its inner command is command-like) but is not itself
		// checked, mirroring the former passes.
		if n.IsNamed() {
			kind := n.Kind()
			if kind != "negated_statement" && (kind == "command_line" || strings.HasSuffix(kind, "_statement")) {
				f.appendWrongSection(&diags, n, content)
				f.appendProtocolMismatch(&diags, n, content)
				if runVer != "" {
					f.appendCommandVersion(&diags, n, content, runVer)
				}
			}
		}
		// Descend into every child (named + anonymous), threading inError so the
		// syntax check is suppressed throughout an ERROR subtree.
		childInError := inError || n.IsError()
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i), childInError)
		}
	}
	walk(root, false)
	return diags
}

// runRefDiagnostics runs the passes that depend on the symbol table
// (undefined-refs, duplicate-defs). symbols.Index must have run first.
func (f *CiscoIOSFeature) runRefDiagnostics(doc *document.Document) []protocol.Diagnostic {
	diags := make([]protocol.Diagnostic, 0)
	diags = append(diags, f.runUndefinedReferenceDiagnostics(doc)...)
	diags = append(diags, f.runDuplicateDefinitionDiagnostics(doc)...)
	return diags
}

// runDiagnostics returns the full set (tree + ref) in one shot. The live LSP
// path (DidOpen/DidChange) instead publishes runTreeDiagnostics as tier 1
// before symbols.Index, then runRefDiagnostics as tier 2. The set is identical
// to this one-shot result; only the internal ordering differs (ref diagnostics
// move to the tail), which no consumer depends on (goldens sort; inline tests
// search by code). Kept as a convenience for one-shot callers (perf harness).
func (f *CiscoIOSFeature) runDiagnostics(doc *document.Document, tree *sitter.Tree) []protocol.Diagnostic {
	diags := f.runTreeDiagnostics(doc, tree)
	diags = append(diags, f.runRefDiagnostics(doc)...)
	return diags
}
