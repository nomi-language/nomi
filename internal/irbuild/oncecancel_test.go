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

// A `once` RHS runs under the FORCER's cancellation and deadline, and a
// cancelled force leaves the cell unforced. The failure modes each row guards:
// an RHS run with no concurrent state, which a `Task.cancel` or a deadline
// cannot reach, so the program waits out a five-minute sleep; and an RHS frame
// built without its task or deadline, which reports `cancellation reached a
// frame with no task to unwind`. The last row checks a field read straight off
// a `once` (`p.x`).
func TestOnceCancellation_RunsOnTheVM(t *testing.T) {
	const deadline = "deadline exceeded: the context in force when `main` blocked ran out"
	for _, tc := range []struct {
		name, src, stdout, stderr string
		exit                      int
	}{
		{"task cancelled while forcing", `import {
  std/duration.Duration
  std/tasks.Task
  std/timer
}

once slow: Int = {
  timer.sleep(Duration.minutes(5))
  1
}

fn main() {
  outcome = concurrent {
    t = Task.spawn(|| slow)
    timer.sleep(Duration.milliseconds(20))
    Task.cancel(t)
    Task.outcome(t)
  }
  dbg outcome
  Unit
}
`, "dbg line 19: outcome = Cancelled\n", "", 0},
		{"deadline runs out while main forces", `import {
  std/duration.Duration
  std/io
  std/timer
}

once slow: Int = {
  timer.sleep(Duration.minutes(5))
  1
}

fn main() {
  with Run.context = Context.with_timeout(Run.context, Duration.milliseconds(50))
  io.print("entered")
  dbg slow
  Unit
}
struct Run {
  context: Context
}

fn boot(): Run {
  Run{context: Context.root()}
}
`, "entered\n", deadline, 1},
		{"a later force re-runs the RHS", `import {
  std/channels.{Channel, Receiver, Sender}
  std/io
  std/tasks.Task
}

once started: Channel<Int> = Channel.buffered(2)

once gate: Channel<Int> = Channel.buffered(1)

once slow: Int = {
  io.print("forcing")
  _ = Sender.send(started.sender, 1)
  _ = Receiver.receive(gate.receiver)
  1
}

fn main() {
  outcome = concurrent {
    t = Task.spawn(|| slow)
    _ = Receiver.receive(started.receiver)
    Task.cancel(t)
    Task.outcome(t)
  }
  dbg outcome
  _ = Sender.send(gate.sender, 1)
  dbg slow
  dbg slow
  Unit
}
`, "forcing\ndbg line 25: outcome = Cancelled\nforcing\ndbg line 27: slow = 1\ndbg line 28: slow = 1\n", "", 0},
		{"a field read off a once", `struct P {
  x: Int
}

once p: P = P{x: 1}

fn main() {
  dbg p.x
  Unit
}
`, "dbg line 8: p.x = 1\n", "", 0},
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
			for _, row := range []struct {
				via string
				got observation
			}{{"vm command", vmReference(path)}} {
				g := row.got
				if g.exit != tc.exit || g.stdout != tc.stdout || !strings.Contains(g.stderr, tc.stderr) ||
					(tc.stderr == "" && g.stderr != "") {
					t.Errorf("%s: %s; want exit %d, stdout %q, stderr containing %q", row.via, g, tc.exit, tc.stdout, tc.stderr)
				}
			}

			var entry *ir.Module
			for _, mod := range res.IR {
				for _, f := range mod.Funcs() {
					if f.Name() == "main" {
						entry = mod
					}
				}
			}
			if entry == nil {
				t.Fatal("main was not retained, so the VM cannot run this program")
			}
			var out bytes.Buffer
			booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
			if err != nil {
				t.Fatal(err)
			}
			_, err = booted.Run("main")
			if out.String() != tc.stdout {
				t.Errorf("VM output %q; want %q", out.String(), tc.stdout)
			}
			if tc.exit != 0 && (err == nil || !strings.Contains(err.Error(), tc.stderr)) {
				t.Errorf("VM: %v; want %q", err, tc.stderr)
			}
			if tc.exit == 0 && err != nil {
				t.Errorf("VM: %v", err)
			}
		})
	}
}
