package keyword_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/keyword"
)

func TestSetLookup(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "clock", Section: "config-if"},
		{Keyword: "hostname", Section: "config"},
	}
	s := keyword.NewSet(kws)

	kw, ok := s.Lookup("clock")
	if !ok {
		t.Fatal("expected to find clock")
	}
	if kw.Keyword != "clock" {
		t.Errorf("expected clock, got %q", kw.Keyword)
	}
	if kw.Section != "config-if" {
		t.Errorf("expected section config-if, got %q", kw.Section)
	}

	if _, ok := s.Lookup("nope"); ok {
		t.Error("expected Lookup to return false for a missing keyword")
	}
}

func TestSetInSection_ExactMatch(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "clock", Section: "config-if"},
		{Keyword: "hostname", Section: "config"},
	}
	s := keyword.NewSet(kws)
	got := s.InSection("config-if")
	if len(got) != 1 {
		t.Fatalf("expected 1 keyword for config-if, got %d", len(got))
	}
	if got[0].Keyword != "clock" {
		t.Errorf("expected clock, got %q", got[0].Keyword)
	}
}

func TestSetInSection_EmptySectionIsUniversal(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "do", Section: ""},
	}
	s := keyword.NewSet(kws)
	for _, section := range []string{"config", "config-if", "anything"} {
		got := s.InSection(section)
		if len(got) != 1 {
			t.Errorf("InSection(%q): expected universal keyword, got %d results", section, len(got))
			continue
		}
		if got[0].Keyword != "do" {
			t.Errorf("InSection(%q): expected do, got %q", section, got[0].Keyword)
		}
	}
}

func TestSetInSection_ProductionDataHasUniversalKeywords(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "hostname", Section: "config"},
		{Keyword: "do", Section: ""},
		{Keyword: "clock", Section: "config-if"},
	}
	s := keyword.NewSet(kws)
	got := s.InSection("config")
	labels := map[string]bool{}
	for _, kw := range got {
		labels[kw.Keyword] = true
	}
	if !labels["hostname"] {
		t.Errorf("expected config keyword hostname in config section")
	}
	if !labels["do"] {
		t.Errorf("expected empty-Section keyword do to appear in config section")
	}
	if labels["clock"] {
		t.Errorf("config-if keyword clock must not appear in config section")
	}
}

func TestSetLookupOverwrite(t *testing.T) {
	// When two entries share the same Keyword field, the last one wins in the
	// by-name index built by NewSet.
	kws := []keyword.Keyword{
		{Keyword: "dup", Section: "config"},
		{Keyword: "dup", Section: "config-if"},
	}
	s := keyword.NewSet(kws)

	kw, ok := s.Lookup("dup")
	if !ok {
		t.Fatal("expected to find dup")
	}
	if kw.Section != "config-if" {
		t.Errorf("expected last entry to win (config-if), got %q", kw.Section)
	}
}

func TestIsValidInSection(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "foo", Section: "config-if"},
		{Keyword: "bar", Section: ""}, // global
	}
	s := keyword.NewSet(kws)

	cases := []struct {
		name    string
		section string
		want    bool
	}{
		{"foo", "config-if", true},      // exact section match
		{"foo", "config-router", false}, // known but wrong section
		{"bar", "anything", true},       // global valid everywhere
		{"baz", "config", false},        // unknown keyword
	}
	for _, tc := range cases {
		got := s.IsValidInSection(tc.name, tc.section)
		if got != tc.want {
			t.Errorf("IsValidInSection(%q, %q) = %v, want %v", tc.name, tc.section, got, tc.want)
		}
	}
}

// TestSetAddValidSections covers chunt-vzy: AddValidSections extends the
// IsValidInSection index for canonical commands the generated DB
// mis-registers, WITHOUT touching the keyword's canonical Lookup/
// LookupSection record (so hover, completion, and diagnostic messages are
// unchanged).
func TestSetAddValidSections(t *testing.T) {
	// Include keywords under config-router and its child config-router-af so
	// the SectionTree models the hierarchy (otherwise the ancestry check
	// below cannot relate the child to the parent).
	kws := []keyword.Keyword{
		{Keyword: "network", Section: "config-ipv6-pmipv6-domain-mn"},
		{Keyword: "router-id", Section: "config-l2vpn"},
		{Keyword: "passive-interface", Section: "config-router"},
		{Keyword: "aggregate-address", Section: "config-router-af"},
	}
	s := keyword.NewSet(kws)

	// Before the overlay, both are false in config-router.
	if s.IsValidInSection("network", "config-router") {
		t.Fatal("precondition: network should not be valid in config-router before overlay")
	}

	s.AddValidSections("network", "config-router")
	s.AddValidSections("router-id", "config-router")

	// After the overlay, both are valid in config-router (and its children via
	// the existing ancestry check).
	if !s.IsValidInSection("network", "config-router") {
		t.Errorf("network should now be valid in config-router")
	}
	if !s.IsValidInSection("router-id", "config-router") {
		t.Errorf("router-id should now be valid in config-router")
	}
	if !s.IsValidInSection("network", "config-router-af") {
		t.Errorf("network should inherit into config-router-af (child section)")
	}

	// The canonical record is unchanged: LookupSection still reports the DB's
	// first section (so a wrong-section message, when one IS emitted for a
	// genuinely-wrong section, still names the documented home).
	if got := s.LookupSection("network"); got != "config-ipv6-pmipv6-domain-mn" {
		t.Errorf("LookupSection(network) = %q, want config-ipv6-pmipv6-domain-mn (overlay must not change canonical record)", got)
	}
	if kw, ok := s.Lookup("network"); !ok || kw.Section != "config-ipv6-pmipv6-domain-mn" {
		t.Errorf("Lookup(network) canonical record changed: %+v", kw)
	}

	// Still wrong in a genuinely-unrelated section (overlay is surgical).
	if s.IsValidInSection("network", "config-if") {
		t.Errorf("network should still be invalid in config-if")
	}

	// Adding to a keyword unknown to the DB does not panic and records the
	// validity (Lookup still misses, but IsValidInSection answers).
	s.AddValidSections("brand-new", "config-router")
	if !s.IsValidInSection("brand-new", "config-router") {
		t.Errorf("AddValidSections on an unknown keyword should still be queryable")
	}
	if _, ok := s.Lookup("brand-new"); ok {
		t.Errorf("Lookup(brand-new) should still miss (overlay does not create a record)")
	}
}

