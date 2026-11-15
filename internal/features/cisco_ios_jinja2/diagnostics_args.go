package cisco_ios_jinja2

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dgethings/chunt/internal/protocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Snippet-driven argument-value validation (chunt-at0).
//
// The keyword DB encodes each command's argument semantics in its snippets:
// `ip address ${1:ip-address} ${2:mask}` declares that argument 2 is a
// subnet mask. The grammar is deliberately structure-only (multi-word
// command identity and argument meaning are resolved here, against the DB),
// so this pass is where a value like `wibble` in a mask slot gets flagged.
//
// Pipeline per command_line node (folded into the single-pass tree collector,
// see collectTreeDiags in diagnostics.go):
//
//  1. Resolve the command's keyword via the existing longest-prefix probe
//     (firstKeywordFromNode) — `ip address 10.0.0.1 wibble` resolves to
//     "ip address".
//  2. Parse the entry's primary snippet (Snippets[0], the positive form)
//     into ordered slots: literal words, `${N:name}` value placeholders,
//     and optional-group brackets. Choice placeholders (`${N|a,b|}`) are
//     stripped: they are keyword alternatives, not values.
//  3. Align the slots against the command's tokens (its named children in
//     source order). Literal words must match; a mismatch inside an optional
//     `[...]` group skips the whole group (the group is absent); a mismatch
//     outside one aborts — the line follows no known form, so nothing is
//     validated (false negatives preferred over false positives).
//  4. Each binding whose placeholder name has a registered validator and
//     whose token is a plain value (not a Jinja `{{ }}` output, whose
//     rendered value cannot be checked statically) is checked; failures emit
//     an Error anchored on the offending token.
//
// The validator registry is intentionally tiny and conservative, mirroring
// keywordToProtocol's philosophy: only placeholder names whose meaning is
// unambiguous are mapped. The DB distinguishes subnet masks (`mask`,
// `subnet-mask`, `network-mask`) from ACL wildcard masks (`wildcard-mask`)
// and non-IP "masks" (`hash-mask-length`, `bit-mask-pattern`); only the
// subnet-mask family is registered today. `wildcard-mask` is deliberately
// absent: its slots legally hold `any`/`host`-adjacent forms in ACL lines,
// and IOS accepts non-contiguous wildcard bits, so only a plain dotted-quad
// check would be possible there — not worth the false-positive surface.

// appendInvalidArgument is the per-node argument-value check, folded into the
// single-pass tree collector. v1 validates command_line nodes only: dedicated
// *_statement nodes omit the keyword text from their named children, so their
// tokens do not align with snippet shapes (pinned by a test).
func (f *CiscoIOSFeature) appendInvalidArgument(diags *[]protocol.Diagnostic, n *sitter.Node, content []byte) {
	if n.Kind() != "command_line" {
		return
	}
	kw := firstKeywordFromNode(n, content, f.keyword)
	if kw == "" {
		return
	}
	entry, ok := f.keyword.Lookup(kw)
	if !ok || len(entry.Snippets) == 0 {
		return
	}
	slots := parseSnippetSlots(entry.Snippets[0])
	if len(slots) == 0 {
		return
	}
	for _, b := range alignSnippet(slots, commandTokens(n, content)) {
		v, known := argValidators[b.name]
		if !known || b.token.jinja || b.token.node == nil {
			continue
		}
		if v.check(b.token.text) {
			continue
		}
		node := b.token.node
		*diags = append(*diags, protocol.Diagnostic{
			Range: protocol.LineRange(
				node.StartPosition().Row,
				node.StartPosition().Column,
				node.EndPosition().Column,
			),
			Severity: protocol.SeverityError,
			Source:   "chunt",
			Code:     v.code,
			Message:  fmt.Sprintf("expected %s, got %q", v.expected, b.token.text),
		})
	}
}

// ---------------------------------------------------------------------------
// Snippet shape parsing
// ---------------------------------------------------------------------------

// slotKind classifies one whitespace-delimited element of a snippet shape.
type slotKind int

const (
	slotLiteral slotKind = iota // literal word; the document token must match
	slotValue                   // ${N:name} value placeholder; binds one token
	slotOpen                    // '[' opening an optional group
	slotClose                   // ']' closing an optional group
)

// snippetSlot is one ordered element of a parsed snippet shape.
type snippetSlot struct {
	kind    slotKind
	literal string // slotLiteral: the word to match (case-insensitively)
	name    string // slotValue: the placeholder name ("" for bare ${N})
}

// choicePlaceholder matches a VS Code choice placeholder `${N|alt,alt|}`.
// Choices are keyword alternatives, not values, and are removed before
// tokenizing so their embedded spaces cannot break slot order.
var choicePlaceholder = regexp.MustCompile(`\$\{\d+\|[^}]*\}`)

