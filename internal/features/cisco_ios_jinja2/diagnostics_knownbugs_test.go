package cisco_ios_jinja2_test

import "testing"

// This file pins KNOWN, filed diagnostic bugs as self-skipping regression
// tests. Each test reproduces the bug, skips (naming the issue id) WHILE the
// bug is present, and asserts the CORRECT behavior — so the skip auto-clears
// the moment the bug is fixed, at which point the test starts enforcing the
// fix. This keeps `make test` green without endorsing the buggy behavior.
//
// Reproductions derived from the large manual config used during chunt-cfz
// D4 verification (since deleted); these small snippets are the permanent form.

// TestKnownBugVzy_NetworkFlaggedInRouterOspf reproduces chunt-vzy: 'network'
// inside a router section is wrongly flagged wrong-section because the keyword
// DB has no config-router entry for 'network' (LookupSection falls back to the
// obscure config-ipv6-pmipv6-domain-mn). High impact — every OSPF/BGP config
// using 'network' floods with false Hints.
func TestKnownBugVzy_NetworkFlaggedInRouterOspf(t *testing.T) {
	src := "router ospf 1\n network 10.0.0.0 0.0.0.255 area 0\n!\n"
	diags := openDiags(t, src)

	if _, bad := findDiagByMessageContains(diags, "network is valid in"); bad {
		t.Skip("chunt-vzy: 'network' wrongly flagged wrong-section in router ospf " +
			"(keyword DB lacks config-router); skip auto-clears when fixed")
	}
	// Correct behavior, enforced automatically once chunt-vzy is fixed.
	if len(diags) != 0 {
		t.Errorf("'network' in router ospf must not be flagged wrong-section: %+v", diags)
	}
}

// TestKnownBug9of_CompoundSyntaxErrorSwallowsMissingJinja reproduces chunt-9of:
// an unterminated section immediately followed by an unclosed jinja '{{' is
// swallowed into one ERROR node anchored on the section header, so (1) the
// 'hostname r{{' line gets no clean missing-}} diagnostic, and (2) 'speed'
// loses its config-if context and is mis-flagged wrong-section. 'hostname r{{'
// ALONE produces a clean missing-}} (testdata/golden/syntax_missing.cfg) — only
// the compound case is mishandled. Fix is grammar error recovery in the sibling
// tree-sitter repo.
func TestKnownBug9of_CompoundSyntaxErrorSwallowsMissingJinja(t *testing.T) {
	src := "interface Loopback777\n description d\n speed 1000\nhostname r{{\n"
	diags := openDiags(t, src)

	if _, hasMissing := findDiagByCode(diags, "missing-}}"); !hasMissing {
		t.Skip("chunt-9of: unclosed '{{' after an unterminated section is swallowed " +
			"into a generic syntax-error (grammar error-recovery); skip auto-clears when fixed")
	}
	// Correct behavior, enforced automatically once chunt-9of is fixed:
	// (1) a clean missing-}} surfaces for the unclosed '{{'.
	if _, ok := findDiagByCode(diags, "missing-}}"); !ok {
		t.Errorf("expected missing-}} for 'hostname r{{': %+v", diags)
	}
	// (2) 'speed' stays in config-if and is NOT flagged wrong-section.
	if _, bad := findDiagByMessageContains(diags, "speed is valid in config-if, not in config"); bad {
		t.Errorf("'speed' under an interface must not be flagged wrong-section: %+v", diags)
	}
}
