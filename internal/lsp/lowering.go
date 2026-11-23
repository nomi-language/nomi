package lsp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/vmhost"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// CODE THE COMPILER CANNOT LOWER YET.
//
// `nomi check` reports, beyond the front end's errors, each backtick typed
// literal whose handler fails its compile-time run ("typed literal Regex`[`
// is invalid: ..."), and each body the program reaches that the IR builder
// declines ("this call to `skip_odd` is not supported yet, so `fn evens`
// cannot run"; vmhost.Program.Unsupported). The server reports the same
// errors (vmhost.CheckLowering), in the background: when a document is
// opened, when it is saved, loweringDelay after the last edit of a burst,
// and when a nomi.toml changes on disk (manifestChanged), whose
// [effects] lists hold the calls the program reaches. A stdlib file
// is not lowered (vmhost.CheckLowering).
//
// A run lowers the program from the document's installed analysis of the
// same text when that analysis holds the program `nomi check` would build
// (analysis.EntryProgram, analyzedlowering.Check): the document and the
// program's other project files were checked in the same analysis with no
// errors, and each still holds on disk the text the analysis read, as
// nomi.toml does. A document an entry reaches is analyzed in the entry's
// program, and its run lowers the program of the files it reaches, from
// those analyses, as `nomi check` lowers the document: its own main, tests
// and exported functions are the roots, never the entry's. An imported
// document with unsaved edits sends its importers' runs to the text:
// `nomi check` reads that file from disk. A run waits for the analysis
// when the text's analysis is still running. Such a run costs what
// lowering costs, 1 to 7 ms on the corpus's largest such programs, and
// after an edit that changes only bodies, the edited files' lowering
// linked with the last run's (vmhost's checklowering_perfile.go). Any
// other run checks the program as `nomi check` does, its own front end
// included: 10 to 38 ms. The first run in a process lowers the whole
// stdlib, about 120 ms. Hence a longer quiet period than analysisDelay.
//
// A document whose program was opened from its record
// (analysis/stored_program.go) offers no program to lower from. The entry
// lowers from the record's kept lowering (DocumentManager.StoredLowering),
// with the record's texts too, since that lowering also finds the outside
// calls hover shows. Another document the record lists as one whose
// lowering found nothing with every file as it is now takes that answer
// without lowering (DocumentManager.StoredLoweredClean). Any other run, and
// one the kept lowering cannot answer, lowers from the text.
// A run that lowers and finds nothing tells the manager
// (NoteLoweredClean), which writes the program's record when the program
// is kept with nothing to report and every text on disk.
//
// A run that lowers from the analysis keeps what it found
// (analyzedlowering.Memo), keyed by every text the lowering read. The next
// run of the document whose analysis was built from the same texts, which
// its files still hold on disk, answers that without lowering: the save
// after an edit's run lowers nothing.
//
// A document has one run at a time. An open, save or edit during a run
// queues one more run of the newest text, which replaces any text queued
// before it. An edit, or an open or save of another text, also stops the
// run in progress (vmhost.CheckLowering's and analyzedlowering.Check's
// ctx): the lowering asks between its phases and before each file's part
// of a phase, so at 50k lines it returns within about a millisecond, and
// lets go of the view lock a recheck of the edit waits for. A stopped run
// stores and publishes nothing, even when the document holds its text
// again by then, and a run whose text is no longer the document's latest
// when it finishes is dropped: the edit that replaced the text scheduled
// its own run. So a run never stores or publishes results for an older
// version. A save starts its run at once and drops the debounced one.
//
// A finished run of the latest text replaces the document's lowering
// diagnostics, and they are published with every later publish of its
// diagnostics until the next run replaces them. While an edit waits for its
// run they keep the ranges of the text they were found in. A run whose
// buffer the front end rejects reports none: the front end's own
// diagnostics are already shown. It keeps the effect calls of the
// declarations that checked, when vmhost.CheckLowering finds them (the
// package doc, "The effects a call has"). A compiler panic during the lowering, or a
// panic in the server around it, is one diagnostic at the top of the file,
// never a crash.

