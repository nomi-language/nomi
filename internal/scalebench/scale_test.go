// Package scalebench measures Nomi's front end, language server and VM on
// generated projects of about 1k, 10k and 50k lines (internal/projectgen)
// and on the stdlib, the largest real Nomi program. It holds tests only.
//
//	NOMI_SCALE_TABLE=1 go test ./internal/scalebench -run TestScaleTable -count=1 -v -timeout 0
//
// prints a table of time and allocations per operation and size.
// NOMI_SCALE_SIZES=1000,10000 picks the sizes. The same operations are Go
// benchmarks, for profiling one:
//
//	go test ./internal/scalebench -run '^$' -bench 'Scale/50000/signature_edit_hub' -cpuprofile cpu.out
package scalebench

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// project is a generated project written to disk.
type project struct {
	gen   *projectgen.Project
	dir   string
	entry string // main.nomi's absolute path
}

// projects caches each size's project for the process.
var projects = map[int]*project{}

func generated(tb testing.TB, lines int) *project {
	if p := projects[lines]; p != nil {
		return p
	}
	gen := projectgen.Generate(projectgen.Config{Seed: 1, Lines: lines})
	dir, err := os.MkdirTemp("", fmt.Sprintf("nomi-scale-%d-", lines))
	if err != nil {
		tb.Fatal(err)
	}
	if err := gen.Write(dir); err != nil {
		tb.Fatal(err)
	}
	p := &project{gen: gen, dir: dir, entry: filepath.Join(dir, "main.nomi")}
	projects[lines] = p
	return p
}

func (p *project) path(key string) string {
	return filepath.Join(p.dir, filepath.FromSlash(key)+".nomi")
}

func (p *project) uri(key string) string { return "file://" + p.path(key) }

// newManager is a document manager over the process's stdlib, as the
// language server makes one.
func newManager(root string) *analysis.DocumentManager {
	lib := std.Shared()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	dm.SetWorkspaceRoot(root)
	return dm
}

// op is one measured operation: setup prepares it outside the clock and
// answers the step a measurement repeats, with the step's index.
type op struct {
	name  string
	setup func(tb testing.TB, p *project) func(tb testing.TB, i int)
}

// checkFrontEnd runs `nomi check`'s front end over the entry.
func checkFrontEnd(tb testing.TB, path string) {
	if _, err := frontend.New(frontend.Config{HostTypesAreHandles: true}).CheckFile(path, frontend.Mode{}); err != nil {
		tb.Fatalf("the front end rejects %s: %v", path, err)
	}
}

