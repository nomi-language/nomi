package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/frontend"
)

// openLoweringRPC opens main.nomi, holding src on disk and in the editor,
// on a server driven over the RPC test client whose debounced lowering
// waits delay after an edit.
func openLoweringRPC(t *testing.T, src string, delay time.Duration) (*Server, *rpcClient, string) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"main.nomi": src,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	uri := "file://" + filepath.Join(dir, "main.nomi")
	s := NewServer()
	s.lowering.delay = delay
	c := startRPC(t, s, dir)
	c.open(uri, src)
	return s, c, uri
}

// waitNoLowering waits for a publication for uri with no lowering
// diagnostic.
func (c *rpcClient) waitNoLowering(uri string) {
	c.t.Helper()
	timeout := time.After(60 * time.Second)
	for {
		select {
		case p := <-c.diags:
			if p.uri != uri {
				continue
			}
			clean := true
			for _, m := range p.msgs {
				if strings.Contains(m, "is not supported yet") {
					clean = false
				}
			}
			if clean {
				return
			}
		case <-timeout:
			c.t.Fatalf("no publication for %s without a lowering diagnostic", uri)
		}
	}
}

// An edit that introduces code the compiler cannot lower publishes the
// diagnostic without a save, and the edit that fixes it clears it.
func TestLoweringOnEdit_ReportedAndClearedWithoutSave(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	s, c, uri := openLoweringRPC(t, loweringFixed, 100*time.Millisecond)
	c.waitDiagnostics(uri)

	c.change(uri, 2, loweringBlocked)
	p := c.waitMessage(uri, "this call to `skip_odd` is not supported yet, so `fn evens` cannot run")
	if n := len(notSupported(s.lowering.diagnostics(uri))); n != 1 {
		t.Fatalf("%d stored lowering diagnostics after the edit, want 1 (published %q)", n, p.msgs)
	}

	c.change(uri, 3, loweringFixed)
	c.waitNoLowering(uri)
	if d := s.lowering.diagnostics(uri); len(d) != 0 {
		t.Fatalf("the edited fix kept lowering diagnostics: %+v", d)
	}
}

// countingLowering replaces the lowering with one that records each text
// it lowers for path and reports "<first line> is not supported yet (run
// <n>)" at the top of it, n counting its runs from 1. A text in block waits
// until it is closed. The first run of a text in finishStopped waits until
// the run is stopped and then until the channel is closed, and reports
// all the same, as a lowering that ends after it was stopped would.
type countingLowering struct {
	mu            sync.Mutex
	path          string
	texts         []string
	block         map[string]chan struct{}
	finishStopped map[string]chan struct{}
}

func installCountingLowering(t *testing.T) *countingLowering {
	t.Helper()
	cl := &countingLowering{block: map[string]chan struct{}{}, finishStopped: map[string]chan struct{}{}}
	saved := checkLoweringFn
	checkLoweringFn = func(ctx context.Context, path, src string, _ *analyzedlowering.Analyzed, _ *analyzedlowering.Memo) (error, error) {
		cl.mu.Lock()
		if path != cl.path {
			cl.mu.Unlock()
			return nil, nil
		}
		cl.texts = append(cl.texts, src)
		n := len(cl.texts)
		wait := cl.block[src]
		finish := cl.finishStopped[src]
		delete(cl.finishStopped, src)
		cl.mu.Unlock()
		if wait != nil {
			<-wait
		}
		if finish != nil {
			<-ctx.Done()
			<-finish
		}
		first, _, _ := strings.Cut(src, "\n")
		return frontend.Diagnostics{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("%s is not supported yet (run %d)", first, n)}}, nil
	}
	t.Cleanup(func() { checkLoweringFn = saved })
	return cl
}

func (cl *countingLowering) runs() []string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return append([]string(nil), cl.texts...)
}

