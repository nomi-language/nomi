package lsp

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The server analyzes and keeps only open documents. Every other workspace
// file is indexed (analysis.IndexedFile): its imports and top-level
// declarations, from a parse. Cross-file features read the index, and
// analyze a closed file on demand when they need its analysis
// (DocumentManager.Analyzed, which caches a bounded number).
//
// Closed files still get diagnostics, as an editor's workspace view
// expects: the background pass below analyzes each one, publishes, and
// keeps nothing. It runs once after the workspace scan and again for a
// closed file whenever something it imports changes.

// closedDiagnostics is the queue of closed files waiting for a diagnostics
// pass, worked by one goroutine at background priority.
type closedDiagnostics struct {
	mu      sync.Mutex
	queue   []string
	queued  map[string]bool
	wake    chan struct{}
	running bool
	// busy is the file being analyzed, or "".
	busy string
}

// startBackground creates the context background work runs under; shutdown
// cancels it.
func (s *Server) startBackground() {
	s.bg, s.stopBg = context.WithCancel(context.Background())
}

// scanWorkspace indexes the workspace's closed files, then queues each for
// a diagnostics pass.
func (s *Server) scanWorkspace() {
	defer recoverPanic("scanning the workspace", nil)
	uris := s.docs.IndexWorkspace(s.bg)
	s.queueClosedDiagnostics(uris...)
}

// queueClosedDiagnostics queues closed files for a diagnostics pass. A file
// already queued keeps its place.
func (s *Server) queueClosedDiagnostics(uris ...string) {
	if s.notify == nil || len(uris) == 0 {
		return
	}
	q := &s.closedDiags
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.queued == nil {
		q.queued = map[string]bool{}
		q.wake = make(chan struct{}, 1)
	}
	for _, uri := range uris {
		if !q.queued[uri] {
			q.queued[uri] = true
			q.queue = append(q.queue, uri)
		}
	}
	if !q.running {
		q.running = true
		go s.runClosedDiagnostics()
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// runClosedDiagnostics works the queue until the server shuts down. Before
// each file it waits until no edit is waiting for analysis and no
// foreground build is running, so a background build delays an edit's or
// a request's by at most one build.
func (s *Server) runClosedDiagnostics() {
	q := &s.closedDiags
	for {
		q.mu.Lock()
		q.busy = ""
		if len(q.queue) == 0 {
			q.mu.Unlock()
			select {
			case <-q.wake:
				continue
			case <-s.bg.Done():
				return
			}
		}
		uri := q.queue[0]
		q.queue = q.queue[1:]
		delete(q.queued, uri)
		q.busy = uri
		q.mu.Unlock()

		if !s.waitForQuiet() {
			return
		}
		s.closedDiagnostics(uri)
	}
}

// closedDiagnostics analyzes and publishes one closed file. A panic is
// logged and the queue moves on (recover.go).
func (s *Server) closedDiagnostics(uri string) {
	defer recoverPanic("analyzing closed file "+uri, nil)
	fault("closed")
	if snap := s.docs.AnalyzeClosed(uri); snap != nil {
		s.publishClosed(snap)
	}
}

// waitForQuiet waits until no edited document is waiting for its analysis
// and no foreground build is running. It reports false when the server
// shuts down first.
func (s *Server) waitForQuiet() bool {
	for {
		if s.bg.Err() != nil {
			return false
		}
		s.sched.mu.Lock()
		pending := len(s.sched.pending)
		s.sched.mu.Unlock()
		if pending == 0 && !s.docs.ForegroundBusy() {
			return true
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-s.bg.Done():
			return false
		}
	}
}

// publishClosed publishes a closed file's diagnostics unless the file has
// been opened since its analysis started: the open document's own
// publication owns it then. The check and the publication hold publishMu,
// which every open document's publication also holds.
func (s *Server) publishClosed(snap *analysis.DocSnapshot) {
	s.sched.publishMu.Lock()
	defer s.sched.publishMu.Unlock()
	if s.docs.IsOpen(snap.URI) {
		return
	}
	s.publishSnapshot(s.notify, snap)
}

// closedDiagnosticsPending reports whether a closed file is queued or
// being analyzed for its diagnostics.
func (s *Server) closedDiagnosticsPending() bool {
	q := &s.closedDiags
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue) > 0 || q.busy != ""
}

// workspaceSymbol answers workspace/symbol from the open documents' nodes
// and the closed files' index entries: every top-level declaration whose
// name contains the query's characters in order, ignoring case.
func (s *Server) workspaceSymbol(ctx *glsp.Context, params *protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	root := s.docs.WorkspaceRoot()
	var out []protocol.SymbolInformation
	// lines is nil for a closed file, whose index entry has byte columns.
	add := func(uri string, lines *lineIndex, d analysis.IndexedDecl) {
		if !fuzzyContains(d.Name, params.Query) {
			return
		}
		loc := makeLocation(uri, d.Pos, d.Name)
		if lines != nil {
			loc.Range = lines.utf16Range(loc.Range)
		}
		out = append(out, protocol.SymbolInformation{
			Name:     d.Name,
			Kind:     declSymbolKind(d.Kind),
			Location: loc,
		})
	}
	for _, uri := range s.docs.OpenURIs() {
		snap := s.docs.Snapshot(uri)
		if snap == nil {
			continue
		}
		var lines *lineIndex
		if snap.Content != "" {
			lines = s.lines.get(uri, snap.Content)
		}
		for _, d := range analysis.TopLevelDecls(snap.Nodes) {
			add(uri, lines, d)
		}
	}
	for _, entry := range s.docs.Indexed(root) {
		for _, d := range entry.Decls {
			add(entry.URI, nil, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Location.URI < out[j].Location.URI
	})
	return out, nil
}

// fuzzyContains reports whether name holds query's characters in order,
// ignoring case. An empty query matches every name.
func fuzzyContains(name, query string) bool {
	q := []rune(strings.ToLower(query))
	i := 0
	for _, r := range strings.ToLower(name) {
		if i < len(q) && q[i] == r {
			i++
		}
	}
	return i == len(q)
}

func declSymbolKind(k analysis.SymbolKind) protocol.SymbolKind {
	switch k {
	case analysis.SymbolFunction:
		return protocol.SymbolKindFunction
	case analysis.SymbolStruct:
		return protocol.SymbolKindStruct
	case analysis.SymbolEnum:
		return protocol.SymbolKindEnum
	case analysis.SymbolInterface:
		return protocol.SymbolKindInterface
	case analysis.SymbolOnce:
		return protocol.SymbolKindConstant
	default:
		return protocol.SymbolKindClass
	}
}
