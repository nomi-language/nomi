package vm_test

// THE OUTPUT WRITER UNDER CONCURRENT ACTIVATIONS.
//
// A `Machine` holds one `io.Writer` and `io.print` writes to it. Two
// activations of the same machine on two goroutines is not exotic: it is what
// a `concurrent` block's tasks are, since each task runs on its own
// goroutine. So the writer is shared mutable state.
//
// THE ASSERTION COUNTS LINES. It does not time anything and it does not
// compare against a wall clock: N activations that each print once must
// produce exactly N lines, and every line must be one of the N the program
// can emit. A timing assertion here would be measuring the mutex rather than
// the corruption.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
)

// concurrentPrintIR lowers testdata/concurrent_print/main.nomi, whose `emit`
// prints exactly one line naming its argument.
func concurrentPrintIR(t *testing.T) *ir.Module {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "concurrent_print", "main.nomi"))
	if err != nil {
		t.Fatalf("resolving the fixture: %v", err)
	}
	prog, err := irbuild.Analyze(p)
	if err != nil {
		t.Fatalf("analyzing the fixture: %v", err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatalf("the fixture must lower: %v", err)
	}
	if len(res.IR) != 1 {
		t.Fatalf("the fixture is one compilation unit and %d retained an ir.Module", len(res.IR))
	}
	return res.IR[0]
}

// writerGoroutines and writerLinesEach are 32 and 8, so the total is 256:
// enough concurrent writes that an unserialized writer loses lines.
const (
	writerGoroutines = 32
	writerLinesEach  = 8
)

// TestWriter_ConcurrentActivationsProduceEveryLine checks that concurrent
// activations of one machine lose no output line.
//
// It fails on the RACE DETECTOR without a serialized writer, and it fails on
// the COUNT without one even when the detector is off — which is the half
// that matters, because the detector is not on in production and a lost line
// is a wrong program output rather than a warning.
func TestWriter_ConcurrentActivationsProduceEveryLine(t *testing.T) {
	mod := concurrentPrintIR(t)

	// ONE Machine and ONE bytes.Buffer, shared by every goroutine. A
	// bytes.Buffer rather than os.Stdout on purpose: a small write to a file
	// descriptor is effectively atomic, so stdout HIDES this defect, and a
	// bytes.Buffer does not.
	var out bytes.Buffer
	m := vm.New(mod, &out)

	total := writerGoroutines * writerLinesEach
	want := make(map[string]bool, total)
	for n := range total {
		want[fmt.Sprintf("line %d", n)] = true
	}

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var runErrs []error
	start := make(chan struct{})
	for g := range writerGoroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := range writerLinesEach {
				n := int64(g*writerLinesEach + i)
				if _, err := m.Run("emit", int64(n)); err != nil {
					errMu.Lock()
					runErrs = append(runErrs, err)
					errMu.Unlock()
					return
				}
			}
		}(g)
	}
	// Released together so the writes actually overlap. Without this the
	// goroutines tend to start in sequence and the test can pass on a machine
	// that has the defect.
	close(start)
	wg.Wait()

	for _, err := range runErrs {
		t.Errorf("an activation failed: %v", err)
	}

	got := readLines(t, &out)

	// THE COUNT, which is the assertion the defect fails.
	if len(got) != total {
		t.Errorf("%d goroutines printing %d lines each produced %d lines, want %d: "+
			"the writer lost %d",
			writerGoroutines, writerLinesEach, len(got), total, total-len(got))
	}
	// THE CONTENT, which catches a torn write that happens to keep the count.
	// A line the program cannot emit is corruption even when nothing is lost.
	seen := make(map[string]bool, total)
	for _, line := range got {
		if !want[line] {
			t.Errorf("the writer produced %q, which no activation can print", line)
			continue
		}
		if seen[line] {
			t.Errorf("the writer produced %q twice", line)
		}
		seen[line] = true
	}
	if len(seen) != total {
		missing := make([]string, 0, total-len(seen))
		for line := range want {
			if !seen[line] {
				missing = append(missing, line)
			}
		}
		sort.Strings(missing)
		if len(missing) > 8 {
			missing = append(missing[:8:8], "…")
		}
		t.Errorf("%d of %d distinct lines arrived; missing %v", len(seen), total, missing)
	}
}

