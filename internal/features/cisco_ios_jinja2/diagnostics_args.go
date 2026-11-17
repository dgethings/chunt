package cisco_ios_jinja2

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/dgethings/chunt/internal/keyword"
	"github.com/dgethings/chunt/internal/protocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Snippet-driven argument-value validation (chunt-at0, chunt-j9t).
//
// The keyword DB encodes each command's argument semantics in its snippets:
// `ip address ${1:ip-address} ${2:mask}` declares that argument 2 is a
// subnet mask. The grammar is deliberately structure-only (multi-word
// command identity and argument meaning are resolved here, against the DB),
// so this is where a value like `wibble` in a typed slot gets flagged.
//
// Pipeline per command_line node (folded into the single-pass tree collector,
// see collectTreeDiags in diagnostics.go):
//
//  1. Resolve the command's keyword via a longest-prefix probe over up to
//     maxArgPrefixTokens leading tokens (deeper than firstKeywordFromNode's
//     4-token cap so five-token keywords like "ip nat outside source static"
//     resolve for the curated layer).
//  2. Parse the entry's primary snippet (Snippets[0], the positive form)
//     into ordered slots: literal words, `${N:name}` value placeholders,
//     `${N|a,b|}` choice placeholders, and optional-group brackets.
//  3. Align the slots against the command's tokens (its named children in
//     source order). Literals must match; choices consume one token when
//     present (never validated); a literal mismatch inside an optional `[...]`
//     group skips the whole group; a mismatch outside one aborts — the line
//     follows no known form, so nothing is validated (false negatives
//     preferred over false positives).
//  4. Each mandatory-slot binding whose placeholder name has a validator and
//     whose token is a plain value (not a Jinja `{{ }}` output, whose
//     rendered value cannot be checked statically) is checked; failures emit
//     an Error anchored on the offending token.
//
// Registries — two layers (chunt-j9t):
//
//   - argValidators: the GLOBAL name-keyed layer, registered only for
//     placeholder names that mean exactly one value kind everywhere in the
//     DB (mask family, ipv6-address, mac-address, as-number, vlan-id,
//     seconds/milliseconds/minutes, ...). The DB distinguishes subnet masks
//     (`mask`) from ACL wildcard masks (`wildcard-mask`) and non-IP "masks"
//     (`hash-mask-length`); ambiguous names (`ip-address` — IOS accepts
//     hostnames in several address-ish slots; `port` — `eq www` is legal;
//     `number`) are deliberately NOT global.
//   - keywordArgValidators: the CURATED per-keyword layer for slots that are
//     address-only on specific documented commands (`ip address`'s first
//     argument, `ip route`'s prefix, ...). Curated entries carry an allow
//     set for documented IOS forms absent from the DB (e.g.
//     `ip address negotiated`), keeping the false-negative preference intact.
//     TestKeywordArgValidatorsAgainstDB guards the curated map against
//     keyword-DB drift.
//
// Suppression rules baked into alignment (all pinned by tests):
//   - bindings from slots inside an optional `[...]` group are marked
//     optional and never validated UNLESS a literal in the same group
//     matched first (proving the group is present); greedy alignment cannot
//     otherwise prove the token fills that slot (e.g. `ip route ... permanent`
//     would bind `permanent` into `[${N:distance}]`);
//   - Jinja `{{ }}` output args bind for alignment but are skipped;
//   - dedicated *_statement nodes are not validated (v1): their named
//     children omit the keyword text, so tokens do not align.

