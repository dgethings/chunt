package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// TestWrongSectionDiagnostics_MultiWordKeyword covers the longest-prefix
// keyword extraction for command_line nodes: a multi-word command whose full
// leading prefix is a known keyword should resolve to that prefix and fire a
// wrong-section Hint when placed in the wrong section.
//
// Note: "match ip address ACL" is also a known multi-word keyword, but it
// parses as a dedicated match_statement node (because `match` is a prec-2
// grammar keyword), and *_statement nodes are explicitly OUT OF SCOPE per the
// chunt-y42 spec — they already carry a single-token leading keyword.
func TestWrongSectionDiagnostics_MultiWordKeyword(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kw   string // expected resolved multi-word keyword substring
	}{
		{
			name: "ip access-group inside router section",
			src:  "!\nrouter bgp 100\n ip access-group ACL in\n!\n",
			kw:   "ip access-group",
		},
		{
			name: "switchport access vlan inside router section",
			src:  "!\nrouter bgp 100\n switchport access vlan 99\n!\n",
			kw:   "switchport access vlan",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			d, found := findHintByKeyword(diags, tc.kw)
			if !found {
				t.Fatalf("expected Hint for %q; got %d diags: %+v", tc.kw, len(diags), diags)
			}
			if !strings.Contains(d.Message, tc.kw) {
				t.Errorf("message should contain resolved keyword %q; got %q", tc.kw, d.Message)
			}
		})
	}
}

// TestWrongSectionDiagnostics_SingleTokenStillValidated ensures the
// longest-prefix change does not alter behavior for single-token keywords.
func TestWrongSectionDiagnostics_SingleTokenStillValidated(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kw   string
	}{
		{
			name: "speed (single token) in router section -> Hint",
			src:  "!\nrouter bgp 100\n speed 1000\n!\n",
			kw:   "speed",
		},
		{
			name: "hostname (single token) in router section -> Hint",
			src:  "!\nrouter bgp 100\n hostname r1\n!\n",
			kw:   "hostname",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			if _, found := findHintByKeyword(diags, tc.kw); !found {
				t.Errorf("expected single-token Hint for %q; got %d diags: %+v", tc.kw, len(diags), diags)
			}
		})
	}
}

// TestWrongSectionDiagnostics_UnknownMultiTokenFallback verifies that a
// command_line whose tokens never form a known multi-word keyword falls back
// to the bare name token cleanly (no false match, no panic) and produces no
// Hint because the bare name is not in the DB.
func TestWrongSectionDiagnostics_UnknownMultiTokenFallback(t *testing.T) {
	src := "!\nrouter bgp 100\n frobnicate widget gadget 42\n!\n"
	diags := openDiags(t, src)
	assertNoDiagAbout(t, diags, "frobnicate")
}
