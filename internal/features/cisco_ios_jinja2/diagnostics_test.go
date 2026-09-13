package cisco_ios_jinja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
)

// openOpen parses src via the feature's DidOpen and returns the produced
// diagnostics. DidOpen triggers a single reparse + diagnostics pass.
func openDiags(t *testing.T, src string) []protocol.Diagnostic {
	t.Helper()
	f := cisco_ios_jinja2.New()
	defer f.Close()
	doc := document.New("file:///test.ios.j2", "cisco_ios_jinja2", 1, []byte(src))
	diags, err := f.DidOpen(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	return diags
}

func findDiagByCode(diags []protocol.Diagnostic, code string) (protocol.Diagnostic, bool) {
	for _, d := range diags {
		if d.Code == code {
			return d, true
		}
	}
	return protocol.Diagnostic{}, false
}

func findDiagByMessageContains(diags []protocol.Diagnostic, needle string) (protocol.Diagnostic, bool) {
	for _, d := range diags {
		if strings.Contains(d.Message, needle) {
			return d, true
		}
	}
	return protocol.Diagnostic{}, false
}

func TestUndefinedReferenceDiagnostics_FlagsEachKind(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantCode string
		wantSub  string // substring expected in Message
	}{
		{
			name:     "undefined ACL via ip access-group",
			src:      "interface Gi0/0\n ip access-group MISSING in\n!\n",
			wantCode: "undefined-acl",
			wantSub:  `undefined acl "MISSING"`,
		},
		{
			name:     "undefined ACL via access-class",
			src:      "line vty 0 4\n access-class MISSING in\n!\n",
			wantCode: "undefined-acl",
			wantSub:  `undefined acl "MISSING"`,
		},
		{
			name:     "undefined ACL via match ip address",
			src:      "class-map match-any VOICE\n match ip address MISSING\n!\n",
			wantCode: "undefined-acl",
			wantSub:  `undefined acl "MISSING"`,
		},
		{
			name:     "undefined route-map",
			src:      "interface Gi0/0\n ip policy route-map MISSING\n!\n",
			wantCode: "undefined-route-map",
			wantSub:  `undefined route-map "MISSING"`,
		},
		{
			name:     "undefined class-map (excluding built-in class-default)",
			src:      "policy-map PM\n class MISSING\n  priority\n!\n",
			wantCode: "undefined-class-map",
			wantSub:  `undefined class-map "MISSING"`,
		},
		{
			name:     "undefined policy-map via service-policy",
			src:      "service-policy input MISSING\n",
			wantCode: "undefined-policy-map",
			wantSub:  `undefined policy-map "MISSING"`,
		},
		{
			name:     "undefined vlan",
			src:      "interface Gi0/0\n switchport access vlan 999\n!\n",
			wantCode: "undefined-vlan",
			wantSub:  `undefined vlan "999"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			d, found := findDiagByCode(diags, tc.wantCode)
			if !found {
				t.Fatalf("no diagnostic with code %q in %d diags: %+v", tc.wantCode, len(diags), diags)
			}
			if d.Severity != protocol.SeverityWarning {
				t.Errorf("severity: got %d, want %d (Warning)", d.Severity, protocol.SeverityWarning)
			}
			if d.Source != "chunt" {
				t.Errorf("source: got %q, want \"chunt\"", d.Source)
			}
			if d.Message != tc.wantSub {
				t.Errorf("message: got %q, want %q", d.Message, tc.wantSub)
			}
		})
	}
}

func TestUndefinedReferenceDiagnostics_SatisfiedByDefinition(t *testing.T) {
	// When a matching definition exists, no undefined-reference diagnostic
	// should fire for that (kind, name).
	src := `ip access-list standard FOO
 permit 10.0.0.0 0.0.0.255
!
interface Gi0/0
 ip access-group FOO in
!
`
	diags := openDiags(t, src)
	if d, found := findDiagByMessageContains(diags, "undefined acl"); found {
		t.Errorf("got unexpected undefined-ACL diagnostic for satisfied ref: %+v", d)
	}
}

func TestUndefinedReferenceDiagnostics_NegationNotFlagged(t *testing.T) {
	src := `interface Gi0/0
 no ip access-group FOO in
!
`
	diags := openDiags(t, src)
	if len(diags) != 0 {
		t.Errorf("got %d diagnostics for a negated reference, want 0: %+v", len(diags), diags)
	}
}

func TestUndefinedReferenceDiagnostics_BuiltInClassDefault(t *testing.T) {
	src := `policy-map PM
 class class-default
  priority
!
`
	diags := openDiags(t, src)
	if d, found := findDiagByMessageContains(diags, `class-default`); found {
		t.Errorf("built-in class-default was flagged: %+v", d)
	}
}

func TestUndefinedReferenceDiagnostics_MultipleInOneDoc(t *testing.T) {
	src := `interface Gi0/0
 ip access-group ACL1 in
 ip policy route-map RM1
!
class-map match-any CM1
 match ip address ACL2
!
`
	diags := openDiags(t, src)
	// Expect 3 undefined refs: ACL1, RM1, ACL2.
	wantCodes := []string{"undefined-acl", "undefined-route-map"}
	var aclCount int
	for _, d := range diags {
		if d.Code == "undefined-acl" {
			aclCount++
		}
	}
	if aclCount != 2 {
		t.Errorf("ACL diagnostics: got %d, want 2 (ACL1 and ACL2)", aclCount)
	}
	for _, code := range wantCodes {
		if _, found := findDiagByCode(diags, code); !found {
			t.Errorf("missing diagnostic with code %q in: %+v", code, diags)
		}
	}
}

func TestDuplicateDefinitionDiagnostics(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantCode    string
		wantMessage string
	}{
		{
			name: "duplicate route-map",
			src: `route-map FOO permit 10
!
route-map FOO deny 20
!
`,
			wantCode:    "duplicate-route-map",
			wantMessage: `duplicate route-map definition "FOO"`,
		},
		{
			name: "duplicate class-map",
			src: `class-map match-any VOICE
!
class-map VOICE
!
`,
			wantCode:    "duplicate-class-map",
			wantMessage: `duplicate class-map definition "VOICE"`,
		},
		{
			name: "duplicate interface",
			src: `interface Gi0/0
!
interface Gi0/0
!
`,
			wantCode:    "duplicate-interface",
			wantMessage: `duplicate interface definition "Gi0/0"`,
		},
		{
			name: "duplicate hostname",
			src: `hostname r1
!
hostname r1
!
`,
			wantCode:    "duplicate-hostname",
			wantMessage: `duplicate hostname definition "r1"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			d, found := findDiagByCode(diags, tc.wantCode)
			if !found {
				t.Fatalf("no diagnostic with code %q in: %+v", tc.wantCode, diags)
			}
			if d.Message != tc.wantMessage {
				t.Errorf("message: got %q, want %q", d.Message, tc.wantMessage)
			}
			if d.Severity != protocol.SeverityWarning {
				t.Errorf("severity: got %d, want Warning", d.Severity)
			}
			if len(d.RelatedInformation) == 0 {
				t.Errorf("expected RelatedInformation pointing to first definition; got none")
			} else {
				ri := d.RelatedInformation[0]
				if ri.Location.URI != "file:///test.ios.j2" {
					t.Errorf("related URI: got %q, want file:///test.ios.j2", ri.Location.URI)
				}
				if ri.Message == "" {
					t.Errorf("related message is empty")
				}
			}
		})
	}
}

