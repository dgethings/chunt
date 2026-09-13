package cisco_ios_jinja2_test

import (
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/protocol"
)

// findDiagBySeverityPrefix returns the first diagnostic whose severity matches
// and whose Code has the given prefix, reporting whether one was found.
func findDiagBySeverityPrefix(diags []protocol.Diagnostic, severity int, codePrefix string) (protocol.Diagnostic, bool) {
	for _, d := range diags {
		if d.Severity == severity && strings.HasPrefix(d.Code, codePrefix) {
			return d, true
		}
	}
	return protocol.Diagnostic{}, false
}

func countSyntaxDiags(diags []protocol.Diagnostic) int {
	var n int
	for _, d := range diags {
		if d.Code == "syntax-error" || strings.HasPrefix(d.Code, "missing-") {
			n++
		}
	}
	return n
}

// TestSyntaxDiagnostics_MissingEosAtEOF is the headline case: an interface
// section without its terminating `!` produces a Warning anchored on the
// section header naming the section.
func TestSyntaxDiagnostics_MissingEosAtEOF(t *testing.T) {
	src := "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n"
	diags := openDiags(t, src)

	d, found := findDiagBySeverityPrefix(diags, protocol.SeverityWarning, "missing-eos")
	if !found {
		t.Fatalf("expected missing-eos Warning; got %d diags: %+v", len(diags), diags)
	}
	if !strings.Contains(d.Message, `missing its terminating`) {
		t.Errorf("message should mention the missing terminator: %q", d.Message)
	}
	if !strings.Contains(d.Message, `interface Gi0/0`) {
		t.Errorf("message should name the section header: %q", d.Message)
	}
	if d.Source != "chunt" {
		t.Errorf("source: got %q, want \"chunt\"", d.Source)
	}
	// Anchored on the header (line 0), spanning "interface Gi0/0".
	if d.Range.Start.Line != 0 || d.Range.End.Line != 0 {
		t.Errorf("expected range on line 0; got %d-%d", d.Range.Start.Line, d.Range.End.Line)
	}
	if d.Range.Start.Character != 0 || d.Range.End.Character == 0 {
		t.Errorf("expected non-empty range starting at col 0; got %d-%d", d.Range.Start.Character, d.Range.End.Character)
	}
}

// TestSyntaxDiagnostics_MissingEosBeforeNextSection confirms a section that is
// followed directly by another section (no `!` between) still reports the
// missing terminator on the correct (outer) header.
func TestSyntaxDiagnostics_MissingEosBeforeNextSection(t *testing.T) {
	src := "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\nrouter bgp 100\n neighbor 1.1.1.1 remote-as 200\n!\n"
	diags := openDiags(t, src)

	d, found := findDiagBySeverityPrefix(diags, protocol.SeverityWarning, "missing-eos")
	if !found {
		t.Fatalf("expected missing-eos Warning; got %d diags: %+v", len(diags), diags)
	}
	if !strings.Contains(d.Message, `interface Gi0/0`) {
		t.Errorf("should flag the interface section (outer): %q", d.Message)
	}
	if d.Range.Start.Line != 0 {
		t.Errorf("should anchor on line 0 (interface header); got line %d", d.Range.Start.Line)
	}
}

// TestSyntaxDiagnostics_MissingEosAcrossSectionTypes verifies each section
// kind surfaces its own header text in the message.
func TestSyntaxDiagnostics_MissingEosAcrossSectionTypes(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantHeader string
	}{
		{"interface", "interface Gi0/0\n speed 1000\n", "interface Gi0/0"},
		{"route-map", "route-map RM-OUT permit 10\n match ip address ACL\n", "route-map RM-OUT permit 10"},
		{"class-map", "class-map match-any VOICE\n match ip address ACL\n", "class-map match-any VOICE"},
		{"policy-map", "policy-map QOS\n description foo\n", "policy-map QOS"},
		{"vlan", "vlan 99\n name VOICE\n", "vlan 99"},
		{"router", "router ospf 1\n network 10.0.0.0 0.0.0.255 area 0\n", "router ospf 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			d, found := findDiagBySeverityPrefix(diags, protocol.SeverityWarning, "missing-eos")
			if !found {
				t.Fatalf("expected missing-eos Warning for %s; got %d diags: %+v", tc.name, len(diags), diags)
			}
			if !strings.Contains(d.Message, tc.wantHeader) {
				t.Errorf("message for %s: got %q, want header %q", tc.name, d.Message, tc.wantHeader)
			}
		})
	}
}

// TestSyntaxDiagnostics_ErrorNode flags an unparseable construct (an
// unterminated jinja `{% if %}` with no matching `{% endif %}`) as an Error.
func TestSyntaxDiagnostics_ErrorNode(t *testing.T) {
	src := "{% if foo %}\nhostname r1\n"
	diags := openDiags(t, src)

	d, found := findDiagBySeverityPrefix(diags, protocol.SeverityError, "syntax-error")
	if !found {
		t.Fatalf("expected syntax-error diagnostic; got %d diags: %+v", len(diags), diags)
	}
	if !strings.Contains(d.Message, `syntax error near`) {
		t.Errorf("message: got %q", d.Message)
	}
	if !strings.Contains(d.Message, `{% if foo %}`) {
		t.Errorf("message should quote the offending snippet: %q", d.Message)
	}
	// ERROR spans multiple lines; range must be clamped to a non-empty span on
	// the first line.
	if d.Range.Start.Line != 0 {
		t.Errorf("expected range on line 0; got line %d", d.Range.Start.Line)
	}
	if d.Range.End.Character <= d.Range.Start.Character {
		t.Errorf("expected non-empty range; got %d-%d", d.Range.Start.Character, d.Range.End.Character)
	}
}

