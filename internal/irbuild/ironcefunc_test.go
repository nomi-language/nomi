package irbuild

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// A `once` may hold a function value. Reading it (`g = add_op`), calling it
// directly (`add_op(n)`), calling it from a lambda and passing it on all
// lower: the cell holds the function value in its ref register, and a call
// through it is an indirect call on the read.
func TestIROnce_AFunctionValuedCellIsReadAndCalled(t *testing.T) {
	const src = `import std/io

fn add(a: Int): Int {
  a + 1
}

once add_op: (Int) -> Int = add

once triple = |n: Int| n * 3

fn use_it(n: Int): Int {
  add_op(n) * 2
}

fn main() {
  g = add_op
  io.inspect(g(1))
  io.inspect(use_it(3))
  io.inspect(add_op(10))
  io.inspect(triple(4))
  io.inspect([1, 2] |> Iter.map(|v| triple(v) + add_op(v)) |> Iter.to_list())
  io.inspect([5] |> Iter.map(add_op) |> Iter.to_list())
}
`
	path := writeTemp(t, src)
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end now rejects this, so the lowering below is untested: %v", err)
	}
	declined := map[string]string{}
	prevD := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prevD
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil {
		t.Fatal("no module for the fixture")
	}
	retained := map[string]bool{}
	for _, f := range module.Funcs() {
		retained[f.Name()] = true
	}
	for _, name := range []string{"main", "use_it"} {
		if !retained[name] {
			t.Errorf("%s not retained (declines: %v)", name, declined)
		}
	}
	var out bytes.Buffer
	if _, err := vm.NewProgram(module, res.IRModules(), &out).Run("main"); err != nil {
		t.Fatalf("VM: %v\nstdout:\n%s", err, out.String())
	}
	const want = "2\n8\n11\n12\n[5, 9]\n[6]\n"
	if out.String() != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out.String(), want)
	}
}
