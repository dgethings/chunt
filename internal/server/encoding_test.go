package server

// White-box tests for the negotiated-encoding conversion layer
// (encoding.go): which encoding the server promises per client offer, and
// that the dispatch-edge converters rescale positions on every wire
// boundary they own. Black-box wire coverage of the handshake
// (general.positionEncodings -> capabilities.positionEncoding) lives in
// TestWire_Initialize_Negotiations; per-line conversion arithmetic is
// pinned by document_test.Test{UTF16ToByte,ByteToUTF16,EncodingRoundTrip}.

import (
	"testing"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
)

// Fixture: line 0 is ASCII (byte == UTF-16 columns); line 1 starts with a
// 2-byte rune so the two scales diverge on it. Line 1 map:
//
//	é bytes[0,2) units[0,1)   i bytes[2] unit[1]     p bytes[3] unit[2]
//	' ' bytes[4] unit[3]      r bytes[5] unit[4]     a bytes[6] unit[5]
//	n bytes[7] unit[6]        g bytes[8] unit[7]     e bytes[9] unit[8]
//	s bytes[10] unit[9]
const convFixture = "hostname r1\néip ranges\n"

func convDoc() *document.Document {
	return document.New("file:///conv.ios.j2", "cisco_ios_jinja2", 1, []byte(convFixture))
}

func TestNegotiatePositionEncoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		what    string
		offered []string
		want    string
	}{
		{"nil offer (client precedes LSP 3.17)", nil, "utf-16"},
		{"empty offer", []string{}, "utf-16"},
		{"VS Code (vscode-languageclient) offer", []string{"utf-16"}, "utf-16"},
		{"utf-8 offered first wins", []string{"utf-8", "utf-16"}, "utf-8"},
		{"client order honored over our preference", []string{"utf-16", "utf-8"}, "utf-16"},
		{"only unknown kinds: LSP default", []string{"utf-32"}, "utf-16"},
	}
	for _, tc := range tests {
		if got := negotiatePositionEncoding(tc.offered); got != tc.want {
			t.Errorf("%s: negotiatePositionEncoding(%q) = %q, want %q", tc.what, tc.offered, got, tc.want)
		}
	}
}

func encodingServer(t *testing.T, enc string) *Server {
	t.Helper()
	s := New("test")
	if enc != "" {
		s.setPositionEncoding(enc)
	}
	return s
}

func TestInternalPosition_RescalesIncoming(t *testing.T) {
	t.Parallel()
	tests := []struct {
		what string
		enc  string
		pos  protocol.Position
		want uint
	}{
		{"utf-8 negotiated is identity", "utf-8", protocol.Position{Line: 1, Character: 6}, 6},
		{"utf-16 on ASCII line is identity", "utf-16", protocol.Position{Line: 0, Character: 5}, 5},
		{"utf-16 byte-splitting line", "utf-16", protocol.Position{Line: 1, Character: 2}, 3},
		{"character past the line end clamps", "utf-16", protocol.Position{Line: 1, Character: 99}, 11},
		{"un-negotiated server defaults to utf-16", "", protocol.Position{Line: 1, Character: 2}, 3},
	}
	for _, tc := range tests {
		got := encodingServer(t, tc.enc).internalPosition(convDoc(), tc.pos)
		if got.Character != tc.want {
			t.Errorf("%s: internalPosition(line %d, char %d) = %d, want byte col %d",
				tc.what, tc.pos.Line, tc.pos.Character, got.Character, tc.want)
		}
	}
}

func TestWireRange_RescalesOutgoing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		what string
		enc  string
		want protocol.Range
	}{
		{"utf-8 negotiated is identity", "utf-8", protocol.LineRange(1, 3, 8)},
		{"utf-16 byte cols -> units", "utf-16", protocol.LineRange(1, 2, 7)},
		{"un-negotiated server defaults to utf-16", "", protocol.LineRange(1, 2, 7)},
	}
	for _, tc := range tests {
		got := encodingServer(t, tc.enc).wireRange(convDoc(), protocol.LineRange(1, 3, 8))
		if got != tc.want {
			t.Errorf("%s: wireRange = %+v, want %+v", tc.what, got, tc.want)
		}
	}
}