// ops are the operations every generated size is measured on.
func ops() []op {
	list := []op{
		{"check (front end)", func(tb testing.TB, p *project) func(testing.TB, int) {
			return func(tb testing.TB, _ int) { checkFrontEnd(tb, p.entry) }
		}},
		{"nomi check (front end + lowering)", func(tb testing.TB, p *project) func(testing.TB, int) {
			return func(tb testing.TB, _ int) {
				if err := vmhost.Check(p.entry); err != nil {
					tb.Fatal(err)
				}
			}
		}},
		{"lsp: open the entry (full check)", func(tb testing.TB, p *project) func(testing.TB, int) {
			return func(tb testing.TB, _ int) {
				dm := newManager(p.dir)
				uri := p.uri(p.gen.Entry)
				dm.Open(uri, p.gen.Files[p.gen.Entry])
				dm.AnalyzeLatest(context.Background(), uri)
			}
		}},
	}
	for _, kind := range projectgen.EditKinds {
		list = append(list, op{"lsp: " + string(kind), editOp(kind)})
	}
	list = append(list,
		op{"lsp lowering, from the analysis", func(tb testing.TB, p *project) func(testing.TB, int) {
			dm := newManager(p.dir)
			uri := p.uri(p.gen.Entry)
			src := p.gen.Files[p.gen.Entry]
			dm.Open(uri, src)
			dm.AnalyzeLatest(context.Background(), uri)
			a := analyzedOf(dm.Snapshot(uri))
			if a == nil {
				tb.Fatal("the entry's analysis is not the program nomi check builds")
			}
			return func(tb testing.TB, _ int) {
				problems, ok, err := analyzedlowering.Check(p.entry, src, a)
				if !ok || problems != nil || err != nil {
					tb.Fatalf("lowering from the analysis: ok %v, problems %v, err %v", ok, problems, err)
				}
			}
		}},
		op{"lsp lowering, again after a save with no change", func(tb testing.TB, p *project) func(testing.TB, int) {
			// The language server's run after a save of the text its last
			// run lowered: the memo answers (analyzedlowering.Memo).
			dm := newManager(p.dir)
			uri := p.uri(p.gen.Entry)
			src := p.gen.Files[p.gen.Entry]
			dm.Open(uri, src)
			dm.AnalyzeLatest(context.Background(), uri)
			a := analyzedOf(dm.Snapshot(uri))
			if a == nil {
				tb.Fatal("the entry's analysis is not the program nomi check builds")
			}
			var memo analyzedlowering.Memo
			problems, ok, err := analyzedlowering.Check(p.entry, src, a)
			if !ok {
				tb.Fatal("the entry was not lowered from its analysis")
			}
			memo.Store(p.entry, src, a, problems, err)
			return func(tb testing.TB, _ int) {
				if _, _, ok := memo.Lookup(p.entry, src, a); !ok {
					tb.Fatal("the memo does not answer the same input")
				}
			}
		}},
		op{"lsp lowering, from the text", func(tb testing.TB, p *project) func(testing.TB, int) {
			src := p.gen.Files[p.gen.Entry]
			return func(tb testing.TB, _ int) {
				problems, err := vmhost.CheckLowering(p.entry, src)
				if problems != nil || err != nil {
					tb.Fatalf("lowering: problems %v, err %v", problems, err)
				}
			}
		}},
		op{"lsp lowering of a leaf, from the kept program", memberLowering(projectgen.BodyLeaf, true)},
		op{"lsp lowering of a leaf, from the text", memberLowering(projectgen.BodyLeaf, false)},
		op{"lsp lowering of a hub, from the kept program", memberLowering(projectgen.BodyHub, true)},
		op{"lsp lowering of a hub, from the text", memberLowering(projectgen.BodyHub, false)},
		op{"nomi run (load + run)", func(tb testing.TB, p *project) func(testing.TB, int) {
			return func(tb testing.TB, _ int) {
				prog, err := vmhost.Load(p.entry)
				if err != nil {
					tb.Fatal(err)
				}
				if err := prog.Run(context.Background(), io.Discard, nil, false); err != nil {
					tb.Fatal(err)
				}
			}
		}},
	)
	return list
}

// memberLowering is the language server's lowering run of the file kind
// edits, a file the entry reaches, open beside the entry: from the program
// the server keeps (analysis.EntryProgram offered through the entry), or,
// as `nomi check <file>` does, from its text with its own front end.
func memberLowering(kind projectgen.EditKind, kept bool) func(tb testing.TB, p *project) func(testing.TB, int) {
	return func(tb testing.TB, p *project) func(testing.TB, int) {
		key, _ := p.gen.Edit(kind, 0)
		path, src := p.path(key), p.gen.Files[key]
		if !kept {
			return func(tb testing.TB, _ int) {
				if _, err := vmhost.CheckLowering(path, src); err != nil {
					tb.Fatalf("lowering %s: %v", key, err)
				}
			}
		}
		dm := newManager(p.dir)
		dm.Open(p.uri(p.gen.Entry), p.gen.Files[p.gen.Entry])
		dm.AnalyzeLatest(context.Background(), p.uri(p.gen.Entry))
		dm.Open(p.uri(key), src)
		dm.AnalyzeLatest(context.Background(), p.uri(key))
		a := analyzedOf(dm.Snapshot(p.uri(key)))
		if a == nil {
			tb.Fatalf("%s's program is not offered for lowering", key)
		}
		return func(tb testing.TB, _ int) {
			if _, ok, err := analyzedlowering.Check(path, src, a); !ok || err != nil {
				tb.Fatalf("lowering %s from the kept program: ok %v, err %v", key, ok, err)
			}
		}
	}
}

