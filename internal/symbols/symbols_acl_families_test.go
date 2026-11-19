package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// ACL family extraction (chunt-otg.9): ipv6 / mac / arp join the IPv4 KindACL
// as separate family-aware kinds so a v4 name can never satisfy a v6
// reference (and vice versa).

func TestExtractACLFamilies(t *testing.T) {
	src := `ip access-list extended V4ACL
 permit ip any any
!
ipv6 access-list V6ACL
 permit ipv6 any any
!
mac access-list extended MACACL
 permit any any
!
arp access-list ARPACL
 permit ip any mac any
!
access-list 99 permit 10.0.0.0 255.0.0.0
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	byKind := map[symbols.Kind][]string{}
	for _, s := range syms {
		byKind[s.Kind] = append(byKind[s.Kind], s.Name)
	}
	want := map[symbols.Kind][]string{
		symbols.KindACL:    {"V4ACL", "99"}, // section + numbered statement
		symbols.KindACLv6:  {"V6ACL"},
		symbols.KindMACACL: {"MACACL"},
		symbols.KindARPACL: {"ARPACL"},
	}
	for k, ws := range want {
		if len(byKind[k]) != len(ws) {
			t.Fatalf("%s = %v, want %v", k, byKind[k], ws)
		}
		for i := range ws {
			if byKind[k][i] != ws[i] {
				t.Fatalf("%s[%d] = %q, want %q (all: %v)", k, i, byKind[k][i], ws[i], byKind[k])
			}
		}
	}
}

// Negated family lines register nothing (negation prune).
func TestExtractNegatedACLFamilies(t *testing.T) {
	src := `no ipv6 access-list V6-OLD
no arp access-list ARP-OLD
no mac access-list extended MAC-OLD
!
`
	root, content := parseRoot(t, src)
	for _, s := range symbols.Extract("file:///t.ios.j2", root, content) {
		t.Fatalf("negated definition registered: %v %q", s.Kind, s.Name)
	}
}