// appendInvalidArgument is the per-node argument-value check, folded into the
// single-pass tree collector.
func (f *CiscoIOSFeature) appendInvalidArgument(diags *[]protocol.Diagnostic, n *sitter.Node, content []byte) {
	if n.Kind() != "command_line" {
		return
	}
	kw := resolveCommandKeyword(n, content, f.keyword)
	if kw == "" {
		return
	}
	entry, ok := f.keyword.Lookup(kw)
	if !ok || len(entry.Snippets) == 0 {
		return
	}
	for _, b := range alignSnippetEntry(entry, commandTokens(n, content)) {
		if b.optional {
			continue // optional-slot bindings are greedily aligned; not trustworthy
		}
		v, known := argValidatorFor(kw, b.name)
		if !known || b.token.jinja || b.token.node == nil {
			continue
		}
		if v.allow[b.token.text] || v.check(b.token.text) {
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
// Keyword resolution
// ---------------------------------------------------------------------------

// resolveCommandKeyword resolves a command_line's keyword by longest-prefix
// probe over its leading tokens. It mirrors firstKeywordFromNode
// (diagnostics_section.go) but probes up to maxArgPrefixTokens tokens so
// five-token keywords such as "ip nat outside source static" resolve; the
// curated layer needs them. Entries containing "(" (documentation aliases)
// are excluded, as in firstKeywordFromNode.
const maxArgPrefixTokens = 6

func resolveCommandKeyword(n *sitter.Node, content []byte, kw *keyword.Set) string {
	if n == nil || kw == nil {
		return ""
	}
	var tokens []string
	for i := uint(0); i < n.ChildCount() && len(tokens) < maxArgPrefixTokens; i++ {
		c := n.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		tokens = append(tokens, string(content[c.StartByte():c.EndByte()]))
	}
	for i := len(tokens); i >= 1; i-- {
		candidate := strings.Join(tokens[:i], " ")
		if entry, ok := kw.Lookup(candidate); ok && !strings.Contains(entry.Keyword, "(") {
			return candidate
		}
	}
	if len(tokens) == 0 {
		return ""
	}
	return tokens[0]
}

// ---------------------------------------------------------------------------
// Snippet shape parsing
// ---------------------------------------------------------------------------

// slotKind classifies one whitespace-delimited element of a snippet shape.
type slotKind int

const (
	slotLiteral slotKind = iota // literal word; the document token must match
	slotValue                   // ${N:name} value placeholder; binds one token
	slotChoice                  // ${N|a,b|} keyword alternative; consumes one token, never validated
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
// Choices are keyword alternatives spanning arbitrary text (including
// spaces), so each match is collapsed to the single sentinel token before
// whitespace tokenization; the sentinel then becomes one slotChoice.
var choicePlaceholder = regexp.MustCompile(`\$\{\d+\|[^}]*\}`)

// choiceSentinel cannot occur in scraped snippets; it stands in for a choice
// placeholder between tokenization and slot emission.
const choiceSentinel = "\x00"

// parseSnippetSlots tokenizes a snippet into ordered slots. Choice
// placeholders become single token-consuming slotChoice entries; brackets
// fused to words ("[client-id", "hostname]") expand to open/close markers
// around the word. Placeholder names normalize: `${N:a/b}` keeps only `a`
// (the DB writes compound names like ipv6-prefix/prefix-length while the
// token holds the prefix including its length). Returns nil for a snippet
// containing a malformed placeholder (e.g. `${1:prefix}/`), which suppresses
// validation for that keyword rather than guessing.
func parseSnippetSlots(snippet string) []snippetSlot {
	var slots []snippetSlot
	for _, tok := range strings.Fields(choicePlaceholder.ReplaceAllString(snippet, " "+choiceSentinel+" ")) {
		if tok == choiceSentinel {
			slots = append(slots, snippetSlot{kind: slotChoice})
			continue
		}
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

// placeholderName extracts the normalized name from a tabstop placeholder
// body: "2:mask" -> "mask", "1:ipv6-prefix/prefix-length" -> "ipv6-prefix",
// "5" (bare tabstop, no name) -> "".
func placeholderName(inner string) string {
	if i := strings.IndexByte(inner, ':'); i >= 0 {
		inner = inner[i+1:]
	} else {
		return ""
	}
	if i := strings.IndexByte(inner, '/'); i >= 0 {
		inner = inner[:i]
	}
	return inner
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
// optional marks bindings from slots inside an optional `[...]` group: greedy
// alignment cannot prove the token fills that slot, so validators skip them.
type argBinding struct {
	name     string // placeholder name ("" for bare tabstops)
	token    docToken
	optional bool
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

// alignSnippetEntry aligns toks against a keyword entry's snippet forms in
// document order, returning the bindings of the FIRST form that aligns. An
// entry's Snippets slice carries each documented syntax variant of the
// command (e.g. `ip nat outside source`'s list/pool form and its `static`
// form); a document line typically matches exactly one, so the first
// non-bailing alignment is the right one. All forms bailing (the line
// follows no documented form) yields nil — nothing is validated.
func alignSnippetEntry(entry keyword.Keyword, toks []docToken) []argBinding {
	for _, snip := range entry.Snippets {
		slots := parseSnippetSlots(snip)
		if len(slots) == 0 {
			continue
		}
		if bindings := alignSnippet(slots, toks); bindings != nil {
			return bindings
		}
	}
	return nil
}

// alignSnippet walks slots and document tokens together, binding each value
// placeholder to the token that fills it. Literal slots must match the next
// token (case-insensitively); a mismatch inside an optional group skips the
// whole group (the group is absent from this line); a mismatch outside an
// optional group returns nil — the line follows no snippet form this pass
// understands, so no binding is trusted. Choice slots consume one token when
// present (never validated). Value slots inside an optional group bind but
// are marked optional UNLESS a literal earlier in the same group matched —
// a matched literal proves the group is present on this line, so the
// binding is trusted (e.g. `ip route vrf FOO ...` genuinely fills
// `[vrf ${1:vrf-name}]`). Alignment ends when either side is exhausted:
// trailing tokens (extra args) and trailing slots (omitted optionals) are
// both fine.
func alignSnippet(slots []snippetSlot, toks []docToken) []argBinding {
	var bindings []argBinding
	si, di := 0, 0
	depth := 0 // current optional-group nesting depth
	// present[depth-1]: has a literal/choice in the innermost open group
	// matched yet? A matched literal proves the group is present, so value
	// bindings inside it are not optional.
	var present []bool
	markPresent := func() {
		if depth > 0 {
			present[depth-1] = true
		}
	}
	for si < len(slots) && di < len(toks) {
		s := slots[si]
		switch s.kind {
		case slotOpen:
			depth++
			present = append(present, false)
			si++
		case slotClose:
			if depth > 0 {
				depth--
				present = present[:depth]
			}
			si++
		case slotLiteral:
			if strings.EqualFold(toks[di].text, s.literal) {
				markPresent()
				si++
				di++
			} else if depth > 0 {
				// The optional group this literal belongs to is absent: skip
				// to its matching close bracket, consuming nothing.
				si = skipOptionalGroup(slots, si)
				depth--
				present = present[:depth]
			} else {
				return nil
			}
		case slotChoice:
			// A keyword alternative: consumes one token when present, is never
			// bound or validated. It proves its group present, like a literal.
			markPresent()
			si++
			di++
		case slotValue:
			optional := depth > 0 && !present[depth-1]
			bindings = append(bindings, argBinding{name: s.name, token: toks[di], optional: optional})
			si++
			di++
		}
	}
	return bindings
}

// skipOptionalGroup advances si past the remainder of the optional group
// whose contents si points into (the group's open bracket already consumed),
// up to AND INCLUDING the matching close bracket, handling nested groups.
// Returns the index just after the group's close.
func skipOptionalGroup(slots []snippetSlot, si int) int {
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
	return si
}

// ---------------------------------------------------------------------------
// Value validators
// ---------------------------------------------------------------------------

// argValidator couples a placeholder-name validator with the diagnostic code,
// the human phrase used in its message, and an allow set of documented IOS
// forms absent from the keyword DB that must never be flagged.
type argValidator struct {
	check    func(string) bool
	code     string
	expected string
	allow    map[string]bool
}

var (
	subnetMaskArg = argValidator{
		check:    isSubnetMask,
		code:     "invalid-mask",
		expected: "a subnet mask (e.g. 255.255.255.0)",
	}
	wildcardMaskArg = argValidator{
		check:    isIPv4Address, // any dotted quad: IOS wildcard masks need no contiguity
		code:     "invalid-wildcard-mask",
		expected: "a wildcard mask (e.g. 0.0.0.255)",
	}
	ipv6AddressArg = argValidator{
		check:    isIPv6Address,
		code:     "invalid-ipv6-address",
		expected: "an IPv6 address (e.g. 2001:db8::1)",
	}
	ipv6PrefixArg = argValidator{
		check:    isIPv6Prefix,
		code:     "invalid-ipv6-prefix",
		expected: "an IPv6 prefix (e.g. 2001:db8::/32)",
	}
	macAddressArg = argValidator{
		check:    isMACAddress,
		code:     "invalid-mac-address",
		expected: "a MAC address (e.g. 00aa.bbcc.ddee)",
	}
	asNumberArg = argValidator{
		check:    isASNumber,
		code:     "invalid-as-number",
		expected: "an AS number (e.g. 65001 or 1.1)",
	}
	vlanIDArg = argValidator{
		check:    isVLANID,
		code:     "invalid-vlan-id",
		expected: "a VLAN ID (1-4094)",
	}
	secondsArg = argValidator{
		check:    isUnsignedNumber, // per-command ranges are not in the DB; syntax only
		code:     "invalid-seconds",
		expected: "a number of seconds",
	}
	millisecondsArg = argValidator{
		check:    isUnsignedNumber,
		code:     "invalid-milliseconds",
		expected: "a number of milliseconds",
	}
	minutesArg = argValidator{
		check:    isUnsignedNumber,
		code:     "invalid-minutes",
		expected: "a number of minutes",
	}
	distanceArg = argValidator{
		check:    isDistance,
		code:     "invalid-distance",
		expected: "an administrative distance (1-255)",
	}
	// ipv4AddressCurated is the CURATED ip-address validator: IOS accepts
	// hostnames in several address-ish slots, so plain dotted-quad validation
	// is only applied on commands documented as address-only (per-keyword
	// below). `negotiated` is a documented IOS form whose command page is not
	// in sources.yaml; the allow set keeps it unflagged.
	ipv4AddressCurated = argValidator{
		check:    isIPv4Address,
		code:     "invalid-ip-address",
		expected: "an IPv4 address (e.g. 10.0.0.1)",
		allow:    map[string]bool{"negotiated": true},
	}
)

// argValidators is the GLOBAL placeholder-name registry. Conservative by
// design: only unambiguous names are mapped (see the pass comment). Adding a
// value kind = one entry here plus tests.
var argValidators = map[string]argValidator{
	"mask":         subnetMaskArg,
	"subnet-mask":  subnetMaskArg,
	"network-mask": subnetMaskArg,
	// wildcard-mask's only DB holders ("network (EIGRP)", "distance (IP)")
	// are paren-aliased doc pages excluded from keyword probing, so this
	// entry is currently unreachable in practice; registered so a future DB
	// entry (e.g. a reachable ACL permit) activates it correctly.
	"wildcard-mask":            wildcardMaskArg,
	"ipv6-address":             ipv6AddressArg,
	"ipv6-prefix":              ipv6PrefixArg,
	"mac-address":              macAddressArg,
	"as-number":                asNumberArg,
	"autonomous-system-number": asNumberArg,
	"vlan-id":                  vlanIDArg,
	"seconds":                  secondsArg,
	"milliseconds":             millisecondsArg,
	"minutes":                  minutesArg,
	// distance's slots sit inside optional [...] groups on every reachable
	// keyword (ip route family); optional-slot bindings are suppressed, so
	// this fires nowhere today — registered for when a mandatory holder
	// arrives.
	"distance": distanceArg,
}

// keywordArgValidators is the CURATED per-keyword layer: keyword -> placeholder
// name -> validator, for slots documented as one specific type on that
// command even though the name alone is ambiguous DB-wide (ip-address, and
// names that only mean an address on some commands, like ip route's prefix).
// Curated lookup wins over the global registry.
//
// The ip nat key covers the entry's `static` SYNTAX FORM (one of its several
// snippets): global-ip/local-ip are only bound when the document line aligns
// against the static form, which alignSnippetEntry selects automatically.
var keywordArgValidators = map[string]map[string]argValidator{
	"ip address":            {"ip-address": ipv4AddressCurated},
	"ip route":              {"prefix": ipv4AddressCurated},
	"ip route vrf":          {"prefix": ipv4AddressCurated},
	"ip nat outside source": {"global-ip": ipv4AddressCurated, "local-ip": ipv4AddressCurated},
}

// argValidatorFor resolves the validator for a binding: the curated layer
// (keyword + placeholder name) wins over the global name registry; ip-address
// and friends are only ever curated.
func argValidatorFor(kw, name string) (argValidator, bool) {
	if m, ok := keywordArgValidators[kw]; ok {
		if v, ok := m[name]; ok {
			return v, true
		}
	}
	v, ok := argValidators[name]
	return v, ok
}

// ---------------------------------------------------------------------------
// Predicates
// ---------------------------------------------------------------------------

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

// isIPv4Address reports whether s is a dotted-quad IPv4 address (any octet
// values; IOS does not reserve anything syntactically).
func isIPv4Address(s string) bool {
	_, ok := dottedQuadValue(s)
	return ok
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

// isIPv6Address reports whether s parses as an IPv6 literal. The textual
// colon requirement routes IPv4-mapped forms written in hex (::ffff:...) to
// the true branch while plain dotted quads fail.
func isIPv6Address(s string) bool {
	return net.ParseIP(s) != nil && strings.Contains(s, ":")
}

// isIPv6Prefix reports whether s parses as an IPv6 prefix (address/length).
// Host bits set are accepted (conservative: IOS normalizes on some commands).
func isIPv6Prefix(s string) bool {
	_, inet, err := net.ParseCIDR(s)
	return err == nil && inet.IP.To4() == nil
}

// macAddressPattern is IOS's dotted-triplet MAC form (hhhh.hhhh.hhhh) —
// the syntax the command reference documents for mac-address arguments.
var macAddressPattern = regexp.MustCompile(`^[0-9a-fA-F]{4}\.[0-9a-fA-F]{4}\.[0-9a-fA-F]{4}$`)

// isMACAddress reports whether s matches IOS's dotted-triplet MAC syntax.
func isMACAddress(s string) bool {
	return macAddressPattern.MatchString(s)
}

// isASNumber reports whether s is a valid AS number: plain decimal
// (0-4294967295, 4-byte AS space) or AS-dot x.y with two 16-bit parts.
func isASNumber(s string) bool {
	if a, b, ok := strings.Cut(s, "."); ok {
		x, err1 := strconv.ParseUint(a, 10, 32)
		y, err2 := strconv.ParseUint(b, 10, 32)
		return err1 == nil && err2 == nil && x <= 65535 && y <= 65535
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return err == nil && v <= 4294967295
}

// isVLANID reports whether s is a configurable VLAN ID (1-4094).
func isVLANID(s string) bool {
	v, err := strconv.ParseUint(s, 10, 32)
	return err == nil && v >= 1 && v <= 4094
}

// isUnsignedNumber reports whether s is a plain non-negative integer.
// Per-command ranges are not carried by the snippets, so duration-style
// placeholders validate syntax only.
func isUnsignedNumber(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// isDistance reports whether s is a valid administrative distance (1-255).
func isDistance(s string) bool {
	v, err := strconv.ParseUint(s, 10, 32)
	return err == nil && v >= 1 && v <= 255
}