func TestDuplicateDefinitionDiagnostics_NotFlaggedWhenUnique(t *testing.T) {
	src := `route-map FOO permit 10
!
route-map BAR permit 10
!
interface Gi0/0
!
`
	diags := openDiags(t, src)
	for _, d := range diags {
		if strings.HasPrefix(d.Code, "duplicate-") {
			t.Errorf("unexpected duplicate diagnostic: %+v", d)
		}
	}
}

func TestDuplicateDefinitionDiagnostics_RouterProcessId(t *testing.T) {
	// Two router bgp 100 sections in one config — a common copy-paste error.
	src := `router bgp 100
 neighbor 10.0.0.2 remote-as 200
!
router bgp 100
 neighbor 10.0.0.3 remote-as 200
!
`
	diags := openDiags(t, src)
	if _, found := findDiagByCode(diags, "duplicate-router"); !found {
		t.Errorf("missing duplicate-router diagnostic in: %+v", diags)
	}
}

func TestDuplicateDefinitionDiagnostics_RedundancySingletonExcluded(t *testing.T) {
	// Multiple `redundancy` sections are unusual but not a duplicate-definition
	// condition — the kind is treated as a singleton.
	src := `redundancy
!
redundancy
!
`
	diags := openDiags(t, src)
	for _, d := range diags {
		if d.Code == "duplicate-redundancy" {
			t.Errorf("singleton redundancy should not be flagged as duplicate: %+v", d)
		}
	}
}

func TestVersionMismatchStillWorks(t *testing.T) {
	// Regression guard: the original version-mismatch diagnostic still fires
	// after the dispatcher refactor.
	src := "! version 17.3\n!\nversion 17.4\n!\n"
	diags := openDiags(t, src)
	if _, found := findDiagByMessageContains(diags, "version mismatch"); !found {
		t.Errorf("version-mismatch diagnostic missing after refactor: %+v", diags)
	}
}

// TestMergedCollector_AllFamiliesInOnePass is the chunt-zob regression guard.
// A single document triggers three diagnostic families at once — syntax
// (missing-}}), wrong-section (an interface command in a router section), and
// protocol-mismatch (an OSPF "area" in a BGP router) — and all three must
// surface. collectTreeDiags folds the former independent tree walks into ONE
// traversal; this asserts every per-node check still runs in that single walk
// and emits its diagnostic. (command-version is exercised through the same
// collector by TestCommandVersionDiagnostics.)
func TestMergedCollector_AllFamiliesInOnePass(t *testing.T) {
	src := "router bgp 100\n speed 1000\n area 0 range 10.0.0.0 255.0.0.0\n!\nhostname r{{\n"
	diags := openDiags(t, src)

	// protocol-mismatch: the OSPF-exclusive "area" command_line inside router
	// bgp yields exactly one Error.
	if pm := diagsByCode(diags, "protocol-mismatch"); len(pm) != 1 {
		t.Errorf("want 1 protocol-mismatch diagnostic, got %d: %+v", len(pm), pm)
	}

	// wrong-section: "speed" (valid in config-if) inside a router section. This
	// Hint carries no Code, so match on its message substring.
	if _, ok := findDiagByMessageContains(diags, "speed is valid in config-if"); !ok {
		t.Errorf("want a wrong-section hint for speed; got %d diags: %+v", len(diags), diags)
	}

	// syntax: the unterminated {{ yields a recovered missing-}} Error.
	if _, ok := findDiagByCode(diags, "missing-}}"); !ok {
		t.Errorf("want a missing-}} syntax diagnostic; got %d diags: %+v", len(diags), diags)
	}
}