// TestSyntaxDiagnostics_MissingToken (non-eos) uses an unterminated jinja
// output `{{` to force a MISSING `}}`, which must be an Error naming what is
// missing.
func TestSyntaxDiagnostics_MissingToken(t *testing.T) {
	src := "hostname r{{\n"
	diags := openDiags(t, src)

	d, found := findDiagBySeverityPrefix(diags, protocol.SeverityError, "missing-")
	if !found {
		t.Fatalf("expected missing-* Error diagnostic; got %d diags: %+v", len(diags), diags)
	}
	if !strings.Contains(d.Message, `missing`) {
		t.Errorf("message should say what is missing: %q", d.Message)
	}
	if d.Severity != protocol.SeverityError {
		t.Errorf("non-eos MISSING must be an Error; got %d", d.Severity)
	}
}

// TestSyntaxDiagnostics_UnclosedJinjaSwallowedByError guards chunt-9of
// symptom 1: when an unterminated section is immediately followed by an
// unclosed jinja `{{`, error recovery wraps both into a single ERROR node and
// the bare `{{` token never forms an `output` node, so there is no MISSING
// `}}` for the pass to report. Without the recovery scan the user sees only a
// generic "syntax error near <section header>" anchored lines earlier and
// nothing on the actual `{{` line. The scan must recover a missing-}} Error
// anchored on the `{{` line.
func TestSyntaxDiagnostics_UnclosedJinjaSwallowedByError(t *testing.T) {
	src := "router ospf 1\n network 10.0.0.0 0.0.0.255 area 0\nhostname r{{\n"
	diags := openDiags(t, src)

	const jinjaLine = 2 // "hostname r{{"
	var seen bool
	for _, d := range diags {
		if d.Code == "missing-}}" {
			seen = true
			if d.Range.Start.Line != jinjaLine {
				t.Errorf("missing-}} should anchor on line %d (the `{{`); got line %d", jinjaLine, d.Range.Start.Line)
			}
			if d.Severity != protocol.SeverityError {
				t.Errorf("recovered missing-}} must be an Error; got %d", d.Severity)
			}
		}
	}
	if !seen {
		t.Fatalf("expected a recovered missing-}} diagnostic on line %d; got %d diags: %+v", jinjaLine, len(diags), diags)
	}
}

// TestSyntaxDiagnostics_ClosedJinjaInErrorNotDoubleReported confirms the
// Jinja-recovery scan is balanced: a jinja expression that IS closed does not
// earn a spurious missing-closer diagnostic even when wrapped inside an ERROR
// node (only truly unmatched openers are flagged).
func TestSyntaxDiagnostics_ClosedJinjaInErrorNotDoubleReported(t *testing.T) {
	// Unterminated router section followed by a CLOSED jinja output. The
	// router section + following content form an ERROR node, but `{{ x }}` is
	// balanced, so no missing-}} should be emitted.
	src := "router ospf 1\n network 10.0.0.0 0.0.0.255 area 0\nhostname r{{ x }}\n"
	diags := openDiags(t, src)
	for _, d := range diags {
		if strings.HasPrefix(d.Code, "missing-}") {
			t.Errorf("balanced `{{ x }}` must not produce a missing-closer diagnostic: %+v", d)
		}
	}
}

// TestSyntaxDiagnostics_CleanFileProducesNone ensures well-formed input
// produces zero syntax/missing diagnostics (regression guard for the
// root.HasError() gate and against over-eager flagging of existing fixtures).
func TestSyntaxDiagnostics_CleanFileProducesNone(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"single section", "interface Gi0/0\n ip address 10.0.0.1 255.255.255.0\n!\n"},
		{"two sections", "interface Gi0/0\n speed 1000\n!\nrouter bgp 100\n neighbor 1.1.1.1 remote-as 200\n!\n"},
		{"bare command", "hostname r1\n"},
		{"negated ref", "interface Gi0/0\n no ip access-group FOO in\n!\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := openDiags(t, tc.src)
			if n := countSyntaxDiags(diags); n != 0 {
				t.Errorf("expected 0 syntax diagnostics; got %d: %+v", n, diags)
			}
		})
	}
}

// TestSyntaxDiagnostics_MissingEosSeverityIsWarning explicitly asserts the
// eos case is a Warning (not an Error) while a stray MISSING token is an
// Error, codifying the severity split.
func TestSyntaxDiagnostics_SeveritySplit(t *testing.T) {
	// Missing `!` -> Warning.
	wDiags := openDiags(t, "interface Gi0/0\n speed 1000\n")
	w, found := findDiagBySeverityPrefix(wDiags, protocol.SeverityWarning, "missing-eos")
	if !found {
		t.Fatalf("missing `!` should be a Warning: %+v", wDiags)
	}
	if w.Code != "missing-eos" {
		t.Errorf("code: got %q, want missing-eos", w.Code)
	}
	// Missing `}}` -> Error.
	eDiags := openDiags(t, "hostname r{{\n")
	e, found := findDiagBySeverityPrefix(eDiags, protocol.SeverityError, "missing-")
	if !found {
		t.Fatalf("missing `}}` should be an Error: %+v", eDiags)
	}
	if e.Code == "missing-eos" {
		t.Errorf("missing `}}` must not be classified as missing-eos: %+v", e)
	}
}
