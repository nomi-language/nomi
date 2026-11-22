package vm_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// inlineAllocProgram is a program the allocation comparison runs: its tests
// when args is nil, else its main with args.
type inlineAllocProgram struct {
	file string
	args []string
}

// inlineAllocPrograms are programs whose time goes to small calls made
// over and over while collections and records are updated: a set grown
// through a recursive scan and a generator mapped over each draw
// (baffling-birthdays), records rebuilt each turn (camicia), and three Are
// We Fast Yet programs, a constraint solver over maps and vectors
// (deltablue), a scheduler of records (richards) and a parser (json), at
// sizes where each allocates tens of megabytes or more, so the code a run
// compiles is a small part of what it allocates. Each compiles calls into
// their callers in its loops.
var inlineAllocPrograms = []inlineAllocProgram{
	{file: "tests/exercism/baffling-birthdays/baffling_birthdays_test.nomi"},
	{file: "tests/exercism/camicia/camicia_test.nomi"},
	{file: "benchmarks/awfy/deltablue.nomi", args: []string{"1", "1000"}},
	{file: "benchmarks/awfy/richards.nomi", args: []string{"1", "2"}},
	{file: "benchmarks/awfy/json.nomi", args: []string{"1", "20"}},
}

// maxInlineAllocRatio is the most a program may allocate with calls
// compiled into their callers, as a multiple of what it allocates with
// none compiled in. A view runs the callee's instructions over the
// caller's window, so the two allocate the same values; the margin is the
// views' own code.
const maxInlineAllocRatio = 1.05

// inlineAllocMinBytes is the least a run of an exercise's tests with no
// call compiled in allocates for the full walk to compare it: below it,
// compiling the views is most of the difference.
const inlineAllocMinBytes = 8 << 20

// Calls compiled into their callers allocate what the calls would: each
// program runs with and without them, and the bytes allocated with them
// stay within maxInlineAllocRatio of those without. An update the callee
// made in place that a view made copy, a value a view built per iteration
// that the call did not, or a machine a crossing in a view copied that the
// callee's activation kept (boundAt) multiplies the bytes. The run with
// them must compile more bodies than the run without, a view for each call
// compiled in, so the comparison is of code that ran as views. It takes
// about 8 s. NOMI_CORPUS_WALKS=full also compares every exercise under
// tests/exercism that allocates inlineAllocMinBytes or more (about 75 s
// more).
func TestInline_CorpusProgramsAllocateWhatTheCallsWould(t *testing.T) {
	if testing.Short() {
		t.Skip("runs programs twice each; -short")
	}
	root := filepath.Join("..", "..")
	programs := inlineAllocPrograms
	fixed := map[string]bool{}
	for _, p := range programs {
		fixed[p.file] = true
	}
	if rotation.CorpusWalk(t, 1).Full {
		files, err := filepath.Glob(filepath.Join(root, "tests", "exercism", "*", "*_test.nomi"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			rel, err := filepath.Rel(root, f)
			if err != nil {
				t.Fatal(err)
			}
			if rel = filepath.ToSlash(rel); !fixed[rel] {
				programs = append(programs, inlineAllocProgram{file: rel})
			}
		}
	}
	compared := 0
	for _, p := range programs {
		path := filepath.Join(root, filepath.FromSlash(p.file))
		os.Unsetenv(vm.NoInlineEnv)
		with, compiledWith := inlineRunBytes(t, path, p.args)
		t.Setenv(vm.NoInlineEnv, "1")
		without, compiledWithout := inlineRunBytes(t, path, p.args)
		os.Unsetenv(vm.NoInlineEnv)
		if !fixed[p.file] && without < inlineAllocMinBytes {
			continue
		}
		compared++
		ratio := float64(with) / float64(without)
		t.Logf("%s: %d bytes and %d compiles compiled in, %d bytes and %d compiles as calls, ratio %.3f",
			p.file, with, compiledWith, without, compiledWithout, ratio)
		if fixed[p.file] && compiledWith <= compiledWithout {
			t.Errorf("%s compiles no call into its caller, so it tests nothing", p.file)
		}
		if ratio > maxInlineAllocRatio {
			t.Errorf("%s allocates %d bytes with calls compiled in and %d as calls: %.2f times, more than %.2f",
				p.file, with, without, ratio, maxInlineAllocRatio)
		}
	}
	t.Logf("compared %d programs", compared)
}

// inlineRunBytes runs the program at path, its tests when args is nil and
// else its main with args, and answers the bytes the run allocated and how
// many bodies its machine compiled, functions and views (vm.CompileHook).
// Every case must pass and main must succeed.
func inlineRunBytes(t *testing.T, path string, args []string) (uint64, int64) {
	t.Helper()
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var compiled atomic.Int64
	vm.CompileHook = func(*ir.Func) { compiled.Add(1) }
	defer func() { vm.CompileHook = nil }()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if args == nil {
		for _, r := range p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}) {
			if r.Err != nil || len(r.Blocked) > 0 {
				t.Fatalf("%s: %s: %v %v", path, r.Name, r.Err, r.Blocked)
			}
		}
	} else if err := p.Run(context.Background(), &bytes.Buffer{}, args, false); err != nil {
		t.Fatalf("%s %v: %v", path, args, err)
	}
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, compiled.Load()
}
