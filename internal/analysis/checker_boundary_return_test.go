package analysis_test

import "testing"

func TestBoundaryReturn_InferredValues(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"lambda value", `fn main() { f = fn -> { return 7 } result = f() _ = result }`, "Int"},
		{"lambda unit", `fn main() { f = fn -> { dbg 1 return } result = f() _ = result }`, "Unit"},
		{"both branches", `fn main() {
  f = fn b: Bool -> { if b { return 1 } else { return 2 } }
  result = f(True)
  _ = result
}`, "Int"},
		{"nested lambda", `fn main() {
  f = fn -> {
    inner = fn -> { return "inner" }
    _ = inner()
    return 9
  }
  result = f()
  _ = result
}`, "Int"},
		{"nested named function", `fn main() {
  f = fn -> {
    fn inner() -> String { return "inner" }
    _ = inner()
    return 9
  }
  result = f()
  _ = result
}`, "Int"},
		{"concurrent value", `fn main() { result = concurrent { return 7 } _ = result }`, "Int"},
		{"nested concurrent", `fn main() {
  f = fn -> { _ = concurrent { return "inner" } return 9 }
  result = f()
  _ = result
}`, "Int"},
		{"generic returned value", `fn identity<T>(value: T) -> T {
  f = fn -> { return value }
  f()
}
fn main() { result = identity(7) _ = result }`, "Int"},
		{"try before explicit result", `fn fallible() -> Result<Int, String> { Ok(3) }
fn main() {
  f = fn -> { value = fallible()? return Ok(value) }
  result = f()
  _ = result
}`, "Result<Int, String>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fa, errs := checkSourceWithStdlib(tc.src)
			expectClean(t, errs)
			for _, sym := range fa.Definitions {
				if sym.Name == "result" {
					if sym.Type == nil || sym.Type.String() != tc.want {
						t.Fatalf("result type = %v, want %s", sym.Type, tc.want)
					}
					return
				}
			}
			t.Fatal("result binding was not analyzed")
		})
	}
}

func TestBoundaryReturn_TryCannotEscapeScalarReturn(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn fallible() -> Result<Int, String> { Ok(3) }
fn main() { f = fn -> { value = fallible()? return value } _ = f() }`)
	expectErrorContaining(t, errs, "`?` cannot propagate a Result out of a lambda whose result is Int")
}

func TestBoundaryReturn_ContextualInterface(t *testing.T) {
	for _, tail := range []string{"return #Right{}", "#Right{}"} {
		_, errs := checkSourceWithStdlib(`interface Value {}
struct Left {}
struct Right {}
impl Value for Left {}
impl Value for Right {}
fn use(f: (Bool) -> Value) -> Value { f(True) }
fn main() { _ = use(fn b -> { if b { return #Left{} } ` + tail + ` }) }`)
		expectClean(t, errs)
	}
}

func TestBoundaryReturn_RejectsDisagreement(t *testing.T) {
	for _, src := range []string{
		`fn main() { f = fn b: Bool -> { if b { return "bad" } 7 } _ = f(False) }`,
		`fn main() { f = fn b: Bool -> { if b { return 7 } return "bad" } _ = f(False) }`,
		`fn main() { f = fn b: Bool -> { if b { return } return 7 } _ = f(False) }`,
		`fn main() { _ = concurrent { if True { return "bad" } 7 } }`,
		`fn use(f: (Bool) -> Int) -> Int { f(True) }
fn main() { _ = use(fn b -> { if b { return 7 } "bad" }) }`,
	} {
		_, errs := checkSourceWithStdlib(src)
		expectErrorContaining(t, errs, "return type mismatch")
	}
}
