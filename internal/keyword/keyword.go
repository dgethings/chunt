package keyword

import "github.com/dgethings/chunt/internal/protocol"

type Keyword struct {
	Keyword string
	Description
	Section      string
	Snippets     []string
	Defaults     string
	EnterMode    string
	EnterModeArg int // 0 = none; otherwise the 1-based positional arg index that refines the mode (e.g. router bgp -> 1)
	MinVersion   string
	MaxVersion   string
	History      CommandHistory
	Usage        UsageGuideline
	Examples     Examples
	DeviceTypes  []string
}

// CommandHistory records the release that introduced (or last modified) a
// command, scraped from the Cisco "Command History" table.
type CommandHistory struct {
	Release      string
	Modification string
}

// UsageGuideline holds the prose scraped from the "Usage Guidelines" and any
// accompanying note for a command.
type UsageGuideline struct {
	Preamble string
	Note     string
}

// Examples holds the preamble and code block scraped from the "Examples"
// section of a command page.
type Examples struct {
	Preamble string
	Code     string
}

type Description struct {
	Format protocol.MarkupKind
	Value  string
}

type Keywords []Keyword

// Set provides O(1) keyword lookups by name and by section. Build it once at
// startup via NewSet from the full keyword slice.
type Set struct {
	byName        map[string]Keyword
	bySection     map[string][]Keyword
	byNameSection map[string]map[string]bool
	tree          *SectionTree
	all           []Keyword
	// inSectionCache precomputes, per section, the global keywords (Section
	// "") folded together with that section's keywords, so InSection is O(1)
	// and allocation-free. The cached slices are shared and MUST NOT be
	// mutated by callers (documented on InSection). Built once in NewSet
	// (chunt-4qw).
	inSectionCache map[string][]Keyword
	// byNameFirstSection maps a keyword name to the first non-empty Section
	// encountered for it (document order), so LookupSection is an O(1) map
	// read instead of a linear scan over all entries (chunt-4qw).
	byNameFirstSection map[string]string
}

// NewSet builds a Set from a keyword slice, indexing by name and section in a
// single pass. Keywords whose Section is "" are global and are returned by
// every InSection query. When two entries share the same Keyword field, the
// last one wins in the by-name index.
func NewSet(kws []Keyword) *Set {
	s := &Set{
		byName:    make(map[string]Keyword, len(kws)),
		bySection: make(map[string][]Keyword),
		all:       kws,
	}
	s.byNameFirstSection = make(map[string]string, len(kws))
	for _, kw := range kws {
		s.byName[kw.Keyword] = kw
		s.bySection[kw.Section] = append(s.bySection[kw.Section], kw)
		// Record the first non-empty section per name in document order —
		// exactly what LookupSection returns (chunt-4qw).
		if kw.Section != "" {
			if _, ok := s.byNameFirstSection[kw.Keyword]; !ok {
				s.byNameFirstSection[kw.Keyword] = kw.Section
			}
		}
	}

	// Build the by-name-section index: for each keyword name, the set of
	// sections it is valid in (including "" for global keywords). This lets
	// IsValidInSection answer in O(1) without scanning the keyword list.
	s.byNameSection = make(map[string]map[string]bool)
	for _, kw := range kws {
		if s.byNameSection[kw.Keyword] == nil {
			s.byNameSection[kw.Keyword] = make(map[string]bool)
		}
		s.byNameSection[kw.Keyword][kw.Section] = true
	}

	// Build the section hierarchy tree from all known sections.
	var sections []string
	for sec := range s.bySection {
		if sec != "" {
			sections = append(sections, sec)
		}
	}
	s.tree = BuildSectionTree(sections)

	// Precompute the per-section InSection slices once (globals folded into
	// every section) so InSection returns a shared, allocation-free slice
	// (chunt-4qw).
	globals := s.bySection[""]
	s.inSectionCache = make(map[string][]Keyword, len(s.bySection))
	for sec, secKws := range s.bySection {
		if sec == "" {
			s.inSectionCache[""] = append([]Keyword(nil), globals...)
			continue
		}
		combined := make([]Keyword, 0, len(globals)+len(secKws))
		combined = append(combined, globals...)
		combined = append(combined, secKws...)
		s.inSectionCache[sec] = combined
	}

	return s
}

