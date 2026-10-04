package irbuild

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

func TestIROnce_FixtureSemantics(t *testing.T) {
	for _, name := range []string{"once_bindings.nomi", "once_cycle.nomi"} {
		t.Run(name, func(t *testing.T) {
			path := fixture(name)

			p, err := Analyze(path)
			if err != nil {
				t.Fatal(err)
			}
			r, _, err := GenerateIR(p)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			_, err = vmRunV(vm.NewProgram(r.IR[0], r.IRModules(), &out), "main")
			want := goldenReference(t, path)
			if out.String() != want.stdout {
				t.Fatalf("VM stdout %q, want %q", out.String(), want.stdout)
			}
			if name == "once_cycle.nomi" {
				if err == nil || err.Error() != "cyclic 'once' binding: 'seed' depends on itself" {
					t.Fatalf("VM cycle: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIROnce_LambdaReadIsLazyAndLocalsShadow(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn initialize(n: Int): Int { io.print("initialize ${n}") n }
once answer = initialize(21)
once unused = initialize(99)
fn main() {
  read = || answer
  io.print("created")
  io.print(read())
  io.print(read())
  answer = 7
  local_read = || answer
  io.print(local_read())
}
`, "created\ninitialize 21\n21\n21\n7\n")
}

func TestIROnce_CompleteProgram(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
once max_retries = 3
once timeout_ms = 30 * 1000
fn main() {
  io.print(max_retries)
  io.print(timeout_ms)
  io.print(timeout_ms)
}
`, "3\n30000\n30000\n")
}

func TestIROnce_ConditionalInitializerAndDefault(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int): Int { io.print(n) n }
once answer: Int = if True { mark(41) + 1 } else { mark(99) }
fn choose(n: Int = answer): Int { n }
fn main() {
  io.print(choose(7))
  io.print(choose())
  io.print(choose())
}
`, "7\n41\n42\n42\n")
}

// A retained body may not read a once whose initializer no module retained:
// the VM would stop at the read with "has no retained initializer". `early`
// reads `late` before `late`'s cell is attempted, `chained`'s initializer and
// `main` read it after, and each is withdrawn. `fine` reads a retained cell and
// stays.
func TestIROnce_ReadOfAnUnretainedCellDeclines(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `import std/io
fn early(): Int { late + 1 }
once late: Int = case Context.with_value(Context.root(), 1) { _ -> 2 }
once chained: Int = late
fn fine(): Int { ok + 1 }
fn main() {
  io.print(early())
  io.print(chained)
  io.print(fine())
}
once ok: Int = 3
`)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.IR) != 1 {
		t.Fatalf("%d user modules retained; want 1", len(res.IR))
	}
	mod := res.IR[0]
	cells := map[string]bool{}
	for _, c := range mod.Cells() {
		cells[c.Sym().Name()] = c.Initializer() != nil
	}
	funcs := map[string]bool{}
	for _, f := range mod.Funcs() {
		funcs[f.Name()] = true
	}
	if cells["late"] {
		t.Fatal("late's initializer now retains, so nothing here reads an unretained cell; " +
			"give it an initializer the builder declines")
	}
	if cells["chained"] || funcs["early"] || funcs["main"] {
		t.Fatalf("a retained body reads the unretained late: cells %v, funcs %v", cells, funcs)
	}
	if !cells["ok"] || !funcs["fine"] {
		t.Fatalf("a read of a retained cell was withdrawn: cells %v, funcs %v", cells, funcs)
	}
	var out bytes.Buffer
	if v, err := vmRunV(vm.NewProgram(mod, res.IRModules(), &out), "fine"); err != nil || v != any(int64(4)) {
		t.Fatalf("VM fine: %v, %v", v, err)
	}
}
