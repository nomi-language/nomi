package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// The Context field's reads and rebinds, and `Context.with_timeout`, in
// retained bodies: the Tour program that bounds a five-minute sleep with a
// 50ms deadline, and focused programs for the rebind's floor, its release at
// the activation's exit, a custom boot's Context field and a deadline that
// does not expire.
func TestIRContext_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"concurrency.md:L800", `import {
  std/tasks.Task
  std/duration.Duration
  std/timer
}

fn slow(): Int {
  timer.sleep(Duration.minutes(5))
  1
}

// The deadline covers ` + "`slow`" + ` and nothing else.
fn slow_bounded(): Int {
  with Run.context = Context.with_timeout(
    Run.context,
    Duration.milliseconds(50),
  )

  slow()
}

fn main(): String {
  outcome = concurrent {
    t = Task.spawn(|| slow_bounded())
    Task.outcome(t)
  }

  // Reached because ` + "`Task.outcome`" + ` sits outside the deadline.
  result = case outcome {
    .Completed(v) -> "got " + Display.to_string(v)
    .Cancelled -> "timed out, falling back"
    .Failed(_) -> "broke"
  }
  dbg result
}

struct Run {
  context: Context
}

fn boot(): Run {
  Run{context: Context.root()}
}
`, "dbg line 34: result = \"timed out, falling back\"\n"},
		{"a wider inner rebind keeps the outer deadline", `import {
  std/tasks.Task
  std/duration.Duration
  std/timer
}

fn inner(): Int {
  with Run.context = Context.with_timeout(Run.context, Duration.minutes(10))
  timer.sleep(Duration.minutes(5))
  1
}

fn outer(): Int {
  with Run.context = Context.with_timeout(Run.context, Duration.milliseconds(40))
  inner()
}

fn main() {
  outcome = concurrent {
    t = Task.spawn(|| outer())
    Task.outcome(t)
  }
  dbg outcome
  Unit
}
struct Run {
  context: Context
}

fn boot(): Run {
  Run{context: Context.root()}
}
`, "dbg line 23: outcome = Cancelled\n"},
		{"the deadline ends with the rebinding activation", `import {
  std/duration.Duration
  std/timer
}

fn bounded(n: Int): Int {
  with Run.context = Context.with_timeout(Run.context, Duration.milliseconds(20))
  n + 1
}

fn main() {
  a = bounded(1)
  timer.sleep(Duration.milliseconds(60))
  dbg a
  Unit
}
struct Run {
  context: Context
}

fn boot(): Run {
  Run{context: Context.root()}
}
`, "dbg line 14: a = 2\n"},
		{"a custom boot's Context field and a deadline that does not expire", `import {
  std/tasks.Task
  std/duration.Duration
  std/timer
}

struct App {
  ctx: Context
  label: String
}

fn boot(): App {
  App{ctx: Context.root(), label: "app"}
}

fn quick(): Int {
  timer.sleep(Duration.milliseconds(5))
  7
}

fn roomy(): Int {
  with App.ctx = Context.with_timeout(App.ctx, Duration.minutes(5))
  quick()
}

fn tight(): Int {
  with App.ctx = Context.with_timeout(App.ctx, Duration.milliseconds(30))
  timer.sleep(Duration.minutes(5))
  1
}

fn main() {
  outcomes = concurrent {
    a = Task.spawn(|| roomy())
    b = Task.spawn(|| tight())
    (Task.outcome(a), Task.outcome(b))
  }
  dbg outcomes
  dbg App.label
  Unit
}
`, "dbg line 38: outcomes = (Completed(7), Cancelled)\ndbg line 39: App.label = \"app\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// Context operations the builder does not lower are declined: the enclosing function
// is not retained.
func TestIRContext_UnsupportedShapesPreserveEmission(t *testing.T) {
	for _, body := range []string{
		// A Context value whose type (a generic Maybe) has no single-name identity.
		"_ = Context.with_value(Context.with_timeout(Run.context, Duration.seconds(1)), Some(1))\n 1",
	} {
		p, err := AnalyzeSource("main.nomi", "import std/duration.Duration\nfn run(): Int {\n"+body+"\n}\nfn main(): Int { run() }\n"+
			"struct Run {\n context: Context\n}\nfn boot(): Run { Run{context: Context.root()} }")
		if err != nil {
			t.Fatalf("the front end now rejects this, so the preservation below is untested: %v", err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range got.IR {
			for _, f := range mod.Funcs() {
				if f.Name() == "run" {
					t.Fatalf("unsupported Context shape was retained:\n%s", body)
				}
			}
		}
	}
}

// A deadline that runs out while the MAIN LINE waits inside a `concurrent`
// block reports the deadline and exits 1. The block runs on rt.EnterScope's
// frame; when that frame dropped the rebind's `underDeadline` flag, the
// program died with `concurrent: cancellation reached a frame with no task to
// unwind` instead. The first program is the corpus case "env context timeout cancels blocked channel
// receives" as a standalone main; its rebind sits in a nested block, which the
// IR does not retain, so the VM row runs the second program, whose rebind
// opens a retained function.
func TestIRContext_AnExpiredDeadlineInABlockOnTheMainLine(t *testing.T) {
	const deadline = "deadline exceeded: the context in force when `main` blocked ran out"
	for _, tc := range []struct {
		name, src string
		vm        bool
	}{
		{"nested block rebind", `import {
  std/channels.{Channel, Receiver}
  std/duration.Duration
  std/io
  std/tasks.Task
}

struct Config {
  context: Context
}

fn boot(): Config {
  Config{context: Context.root()}
}

fn main() {
  ch: Channel<Int> = Channel.unbuffered()

  {
    with Config.context = Context.with_timeout(Config.context, Duration.milliseconds(50))
    io.print("entered")
    concurrent {
      r1 = Task.spawn(|| Receiver.receive(ch.receiver))
      r2 = Task.spawn(|| Receiver.receive(ch.receiver))
      _v1 = Task.await(r1)
      _v2 = Task.await(r2)
    }

    io.print("unreachable")
  }
}
`, false},
		{"function rebind", `import {
  std/duration.Duration
  std/io
  std/tasks.Task
  std/timer
}

fn slow(): Int {
  timer.sleep(Duration.minutes(5))
  1
}

fn bounded(): Int {
  with Run.context = Context.with_timeout(Run.context, Duration.milliseconds(50))
  io.print("entered")
  n = concurrent {
    t = Task.spawn(|| slow())
    Task.await(t)
  }
  io.print("unreachable")
  n
}

fn main() {
  dbg bounded()
  Unit
}
struct Run {
  context: Context
}

fn boot(): Run {
  Run{context: Context.root()}
}
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.nomi")
			if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := Analyze(path)
			if err != nil {
				t.Fatal(err)
			}
			res, _, err := GenerateIR(p)
			if err != nil {
				t.Fatal(err)
			}
			rows := []struct {
				via string
				got observation
			}{{"vm command", vmReference(path)}}
			for _, row := range rows {
				if row.got.exit != 1 || row.got.stdout != "entered\n" || !strings.Contains(row.got.stderr, deadline) {
					t.Errorf("%s: %s; want exit 1, stdout %q, the deadline on stderr", row.via, row.got, "entered\n")
				}
			}
			if !tc.vm {
				return
			}
			var entry *ir.Module
			retained := map[string]bool{}
			for _, mod := range res.IR {
				for _, f := range mod.Funcs() {
					retained[f.Name()] = true
					if f.Name() == "main" {
						entry = mod
					}
				}
			}
			if !retained["main"] || !retained["bounded"] {
				t.Fatalf("main or bounded was not retained (%v), so the VM row is untested", retained)
			}
			var out bytes.Buffer
			booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
			if err != nil {
				t.Fatal(err)
			}
			_, err = booted.Run("main")
			if err == nil || !strings.Contains(err.Error(), deadline) || out.String() != "entered\n" {
				t.Fatalf("VM: %v; output %q", err, out.String())
			}
		})
	}
}