func TestWireLocations_CrossDocumentScaling(t *testing.T) {
	t.Parallel()
	// Documents that a location's own URI points at supply the line text
	// for that location's scaling; unknown URIs pass through unchanged.
	otherURI := "file:///other.ios.j2"
	other := document.New(otherURI, "cisco_ios_jinja2", 1, []byte("é𝄞 ACL_NAME\n"))
	// other line 0: é bytes[0,2) units[0,1); 𝄞 bytes[2,6) units[1,3);
	// ' ' byte[6] unit[3]; A:J bytes[7,11) units[4,8) ("ACL_").
	s := encodingServer(t, "utf-16")
	s.documents.Put(other)
	locs := []protocol.Location{
		{URI: "file:///conv.ios.j2", Range: protocol.LineRange(1, 3, 8)},
		{URI: otherURI, Range: protocol.LineRange(0, 7, 11)},
		{URI: "file:///ghost.ios.j2", Range: protocol.LineRange(0, 3, 3)},
	}
	got := s.wireLocations(convDoc(), locs)
	if want := (protocol.LineRange(1, 2, 7)); got[0].Range != want {
		t.Errorf("same-doc location = %+v, want %+v", got[0].Range, want)
	}
	if want := (protocol.LineRange(0, 4, 8)); got[1].Range != want {
		t.Errorf("other-doc location = %+v, want %+v", got[1].Range, want)
	}
	if got[2].Range != locs[2].Range {
		t.Errorf("unknown-doc location = %+v, want passthrough %+v", got[2].Range, locs[2].Range)
	}
}

func TestWireDiagnostics_ScalesRangeAndRelatedInfo(t *testing.T) {
	t.Parallel()
	otherURI := "file:///other.ios.j2"
	s := encodingServer(t, "utf-16")
	s.documents.Put(document.New(otherURI, "cisco_ios_jinja2", 1, []byte("é𝄞 ACL_NAME\n")))
	diags := []protocol.Diagnostic{
		{Range: protocol.LineRange(1, 3, 8), Severity: protocol.SeverityError, Message: "primary"},
		{Range: protocol.LineRange(0, 0, 1), Message: "related on another doc", RelatedInformation: []protocol.DiagnosticRelatedInformation{{
			Location: protocol.Location{URI: otherURI, Range: protocol.LineRange(0, 7, 11)},
			Message:  "duplicate",
		}}},
	}
	got := s.wireDiagnostics(convDoc(), diags)
	if want := (protocol.LineRange(1, 2, 7)); got[0].Range != want {
		t.Errorf("diagnostic range = %+v, want %+v", got[0].Range, want)
	}
	if want := (protocol.LineRange(0, 4, 8)); got[1].RelatedInformation[0].Location.Range != want {
		t.Errorf("relatedInformation location = %+v, want %+v", got[1].RelatedInformation[0].Location.Range, want)
	}
}

func TestWireDocumentSymbols_RecursesChildren(t *testing.T) {
	t.Parallel()
	s := encodingServer(t, "utf-16")
	syms := []protocol.DocumentSymbol{{
		Name:           "ip",
		Kind:           protocol.SymbolKindNamespace,
		Range:          protocol.LineRange(1, 3, 8),
		SelectionRange: protocol.LineRange(1, 2, 3), // "i"
		Children: []protocol.DocumentSymbol{{
			Name:           "child",
			Range:          protocol.LineRange(1, 5, 7),
			SelectionRange: protocol.LineRange(1, 5, 6),
		}},
	}}
	got := s.wireDocumentSymbols(convDoc(), syms)
	if want := (protocol.LineRange(1, 2, 7)); got[0].Range != want {
		t.Errorf("symbol range = %+v, want %+v", got[0].Range, want)
	}
	if want := (protocol.LineRange(1, 1, 2)); got[0].SelectionRange != want {
		t.Errorf("symbol selectionRange = %+v, want %+v", got[0].SelectionRange, want)
	}
	child := got[0].Children[0]
	if want := (protocol.LineRange(1, 4, 6)); child.Range != want {
		t.Errorf("child symbol range = %+v, want %+v", child.Range, want)
	}
}

// TestWireEncodingMutations exercises the accessor pair the Initialize
// handshake uses, independent of arriving via the jrpc2 wire.
func TestWireEncodingMutations(t *testing.T) {
	t.Parallel()
	s := encodingServer(t, "")
	if got := s.wireEncoding(); got != protocol.PositionEncodingUTF16 {
		t.Errorf("fresh server wireEncoding = %q, want %q", got, protocol.PositionEncodingUTF16)
	}
	s.setPositionEncoding(protocol.PositionEncodingUTF8)
	if got := s.wireEncoding(); got != protocol.PositionEncodingUTF8 {
		t.Errorf("wireEncoding after setPositionEncoding = %q, want %q", got, protocol.PositionEncodingUTF8)
	}
}
