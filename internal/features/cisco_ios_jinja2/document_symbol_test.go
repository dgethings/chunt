package cisco_ios_jinja2_test

import (
	"context"
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
)

func TestDocumentSymbol_AllKindsPresent(t *testing.T) {
	src := `!
interface GigabitEthernet0/0
!
router bgp 100
!
route-map RM permit 10
!
class-map match-any CM
!
policy-map PM
!
vlan 10
!
line vty 0 4
!
redundancy
!
ip access-list standard NAMED-ACL
!
access-list 100 permit ip any any
!
hostname r1
!
`
	f, doc := defTestFeature(t, src)
	syms, err := f.DocumentSymbol(context.Background(), doc)
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	// Expect 11 symbols (one per definition line, minus the `!` preamble).
	wantKinds := map[symbols.Kind]int{
		symbols.KindInterface:  1,
		symbols.KindRouter:     1,
		symbols.KindRouteMap:   1,
		symbols.KindClassMap:   1,
		symbols.KindPolicyMap:  1,
		symbols.KindVlan:       1,
		symbols.KindLine:       1,
		symbols.KindRedundancy: 1,
		symbols.KindACL:        2, // NAMED-ACL + access-list 100
		symbols.KindHostname:   1,
	}
	got := make(map[symbols.Kind]int)
	for _, s := range syms {
		// Detail holds the Kind string; recover it for the assertion.
		k := symbols.Kind(s.Detail)
		got[k]++
		if s.Name == "" {
			t.Errorf("symbol with empty Name: %+v", s)
		}
		if s.Range.Start.Line != s.SelectionRange.Start.Line &&
			s.Range.End.Line != s.SelectionRange.End.Line {
			// Range can be a strict superset of SelectionRange (different
			// start/end columns are fine), but they should at least be on
			// the same line for our flat symbols.
			t.Errorf("Range and SelectionRange on different lines: %+v", s)
		}
		if s.Kind == 0 {
			t.Errorf("symbol %q has Kind=0 (no LSP kind mapping): %+v", s.Name, s)
		}
	}
	for k, c := range wantKinds {
		if got[k] != c {
			t.Errorf("kind %s: got %d, want %d", k, got[k], c)
		}
	}
}

func TestDocumentSymbol_EmptyDocument(t *testing.T) {
	f, doc := defTestFeature(t, "!\n!\n")
	syms, err := f.DocumentSymbol(context.Background(), doc)
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if len(syms) != 0 {
		t.Errorf("got %d symbols for empty doc, want 0: %+v", len(syms), syms)
	}
}

func TestDocumentSymbol_LSPKindMapping(t *testing.T) {
	cases := []struct {
		kind    symbols.Kind
		wantLSP int
	}{
		{symbols.KindInterface, protocol.SymbolKindInterface},
		{symbols.KindRouter, protocol.SymbolKindClass},
		{symbols.KindRouteMap, protocol.SymbolKindClass},
		{symbols.KindClassMap, protocol.SymbolKindClass},
		{symbols.KindPolicyMap, protocol.SymbolKindClass},
		{symbols.KindVlan, protocol.SymbolKindNumber},
		{symbols.KindLine, protocol.SymbolKindVariable},
		{symbols.KindRedundancy, protocol.SymbolKindNamespace},
		{symbols.KindACL, protocol.SymbolKindNamespace},
		{symbols.KindHostname, protocol.SymbolKindVariable},
	}
	src := `interface Gi0/0
!
router bgp 100
!
route-map RM permit 10
!
class-map match-any CM
!
policy-map PM
!
vlan 10
!
line vty 0 4
!
redundancy
!
ip access-list standard ACL1
!
hostname r1
!
`
	f, doc := defTestFeature(t, src)
	syms, err := f.DocumentSymbol(context.Background(), doc)
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	gotLSP := make(map[symbols.Kind]int)
	for _, s := range syms {
		gotLSP[symbols.Kind(s.Detail)] = s.Kind
	}
	for _, tc := range cases {
		got, ok := gotLSP[tc.kind]
		if !ok {
			t.Errorf("kind %s missing from DocumentSymbol output", tc.kind)
			continue
		}
		if got != tc.wantLSP {
			t.Errorf("kind %s: got LSP kind %d, want %d", tc.kind, got, tc.wantLSP)
		}
	}
}

func TestDocumentSymbol_SelectionRangeInsideRange(t *testing.T) {
	// SelectionRange should be inside (or equal to) Range — both must
	// reference the same line at minimum for our flat symbols.
	src := `route-map FOO permit 10
 match ip address ACL
!
`
	f, doc := defTestFeature(t, src)
	syms, err := f.DocumentSymbol(context.Background(), doc)
	if err != nil {
		t.Fatalf("DocumentSymbol: %v", err)
	}
	if len(syms) == 0 {
		t.Fatalf("got 0 symbols, want >= 1")
	}
	for _, s := range syms {
		if s.SelectionRange.Start.Line < s.Range.Start.Line ||
			s.SelectionRange.Start.Line > s.Range.End.Line {
			t.Errorf("symbol %q: SelectionRange not inside Range: %+v", s.Name, s)
		}
	}
}
