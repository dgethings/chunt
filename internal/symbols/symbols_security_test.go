package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Security object extraction (chunt-otg.8): crypto maps (seq-addressed),
// transform-sets, isakmp policy/profile, ipsec profile, zones, and
// parameter-maps all parse flat; the parameter-map header needs the text
// catch-all workaround (hyphenated keyword lexes as bare `parameter`).

func TestExtractSecurityObjects(t *testing.T) {
	src := `crypto map CM1 10 ipsec-isakmp
 match address 101
!
crypto map CM1 20 ipsec-isakmp
!
crypto ipsec transform-set TS1 esp-aes esp-sha-hmac
!
crypto isakmp policy 10
 encryption aes
!
crypto isakmp profile PROF1
 keyring KR1
!
crypto ipsec profile IPP1
 set transform-set TS1
!
zone security INSIDE
!
parameter-map type inspect PM1
 log-flow
!
parameter-map type regex REGEX1
 pattern foo
!
parameter-map type inspect-global
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	type want struct {
		kind symbols.Kind
		name string
		seq  string
	}
	wants := []want{
		{symbols.KindCryptoMap, "CM1", "10"},
		{symbols.KindCryptoMap, "CM1", "20"}, // entries are seq-addressed
		{symbols.KindTransformSet, "TS1", ""},
		{symbols.KindIsakmpPolicy, "10", ""},
		{symbols.KindIsakmpProfile, "PROF1", ""},
		{symbols.KindIPsecProfile, "IPP1", ""},
		{symbols.KindZone, "INSIDE", ""},
		{symbols.KindParameterMap, "PM1", ""},
		{symbols.KindParameterMap, "REGEX1", ""},
		// parameter-map type inspect-global: nameless singleton, no symbol
	}
	if len(syms) != len(wants) {
		t.Fatalf("got %d symbols, want %d: %+v", len(syms), len(wants), syms)
	}
	for i, w := range wants {
		if syms[i].Kind != w.kind || syms[i].Name != w.name || syms[i].Seq != w.seq {
			t.Errorf("symbol %d: got (%s, %q, seq %q), want (%s, %q, seq %q)",
				i, syms[i].Kind, syms[i].Name, syms[i].Seq, w.kind, w.name, w.seq)
		}
	}
}

// Non-definition crypto/zone forms must not register symbols.
func TestSecurityNonDefinitionFormsSkipped(t *testing.T) {
	src := `interface Gi0/0
 crypto map CM1
 zone-member security INSIDE
 service-policy type inspect PM1
!
crypto isakmp keepalive 10
crypto isakmp nat traversal 20
zone security
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	for _, s := range syms {
		switch s.Kind {
		case symbols.KindCryptoMap, symbols.KindZone, symbols.KindParameterMap,
			symbols.KindIsakmpPolicy, symbols.KindIsakmpProfile:
			t.Errorf("non-definition form registered as %s %q", s.Kind, s.Name)
		}
	}
}
