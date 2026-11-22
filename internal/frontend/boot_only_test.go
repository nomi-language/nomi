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
	return "import\n    std::jobs.{Queue, Worker}\n    std::jobs::testing.Inline\n    " + module + "\nend\n\n" + bootOnlyAppType
}

const bootOnlyAppType = `pub struct App
    context: Context
    queue: Queue
    worker: Worker
end

`

// A request handler in another module, a lambda the server would call long
// after boot, creating a worker: rejected at its call to Worker.new.
func TestBootOnly_WorkerNewInAnotherModulesHandlerIsRejectedAtTheCall(t *testing.T) {
	got := checkShop(t, map[string]string{
		"main.nomi": bootOnlyApp("handlers") + `fn boot() -> Result<App, String>
    queue: Queue = Inline.new()
    Ok(App{context: Context.root(), queue:, worker: Worker.new(queue, 1)})
end

fn main()
    handle = handlers.routes(App.queue)
    dbg handle(1)
end
`,
		"handlers.nomi": `import std::jobs.{Queue, Worker}

pub fn routes(queue: Queue) -> (Int) -> Int
    fn n ->
        _ = Worker.new(queue, 1)
        n
    end
end
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
		"main.nomi": bootOnlyApp("handlers") + `fn boot() -> Result<App, String>
    queue: Queue = Inline.new()
    Ok(App{context: Context.root(), queue:, worker: Worker.new(queue, 1)})
end

fn main()
    dbg handlers.handle(App.queue)
end
`,
		"handlers.nomi": `import std::jobs.{Queue, Worker}

pub fn handle(queue: Queue) -> Int
    _ = Worker.new(queue, 1)
    1
end
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
		"main.nomi": bootOnlyApp("workers") + `fn boot() -> Result<App, String>
    queue: Queue = Inline.new()
    Ok(App{context: Context.root(), queue:, worker: workers.make(queue)})
end

fn main()
    Worker.start(App.worker, [])!
end
`,
		"workers.nomi": `import std::jobs.{Queue, Worker}

pub fn make(queue: Queue) -> Worker
    Worker.new(queue, max_running: 2)
end
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
import std::supervisors.Supervisor

pub fn outer() -> Supervisor
    b.inner()
end
`
	bFile := `import std::supervisors.Supervisor

pub fn inner() -> Supervisor
    Supervisor.new(max_running: 1)
end
`
	rejected := checkShop(t, map[string]string{
		"main.nomi": `import a

fn main()
    _ = a.outer()
end
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
import std::supervisors.Supervisor

pub struct App
    context: Context
    group: Supervisor
end

fn boot() -> Result<App, String>
    Ok(App{context: Context.root(), group: a.outer()})
end

fn main()
    Unit
end
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
import std::supervisors.Supervisor

pub struct App
    context: Context
    make: () -> Supervisor
end

fn boot() -> Result<App, String>
    Ok(App{context: Context.root(), make: a.outer})
end

fn main()
    Unit
end
`,
		"a.nomi": `import std::supervisors.Supervisor

pub fn outer() -> Supervisor
    Supervisor.new(max_running: 1)
end
`,
	})
	want := "main.nomi:10:45: `outer` creates a supervisor (`Supervisor.new` at a.nomi:4), so it cannot be passed as a value"
	if len(got) != 1 || !strings.HasPrefix(got[0], want) {
		t.Fatalf("got %q; want one error starting %q", got, want)
	}
}

// workerNewBecause is the reason every call or naming of std::jobs's
// Worker.new gives: its chain is its own call of Supervisor.new.
const workerNewBecause = "`Worker.new` creates a supervisor \\(`Supervisor.new` at jobs.nomi:\\d+\\), "

// A std boot-only function the program reaches only from code that runs
// outside boot is reported where that code calls or names it, as the spec
// says: no function of the program need call it in boot for the check to
// know it is boot-only.
func TestBootOnly_AStdBootOnlyFunctionReachedOnlyOutsideBootIsRejected(t *testing.T) {
	callMessage := "so it can only be called while `boot` builds the app value, in boot or in something boot calls"
	for _, c := range []struct {
		name, src string
		want      []string
	}{
		{
			name: "a lambda in a pub function",
			src: `import
    std::jobs.Worker
    std::jobs::testing.Inline
end

fn main()
    Unit
end

pub fn later() -> List<Worker>
    [1, 2] |> Iter.map(fn _ -> Worker.new(Inline.new(), max_running: 2)) |> Iter.to_list()
end
`,
			want: []string{"^main.nomi:11:42: " + workerNewBecause + callMessage},
		},
		{
			name: "a test body",
			src: `import
    std::jobs.Worker
    std::jobs::testing.Inline
end

fn main()
    Unit
end

test "a worker"
    w = Worker.new(Inline.new(), max_running: 2)
    assert Worker.id(w) != ""
end
`,
			want: []string{"^main.nomi:11:19: " + workerNewBecause + callMessage},
		},
		{
			name: "a capture",
			src: `import std::jobs.{Queue, Worker}

fn main()
    Unit
end

pub fn later(queues: List<Queue>) -> List<Worker>
    queues |> Iter.map(&Worker.new(&1, max_running: 2)) |> Iter.to_list()
end
`,
			want: []string{"^main.nomi:8:35: " + workerNewBecause + callMessage},
		},
		{
			name: "a function value",
			src: `import std::jobs.Worker

fn main()
    make = Worker.new
    _ = make
end
`,
			want: []string{"^main.nomi:4:19: " + workerNewBecause + "so it cannot be passed as a value"},
		},
		{
			name: "a test body through the program's own helper",
			src: `import
    std::jobs.Worker
    std::jobs::testing.Inline
end

fn main()
    Unit
end

pub fn make_worker() -> List<Worker>
    [1] |> Iter.map(fn _ -> Worker.new(Inline.new(), max_running: 2)) |> Iter.to_list()
end

pub fn first_worker() -> Worker
    Worker.new(Inline.new(), max_running: 2)
end

test "a worker through a helper"
    w = first_worker()
    assert Worker.id(w) != ""
end
`,
			want: []string{
				"^main.nomi:11:39: " + workerNewBecause + callMessage,
				"^main.nomi:19:21: `first_worker` creates a supervisor \\(through `Worker.new` at main.nomi:15, then `Supervisor.new` at jobs.nomi:\\d+\\), " + callMessage,
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkShop(t, map[string]string{"main.nomi": c.src})
			if len(got) == 0 {
				t.Fatalf("the program is admitted; want %d errors matching %q", len(c.want), c.want)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %q; want %d errors matching %q", got, len(c.want), c.want)
			}
			for i, w := range c.want {
				if !regexp.MustCompile(w).MatchString(got[i]) {
					t.Errorf("error %d is %q; want one matching %s", i, got[i], w)
				}
			}
		})
	}
}

// A group's boot line builds the app in boot, so a worker the entry's boot
// makes is no error, and neither is a test's call of the entry boot.
func TestBootOnly_AWorkerMadeByTheEntryBootIsAcceptedInTests(t *testing.T) {
	got := checkShop(t, map[string]string{
		"main.nomi": `import
    std::jobs.{Queue, Worker}
    std::jobs::testing.Inline
end

` + bootOnlyAppType + `pub fn boot() -> Result<App, String>
    queue: Queue = Inline.new()
    Ok(App{context: Context.root(), queue:, worker: Worker.new(queue, max_running: 2)})
end

fn main()
    Worker.start(App.worker, [])!
end

group "the booted worker"
    boot boot()

    test "has an id"
        assert Worker.id(App.worker) != ""
    end
end

test "boot builds a worker"
    app = boot()!
    assert Worker.id(app.worker) != ""
end
`,
	})
	if len(got) != 0 {
		t.Fatalf("the program is rejected: %q", got)
	}
}
