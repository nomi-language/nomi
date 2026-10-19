package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

fn close(conn: Conn) -> Unit {
  io.print("close ${conn.name}")
}

fn main() {
  {
    first = #Conn{name: "first"}
    defer close(first)

    second = #Conn{name: "second"}
    defer close(second)

    io.print("body")
  }

  io.print("after")
}
`, "body\nclose second\nclose first\nafter\n", "main")
}

func TestIRDefer_FunctionBodiesBlockValuesAndEarlyReturns(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn note(label: String) -> Unit {
  io.print("cleanup ${label}")
}

fn work(n: Int) -> Int {
  defer note("a")
  defer note("b")
  io.print("work")
  n + 1
}

fn scoped() -> Int {
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

fn guard(n: Int) -> Int {
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

fn note(label: String) -> Unit {
  io.print("cleanup ${label}")
}

fn check(ok: Bool) -> Result<Int, String> {
  if ok { Ok(1) } else { Err("no") }
}

fn attempt(ok: Bool) -> Result<Int, String> {
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

fn note(label: String) -> Unit {
  io.print("cleanup ${label}")
}

fn divide(a: Int, b: Int) -> Int {
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

// A `defer` in a lambda runs when that invocation ends, on each exit: the
// normal end, `return`, `try` propagating, and in an iteration callback
// `break` and `continue`. Several run most recent first. The builder
// declined a defer in any lambda.
func TestIRDefer_ALambdaRunsItsDeferredCallsWhenEachInvocationEnds(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn note(label: String) -> Unit {
  io.print("cleanup ${label}")
}

fn check(n: Int) -> Result<Int, String> {
  if n > 0 { Ok(n) } else { Err("no") }
}

fn main() {
  greet = fn name: String -> {
    defer note("first")
    defer note(name)
    io.print("hello ${name}")
  }
  greet("a")
  greet("b")
  early = fn n: Int -> {
    defer note("early")
    if n > 1 { return n * 10 }
    n
  }
  io.print(early(1) + early(2))
  attempt = fn n: Int -> {
    defer note("attempt")
    v = try check(n)
    io.print("checked")
    Ok(v + 1)
  }
  io.inspect(attempt(1))
  io.inspect(attempt(0))
  kept = [1, 2, 3, 4]
    |> Iter.filter(fn x -> {
      defer note("filter")
      if x == 2 { continue }
      if x == 4 { break }
      True
    })
    |> Iter.to_list()
  io.inspect(kept)
}
`, "hello a\ncleanup a\ncleanup first\nhello b\ncleanup b\ncleanup first\ncleanup early\ncleanup early\n21\nchecked\ncleanup attempt\nOk(2)\ncleanup attempt\nErr(\"no\")\ncleanup filter\ncleanup filter\ncleanup filter\ncleanup filter\n[1, 3]\n", "main")
}

