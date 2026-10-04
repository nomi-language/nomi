package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// Type-parameter-qualified dispatch: `T.method(x)` where T is a generic
// parameter bounded by an interface. The analyzer resolves T to the bound
// that declares the method and types the call by that method's signature —
// the type-param analogue of an interface-qualified call. See
// checkTypeParamQualifiedCall in analysis/checker.go.

func TestTypeParamQualifiedDispatch_Valid(t *testing.T) {
	src := `import std/io
fn show<T>(x: T): String where T: Display {
  T.to_string(x)
}
fn main() {
  io.print(show(42))
}`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for a valid T.method call, got: %v", errs)
	}
}

// The method name in `T.to_string` must resolve to the bound interface's
// method symbol — the same target `Display.to_string` resolves to — so hover
// and go-to-def on it work. (The `T` qualifier keeps its own type-parameter
// reference, recorded separately.)
func TestTypeParamQualifiedDispatch_MethodReference(t *testing.T) {
	src := `import std/io
fn show<T>(x: T): String where T: Display {
  T.to_string(x)
}
fn main() {
  io.print(show(42))
}`
	fa, _ := checkSourceWithStdlib(src)
	for _, sym := range fa.References {
		if sym != nil && sym.Kind == analysis.SymbolInterfaceMethod && sym.Name == "to_string" {
			// The method symbol carries its owning interface, so hover can show
			// which interface a dispatched method is contracted by.
			if sym.OwningInterface != "Display" {
				t.Errorf("to_string method OwningInterface = %q, want %q", sym.OwningInterface, "Display")
			}
			return
		}
	}
	t.Error("expected `to_string` in `T.to_string` to reference the Display interface method (hover/go-to-def)")
}

func TestTypeParamQualifiedDispatch_SelfReturnStaysConcreteTypeParam(t *testing.T) {
	src := `interface Stepper<S> {
  fn step(value: self, by: S): Maybe<self>
}

fn advance<T>(value: T): Maybe<T> where T: Stepper<Int> {
  T.step(value, 1)
}

fn main() { Unit }`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("expected self-returning T.method call to keep Maybe<T>, got: %v", errs)
	}
}

func TestTypeParamQualifiedDispatch_Errors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "unbounded type parameter",
			src: `fn f<T>(x: T): String { T.to_string(x) }
fn main() { Unit }`,
			want: "has no interface bound",
		},
		{
			name: "function not on bound",
			src: `fn f<T>(x: T): String where T: Display { T.nope(x) }
fn main() { Unit }`,
			want: "declares function 'nope'",
		},
		{
			name: "ambiguous across two bounds",
			src: `interface A { fn tag(value: self): String }
interface B { fn tag(value: self): String }
fn f<T>(x: T): String where T: A and B { T.tag(x) }
fn main() { Unit }`,
			want: "is ambiguous",
		},
		{
			// The bound interface's method signature now types the call, so a
			// declared return that disagrees with it is caught at compile time
			// instead of trapping at runtime.
			name: "return type checked through the bound",
			src: `fn show<T>(x: T): Int where T: Display { T.to_string(x) }
fn main() { Unit }`,
			want: "return type mismatch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			for _, e := range errs {
				if strings.Contains(e.Message, tc.want) {
					return
				}
			}
			t.Errorf("expected an error containing %q, got: %v", tc.want, errs)
		})
	}
}

