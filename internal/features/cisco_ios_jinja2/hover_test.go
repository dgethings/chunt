package cisco_ios_jinja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/protocol"
)

func TestHoverHostname(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: !
	//       line 1: ! version 26.1.0
	//       line 2: !
	//       line 3: hostname test
	//       line 4: !
	content := []byte("!\n! version 26.1.0\n!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover on "hostname" — line 3, character 0 (zero-based)
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 3, Character: 0})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	// hostname now carries Usage/Examples/Defaults/Command-History data, so the
	// hover is a structured Markdown document whose leading text is still the
	// description (chunt-97u). Assert the description is present and that the
	// rich data promoted it to Markdown.
	desc := "To specify or modify the hostname for the network server, use the hostname command in global configuration mode."
	if !strings.Contains(result.Contents.Value, desc) {
		t.Errorf("hover value missing description %q; got prefix %q", desc, truncForLog(result.Contents.Value, 120))
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Errorf("hover kind = %q, want %q (hostname has rich data)", result.Contents.Kind, protocol.Markdown)
	}
}

func TestHoverNoKeyword(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	content := []byte("!\n! version 26.1.0\n!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover on the version line — not a documented keyword
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 1, Character: 1})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	// Currently returns a zero-value HoverResult for unknown nodes; adjust
	// if you change nodeByPosition to return nil for misses.
	if result != nil && result.Contents.Value != "" {
		t.Errorf("expected empty/nil hover for undocumented node, got %q", result.Contents.Value)
	}
}

func TestHoverHostnameKeywordEnd(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: !
	//       line 1: ! version 26.1.0
	//       line 2: !
	//       line 3: hostname test
	//       line 4: !
	content := []byte("!\n! version 26.1.0\n!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover at the END of "hostname" — line 3, character 8 (zero-based).
	// This boundary resolves to the hostname_statement node, not the leaf.
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 3, Character: 8})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	desc := "To specify or modify the hostname for the network server, use the hostname command in global configuration mode."
	if !strings.Contains(result.Contents.Value, desc) {
		t.Errorf("hover value missing description %q; got prefix %q", desc, truncForLog(result.Contents.Value, 120))
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Errorf("hover kind = %q, want %q (hostname has rich data)", result.Contents.Kind, protocol.Markdown)
	}
}

func TestHoverInterface(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: !
	//       line 1: interface GigabitEthernet0/0
	//       line 2: !
	content := []byte("!\ninterface GigabitEthernet0/0\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)

	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	// Hover on "interface" — line 1, character 0 (zero-based)
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}

	desc := "To configure an interface type and to enter interface configuration mode, use the interface command in the appropriate configuration mode."
	if !strings.Contains(result.Contents.Value, desc) {
		t.Errorf("hover value missing description %q; got prefix %q", desc, truncForLog(result.Contents.Value, 120))
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Errorf("hover kind = %q, want %q (interface has rich data)", result.Contents.Kind, protocol.Markdown)
	}
}

// TestHoverRichMarkdownHostname verifies the rich hover for a command that
// carries the full scraped dataset: the Markdown must include every section
// label and the description as the leading text (chunt-97u).
func TestHoverRichMarkdownHostname(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	content := []byte("!\nhostname test\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}
	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected hover result, got nil")
	}
	if result.Contents.Kind != protocol.Markdown {
		t.Fatalf("hover kind = %q, want %q", result.Contents.Kind, protocol.Markdown)
	}
	for _, want := range []string{
		"**Usage Guidelines**",
		"**Examples**",
		"**Defaults**",
		"**Command History**",
		"Introduced in release 12.2",
	} {
		if !strings.Contains(result.Contents.Value, want) {
			t.Errorf("hover Markdown missing %q; got prefix %q", want, truncForLog(result.Contents.Value, 200))
		}
	}
	// Description must lead the document.
	if !strings.HasPrefix(result.Contents.Value, "To specify or modify the hostname") {
		t.Errorf("description is not the leading text; got prefix %q", truncForLog(result.Contents.Value, 80))
	}
}

