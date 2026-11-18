package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Routing policy object extraction (chunt-otg.6): prefix-lists,
// community-lists, and as-path ACLs parse as flat command lines; extraction
// is hostname-style with numbered-name normalization and seq tracking.

func TestExtractRoutingPolicyKinds(t *testing.T) {
	src := `ip prefix-list PL1 seq 10 permit 10.0.0.0/8
ip prefix-list PL1 seq 20 deny 20.0.0.0/8
ip prefix-list PL2 deny 30.0.0.0/8
ipv6 prefix-list PL6 seq 10 permit 2001:db8::/32
ip community-list standard COMM1 permit 65000:100
ip community-list expanded EXP1 permit ^65000_
ip community-list 5 permit 65000:100
ip as-path access-list 10 permit ^65000$
ip as-path access-list AS-PATHS permit _65001_
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	type want struct {
		kind symbols.Kind
		name string
		seq  string
	}
	wants := []want{
		{symbols.KindPrefixList, "PL1", "10"},
		{symbols.KindPrefixList, "PL1", "20"},
		{symbols.KindPrefixList, "PL2", ""}, // no seq clause
		{symbols.KindPrefixList, "PL6", "10"},
		{symbols.KindCommunityList, "COMM1", ""},
		{symbols.KindCommunityList, "EXP1", ""},
		{symbols.KindCommunityList, "5", ""}, // numbered form normalizes to string
		{symbols.KindAsPathACL, "10", ""},
		{symbols.KindAsPathACL, "AS-PATHS", ""},
	}
	if len(syms) != len(wants) {
		t.Fatalf("got %d symbols, want %d: %v", len(syms), len(wants), syms)
	}
	for i, w := range wants {
		s := syms[i]
		if s.Kind != w.kind || s.Name != w.name || s.Seq != w.seq {
			t.Fatalf("symbol[%d] = {%v %q seq=%q}, want {%v %q seq=%q}", i, s.Kind, s.Name, s.Seq, w.kind, w.name, w.seq)
		}
	}
}

// Negated definition lines must NOT register symbols: `no ip prefix-list
// PL2 ...` removes the entry, and `no hostname R9` is not a second hostname.
// Extraction previously descended into negated_statement bodies (chunt-otg.6).
func TestExtractSkipsNegatedDefinitions(t *testing.T) {
	src := `ip prefix-list PL1 seq 10 permit 10.0.0.0/8
no ip prefix-list PL2 seq 10 permit 30.0.0.0/8
no ip community-list 7 deny 65000:200
no ip as-path access-list 20 deny _65001_
hostname R1
no hostname R9
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	for _, s := range syms {
		switch s.Name {
		case "PL2", "7", "20", "R9":
			t.Fatalf("negated definition registered as symbol: %v %q", s.Kind, s.Name)
		}
	}
	if len(syms) != 2 { // PL1 + R1
		t.Fatalf("got %v, want exactly [prefix-list PL1, hostname R1]", syms)
	}
}