// loweringDelay is how long an edited document waits for the next edit
// before it is lowered. A fast typist sends an edit every 30-60 ms, so a
// burst of typing is lowered once, and its lowering diagnostics arrive
// about half a second after the last key.
const loweringDelay = 400 * time.Millisecond

// loweringChecks holds each document's latest lowering diagnostics and the
// runs in progress.
type loweringChecks struct {
	mu sync.Mutex
	// docs holds each document's latest finished run of its latest text,
	// fixes the errors of that run that carry a fix
	// (problemDiagnosticsAndFixes), manifestFixes the errors it found in
	// each nomi.toml that carry a fix, by the nomi.toml's URI
	// (manifestFixesIn), and reached the effect calls its program
	// reaches, which hover shows (reachedIn).
	docs          map[string][]protocol.Diagnostic
	fixes         map[string][]parser.ParseError
	manifestFixes map[string]map[string][]parser.ParseError
	reached       map[string]loweringReach
	// running holds the run in progress of each document that has one;
	// queued is the text an open, save or edit during it asked for next.
	running map[string]*loweringRun
	queued  map[string]string
	// timers holds each edited document's debounced run.
	timers map[string]*loweringWait
	// gen counts a document's closes, so a run that finishes after its
	// document closed stores nothing.
	gen map[string]int
	// last is the most recent run's duration, for measurement.
	last time.Duration
	// memo keeps each document's last lowering from its analysis, which a
	// run of the same input answers again (analyzedlowering.Memo).
	memo analyzedlowering.Memo
	// lowered counts the runs that lowered, reused those the memo
	// answered, and stopped those a newer text or a close stopped.
	lowered, reused, stopped atomic.Int64
	// recorded counts the runs a program's record answered
	// (analysis.DocumentManager.StoredLoweredClean).
	recorded atomic.Int64
	// delay replaces loweringDelay when set (tests).
	delay time.Duration
}

// loweringWait is one debounced run. Its timer is read under
// loweringChecks.mu only.
type loweringWait struct {
	timer *time.Timer
}

// loweringRun is a document's run in progress: the text it lowers, and
// the cancel of the context that stops it. Read under loweringChecks.mu
// only.
type loweringRun struct {
	content string
	cancel  context.CancelFunc
}

// loweringReach is the effect calls a document's run found
// (analyzedlowering.Found's Reached), with the text it lowered, whose
// positions they are in.
type loweringReach struct {
	content string
	calls   []analyzedlowering.Reached
}

// loweringChecksEnabled turns the lowering runs on.
var loweringChecksEnabled = true

// checkLoweringFn is the lowering a run performs: lowerFromAnalysis, or a
// test's stand-in.
var checkLoweringFn = lowerFromAnalysis

// lowerFromAnalysis lowers the file at path whose text is src from a, the
// document's analysis of src, when a is the program `nomi check` builds,
// and keeps what it found in memo; otherwise it lowers the text
// (vmhost.CheckLowering). a and memo may be nil. Once ctx is done the
// lowering stops at its next step and answers ctx's error, which the memo
// does not keep.
func lowerFromAnalysis(ctx context.Context, path, src string, a *analyzedlowering.Analyzed, memo *analyzedlowering.Memo) (analyzedlowering.Found, error) {
	if a != nil {
		if found, ok, err := analyzedlowering.Check(ctx, path, src, a); ok {
			if ctx.Err() == nil {
				memo.Store(path, src, a, found, err)
			}
			return found, err
		}
	}
	return vmhost.CheckLowering(ctx, path, src)
}

// lower is what lowering the file at path whose text is src from a finds:
// the memo's answer when the document's last lowering read the same texts,
// else checkLoweringFn's.
func (c *loweringChecks) lower(ctx context.Context, path, src string, a *analyzedlowering.Analyzed) (analyzedlowering.Found, error) {
	if found, err, ok := c.memo.Lookup(path, src, a); ok {
		c.reused.Add(1)
		return found, err
	}
	c.lowered.Add(1)
	return checkLoweringFn(ctx, path, src, a, &c.memo)
}

// analysisWaitForLowering bounds how long a run waits for the analysis of
// its text before it lowers the text with its own front end.
const analysisWaitForLowering = 2 * time.Second

func (c *loweringChecks) editDelay() time.Duration {
	if c.delay > 0 {
		return c.delay
	}
	return loweringDelay
}

