package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// irRetainedNames answers the names of every function src retains and the
// retained IR.
func irRetainedNames(t *testing.T, src string) (map[string]bool, *Result) {
	t.Helper()
	p, err := AnalyzeSource("main.nomi", src)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, mod := range res.IR {
		for _, f := range mod.Funcs() {
			names[f.Name()] = true
		}
	}
	return names, res
}

func verifyDeferProgram(t *testing.T, src, want string, retained ...string) {
	t.Helper()
	names, _ := irRetainedNames(t, src)
	for _, name := range retained {
		if !names[name] {
			t.Fatalf("%s was not retained", name)
		}
	}
	verifyLambdaProgram(t, src, want)
}

func TestIRDefer_TourScopedBlock(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct Conn {
  name: String
}

fn close(conn: Conn): Unit {
  io.print("close ${conn.name}")
}

fn main() {
  {
    first = Conn{name: "first"}
    defer close(first)

    second = Conn{name: "second"}
    defer close(second)

    io.print("body")
  }

  io.print("after")
}
`, "body\nclose second\nclose first\nafter\n", "main")
}

func TestIRDefer_FunctionBodiesBlockValuesAndEarlyReturns(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn note(label: String): Unit {
  io.print("cleanup ${label}")
}

fn work(n: Int): Int {
  defer note("a")
  defer note("b")
  io.print("work")
  n + 1
}

fn scoped(): Int {
  x = {
    defer note("inner")
    io.print("in block")
    41
  }
  io.print("after block")
  {
    y = "statement"
    defer note(y)
    { defer note("nested") }
    io.print("statement body")
  }
  x + 1
}

fn guard(n: Int): Int {
  defer note("guard")
  if n > 0 {
    return n
  }
  io.print("fell through")
  0
}

fn main() {
  io.print(work(1))
  io.print(scoped())
  io.print(guard(5))
  io.print(guard(0))
}
`, "work\ncleanup b\ncleanup a\n2\nin block\ncleanup inner\nafter block\ncleanup nested\nstatement body\ncleanup statement\n42\ncleanup guard\n5\nfell through\ncleanup guard\n0\n",
		"work", "scoped", "guard", "main")
}

// A scoped block's names are its own: a shadow inside it and a later outer
// binding of a name it used are separate locals.
func TestIRDefer_ScopedBlockNamesStayLocal(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn main() {
  x = 1
  io.print(x)
  {
    x = 2
    io.print(x)
  }
  {
    y = 3
    io.print(y)
  }
  y = 4
  io.print(x)
  io.print(y)
}
`, "1\n2\n3\n1\n4\n", "main")
}

func TestIRDefer_TryPropagationRunsDeferredCalls(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn note(label: String): Unit {
  io.print("cleanup ${label}")
}

fn check(ok: Bool): Result<Int, String> {
  if ok { Ok(1) } else { Err("no") }
}

fn attempt(ok: Bool): Result<Int, String> {
  defer note("attempt")
  v = try check(ok)
  io.print("checked")
  Ok(v + 1)
}

fn main() {
  dbg attempt(True)
  dbg attempt(False)
  io.print("done")
}
`, "checked\ncleanup attempt\ndbg line 19: attempt(True) = Ok(2)\ncleanup attempt\ndbg line 20: attempt(False) = Err(\"no\")\ndone\n", "attempt")
}

// A fault inside a scope runs every pending deferred call, innermost first,
// before the fault leaves the program.
func TestIRDefer_AFaultRunsEveryPendingDeferredCall(t *testing.T) {
	src := `import std/io

fn note(label: String): Unit {
  io.print("cleanup ${label}")
}

fn divide(a: Int, b: Int): Int {
  defer note("divide")
  {
    defer note("inner")
    io.print(a / b)
  }
  a
}

fn main() {
  io.print(divide(4, 2))
  io.print(divide(1, 0))
}
`
	const stdout = "2\ncleanup inner\ncleanup divide\n4\ncleanup inner\ncleanup divide\n"
	const fault = "line 11: division by zero"
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
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
	if len(res.IR) == 0 {
		t.Fatal("nothing was retained")
	}
	entry := res.IR[0]
	retained := map[string]bool{}
	for _, f := range entry.Funcs() {
		retained[f.Name()] = true
	}
	if !retained["divide"] || !retained["main"] {
		t.Fatalf("retained %v", retained)
	}
	var out bytes.Buffer
	_, err = vm.NewProgram(entry, res.IRModules(), &out).Run("main")
	if out.String() != stdout || err == nil || err.Error() != fault {
		t.Fatalf("VM: %q, %v", out.String(), err)
	}
	if got := vmReference(path); got.exit != 1 || got.stdout != stdout || got.stderr != fault+"\n" {
		t.Fatalf("vm command: %s", got)
	}
}

// A deferred call whose operand is itself a call needs its operand evaluated
// at the `defer`. The builder retains a deferred call only over stable
// operands, so the function is not retained.
func TestIRDefer_AnImpureOperandPreservesFallback(t *testing.T) {
	names, _ := irRetainedNames(t, `import std/io

fn label(): String {
  io.print("label")
  "x"
}

fn note(s: String): Unit {
  io.print(s)
}

fn main() {
  defer note(label())
  io.print("body")
}
`)
	if names["main"] {
		t.Fatal("a defer with a call operand was retained")
	}
}