func (s *Set) Lookup(name string) (Keyword, bool) {
	kw, ok := s.byName[name]
	return kw, ok
}

// IsValidInSection returns true if the keyword name is known AND valid in the
// given section. A keyword is valid in `section` when it is global (Section ==
// ""), has an exact entry for `section`, OR is valid in an ANCESTOR of `section`
// (a keyword documented for `config-if` is also valid in its sub-modes such as
// `config-if-atm-range`, which the grammar models as a child section). Returns
// false for unknown keywords.
//
// The ancestry check consults the SectionTree; for a `section` that the tree
// does not know at all (no keyword in the DB references it), callers should
// first collapse it to its nearest known ancestor (see SectionTree.NearestKnown)
// — IsValidInSection alone cannot relate an unknown section to its parents
// (B4/B5, chunt-mpc).
func (s *Set) IsValidInSection(name, section string) bool {
	sections, ok := s.byNameSection[name]
	if !ok {
		return false
	}
	if sections[""] || sections[section] {
		return true
	}
	for validSec := range sections {
		// Inherit a keyword into child sub-modes (a config-if keyword is valid in
		// config-if-atm-range), but NOT from the root: a "config" (global config
		// mode) keyword such as `hostname` is valid only at the top level, not
		// inside sub-modes, so both the empty-string and "config" sections are
		// excluded from the ancestry grant. Without this exclusion, `hostname`
		// inside `router bgp` would stop being flagged (chunt-mpc).
		if validSec != "" && validSec != "config" && s.tree.IsAncestor(validSec, section) {
			return true
		}
	}
	return false
}

// LookupSection returns the first non-empty Section for the given keyword
// name, or "" if the keyword is unknown or only global. Used to build
// diagnostic messages that point the user at where the keyword does belong.
// O(1) map read over byNameFirstSection (chunt-4qw).
func (s *Set) LookupSection(name string) string {
	return s.byNameFirstSection[name]
}

// AddValidSections marks name as valid in each of the listed sections, in
// addition to whatever the generated keyword data already records. It ONLY
// extends the byNameSection index consulted by IsValidInSection — Lookup,
// LookupSection, InSection and completion are unchanged — so it suppresses
// wrong-section false positives for canonical commands the generated DB
// registers under an obscure (or no) section, without altering their canonical
// hover/completion record or diagnostic message.
//
// This is the chunt-side seam for the generated keyword DB's gaps (e.g.
// `network` and `router-id` are canonical router-process commands the DB only
// registers under IPv6-PMIPv6 / L2VPN sections); the deeper fix lives in the
// external keyword generator. See chunt-vzy.
func (s *Set) AddValidSections(name string, sections ...string) {
	if _, ok := s.byNameSection[name]; !ok {
		s.byNameSection[name] = make(map[string]bool)
	}
	for _, sec := range sections {
		s.byNameSection[name][sec] = true
	}
}

// InSection returns the keywords valid in section: the global keywords
// (Section "") plus the keywords whose Section is exactly section. The
// returned slice is SHARED across calls and MUST NOT be mutated by the
// caller — it is precomputed once in NewSet for O(1), allocation-free access
// (chunt-4qw). For a section with no dedicated keywords, only the globals
// are returned (matching the previous append(globals, nil) behavior).
func (s *Set) InSection(section string) []Keyword {
	if cached, ok := s.inSectionCache[section]; ok {
		return cached
	}
	return s.inSectionCache[""] // unknown section -> just the globals
}

// SectionTree returns the section hierarchy tree built from the keyword data.
func (s *Set) SectionTree() *SectionTree {
	return s.tree
}

// SectionsWithKeywords returns the set of section IDs that have at least one
// keyword (excluding the global "" section).
func (s *Set) SectionsWithKeywords() map[string]bool {
	result := make(map[string]bool, len(s.bySection))
	for sec, kws := range s.bySection {
		if sec != "" && len(kws) > 0 {
			result[sec] = true
		}
	}
	return result
}
