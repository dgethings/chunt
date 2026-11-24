package cisco_ios_jinja2

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features"
	"github.com/dgethings/chunt/internal/protocol"
)

// Workspace-mode tests: a directory of *.ios.j2 templates as ONE logical
// device config. See workspace.go for the model.

// wsSetup writes files (name -> content) into a fresh temp dir and returns
// the dir path.
func wsSetup(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// wsOpen opens one file of the dir through the feature (DidOpen), like the
// LSP server would, and returns the feature plus the returned diagnostics.
func wsOpen(t *testing.T, f *CiscoIOSFeature, dir, name, content string) []protocol.Diagnostic {
	t.Helper()
	doc := document.New("file://"+filepath.Join(dir, name), "cisco_ios_jinja2", 1, []byte(content))
	diags, err := f.DidOpen(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("DidOpen %s: %v", name, err)
	}
	return diags
}

// wsOpen2 is wsOpen with an explicit publish callback (for tests that
// inspect sibling publishes).
func wsOpen2(t *testing.T, f *CiscoIOSFeature, pub features.PublishFunc, dir, name, content string) []protocol.Diagnostic {
	t.Helper()
	doc := document.New("file://"+filepath.Join(dir, name), "cisco_ios_jinja2", 1, []byte(content))
	diags, err := f.DidOpen(context.Background(), doc, pub)
	if err != nil {
		t.Fatalf("DidOpen %s: %v", name, err)
	}
	return diags
}

// wsPublishLog records the last published set per URI and the publish call
// count.
type wsPublishLog struct {
	last  map[string][]protocol.Diagnostic
	calls int
}

func wsRecorder() (features.PublishFunc, *wsPublishLog) {
	l := &wsPublishLog{last: make(map[string][]protocol.Diagnostic)}
	return func(d *document.Document, diags []protocol.Diagnostic) {
		l.calls++
		l.last[d.URI] = diags
	}, l
}

func wsByCode(diags []protocol.Diagnostic, code string) []protocol.Diagnostic {
	var out []protocol.Diagnostic
	for _, d := range diags {
		if d.Code == code {
			out = append(out, d)
		}
	}
	return out
}

func wsCodeCount(diags []protocol.Diagnostic, code string) int { return len(wsByCode(diags, code)) }

// TestWorkspaceRouterIDConflictAcrossFiles is the canonical multi-file
// scenario: the same logical `router bgp 65001` section split over two
// files with CONFLICTING router-id values must surface an Error on the
// later definition (merge order = sorted filename), with
// relatedInformation pointing back into the FIRST file.
func TestWorkspaceRouterIDConflictAcrossFiles(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n router-id 1.1.1.1\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n router-id 2.2.2.2\n neighbor 10.0.0.2 remote-as 65002\n!\n",
	})
	f := New()
	defer f.Close()

	// Opening the FIRST file already sees the conflict: the sibling is
	// loaded as a background document and the merged logical section flags
	// the conflicting router-id anchored in the later file. With publish
	// nil, the returned set is a's own — but the sibling publish carries
	// the Error. Use the recorder to see both.
	pub, log := wsRecorder()
	docA := document.New("file://"+filepath.Join(dir, "a-base.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("router bgp 65001\n router-id 1.1.1.1\n!\n"))
	if _, err := f.DidOpen(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidOpen a: %v", err)
	}

	uriB := "file://" + filepath.Join(dir, "b-bgp.ios.j2")
	setB, ok := log.last[uriB]
	if !ok {
		t.Fatalf("no publish for background sibling b; publishes: %+v", log.last)
	}
	hits := wsByCode(setB, "redefined-setting")
	if len(hits) != 1 {
		t.Fatalf("want 1 redefined-setting in b's set, got %d: %+v", len(hits), setB)
	}
	d := hits[0]
	if d.Severity != protocol.SeverityError {
		t.Errorf("severity: got %d, want Error (1)", d.Severity)
	}
	if want := `router-id "2.2.2.2" replaces "1.1.1.1"`; !strings.Contains(d.Message, want) {
		t.Errorf("message %q missing %q", d.Message, want)
	}
	// Line 1 (0-based) of b = the ` router-id 2.2.2.2` command.
	if d.Range.Start.Line != 1 {
		t.Errorf("anchor line: got %d, want 1", d.Range.Start.Line)
	}
	// relatedInformation jumps into file a at its router-id line.
	if len(d.RelatedInformation) != 1 {
		t.Fatalf("relatedInformation: got %d entries, want 1", len(d.RelatedInformation))
	}
	rel := d.RelatedInformation[0]
	if rel.Location.URI != docA.URI {
		t.Errorf("related URI: got %q, want %q", rel.Location.URI, docA.URI)
	}
	if rel.Location.Range.Start.Line != 1 {
		t.Errorf("related line: got %d, want 1", rel.Location.Range.Start.Line)
	}
}

// TestWorkspaceDistinctSectionsNoConflict: different sections across files
// (`router ospf` in one, `router bgp` in another — even with the same
// process number) are distinct logical sections: allowed, no diagnostics.
func TestWorkspaceDistinctSectionsNoConflict(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-ospf.ios.j2": "router ospf 1\n router-id 1.1.1.1\n network 10.0.0.0 0.0.0.255 area 0\n!\n",
		"b-bgp.ios.j2":  "router bgp 1\n router-id 2.2.2.2\n neighbor 10.0.0.2 remote-as 65002\n!\n",
	})
	f := New()
	defer f.Close()

	pub, log := wsRecorder()
	diags := wsOpen(t, f, dir, "a-ospf.ios.j2", "router ospf 1\n router-id 1.1.1.1\n network 10.0.0.0 0.0.0.255 area 0\n!\n")
	_ = pub
	if got := wsCodeCount(diags, "redefined-setting"); got != 0 {
		t.Errorf("a: want 0 redefined-setting, got %d: %+v", got, wsByCode(diags, "redefined-setting"))
	}
	for uri, set := range log.last {
		if got := wsCodeCount(set, "redefined-setting") + wsCodeCount(set, "duplicate-command"); got != 0 {
			t.Errorf("%s: want no cross-file findings, got %+v", uri, set)
		}
	}
}

