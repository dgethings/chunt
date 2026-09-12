package cisco_ios_jinja2

import (
	"context"
	"fmt"
	"testing"

	"github.com/dgethings/chunter/internal/document"
	"github.com/dgethings/chunter/internal/section"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// TestProbeCollision confirms the area/area-password hyphenation collision and
// checks candidate-signal exclusivity across protocols.
func TestProbeCollision(t *testing.T) {
	// 1. Does area-password inside router isis get falsely flagged today?
	runDiag := func(src string) []string {
		f := New()
		defer f.Close()
		doc := document.New("file:///t.ios.j2", "cisco_ios_jinja2", 1, []byte(src))
		ds, _ := f.DidOpen(context.Background(), doc, nil)
		var msgs []string
		for _, d := range ds {
			if d.Code == "protocol-mismatch" {
				msgs = append(msgs, d.Message)
			}
		}
		return msgs
	}
	fmt.Println("--- collision: area-password inside router isis ---")
	fmt.Println(runDiag("router isis FOO\n area-password SECRETHASH\n!\n"))
	fmt.Println("--- collision: area inside router isis (real ospf cmd, SHOULD flag) ---")
	fmt.Println(runDiag("router isis FOO\n area 0 range 10.0.0.0 255.0.0.0\n!\n"))

	// 2. negated_statement structure for "no area"
	fmt.Println("\n--- negated: no area inside router bgp ---")
	{
		f := New()
		defer f.Close()
		src := "router bgp 100\n no area 0 range 10.0.0.0 255.0.0.0\n!\n"
		doc := document.New("file:///t.ios.j2", "cisco_ios_jinja2", 1, []byte(src))
		tree := f.parser.Parse(doc.Content, nil)
		defer tree.Close()
		dump(t, tree.RootNode(), doc.Content, 0)
	}

	// 3. exclusivity: does eigrp_router_statement / version_statement fire under
	// the WRONG protocol? (if a dedicated node kind fires under another proto it
	// is still correctly attributed; we check it doesn't fire for a SHARED use)
	for _, c := range []struct{ proto, cmd string }{
		{"bgp", "eigrp stub"},
		{"ospf", "version 2"},
		{"bgp", "version 2"},
		{"eigrp", "version 2"},
		{"ospf", "net 49.0001"},
		{"bgp", "net 49.0001"},
	} {
		src := "router " + c.proto + " 100\n " + c.cmd + "\n!\n"
		f := New()
		defer f.Close()
		doc := document.New("file:///t.ios.j2", "cisco_ios_jinja2", 1, []byte(src))
		tree := f.parser.Parse(doc.Content, nil)
		rsec := findFirst(tree.RootNode(), "router_section")
		var k string
		if rsec != nil {
			for i := uint(0); i < rsec.NamedChildCount(); i++ {
				ch := rsec.NamedChild(i)
				if ch.Kind() == "router_header" || ch.Kind() == "eos" {
					continue
				}
				k = ch.Kind()
				break
			}
		}
		_, proto := section.EnclosingSection(rsec, doc.Content)
		tree.Close()
		fmt.Printf("exclusivity: router %-5s | %-16s -> kind=%-22s encProto=%q\n", c.proto, c.cmd, k, proto)
	}
}

func dump(t *testing.T, n *sitter.Node, content []byte, depth int) {
	t.Helper()
	ind := ""
	for i := 0; i < depth; i++ {
		ind += "  "
	}
	txt := string(content[n.StartByte():n.EndByte()])
	if len(txt) > 45 {
		txt = txt[:45] + "…"
	}
	for i := 0; i < len(txt); i++ {
		if txt[i] == '\n' {
			txt = txt[:i] + "⏎"
		}
	}
	fmt.Printf("%s%s [%s]\n", ind, n.Kind(), txt)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		dump(t, n.NamedChild(i), content, depth+1)
	}
}
