package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// The once-cycle check (analysis.CheckOnceCycles): a `once` whose
// initializer reaches the `once` itself, through reads and the calls the
// checker resolved, is an error at its declaration naming the chain.

// onceCycleErrors opens files (main.nomi among them) in a document manager
// and answers, per file, the once-cycle diagnostics its document carries,
// as "line:col: message".
func onceCycleErrors(t *testing.T, files map[string]string) map[string][]string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dm := newManager()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		dm.Open(fileuri.FromPath(filepath.Join(dir, name)), files[name])
	}
	out := map[string][]string{}
	for _, name := range names {
		out[name] = onceCycleMessages(analysis.InstalledForTest(dm, fileuri.FromPath(filepath.Join(dir, name))).TypeErrors)
	}
	return out
}

func onceCycleMessages(errs []analysis.TypeError) []string {
	var out []string
	for _, e := range errs {
		if strings.Contains(e.Message, "cyclic 'once' binding") {
			out = append(out, fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message))
		}
	}
	return out
}

func wantCycles(t *testing.T, got map[string][]string, want map[string][]string) {
	t.Helper()
	for file := range got {
		if !slices.Equal(got[file], want[file]) {
			t.Errorf("%s reports:\n%s\nwant:\n%s", file, strings.Join(got[file], "\n"), strings.Join(want[file], "\n"))
		}
	}
}

func TestOnceCycle_ADirectSelfReadIsAnError(t *testing.T) {
	got := onceCycleErrors(t, map[string]string{"main.nomi": `once count: Int = count + 1

fn main()
    dbg count
end
`})
	wantCycles(t, got, map[string][]string{"main.nomi": {
		"1:6: cyclic 'once' binding: `count` depends on itself: it reads `once count` (main.nomi:1)",
	}})
}

// Each `once` on the cycle is reported, its chain starting from itself.
func TestOnceCycle_TwoOncesReadingEachOtherAreEachAnError(t *testing.T) {
	got := onceCycleErrors(t, map[string]string{"main.nomi": `once a: Int = b + 1
once b: Int = a * 2

fn main()
    dbg a
end
`})
	wantCycles(t, got, map[string][]string{"main.nomi": {
		"1:6: cyclic 'once' binding: `a` depends on itself: it reads `once b` (main.nomi:1), which reads `once a` (main.nomi:2)",
		"2:6: cyclic 'once' binding: `b` depends on itself: it reads `once a` (main.nomi:2), which reads `once b` (main.nomi:1)",
	}})
}

func TestOnceCycle_ACycleThroughAFunctionIsAnError(t *testing.T) {
	got := onceCycleErrors(t, map[string]string{"main.nomi": `fn read_seed() -> Int
    seed
end

fn grow(n: Int) -> Int
    read_seed() + n
end

once seed: Int = grow(1)

fn main()
    dbg seed
end
`})
	wantCycles(t, got, map[string][]string{"main.nomi": {
		"9:6: cyclic 'once' binding: `seed` depends on itself: it calls `grow` (main.nomi:9), which calls `read_seed` (main.nomi:6), which reads `once seed` (main.nomi:2)",
	}})
}

// A cycle across modules is reported in each module whose `once` is on it,
// and an impl's `once` is named with its type.
func TestOnceCycle_ACycleAcrossModulesIsAnErrorInEach(t *testing.T) {
	got := onceCycleErrors(t, map[string]string{
		"main.nomi": `import config

fn main()
    dbg config.limit
end
`,
		"config.nomi": `import rates

pub once limit: Int = rates.scaled(10)

pub fn base() -> Int
    rates.Rate.standard.n
end
`,
		"rates.nomi": `import config

pub struct Rate
    n: Int
end

impl Rate
    pub once standard: Rate = Rate{n: config.limit}
end

pub fn scaled(n: Int) -> Int
    n * config.base()
end
`,
	})
	wantCycles(t, got, map[string][]string{
		"config.nomi": {
			"3:10: cyclic 'once' binding: `limit` depends on itself: it calls `scaled` (config.nomi:3), which calls `base` (rates.nomi:12), which reads `once Rate.standard` (config.nomi:6), which reads `once limit` (rates.nomi:8)",
		},
		"rates.nomi": {
			"8:14: cyclic 'once' binding: `Rate.standard` depends on itself: it reads `once limit` (rates.nomi:8), which calls `scaled` (config.nomi:3), which calls `base` (rates.nomi:12), which reads `once Rate.standard` (config.nomi:6)",
		},
	})
}