// TestWorkspaceSameSectionMergesSilently: the same section identity across
// files is a valid split. Disjoint commands under the same logical section
// produce NO duplicate-definition or duplicate-command diagnostics.
func TestWorkspaceSameSectionMergesSilently(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n neighbor 10.0.0.2 remote-as 65002\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n neighbor 10.0.0.3 remote-as 65003\n!\n",
	})
	f := New()
	defer f.Close()

	diags := wsOpen(t, f, dir, "a-base.ios.j2", "router bgp 65001\n neighbor 10.0.0.2 remote-as 65002\n!\n")
	for _, code := range []string{"redefined-setting", "duplicate-command", "duplicate-router"} {
		if got := wsCodeCount(diags, code); got != 0 {
			t.Errorf("a: want 0 %s, got %d", code, got)
		}
	}
	// b (background) must be clean too.
	_, log := wsRecorder()
	_ = log
}

// TestWorkspaceIdenticalCommandAcrossFiles: the exact same line under the
// same logical section in two files is a duplicate-command Warning on the
// later file (cross-file copy-paste).
func TestWorkspaceIdenticalCommandAcrossFiles(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n neighbor 10.0.0.2 remote-as 65002\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n neighbor 10.0.0.2 remote-as 65002\n!\n",
	})
	f := New()
	defer f.Close()

	pub, log := wsRecorder()
	wsOpen2(t, f, pub, dir, "a-base.ios.j2", "router bgp 65001\n neighbor 10.0.0.2 remote-as 65002\n!\n")
	uriB := "file://" + filepath.Join(dir, "b-bgp.ios.j2")
	setB, ok := log.last[uriB]
	if !ok {
		t.Fatalf("no publish for background sibling b")
	}
	hits := wsByCode(setB, "duplicate-command")
	if len(hits) != 1 {
		t.Fatalf("want 1 duplicate-command in b's set, got %d: %+v", len(hits), setB)
	}
	if hits[0].Severity != protocol.SeverityWarning {
		t.Errorf("severity: got %d, want Warning (2)", hits[0].Severity)
	}
	if hits[0].RelatedInformation[0].Location.URI != "file://"+filepath.Join(dir, "a-base.ios.j2") {
		t.Errorf("related URI should point at a: %+v", hits[0].RelatedInformation)
	}
}