// waitRuns waits until the lowering has run n times.
func (cl *countingLowering) waitRuns(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if r := cl.runs(); len(r) >= n {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lowering ran %d times, want %d", len(cl.runs()), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitText waits until the server holds text as uri's latest.
func waitText(t *testing.T, s *Server, uri, text string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if snap := s.docs.Snapshot(uri); snap != nil && snap.Text == text {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never held the edit's text")
		}
		time.Sleep(time.Millisecond)
	}
}

// entryToml declares main.nomi an entry.
const entryToml = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"

func lowerOnEditSource(marker string) string {
	return "// " + marker + "\nfn main() {}\n"
}

// A burst of edits is lowered once, after it, as its last text.
func TestLoweringOnEdit_OneRunPerQuietPeriod(t *testing.T) {
	cl := installCountingLowering(t)
	const delay = 250 * time.Millisecond
	dir := writeProject(t, map[string]string{"main.nomi": lowerOnEditSource("open"), "nomi.toml": entryToml})
	cl.path = filepath.Join(dir, "main.nomi")
	uri := "file://" + cl.path
	s := NewServer()
	s.lowering.delay = delay
	c := startRPC(t, s, dir)
	c.open(uri, lowerOnEditSource("open"))
	c.waitMessage(uri, "// open is not supported yet")
	cl.waitRuns(t, 1)

	var last string
	for i := range 10 {
		last = lowerOnEditSource("burst " + string(rune('a'+i)))
		c.change(uri, i+2, last)
	}
	c.waitMessage(uri, "// burst j is not supported yet")
	time.Sleep(3 * delay)
	runs := cl.runs()
	if len(runs) != 2 || runs[1] != last {
		t.Fatalf("the open and a burst of 10 edits lowered %d texts %q; want the open's and the burst's last", len(runs), runs)
	}

	// The next edit after the quiet period is lowered again.
	c.change(uri, 20, lowerOnEditSource("after"))
	c.waitMessage(uri, "// after is not supported yet")
	if n := len(cl.runs()); n != 3 {
		t.Fatalf("%d runs after one more edit, want 3", n)
	}
}

// A run that finishes after an edit replaced its text is dropped: its
// diagnostics are neither stored nor published, and the edit's own run
// follows it.
func TestLoweringOnEdit_StaleRunIsDropped(t *testing.T) {
	cl := installCountingLowering(t)
	v1, v2 := lowerOnEditSource("v1"), lowerOnEditSource("v2")
	release := make(chan struct{})
	cl.block[v1] = release
	dir := writeProject(t, map[string]string{"main.nomi": v1, "nomi.toml": entryToml})
	cl.path = filepath.Join(dir, "main.nomi")
	uri := "file://" + cl.path
	s := NewServer()
	s.lowering.delay = 20 * time.Millisecond
	c := startRPC(t, s, dir)
	c.open(uri, v1)
	cl.waitRuns(t, 1) // the open's run of v1, waiting on release

	c.change(uri, 2, v2)
	waitText(t, s, uri, v2)
	// Let the edit's debounced run queue behind the running one.
	time.Sleep(100 * time.Millisecond)
	if n := len(cl.runs()); n != 1 {
		t.Fatalf("%d runs while the first is in progress, want 1", n)
	}
	close(release)

	timeout := time.After(30 * time.Second)
	for done := false; !done; {
		select {
		case p := <-c.diags:
			if p.uri != uri {
				continue
			}
			for _, m := range p.msgs {
				if strings.Contains(m, "// v1 is not supported yet") {
					t.Fatalf("the stale run of v1 published %q", p.msgs)
				}
				if strings.Contains(m, "// v2 is not supported yet") {
					done = true
				}
			}
		case <-timeout:
			t.Fatal("the edit's run never published")
		}
	}
	runs := cl.runs()
	if len(runs) != 2 || runs[1] != v2 {
		t.Fatalf("lowered %q; want v1 and then v2", runs)
	}
	if n := s.lowering.stopped.Load(); n != 1 {
		t.Fatalf("%d runs stopped, want v1's", n)
	}
	d := s.lowering.diagnostics(uri)
	if len(d) != 1 || !strings.Contains(d[0].Message, "// v2") {
		t.Fatalf("stored lowering diagnostics %+v; want v2's only", d)
	}
}

// An edit stops the run in progress of the text it replaced, and that run
// stores and publishes nothing, even when a later edit has brought the
// document back to the text it lowered by the time it ends. The run of the
// text the edits leave publishes.
func TestLoweringOnEdit_SupersededRunPublishesNothing(t *testing.T) {
	cl := installCountingLowering(t)
	v1, v2 := lowerOnEditSource("v1"), lowerOnEditSource("v2")
	finish := make(chan struct{})
	cl.finishStopped[v1] = finish
	dir := writeProject(t, map[string]string{"main.nomi": v1, "nomi.toml": entryToml})
	cl.path = filepath.Join(dir, "main.nomi")
	uri := "file://" + cl.path
	s := NewServer()
	s.lowering.delay = 20 * time.Millisecond
	c := startRPC(t, s, dir)
	c.open(uri, v1)
	cl.waitRuns(t, 1) // the open's run of v1, waiting to be stopped

	c.change(uri, 2, v2)
	c.change(uri, 3, v1)
	waitText(t, s, uri, v1)
	// The revert's debounced run of v1 queues behind the stopped one.
	time.Sleep(100 * time.Millisecond)
	close(finish)

	timeout := time.After(30 * time.Second)
	for done := false; !done; {
		select {
		case p := <-c.diags:
			if p.uri != uri {
				continue
			}
			for _, m := range p.msgs {
				if strings.Contains(m, "(run 1)") {
					t.Fatalf("the stopped run published %q", p.msgs)
				}
				if strings.Contains(m, "// v1 is not supported yet (run 2)") {
					done = true
				}
			}
		case <-timeout:
			t.Fatalf("the revert's run never published; lowered %q", cl.runs())
		}
	}
	if runs := cl.runs(); len(runs) != 2 || runs[0] != v1 || runs[1] != v1 {
		t.Fatalf("lowered %q; want v1 twice", runs)
	}
	if n := s.lowering.stopped.Load(); n != 1 {
		t.Fatalf("%d runs stopped, want 1", n)
	}
	d := s.lowering.diagnostics(uri)
	if len(d) != 1 || !strings.Contains(d[0].Message, "(run 2)") {
		t.Fatalf("stored lowering diagnostics %+v; want the second run's only", d)
	}
}

// A panic in the debounced start of an edit's run, or in the publish after
// a run, is logged, and the document's next edit is lowered as usual.
func TestRecover_LoweringOnEditPanic(t *testing.T) {
	for _, where := range []string{"lowering edit", "lowering publish"} {
		t.Run(where, func(t *testing.T) {
			cl := installCountingLowering(t)
			dir := writeProject(t, map[string]string{"main.nomi": lowerOnEditSource("open"), "nomi.toml": entryToml})
			cl.path = filepath.Join(dir, "main.nomi")
			uri := "file://" + cl.path
			s := NewServer()
			s.lowering.delay = 20 * time.Millisecond
			c := startRPC(t, s, dir)
			c.open(uri, lowerOnEditSource("open"))
			c.waitMessage(uri, "// open is not supported yet")

			log := plantPanic(t, where)
			c.change(uri, 2, lowerOnEditSource("panics"))
			deadline := time.Now().Add(30 * time.Second)
			for !strings.Contains(logText(log), "planted panic") {
				if time.Now().After(deadline) {
					t.Fatal("no panic logged")
				}
				time.Sleep(5 * time.Millisecond)
			}
			faultHook.Store(nil)

			c.change(uri, 3, lowerOnEditSource("after"))
			c.waitMessage(uri, "// after is not supported yet")
		})
	}
}
