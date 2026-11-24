package cisco_ios_jinja2

import (
	"fmt"
	"math/bits"
	"net/netip"
	"strings"

	"github.com/dgethings/chunt/internal/ast"
	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
	"github.com/dgethings/chunt/internal/symbols"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Subnet overlap diagnostics (chunt-otg.5). Duplicate detection elsewhere is
// text/symbol based; it cannot see that `ip address 10.1.1.1 255.255.255.0`
// on two interfaces is a broken config (different text, same subnet), or
// that 10.1.1.0/24 and 10.1.1.128/25 overlap. This pass collects every
// interface's static primary address plus the FHRP virtual IPs and flags:
//
//   - duplicate-subnet (Error): the same exact subnet on two interfaces
//   - overlapping-subnet (Warning): one subnet strictly inside another —
//     occasionally legitimate (secondary/point-to-point designs), always
//     surprising
//   - duplicate-virtual-ip (Warning): the same HSRP/VRRP/GLBP virtual IP on
//     two interfaces, or twice on one interface in different groups
//
// False-positive controls: secondary addresses and dhcp/unnumbered forms are
// exempt, Jinja-computed addresses are skipped (unresolvable), and two
// interfaces in DIFFERENT VRFs never compare (separate routing tables may
// legitimately reuse a subnet). V1 scope: one document's interface sections;
// workspace cross-file comparison is not wired (the tree pass runs per
// document).

// ifaceAddr is one static primary address on one interface.
type ifaceAddr struct {
	iface string
	vrf   string // "" when the interface has no vrf forwarding
	sub   netip.Prefix
	cmd   scopeCmd
}

// fhrpVIP is one FHRP virtual IP occurrence.
type fhrpVIP struct {
	iface string
	proto string
	group string
	ip    string
	cmd   scopeCmd
}

// runSubnetDiagnostics scans interface sections for static primary addresses
// and FHRP virtual IPs and flags subnet duplication/overlap and virtual-IP
// duplication. Tree-only: no symbol table needed.
func (f *CiscoIOSFeature) runSubnetDiagnostics(doc *document.Document, tree *sitter.Tree) []protocol.Diagnostic {
	if tree == nil || tree.RootNode() == nil {
		return nil
	}
	var addrs []ifaceAddr
	var vips []fhrpVIP
	root := tree.RootNode()
	ast.WalkNamed(root, func(n *sitter.Node) bool {
		if n == root || n.Kind() != "interface_section" {
			return true
		}
		collectInterfaceAddrs(doc, n, &addrs, &vips)
		return true
	})

	var diags []protocol.Diagnostic
	// Exact duplicates and containment: pairwise over primaries.
	for i := range addrs {
		for j := i + 1; j < len(addrs); j++ {
			a, b := addrs[i], addrs[j]
			if a.iface == b.iface {
				continue // same-interface conflicts are redefined-setting's
			}
			if a.vrf != "" && b.vrf != "" && a.vrf != b.vrf {
				continue // separate routing tables may reuse a subnet
			}
			switch {
			case a.sub == b.sub:
				diags = append(diags, subnetDiag(b.cmd, a.cmd, protocol.SeverityError, "duplicate-subnet",
					fmt.Sprintf("subnet %v is configured on both %q and %q; the second interface will not come up as configured",
						a.sub, a.iface, b.iface)))
			case a.sub.Bits() < b.sub.Bits() && a.sub.Contains(b.sub.Addr()):
				diags = append(diags, subnetDiag(b.cmd, a.cmd, protocol.SeverityWarning, "overlapping-subnet",
					fmt.Sprintf("subnet %v (%q) is contained in subnet %v (%q); overlapping prefixes are easy to route wrongly",
						b.sub, b.iface, a.sub, a.iface)))
			case b.sub.Bits() < a.sub.Bits() && b.sub.Contains(a.sub.Addr()):
				diags = append(diags, subnetDiag(b.cmd, a.cmd, protocol.SeverityWarning, "overlapping-subnet",
					fmt.Sprintf("subnet %v (%q) is contained in subnet %v (%q); overlapping prefixes are easy to route wrongly",
						a.sub, a.iface, b.sub, b.iface)))
			}
		}
	}
	// Virtual IP duplicates: same IP twice on one interface (different
	// groups) or across two interfaces.
	for i := range vips {
		for j := i + 1; j < len(vips); j++ {
			a, b := vips[i], vips[j]
			if a.ip != b.ip || (a.iface == b.iface && a.group == b.group) {
				continue
			}
			diags = append(diags, subnetDiag(b.cmd, a.cmd, protocol.SeverityWarning, "duplicate-virtual-ip",
				fmt.Sprintf("%s virtual ip %s is duplicated: group %s on %q and group %s on %q share it; only one group can own the address",
					b.proto, b.ip, a.group, a.iface, b.group, b.iface)))
		}
	}
	return diags
}

// collectInterfaceAddrs walks one interface_section's direct command-like
// children collecting static primaries and FHRP virtual IPs. Negated lines
// are skipped; secondary/dhcp/unnumbered forms and Jinja-computed addresses
// are exempt.
func collectInterfaceAddrs(doc *document.Document, sec *sitter.Node, addrs *[]ifaceAddr, vips *[]fhrpVIP) {
	iface := interfaceName(sec, doc.Content)
	vrf := ""
	for i := uint(0); i < sec.ChildCount(); i++ {
		c := sec.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		kind := c.Kind()
		if kind == "negated_statement" {
			continue
		}
		if kind != "command_line" && !strings.HasSuffix(kind, "_statement") {
			continue
		}
		cmd := scopeCmd{node: c, doc: doc}
		full := commandWords(cmd)
		if len(full) < 3 {
			continue
		}
		// The VRF binding applies to the addresses that follow it.
		if name, ok := interfaceVrfName(full); ok {
			vrf = name
			continue
		}
		if sub, ok := interfacePrimary(full, cmd); ok {
			*addrs = append(*addrs, ifaceAddr{iface: iface, vrf: vrf, sub: sub, cmd: cmd})
			continue
		}
		if vip, ok := fhrpVirtualIP(iface, full, cmd); ok {
			*vips = append(*vips, vip)
		}
	}
}

// interfaceVrfName recognizes the vrf-binding forms (`vrf forwarding NAME`,
// legacy `ip vrf forwarding NAME`) and returns the VRF name.
func interfaceVrfName(full []string) (string, bool) {
	switch {
	case len(full) >= 3 && full[0] == "vrf" && full[1] == "forwarding":
		return full[2], true
	case len(full) >= 4 && full[0] == "ip" && full[1] == "vrf" && full[2] == "forwarding":
		return full[3], true
	}
	return "", false
}

// interfacePrimary recognizes the static primary `ip address <addr> <mask>`
// form and returns the masked network subnet. Secondary addresses are
// additive and exempt; dhcp/unnumbered forms carry no static mask; Jinja
// computed addresses are unresolvable (chunt-otg.5).
func interfacePrimary(full []string, cmd scopeCmd) (netip.Prefix, bool) {
	if len(full) < 4 || full[0] != "ip" || full[1] != "address" {
		return netip.Prefix{}, false
	}
	if wordsContain(full, "secondary") ||
		symbols.HasJinjaExpression(full[2]) || symbols.HasJinjaExpression(full[3]) {
		return netip.Prefix{}, false
	}
	return parseInterfaceSubnet(full[2], full[3])
}

// fhrpVirtualIP recognizes `standby|vrrp|glbp <grp> ip <addr>` and returns
// the virtual IP occurrence. Jinja computed addresses are skipped.
func fhrpVirtualIP(iface string, full []string, cmd scopeCmd) (fhrpVIP, bool) {
	if len(full) < 4 || full[2] != "ip" {
		return fhrpVIP{}, false
	}
	if full[0] != "standby" && full[0] != "vrrp" && full[0] != "glbp" {
		return fhrpVIP{}, false
	}
	if symbols.HasJinjaExpression(full[3]) {
		return fhrpVIP{}, false
	}
	if vip, err := netip.ParseAddr(full[3]); err == nil && vip.IsValid() {
		return fhrpVIP{iface: iface, proto: full[0], group: full[1], ip: full[3], cmd: cmd}, true
	}
	return fhrpVIP{}, false
}

// wordsContain reports whether any word equals s.
func wordsContain(words []string, s string) bool {
	for _, w := range words {
		if w == s {
			return true
		}
	}
	return false
}

// parseInterfaceSubnet normalizes a dotted-quad address + netmask pair into
// the masked network prefix. Non-contiguous and zero masks are rejected (the
// invalid-mask pass owns those).
func parseInterfaceSubnet(addrStr, maskStr string) (netip.Prefix, bool) {
	addr, err := netip.ParseAddr(addrStr)
	if err != nil || !addr.Is4() {
		return netip.Prefix{}, false
	}
	mask, err := netip.ParseAddr(maskStr)
	if err != nil || !mask.Is4() {
		return netip.Prefix{}, false
	}
	mb := mask.As4()
	parBits, sawZero := 0, false
	for _, b := range mb {
		n := bits.OnesCount8(b)
		if sawZero && n != 0 {
			return netip.Prefix{}, false // non-contiguous mask
		}
		if n == 0 {
			sawZero = true
		}
		parBits += n
	}
	if parBits == 0 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, parBits).Masked(), true
}