// TestWorkspaceRefsResolveAcrossFiles: a reference in one file is satisfied
// by a definition in another (full cross-file scope). The historical
// single-file `undefined acl` false positive disappears.
func TestWorkspaceRefsResolveAcrossFiles(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-if.ios.j2":  "interface Gi0/0\n ip access-group MGMT in\n!\n",
		"b-acl.ios.j2": "ip access-list extended MGMT\n permit tcp 10.0.0.0 0.0.0.255 any\n!\n",
	})
	f := New()
	defer f.Close()

	diags := wsOpen(t, f, dir, "a-if.ios.j2", "interface Gi0/0\n ip access-group MGMT in\n!\n")
	if got := wsCodeCount(diags, "undefined-acl"); got != 0 {
		t.Errorf("want 0 undefined-acl (ACL lives in sibling), got %d: %+v", got, wsByCode(diags, "undefined-acl"))
	}
}

// TestWorkspaceUndefinedRefWhenMissingEverywhere: with no definition in ANY
// group file, the reference is still flagged — in the referencing file's
// published set.
func TestWorkspaceUndefinedRefWhenMissingEverywhere(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-if.ios.j2":    "interface Gi0/0\n ip access-group MGMT in\n!\n",
		"b-empty.ios.j2": "hostname r1\n",
	})
	f := New()
	defer f.Close()

	diags := wsOpen(t, f, dir, "a-if.ios.j2", "interface Gi0/0\n ip access-group MGMT in\n!\n")
	if got := wsCodeCount(diags, "undefined-acl"); got != 1 {
		t.Fatalf("want 1 undefined-acl, got %d: %+v", got, diags)
	}
}

// TestWorkspaceNegationClearsAcrossFiles: `no router-id` in a later file
// clears the tracked state from the earlier file; the re-add is fresh, not
// a conflict (remove-then-re-add across files).
func TestWorkspaceNegationClearsAcrossFiles(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n router-id 1.1.1.1\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n no router-id\n router-id 2.2.2.2\n!\n",
	})
	f := New()
	defer f.Close()

	pub, log := wsRecorder()
	wsOpen(t, f, dir, "a-base.ios.j2", "router bgp 65001\n router-id 1.1.1.1\n!\n")
	_ = pub
	for uri, set := range log.last {
		if got := wsCodeCount(set, "redefined-setting"); got != 0 {
			t.Errorf("%s: want 0 redefined-setting after cross-file no/reset, got %d: %+v", uri, got, set)
		}
	}
}

// TestWorkspaceHostnameAcrossFiles: top-level (root scope) scalars merge
// across files too — two different hostnames in two files conflict.
func TestWorkspaceHostnameAcrossFiles(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "hostname r1\n",
		"b-host.ios.j2": "hostname r2\n",
	})
	f := New()
	defer f.Close()

	pub, log := wsRecorder()
	wsOpen2(t, f, pub, dir, "a-base.ios.j2", "hostname r1\n")
	uriB := "file://" + filepath.Join(dir, "b-host.ios.j2")
	setB := log.last[uriB]
	if got := wsCodeCount(setB, "redefined-setting"); got != 1 {
		t.Fatalf("want 1 redefined-setting in b, got %d: %+v", got, setB)
	}
}