func TestLookupSection(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "foo", Section: "config-if"},
		{Keyword: "bar", Section: ""}, // global only
		{Keyword: "baz", Section: "config-router"},
	}
	s := keyword.NewSet(kws)

	if got := s.LookupSection("foo"); got != "config-if" {
		t.Errorf("LookupSection(foo) = %q, want config-if", got)
	}
	if got := s.LookupSection("baz"); got != "config-router" {
		t.Errorf("LookupSection(baz) = %q, want config-router", got)
	}
	// bar is global only -> no non-empty section to report.
	if got := s.LookupSection("bar"); got != "" {
		t.Errorf("LookupSection(bar) = %q, want \"\"", got)
	}
	// Unknown keyword.
	if got := s.LookupSection("missing"); got != "" {
		t.Errorf("LookupSection(missing) = %q, want \"\"", got)
	}
}

// TestIsValidInSection_Ancestry covers B4 (chunt-mpc): a keyword documented for
// a section is also valid in that section's DESCENDANTS (a config-if keyword is
// valid in config-if-atm-range), but the root "config" is NOT inherited into
// sub-modes (a global-config keyword such as hostname stays invalid inside an
// interface or router section).
func TestIsValidInSection_Ancestry(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "speed", Section: "config-if"},             // parent-section keyword
		{Keyword: "atm-cmd", Section: "config-if-atm-range"}, // forces the child into the tree
		{Keyword: "hostname", Section: "config"},             // global-config (root) keyword
		{Keyword: "do", Section: ""},                         // truly universal
	}
	s := keyword.NewSet(kws)

	cases := []struct {
		name    string
		kw      string
		section string
		want    bool
	}{
		// B4 ancestry: a config-if keyword is valid in its child config-if-atm-range.
		{"speed in child config-if-atm-range", "speed", "config-if-atm-range", true},
		{"speed in config-if (exact)", "speed", "config-if", true},
		{"speed in unrelated config-router", "speed", "config-router", false},
		// "config" (root) is NOT inherited: hostname is invalid in sub-modes...
		{"hostname in config-if", "hostname", "config-if", false},
		{"hostname in config-router", "hostname", "config-router", false},
		// ...but valid at the top level.
		{"hostname in config", "hostname", "config", true},
		// a universal ("") keyword is valid everywhere.
		{"do in config-if-atm-range", "do", "config-if-atm-range", true},
		{"do in config-router", "do", "config-router", true},
	}
	for _, tc := range cases {
		got := s.IsValidInSection(tc.kw, tc.section)
		if got != tc.want {
			t.Errorf("IsValidInSection(%q, %q) = %v, want %v", tc.kw, tc.section, got, tc.want)
		}
	}
}

// TestLookupSection_MatchesLinearScan is the chunt-4qw regression guard for
// the O(1) map-based LookupSection: it must return exactly what the original
// linear scan returned — the first non-empty Section in document order —
// across duplicates, multi-section entries, trailing-empty entries, and
// unknown names.
func TestLookupSection_MatchesLinearScan(t *testing.T) {
	kws := []keyword.Keyword{
		{Keyword: "dup", Section: "config-if"},
		{Keyword: "dup", Section: "config-router"}, // later duplicate must NOT win
		{Keyword: "shared", Section: ""},
		{Keyword: "shared", Section: "config"},
		{Keyword: "only-global", Section: ""},
		{Keyword: "solo", Section: "config-vlan"},
		{Keyword: "trailing-empty", Section: "config-line"},
		{Keyword: "trailing-empty", Section: ""}, // later empty must NOT clear the value
	}
	s := keyword.NewSet(kws)
	bruteForce := func(name string) string { // the pre-refactor linear scan
		for _, kw := range kws {
			if kw.Keyword == name && kw.Section != "" {
				return kw.Section
			}
		}
		return ""
	}
	for _, name := range []string{"dup", "shared", "only-global", "solo", "trailing-empty", "missing"} {
		want := bruteForce(name)
		if got := s.LookupSection(name); got != want {
			t.Errorf("LookupSection(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestInSection_NoAllocations proves InSection returns a precomputed, shared
// slice: it allocates 0 times per call (chunt-4qw), so it is safe to call on
// every completion request.
func TestInSection_NoAllocations(t *testing.T) {
	s := keyword.NewSet([]keyword.Keyword{
		{Keyword: "global", Section: ""},
		{Keyword: "if-cmd", Section: "config-if"},
		{Keyword: "rtr-cmd", Section: "config-router"},
	})
	for _, section := range []string{"config-if", "config-router", "does-not-exist", ""} {
		allocs := testing.AllocsPerRun(100, func() { _ = s.InSection(section) })
		if allocs != 0 {
			t.Errorf("InSection(%q): %v allocs/call, want 0", section, allocs)
		}
	}
}
