package frontend_test

import (
	"regexp"
	"strings"
	"testing"
)

// The boot-only check follows calls across modules, std included
// (analysis.CheckBootScope): a function that reaches Supervisor.new can be
// called only while boot builds the app value, and a call from anywhere else
// is an error at that call, naming the chain down to Supervisor.new.

// bootOnlyApp is an app's imports, with the module %s imports beside std's,
// and its app type.
func bootOnlyApp(module string) string {
	return "import {\n    std/jobs.{Queue, Worker}\n    std/jobs_testing.Inline\n    " + module + "\n}\n\n" + bootOnlyAppType
}

const bootOnlyAppType = `pub struct App {
    context: Context
    queue: Queue
    worker: Worker
}

`

// A request handler in another module, a lambda the server would call long
// after boot, creating a worker: rejected at its call to Worker.new.
func TestBootOnly_WorkerNewInAnotherModulesHandlerIsRejectedAtTheCall(t *testing.T) {
	got := checkShop(t, map[string]string{
		"main.nomi": bootOnlyApp("handlers") + `fn boot() -> Result<App, String> {
    queue: Queue = Inline.new()
    Ok(App{context: Context.root(), queue, worker: Worker.new(queue, 1)})
}

fn main() {
    handle = handlers.routes(App.queue)
    dbg handle(1)
}
`,
		"handlers.nomi": `import std/jobs.{Queue, Worker}

pub fn routes(queue: Queue) -> (Int) -> Int {
    fn n -> {
        _ = Worker.new(queue, 1)
        n
    }
}
`,
	})
	want := regexp.MustCompile("^handlers.nomi:5:23: `Worker.new` creates a supervisor \\(`Supervisor.new` at jobs.nomi:\\d+\\), " +
		"so it can only be called while `boot` builds the app value, in boot or in something boot calls")
	if len(got) != 1 || !want.MatchString(got[0]) {
		t.Fatalf("got %q; want one error matching %s", got, want)
	}
}

// The same handler as a function another module calls from main: rejected at
// main's call, naming the chain through Worker.new.
func TestBootOnly_AFunctionReachingWorkerNewIsRejectedWhereMainCallsIt(t *testing.T) {
	got := checkShop(t, map[string]string{
		"main.nomi": bootOnlyApp("handlers") + `fn boot() -> Result<App, String> {
    queue: Queue = Inline.new()
    Ok(App{context: Context.root(), queue, worker: Worker.new(queue, 1)})
}

fn main() {
    dbg handlers.handle(App.queue)
}
`,
		"handlers.nomi": `import std/jobs.{Queue, Worker}

pub fn handle(queue: Queue) -> Int {
    _ = Worker.new(queue, 1)
    1
}
`,
	})
	want := regexp.MustCompile("^main.nomi:\\d+:\\d+: `handle` creates a supervisor \\(through `Worker.new` at handlers.nomi:4, " +
		"then `Supervisor.new` at jobs.nomi:\\d+\\), so it can only be called while `boot` builds the app value")
	if len(got) != 1 || !want.MatchString(got[0]) {
		t.Fatalf("got %q; want one error matching %s", got, want)
	}
}

// Worker.new called in boot, directly and through another module's
// function, is accepted.
func TestBootOnly_WorkerNewInBootIsAccepted(t *testing.T) {
	got := checkShop(t, map[string]string{
		"main.nomi": bootOnlyApp("workers") + `fn boot() -> Result<App, String> {
    queue: Queue = Inline.new()
    Ok(App{context: Context.root(), queue, worker: workers.make(queue)})
}

fn main() {
    Worker.start(App.worker, [])
}
`,
		"workers.nomi": `import std/jobs.{Queue, Worker}

pub fn make(queue: Queue) -> Worker {
    Worker.new(queue, max_running: 2)
}
`,
	})
	if len(got) != 0 {
		t.Fatalf("the program is rejected: %q", got)
	}
}

// Two hops across modules: main calls a.outer, which calls b.inner, which
// creates a supervisor. Rejected from main, accepted from boot.
func TestBootOnly_ATwoHopChainAcrossModules(t *testing.T) {
	a := `import b
import std/supervisors.Supervisor

pub fn outer() -> Supervisor {
    b.inner()
}
`
	bFile := `import std/supervisors.Supervisor

pub fn inner() -> Supervisor {
    Supervisor.new(max_running: 1)
}
`
	rejected := checkShop(t, map[string]string{
		"main.nomi": `import a

fn main() {
    _ = a.outer()
}
`,
		"a.nomi": a,
		"b.nomi": bFile,
	})
	want := "main.nomi:4:16: `outer` creates a supervisor (through `inner` at a.nomi:5, then `Supervisor.new` at b.nomi:4), " +
		"so it can only be called while `boot` builds the app value"
	if len(rejected) != 1 || !strings.HasPrefix(rejected[0], want) {
		t.Fatalf("got %q; want one error starting %q", rejected, want)
	}

	accepted := checkShop(t, map[string]string{
		"main.nomi": `import a
import std/supervisors.Supervisor

pub struct App {
    context: Context
    group: Supervisor
}

fn boot() -> Result<App, String> {
    Ok(App{context: Context.root(), group: a.outer()})
}

fn main() {
    Unit
}
`,
		"a.nomi": a,
		"b.nomi": bFile,
	})
	if len(accepted) != 0 {
		t.Fatalf("the program is rejected: %q", accepted)
	}
}

// A boot-only function named as a value is refused where it is named, even
// in boot: a function value may be called anywhere.
func TestBootOnly_ABootOnlyFunctionAsAValueIsRejected(t *testing.T) {
	got := checkShop(t, map[string]string{
		"main.nomi": `import a
import std/supervisors.Supervisor

pub struct App {
    context: Context
    make: () -> Supervisor
}

fn boot() -> Result<App, String> {
    Ok(App{context: Context.root(), make: a.outer})
}

fn main() {
    Unit
}
`,
		"a.nomi": `import std/supervisors.Supervisor

pub fn outer() -> Supervisor {
    Supervisor.new(max_running: 1)
}
`,
	})
	want := "main.nomi:10:45: `outer` creates a supervisor (`Supervisor.new` at a.nomi:4), so it cannot be passed as a value"
	if len(got) != 1 || !strings.HasPrefix(got[0], want) {
		t.Fatalf("got %q; want one error starting %q", got, want)
	}
}