// analyzedOf is the language server's analyzedOf (internal/lsp/lowering.go).
func analyzedOf(snap *analysis.DocSnapshot) *analyzedlowering.Analyzed {
	if snap == nil || snap.Program == nil || len(snap.Errors) > 0 || len(snap.Damaged) > 0 {
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

// lastPath is the recheck path each edit op's last step took, by op name
// and size, for the table.
var lastPath = map[string]string{}

// editOp opens the edited file in a language server's document manager,
// which checks the entry's program and keeps it, and then edits the file
// once per step: step i applies the kind's i-th edit (projectgen.Edit), so
// every step changes the text.
func editOp(kind projectgen.EditKind) func(tb testing.TB, p *project) func(testing.TB, int) {
	return func(tb testing.TB, p *project) func(testing.TB, int) {
		dm := newManager(p.dir)
		key, _ := p.gen.Edit(kind, 0)
		uri := p.uri(key)
		dm.Open(uri, p.gen.Files[key])
		dm.AnalyzeLatest(context.Background(), uri)
		requireClean(tb, dm, uri, "before the edits")
		return func(tb testing.TB, i int) {
			_, text := p.gen.Edit(kind, i)
			b0, d0, f0, _ := dm.ProgramChecks()
			dm.SetText(uri, text)
			if _, ok := dm.AnalyzeLatest(context.Background(), uri); !ok {
				tb.Fatalf("%s: step %d analyzed nothing", kind, i)
			}
			b1, d1, f1, _ := dm.ProgramChecks()
			path := "?"
			switch {
			case b1 > b0:
				path = "bodies"
			case d1 > d0:
				path = "dependents"
			case f1 > f0:
				path = "full"
			}
			lastPath[fmt.Sprintf("%s/%d", kind, len(p.gen.Units))] = path
			if i < 2 {
				requireClean(tb, dm, uri, fmt.Sprintf("after step %d of %s", i, kind))
			}
		}
	}
}

func requireClean(tb testing.TB, dm *analysis.DocumentManager, uri, when string) {
	snap := dm.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		tb.Fatalf("%s: %s has no analysis", when, uri)
	}
	if len(snap.Errors) > 0 {
		tb.Fatalf("%s: %s has a syntax error: %v", when, uri, snap.Errors[0])
	}
	if errs := snap.Analysis.TypeErrors; len(errs) > 0 {
		tb.Fatalf("%s: %s has %d diagnostics, the first: %s", when, uri, len(errs), errs[0].Message)
	}
	if snap.Program == nil && strings.HasSuffix(uri, "/main.nomi") {
		tb.Fatalf("%s: the entry's program is not offered for lowering", when)
	}
}

// result is one measurement.
type result struct {
	perOp  time.Duration
	allocs uint64
	bytes  uint64
	n      int
	note   string
}

func (r result) cell() string {
	if r.n == 0 {
		return r.note
	}
	s := fmt.Sprintf("%s, %s allocs", dur(r.perOp), count(r.allocs))
	if r.note != "" {
		s += " (" + r.note + ")"
	}
	return s
}

func dur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2f s", d.Seconds())
	case d >= 10*time.Millisecond:
		return fmt.Sprintf("%.0f ms", float64(d)/1e6)
	default:
		return fmt.Sprintf("%.2f ms", float64(d)/1e6)
	}
}

