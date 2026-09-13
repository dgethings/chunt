package symbols_test

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
	ts_ci "github.com/dgethings/tree-sitter-cisco-ios-jinja2/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func parseRoot(t *testing.T, src string) (*sitter.Node, []byte) {
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

// namesByKind groups symbols by Kind, returning the sorted names within each
// kind for stable comparison in tests.
func namesByKind(syms []symbols.Symbol) map[symbols.Kind][]string {
	out := make(map[symbols.Kind][]string)
	for _, s := range syms {
		out[s.Kind] = append(out[s.Kind], s.Name)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func TestExtract_Sections(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want map[symbols.Kind][]string
	}{
		{
			name: "interface",
			src:  "interface GigabitEthernet0/0\n!\n",
			want: map[symbols.Kind][]string{symbols.KindInterface: {"GigabitEthernet0/0"}},
		},
		{
			name: "router bgp",
			src:  "router bgp 100\n!\n",
			want: map[symbols.Kind][]string{symbols.KindRouter: {"100"}},
		},
		{
			name: "route-map with action and sequence",
			src:  "route-map FOO permit 10\n!\n",
			want: map[symbols.Kind][]string{symbols.KindRouteMap: {"FOO"}},
		},
		{
			name: "route-map bare",
			src:  "route-map BAR\n!\n",
			want: map[symbols.Kind][]string{symbols.KindRouteMap: {"BAR"}},
		},
		{
			name: "class-map with match-type",
			src:  "class-map match-any VOICE\n!\n",
			want: map[symbols.Kind][]string{symbols.KindClassMap: {"VOICE"}},
		},
		{
			name: "class-map bare name",
			src:  "class-map DATA\n!\n",
			want: map[symbols.Kind][]string{symbols.KindClassMap: {"DATA"}},
		},
		{
			name: "policy-map",
			src:  "policy-map QOS\n!\n",
			want: map[symbols.Kind][]string{symbols.KindPolicyMap: {"QOS"}},
		},
		{
			name: "vlan",
			src:  "vlan 10\n name MANAGEMENT\n!\n",
			want: map[symbols.Kind][]string{symbols.KindVlan: {"10"}},
		},
		{
			name: "line vty with range",
			src:  "line vty 0 4\n!\n",
			want: map[symbols.Kind][]string{symbols.KindLine: {"vty-0-4"}},
		},
		{
			name: "line console single number",
			src:  "line console 0\n!\n",
			want: map[symbols.Kind][]string{symbols.KindLine: {"console-0"}},
		},
		{
			name: "redundancy singleton",
			src:  "redundancy\n auto-sync running-config\n!\n",
			want: map[symbols.Kind][]string{symbols.KindRedundancy: {"redundancy"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, content := parseRoot(t, tc.src)
			got := namesByKind(symbols.Extract("file:///test.ios.j2", root, content))
			for k, wantNames := range tc.want {
				gotNames, ok := got[k]
				if !ok {
					t.Fatalf("kind %s: no symbols extracted; want %v", k, wantNames)
				}
				if len(gotNames) != len(wantNames) {
					t.Fatalf("kind %s: got %v, want %v", k, gotNames, wantNames)
				}
				for i := range wantNames {
					if gotNames[i] != wantNames[i] {
						t.Errorf("kind %s[%d]: got %q, want %q", k, i, gotNames[i], wantNames[i])
					}
				}
			}
		})
	}
}

func TestExtract_ACLs(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // expected ACL names
	}{
		{
			name: "named standard ACL",
			src:  "ip access-list standard FOO\n!\n",
			want: []string{"FOO"},
		},
		{
			name: "named extended ACL",
			src:  "ip access-list extended BAR\n!\n",
			want: []string{"BAR"},
		},
		{
			name: "numbered ACL",
			src:  "access-list 101 permit ip any any\n!\n",
			want: []string{"101"},
		},
		{
			name: "mixed ACL forms",
			src: "ip access-list standard NAMED\n" +
				"!\n" +
				"access-list 100 permit ip any any\n" +
				"!\n" +
				"ip access-list extended NAMED2\n" +
				"!\n" +
				"access-list 101 deny tcp any any\n",
			want: []string{"NAMED", "100", "NAMED2", "101"},
		},
		// Phase B (PLAN-IP-ACCESS-LIST.md): documents that numbered ACLs
		// now route through the dedicated access_list_statement AST node
		// rather than the old command_line(access) + text sibling shape.
		// The assertion (name "101", kind KindACL) intentionally mirrors
		// the numbered_ACL case above — both AST node types now contribute
		// ACL symbols.
		{
			name: "numbered ACL via access_list_statement node",
			src:  "access-list 101 permit ip any any\n!\n",
			want: []string{"101"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, content := parseRoot(t, tc.src)
			syms := symbols.Extract("file:///test.ios.j2", root, content)
			var got []string
			for _, s := range syms {
				if s.Kind == symbols.KindACL {
					got = append(got, s.Name)
				}
			}
			sort.Strings(got)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			// Compare as sets since numbered ACL ordering within document may
			// not match the input order across mixed forms.
			wantSorted := append([]string(nil), tc.want...)
			sort.Strings(wantSorted)
			for i := range wantSorted {
				if got[i] != wantSorted[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], wantSorted[i])
				}
			}
		})
	}
}