// interfaceName reads the interface_header's name field, falling back to the
// whole header text.
func interfaceName(sec *sitter.Node, content []byte) string {
	for i := uint(0); i < sec.ChildCount(); i++ {
		c := sec.Child(i)
		if c == nil || !c.IsNamed() || !strings.HasSuffix(c.Kind(), "_header") {
			continue
		}
		if nameNode := c.ChildByFieldName("name"); nameNode != nil {
			return strings.TrimSpace(string(content[nameNode.StartByte():nameNode.EndByte()]))
		}
	}
	return strings.TrimSpace(string(content[sec.StartByte():sec.EndByte()]))
}

// subnetDiag anchors a subnet finding on the later occurrence, with
// relatedInformation back to the earlier one.
func subnetDiag(c, first scopeCmd, severity int, code, msg string) protocol.Diagnostic {
	return protocol.Diagnostic{
		Range:    protocol.LineRange(c.node.StartPosition().Row, c.node.StartPosition().Column, c.node.EndPosition().Column),
		Severity: severity,
		Source:   "chunt",
		Code:     code,
		Message:  msg,
		RelatedInformation: []protocol.DiagnosticRelatedInformation{{
			Location: protocol.Location{
				URI:   first.doc.URI,
				Range: protocol.LineRange(first.node.StartPosition().Row, first.node.StartPosition().Column, first.node.EndPosition().Column),
			},
			Message: "also configured here",
		}},
	}
}
