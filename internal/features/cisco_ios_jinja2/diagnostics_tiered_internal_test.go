package cisco_ios_jinja2

import (
	"context"
	"fmt"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
)

// Tests for the progressive-publishing contract (chunt-cfz): DidOpen/DidChange
// with a non-nil publish callback emit diagnostics in tiers — tree-only passes
// first (before symbols.Index), then the full set (after). See
// DESIGN-chunt-cfz-progressive-diagnostics.md.

func diagKey(d protocol.Diagnostic) string {
	return fmt.Sprintf("%s|%d:%d|%s", d.Code, d.Range.Start.Line, d.Range.Start.Character, d.Message)
}

func diagSet(ds []protocol.Diagnostic) map[string]struct{} {
	m := make(map[string]struct{}, len(ds))
	for _, d := range ds {
		m[diagKey(d)] = struct{}{}
	}
	return m
}

// capturePublish returns a publish callback that snapshots every tier it
// receives (copied, so later caller mutation cannot rewrite a captured tier).
func capturePublish() (func([]protocol.Diagnostic), *[][]protocol.Diagnostic) {
	var tiers [][]protocol.Diagnostic
	cb := func(ds []protocol.Diagnostic) {
		cp := make([]protocol.Diagnostic, len(ds))
		copy(cp, ds)
		tiers = append(tiers, cp)
	}
	return cb, &tiers
}