// TestHoverMultiWordCommand covers longest-prefix resolution for generic
// command_line nodes: `ip address ...` must resolve to the multi-word DB key
// "ip address", not the bare first token "ip" (chunt-92f). Hovering anywhere
// on the command — leading token, mid-token, or over an argument — resolves
// the same keyword.
func TestHoverMultiWordCommand(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: !
	//       line 1: interface GigabitEthernet0/0
	//       line 2:  ip address 10.0.0.1 255.255.255.0
	//       line 3: !
	content := []byte("!\ninterface GigabitEthernet0/0\n ip address 10.0.0.1 255.255.255.0\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	want := "use the ip address command in interface configuration mode"
	for _, pos := range []protocol.Position{
		{Line: 2, Character: 1},  // on "ip"
		{Line: 2, Character: 3},  // mid-token inside "ip"
		{Line: 2, Character: 4},  // on "address"
		{Line: 2, Character: 12}, // on the address argument
	} {
		result, err := f.Hover(context.Background(), doc, pos)
		if err != nil {
			t.Fatalf("Hover@%d:%d failed: %v", pos.Line, pos.Character, err)
		}
		if result == nil {
			t.Errorf("Hover@%d:%d = nil, want ip address hover", pos.Line, pos.Character)
			continue
		}
		if !strings.Contains(result.Contents.Value, want) {
			t.Errorf("Hover@%d:%d missing %q; got prefix %q", pos.Line, pos.Character, want, truncForLog(result.Contents.Value, 120))
		}
	}
}

// TestHoverMultiWordHeader covers longest-prefix resolution for section
// headers: `router bgp 65000` parses as a router_header whose first token is
// "router"; the hover must resolve the multi-word key "router bgp".
func TestHoverMultiWordHeader(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	content := []byte("!\nrouter bgp 65000\n neighbor 10.0.0.2 remote-as 65000\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 1, Character: 0})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected router bgp hover, got nil")
	}
	want := "To configure the Border Gateway Protocol (BGP) routing process"
	if !strings.Contains(result.Contents.Value, want) {
		t.Errorf("hover value missing %q; got prefix %q", want, truncForLog(result.Contents.Value, 120))
	}
}

// TestHoverNegatedCommand verifies negated statements resolve their wrapped
// command: `no ip address ...` hovers as "ip address", both when the cursor is
// on the command and on the `no` token itself.
func TestHoverNegatedCommand(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	//       line 0: interface GigabitEthernet0/0
	//       line 1:  ip address 10.0.0.1 255.255.255.0
	//       line 2:  no ip address 10.0.0.2 255.255.255.255
	content := []byte("interface GigabitEthernet0/0\n ip address 10.0.0.1 255.255.255.0\n no ip address 10.0.0.2 255.255.255.255\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	want := "use the ip address command in interface configuration mode"
	for _, pos := range []protocol.Position{
		{Line: 2, Character: 4}, // on "ip" of the negated command
		{Line: 2, Character: 1}, // on the "no" token itself
	} {
		result, err := f.Hover(context.Background(), doc, pos)
		if err != nil {
			t.Fatalf("Hover@%d:%d failed: %v", pos.Line, pos.Character, err)
		}
		if result == nil {
			t.Errorf("Hover@%d:%d = nil, want ip address hover", pos.Line, pos.Character)
			continue
		}
		if !strings.Contains(result.Contents.Value, want) {
			t.Errorf("Hover@%d:%d missing %q; got prefix %q", pos.Line, pos.Character, want, truncForLog(result.Contents.Value, 120))
		}
	}
}

// TestHoverStatementMultiWordKeyword verifies a dedicated *_statement whose
// tokens include the full multi-word key resolves to it (match ip address).
func TestHoverStatementMultiWordKeyword(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	content := []byte("!\nroute-map RM permit 10\n match ip address ACL\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	result, err := f.Hover(context.Background(), doc, protocol.Position{Line: 2, Character: 1})
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected match ip address hover, got nil")
	}
	want := "To distribute any routes"
	if !strings.Contains(result.Contents.Value, want) {
		t.Errorf("hover value missing %q; got prefix %q", want, truncForLog(result.Contents.Value, 120))
	}
}

// TestHoverUnknownCommandStillNull: commands whose keywords are not in the
// DB (description, transport input) must stay null — resolution must not
// fabricate a match from argument tokens.
func TestHoverUnknownCommandStillNull(t *testing.T) {
	f := cisco_ios_jinja2.New()
	defer f.Close()

	content := []byte("!\ninterface GigabitEthernet0/0\n description uplink\n!\nline vty 0 4\n transport input ssh\n!\n")
	doc := document.New("file:///test.cfg", "cisco_ios_jinja2", 1, content)
	if _, err := f.DidOpen(context.Background(), doc, nil); err != nil {
		t.Fatalf("DidOpen failed: %v", err)
	}

	for _, pos := range []protocol.Position{
		{Line: 2, Character: 1}, // description (not in DB)
		{Line: 4, Character: 0}, // line vty header (not in DB)
		{Line: 5, Character: 1}, // transport input (not in DB)
	} {
		result, err := f.Hover(context.Background(), doc, pos)
		if err != nil {
			t.Fatalf("Hover@%d:%d failed: %v", pos.Line, pos.Character, err)
		}
		if result != nil && result.Contents.Value != "" {
			t.Errorf("Hover@%d:%d = %q, want nil/empty (keyword not in DB)", pos.Line, pos.Character, truncForLog(result.Contents.Value, 80))
		}
	}
}

// truncForLog returns the first n bytes of s (or all of it if shorter), for
// readable test-failure output without dumping a multi-thousand-char docstring.
func truncForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
