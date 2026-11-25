package server

import (
	"fmt"
	"sync"

	"github.com/dgethings/chunt/internal/document"
	"github.com/dgethings/chunt/internal/features"
	"github.com/dgethings/chunt/internal/protocol"
)

type serverState int

const (
	stateCreated serverState = iota
	stateInitialized
	stateShutDown
)

type Server struct {
	documents *document.Store
	features  *features.Router

	// posEncoding is the wire encoding negotiated at initialize time. It
	// is written once during Initialize and read by every request handler,
	// so a dedicated lock (rather than reusing stateMu) keeps the race
	// detector happy without coupling the two lifecycle aspects. It lives
	// on the Server (not per-feature) because the feature layer is
	// encoding-agnostic: it speaks UTF-8 byte columns (tree-sitter
	// Point.Column) and the server converts at the dispatch edge. See
	// encoding.go.
	posEncodingMu sync.RWMutex
	posEncoding   string

	stateMu sync.Mutex
	state   serverState
	// initSeen records that an initialize message has been SEEN on the
	// wire, independently of whether its handler has completed. jrpc2
	// assigns message handlers in stream order (batches are dequeued FIFO
	// and assigned under the server's queue lock) but explicitly does not
	// constrain dispatch order, so "initialize handler finished" cannot be
	// observed reliably when messages are pipelined after initialize in a
	// single write (chunt-5ks). The stateGate assigner uses this flag to
	// let stream-order followers of initialize through the pre-initialize
	// gate while the handshake is still in flight.
	initSeen bool
	version  string
}

// New creates a Server with no features registered. Callers register the
// language-specific features they need via RegisterFeature. Keeping the CGO
// feature out of this constructor lets the server-package tests run without
// pulling in the tree-sitter parser.
func New(version string) *Server {
	s := &Server{
		documents: document.NewStore(),
		features:  features.NewRouter(),
		// LSP default for handshakes that do not negotiate encodings;
		// Initialize overwrites it with the negotiated choice.
		posEncoding: protocol.PositionEncodingUTF16,
		version:     version,
	}
	return s
}

func (s *Server) RegisterFeature(f features.Feature) {
	s.features.Register(f)
}

func (s *Server) setState(newState serverState) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if newState == stateShutDown && s.state != stateInitialized {
		return fmt.Errorf("server not initialized")
	}
	s.state = newState
	return nil
}

func (s *Server) getState() serverState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state
}

// markInitializeSeen records that an initialize message was assigned —
// seen on the wire. Called by the stateGate assigner, which runs in stream
// order, so once this is set every message assigned afterwards follows
// initialize in the stream.
func (s *Server) markInitializeSeen() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.initSeen = true
}

// initializeSeen reports whether an initialize message has been assigned.
func (s *Server) initializeSeen() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.initSeen
}