// init makes the maps. c.mu is held.
func (c *loweringChecks) init() {
	if c.running == nil {
		c.running, c.queued, c.docs = map[string]*loweringRun{}, map[string]string{}, map[string][]protocol.Diagnostic{}
		c.fixes = map[string][]parser.ParseError{}
		c.manifestFixes = map[string]map[string][]parser.ParseError{}
		c.reached = map[string]loweringReach{}
		c.timers = map[string]*loweringWait{}
		if c.gen == nil {
			c.gen = map[string]int{}
		}
	}
}

// diagnostics are uri's latest lowering diagnostics.
func (c *loweringChecks) diagnostics(uri string) []protocol.Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.docs[uri]
}

// fixesOf are the errors of uri's latest lowering diagnostics that carry a
// fix, which a code action offers (buildParseFixActions).
func (c *loweringChecks) fixesOf(uri string) []parser.ParseError {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fixes[uri]
}

// manifestFixesIn are the errors every document's latest run found in the
// nomi.toml at uri that carry a fix (manifestDiagnostics).
func (c *loweringChecks) manifestFixesIn(uri string) []parser.ParseError {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []parser.ParseError
	for _, byManifest := range c.manifestFixes {
		out = append(out, byManifest[uri]...)
	}
	return out
}

// reachedIn is the effect calls a hover in uri, whose text is content,
// may show (hoverdoc.AppendReach): those uri's own last run found when it
// lowered content, then those each other document's last run found in the
// file, which that lowering read from disk, when content is the file on
// disk.
func (c *loweringChecks) reachedIn(uri, content string) []analyzedlowering.Reached {
	path := uriToPath(uri)
	c.mu.Lock()
	var own, others []analyzedlowering.Reached
	for u, run := range c.reached {
		switch {
		case u == uri && run.content == content:
			own = append(own, run.calls...)
		case u != uri:
			for _, r := range run.calls {
				if inFile(r.Path, path) {
					others = append(others, r)
				}
			}
		}
	}
	c.mu.Unlock()
	if len(others) > 0 {
		if disk, ok := diskContent(protocol.DocumentUri(uri)); !ok || disk != content {
			others = nil
		}
	}
	return append(own, others...)
}

// inFile reports whether a call's path, as a lowering names its file, is
// the file at path.
func inFile(callPath, path string) bool {
	return callPath == path || filepath.Base(callPath) == filepath.Base(path) && samePath(callPath, path)
}

// stopTimer drops uri's debounced run, if one is waiting. c.mu is held.
func (c *loweringChecks) stopTimer(uri string) {
	if w := c.timers[uri]; w != nil {
		w.timer.Stop()
		delete(c.timers, uri)
	}
}

// stopStale stops uri's run in progress unless it lowers content, the
// document's latest text. c.mu is held.
func (c *loweringChecks) stopStale(uri, content string) {
	if r := c.running[uri]; r != nil && r.content != content {
		r.cancel()
	}
}

// forget drops a closed document's diagnostics, its debounced run and any
// queued run, and stops its run in progress.
func (c *loweringChecks) forget(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	delete(c.docs, uri)
	delete(c.fixes, uri)
	delete(c.manifestFixes, uri)
	delete(c.reached, uri)
	delete(c.queued, uri)
	c.stopTimer(uri)
	if r := c.running[uri]; r != nil {
		r.cancel()
	}
	c.gen[uri]++
	c.memo.Forget(uriToPath(uri))
}

// lowers reports whether the server lowers uri: a file on disk, whose
// program the lowering reads from its directory.
func lowers(uri string) bool {
	return loweringChecksEnabled && strings.HasPrefix(uri, "file://") && uriToPath(uri) != ""
}

// scheduleLowering lowers uri's latest text once loweringDelay passes with
// no newer edit. Each edit restarts the wait, and stops a run in progress
// of another text.
func (s *Server) scheduleLowering(uri string) {
	if !lowers(uri) {
		return
	}
	latest := ""
	if snap := s.docs.Snapshot(uri); snap != nil {
		latest = snap.Text
	}
	c := &s.lowering
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	c.stopStale(uri, latest)
	c.stopTimer(uri)
	w := &loweringWait{}
	w.timer = time.AfterFunc(c.editDelay(), func() { s.lowerEdited(uri, w) })
	c.timers[uri] = w
}