func TestExtract_Integration(t *testing.T) {
	src := `!
hostname r1
!
interface GigabitEthernet0/0
 ip address 10.0.0.1 255.255.255.0
!
router bgp 100
 neighbor 10.0.0.2 remote-as 200
!
route-map RM-OUT permit 10
 match ip address NAMED-ACL
!
class-map match-any VOICE
 match ip address NAMED-ACL
!
policy-map QOS
 class VOICE
  priority
!
ip access-list standard NAMED-ACL
 permit 10.0.0.0 0.0.0.255
!
vlan 10
 name MGMT
!
line vty 0 4
 transport input ssh
!
redundancy
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///test.ios.j2", root, content)

	want := map[symbols.Kind]int{
		symbols.KindInterface:  1,
		symbols.KindRouter:     1,
		symbols.KindRouteMap:   1,
		symbols.KindClassMap:   1,
		symbols.KindPolicyMap:  1,
		symbols.KindACL:        1,
		symbols.KindVlan:       1,
		symbols.KindLine:       1,
		symbols.KindRedundancy: 1,
		symbols.KindHostname:   1,
	}
	got := make(map[symbols.Kind]int)
	for _, s := range syms {
		got[s.Kind]++
	}
	for k, wantCount := range want {
		if got[k] != wantCount {
			t.Errorf("kind %s: got %d symbols, want %d", k, got[k], wantCount)
		}
	}
}

func TestExtract_NameRangeNonZero(t *testing.T) {
	// Every symbol's NameRange must be non-empty (Start < End on at least
	// one dimension). This catches bugs where NameRange was left at zero.
	src := "route-map FOO permit 10\n!\ninterface Gi0/0\n!\nvlan 10\n!\n"
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///test.ios.j2", root, content)
	if len(syms) != 3 {
		t.Fatalf("got %d symbols, want 3", len(syms))
	}
	for _, s := range syms {
		if s.NameRange.Start.Line == 0 && s.NameRange.Start.Character == 0 &&
			s.NameRange.End.Line == 0 && s.NameRange.End.Character == 0 {
			t.Errorf("symbol %s %q has zero NameRange", s.Kind, s.Name)
		}
		if s.Name == "" {
			t.Errorf("symbol %v has empty Name", s.Kind)
		}
	}
}

func TestTable_Lookup(t *testing.T) {
	src := "route-map FOO permit 10\n!\nroute-map BAR deny 5\n!\ninterface Gi0/0\n!\n"
	root, content := parseRoot(t, src)
	tbl := symbols.NewTable()
	tbl.Index("file:///test", root, content)

	if got := tbl.Lookup("file:///test", symbols.KindRouteMap, "FOO"); len(got) != 1 {
		t.Errorf("Lookup(FOO): got %d, want 1", len(got))
	}
	if got := tbl.Lookup("file:///test", symbols.KindRouteMap, "MISSING"); len(got) != 0 {
		t.Errorf("Lookup(MISSING): got %d, want 0", len(got))
	}
	if got := tbl.Lookup("file:///test", symbols.KindInterface, "FOO"); len(got) != 0 {
		t.Errorf("Lookup(FOO/interface): got %d, want 0", len(got))
	}
	if got := tbl.LookupAny("file:///test", "BAR"); len(got) != 1 {
		t.Errorf("LookupAny(BAR): got %d, want 1", len(got))
	}
	if got := tbl.All("file:///test"); len(got) != 3 {
		t.Errorf("All: got %d, want 3", len(got))
	}
	if got := tbl.All("file:///other"); len(got) != 0 {
		t.Errorf("All(other URI): got %d, want 0", len(got))
	}

	tbl.Clear("file:///test")
	if got := tbl.All("file:///test"); len(got) != 0 {
		t.Errorf("after Clear: got %d, want 0", len(got))
	}
}

func TestExtract_NilRoot(t *testing.T) {
	if got := symbols.Extract("file:///test", nil, nil); got != nil {
		t.Errorf("Extract(nil): got %v, want nil", got)
	}
}

// TestExtract_SkipsIncompleteHeaders pins the grammar's optional-name-field
// behavior: when the user has typed a section header keyword but not yet
// entered its name value (e.g. `interface ` or `router bgp `), the parser
// produces a `*_header` node with no name child. The symbol extractor must
// skip such headers rather than indexing them with an empty name — otherwise
// every typing-in-progress header would show up as a phantom symbol named "".
func TestExtract_SkipsIncompleteHeaders(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"interface_no_name", "!\ninterface \n!\n"},
		{"vlan_no_name", "!\nvlan \n!\n"},
		{"route_map_no_name", "!\nroute-map \n!\n"},
		{"class_map_no_name", "!\nclass-map \n!\n"},
		{"policy_map_no_name", "!\npolicy-map \n!\n"},
		{"router_bgp_no_process_id", "!\nrouter bgp \n!\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, content := parseRoot(t, tc.src)
			syms := symbols.Extract("file:///test", root, content)
			for _, s := range syms {
				if s.Name == "" {
					t.Errorf("extracted symbol with empty name: kind=%s range=%+v", s.Kind, s.Range)
				}
			}
		})
	}
}

func TestExtract_IgnoresNonDefinitionLines(t *testing.T) {
	// Body commands (match, set, class, permit, deny, neighbor, etc.) must
	// NOT be indexed as symbols. Only headers / definitions count.
	src := `route-map FOO permit 10
 match ip address ACL1
 set metric 100
!
ip access-list extended ACL1
 permit tcp any any
!
router bgp 100
 neighbor 10.0.0.2 remote-as 200
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///test", root, content)

	// Expected: route-map FOO, ACL ACL1, router 100.
	wantKinds := map[symbols.Kind]int{
		symbols.KindRouteMap: 1,
		symbols.KindACL:      1,
		symbols.KindRouter:   1,
	}
	got := make(map[symbols.Kind]int)
	for _, s := range syms {
		got[s.Kind]++
		// No body command keyword should ever appear as a name.
		if strings.HasPrefix(s.Name, "match ") ||
			strings.HasPrefix(s.Name, "set ") ||
			strings.HasPrefix(s.Name, "permit ") ||
			strings.HasPrefix(s.Name, "neighbor ") {
			t.Errorf("body command leaked into symbols: %s %q", s.Kind, s.Name)
		}
	}
	for k, c := range wantKinds {
		if got[k] != c {
			t.Errorf("kind %s: got %d, want %d", k, got[k], c)
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 3: reference extraction
// ---------------------------------------------------------------------------

func TestExtractReferences_Patterns(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantKind symbols.Kind
		wantName string
	}{
		{"ip access-group", "interface Gi0/0\n ip access-group FOO in\n!\n", symbols.KindACL, "FOO"},
		{"access-class in line section", "line vty 0 4\n access-class ACL-VTY in\n!\n", symbols.KindACL, "ACL-VTY"},
		{"match ip address in class-map", "class-map match-any VOICE\n match ip address ACL-VOICE\n!\n", symbols.KindACL, "ACL-VOICE"},
		{"ip policy route-map in interface", "interface Gi0/0\n ip policy route-map RM-OUT\n!\n", symbols.KindRouteMap, "RM-OUT"},
		{"class in policy-map body", "policy-map PM\n class VOICE\n  priority\n!\n", symbols.KindClassMap, "VOICE"},
		{"switchport access vlan", "interface Gi0/0\n switchport access vlan 10\n!\n", symbols.KindVlan, "10"},
		{"service-policy input", "service-policy input PM-IN\n", symbols.KindPolicyMap, "PM-IN"},
		{"service-policy output", "service-policy output PM-OUT\n", symbols.KindPolicyMap, "PM-OUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, content := parseRoot(t, tc.src)
			refs := symbols.ExtractReferences("file:///test", root, content)
			var found bool
			for _, r := range refs {
				if r.Kind == tc.wantKind && r.Name == tc.wantName {
					found = true
					if r.Range.Start.Line == 0 && r.Range.Start.Character == 0 &&
						r.Range.End.Line == 0 && r.Range.End.Character == 0 {
						t.Errorf("reference %v %q has zero Range", r.Kind, r.Name)
					}
				}
			}
			if !found {
				t.Errorf("missing reference %v %q in %d refs: %v", tc.wantKind, tc.wantName, len(refs), refs)
			}
		})
	}
}

func TestExtractReferences_NegationSkipped(t *testing.T) {
	src := `interface GigabitEthernet0/0
 no ip access-group FOO in
 ip access-group BAR in
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///test", root, content)
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1 (negated line should be skipped): %v", len(refs), refs)
	}
	if refs[0].Name != "BAR" {
		t.Errorf("got ref to %q, want BAR", refs[0].Name)
	}
}

func TestExtractReferences_NoFalsePositives(t *testing.T) {
	// Body commands that should NOT be treated as references.
	src := `interface GigabitEthernet0/0
 description uplink
 shutdown
 speed 1000
!
hostname r1
!
no service pad
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///test", root, content)
	if len(refs) != 0 {
		t.Errorf("got %d refs, want 0: %v", len(refs), refs)
	}
}

func TestExtractReferences_Integration(t *testing.T) {
	src := `!
interface GigabitEthernet0/0
 ip access-group ACL-OUT in
 ip policy route-map RM-OUT
 switchport access vlan 10
!
class-map match-any VOICE
 match ip address ACL-VOICE
!
policy-map QOS
 class VOICE
  priority
!
service-policy input QOS
!
`
	root, content := parseRoot(t, src)
	refs := symbols.ExtractReferences("file:///test", root, content)

	// Expect references to: ACL-OUT, RM-OUT, vlan 10, ACL-VOICE, VOICE (class),
	// QOS (service-policy). 6 total.
	want := map[symbols.Kind]string{
		symbols.KindACL:        "ACL-OUT",
		symbols.KindRouteMap:   "RM-OUT",
		symbols.KindVlan:       "10",
		symbols.KindClassMap:   "VOICE",
		symbols.KindPolicyMap:  "QOS",
		symbols.KindACL + "_2": "ACL-VOICE",
	}
	got := make(map[string]int)
	for _, r := range refs {
		key := string(r.Kind)
		// Disambiguate the two ACL refs by name suffix.
		if r.Kind == symbols.KindACL {
			if r.Name == "ACL-VOICE" {
				key = string(r.Kind) + "_2"
			}
		}
		got[key]++
		if wantName, ok := want[symbols.Kind(key)]; ok && r.Name != wantName {
			t.Errorf("kind %s: got ref %q, want %q", key, r.Name, wantName)
		}
	}
	wantCount := 6
	if len(refs) != wantCount {
		t.Errorf("got %d refs, want %d: %+v", len(refs), wantCount, refs)
	}
}

func TestExtractReferences_NilRoot(t *testing.T) {
	if got := symbols.ExtractReferences("file:///test", nil, nil); got != nil {
		t.Errorf("ExtractReferences(nil): got %v, want nil", got)
	}
}

func TestTable_References(t *testing.T) {
	src := `interface Gi0/0
 ip access-group FOO in
 ip access-group BAR in
!
interface Gi1/0
 ip access-group FOO in
!
`
	root, content := parseRoot(t, src)
	tbl := symbols.NewTable()
	tbl.Index("file:///test", root, content)

	refs := tbl.ReferencesAll("file:///test")
	if len(refs) != 3 {
		t.Fatalf("ReferencesAll: got %d, want 3", len(refs))
	}

	fooRefs := tbl.ReferencesLookup("file:///test", symbols.KindACL, "FOO")
	if len(fooRefs) != 2 {
		t.Errorf("ReferencesLookup(FOO): got %d, want 2", len(fooRefs))
	}

	bazRefs := tbl.ReferencesLookup("file:///test", symbols.KindACL, "BAZ")
	if len(bazRefs) != 0 {
		t.Errorf("ReferencesLookup(BAZ): got %d, want 0", len(bazRefs))
	}

	tbl.Clear("file:///test")
	if got := tbl.ReferencesAll("file:///test"); len(got) != 0 {
		t.Errorf("after Clear: got %d refs, want 0", len(got))
	}
}

// BenchmarkLookupScaling demonstrates that Lookup/LookupAny/ReferencesLookup
// are O(1) in the number of indexed symbols: as the document grows, per-call
// time stays ~flat (the pre-refactor linear scan grew linearly with the
// symbol/reference count). Run with:
//
//	go test ./internal/symbols/ -bench LookupScaling -benchmem
//
// chunt-4qw.
func BenchmarkLookupScaling(b *testing.B) {
	for _, n := range []int{128, 1024, 8192} {
		b.Run("symbols="+strconv.Itoa(n), func(b *testing.B) {
			src, wantInterface, wantRef := benchConfig(n)
			root, content := parseRootB(b, src)
			tbl := symbols.NewTable()
			tbl.Index("file:///bench", root, content)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tbl.Lookup("file:///bench", symbols.KindInterface, wantInterface)
				_ = tbl.LookupAny("file:///bench", wantInterface)
				_ = tbl.ReferencesLookup("file:///bench", symbols.KindACL, wantRef)
			}
		})
	}
}

// benchConfig builds a config with n interfaces, each referencing an ACL, so
// the table holds O(n) symbols and O(n) references. wantInterface is the LAST
// interface name and wantRef the LAST ACL — the worst case for a linear scan.
func benchConfig(n int) (src, wantInterface, wantRef string) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "interface if%d\n ip access-group ACL%d in\n!\n", i, i)
	}
	return b.String(), "if" + strconv.Itoa(n-1), "ACL" + strconv.Itoa(n-1)
}

// parseRootB is the benchmark analogue of parseRoot (takes *testing.B so the
// cleanup hook registers against the benchmark).
func parseRootB(b *testing.B, src string) (*sitter.Node, []byte) {
	p := sitter.NewParser()
	p.SetLanguage(sitter.NewLanguage(ts_ci.Language()))
	tree := p.Parse([]byte(src), nil)
	b.Cleanup(func() {
		tree.Close()
		p.Close()
	})
	return tree.RootNode(), []byte(src)
}
