package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// A `once` initializer reads the application fields boot PUBLISHED, and never
// the forcer's `with` overrides. Spec §27: "The runtime publishes application
// fields only after boot succeeds", and a cached value must not depend on
// which caller forced it first. An initializer frame built with no scoped
// fields at all fails every row here with "$label is not published"
// (or reads the root context instead of boot's).
func TestOnceAppField_RunsOnTheVM(t *testing.T) {
	const header = `
type Trace String

struct App {
  label: String
  context: Context
}

`
	const plainBoot = `fn boot(): App {
  App{label: "booted", context: Context.root()}
}

`
	for _, tc := range []struct {
		name, imports, boot, body, stdout string
	}{
		{"main forces a once reading a field", "", plainBoot, `once seen: String = App.label

fn main() {
  io.print(seen)
}
`, "booted\n"},
		{"a forcer's rebind stays hidden", "", plainBoot, `once seen: String = "saw ${App.label}"

fn main() {
  with App.label = "rebound"
  io.print(App.label)
  io.print(seen)
}
`, "rebound\nsaw booted\n"},
		{"a helper's rebind stays hidden", "", plainBoot, `once seen: String = App.label

fn force(): String {
  with App.label = "helper"
  seen
}

fn main() {
  io.print(force())
  io.print(seen)
}
`, "booted\nbooted\n"},
		{"a task forces it", "\n  std/tasks.Task", plainBoot, `once seen: String = App.label

fn main() {
  got = concurrent {
    t = Task.spawn(|| {
      with App.label = "task"
      seen
    })
    Task.await(t)
  }
  io.print(got)
}
`, "booted\n"},
		{"the initializer reads boot's context", "", `fn boot(): App {
  App{label: "booted", context: Context.with_value(Context.root(), Trace("boot-trace"))}
}

`, `once trace: String = case Context.value(App.context, Trace) {
  Some(Trace(id)) -> id
  None -> "none"
}

fn main() {
  with App.context = Context.with_value(App.context, Trace("main-trace"))
  io.print(trace)
}
`, "boot-trace\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.nomi")
			if err := os.WriteFile(path, []byte("import {\n  std/io"+tc.imports+"\n}\n"+header+tc.boot+tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			got := vmReference(path)
			vmSkipIfKnownBlocked(t, got)
			if got.exit != 0 || got.stdout != tc.stdout || got.stderr != "" {
				t.Fatalf("vm command: %s; want %q", got, tc.stdout)
			}
			p, err := Analyze(path)
			if err != nil {
				t.Fatal(err)
			}
			res, _, err := GenerateIR(p)
			if err != nil {
				t.Fatal(err)
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
				t.Log("main was not retained, so the VM cannot run this program")
				return
			}
			var out bytes.Buffer
			booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := booted.Run("main"); err != nil {
				t.Errorf("VM: %v (output %q)", err, out.String())
			} else if out.String() != tc.stdout {
				t.Errorf("VM output %q; want %q", out.String(), tc.stdout)
			}
		})
	}
}
