package cisco_ios_jinja2

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

var runningVersionRe = regexp.MustCompile(`^!\s*version\s+(\S+)`)

// runVersionMismatchDiagnostics reports when the running version (recorded
// in a `! version X` comment, as emitted by `show run`) differs from the
// configured `version Y` statement. The two are normally kept in sync by
// IOS; a mismatch usually means the file was hand-edited or the running
// image was downgraded without rewriting the config.
//
// All top-level named children are scanned: the running version is taken
// from the first comment whose text matches runningVersionRe (first wins,
// matching show-run emit order); the configured version is taken from the
// first version_statement. If only one is present, no diagnostic is
// emitted (that is not an error condition).
func (f *CiscoIOSFeature) runVersionMismatchDiagnostics(doc *document.Document, tree *sitter.Tree) []protocol.Diagnostic {
	var diags []protocol.Diagnostic
	if tree == nil {
		return diags
	}
	root := tree.RootNode()
	if root == nil {
		return diags
	}

	var cfgVerNode, cfgVerField *sitter.Node
	for i := uint(0); i < root.NamedChildCount(); i++ {
		c := root.NamedChild(i)
		if c == nil {
			continue
		}
		if cfgVerNode == nil && c.Kind() == "version_statement" {
			cfgVerNode = c
			cfgVerField = c.ChildByFieldName("configured_version")
		}
	}

	runVer := runningVersion(doc, tree)
	var cfgVer string
	if cfgVerField != nil {
		cfgVer = string(doc.Content[cfgVerField.StartByte():cfgVerField.EndByte()])
	}
	if runVer != "" && cfgVer != "" && runVer != cfgVer {
		diags = append(diags, protocol.Diagnostic{
			Range:    protocol.LineRange(cfgVerNode.StartPosition().Row, cfgVerNode.StartPosition().Column, cfgVerNode.EndPosition().Column),
			Severity: protocol.SeverityError,
			Source:   "chunt",
			Code:     "version-mismatch",
			Message:  "running version and configured version mismatch",
		})
	}

	return diags
}

// appendCommandVersion is the per-node command-version check, folded into
// the single-pass tree collector (chunt-zob, see collectTreeDiagnostics in
// diagnostics.go). It emits a Hint for each command whose documented
// MinVersion is later than the running version (recorded in a `! version X`
// comment): the command was introduced after the image currently running the
// config, so it may not be recognized. Only the introduced-after signal is
// implemented: the scraper's MaxVersion extraction is too unreliable to flag
// safely (thousands of clean-numeric MaxVersion values are smaller than a
// modern running version — e.g. hostname=15.0 — which would flood clean
// configs with false positives). Both version comparators refuse non-numeric
// components, so a heuristic value like "3.9S" is treated as incomparable and
// never flagged (chunt-y9d).
//
// The collector computes the running version once and only calls this when it
// is non-empty; with no `! version X` comment this is never invoked. The
// collector calls this only on named command-like nodes; negated_statement is
// descended into (mirrors wrong-section).
func (f *CiscoIOSFeature) appendCommandVersion(diags *[]protocol.Diagnostic, n *sitter.Node, content []byte, runVer string) {
	name := firstKeywordFromNode(n, content, f.keyword)
	if name == "" {
		return
	}
	kw, ok := f.keyword.Lookup(name)
	if !ok || kw.MinVersion == "" {
		return
	}
	cmp, comparable := compareVersions(kw.MinVersion, runVer)
	if !comparable || cmp <= 0 {
		return
	}
	*diags = append(*diags, protocol.Diagnostic{
		Range:    protocol.LineRange(n.StartPosition().Row, n.StartPosition().Column, n.EndPosition().Column),
		Severity: protocol.SeverityHint,
		Source:   "chunt",
		Code:     "version-introduced",
		Message:  fmt.Sprintf("%s was introduced in release %s, later than the running version %s", name, kw.MinVersion, runVer),
	})
}

// runningVersion returns the IOS version recorded in the first `! version X`
// top-level comment, or "" when none is present. show-run emits the running
// version as the first comment, so first-wins matches the on-wire order.
func runningVersion(doc *document.Document, tree *sitter.Tree) string {
	if tree == nil {
		return ""
	}
	root := tree.RootNode()
	if root == nil {
		return ""
	}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		c := root.NamedChild(i)
		if c == nil || c.Kind() != "comment" {
			continue
		}
		if m := runningVersionRe.FindStringSubmatch(string(doc.Content[c.StartByte():c.EndByte()])); m != nil {
			return m[1]
		}
	}
	return ""
}

// compareVersions compares two dot-separated IOS version strings component by
// component (e.g. "12.2" < "17.3", "26.1.0" > "17.3"). It returns (cmp, true)
// where cmp is -1, 0, or 1, or (0, false) when either version contains a
// component that is not a pure integer (e.g. "3.9S", "15.7(3)M"): such values
// are not safely comparable and callers must refuse to flag on them. When the
// versions share a common prefix and differ in length, the longer one is
// greater only if a trailing component is non-zero (so 17.3 == 17.3.0).
func compareVersions(a, b string) (int, bool) {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for _, p := range ap {
		if _, err := strconv.Atoi(p); err != nil {
			return 0, false
		}
	}
	for _, p := range bp {
		if _, err := strconv.Atoi(p); err != nil {
			return 0, false
		}
	}
	n := len(ap)
	if len(bp) < n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		ai, _ := strconv.Atoi(ap[i])
		bi, _ := strconv.Atoi(bp[i])
		if ai != bi {
			if ai < bi {
				return -1, true
			}
			return 1, true
		}
	}
	// Common prefix equal: decide on the longer tail.
	if len(ap) == len(bp) {
		return 0, true
	}
	if len(ap) > len(bp) {
		for i := n; i < len(ap); i++ {
			if v, _ := strconv.Atoi(ap[i]); v != 0 {
				return 1, true
			}
		}
		return 0, true
	}
	for i := n; i < len(bp); i++ {
		if v, _ := strconv.Atoi(bp[i]); v != 0 {
			return -1, true
		}
	}
	return 0, true
}
