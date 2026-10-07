package lsp

import (
	"context"
	"sync"
	"time"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// analysisDelay is how long an edited document waits for the next edit
// before it is analyzed. Analysis of a file the size of
// tests/05-calendar-and-time/dates_test.nomi takes 25-40 ms; a fast typist
// sends an edit every 30-60 ms. At 50 ms a burst of typing analyzes once,
// at its end, instead of once per key, and the burst's diagnostics still
// arrive about 90 ms after the last key. Requests never wait on the delay:
// one that needs the current analysis starts it at once (flushAnalysis).
const analysisDelay = 50 * time.Millisecond

// analysisScheduler runs each edited document's analysis in the
// background, debounced, with a newer edit superseding an older one.
type analysisScheduler struct {
	mu      sync.Mutex
	pending map[string]*pendingAnalysis
	// publishMu orders diagnostics publication, and published records the
	// last version published per document, so an older analysis's
	// diagnostics never overwrite a newer one's.
	publishMu sync.Mutex
	published map[string]int
	// delay and wait replace analysisDelay and analysisWait when set
	// (tests).
	delay, wait time.Duration
}

func (sch *analysisScheduler) editDelay() time.Duration {
	if sch.delay > 0 {
		return sch.delay
	}
	return analysisDelay
}

func (sch *analysisScheduler) maxWait() time.Duration {
	if sch.wait > 0 {
		return sch.wait
	}
	return analysisWait
}

// pendingAnalysis is one scheduled analysis. cancel abandons it: a run
// that has not started its build skips it, and a run that has finished
// its build publishes nothing, because a newer edit has arrived.
type pendingAnalysis struct {
	timer  *time.Timer
	ctx    context.Context
	cancel context.CancelFunc
	notify glsp.NotifyFunc
}

// scheduleAnalysis analyzes uri's latest text after delay, superseding any
// analysis scheduled or running for an older text. notify publishes the
// diagnostics.
func (s *Server) scheduleAnalysis(uri string, delay time.Duration, notify glsp.NotifyFunc) {
	sch := &s.sched
	sch.mu.Lock()
	defer sch.mu.Unlock()
	if sch.pending == nil {
		sch.pending = map[string]*pendingAnalysis{}
	}
	if old := sch.pending[uri]; old != nil {
		old.timer.Stop()
		old.cancel()
	}
	// The older text's typed literals no longer matter.
	s.literals.cancel(uri)
	ctx, cancel := context.WithCancel(context.Background())
	p := &pendingAnalysis{ctx: ctx, cancel: cancel, notify: notify}
	p.timer = time.AfterFunc(delay, func() { s.runAnalysis(uri, p) })
	sch.pending[uri] = p
}

// flushAnalysis starts uri's scheduled analysis now instead of after its
// delay. It does nothing when none is waiting.
func (s *Server) flushAnalysis(uri string) {
	sch := &s.sched
	sch.mu.Lock()
	p := sch.pending[uri]
	started := p != nil && p.timer.Stop()
	sch.mu.Unlock()
	if started {
		go s.runAnalysis(uri, p)
	}
}

// cancelAnalysis abandons uri's scheduled analysis.
func (s *Server) cancelAnalysis(uri string) {
	sch := &s.sched
	sch.mu.Lock()
	defer sch.mu.Unlock()
	if p := sch.pending[uri]; p != nil {
		p.timer.Stop()
		p.cancel()
		delete(sch.pending, uri)
	}
}

// runAnalysis analyzes uri's latest text and, when that text is still the
// latest once the analysis is installed, publishes its diagnostics and
// carries the change to the files that import uri.
//
// The text may already be analyzed, by a superseded run that read the
// newer text when its build started or by propagation from another file;
// publishCurrent still publishes it once.
//
// A panic during the run publishes one diagnostic at the top of the file
// saying the server hit an internal error, unless a newer edit has
// superseded the run (recover.go). The document keeps its last installed
// analysis; its next edit analyzes it again.
func (s *Server) runAnalysis(uri string, p *pendingAnalysis) {
	defer func() {
		s.sched.mu.Lock()
		if s.sched.pending[uri] == p {
			delete(s.sched.pending, uri)
		}
		s.sched.mu.Unlock()
	}()
	defer recoverPanic("analyzing "+uri, func(sp *serverPanic) {
		if p.ctx.Err() != nil || p.notify == nil {
			return
		}
		s.sched.publishMu.Lock()
		defer s.sched.publishMu.Unlock()
		p.notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         protocol.DocumentUri(uri),
			Diagnostics: []protocol.Diagnostic{sp.diagnostic()},
		})
	})
	fault("analysis")
	s.docs.AnalyzeLatest(p.ctx, uri)
	if p.ctx.Err() != nil {
		return
	}
	if !s.publishCurrent(uri, p.notify) {
		return
	}
	s.docs.UpdateImportEdges(uri)
	s.propagateAsync(uri)
}

// publishCurrent publishes uri's diagnostics when its installed analysis
// is of its latest text and newer than the last one published. It reports
// whether it published.
func (s *Server) publishCurrent(uri string, notify glsp.NotifyFunc) bool {
	sch := &s.sched
	sch.publishMu.Lock()
	defer sch.publishMu.Unlock()
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil || !snap.Current() {
		return false
	}
	if sch.published == nil {
		sch.published = map[string]int{}
	}
	if snap.AnalyzedVersion <= sch.published[uri] {
		return false
	}
	sch.published[uri] = snap.AnalyzedVersion
	s.publishSnapshot(notify, snap)
	return true
}

// republishLiterals publishes uri's diagnostics again once a background
// typed-literal evaluation of content has finished (literal_eval.go). It
// publishes only while content is still the analyzed latest text and is not
// older than the last version publishCurrent published, so it never puts
// back diagnostics an edit has superseded.
func (s *Server) republishLiterals(uri, content string, notify glsp.NotifyFunc) {
	sch := &s.sched
	sch.publishMu.Lock()
	defer sch.publishMu.Unlock()
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil || !snap.Current() || snap.Content != content {
		return
	}
	if snap.AnalyzedVersion < sch.published[uri] {
		return
	}
	s.publishSnapshot(notify, snap)
}

// awaitAnalysis waits until uri's installed analysis is of its latest
// text, starting a debounced analysis at once rather than after its delay.
// It reports false when ctx ends or timeout passes first. An untracked
// document has nothing to wait for.
func (s *Server) awaitAnalysis(ctx context.Context, uri string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		installed := s.docs.Installed()
		snap := s.docs.Snapshot(uri)
		if snap == nil || snap.Current() {
			return true
		}
		s.flushAnalysis(uri)
		select {
		case <-installed:
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		}
	}
}