// TestWriter_TheCountAssertionCanFail is the positive control for the test
// above. A ZERO IS VALIDATED BY PLANTING A POSITIVE.
//
// The specific way the acceptance test could be vacuous is that the machine
// never wrote anything at all, or that `Run("emit", …)` failed on every
// goroutine and the count was compared against zero either way. So this runs
// the same path serially and requires exactly the expected lines — including
// that a DIFFERENT argument produces a DIFFERENT line, because a writer that
// printed a constant would satisfy a count and satisfy nothing else.
func TestWriter_TheCountAssertionCanFail(t *testing.T) {
	mod := concurrentPrintIR(t)

	var out bytes.Buffer
	m := vm.New(mod, &out)
	if _, err := m.Run("emit", int64(7)); err != nil {
		t.Fatalf("one activation of emit: %v", err)
	}
	if _, err := m.Run("emit", int64(9)); err != nil {
		t.Fatalf("a second activation of emit: %v", err)
	}
	got := readLines(t, &out)
	if len(got) != 2 || got[0] != "line 7" || got[1] != "line 9" {
		t.Fatalf("the fixture's emit does not print one distinguishable line per call; got %q",
			got)
	}
}

// TestWriter_NewSerializesTheWriterItWasHanded states the placement as a
// check rather than as prose in a comment.
//
// `New` is where the obligation is discharged, so a caller who hands over a
// plain `bytes.Buffer` gets serialization without asking. Two reads of the
// machine's writer must answer the SAME wrapper, because a fresh mutex per
// read synchronizes nothing.
func TestWriter_NewSerializesTheWriterItWasHanded(t *testing.T) {
	var target bytes.Buffer
	m := vm.New(nil, &target)
	w := m.Output()
	if w == io.Writer(&target) {
		t.Fatal("Machine.Output answered the writer New was handed, so nothing serializes it")
	}
	// It must still be the same SINK. A wrapper that dropped writes would
	// pass the identity check above and be useless.
	if _, err := fmt.Fprintln(w, "through"); err != nil {
		t.Fatalf("writing through the machine's writer: %v", err)
	}
	if target.String() != "through\n" {
		t.Fatalf("the machine's writer does not reach the writer New was handed; target holds %q",
			target.String())
	}
	if m.Output() != w {
		t.Fatal("two reads of Machine.Output answered two wrappers, so each caller locks its own mutex")
	}
}

// TestWriter_TwoMachinesShareOneSinkThroughACallerSuppliedLock pins the LIMIT
// of the guarantee, which is the other half of the placement argument.
//
// `New` serializes PER MACHINE. Two machines handed the same raw buffer each
// hold their own mutex and still race, and wrapping at construction cannot
// close that. So the contract is: a caller sharing a sink hands over a
// writer that is already safe. This is that caller, and it must work.
func TestWriter_TwoMachinesShareOneSinkThroughACallerSuppliedLock(t *testing.T) {
	mod := concurrentPrintIR(t)

	var out bytes.Buffer
	shared := &callerLockedWriter{w: &out}
	first, second := vm.New(mod, shared), vm.New(mod, shared)

	total := writerGoroutines * writerLinesEach
	want := make(map[string]bool, total)
	for n := range total {
		want[fmt.Sprintf("line %d", n)] = true
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := range writerGoroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// Alternating machines, so the overlap is BETWEEN machines and
			// not merely within one.
			m := first
			if g%2 == 1 {
				m = second
			}
			<-start
			for i := range writerLinesEach {
				n := int64(g*writerLinesEach + i)
				if _, err := m.Run("emit", int64(n)); err != nil {
					t.Errorf("activation %d: %v", n, err)
					return
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()

	got := readLines(t, &out)
	if len(got) != total {
		t.Errorf("two machines over one caller-locked sink produced %d lines, want %d: lost %d",
			len(got), total, total-len(got))
	}
	seen := make(map[string]bool, total)
	for _, line := range got {
		if !want[line] {
			t.Errorf("the shared sink holds %q, which no activation can print", line)
			continue
		}
		seen[line] = true
	}
	if len(seen) != total {
		t.Errorf("%d of %d distinct lines arrived across the two machines", len(seen), total)
	}
}

// callerLockedWriter is the embedder-side half of the contract above: one
// mutex over one sink, shared by every machine the caller opens.
type callerLockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (c *callerLockedWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(p)
}

func readLines(t *testing.T, r io.Reader) []string {
	t.Helper()
	var lines []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the machine's output: %v", err)
	}
	return lines
}
