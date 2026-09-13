package cisco_ios_jinja2

import (
	"strings"

	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/section"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// nodeKindToProtocol maps a dedicated *_statement AST node kind to the single
// routing protocol that owns it. These are unambiguous signals: a node kind
// exists for the command precisely because the grammar can recognize it, so a
// match is a high-confidence protocol attribution. v1 covers bgp/ospf only —
// the grammar's routing_protocol rule matches only "bgp" and "ospf"
// (grammar.js), so "isis" never resolves as an enclosing protocol and
// metric_style_statement will simply never fire (kept for completeness).
// summary_prefix_statement is listed per spec but the grammar does not emit it
// for "summary-address" today (the command splits into command_line + text);
// it is harmless and will activate if/when the grammar gains the rule.
var nodeKindToProtocol = map[string]string{
	"aggregate_address_statement": "bgp",
	"graceful_restart_statement":  "bgp",
	"auto_cost_statement":         "ospf",
	"summary_prefix_statement":    "ospf",
	"metric_style_statement":      "isis",
}

// keywordToProtocol maps the leading identifier of a generic command_line to
// the single routing protocol that owns it, for commands the grammar defers
// (parses as command_line rather than a dedicated *_statement). The list is
// intentionally tiny and conservative: only commands exclusive to ONE
// protocol are mapped, so a command valid in both bgp and ospf (network,
// neighbor, redistribute, router-id, passive-interface, default-information,
// maximum-prefix, distribute-list) is never flagged. "distance ospf" was
// considered but omitted: plain "distance" is multi-protocol, and resolving
// the ospf-specific two-token form reliably is not worth the false-positive
// risk (prefer false negatives). 'area' is not in the keyword DB, so this
// registry is self-contained.
var keywordToProtocol = map[string]string{
	"area": "ospf",
}

// appendProtocolMismatch is the per-node cross-protocol check, folded into
// the single-pass tree collector (chunt-zob, see collectTreeDiagnostics in
// diagnostics.go). It emits an Error when a command owned by one routing
// protocol appears inside another protocol's router (or address-family)
// section — e.g. an OSPF "area" command under "router bgp". This is
// undetectable from the keyword DB alone, which tags both bgp-only and
// ospf-only commands under the generic "config-router" section.
//
// The owning protocol is resolved via the hybrid registry: dedicated
// *_statement node kinds (nodeKindToProtocol) take priority, then the leading
// identifier of a command_line (keywordToProtocol). A command is flagged iff
// its owning protocol is non-empty and differs from the enclosing section's
// protocol (resolved by section.EnclosingSection, which inherits the router's
// protocol through address-family and up through negated_statement). Commands
// shared by both protocols are absent from the registry and are therefore
// never flagged. The collector only calls this on named command-like nodes;
// negated_statement is descended into so "no area ..." is still flagged. v1
// covers bgp <-> ospf only (chunt-pwz).
func (f *CiscoIOSFeature) appendProtocolMismatch(diags *[]protocol.Diagnostic, n *sitter.Node, content []byte) {
	kind := n.Kind()

	// Resolve the command's owning protocol and its display text.
	owning := ""
	cmd := ""
	quote := false
	if p, ok := nodeKindToProtocol[kind]; ok {
		owning = p
		cmd = commandFromKind(kind) // node-kind commands are unquoted in the message
	} else if kind == "command_line" {
		name := leadingIdentifier(n, content)
		if p, ok := keywordToProtocol[name]; ok {
			owning = p
			cmd = name
			quote = true // bare-keyword commands are quoted in the message
		}
	}
	if owning == "" {
		return
	}

	_, encProto := section.EnclosingSection(n, content)
	if encProto == "" || owning == encProto {
		return
	}

	display := protocolDisplay(owning)
	if quote {
		cmd = `"` + cmd + `"`
	}
	*diags = append(*diags, protocol.Diagnostic{
		Range: protocol.LineRange(
			n.StartPosition().Row,
			n.StartPosition().Column,
			n.EndPosition().Column,
		),
		Severity: protocol.SeverityError,
		Source:   "chunt",
		Code:     "protocol-mismatch",
		Message:  cmd + " is " + article(display) + " " + display + " command, not valid under \"router " + encProto + "\"",
	})
}

// leadingIdentifier returns the text of the first named child of n (the
// command's leading identifier), or "" when n has no named children. Used for
// the command_line keyword-text protocol signal.
func leadingIdentifier(n *sitter.Node, content []byte) string {
	if n == nil || n.NamedChildCount() == 0 {
		return ""
	}
	c := n.NamedChild(0)
	if c == nil {
		return ""
	}
	return string(content[c.StartByte():c.EndByte()])
}

// commandFromKind renders the display command name from a *_statement node
// kind: aggregate_address_statement -> "aggregate-address". Deterministic and
// avoids parsing anonymous leaf tokens, which can include inter-token
// whitespace.
func commandFromKind(kind string) string {
	return strings.ReplaceAll(strings.TrimSuffix(kind, "_statement"), "_", "-")
}

// protocolDisplay returns the uppercase display name for a lowercase protocol
// token used in diagnostic messages ("bgp" -> "BGP", "ospf" -> "OSPF").
func protocolDisplay(proto string) string {
	return strings.ToUpper(proto)
}

// article returns the grammatical article for a display word: "an" when it
// begins with a vowel sound (BGP is read letter-by-letter, so "a BGP"; OSPF
// -> "an OSPF"; ISIS -> "an ISIS"), else "a".
func article(display string) string {
	if display == "" {
		return "a"
	}
	switch display[0] {
	case 'A', 'E', 'I', 'O', 'U':
		return "an"
	}
	return "a"
}
