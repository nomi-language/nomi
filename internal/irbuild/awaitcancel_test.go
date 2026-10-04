package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"

	"github.com/nomi-language/nomi/rt"
)

// Awaiting a task the program cancelled propagates the cancellation
// (std/tasks.nomi). On the main line there is no owner to unwind to, so the
// program fails with rt.AwaitedCancelledTaskText. Inside a task the same await
// unwinds the awaiter, which settles Cancelled. Checked through the VM command
// and through the machine directly.
func TestAwaitCancelledTask_PropagatesTheCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, src, stdout string
		fails             bool
	}{
		{"await on the main line", `import {
  std/duration.Duration
  std/io
  std/tasks.Task
  std/timer
}

fn main() {
  concurrent {
    t = Task.spawn(|| {
      timer.sleep(Duration.seconds(5))
      1
    })
    Task.cancel(t)
    v = Task.await(t)
    io.print("awaited ${v}")
  }
  io.print("after")
}
`, "", true},
		{"await_all under a live deadline", `import {
  std/duration.Duration
  std/io
  std/tasks.Task
  std/timer
}

fn main() {
  with Run.context = Context.with_timeout(Run.context, Duration.seconds(30))
  xs = concurrent {
    a = Task.spawn(|| {
      timer.sleep(Duration.seconds(5))
      1
    })
    Task.cancel(a)
    Task.await_all([a])
  }
  io.print("after ${xs}")
}
struct Run {
  context: Context
}

fn boot(): Run {
  Run{context: Context.root()}
}
`, "", true},
		{"await inside a task", `import {
  std/duration.Duration
  std/tasks.Task
  std/timer
}

fn inner(): Int {
  concurrent {
    t = Task.spawn(|| {
      timer.sleep(Duration.seconds(5))
      1
    })
    Task.cancel(t)
    Task.await(t)
  }
}

fn main() {
  outcome = concurrent {
    o = Task.spawn(|| inner())
    Task.outcome(o)
  }
  dbg outcome
  Unit
}
`, "dbg line 23: outcome = Cancelled\n", false},
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
			want := rt.AwaitedCancelledTaskText()
			for _, row := range []struct {
				via string
				got observation
			}{{"vm command", vmReference(path)}} {
				vmSkipIfKnownBlocked(t, row.got)
				if row.got.stdout != tc.stdout {
					t.Errorf("%s: %s; want stdout %q", row.via, row.got, tc.stdout)
				}
				if tc.fails && (row.got.exit != 1 || !strings.Contains(row.got.stderr, want)) {
					t.Errorf("%s: %s; want exit 1 and %q", row.via, row.got, want)
				}
				if !tc.fails && (row.got.exit != 0 || row.got.stderr != "") {
					t.Errorf("%s: %s; want a clean exit", row.via, row.got)
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
				// The vm command row above still ran.
				t.Log("main was not retained, so the VM cannot run this program")
				return
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
			if tc.fails && (err == nil || !strings.Contains(err.Error(), want)) {
				t.Errorf("VM: %v; want %q", err, want)
			}
			if !tc.fails && err != nil {
				t.Errorf("VM: %v", err)
			}
		})
	}
}