// A type-parameter-qualified member taken BARE — not the direct callee of a
// call — reached neither validator at 1517b166. checkTypeParamQualifiedCall
// runs from checkCall only, and checkFieldAccess's terminal deliberately
// skipped a type-param qualifier because its comment credited that function
// with the case. So `T.<anything>` in value position typed as a permissive
// nil for ANY member name, `nomi check` said ok, and the program died on
// `unknown builtin 'T.<member>'`. Measured at 1517b166 with the real binary:
//
//	fn start<T>(): T where T: Machine { T.Nonexistent }
//	  nomi check -> ok
//	  nomi run   -> line 21: unknown builtin 'T.Nonexistent'
//
// where `Nonexistent` is a member no type in the program declares under any
// interface. The count assertions are the point as much as the text: the same
// rule now covers a callee and a bare value, so a relocation that left the
// call path emitting too would double-report.
func TestTypeParamQualifiedMember_ValuePosition(t *testing.T) {
	const decls = `interface Machine {
  fn label(value: self): String
}

interface Other {
  fn tag(value: self): String
}

struct Robot {
  name: String
}

impl Machine for Robot {
  fn label(value: Robot): String { value.name }
}

impl Other for Robot {
  fn tag(value: Robot): String { value.name }
}
`
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "bare member no bound declares",
			src: decls + `fn start<T>(): String where T: Machine { T.Nonexistent }
fn main() { Unit }`,
			want: "no interface bound of type parameter 'T' (Machine) declares function 'Nonexistent'",
		},
		{
			name: "bare member on an unbounded parameter",
			src: `fn start<T>(): String { T.Nonexistent }
fn main() { Unit }`,
			want: "type parameter 'T' has no interface bound, so 'T.Nonexistent' has nothing to dispatch through; add a `where T: SomeInterface` bound whose interface declares 'Nonexistent'",
		},
		{
			name: "bare member with two bounds, neither declaring it",
			src: decls + `fn start<T>(): String where T: Machine and Other { T.Nonexistent }
fn main() { Unit }`,
			want: "no interface bound of type parameter 'T' (Machine, Other) declares function 'Nonexistent'",
		},
		{
			name: "bare member ambiguous across two bounds",
			src: `interface A { fn tag(value: self): String }
interface B { fn tag(value: self): String }
fn f<T>(x: T): String where T: A and B { g = T.tag
  g(x) }
fn main() { Unit }`,
			want: "'T.tag' is ambiguous",
		},
		{
			name: "callee position still reports exactly once",
			src: decls + `fn start<T>(x: T): String where T: Machine { T.Nonexistent(x) }
fn main() { Unit }`,
			want: "no interface bound of type parameter 'T' (Machine) declares function 'Nonexistent'",
		},
		{
			// The second cause, independent of position: `T` appears in
			// neither a parameter nor the return, so collectTypeParamsByName
			// did not carry it and BOTH validators answered "not a type
			// parameter". Measured at 1517b166 — `nomi check` ok, then
			// `line 5: unknown builtin 'T.nope'` — while the same function
			// with `T` in a parameter was rejected at compile time.
			name: "where-clause-only parameter, callee position",
			src: decls + `fn start<T>(): String where T: Machine { T.nope() }
fn main() { Unit }`,
			want: "no interface bound of type parameter 'T' (Machine) declares function 'nope'",
		},
		{
			name: "where-clause-only parameter, value position",
			src: decls + `fn start<T>(): String where T: Machine { T.nope }
fn main() { Unit }`,
			want: "no interface bound of type parameter 'T' (Machine) declares function 'nope'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			hits := 0
			for _, e := range errs {
				if strings.Contains(e.Message, tc.want) {
					hits++
				}
			}
			if hits != 1 {
				t.Fatalf("expected exactly 1 error containing %q, got %d: %v", tc.want, hits, errs)
			}
			if len(errs) != 1 {
				t.Fatalf("expected exactly 1 diagnostic, got %d: %v", len(errs), errs)
			}
		})
	}
}

// The planted positive for the rejection above. A rule that refused every
// bare `T.<member>` would break type-parameter-qualified dispatch, which the
// language supports and `std/json`, `std/ranges` and the tour all use. A
// member a bound DOES declare types as that bound's method signature in both
// positions, so a call is unaffected and a bare reference becomes a usable
// function value instead of a runtime death.
func TestTypeParamQualifiedMember_ProvidedMemberStillTypes(t *testing.T) {
	src := `import std/io

interface Greet {
  fn hello(value: self): String
}

struct Dog {
  name: String
}

impl Display for Dog {
  fn to_string(value: Dog): String { "Dog(" + value.name + ")" }
}

impl Greet for Dog {
  fn hello(value: Dog): String { "woof, I am " + value.name }
}

fn called<T>(x: T): String where T: Display and Greet {
  T.hello(x) + " - " + T.to_string(x)
}

fn piped<T>(x: T): String where T: Greet {
  x |> T.hello
}

fn bound<T>(x: T): String where T: Greet {
  f = T.hello
  f(x)
}

fn main() {
  io.print(called(Dog{name: "Rex"}))
  io.print(piped(Dog{name: "Rex"}))
  io.print(bound(Dog{name: "Rex"}))
}`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for provided members in call, pipe and binding position, got: %v", errs)
	}
}
