package lsp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// CODE THE COMPILER CANNOT LOWER YET.
//
// `nomi check` reports, beyond the front end's errors, each body the program
// reaches that the IR builder declines ("this call to `skip_odd` is not
// supported yet, so `fn evens` cannot run"; vmhost.Program.Unsupported). The
// server reports the same errors, but not per keystroke: lowering a program
// costs tens to hundreds of milliseconds more than its analysis, and the
// first lowering in a process lowers the whole stdlib. So a document is
// lowered when it is opened and each time it is saved, in the background,
// one run per document at a time; a save during a run queues one more run of
// the newest text.
//
// A finished run replaces the document's lowering diagnostics, and they are
// published with every later publish of its diagnostics until the next run
// replaces them, as a build tool's diagnostics stay until the next build.
// After an edit they keep the ranges of the text that was saved. A run
// whose buffer the front end rejects reports none: the front end's own
// diagnostics are already shown. A compiler panic during the lowering is one
// diagnostic at the top of the file, never a crash.

// loweringChecks holds each document's latest lowering diagnostics and the
// runs in progress.
type loweringChecks struct {
	mu sync.Mutex
	// docs holds each document's latest finished run.
	docs map[string][]protocol.Diagnostic
	// running marks a document with a run in progress; queued is the text
	// a save during it asked for next.
	running map[string]bool
	queued  map[string]string
	// gen counts a document's closes, so a run that finishes after its
	// document closed stores nothing.
	gen map[string]int
	// last is the most recent run's duration, for measurement.
	last time.Duration
}

// loweringChecksEnabled turns the open and save lowering runs on.
var loweringChecksEnabled = true

// checkLoweringFn is the lowering a run performs: vmhost.CheckLowering, or a
// test's stand-in.
var checkLoweringFn = vmhost.CheckLowering

// diagnostics are uri's latest lowering diagnostics.
func (c *loweringChecks) diagnostics(uri string) []protocol.Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.docs[uri]
}

// forget drops a closed document's diagnostics and any queued run.
func (c *loweringChecks) forget(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.docs, uri)
	delete(c.queued, uri)
	if c.gen == nil {
		c.gen = map[string]int{}
	}
	c.gen[uri]++
}

// checkLowering lowers uri's text content in the background and publishes
// the document's diagnostics again when the run finishes. It returns at
// once.
func (s *Server) checkLowering(uri, content string) {
	if !loweringChecksEnabled || uriToPath(uri) == "" || !strings.HasPrefix(uri, "file://") {
		return
	}
	c := &s.lowering
	c.mu.Lock()
	if c.running == nil {
		c.running, c.queued, c.docs = map[string]bool{}, map[string]string{}, map[string][]protocol.Diagnostic{}
		if c.gen == nil {
			c.gen = map[string]int{}
		}
	}
	if c.running[uri] {
		c.queued[uri] = content
		c.mu.Unlock()
		return
	}
	c.running[uri] = true
	gen := c.gen[uri]
	c.mu.Unlock()
	go s.runLoweringChecks(uri, content, gen)
}

// runLoweringChecks runs uri's lowering for content, then for each text a
// save queued meanwhile.
func (s *Server) runLoweringChecks(uri, content string, gen int) {
	c := &s.lowering
	for {
		start := time.Now()
		diags := loweringDiagnostics(uriToPath(uri), content)
		elapsed := time.Since(start)
		c.mu.Lock()
		c.last = elapsed
		if c.gen[uri] == gen {
			c.docs[uri] = diags
		}
		next, more := c.queued[uri]
		delete(c.queued, uri)
		if !more {
			delete(c.running, uri)
		}
		// A text queued after a close was queued by the reopened document.
		gen = c.gen[uri]
		c.mu.Unlock()
		s.republishLowering(uri)
		if !more {
			return
		}
		content = next
	}
}

// republishLowering publishes uri's diagnostics again with its new lowering
// diagnostics. When the installed analysis is not of the latest text, the
// analysis in progress publishes them when it finishes.
func (s *Server) republishLowering(uri string) {
	if s.notify == nil {
		return
	}
	sch := &s.sched
	sch.publishMu.Lock()
	defer sch.publishMu.Unlock()
	snap := s.docs.Snapshot(uri)
	if snap == nil || !snap.Open || snap.Analysis == nil || !snap.Current() {
		return
	}
	s.publishSnapshot(s.notify, snap)
}

// loweringDiagnostics lowers the file at path whose text is content and
// answers what `nomi check` would report beyond the front end, as protocol
// diagnostics in content's UTF-16 columns. A blocker in another file of the
// program is reported at the top of this one, naming where it is.
func loweringDiagnostics(path, content string) []protocol.Diagnostic {
	unsupported, err := checkLoweringFn(path, content)
	var internal *vmhost.InternalError
	if errors.As(err, &internal) {
		return []protocol.Diagnostic{internalErrorDiagnostic(internal)}
	}
	var ds frontend.Diagnostics
	if err != nil || !errors.As(unsupported, &ds) {
		return nil
	}
	severity := protocol.DiagnosticSeverityError
	source := "nomi"
	code := &protocol.IntegerOrString{Value: "not-supported-yet"}
	lines := newLineIndex(content)
	var out []protocol.Diagnostic
	for _, d := range ds {
		msg := d.Message
		for _, h := range d.Hints {
			msg += "\nhelp: " + h
		}
		diag := protocol.Diagnostic{Severity: &severity, Source: &source, Code: code, Message: msg}
		if samePath(d.Path, path) {
			diag.Range = lines.utf16Range(errorRange(d.Line, d.Col, d.EndLine, d.EndCol))
		} else if d.Line > 0 && filepath.IsAbs(d.Path) {
			diag.Message = fmt.Sprintf("in %s:%d:%d: %s", filepath.Base(d.Path), d.Line, d.Col, msg)
			diag.RelatedInformation = []protocol.DiagnosticRelatedInformation{{
				Location: protocol.Location{
					URI:   protocol.DocumentUri(pathToURI(d.Path)),
					Range: errorRange(d.Line, d.Col, d.EndLine, d.EndCol),
				},
				Message: d.Message,
			}}
		} else {
			// The body has no source position (a derive-synthesized one).
			diag.Message = msg
		}
		out = append(out, diag)
	}
	return out
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
