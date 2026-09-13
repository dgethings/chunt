package cisco_ios_jinja2_test

import (
	"context"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
)

// defTestFeature returns a freshly-opened feature for src, used by the
// definition tests. The caller does not need to defer Close because
// openDiags-style helpers in other test files pattern-match on this; we
// inline the cleanup here.
func defTestFeature(t *testing.T, src string) (*cisco_ios_jinja2.CiscoIOSFeature, *document.Document) {
	t.Helper()
	f := cisco_ios_jinja2.New()
	t.Cleanup(func() { f.Close() })
	doc := document.New("file:///test.ios.j2", "cisco_ios_jinja2", 1, []byte(src))
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	return f, doc
}

func TestDefinition_FromReference(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		posLine  uint
		posChar  uint
		wantURI  string
		wantLine uint // expected def line
	}{
		{
			name:     "ip access-group -> ip access-list",
			src:      "ip access-list standard FOO\n!\ninterface Gi0/0\n ip access-group FOO in\n!\n",
			posLine:  3,
			posChar:  18, // middle of "FOO" in " ip access-group FOO in"
			wantURI:  "file:///test.ios.j2",
			wantLine: 0,
		},
		{
			name:     "ip policy route-map -> route-map",
			src:      "route-map RM-OUT permit 10\n!\ninterface Gi0/0\n ip policy route-map RM-OUT\n!\n",
			posLine:  3,
			posChar:  24, // middle of "RM-OUT" in " ip policy route-map RM-OUT"
			wantURI:  "file:///test.ios.j2",
			wantLine: 0,
		},
		{
			name:     "class inside policy-map -> class-map",
			src:      "class-map match-any VOICE\n!\npolicy-map QOS\n class VOICE\n  priority\n!\n",
			posLine:  3,
			posChar:  9, // middle of "VOICE" in " class VOICE"
			wantURI:  "file:///test.ios.j2",
			wantLine: 0,
		},
		{
			name:     "service-policy -> policy-map",
			src:      "policy-map PM\n!\nservice-policy input PM\n",
			posLine:  2,
			posChar:  22, // middle of "PM" in "service-policy input PM"
			wantURI:  "file:///test.ios.j2",
			wantLine: 0,
		},
		{
			name:     "switchport access vlan -> vlan",
			src:      "vlan 10\n name MGMT\n!\ninterface Gi0/0\n switchport access vlan 10\n!\n",
			posLine:  4,
			posChar:  25, // middle of "10" in " switchport access vlan 10"
			wantURI:  "file:///test.ios.j2",
			wantLine: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, doc := defTestFeature(t, tc.src)
			locs, err := f.Definition(context.Background(), doc, protocol.Position{Line: tc.posLine, Character: tc.posChar})
			if err != nil {
				t.Fatalf("Definition: %v", err)
			}
			if len(locs) == 0 {
				t.Fatalf("got 0 locations, want >= 1")
			}
			l := locs[0]
			if l.URI != tc.wantURI {
				t.Errorf("URI: got %q, want %q", l.URI, tc.wantURI)
			}
			if l.Range.Start.Line != tc.wantLine {
				t.Errorf("def line: got %d, want %d", l.Range.Start.Line, tc.wantLine)
			}
		})
	}
}

func TestDefinition_OnDefinitionItself(t *testing.T) {
	// Cursor on the name token of a definition returns its own location.
	src := "route-map FOO permit 10\n!\n"
	f, doc := defTestFeature(t, src)
	// "route-map FOO permit 10" — FOO at col 10-13 (inclusive start, exclusive end).
	locs, err := f.Definition(context.Background(), doc, protocol.Position{Line: 0, Character: 11})
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("got %d locations, want 1: %+v", len(locs), locs)
	}
	if locs[0].Range.Start.Line != 0 {
		t.Errorf("def line: got %d, want 0", locs[0].Range.Start.Line)
	}
}

func TestDefinition_OnNonReferenceToken(t *testing.T) {
	// Cursor on a leading identifier ("ip") is NOT a reference name token;
	// expect no locations.
	src := "ip access-list standard FOO\n!\ninterface Gi0/0\n ip access-group FOO in\n!\n"
	f, doc := defTestFeature(t, src)
	// Cursor at "ip" in the second interface line: col 1-2.
	locs, err := f.Definition(context.Background(), doc, protocol.Position{Line: 3, Character: 1})
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) != 0 {
		t.Errorf("got %d locations for non-reference token, want 0: %+v", len(locs), locs)
	}
}

func TestDefinition_UnresolvedReference(t *testing.T) {
	// Cursor on a reference name with no matching definition returns nil
	// (the diagnostic pass will flag it; Definition should silently return).
	src := "interface Gi0/0\n ip access-group MISSING in\n!\n"
	f, doc := defTestFeature(t, src)
	locs, err := f.Definition(context.Background(), doc, protocol.Position{Line: 1, Character: 18})
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) != 0 {
		t.Errorf("got %d locations for unresolved ref, want 0: %+v", len(locs), locs)
	}
}

func TestDefinition_FallbackLookupAny(t *testing.T) {
	// `match ip address FOO` introduces an ACL ref by default. If no ACL
	// named FOO exists but a route-map (or any other kind) named FOO does,
	// the Definition fallback (LookupAny) should still find it. This
	// catches the ambiguous-prefix-list case where the actual target kind
	// differs from the reference's expected kind.
	src := `route-map FOO permit 10
!
class-map match-any CM
 match ip address FOO
!
`
	f, doc := defTestFeature(t, src)
	// Cursor on "FOO" in " match ip address FOO" — line 3, col 19 (middle of FOO).
	locs, err := f.Definition(context.Background(), doc, protocol.Position{Line: 3, Character: 19})
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) == 0 {
		t.Errorf("LookupAny fallback should find route-map FOO; got 0 locations")
	}
}

func TestDefinition_NumberedACL(t *testing.T) {
	// `access-list 101 permit ip any any` defines an ACL whose symbol name
	// is the bare number "101" (matching how every reference writes it).
	// A reference via `ip access-group 101 in` should resolve to that
	// definition.
	src := "access-list 101 permit ip any any\n!\ninterface Gi0/0\n ip access-group 101 in\n!\n"
	f, download := defTestFeature(t, src)
	locs, err := f.Definition(context.Background(), download, protocol.Position{Line: 3, Character: 19})
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("got %d locations, want 1: %+v", len(locs), locs)
	}
	if locs[0].Range.Start.Line != 0 {
		t.Errorf("def line: got %d, want 0", locs[0].Range.Start.Line)
	}
}
