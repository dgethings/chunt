package server

// Wire-encoding negotiation and the wire <-> internal position translation.
//
// The LSP 3.17 spec lets a client offer the position encodings it can honor
// (initialize general.positionEncodings) and requires the server to answer
// with one of them (capabilities.positionEncoding). It also fixes the
// default for handshakes that do not negotiate: UTF-16.
//
// chunt's internal coordinates are UTF-8 byte columns because tree-sitter
// Point.Column yields and consumes byte offsets and every feature below the
// server layer is written on that convention. A server-side byte claim
// ("utf-8" unconditionally) broke VS Code clients — its bundled
// vscode-languageclient offers only utf-16 and aborts the connection with
// "Unsupported position encoding (utf-8) received from server" otherwise.
//
// The negotiated encoding therefore lives on the Server and every request
// handler translates across the dispatch edge:
//
//   - utf-8 negotiated: no conversion — the wire scale IS the internal one.
//   - utf-16 negotiated: incoming positions convert UTF-16 -> bytes before
//     reaching the feature layer, and every outgoing byte range converts
//     back. The per-line math lives in document.UTF16ToByte/ByteToUTF16; a
//     document's line text is fetched with Document.ContentLine.

import (
	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/protocol"
)

// negotiatePositionEncoding returns the encoding to advertise in the
// initialize result: the first client-offered encoding chunt supports. Both
// LSP kinds are supportable — utf-8 natively (it IS the internal scale) and
// utf-16 via the converters below — so preference follows the client's own
// order, mirroring e.g. gopls's preference for an offered utf-8. Clients
// that negotiate nothing, or offer only unknown kinds, get the LSP default
// (utf-16), which every historical client treats as the baseline.
func negotiatePositionEncoding(offered []string) string {
	for _, enc := range offered {
		if enc == protocol.PositionEncodingUTF8 || enc == protocol.PositionEncodingUTF16 {
			return enc
		}
	}
	return protocol.PositionEncodingUTF16
}

// setPositionEncoding records the encoding negotiated during Initialize.
func (s *Server) setPositionEncoding(enc string) {
	s.posEncodingMu.Lock()
	defer s.posEncodingMu.Unlock()
	s.posEncoding = enc
}

// wireEncoding returns the negotiated wire encoding.
func (s *Server) wireEncoding() string {
	s.posEncodingMu.RLock()
	defer s.posEncodingMu.RUnlock()
	return s.posEncoding
}

// internalPosition translates a client-supplied wire position into the
// server's internal UTF-8 byte-column scale. It is the single entry point
// handlers use before handing a position to the feature layer.
func (s *Server) internalPosition(doc *document.Document, pos protocol.Position) protocol.Position {
	if s.wireEncoding() != protocol.PositionEncodingUTF16 {
		return pos
	}
	return protocol.Position{
		Line:      pos.Line,
		Character: document.UTF16ToByte(doc.ContentLine(pos.Line), pos.Character),
	}
}

// wireRange translates an internal byte-column range into the negotiated
// wire scale for a response payload.
func (s *Server) wireRange(doc *document.Document, r protocol.Range) protocol.Range {
	if s.wireEncoding() != protocol.PositionEncodingUTF16 {
		return r
	}
	return protocol.Range{
		Start: wirePosition(doc, r.Start),
		End:   wirePosition(doc, r.End),
	}
}

// wirePosition converts one internal byte-column position to UTF-16 units.
func wirePosition(doc *document.Document, p protocol.Position) protocol.Position {
	return protocol.Position{
		Line:      p.Line,
		Character: document.ByteToUTF16(doc.ContentLine(p.Line), p.Character),
	}
}

// wireLocations translates definition/reference results. Locations state
// their own URI, so a target in a different document is scaled against that
// document's lines; targets whose document is no longer in the store pass
// through unchanged rather than being scaled against the wrong geometry.
func (s *Server) wireLocations(doc *document.Document, locs []protocol.Location) []protocol.Location {
	if s.wireEncoding() != protocol.PositionEncodingUTF16 {
		return locs
	}
	out := make([]protocol.Location, len(locs))
	for i, loc := range locs {
		src := doc
		if loc.URI != doc.URI {
			if other, err := s.documents.Get(loc.URI); err == nil {
				src = other
			}
		}
		loc.Range = s.wireRange(src, loc.Range)
		out[i] = loc
	}
	return out
}

// wireDocumentSymbols translates the outline view recursively (each
// symbol's Range and SelectionRange, and nested Children).
func (s *Server) wireDocumentSymbols(doc *document.Document, syms []protocol.DocumentSymbol) []protocol.DocumentSymbol {
	if s.wireEncoding() != protocol.PositionEncodingUTF16 {
		return syms
	}
	out := make([]protocol.DocumentSymbol, len(syms))
	for i, sym := range syms {
		sym.Range = s.wireRange(doc, sym.Range)
		sym.SelectionRange = s.wireRange(doc, sym.SelectionRange)
		sym.Children = s.wireDocumentSymbols(doc, sym.Children)
		out[i] = sym
	}
	return out
}

// wireDiagnostics translates push-channel diagnostics. RelatedInformation
// locations may point at another document (e.g. the duplicate-definition
// site); each is scaled against that document's lines when available.
func (s *Server) wireDiagnostics(doc *document.Document, diags []protocol.Diagnostic) []protocol.Diagnostic {
	if s.wireEncoding() != protocol.PositionEncodingUTF16 {
		return diags
	}
	out := make([]protocol.Diagnostic, len(diags))
	for i, d := range diags {
		d.Range = s.wireRange(doc, d.Range)
		for j := range d.RelatedInformation {
			loc := &d.RelatedInformation[j].Location
			src := doc
			if loc.URI != doc.URI {
				if other, err := s.documents.Get(loc.URI); err == nil {
					src = other
				}
			}
			loc.Range = s.wireRange(src, loc.Range)
		}
		out[i] = d
	}
	return out
}