// lowerEdited starts the run of uri's latest text once the debounced wait
// w has passed. A wait that a newer edit, a save or a close replaced does
// nothing.
func (s *Server) lowerEdited(uri string, w *loweringWait) {
	defer recoverPanic("lowering the edit of "+uri, nil)
	c := &s.lowering
	c.mu.Lock()
	if c.timers[uri] != w {
		c.mu.Unlock()
		return
	}
	delete(c.timers, uri)
	c.mu.Unlock()
	fault("lowering edit")
	snap := s.docs.Snapshot(uri)
	if snap == nil || !snap.Open {
		return
	}
	s.checkLowering(uri, snap.Text)
}

// checkLowering lowers uri's text content in the background and publishes
// the document's diagnostics again when the run finishes. It returns at
// once. A debounced run waiting for uri is dropped: content is the text it
// would lower. A run in progress of another text is stopped, and content's
// run follows it.
func (s *Server) checkLowering(uri, content string) {
	if !lowers(uri) {
		return
	}
	c := &s.lowering
	c.mu.Lock()
	c.init()
	c.stopTimer(uri)
	if c.running[uri] != nil {
		c.stopStale(uri, content)
		c.queued[uri] = content
		c.mu.Unlock()
		return
	}
	ctx, run := c.start(uri, content)
	gen := c.gen[uri]
	c.mu.Unlock()
	go s.runLoweringChecks(ctx, uri, run, gen)
}

// start records uri's run of content as the one in progress and answers
// it with the context that stops it. c.mu is held.
func (c *loweringChecks) start(uri, content string) (context.Context, *loweringRun) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &loweringRun{content: content, cancel: cancel}
	c.running[uri] = run
	return ctx, run
}

// runLoweringChecks runs uri's lowering run, which ctx stops, then one for
// each text queued meanwhile. A run that a newer text or a close stopped
// stores and publishes nothing: what it found is of an older text, even
// when the document holds that text again by the time it ends. A panic
// outside the lowering itself (which safeLoweringDiagnostics answers) ends
// the runs and frees the document for its next one.
func (s *Server) runLoweringChecks(ctx context.Context, uri string, run *loweringRun, gen int) {
	c := &s.lowering
	defer recoverPanic("publishing the lowering of "+uri, func(*serverPanic) {
		c.mu.Lock()
		delete(c.running, uri)
		delete(c.queued, uri)
		c.mu.Unlock()
	})
	for {
		start := time.Now()
		content := run.content
		var found loweringFound
		lowered := false
		if _, latest := s.waitLoweringAnalysis(ctx, uri, content); latest && ctx.Err() == nil {
			// The entry of a program opened from its record lowers from the
			// record's kept lowering (analysis.StoredLowering), the record's
			// texts too, since its walk finds the calls hover shows.
			stored := s.docs.StoredLowering(uri, content)
			if stored == nil && s.docs.StoredLoweredClean(uri, content) {
				// Another document of a program opened from its record,
				// which says this text's lowering finds nothing.
				c.recorded.Add(1)
			} else {
				// The analysis is taken again under the view lock, which
				// keeps a recheck from rewriting it while it is lowered.
				release := s.docs.View()
				if a, latest := s.installedLoweringAnalysis(uri, content); latest {
					if a == nil && stored != nil {
						a = &analyzedlowering.Analyzed{Stored: stored}
					}
					found = s.lowerDocument(ctx, uri, content, a)
					lowered = true
				}
				release()
			}
		}
		if ctx.Err() != nil {
			c.stopped.Add(1)
		} else {
			s.finishLowering(uri, content, gen, found, time.Since(start))
			if lowered && len(found.diags) == 0 {
				// A record of the program, which a later server opens
				// from, is written now if it can be.
				s.docs.NoteLoweredClean(uri, content, analyzedlowering.Kept)
			}
		}
		run.cancel()
		c.mu.Lock()
		next, more := c.queued[uri]
		delete(c.queued, uri)
		if more {
			ctx, run = c.start(uri, next)
		} else {
			delete(c.running, uri)
		}
		// A text queued after a close was queued by the reopened document.
		gen = c.gen[uri]
		c.mu.Unlock()
		if !more {
			return
		}
	}
}