// parseSnippetSlots tokenizes a snippet into ordered slots. Choice
// placeholders are stripped; brackets fused to words ("[client-id",
// "hostname]") expand to open/close markers around the word. Returns nil for
// a snippet containing a malformed placeholder (e.g. `${1:prefix}/`), which
// suppresses validation for that keyword rather than guessing.
func parseSnippetSlots(snippet string) []snippetSlot {
	var slots []snippetSlot
	for _, tok := range strings.Fields(choicePlaceholder.ReplaceAllString(snippet, " ")) {
		for strings.HasPrefix(tok, "[") {
			slots = append(slots, snippetSlot{kind: slotOpen})
			tok = tok[1:]
		}
		closes := 0
		for strings.HasSuffix(tok, "]") {
			closes++
			tok = tok[:len(tok)-1]
		}
		switch {
		case tok == "":
			// "[]" or a lone bracket: no word content.
		case strings.HasPrefix(tok, "${"):
			if !strings.HasSuffix(tok, "}") {
				return nil
			}
			inner := tok[2 : len(tok)-1]
			if strings.ContainsAny(inner, "|$") {
				return nil
			}
			slots = append(slots, snippetSlot{kind: slotValue, name: placeholderName(inner)})
		case strings.Contains(tok, "$"):
			return nil // stray '$' — not a form we understand
		default:
			slots = append(slots, snippetSlot{kind: slotLiteral, literal: tok})
		}
		for ; closes > 0; closes-- {
			slots = append(slots, snippetSlot{kind: slotClose})
		}
	}
	return slots
}

// placeholderName extracts the name from a tabstop placeholder body:
// "2:mask" -> "mask", "5" -> "" (bare tabstop).
func placeholderName(inner string) string {
	if i := strings.IndexByte(inner, ':'); i >= 0 {
		return inner[i+1:]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Slot/document alignment
// ---------------------------------------------------------------------------

// docToken is one named child of a command_line (the leading identifier or an
// arg) as a matchable token. jinja marks a `{{ ... }}` output arg: it fills
// the slot for alignment purposes but its rendered value cannot be validated
// statically, so validators skip it.
type docToken struct {
	text  string
	jinja bool
	node  *sitter.Node // backing node, for anchoring diagnostics
}

// argBinding is one value placeholder bound to the document token filling it.
type argBinding struct {
	name  string // placeholder name ("" for bare tabstops)
	token docToken
}

// commandTokens returns the command-like node's named children in source
// order as tokens: the leading identifier plus every arg (value or Jinja
// output).
func commandTokens(n *sitter.Node, content []byte) []docToken {
	var toks []docToken
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		toks = append(toks, docToken{
			text:  string(content[c.StartByte():c.EndByte()]),
			jinja: c.Kind() == "output",
			node:  c,
		})
	}
	return toks
}

// alignSnippet walks slots and document tokens together, binding each value
// placeholder to the token that fills it. Literal slots must match the next
// token (case-insensitively); a mismatch inside an optional group skips the
// whole group (the group is absent from this line); a mismatch outside an
// optional group returns nil — the line follows no snippet form this pass
// understands, so no binding is trusted. Alignment ends when either side is
// exhausted: trailing tokens (extra args) and trailing slots (omitted
// optionals) are both fine.
func alignSnippet(slots []snippetSlot, toks []docToken) []argBinding {
	var bindings []argBinding
	si, di := 0, 0
	depth := 0 // current optional-group nesting depth
	for si < len(slots) && di < len(toks) {
		s := slots[si]
		switch s.kind {
		case slotOpen:
			depth++
			si++
		case slotClose:
			if depth > 0 {
				depth--
			}
			si++
		case slotLiteral:
			if strings.EqualFold(toks[di].text, s.literal) {
				si++
				di++
			} else if depth > 0 {
				// The optional group this literal belongs to is absent: skip
				// to its matching close bracket, consuming nothing.
				nest := 1
				for si < len(slots) && nest > 0 {
					si++
					switch slots[si-1].kind {
					case slotOpen:
						nest++
					case slotClose:
						nest--
					}
				}
				depth--
			} else {
				return nil
			}
		case slotValue:
			bindings = append(bindings, argBinding{name: s.name, token: toks[di]})
			si++
			di++
		}
	}
	return bindings
}

// ---------------------------------------------------------------------------
// Value validators
// ---------------------------------------------------------------------------

// argValidator couples a placeholder-name validator with the diagnostic code
// and the human phrase used in its message.
type argValidator struct {
	check    func(string) bool
	code     string
	expected string
}

// subnetMaskArg is the shared subnet-mask validator: a dotted quad whose 32
// bits are a contiguous run of leading ones (255.255.255.0, 0.0.0.0,
// 255.255.255.255, ...).
var subnetMaskArg = argValidator{
	check:    isSubnetMask,
	code:     "invalid-mask",
	expected: "a subnet mask (e.g. 255.255.255.0)",
}

// argValidators maps snippet placeholder names to validators. Conservative by
// design: only unambiguous subnet-mask names are registered (see the pass
// comment). Adding a value kind = one entry here plus tests.
var argValidators = map[string]argValidator{
	"mask":         subnetMaskArg,
	"subnet-mask":  subnetMaskArg,
	"network-mask": subnetMaskArg,
}

// dottedQuadValue parses s as four dot-separated decimal octets 0-255,
// returning the composed 32-bit value. ok is false for any other shape.
func dottedQuadValue(s string) (val uint32, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return 0, false
	}
	for _, p := range parts {
		o, err := strconv.ParseUint(p, 10, 8)
		if err != nil {
			return 0, false
		}
		val = val<<8 | uint32(o)
	}
	return val, true
}

// isSubnetMask reports whether s is a dotted quad forming a legal subnet
// mask: the 32-bit value must be a contiguous run of leading one-bits. That
// holds iff the complement (~m) is a run of trailing ones, iff ~m+1 is a
// power of two — checked branch-free as inv&(inv+1)==0 (0 maps to 0, so the
// all-ones and all-zero masks are both accepted, as on IOS).
func isSubnetMask(s string) bool {
	m, ok := dottedQuadValue(s)
	if !ok {
		return false
	}
	inv := ^m
	return inv&(inv+1) == 0
}
