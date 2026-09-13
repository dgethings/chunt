package cisco_ios_jinja2_test

import (
	"context"
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
)

func TestReferences_FromReference(t *testing.T) {
	// Two references to ACL FOO; cursor on the second should return both
	// reference sites (and the definition when IncludeDeclaration is true).
	src := `ip access-list standard FOO
!
interface Gi0/0
 ip access-group FOO in
!
interface Gi1/0
 ip access-group FOO in
!
`
	f, doc := defTestFeature(t, src)
	// Cursor on FOO in the second reference (line 6, col 18 middle of FOO).
	locs, err := f.References(context.Background(), doc, protocol.Position{Line: 6, Character: 18}, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	// Expect: 2 references + 1 declaration = 3 locations.
	if len(locs) != 3 {
		t.Fatalf("got %d locations, want 3 (2 refs + 1 decl): %+v", len(locs), locs)
	}
	// Verify one of them is at line 0 (the definition site).
	var foundDecl bool
	for _, l := range locs {
		if l.Range.Start.Line == 0 {
			foundDecl = true
			break
		}
	}
	if !foundDecl {
		t.Errorf("IncludeDeclaration=true: no location at line 0 (definition site)")
	}
}

func TestReferences_ExcludeDeclaration(t *testing.T) {
	src := `ip access-list standard FOO
!
interface Gi0/0
 ip access-group FOO in
!
`
	f, doc := defTestFeature(t, src)
	locs, err := f.References(context.Background(), doc, protocol.Position{Line: 3, Character: 18}, false)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("got %d locations, want 1 (ref only): %+v", len(locs), locs)
	}
	if locs[0].Range.Start.Line != 3 {
		t.Errorf("expected ref at line 3, got line %d", locs[0].Range.Start.Line)
	}
}

func TestReferences_FromDefinition(t *testing.T) {
	// Cursor on the definition site should also return all references.
	src := `route-map RM permit 10
!
interface Gi0/0
 ip policy route-map RM
!
`
	f, doc := defTestFeature(t, src)
	// Cursor on RM in line 0 (col 11, middle of "RM").
	locs, err := f.References(context.Background(), doc, protocol.Position{Line: 0, Character: 11}, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	// Expect 1 reference (line 3) + 1 declaration (line 0) = 2.
	if len(locs) != 2 {
		t.Fatalf("got %d locations, want 2: %+v", len(locs), locs)
	}
	var foundRef bool
	for _, l := range locs {
		if l.Range.Start.Line == 3 {
			foundRef = true
		}
	}
	if !foundRef {
		t.Errorf("no reference at line 3 returned when invoking from definition")
	}
}

func TestReferences_NotOnSymbolOrReference(t *testing.T) {
	src := `route-map RM permit 10
!
interface Gi0/0
 ip policy route-map RM
!
`
	f, doc := defTestFeature(t, src)
	// Cursor on "interface" keyword — not a name token.
	locs, err := f.References(context.Background(), doc, protocol.Position{Line: 2, Character: 3}, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(locs) != 0 {
		t.Errorf("got %d locations for non-name cursor, want 0: %+v", len(locs), locs)
	}
}

func TestReferences_NoCrossKindLeakage(t *testing.T) {
	// A class-map named FOO and an ACL reference named FOO should NOT cross-
	// match. Cursor on the ACL ref should only find ACL refs.
	src := `class-map match-any FOO
!
interface Gi0/0
 ip access-group FOO in
!
`
	f, doc := defTestFeature(t, src)
	// Cursor on FOO in line 3 (col 18, middle of FOO).
	locs, err := f.References(context.Background(), doc, protocol.Position{Line: 3, Character: 18}, true)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	// Should NOT include the class-map definition (line 0).
	for _, l := range locs {
		if l.Range.Start.Line == 0 {
			t.Errorf("cross-kind leakage: class-map FOO returned from ACL ref cursor: %+v", l)
		}
	}
}
