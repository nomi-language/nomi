package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// An application field of function type is read as any app field is and
// called through the value it holds, as a struct's function field is.

func TestIRAppFuncField_EveryFormRuns(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct Cfg {
  f: (Int) -> Int
}

struct App {
  steps: (Int) -> Int
  add: (Int) -> Int
  scale: (Int) -> Int
  cfg: Cfg
  hook: Maybe<(Int) -> Int>
  context: Context
}

fn double(x: Int) -> Int {
  x * 2
}

fn plus(a: Int, b: Int) -> Int {
  a + b
}

fn boot() -> App {
  k = 7
  #App{
    steps: double
    add: &plus(100, &1)
    scale: fn x -> x * k
    cfg: #Cfg{f: fn x -> x + 1}
    hook: Some(fn x -> x - 1)
    context: Context.root()
  }
}

fn tripled(n: Int) -> Int {
  with App.steps = fn x -> x * 3
  App.steps(n) + (n |> App.steps())
}

fn main() {
  io.print("call ${App.steps(10)}")
  f = App.steps
  io.print("value ${f(10)}")
  io.print("argument ${[1, 2, 3] |> Iter.map(App.steps) |> Iter.to_list()}")
  io.print("pipe ${10 |> App.steps()}")
  io.print("partial ${App.add(1)}")
  io.print("closure ${App.scale(2)}")
  io.print("struct field ${App.cfg.f(1)}")
  io.print("with ${tripled(10)}")
  io.print("after with ${App.steps(10)}")
  case App.hook {
    Some(h) -> io.print("maybe ${h(5)}")
    None -> io.print("none")
  }
}
`, "call 20\nvalue 20\nargument [2, 4, 6]\npipe 20\npartial 101\nclosure 14\nstruct field 2\nwith 60\nafter with 20\nmaybe 4\n",
		"boot", "tripled", "main")
}

func TestIRAppFuncField_ATestGroupCallsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steps_test.nomi")
	src := `import std/io

pub struct App {
  context: Context
  steps: (Int) -> Int
}

pub fn boot() -> App {
  #App{context: Context.root(), steps: fn x -> x * 2}
}

fn main() {}

group "app steps" {
  boot boot()

  test "calls the field" {
    io.print("${App.steps(10)}")
    f = App.steps
    assert f(4) == 8
    with App.steps = fn x -> x * 3
    assert App.steps(10) == 30
    assert 10 |> App.steps() == 30
  }
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	prev := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prev
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IR {
		if m.Name() != path {
			continue
		}
		if len(m.Tests()) != 1 {
			t.Fatalf("retained %d case(s); want 1 (declines: %v)", len(m.Tests()), declined)
		}
		var out bytes.Buffer
		exit, reason := vm.NewProgram(m, res.IRModules(), &out).RunTests(path)
		if reason != "" {
			t.Fatalf("the VM could not run the case: %s (declines: %v)", reason, declined)
		}
		want := "ok " + path + " :: app steps / calls the field\n  20\ntest result: ok. 1 passed, 0 failed\n"
		if exit != 0 || out.String() != want {
			t.Fatalf("VM (exit %d): %q; want exit 0 and %q", exit, out.String(), want)
		}
		return
	}
	t.Fatalf("the fixture retained nothing (declines: %v)", declined)
}

// A function value has no parameter names, so a named argument through an
// application field is the checker's error.
func TestIRAppFuncField_ANamedArgumentIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std/io

struct App {
  steps: (Int) -> Int
  context: Context
}

fn double(x: Int) -> Int {
  x * 2
}

fn boot() -> App {
  #App{steps: double, context: Context.root()}
}

fn main() {
  io.print("${App.steps(x: 10)}")
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Analyze(path)
	if err == nil {
		t.Fatal("the front end admits a named argument through a function-typed app field")
	}
	if !strings.Contains(err.Error(), "no parameter named 'x'") {
		t.Fatalf("the rejection is %v; want the no-parameter-named error", err)
	}
}
