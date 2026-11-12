package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Passing a value whose type still has unsolved type params (a fresh
// `Channel.buffered(...)` → `Channel<?>`) directly to a concrete parameter
// (`Channel<Int>`) should unify (solve `? = Int`), not reject. Inline
// arguments aren't bindings, so the locally-determined rule doesn't apply
// here — this is the unification capability.
func TestUnificationGap_UnsolvedParamIntoConcreteParam(t *testing.T) {
	src := `import
  std::channels.{Channel, Sender}
  std::io
end
fn produce(ch: Channel<Int>, n: Int) -> Unit
  r = Sender.send(ch.sender, n)
  io.print("sent")
end
fn main() -> Unit
  produce(Channel.buffered(4), 10)
end`
	expectClean(t, checkWithStdlib(src))
}

// --- Shared type-var enforcement (the bug this work fixes) ---
//
// A type param appearing in more than one parameter is bound by the first
// argument; a later argument that conflicts with that binding must be rejected.

func TestUnificationGap_SharedTypeVarConflict_Direct(t *testing.T) {
	src := `fn g<T>(a: T, _b: T) -> T
    a
end
fn main() -> Unit
  x = g(1, "two")
end`
	expectErrorContaining(t, checkWithStdlib(src), "expected Int, got String")
}

func TestUnificationGap_SharedTypeVarConflict_Pipe(t *testing.T) {
	src := `fn g<T>(a: T, _b: T) -> T
    a
end
fn main() -> Unit
  x = 1 |> g("two")
end`
	expectErrorContaining(t, checkWithStdlib(src), "expected Int, got String")
}

func TestUnificationGap_MapGetWrongKey_Direct(t *testing.T) {
	src := `import std::maps: Map
fn main() -> Unit
  m = {1 => "one"}
  v = Map.get(m, "wrong")
end`
	expectErrorContaining(t, checkWithStdlib(src), "expected Int, got String")
}

func TestUnificationGap_MapGetWrongKey_Pipe(t *testing.T) {
	src := `import std::maps: Map
fn main() -> Unit
  m = {1 => "one"}
  v = m |> Map.get("wrong")
end`
	expectErrorContaining(t, checkWithStdlib(src), "expected Int, got String")
}

// Same-type arguments to a shared type var must still type-check cleanly —
// enforcement must not over-report.
func TestUnificationGap_SharedTypeVarSameType_Clean(t *testing.T) {
	src := `fn g<T>(a: T, _b: T) -> T
    a
end
fn main() -> Unit
  x = g(1, 2)
end`
	expectClean(t, checkWithStdlib(src))
}

// --- Struct.update gains real patch validation under enforcement ---

func TestStructsUpdate_ValidPartialPatch_Clean(t *testing.T) {
	src := `struct User
    name: String
    age: Int
end
fn main() -> Unit
  u = User{name: "Alice", age: 30}
  updated = Struct.update(u, {name: "Bob"})
end`
	expectClean(t, checkWithStdlib(src))
}

func TestStructsUpdate_BogusPatchField_Rejected(t *testing.T) {
	src := `struct User
    name: String
    age: Int
end
fn main() -> Unit
  u = User{name: "Alice", age: 30}
  updated = Struct.update(u, {bogus: 5})
end`
	expectErrorContaining(t, checkWithStdlib(src), "User has no field 'bogus'")
}

// --- A user enum named Control is an ordinary enum (no special treatment) ---
//
// `Control` was once a stdlib enum the checker recognized by base name; it is
// now internal to the runtime with no surface spelling, so a user-declared
// enum that happens to be named Control gets zero special treatment. A generic
// `apply<T>(f: (T) -> T, ...)` is NOT an iter callback: the lambda body solves
// T = Control<Int> from the callback's return position, then the `5` argument
// clashes (`expected Control<Int>, got Int`) — plain unification, exactly as
// it would for any other enum name. The now-removed Control-coercion machinery
// used to rewrite the callback's return type here; this pins that it doesn't.
func TestUserControlEnum_OrdinaryUnification(t *testing.T) {
	src := `enum Control<R>
  Break
  BreakWith R
  Continue
  Return
  ReturnWith R
end

fn apply<T>(f: (T) -> T, x: T) -> T
    f(x)
end
fn main() -> Unit
  r = apply(fn _n -> Control.BreakWith(42), 5)
end`
	expectErrorContaining(t, checkWithStdlib(src), "expected Control<Int>, got Int")
}

// --- loop inference under the (S) -> S signature ---

// A stateful loop using the keyword sugar must type-check cleanly, with S
// resolved from the state parameter's default (`fn sum: Int = 0 ->` pins S = Int;
// `break sum` exits with it). Pins S-from-default inference under loop's
// `(S) -> S` signature.
func TestLoopStatefulKeywordSugar_Clean(t *testing.T) {
	src := `fn main() -> Unit
  total = Iter.loop(0, fn sum: Int ->
    if sum > 10 then break sum else sum + 1
  end
  )
end`
	expectClean(t, checkWithStdlib(src))
}

// --- Control is not importable (no surface spelling) ---

// Control is internal to the runtime — no stdlib module declares it, so
// importing it by name is an error like any other unknown name. Pins that the
// type has no surface spelling left. The builder reports the missing name and
// binds a placeholder so downstream references don't cascade; the checker
// does not also call the placeholder private.
func TestControlNotImportable(t *testing.T) {
	src := withStdlibTestImports(`import std::iter.{Control}
fn main() -> Unit
  io_check = 1
end`)
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	errs := append([]analysis.TypeError{}, fa.TypeErrors...)
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	expectErrorContaining(t, errs, "has no exported name 'Control'")
	for _, e := range errs {
		if strings.Contains(e.Message, "is private") {
			t.Fatalf("a name the file does not declare is reported as private: %s", e.Message)
		}
	}
}

// --- Unit-discarding callbacks (Iter.each / lists.each's `(T) -> ()`) ---

// A side-effect callback declared `(T) -> ()` discards its return, so a lambda
// returning a value is accepted (the parameter types must still match).
func TestUnitDiscardCallback_ValueReturningLambda_Clean(t *testing.T) {
	src := `fn main() -> Unit
  Iter.each([1, 2, 3], fn x -> x + 1)
end`
	expectClean(t, checkWithStdlib(src))
}

// The Unit-discard relation ignores only the return — a wrong parameter type is
// still rejected.
func TestUnitDiscardCallback_WrongParamType_Rejected(t *testing.T) {
	src := `fn main() -> Unit
  Iter.each([1, 2, 3], fn s: String -> s)
end`
	errs := checkWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("expected rejection: a (String) -> _ lambda for lists.each over List<Int>, got none")
	}
}

// --- Partial<T> is coherent in user-written (non-generic) functions ---

func TestPartial_UserFunctionAcceptsDeepPartial(t *testing.T) {
	src := `struct User
    name: String
    age: Int
end
fn patch(u: User, _p: Partial<User>) -> User
    u
end
fn main() -> Unit
  base = User{name: "A", age: 1}
  result = patch(base, {name: "B"})
end`
	expectClean(t, checkWithStdlib(src))
}

func TestPartial_UserFunctionRejectsBogusPatch(t *testing.T) {
	src := `struct User
    name: String
    age: Int
end
fn patch(u: User, _p: Partial<User>) -> User
    u
end
fn main() -> Unit
  base = User{name: "A", age: 1}
  result = patch(base, {bogus: 5})
end`
	errs := checkWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("expected rejection of a bogus patch field for Partial<User>, got none")
	}
}