// loweringAnalysis is the installed analysis of content to lower uri from,
// or nil when there is none to use: the document's analysis of content is
// not the program `nomi check` builds (analysis.EntryProgram), the text
// has syntax errors, or the analysis did not arrive within
// analysisWaitForLowering. latest is false when content is no longer the
// document's latest text, whose run finishLowering would drop, so it need
// not lower at all.
func (s *Server) loweringAnalysis(uri, content string) (a *analyzedlowering.Analyzed, latest bool) {
	return s.waitLoweringAnalysis(context.Background(), uri, content)
}

// waitLoweringAnalysis is loweringAnalysis, whose wait for the analysis
// also ends when ctx is done.
func (s *Server) waitLoweringAnalysis(ctx context.Context, uri, content string) (a *analyzedlowering.Analyzed, latest bool) {
	deadline := time.NewTimer(analysisWaitForLowering)
	defer deadline.Stop()
	for {
		installed := s.docs.Installed()
		snap := s.docs.Snapshot(uri)
		if snap == nil || snap.Text != content {
			return nil, false
		}
		if snap.Analysis != nil && snap.Current() {
			return analyzedOf(snap), true
		}
		select {
		case <-installed:
		case <-deadline.C:
			return nil, true
		case <-ctx.Done():
			return nil, true
		}
	}
}

// installedLoweringAnalysis is loweringAnalysis without the wait: the
// analysis of content installed now, or nil, and whether content is still
// the document's latest text.
func (s *Server) installedLoweringAnalysis(uri, content string) (*analyzedlowering.Analyzed, bool) {
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Text != content {
		return nil, false
	}
	if snap.Analysis == nil || !snap.Current() {
		return nil, true
	}
	return analyzedOf(snap), true
}

// analyzedOf is the program to lower from snap's analysis, which is of its
// latest text, or nil when it is not the program `nomi check` builds.
func analyzedOf(snap *analysis.DocSnapshot) *analyzedlowering.Analyzed {
	if snap.Program == nil || len(snap.Errors) > 0 || len(snap.Damaged) > 0 {
		return nil
	}
	p := snap.Program
	return &analyzedlowering.Analyzed{
		Nodes:        p.Nodes,
		FA:           snap.Analysis,
		Root:         p.Root,
		Files:        p.Files,
		ReachesEntry: p.ReachesEntry,
		Manifest:     p.Manifest,
	}
}

// finishLowering stores a run's diagnostics and publishes the document's
// diagnostics with them, and what it found in a nomi.toml in that file
// (publishLoweringManifests), when content is still the open document's
// latest text and the document has not been closed since the run started.
// Otherwise it drops them. Holding publishMu across the check, the store
// and the publish keeps an edit's publish from slipping between them.
func (s *Server) finishLowering(uri, content string, gen int, found loweringFound, elapsed time.Duration) {
	c := &s.lowering
	sch := &s.sched
	sch.publishMu.Lock()
	defer sch.publishMu.Unlock()
	fault("lowering publish")
	defer s.docs.View()()
	snap := s.docs.Snapshot(uri)
	c.mu.Lock()
	c.last = elapsed
	fresh := c.gen[uri] == gen && snap != nil && snap.Open && snap.Text == content
	moved := false
	if fresh {
		c.docs[uri] = found.diags
		c.fixes[uri] = found.fixes
		c.manifestFixes[uri] = found.manifestFixes
		old, had := c.reached[uri]
		moved = !had || old.content != content || !slices.Equal(old.calls, found.reached)
		c.reached[uri] = loweringReach{content: content, calls: found.reached}
	}
	c.mu.Unlock()
	if fresh {
		s.publishLoweringManifests(uri, found.manifests)
	}
	if moved {
		// The tokens that reach outside follow what the run found, in this
		// document and in each other file of its program.
		s.refreshSemanticTokens()
	}
	// When the installed analysis is not of the latest text, the analysis
	// in progress publishes the new diagnostics when it finishes.
	if !fresh || s.notify == nil || snap.Analysis == nil || !snap.Current() {
		return
	}
	s.publishViewed(s.notify, snap)
}