// An `Iter.loop` callback lowers inline in its enclosing function, so the
// iteration runs its deferred calls at each of its exits: the next state, a
// `return` from the callback, a `break`, and a block statement's own scope
// inside it, innermost first. The builder declined a defer in a loop callback.
func TestIRDefer_AnIterLoopIterationRunsItsDeferredCalls(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn note(label: String) -> Unit {
  io.print("cleanup ${label}")
}

fn leave(n: Int) -> Unit {
  io.print("leave ${n}")
}

fn main() {
  defer note("main")
  total = Iter.loop(fn n = 0 -> {
    defer leave(n)
    defer note("second")
    if n == 1 {
      return 2
    }
    if n == 3 { break n * 10 }
    io.print("step ${n}")
    n + 1
  })
  io.print("total ${total}")
  _ = Iter.loop(fn n = 0 -> {
    defer leave(n)
    {
      defer note("block")
      if n == 1 { break n }
      io.print("in block ${n}")
    }
    n + 1
  })
  _ = Iter.loop(fn -> {
    defer note("stateless")
    break
  })
  io.print("end")
}
`, "step 0\ncleanup second\nleave 0\ncleanup second\nleave 1\nstep 2\ncleanup second\nleave 2\ncleanup second\nleave 3\ntotal 30\nin block 0\ncleanup block\nleave 0\ncleanup block\nleave 1\ncleanup stateless\nend\ncleanup main\n", "main")
}

// A block that leaves an `Iter.loop` iteration (a guard's arm, a `case`
// arm, a binding's `else`) runs its own deferred calls, then the
// iteration's; a value arm runs its own where it ends, and the paths that
// did not pass a `defer` run nothing for it.
func TestIRDefer_AnArmLeavingAnIterationRunsItsDeferredCalls(t *testing.T) {
	verifyDeferProgram(t, `import std/io

fn note(label: String) -> Unit {
  io.print("cleanup ${label}")
}

fn main() {
  a = Iter.loop(fn n = 0 -> {
    defer note("iteration")
    if n == 3 {
      defer note("guard")
      break n
    }
    case n {
      2 -> {
        defer note("case arm")
        return n + 1
      }
      _ -> {
        defer note("value arm")
        n + 1
      }
    }
  })
  io.print(a)
  m: Maybe<Int> = None
  b = Iter.loop(fn n = 0 -> {
    defer note("iteration b")
    Some(x) = m else {
      defer note("else")
      break n
    }
    x
  })
  io.print(b)
}
`, "cleanup value arm\ncleanup iteration\ncleanup value arm\ncleanup iteration\ncleanup case arm\ncleanup iteration\ncleanup guard\ncleanup iteration\n3\ncleanup else\ncleanup iteration b\n0\n", "main")
}

// A trap inside a lambda or a loop iteration runs that invocation's pending
// deferred calls, then the enclosing function's, before it leaves.
func TestIRDefer_ATrapInALambdaOrIterationRunsItsDeferredCalls(t *testing.T) {
	src := `import std/io

fn note(label: String) -> Unit {
  io.print("cleanup ${label}")
}

fn main() {
  defer note("main")
  divide = fn d: Int -> {
    defer note("divide")
    10 / d
  }
  io.print(divide(2))
  _ = Iter.loop(fn n = 1 -> {
    defer note("iteration")
    io.print(divide(n))
    n - 1
  })
}
`
	const stdout = "cleanup divide\n5\ncleanup divide\n10\ncleanup iteration\ncleanup divide\ncleanup iteration\ncleanup main\n"
	const fault = "line 11: division by zero"
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if got := vmReference(path); got.exit != 1 || got.stdout != stdout || got.stderr != fault+"\n" {
		t.Fatalf("vm command: %s", got)
	}
}

// A block lowered as a value in a test body (an arm of an `if` or `case`
// used as a value, an arm nested in one, a subtests row's arm, an arm of an
// `Iter.loop` callback) runs its deferred calls when that block ends, and
// on a failed assertion inside it, as it does in a function body.
func TestIRDefer_ATestBodyValueArmRunsItsDeferredCalls(t *testing.T) {
	const src = `import std/io

test "an if arm" {
  x = if True {
    defer io.print("if arm ends")
    io.print("in if arm")
    1
  } else {
    2
  }
  io.print("after if")
  assert x == 1
}

test "nested case arms" {
  x = case 1 {
    1 -> {
      defer io.print("outer arm ends")
      y = case 2 {
        2 -> {
          defer io.print("inner arm ends")
          10
        }
        _ -> 0
      }
      y + 1
    }
    _ -> 0
  }
  assert x == 11
}

test "a loop callback's arm" {
  n = Iter.loop(fn n = 0 -> {
    case n {
      2 -> {
        defer io.print("break arm ends")
        break n
      }
      _ -> {
        defer io.print("next arm ends")
        n + 1
      }
    }
  })
  assert n == 2
}

test "subtests rows" {
  subtests [1, 2], n {
    x = if n == 1 {
      defer io.print("row one ends")
      1
    } else {
      defer io.print("row two ends")
      n
    }
    assert x == n
  }
}

test "a failed assertion in the arm" {
  x = if True {
    defer io.print("released on failure")
    assert False
    1
  } else {
    2
  }
  assert x == 1
}
`
	out := irTestBodyVM(t, src, 5)
	for _, want := range []string{
		"  in if arm\n  if arm ends\n  after if\n",
		"  inner arm ends\n  outer arm ends\n",
		"  next arm ends\n  next arm ends\n  break arm ends\n",
		"  row one ends\n",
		"  row two ends\n",
		"  released on failure\n",
		"assertion failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}
