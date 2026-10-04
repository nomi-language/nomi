package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A generic function named as a value is instantiated against the function
// type its position expects, as a call instantiates it from its arguments.
func TestGenericFuncRef_InstantiatedAgainstExpectedType(t *testing.T) {
	cases := map[string]string{
		"stdlib io.print into Iter.each": `import std/io
fn main() {
  ["x", "y"] |> Iter.each(io.print)
}`,
		"user generic into Iter.map": `import std/io
fn ident<T>(x: T): T {
  x
}
fn main() {
  [1, 2] |> Iter.map(ident) |> Iter.each(io.print)
}`,
		"bounded generic at a type meeting the bound": `import std/io
fn show<T>(x: T): String where T: Display {
  Display.to_string(x)
}
fn main() {
  [1, 2] |> Iter.map(show) |> Iter.each(io.print)
}`,
		"annotated binding": `import std/io
fn ident<T>(x: T): T {
  x
}
fn main() {
  f: (Int) -> Int = ident
  io.print(f(1))
}`,
		"non-generic parameter": `import std/io
fn ident<T>(x: T): T {
  x
}
fn apply(f: (Int) -> Int, x: Int): Int {
  f(x)
}
fn main() {
  io.print(apply(ident, 7))
}`,
		"generic body passes its own type parameter on": `import std/io
fn ident<T>(x: T): T {
  x
}
fn same<U>(xs: List<U>): List<U> {
  xs |> Iter.map(ident) |> Iter.to_list()
}
fn main() {
  io.print(same([1]))
}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			expectClean(t, checkWithStdlib(src))
		})
	}
}

// With no expected function type, nothing picks the instance: the reference
// is rejected and asks for an annotation.
func TestGenericFuncRef_NothingConstrainsTheTypeParameter(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"unannotated binding": {`fn ident<T>(x: T): T {
  x
}
fn main() {
  f = ident
  f(1)
}`, "cannot infer type parameter T of generic function 'ident' used as a value"},
		"expected type leaves T open": {`fn make<T>(): List<T> {
  []
}
fn run<B>(f: () -> B): B {
  f()
}
fn main() {
  run(make)
}`, "cannot infer type parameter T of generic function 'make' used as a value"},
		"interface function, unannotated binding": {`fn main() {
  f = Display.to_string
  f(1)
}`, "cannot infer type parameter self of generic function 'Display.to_string' used as a value"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			expectErrorContaining(t, checkWithStdlib(c.src), c.want)
		})
	}
}

// The bound is checked at the instantiation, as a call checks it.
func TestGenericFuncRef_BoundCheckedAtInstantiation(t *testing.T) {
	src := `import std/io
struct Opaque {
  n: Int
}
fn show<T>(x: T): String where T: Display {
  Display.to_string(x)
}
fn main() {
  [Opaque{n: 1}] |> Iter.map(show) |> Iter.each(io.print)
}`
	expectErrorContaining(t, checkWithStdlib(src), "Opaque does not implement Display (required by `where T: Display`)")
}

// An expected type of the wrong shape is a mismatch, not an inference failure.
func TestGenericFuncRef_WrongShapeIsAMismatch(t *testing.T) {
	src := `fn ident<T>(x: T): T {
  x
}
fn main() {
  f: (Int) -> String = ident
}`
	errs := checkWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("the front end admits ident as (Int) -> String")
	}
	for _, e := range errs {
		if strings.Contains(e.Message, "cannot infer") {
			t.Fatalf("a shape mismatch reported as an inference failure: %s", e.Message)
		}
	}
}

// An interface function named as a value is instantiated against the expected
// function type: its `self` is solved from the position, and the instantiated
// signature is recorded on the reference for the IR builder to read.
func TestGenericFuncRef_InterfaceFunctionValueRecordsTheInstance(t *testing.T) {
	fa, errs := checkSourceWithStdlib(`
fn f(): List<String> {
    [1, 2] |> Iter.map(Display.to_string) |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
	var ref *analysis.Symbol
	for pos, sym := range fa.References {
		if sym.Name == "to_string" && pos.Line == 3 {
			ref = sym
		}
	}
	if ref == nil {
		t.Fatal("no reference recorded at Display.to_string")
	}
	ft, ok := ref.CallType.(*analysis.FuncType)
	if !ok || ft.String() != "(Int) -> String" {
		t.Fatalf("Display.to_string's recorded instance is %v, want (Int) -> String", ref.CallType)
	}
}