// An interface call the checker resolves to one impl, on a value of a
// concrete type, is followed into that impl. A call through a value of the
// interface type is not: which impl runs is known only at run time, and the
// run-time check reports that cycle.
func TestOnceCycle_AnInterfaceCallIsFollowedOnlyWhenTheCheckerResolvedIt(t *testing.T) {
	got := onceCycleErrors(t, map[string]string{"main.nomi": `interface Seeded
    fn seed_of(value: self) -> Int
end

struct Box
    n: Int
end

struct Crate
    n: Int
end

impl Seeded for Box
    fn seed_of(b: Box) -> Int
        resolved + b.n
    end
end

impl Seeded for Crate
    fn seed_of(c: Crate) -> Int
        dispatched + c.n
    end
end

fn through(s: Seeded) -> Int
    Seeded.seed_of(s)
end

once resolved: Int = Seeded.seed_of(Box{n: 1})
once dispatched: Int = through(Crate{n: 2})

fn main()
    dbg resolved
    dbg dispatched
end
`})
	wantCycles(t, got, map[string][]string{"main.nomi": {
		"29:6: cyclic 'once' binding: `resolved` depends on itself: it calls `Box.seed_of` (main.nomi:29), which reads `once resolved` (main.nomi:15)",
	}})
}

// A lambda that reads the `once` and is stored, not called, while the
// initializer runs is no cycle. A lambda a `then` or `tap` stage applies
// where it is written runs during initialization, and is one.
func TestOnceCycle_AStoredLambdaIsNoCycleAndAnAppliedOneIs(t *testing.T) {
	got := onceCycleErrors(t, map[string]string{"main.nomi": `once retry: () -> Int = fn -> retry() + 1
once handlers: List<() -> Int> = [fn -> List.length(handlers)]
once piped: Int = 3 |> then fn v -> v + piped

fn main()
    dbg List.length(handlers)
    dbg piped
end
`})
	wantCycles(t, got, map[string][]string{"main.nomi": {
		"3:6: cyclic 'once' binding: `piped` depends on itself: it reads `once piped` (main.nomi:3)",
	}})
}

// An unannotated `once` whose value reads its own type draws type
// inference's error, and no second one from this check.
func TestOnceCycle_ATypeCycleIsReportedOnce(t *testing.T) {
	dir := t.TempDir()
	src := `once a = [b]
once b = [a]

fn main()
    dbg a
end
`
	if err := os.WriteFile(filepath.Join(dir, "main.nomi"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	dm := newManager()
	dm.Open(uri, src)
	errs := analysis.InstalledForTest(dm, uri).TypeErrors
	if got := onceCycleMessages(errs); len(got) != 0 {
		t.Fatalf("the once-cycle check reports %q beside type inference", got)
	}
	var typeCycles int
	for _, e := range errs {
		if strings.Contains(e.Message, "depends on its own type") {
			typeCycles++
		}
	}
	if typeCycles != 2 {
		t.Fatalf("type inference reports %d cycles, want 2: %v", typeCycles, messages(errs))
	}
}

// An edit to one module that closes a cycle through another rechecks the
// other's open document: its check reads the program as it is now.
func TestOnceCycle_AnEditThatClosesACycleRechecksTheOtherModule(t *testing.T) {
	main := `import lib

once total: Int = lib.count() + 1

fn main()
    dbg total
end
`
	plain := `pub fn count() -> Int
    1
end
`
	cyclic := `import main

pub fn count() -> Int
    main.total
end
`
	dir := t.TempDir()
	for name, text := range map[string]string{"main.nomi": main, "lib.nomi": plain} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mainURI := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	libURI := fileuri.FromPath(filepath.Join(dir, "lib.nomi"))
	dm := newManager()
	dm.Open(mainURI, main)
	dm.Open(libURI, plain)
	dm.UpdateImportEdges(mainURI)
	dm.UpdateImportEdges(libURI)
	if errs := onceCycleMessages(analysis.InstalledForTest(dm, mainURI).TypeErrors); len(errs) != 0 {
		t.Fatalf("before the edit main reports %q", errs)
	}

	dm.Update(libURI, cyclic)
	dm.PropagateChange(libURI)
	dm.UpdateImportEdges(libURI)
	want := "3:6: cyclic 'once' binding: `total` depends on itself: it calls `count` (main.nomi:3), which reads `once total` (lib.nomi:4)"
	if errs := onceCycleMessages(analysis.InstalledForTest(dm, mainURI).TypeErrors); len(errs) != 1 || errs[0] != want {
		t.Fatalf("after the edit main reports %q; want %q", errs, want)
	}

	dm.Update(libURI, plain)
	dm.PropagateChange(libURI)
	if errs := onceCycleMessages(analysis.InstalledForTest(dm, mainURI).TypeErrors); len(errs) != 0 {
		t.Fatalf("after the edit is undone main reports %q", errs)
	}
}