func count(n uint64) string {
	switch {
	case n >= 10_000_000:
		return fmt.Sprintf("%.0fM", float64(n)/1e6)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return strconv.FormatUint(n, 10)
}

// measure runs step at least three times and until about budget has passed,
// after one unmeasured run, and answers the mean per step.
func measure(tb testing.TB, step func(testing.TB, int), budget time.Duration) result {
	step(tb, 0)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	n := 0
	for n < 3 || time.Since(start) < budget && n < 1000 {
		step(tb, n+1)
		n++
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	return result{
		perOp:  elapsed / time.Duration(n),
		allocs: (after.Mallocs - before.Mallocs) / uint64(n),
		bytes:  (after.TotalAlloc - before.TotalAlloc) / uint64(n),
		n:      n,
	}
}

// heldByKeptProgram answers the heap a language server's kept program of
// p holds after a collection: the document manager with the entry open.
func heldByKeptProgram(tb testing.TB, p *project) result {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	dm := newManager(p.dir)
	uri := p.uri(p.gen.Entry)
	dm.Open(uri, p.gen.Files[p.gen.Entry])
	dm.AnalyzeLatest(context.Background(), uri)
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(dm)
	return result{note: fmt.Sprintf("%.1f MB", float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/1e6)}
}

// coldCheck runs the front end once in a fresh process, after it loads the
// stdlib: the first check a `nomi check` makes. what is a generated
// project's directory, or "std" for a fresh analysis of the stdlib.
func coldCheck(tb testing.TB, what string) result {
	cmd := exec.Command(os.Args[0], "-test.run=^TestScaleColdChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), "NOMI_SCALE_COLD="+what)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("cold check of %s: %v\n%s", what, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		var ns, allocs, bytes uint64
		if _, err := fmt.Sscanf(line, "COLD %d %d %d", &ns, &allocs, &bytes); err == nil {
			return result{perOp: time.Duration(ns), allocs: allocs, bytes: bytes, n: 1}
		}
	}
	tb.Fatalf("cold check of %s printed no measurement:\n%s", what, out)
	return result{}
}

// TestScaleColdChild is coldCheck's child process.
func TestScaleColdChild(t *testing.T) {
	what := os.Getenv("NOMI_SCALE_COLD")
	if what == "" {
		t.Skip("run by TestScaleTable")
	}
	run := func() { checkFrontEnd(t, filepath.Join(what, "main.nomi")) }
	if what == "std" {
		run = func() { std.Load() }
	} else {
		std.Shared()
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	run()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	fmt.Printf("COLD %d %d %d\n", elapsed.Nanoseconds(), after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc)
}

// TestScaleTable prints the table. It runs only when NOMI_SCALE_TABLE is
// set; at 50k lines it takes a few minutes.
func TestScaleTable(t *testing.T) {
	if os.Getenv("NOMI_SCALE_TABLE") == "" {
		t.Skip("set NOMI_SCALE_TABLE=1 to print the scaling table")
	}
	sizes := []int{1000, 10000, 50000}
	if s := os.Getenv("NOMI_SCALE_SIZES"); s != "" {
		sizes = nil
		for _, f := range strings.Split(s, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil {
				t.Fatalf("NOMI_SCALE_SIZES: %v", err)
			}
			sizes = append(sizes, n)
		}
	}
	std.Shared()
	budget := 2 * time.Second
	header := []string{"operation"}
	rows := map[string][]string{}
	var order []string
	add := func(name, cell string) {
		if _, ok := rows[name]; !ok {
			order = append(order, name)
		}
		rows[name] = append(rows[name], cell)
	}
	for _, lines := range sizes {
		p := generated(t, lines)
		header = append(header, fmt.Sprintf("%d lines, %d files", p.gen.Lines(), len(p.gen.Files)-len(p.gen.Tests)))
		add("check, cold (front end, own process)", coldCheck(t, p.dir).cell())
		for _, o := range ops() {
			r := measure(t, o.setup(t, p), budget)
			if strings.HasPrefix(o.name, "lsp: ") && o.name != "lsp: open the entry (full check)" {
				r.note = lastPath[fmt.Sprintf("%s/%d", strings.TrimPrefix(o.name, "lsp: "), len(p.gen.Units))]
			}
			add(o.name, r.cell())
			t.Logf("%d lines: %s: %s", lines, o.name, r.cell())
		}
		add("kept program, heap after GC", heldByKeptProgram(t, p).cell())
	}
	// The stdlib: 8.7k lines analyzed as its own program (std.Load).
	header = append(header, "stdlib (std.Load)")
	for _, name := range order {
		cell := "n/a"
		switch name {
		case "check, cold (front end, own process)":
			cell = coldCheck(t, "std").cell()
		case "check (front end)":
			cell = measure(t, func(testing.TB, int) { std.Load() }, budget).cell()
		case "kept program, heap after GC":
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			lib := std.Load()
			runtime.GC()
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(lib)
			cell = fmt.Sprintf("%.1f MB", float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/1e6)
		}
		rows[name] = append(rows[name], cell)
	}
	var b strings.Builder
	b.WriteString("\n| " + strings.Join(header, " | ") + " |\n|" + strings.Repeat("---|", len(header)) + "\n")
	for _, name := range order {
		b.WriteString("| " + name + " | " + strings.Join(rows[name], " | ") + " |\n")
	}
	fmt.Print(b.String())
}

// BenchmarkScale runs each operation at each size as a Go benchmark, for
// profiling: -bench 'Scale/50000/signature_edit_hub'.
func BenchmarkScale(b *testing.B) {
	std.Shared()
	for _, lines := range []int{1000, 10000, 50000} {
		b.Run(strconv.Itoa(lines), func(b *testing.B) {
			p := generated(b, lines)
			for _, o := range ops() {
				name := strings.NewReplacer("lsp: ", "", " ", "_", ",", "", "(", "", ")", "", "+", "").Replace(o.name)
				name = strings.ReplaceAll(name, "__", "_")
				b.Run(name, func(b *testing.B) {
					step := o.setup(b, p)
					step(b, 0)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						step(b, i+1)
					}
				})
			}
		})
	}
}

// TestMain removes the projects the run wrote.
func TestMain(m *testing.M) {
	code := m.Run()
	for _, p := range projects {
		os.RemoveAll(p.dir)
	}
	os.Exit(code)
}