// loweringFound is what a run found: the document's diagnostics, those of
// its errors that carry a fix, the diagnostics in each nomi.toml and those
// of them that carry a fix, by its URI, and the effect calls the program
// reaches.
type loweringFound struct {
	diags         []protocol.Diagnostic
	fixes         []parser.ParseError
	manifests     map[string][]protocol.Diagnostic
	manifestFixes map[string][]parser.ParseError
	reached       []analyzedlowering.Reached
}

// safeLoweringDiagnostics is the document's diagnostics lowerDocument
// finds.
func (s *Server) safeLoweringDiagnostics(ctx context.Context, uri, content string, a *analyzedlowering.Analyzed) []protocol.Diagnostic {
	return s.lowerDocument(ctx, uri, content, a).diags
}

// lowerDocument is what the server's loweringDiagnostics finds, through
// its memo (loweringChecks.lower), with the errors that carry a fix
// (problemDiagnosticsAndFixes), the diagnostics in a nomi.toml
// (s.manifestDiagnostics) and the effect calls the program reaches, and
// with a panic in the server around the
// lowering (one vmhost does not already answer as an InternalError)
// answered as one diagnostic at the top of the file (recover.go).
func (s *Server) lowerDocument(ctx context.Context, uri, content string, a *analyzedlowering.Analyzed) (found loweringFound) {
	defer recoverPanic("lowering "+uri, func(p *serverPanic) {
		found = loweringFound{diags: []protocol.Diagnostic{p.diagnostic()}}
	})
	fault("lowering")
	path := uriToPath(uri)
	got, err := s.lowering.lower(ctx, path, content, a)
	diags, fixes := problemDiagnosticsAndFixes(path, content, got.Problems, err)
	manifests, manifestFixes := s.manifestDiagnostics(got.Problems, err)
	return loweringFound{diags: diags, fixes: fixes, manifests: manifests, manifestFixes: manifestFixes, reached: got.Reached}
}

// loweringDiagnostics lowers the file at path whose text is content, from
// a, its analysis, when that is not nil (lowerFromAnalysis), and answers
// what `nomi check` would report beyond the front end, as protocol
// diagnostics in content's UTF-16 columns (problemDiagnostics).
func loweringDiagnostics(path, content string, a *analyzedlowering.Analyzed) []protocol.Diagnostic {
	found, err := checkLoweringFn(context.Background(), path, content, a, nil)
	return problemDiagnostics(path, content, found.Problems, err)
}

// problemDiagnostics are a lowering's problems and err for the file at
// path whose text is content, as protocol diagnostics in content's UTF-16
// columns. A blocker in another file of the program is reported at the top
// of this one, naming where it is.
func problemDiagnostics(path, content string, problems, err error) []protocol.Diagnostic {
	diags, _ := problemDiagnosticsAndFixes(path, content, problems, err)
	return diags
}