// TestWorkspaceSiblingRepublishOnChange: editing one open file must
// republish the OTHER open file when its set moved — here, defining the ACL
// in b clears a's undefined-acl without a itself being touched.
func TestWorkspaceSiblingRepublishOnChange(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-if.ios.j2":  "interface Gi0/0\n ip access-group MGMT in\n!\n",
		"b-acl.ios.j2": "hostname r1\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	docA := document.New("file://"+filepath.Join(dir, "a-if.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("interface Gi0/0\n ip access-group MGMT in\n!\n"))
	if _, err := f.DidOpen(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidOpen a: %v", err)
	}
	if got := wsCodeCount(log.last[docA.URI], "undefined-acl"); got != 1 {
		t.Fatalf("precondition: a should have 1 undefined-acl, got %d", got)
	}

	docB := document.New("file://"+filepath.Join(dir, "b-acl.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("hostname r1\n"))
	if _, err := f.DidOpen(context.Background(), docB, pub); err != nil {
		t.Fatalf("DidOpen b: %v", err)
	}
	docB.Content = []byte("ip access-list extended MGMT\n permit tcp 10.0.0.0 0.0.0.255 any\n!\n")
	if _, err := f.DidChange(context.Background(), docB, pub); err != nil {
		t.Fatalf("DidChange b: %v", err)
	}

	// a was never edited, but its set was recomputed and republished clean.
	setA, ok := log.last[docA.URI]
	if !ok {
		t.Fatal("no publish recorded for a")
	}
	if got := wsCodeCount(setA, "undefined-acl"); got != 0 {
		t.Errorf("a still shows undefined-acl after b defined the ACL: %+v", setA)
	}
}

// TestWorkspaceOutofBandDiskEdit: a sibling changed on disk (another
// editor/tool) is reloaded on the next keystroke in any open file of the
// group (mtime-driven rescan).
func TestWorkspaceOutofBandDiskEdit(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n router-id 1.1.1.1\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n router-id 2.2.2.2\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	docA := document.New("file://"+filepath.Join(dir, "a-base.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("router bgp 65001\n router-id 1.1.1.1\n!\n"))
	if _, err := f.DidOpen(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidOpen a: %v", err)
	}
	uriB := "file://" + filepath.Join(dir, "b-bgp.ios.j2")
	if got := wsCodeCount(log.last[uriB], "redefined-setting"); got != 1 {
		t.Fatalf("precondition: conflict in b, got %d", got)
	}

	// Fix b on disk; nudge mtime into the future so the change is visible
	// even on coarse-grained filesystems.
	newB := "router bgp 65001\n router-id 1.1.1.1\n neighbor 10.0.0.2 remote-as 65002\n!\n"
	pathB := filepath.Join(dir, "b-bgp.ios.j2")
	if err := os.WriteFile(pathB, []byte(newB), 0o644); err != nil {
		t.Fatalf("rewrite b: %v", err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(pathB, future, future); err != nil {
		t.Fatalf("chtimes b: %v", err)
	}

	// A trivial DidChange on the OPEN file triggers the group rescan.
	if _, err := f.DidChange(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidChange a: %v", err)
	}
	if got := wsCodeCount(log.last[uriB], "redefined-setting"); got != 0 {
		t.Errorf("conflict survived the out-of-band fix: %+v", log.last[uriB])
	}
}

// TestWorkspaceEvictionClearsBackgroundPublishes: closing the last open
// document of a directory tears the group down and publishes an empty set
// for background files that had diagnostics, so the editor's Problems panel
// does not keep showing stale cross-file findings for untracked files.
func TestWorkspaceEvictionClearsBackgroundPublishes(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n router-id 1.1.1.1\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n router-id 2.2.2.2\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	docA := document.New("file://"+filepath.Join(dir, "a-base.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("router bgp 65001\n router-id 1.1.1.1\n!\n"))
	if _, err := f.DidOpen(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidOpen a: %v", err)
	}
	uriB := "file://" + filepath.Join(dir, "b-bgp.ios.j2")
	if len(log.last[uriB]) == 0 {
		t.Fatal("precondition: b should have published diagnostics")
	}

	if err := f.DidClose(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidClose a: %v", err)
	}
	setB, ok := log.last[uriB]
	if !ok || len(setB) != 0 {
		t.Errorf("b not cleared on eviction: ok=%v set=%+v", ok, setB)
	}
	if _, still := f.groups[filepath.Dir(filepath.Join(dir, "a-base.ios.j2"))]; still {
		t.Errorf("group not evicted: %+v", f.groups)
	}
	if _, stillTracked := f.trees[uriB]; stillTracked {
		t.Error("background tree not released on eviction")
	}
}

// TestWorkspaceDidCloseReloadsFromDisk: when one of several open documents
// closes, the file remains part of the device config — it is reloaded from
// disk as a background document and the remaining files keep their
// cross-file diagnostics.
func TestWorkspaceDidCloseReloadsFromDisk(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n router-id 1.1.1.1\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n router-id 2.2.2.2\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	docA := document.New("file://"+filepath.Join(dir, "a-base.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("router bgp 65001\n router-id 1.1.1.1\n!\n"))
	docB := document.New("file://"+filepath.Join(dir, "b-bgp.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("router bgp 65001\n router-id 2.2.2.2\n!\n"))
	for _, d := range []*document.Document{docA, docB} {
		if _, err := f.DidOpen(context.Background(), d, pub); err != nil {
			t.Fatalf("DidOpen: %v", err)
		}
	}
	if err := f.DidClose(context.Background(), docB, pub); err != nil {
		t.Fatalf("DidClose b: %v", err)
	}
	// b was open (not published as background before close, editor owns its
	// clearing) but its on-disk content still feeds the group: the conflict
	// for a… is anchored on b, which is now background — so b gets a
	// published set again via the post-close group refresh.
	setB := log.last[docB.URI]
	if got := wsCodeCount(setB, "redefined-setting"); got != 1 {
		t.Errorf("want 1 redefined-setting re-published for b after close, got %d: %+v", got, setB)
	}
}

// TestWorkspaceNonFileURIsRunSolo: untitled/non-file documents must behave
// exactly like the historical single-file world (no group, no sibling
// scan).
func TestWorkspaceNonFileURIsRunSolo(t *testing.T) {
	f := New()
	defer f.Close()

	doc := document.New("untitled:Untitled-1", "cisco_ios_jinja2", 1,
		[]byte("hostname r1\n"))
	diags, err := f.DidOpen(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("solo untitled doc should be clean, got %+v", diags)
	}
	if len(f.groups) != 0 {
		t.Errorf("no group should exist for untitled URIs, got %+v", f.groups)
	}
}

// TestWorkspaceOutofBandNewFile: a NEW sibling template created on disk
// while another file is open joins the logical config on the next
// keystroke (rescan discovers it; the merge order is rebuilt — regression
// pin for the rescan path that once lost its rebuildOrder call).
func TestWorkspaceOutofBandNewFile(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n router-id 1.1.1.1\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	docA := document.New("file://"+filepath.Join(dir, "a-base.ios.j2"), "cisco_ios_jinja2", 1,
		[]byte("router bgp 65001\n router-id 1.1.1.1\n!\n"))
	if _, err := f.DidOpen(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidOpen a: %v", err)
	}
	if got := wsCodeCount(log.last[docA.URI], "redefined-setting"); got != 0 {
		t.Fatalf("precondition: a clean, got %d", got)
	}

	// Someone creates the conflicting template on disk.
	pathC := filepath.Join(dir, "c-new.ios.j2")
	if err := os.WriteFile(pathC, []byte("router bgp 65001\n router-id 9.9.9.9\n!\n"), 0o644); err != nil {
		t.Fatalf("write c: %v", err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(pathC, future, future); err != nil {
		t.Fatalf("chtimes c: %v", err)
	}

	if _, err := f.DidChange(context.Background(), docA, pub); err != nil {
		t.Fatalf("DidChange a: %v", err)
	}
	uriC := "file://" + pathC
	setC, ok := log.last[uriC]
	if !ok {
		t.Fatal("new sibling was not discovered/published")
	}
	if got := wsCodeCount(setC, "redefined-setting"); got != 1 {
		t.Errorf("want 1 redefined-setting in new sibling's set, got %d: %+v", got, setC)
	}
}

// TestWorkspaceCompanionWarningOnFirstDefinition: a cross-file conflict
// participates in BOTH files — the Error anchors on the later definition
// (b), and a companion superseded-setting Warning anchors on the FIRST
// definition (a), naming the replacing value and the file it moved to,
// with relatedInformation forward to it. Checking/opening the first file
// alone therefore surfaces the conflict too.
func TestWorkspaceCompanionWarningOnFirstDefinition(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2": "router bgp 65001\n router-id 1.1.1.1\n!\n",
		"b-bgp.ios.j2":  "router bgp 65001\n router-id 2.2.2.2\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	uriA := "file://" + filepath.Join(dir, "a-base.ios.j2")
	uriB := "file://" + filepath.Join(dir, "b-bgp.ios.j2")
	wsOpen2(t, f, pub, dir, "a-base.ios.j2", "router bgp 65001\n router-id 1.1.1.1\n!\n")

	// b: the Error on the later definition (unchanged contract).
	setB := log.last[uriB]
	if got := wsCodeCount(setB, "redefined-setting"); got != 1 {
		t.Fatalf("b: want 1 redefined-setting Error, got %d: %+v", got, setB)
	}
	// a: the companion Warning on the first definition.
	setA := log.last[uriA]
	hits := wsByCode(setA, "superseded-setting")
	if len(hits) != 1 {
		t.Fatalf("a: want 1 superseded-setting Warning, got %d: %+v", len(hits), setA)
	}
	w := hits[0]
	if w.Severity != protocol.SeverityWarning {
		t.Errorf("companion severity: got %d, want Warning (2)", w.Severity)
	}
	if w.Range.Start.Line != 1 {
		t.Errorf("companion anchor line: got %d, want 1 (a's router-id)", w.Range.Start.Line)
	}
	if want := `router-id "1.1.1.1" is superseded by "2.2.2.2" defined later in b-bgp.ios.j2`; !strings.Contains(w.Message, want) {
		t.Errorf("companion message %q missing %q", w.Message, want)
	}
	if len(w.RelatedInformation) != 1 || w.RelatedInformation[0].Location.URI != uriB {
		t.Fatalf("companion relatedInformation should jump to b: %+v", w.RelatedInformation)
	}
	if w.RelatedInformation[0].Message != "later definition here" {
		t.Errorf("companion related message: got %q", w.RelatedInformation[0].Message)
	}
	// a must NOT carry the Error itself (it anchors on b only), and b must
	// not carry the companion.
	if got := wsCodeCount(setA, "redefined-setting"); got != 0 {
		t.Errorf("a: want 0 redefined-setting, got %d", got)
	}
	if got := wsCodeCount(setB, "superseded-setting"); got != 0 {
		t.Errorf("b: want 0 superseded-setting, got %d", got)
	}
}

// TestWorkspaceCompanionNotEmittedInFile: the companion Warning is a
// cross-file affordance. Same-file conflicts keep the established
// single-anchor convention (Error on the later definition only).
func TestWorkspaceCompanionNotEmittedInFile(t *testing.T) {
	f := New()
	defer f.Close()

	doc := document.New("untitled:Untitled-1", "cisco_ios_jinja2", 1,
		[]byte("router bgp 65001\n router-id 1.1.1.1\n router-id 2.2.2.2\n!\n"))
	diags, err := f.DidOpen(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if got := wsCodeCount(diags, "redefined-setting"); got != 1 {
		t.Errorf("want 1 redefined-setting Error, got %d: %+v", got, diags)
	}
	if got := wsCodeCount(diags, "superseded-setting"); got != 0 {
		t.Errorf("in-file conflict must not emit a companion, got %d: %+v", got, wsByCode(diags, "superseded-setting"))
	}
}

// TestWorkspaceRegistryScalarAcrossFiles (chunt-otg.1): a registry scalar
// (description) redefined in a second file on the same logical interface
// follows the cross-file contract — redefined-setting Error on the later
// definition in b, companion superseded-setting Warning on the first in a.
func TestWorkspaceRegistryScalarAcrossFiles(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2":  "interface Gi0/0\n description primary uplink\n!\n",
		"b-iface.ios.j2": "interface Gi0/0\n description backup uplink\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	uriA := "file://" + filepath.Join(dir, "a-base.ios.j2")
	uriB := "file://" + filepath.Join(dir, "b-iface.ios.j2")
	wsOpen2(t, f, pub, dir, "a-base.ios.j2", "interface Gi0/0\n description primary uplink\n!\n")

	// b: the Error on the later definition, with the registry's replacement
	// wording and the first definition in a as relatedInformation.
	setB := log.last[uriB]
	if got := wsCodeCount(setB, "redefined-setting"); got != 1 {
		t.Fatalf("b: want 1 redefined-setting Error, got %d: %+v", got, setB)
	}
	e := wsByCode(setB, "redefined-setting")[0]
	if want := `description "backup uplink" replaces "primary uplink"`; !strings.Contains(e.Message, want) {
		t.Errorf("b: message %q missing %q", e.Message, want)
	}
	if e.Severity != protocol.SeverityError {
		t.Errorf("b: severity got %d, want Error (1)", e.Severity)
	}
	if len(e.RelatedInformation) != 1 || e.RelatedInformation[0].Location.URI != uriA {
		t.Fatalf("b: relatedInformation should jump to a: %+v", e.RelatedInformation)
	}

	// a: the companion Warning on the first definition.
	setA := log.last[uriA]
	hits := wsByCode(setA, "superseded-setting")
	if len(hits) != 1 {
		t.Fatalf("a: want 1 superseded-setting Warning, got %d: %+v", len(hits), setA)
	}
	if want := `description "primary uplink" is superseded by "backup uplink" defined later in b-iface.ios.j2`; !strings.Contains(hits[0].Message, want) {
		t.Errorf("a: companion message %q missing %q", hits[0].Message, want)
	}
	if got := wsCodeCount(setA, "redefined-setting"); got != 0 {
		t.Errorf("a: want 0 redefined-setting, got %d", got)
	}
}

// TestWorkspaceUnusedAcrossFiles (chunt-otg.23): a definition referenced in
// ANY file of the directory group counts as used, while a definition no
// file references still hints — anchored in its own file's publish.
func TestWorkspaceUnusedAcrossFiles(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2":  "ip access-list extended USED\n permit ip any any\n!\nip access-list extended ORPHAN\n permit ip any any\n!\n",
		"b-iface.ios.j2": "interface Gi0/0\n ip access-group USED in\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	uriA := "file://" + filepath.Join(dir, "a-base.ios.j2")
	wsOpen2(t, f, pub, dir, "a-base.ios.j2", "ip access-list extended USED\n permit ip any any\n!\nip access-list extended ORPHAN\n permit ip any any\n!\n")
	wsOpen2(t, f, pub, dir, "b-iface.ios.j2", "interface Gi0/0\n ip access-group USED in\n!\n")

	setA := log.last[uriA]
	if got := wsCodeCount(setA, "unused-acl"); got != 1 {
		t.Fatalf("a: want exactly 1 unused-acl (ORPHAN only; USED is referenced from b), got %d: %+v", got, setA)
	}
}

// TestWorkspaceNewKindsParity (chunt-otg.24): the new def/ref kinds resolve
// in directory mode — a vrf defined in a satisfies `vrf forwarding` in b
// (no undefined-vrf in b), a cross-file scalar conflict for a new setting
// (standby priority) carries the companion superseded-setting Warning in
// a, and doc-local duplicates still flag.
func TestWorkspaceNewKindsParity(t *testing.T) {
	dir := wsSetup(t, map[string]string{
		"a-base.ios.j2":  "vrf definition BLUE\n!\ninterface Gi0/0\n standby 1 priority 110\n!\n",
		"b-iface.ios.j2": "interface Gi0/0\n vrf forwarding BLUE\n standby 1 priority 120\n!\n",
	})
	f := New()
	defer f.Close()
	pub, log := wsRecorder()

	uriA := "file://" + filepath.Join(dir, "a-base.ios.j2")
	uriB := "file://" + filepath.Join(dir, "b-iface.ios.j2")
	wsOpen2(t, f, pub, dir, "a-base.ios.j2", "vrf definition BLUE\n!\ninterface Gi0/0\n standby 1 priority 110\n!\n")
	wsOpen2(t, f, pub, dir, "b-iface.ios.j2", "interface Gi0/0\n vrf forwarding BLUE\n standby 1 priority 120\n!\n")

	// b: the BLUE vrf from a satisfies the vrf forwarding reference, and
	// the standby priority conflict carries the Error.
	setB := log.last[uriB]
	if got := wsCodeCount(setB, "undefined-vrf"); got != 0 {
		t.Fatalf("b: vrf BLUE from a must resolve, got %d undefined-vrf: %+v", got, setB)
	}
	if got := wsCodeCount(setB, "redefined-setting"); got != 1 {
		t.Fatalf("b: want 1 redefined-setting (standby priority), got %d: %+v", got, setB)
	}
	// a: the companion superseded-setting Warning for the cross-file scalar.
	setA := log.last[uriA]
	if got := wsCodeCount(setA, "superseded-setting"); got != 1 {
		t.Fatalf("a: want 1 superseded-setting companion, got %d: %+v", got, setA)
	}
}