func tieredFindByCode(ds []protocol.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func setSubset(small, big map[string]struct{}) bool {
	for k := range small {
		if _, ok := big[k]; !ok {
			return false
		}
	}
	return true
}

func setEq(a, b map[string]struct{}) bool {
	return len(a) == len(b) && setSubset(a, b)
}

// TestTieredPublishing_BothTiers exercises the full contract on a config with
// both a tree-only diagnostic (missing "}}") and a symbol-table diagnostic
// (undefined acl): tier 1 publishes the tree pass before symbols.Index, tier 2
// publishes the full set after, and that full set equals a nil-publish DidOpen.
func TestTieredPublishing_BothTiers(t *testing.T) {
	src := []byte("interface Gi0/0\n ip access-group ACL1 in\n!\nhostname r{{\n")
	doc := document.New("file:///tiered.ios.j2", "cisco_ios_jinja2", 1, src)

	publish, tiersPtr := capturePublish()
	f := New()
	defer f.Close()

	final, err := f.DidOpen(context.Background(), doc, publish)
	if err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	tiers := *tiersPtr
	if len(tiers) != 2 {
		t.Fatalf("expected 2 publishes (tier1 + tier2), got %d: %+v", len(tiers), tiers)
	}
	tier1, tier2 := tiers[0], tiers[1]

	// tier 1 (tree-only) must contain the syntax diagnostic, NOT the ref one
	// (the ref diagnostic needs symbols.Index, which has not run yet at tier 1).
	if !tieredFindByCode(tier1, "missing-}}") {
		t.Errorf("tier 1 missing tree diagnostic missing-}}: %+v", tier1)
	}
	if tieredFindByCode(tier1, "undefined-acl") {
		t.Errorf("tier 1 must not contain ref diagnostic undefined-acl (needs symbols.Index): %+v", tier1)
	}

	// tier 2 (the accumulated full set) must contain BOTH.
	if !tieredFindByCode(tier2, "missing-}}") {
		t.Errorf("tier 2 missing tree diagnostic missing-}}: %+v", tier2)
	}
	if !tieredFindByCode(tier2, "undefined-acl") {
		t.Errorf("tier 2 missing ref diagnostic undefined-acl: %+v", tier2)
	}

	// tier 1 ⊆ tier 2.
	if !setSubset(diagSet(tier1), diagSet(tier2)) {
		t.Errorf("tier 1 is not a subset of tier 2\ntier1: %+v\ntier2: %+v", tier1, tier2)
	}

	// tier 2 (final accumulated set) == nil-publish DidOpen; and DidOpen's
	// return value == the same set.
	want, err := f.DidOpen(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("DidOpen(nil): %v", err)
	}
	if !setEq(diagSet(tier2), diagSet(want)) {
		t.Errorf("tier 2 != nil-publish final\ntier2: %+v\nwant:  %+v", tier2, want)
	}
	if !setEq(diagSet(final), diagSet(want)) {
		t.Errorf("DidOpen return != nil-publish final\ngot:  %+v\nwant: %+v", final, want)
	}
}

// TestTieredPublishing_NoRefs verifies the optimization: when there are no
// symbol-table diagnostics, tier 1 already carries the full set and tier 2 is
// skipped (it would be a redundant no-op under publishDiagnostics
// full-replacement semantics).
func TestTieredPublishing_NoRefs(t *testing.T) {
	src := []byte("hostname r{{\n") // tree-only diagnostic, no undefined refs/duplicates
	doc := document.New("file:///tiered-noref.ios.j2", "cisco_ios_jinja2", 1, src)

	publish, tiersPtr := capturePublish()
	f := New()
	defer f.Close()

	final, err := f.DidOpen(context.Background(), doc, publish)
	if err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	tiers := *tiersPtr
	if len(tiers) != 1 {
		t.Fatalf("expected 1 publish (no ref diags -> skip tier 2), got %d: %+v", len(tiers), tiers)
	}
	want, err := f.DidOpen(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("DidOpen(nil): %v", err)
	}
	if !setEq(diagSet(tiers[0]), diagSet(final)) || !setEq(diagSet(final), diagSet(want)) {
		t.Errorf("tier 1 / final / nil-publish mismatch\ntier1: %+v\nfinal: %+v\nwant: %+v", tiers[0], final, want)
	}
}

// TestTieredPublishing_Clean: a clean config publishes once with an empty set.
func TestTieredPublishing_Clean(t *testing.T) {
	src := []byte("version 17.3\n!\nhostname r1\n!\n")
	doc := document.New("file:///tiered-clean.ios.j2", "cisco_ios_jinja2", 1, src)

	publish, tiersPtr := capturePublish()
	f := New()
	defer f.Close()

	if _, err := f.DidOpen(context.Background(), doc, publish); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	tiers := *tiersPtr
	if len(tiers) != 1 {
		t.Fatalf("expected 1 publish for a clean config, got %d: %+v", len(tiers), tiers)
	}
	if len(tiers[0]) != 0 {
		t.Errorf("expected empty tier-1 for a clean config, got %+v", tiers[0])
	}
}

// TestTieredPublishing_DidChange exercises the tiered contract on the
// didChange path. The edit re-sends the SAME content (a no-op, e.g. a redundant
// notify): the stored old tree is valid for identical content, so the
// incremental parse is deterministic and equals a cold parse. A real content
// change would trip chunt's pre-existing stale-incremental-parse limitation
// (DidChange passes oldTree without a tree.Edit because the LSP DTO carries no
// Range) — orthogonal to the tiered-publishing contract under test here.
func TestTieredPublishing_DidChange(t *testing.T) {
	// Config with BOTH a tree diagnostic (missing "}}") and a ref diagnostic
	// (undefined acl) so both tiers are non-empty.
	src := []byte("interface Gi0/0\n ip access-group ACL1 in\n!\nhostname r{{\n")
	doc := document.New("file:///tiered-dc.ios.j2", "cisco_ios_jinja2", 1, src)
	f := New()
	defer f.Close()

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	doc.Content = append([]byte(nil), src...) // re-send identical content
	publish, tiersPtr := capturePublish()
	final, err := f.DidChange(context.Background(), doc, publish)
	if err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	tiers := *tiersPtr
	if len(tiers) != 2 {
		t.Fatalf("expected 2 publishes, got %d: %+v", len(tiers), tiers)
	}
	tier1, tier2 := tiers[0], tiers[1]

	if !tieredFindByCode(tier1, "missing-}}") {
		t.Errorf("tier 1 missing tree diagnostic missing-}}: %+v", tier1)
	}
	if tieredFindByCode(tier1, "undefined-acl") {
		t.Errorf("tier 1 must not contain ref diagnostic (needs symbols.Index): %+v", tier1)
	}
	if !tieredFindByCode(tier2, "missing-}}") || !tieredFindByCode(tier2, "undefined-acl") {
		t.Errorf("tier 2 missing expected diagnostics: %+v", tier2)
	}
	if !setSubset(diagSet(tier1), diagSet(tier2)) {
		t.Errorf("tier 1 not subset of tier 2\ntier1: %+v\ntier2: %+v", tier1, tier2)
	}
	if !setEq(diagSet(final), diagSet(tier2)) {
		t.Errorf("DidChange return != tier 2\ngot:  %+v\nwant: %+v", final, tier2)
	}

	// Incremental parse of identical content must agree with a cold parse.
	f2 := New()
	defer f2.Close()
	cold, err := f2.DidOpen(context.Background(), document.New(doc.URI, "cisco_ios_jinja2", 1, src), nil)
	if err != nil {
		t.Fatalf("cold DidOpen: %v", err)
	}
	if !setEq(diagSet(final), diagSet(cold)) {
		t.Errorf("DidChange (identical content) != DidOpen (cold)\nincr: %+v\ncold: %+v", final, cold)
	}
}