// problemDiagnosticsAndFixes is problemDiagnostics, with the diagnostics
// whose error carries a fix (frontend.Diagnostic's Fix) as parse errors at
// the diagnostic's start with its headline, which buildParseFixActions
// offers: the edit to nomi.toml that allows a call its lists do not. An
// error's related locations (frontend.Diagnostic's Related) are its
// relatedInformation. An error in another file with related locations in
// this one, a refused call in a dependency's file and the places the
// project's code leads to it (vmhost's leadsTo), is shown at each of those
// places, headed by what the location says, with the error's own place as
// its relatedInformation, instead of at the top of the file: the same
// error, with its code, its args and its fix, so the marks come and go
// with it.
func problemDiagnosticsAndFixes(path, content string, problems, err error) ([]protocol.Diagnostic, []parser.ParseError) {
	var internal *vmhost.InternalError
	if errors.As(err, &internal) {
		return []protocol.Diagnostic{internalErrorDiagnostic(internal)}, nil
	}
	var ds frontend.Diagnostics
	if err != nil || !errors.As(problems, &ds) {
		return nil, nil
	}
	var fixes []parser.ParseError
	severity := protocol.DiagnosticSeverityError
	source := "nomi"
	lines := newLineIndex(content)
	var out []protocol.Diagnostic
	for _, d := range ds {
		msg := d.Message
		for _, h := range d.Hints {
			msg += "\nhelp: " + h
		}
		code := &protocol.IntegerOrString{Value: "not-supported-yet"}
		if d.Code != "" {
			code = &protocol.IntegerOrString{Value: d.Code}
		}
		diag := protocol.Diagnostic{Severity: &severity, Source: &source, Code: code, Message: msg}
		if d.Code == vmhost.CustomCurrencyCallCode {
			// A hint `nomi check` does not print: the call runs as written.
			warning := protocol.DiagnosticSeverityWarning
			diag.Severity = &warning
		}
		var related []protocol.DiagnosticRelatedInformation
		var here []frontend.Related
		for _, r := range d.Related {
			related = append(related, protocol.DiagnosticRelatedInformation{
				Location: locationIn(path, lines, r.Path, r.Line, r.Col, r.EndLine, r.EndCol), Message: r.Message})
			if samePath(r.Path, path) {
				here = append(here, r)
			}
		}
		var marks []protocol.Diagnostic
		switch {
		case samePath(d.Path, path):
			diag.Range = lines.utf16Range(errorRange(d.Line, d.Col, d.EndLine, d.EndCol))
			diag.RelatedInformation = related
			marks = append(marks, diag)
		case d.Line > 0 && filepath.IsAbs(d.Path) && len(here) > 0:
			site := protocol.DiagnosticRelatedInformation{
				Location: locationIn(path, lines, d.Path, d.Line, d.Col, d.EndLine, d.EndCol),
				Message:  d.Message,
			}
			for _, r := range here {
				mark := diag
				mark.Range = lines.utf16Range(errorRange(r.Line, r.Col, r.EndLine, r.EndCol))
				mark.Message = r.Message + "\n" + msg
				mark.RelatedInformation = []protocol.DiagnosticRelatedInformation{site}
				marks = append(marks, mark)
			}
		case d.Line > 0 && filepath.IsAbs(d.Path):
			diag.Message = fmt.Sprintf("in %s:%d:%d: %s", filepath.Base(d.Path), d.Line, d.Col, msg)
			diag.RelatedInformation = append([]protocol.DiagnosticRelatedInformation{{
				Location: locationIn(path, lines, d.Path, d.Line, d.Col, d.EndLine, d.EndCol),
				Message:  d.Message,
			}}, related...)
			marks = append(marks, diag)
		default:
			// The body has no source position (a derive-synthesized one).
			diag.RelatedInformation = related
			marks = append(marks, diag)
		}
		out = append(out, marks...)
		if d.Fix != nil {
			for _, mark := range marks {
				fixes = append(fixes, parser.ParseError{
					Line: int(mark.Range.Start.Line) + 1, Col: lines.byteCol(mark.Range.Start.Line, mark.Range.Start.Character),
					Message: diagnosticHeadline(mark.Message), Fix: d.Fix,
				})
			}
		}
	}
	return out, fixes
}

// locationIn is the span from line:col to endLine:endCol (1-based, byte
// columns) in the file at file, as a protocol location in UTF-16 columns:
// against lines, this document's text, when file is the document at path,
// else against the file's text on disk, which the lowering read.
func locationIn(path string, lines *lineIndex, file string, line, col, endLine, endCol int) protocol.Location {
	rng := errorRange(line, col, endLine, endCol)
	uri := protocol.DocumentUri(pathToURI(file))
	if samePath(file, path) {
		return protocol.Location{URI: protocol.DocumentUri(pathToURI(path)), Range: lines.utf16Range(rng)}
	}
	if text, ok := diskContent(uri); ok {
		rng = newLineIndex(text).utf16Range(rng)
	}
	return protocol.Location{URI: uri, Range: rng}
}

// samePath reports whether two paths name the same file.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// internalErrorDiagnostic is a compiler panic during lowering, as one
// diagnostic at the top of the file.
func internalErrorDiagnostic(e *vmhost.InternalError) protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	source := "nomi"
	return protocol.Diagnostic{
		Severity: &severity,
		Source:   &source,
		Code:     &protocol.IntegerOrString{Value: "internal-compiler-error"},
		Message:  e.Error(),
	}
}
