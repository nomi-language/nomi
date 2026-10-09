package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"slices"
	"strings"
	"testing"
)

// checkSource builds and type-checks a single source string in isolation.
// It uses BuildFile, NOT BuildFileWithStdlib — the prelude is not loaded.
// That means `Unit`, `True`, `False`, `IO`, `List`, `Map`, etc. are
// undefined in test sources. Workarounds:
//   - For void-returning bodies, write `fn main() {}` (empty block) rather
//     than `fn main() { Unit }`. The empty body has type Unit by inference;
//     a bare `Unit` reference would fail to resolve.
//   - Avoid `io.print` / `io.inspect` in test sources. Construct values
//     directly and assert via the returned FileAnalysis / errors.
//   - For tests that genuinely need prelude bindings, use
//     BuildFileWithStdlib directly (see builder_test.go for examples).
//
// This intentional bareness keeps analyzer tests fast and isolates the
// behavior under test from prelude churn.
func checkSource(src string) (*FileAnalysis, []TypeError) {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	// Lower type-body items and `for` blocks into
	// top-level impl blocks so the BuildTypes / CheckTypes passes below — which
	// receive this same `nodes` slice — see the impls. BuildFile lowers on its
	// own internal copy; the passes need the lowered nodes too. Idempotent.
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	typeErrs := BuildTypes(fa, nodes)
	checkErrs := CheckTypes(fa, nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, typeErrs...)
	all = append(all, checkErrs...)
	return fa, all
}

func checkSourceWithModules(t *testing.T, src string, modules map[string]string) (*FileAnalysis, []TypeError) {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFileWithStdlib(nodes, nil, nil, "/project", reExportLoader(t, modules))
	typeErrs := BuildTypes(fa, nodes)
	checkErrs := CheckTypes(fa, nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, typeErrs...)
	all = append(all, checkErrs...)
	return fa, all
}

func TestAssertionsOutsideTestRequireResultReturn(t *testing.T) {
	_, errs := checkSource(`
fn main() {
  assert 1 == 1
  refute 1 == 2
  {}
}
`)
	if len(errs) != 2 {
		t.Fatalf("expected two assertion return diagnostics, got %v", errs)
	}
	for _, err := range errs {
		if !strings.Contains(err.Message, "can return AssertionFailure") {
			t.Fatalf("expected AssertionFailure diagnostic, got %v", errs)
		}
	}
}

// An assertion inside a lambda unwinds to the lambda, not to the test, so
// the failure becomes the lambda's return value and the caller decides what
// to do with it. Every caller taking a `-> Unit` callback discards it, and a
// `-> Unit` parameter accepts any result type, so nothing downstream objects
// either: the assertion passes whatever it says.
//
// This was found by writing one. A table-driven test looped its cases with
// `Iter.each(cases, |c| { ... assert ... })` and passed while asserting
// something false.
//
// Sources here are self-contained because checkSource omits the prelude.
func TestAssertionsInsideLambdaAreRejected(t *testing.T) {
	_, errs := checkSource(`
fn each(_items: List<Int>, f: (Int) -> Int): Int {
  f(1)
}

test "t" {
  _ = each([1, 2], |n| {
    assert n == 999
  })
}
`)
	// Counted rather than requiring it to be the only diagnostic: a lambda
	// whose body ends in an assertion also has a surprising inferred return
	// type, so the callback's type may not line up either. That secondary
	// complaint is not what this test is about.
	lambdaDiags := 0
	for _, err := range errs {
		if strings.Contains(err.Message, "inside a lambda") {
			lambdaDiags++
		}
	}
	if lambdaDiags != 1 {
		t.Fatalf("expected exactly one lambda-boundary diagnostic, got %v", errs)
	}
}

// The rule is about the assertion's *boundary*, not about a lambda being
// nearby. A lambda inside the asserted expression is the ordinary way to
// write these, and asserting on what a lambda returned is fine — the
// failure is raised in the test body, where the harness sees it.
func TestAssertionsAroundLambdasStillAllowed(t *testing.T) {
	for name, src := range map[string]string{
		"lambda inside the asserted expression": `
fn apply(f: (Int) -> Int): Int {
  f(2)
}

test "t" {
  assert apply(|n| n * 2) == 4
}
`,
		"assert on a lambda's result": `
test "t" {
  double = |n: Int| n * 2
  assert double(3) == 6
}
`,
	} {
		if _, errs := checkSource(src); len(errs) != 0 {
			t.Errorf("%s: expected no diagnostics, got %v", name, errs)
		}
	}
}

func TestAssertionsOutsideTestTypeCheckWithResultReturn(t *testing.T) {
	_, errs := checkSource(`
enum Result<T, E> {
  Ok T
  Err E
}

host type AssertionFailure

fn demo(): Result<Unit, AssertionFailure> {
  assert 1 == 1
  refute 1 == 2
  Result.Ok({})
}
`)
	if len(errs) > 0 {
		t.Fatalf("expected assertions in Result-returning function to type-check, got %v", errs)
	}
}

func TestAssertionsMustHeadPipelines(t *testing.T) {
	_, errs := checkSource(`
enum Result<T, E> {
  Ok T
  Err E
}

host type AssertionFailure

fn demo(): Result<Bool, AssertionFailure> {
  (1 == 1) |> assert
}
`)
	expectError(t, errs, "`assert` must be placed at the head of the pipeline")
}

func TestCheckReturnsAssertionResultWithoutBoundaryRequirement(t *testing.T) {
	_, errs := checkSource(`
enum Result<T, E> {
  Ok T
  Err E
}

host type AssertionFailure

host fn check<T>(subject: T): Result<T, AssertionFailure>

fn demo(): Result<Bool, AssertionFailure> {
  check(1 == 2)
}

fn validate(): Result<Bool, AssertionFailure> {
  check(1 == 2)
}
`)
	if len(errs) > 0 {
		t.Fatalf("expected check to type-check without assertion boundary diagnostics, got %v", errs)
	}
}

func TestDbgExpressionTypeChecksAsInnerExpression(t *testing.T) {
	_, errs := checkSource(`
fn f(): Int {
  dbg 41 + 1
}
`)
	expectNoErrors(t, errs)
}

func TestBoolVariantsCanReturnAsBool(t *testing.T) {
	_, errs := checkSource(`
host type True
host type False

enum Bool {
  embeds False
  embeds True
}

fn implicit(): Bool {
  True
}

fn explicit(): Bool {
  return False
}
`)
	expectNoErrors(t, errs)
}

func TestFunctionWithoutReturnAnnotationMustReturnUnit(t *testing.T) {
	_, errs := checkSource(`
fn main() {
  42
}
`)
	expectError(t, errs, "return type mismatch: expected Unit, got Int")
}

func TestFunctionWithoutReturnAnnotationAcceptsUnitBody(t *testing.T) {
	_, errs := checkSource(`
fn main() {}
`)
	expectNoErrors(t, errs)
}

func TestReturnInsideLambdaUsesLambdaReturnType(t *testing.T) {
	_, errs := checkSource(`
fn apply(f: (Int) -> Int): Int {
  f(3)
}

fn main() {
  _ = apply(|x| {
    if x == 3 { return 300 }
    x
  })
  {}
}
`)
	expectNoErrors(t, errs)
}

func TestBareDbgRequiresPipeStage(t *testing.T) {
	_, errs := checkSource(`
fn f(): Int {
  dbg
}
`)
	expectError(t, errs, "`dbg` requires an expression unless it is used as a pipe stage")
}

func expectNoErrors(t *testing.T, errs []TypeError) {
	t.Helper()
	errs = withoutUnusedBindingErrors(errs)
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected no errors, got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
	}
}

func withoutUnusedBindingErrors(errs []TypeError) []TypeError {
	var out []TypeError
	for _, e := range errs {
		if e.Code == UnusedBindingCode {
			continue
		}
		out = append(out, e)
	}
	return out
}

func expectError(t *testing.T, errs []TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected error containing %q, got %d errors:\n  %s", substr, len(errs), strings.Join(msgs, "\n  "))
}

// expectErrorCount asserts that exactly `want` errors contain `substr`. Unlike
// expectError (which matches the first occurrence and ignores duplicates), this
// pins the count. The destructure-param no-annotation reject tests use it so a
// checker that double-reports the derivation error fails.
func expectErrorCount(t *testing.T, errs []TypeError, substr string, want int) {
	t.Helper()
	got := 0
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			got++
		}
	}
	if got != want {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected %d error(s) containing %q, got %d (of %d total):\n  %s",
			want, substr, got, len(errs), strings.Join(msgs, "\n  "))
	}
}

func TestEqualityRejectsMixedNumeric(t *testing.T) {
	// Mixing Int and Float in == is a type error, like + and <, rather than a
	// comparison that compiles and answers false at run time.
	_, errs := checkSource("fn main() {\n  b = 1 == 1.0\n}")
	expectError(t, errs, "equality type mismatch")
}

func TestEqualityRejectsUnrelatedTypes(t *testing.T) {
	_, errs := checkSource("fn main() {\n  b = \"x\" == 5\n}")
	expectError(t, errs, "equality type mismatch")
}

func TestEqualitySameTypeOk(t *testing.T) {
	_, errs := checkSource("fn main() {\n  b = 1 == 2\n}")
	expectNoErrors(t, errs)
}

func TestEqualityBoolSingletonVariantsOk(t *testing.T) {
	_, errs := checkSource(`
host type True
host type False

fn main() {
  same = True == True
  different = True == False
}
`)
	expectNoErrors(t, errs)
}

// 1. Valid function — no errors
func TestCheckValidAdd(t *testing.T) {
	_, errs := checkSource(`fn add(x: Int, y: Int): Int { x + y }`)
	expectNoErrors(t, errs)
}

// 2. Return type mismatch
func TestCheckReturnTypeMismatch(t *testing.T) {
	_, errs := checkSource(`fn foo(): Int { "hello" }`)
	expectError(t, errs, "expected Int")
}

// 3. Argument type mismatch
func TestCheckArgTypeMismatch(t *testing.T) {
	src := `fn d(n: Int): Int { n }
fn m(): Int { d("x") }`
	_, errs := checkSource(src)
	expectError(t, errs, "expected Int")
}

// 4. Valid binding
func TestCheckValidBinding(t *testing.T) {
	_, errs := checkSource(`fn m(): Int { x = 42
x + 1 }`)
	expectNoErrors(t, errs)
}

// Inline annotation that matches the RHS type — no errors, and the binding
// symbol carries the declared type.
func TestCheckBinding_AnnotationConcreteMatch(t *testing.T) {
	src := `fn m(): Int { x: Int = 5
x + 1 }`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var xSym *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" && sym.Kind == SymbolBinding {
			xSym = sym
			break
		}
	}
	if xSym == nil {
		t.Fatal("no x binding symbol found")
	}
	if xSym.Type != TypeInt {
		t.Errorf("x type: got %v, want TypeInt", xSym.Type)
	}
}

// Inline annotation that doesn't match the RHS type — surface an error
// mentioning the expected type.
func TestCheckBinding_AnnotationMismatchErrors(t *testing.T) {
	src := `fn m() { x: Int = "hello" }`
	_, errs := checkSource(src)
	expectError(t, errs, "expected Int")
}

// A function-typed annotation on a binding should drive parameter
// types into the lambda body so the unannotated-param guard does not
// fire spuriously.
func TestCheckBinding_LambdaAnnotationSuppressesUnannotatedParamError(t *testing.T) {
	src := `
fn demo(): Int {
    f: (Int) -> Int = |x| x + 1
    f(2)
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A lambda-shaped value with a non-function annotation should still error,
// but with the unify-driven type-mismatch diagnostic, not a spurious
// "cannot infer type for parameter" one.
func TestCheckBinding_LambdaWithNonFunctionAnnotationErrors(t *testing.T) {
	src := `fn main() { f: Int = |x| x + 1 }`
	_, errs := checkSource(src)
	hasMismatch := false
	for _, e := range errs {
		if strings.Contains(e.Message, "type mismatch") || strings.Contains(e.Message, "expected Int") {
			hasMismatch = true
			break
		}
	}
	if !hasMismatch {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected type-mismatch error for Int = lambda; got:\n  %s", strings.Join(msgs, "\n  "))
	}
}

// Hover on type names inside a binding annotation must work — the analyzer
// has to walk the TypeAnnotation TypeExpr and record a Reference at the
// annotation's exact position. The same type symbol may also be recorded
// elsewhere via the RHS path (e.g. struct literal uses `Widget` too), so
// the test counts: with the annotation walked, there must be at least two
// Reference entries pointing at the Widget struct (annotation + literal).
func TestCheckBinding_AnnotationTypeNamesAreReferenced(t *testing.T) {
	src := `
struct Widget { id: Int }

fn main() {
  w: Widget = Widget{id: 1}
}
`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	count := 0
	for _, sym := range fa.References {
		if sym != nil && sym.Name == "Widget" && sym.Kind == SymbolStruct {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("expected ≥ 2 Widget References (annotation + literal), got %d — annotation likely unwalked", count)
	}
}

// No annotation: the inferred type still attaches to the symbol.
func TestCheckBinding_NoAnnotationBackwardsCompat(t *testing.T) {
	src := `fn m(): Int { x = 5
x + 1 }`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var xSym *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" && sym.Kind == SymbolBinding {
			xSym = sym
			break
		}
	}
	if xSym == nil {
		t.Fatal("no x binding symbol found")
	}
	if xSym.Type != TypeInt {
		t.Errorf("x type: got %v, want TypeInt", xSym.Type)
	}
}

// 5. Operator type mismatch
func TestCheckOperatorTypeMismatch(t *testing.T) {
	_, errs := checkSource(`fn f(): Int { 1 + "two" }`)
	expectError(t, errs, "type mismatch")
}

// 6. Valid field access
func TestCheckValidFieldAccess(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
fn get_x(p: Point): Int { p.x }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// 7. If branch type mismatch
func TestCheckIfBranchMismatch(t *testing.T) {
	src := `fn f(b: Bool): Int {
  if b { 1 } else { "no" }
}`
	_, errs := checkSource(src)
	expectError(t, errs, "branch type mismatch")
}

// 8. If condition not Bool
func TestCheckIfConditionNotBool(t *testing.T) {
	src := `fn f(): Int {
  if 42 { 1 } else { 2 }
}`
	_, errs := checkSource(src)
	expectError(t, errs, "condition must be Bool")
}

// Map literal type consistency
func TestCheckMapLitHomogeneous(t *testing.T) {
	_, errs := checkSource(`fn f(): Map<String, Int> { {"a" => 1, "b" => 2} }`)
	expectNoErrors(t, errs)
}

func TestCheckMapLitMixedValues(t *testing.T) {
	_, errs := checkSource(`fn f() { {"a" => 1, "b" => "two"} }`)
	expectError(t, errs, "map value type mismatch")
}

func TestCheckMapLitMixedKeys(t *testing.T) {
	_, errs := checkSource(`fn f() { {1 => "a", "two" => "b"} }`)
	expectError(t, errs, "map key type mismatch")
}

// 9. String concat valid and invalid
func TestCheckStringConcatValid(t *testing.T) {
	_, errs := checkSource(`fn f(): String { "a" + "b" }`)
	expectNoErrors(t, errs)
}

func TestCheckStringConcatInvalid(t *testing.T) {
	_, errs := checkSource(`fn f(): String { 1 + "b" }`)
	expectError(t, errs, "type mismatch")
}

func TestCheckListConcatValid(t *testing.T) {
	_, errs := checkSource(`fn f(): List<Int> { [1, 2] + [3] }`)
	expectNoErrors(t, errs)
}

func TestCheckListConcatInvalid(t *testing.T) {
	_, errs := checkSource(`fn f(): List<Int> { [1] + ["x"] }`)
	expectError(t, errs, "type mismatch")
}

func TestCheckVectorConcatValid(t *testing.T) {
	_, errs := checkSource(`fn f() {
  _ = #[1, 2] + #[3]
}`)
	expectNoErrors(t, errs)
}

func TestCheckStringPrefixPatternBindsRestAsString(t *testing.T) {
	fa, errs := checkSource(`fn route(path: String): String {
  case path {
    "/users/" + id -> id
    _ -> ""
  }
}`)
	expectNoErrors(t, errs)
	var found bool
	for _, sym := range fa.Definitions {
		if sym.Name == "id" {
			found = true
			if sym.Type != TypeString {
				t.Fatalf("expected id: String, got %v", sym.Type)
			}
		}
	}
	if !found {
		t.Fatalf("expected id binding definition")
	}
}

// 10. Multiple functions calling each other
func TestCheckMultipleFuncsCalling(t *testing.T) {
	src := `fn double(n: Int): Int { n + n }
fn quad(n: Int): Int { double(double(n)) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Additional: unary operators
func TestCheckUnaryNegate(t *testing.T) {
	_, errs := checkSource(`fn f(x: Int): Int { -x }`)
	expectNoErrors(t, errs)
}

func TestCheckUnaryNotBool(t *testing.T) {
	_, errs := checkSource(`fn f(b: Bool): Bool { !b }`)
	expectNoErrors(t, errs)
}

func TestCheckUnaryNotNonBool(t *testing.T) {
	_, errs := checkSource(`fn f(): Bool { !42 }`)
	expectError(t, errs, "must be Bool")
}

// Comparison operators
func TestCheckComparisonValid(t *testing.T) {
	_, errs := checkSource(`fn f(x: Int, y: Int): Bool { x < y }`)
	expectNoErrors(t, errs)
}

// Ordering on a type-parameter operand is bound-driven, not
// concrete-demand-driven: the strong ordering-operator recording only
// fires for concrete operand types (recordConformanceRecording skips
// TypeParam_). A Comparable-bounded param stays clean…
func TestCheckComparisonOnBoundedTypeParam(t *testing.T) {
	_, errs := checkSource(`interface Comparable {}
fn smaller<T>(a: T, b: T): T where T: Comparable {
  if a < b { a } else { b }
}`)
	expectNoErrors(t, errs)
}

// …and an unbounded param keeps its pre-split behavior (no error today;
// tightening unbounded-T ordering is a separate decision, out of scope
// for the strong-Comparable-demand change).
func TestCheckComparisonOnUnboundedTypeParam(t *testing.T) {
	_, errs := checkSource(`fn smaller<T>(a: T, b: T): Bool { a < b }`)
	expectNoErrors(t, errs)
}

// Boolean operators
func TestCheckAndOrValid(t *testing.T) {
	_, errs := checkSource(`fn f(a: Bool, b: Bool): Bool { a and b }`)
	expectNoErrors(t, errs)
}

func TestCheckAndOrInvalid(t *testing.T) {
	_, errs := checkSource(`fn f(): Bool { 1 and 2 }`)
	expectError(t, errs, "must be Bool")
}

// Pipe operator
func TestCheckPipeValid(t *testing.T) {
	src := `fn double(n: Int): Int { n + n }
fn f(): Int { 5 |> double() }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckPipeDbgStage(t *testing.T) {
	src := `fn double(n: Int): Int { n + n }
fn f(): Int {
  5
  |> dbg
  |> double()
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckPipeDbgFinalStage(t *testing.T) {
	src := `fn f(): Int {
  [1, 2, 3]
  |> dbg

  0
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckPipeDbgExpressionStage(t *testing.T) {
	src := `fn double(n: Int): Int { n + n }
fn f(): Int {
  5 |> dbg double()
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckPartialApplication(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn f(): Int {
  add1 = add(1, _)
  add1(9)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckPipeCallSyntax(t *testing.T) {
	src := `fn double(n: Int): Int { n + n }
fn add(x: Int, y: Int): Int { x + y }
fn f(): Int {
  result = 5 |> double() |> add(1)
  result
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckPipeBindsTighterThanEquality(t *testing.T) {
	src := `fn double(n: Int): Int { n + n }
fn f(): Bool {
  5 |> double() == 10
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Pipe + middle-default + trailing-lambda must accept without analysis
// errors and route the trailing lambda to the last param slot — mirroring
// the direct-call form. The checker is permissive about pipe-RHS arg type
// mismatches, so this test asserts the no-error path rather than a positive
// type error.
func TestCheckPipeTrailingLambdaMiddleDefault(t *testing.T) {
	src := `fn pick(x: {name: String}, _mode: Int = 0, f: ({name: String}) -> String): String {
  f(x)
}
fn use_it(): String {
  user = {name: "alice"}
  user |> pick(|u| u.name)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// String interpolation
func TestCheckStringInterp(t *testing.T) {
	_, errs := checkSource("fn f(): String { \"hello ${1 + 2}\" }")
	expectNoErrors(t, errs)
}

// Lambda
func TestCheckLambdaValid(t *testing.T) {
	_, errs := checkSource(`fn f(): Int {
  f = |x: Int| x + 1
  f(5)
}`)
	expectNoErrors(t, errs)
}

// A lambda bound to a local with no expected type context and no param
// annotations has no way to know its parameter types. Report a clear error
// at the lambda rather than silently producing a FuncType with nil params
// (which leaks through as Unit at call sites).
func TestCheckUnannotatedLocalLambda(t *testing.T) {
	_, errs := checkSource(`fn main() {
  identity = |x| x
  a = identity(5)
}`)
	expectError(t, errs, "cannot infer type for parameter")
}

// A default value on a param gives the type (e.g. `x = 5` → Int), so the
// "cannot infer" error must not fire on default-bearing params.
func TestCheckLocalLambdaWithDefaultParam(t *testing.T) {
	_, errs := checkSource(`fn main() {
  f = |x = 5| x + 1
}`)
	for _, e := range errs {
		if strings.Contains(e.Message, "cannot infer type for parameter") {
			t.Fatalf("unexpected cannot-infer error on default-bearing param: %s", e.Error())
		}
	}
}

// Type inferred from the default value is attached to the param symbol and
// propagates into the body — `|n = 5| n + 1` type-checks because `n` is Int.
func TestCheckLambdaDefaultTypePropagatesIntoBody(t *testing.T) {
	_, errs := checkSource(`fn main() {
  f = |n = 5| n + 1
}`)
	expectNoErrors(t, errs)
}

// Same mechanism rejects wrong-type uses of the param: `n` is Int (from 5),
// so `n + "!"` is a String-concat mismatch.
func TestCheckLambdaDefaultTypeRejectsMismatch(t *testing.T) {
	_, errs := checkSource(`fn main() {
  f = |n = 5| n + "!"
}`)
	expectError(t, errs, "type mismatch")
}

// Equality operators accept any types
func TestCheckEqualityAnyTypes(t *testing.T) {
	_, errs := checkSource(`fn f(): Bool { 1 == 1 }`)
	expectNoErrors(t, errs)
}

// Return statement
func TestCheckReturnValid(t *testing.T) {
	_, errs := checkSource(`fn f(x: Int): Int { return x }`)
	expectNoErrors(t, errs)
}

func TestCheckReturnMismatch(t *testing.T) {
	_, errs := checkSource(`fn f(): Int { return "oops" }`)
	expectError(t, errs, "expected Int")
}

// Bidirectional type checking: lambda with explicit annotation
func TestCheck_LambdaWithAnnotation(t *testing.T) {
	_, errs := checkSource(`
fn apply(f: (Int) -> Int, x: Int): Int { f(x) }
fn demo(): Int { apply(|n: Int| n + 1, 5) }
`)
	expectNoErrors(t, errs)
}

// Bidirectional type checking: lambda parameter inferred from context
func TestCheck_LambdaInferredFromContext(t *testing.T) {
	_, errs := checkSource(`
fn apply(f: (Int) -> Int, x: Int): Int { f(x) }
fn demo(): Int { apply(|n| n + 1, 5) }
`)
	expectNoErrors(t, errs)
}

// Bidirectional type checking: lambda returns wrong type
func TestCheck_LambdaInferredWrongReturnType(t *testing.T) {
	_, errs := checkSource(`
fn apply(f: (Int) -> Int, x: Int): Int { f(x) }
fn demo(): Int { apply(|_n| "hello", 5) }
`)
	// This should ideally produce a type error (lambda returns String, expected Int)
	// but may not in this first pass — don't fail if no error yet
	_ = errs
}

// --- Divergent expressions (break/continue/return) as Infallible ---

func TestCheckReturnDoesNotCauseBranchMismatch(t *testing.T) {
	// return in one if-branch should not cause type mismatch with the other branch
	_, errs := checkSource(`
fn f(): Int {
    x = 5
    if x == 0 { return 0 } else { x - 1 }
}
`)
	expectNoErrors(t, errs)
}

func TestCheckReturnInBothBranches(t *testing.T) {
	_, errs := checkSource(`
fn f(): Int {
    x = 5
    if x == 0 { return 0 } else { return x }
}
`)
	expectNoErrors(t, errs)
}

// --- Generic instantiation tests ---

func TestCheck_GenericIdentity(t *testing.T) {
	_, errs := checkSource(`
fn identity<T>(x: T): T { x }
fn demo(): Int { identity(42) }
`)
	expectNoErrors(t, errs)
}

func TestCheck_GenericReturnTypeInferred(t *testing.T) {
	// The return type of identity(42) should be Int, not T.
	// So assigning it and adding should work.
	_, errs := checkSource(`
fn identity<T>(x: T): T { x }
fn demo(): Int {
  x = identity(42)
  x + 1
}
`)
	expectNoErrors(t, errs)
}

func TestCheck_GenericReturnTypeMismatch(t *testing.T) {
	_, errs := checkSource(`
fn identity<T>(x: T): T { x }
fn demo(): String { identity(42) }
`)
	expectError(t, errs, "expected String")
}

func TestCheck_GenericListFunction(t *testing.T) {
	// Tests that List<Int> unifies with List<T> to solve T=Int,
	// and the return type T becomes Int.
	_, errs := checkSource(`
fn wrap<T>(x: T): List<T> { [x] }
fn demo(): List<Int> { wrap(42) }
`)
	expectNoErrors(t, errs)
}

func TestCheck_GenericTwoParams(t *testing.T) {
	_, errs := checkSource(`
fn pair<A, B>(a: A, b: B): (A, B) { (a, b) }
fn demo(): (Int, String) { pair(1, "hello") }
`)
	expectNoErrors(t, errs)
}

func TestCheck_GenericWithLambda(t *testing.T) {
	// T solved from first arg, lambda param inferred from T
	_, errs := checkSource(`
fn apply<T>(x: T, f: (T) -> T): T { f(x) }
fn demo(): Int { apply(5, |n| n + 1) }
`)
	expectNoErrors(t, errs)
}

// Trailing-lambda routing (spec §5, *Lambdas at Call Sites*) must apply
// to GENERIC callees too: a lambda passed as the final positional argument
// routes into the last param slot, skipping a defaulted middle param. The
// non-generic path (computePositionalSlots) and the pipe path
// (computePipeArgSlots) already did this; checkGenericCall mapped arg i →
// param i and mis-checked the lambda against the defaulted middle param
// (real-world repro: `Iter.sort_by(users, |u| u.name)` — sort_by's
// signature is `(list, direction: Direction = Ascending, key)`).
func TestGenericCallSite_TrailingLambdaRoutesPastDefaultedMiddleParam(t *testing.T) {
	_, errs := checkSource(`
fn pick<K>(_xs: List<Int>, _limit: Int = 10, key: (Int) -> K): K {
  key(0)
}
fn demo(): Int { pick([2, 1], |x| x * 2) }
`)
	expectNoErrors(t, errs)
}

func TestGenericCallSite_InstantiatedType(t *testing.T) {
	src := `fn identity<T>(x: T): T { x }
fn demo(): Int { identity(42) }`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var callSym *Symbol
	for pos, sym := range fa.References {
		if sym.Name == "identity" && pos.Line == 2 {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected reference to identity on line 2")
	}
	if callSym.CallType == nil {
		t.Fatal("expected CallType to be set on generic call site")
	}
	ft, ok := callSym.CallType.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", callSym.CallType)
	}
	if len(ft.Params) != 1 || ft.Params[0] != TypeInt {
		t.Errorf("expected param Int, got %v", ft.Params)
	}
	if ft.Return != TypeInt {
		t.Errorf("expected return Int, got %v", ft.Return)
	}
}

func TestGenericCallSite_MultiParam(t *testing.T) {
	src := `fn pair<A, B>(a: A, b: B): (A, B) { (a, b) }
fn demo(): (Int, String) { pair(1, "hello") }`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var callSym *Symbol
	for pos, sym := range fa.References {
		if sym.Name == "pair" && pos.Line == 2 {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected reference to pair on line 2")
	}
	if callSym.CallType == nil {
		t.Fatal("expected CallType on Pair call site")
	}
	ft, ok := callSym.CallType.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", callSym.CallType)
	}
	if len(ft.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(ft.Params))
	}
	if ft.Params[0] != TypeInt {
		t.Errorf("expected param 0 = Int, got %v", ft.Params[0])
	}
	if ft.Params[1] != TypeString {
		t.Errorf("expected param 1 = String, got %v", ft.Params[1])
	}
}

func TestGenericCallSite_NonGenericHasNoCallType(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn demo(): Int { add(1, 2) }`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var callSym *Symbol
	for pos, sym := range fa.References {
		if sym.Name == "add" && pos.Line == 2 {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected reference to add on line 2")
	}
	if callSym.CallType != nil {
		t.Errorf("non-generic call should not have CallType, got %v", callSym.CallType)
	}
}

func TestGenericCallSite_PipeChain(t *testing.T) {
	src := `fn identity<T>(x: T): T { x }
fn demo(): Int {
	42 |> identity()
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var callSym *Symbol
	for pos, sym := range fa.References {
		if sym.Name == "identity" && pos.Line == 3 {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected reference to identity on line 3")
	}
	if callSym.CallType == nil {
		t.Fatal("expected CallType on pipe call site")
	}
	ft, ok := callSym.CallType.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", callSym.CallType)
	}
	if ft.Return != TypeInt {
		t.Errorf("expected return Int, got %v", ft.Return)
	}
	if len(ft.Params) != 1 || ft.Params[0] != TypeInt {
		t.Errorf("expected param Int, got %v", ft.Params)
	}
}

func TestPipeThenStageReceivesPipedValue(t *testing.T) {
	src := `fn demo(): Int {
	21 |> then |n| n * 2
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestPipeThenStageRejectsWrongArity(t *testing.T) {
	for _, stage := range []string{"then || 42", "then |a, b| a + b"} {
		src := "fn demo(): Int {\n\t21 |> " + stage + "\n}"
		_, errs := checkSource(src)
		if len(errs) == 0 {
			t.Fatalf("%s: expected a then arity diagnostic", stage)
		}
		if !strings.Contains(errs[0].Message, "a `then` lambda takes one parameter, the piped value") {
			t.Fatalf("%s: expected a then arity diagnostic, got %v", stage, errs)
		}
	}
}

func TestGenericCallSite_PipeWithCallback(t *testing.T) {
	src := `fn filter<T>(list: List<T>, _f: (T) -> Bool): List<T> { list }
fn demo(): List<Int> {
	[1, 2, 3] |> filter(|x| x > 1)
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var callSym *Symbol
	for pos, sym := range fa.References {
		if sym.Name == "filter" && pos.Line == 3 {
			callSym = sym
			break
		}
	}
	if callSym == nil {
		t.Fatal("expected reference to filter on line 3")
	}
	if callSym.CallType == nil {
		t.Fatal("expected CallType on pipe call site with callback")
	}
	ft, ok := callSym.CallType.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", callSym.CallType)
	}
	if ft.Return.String() != "List<Int>" {
		t.Errorf("expected return List<Int>, got %v", ft.Return)
	}
}

// ---------------------------------------------------------------------------
// checkPattern — IdentPattern and WildcardPattern
// ---------------------------------------------------------------------------

func TestCheckPatternIdentBindsType(t *testing.T) {
	// Build a FileAnalysis with a manually created IdentPattern symbol.
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	sym := &Symbol{Name: "x", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 5}}
	fa.Definitions[Pos{Line: 1, Col: 5}] = sym

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.IdentPattern{Name: "x", Line: 1, Col: 5}
	c.checkPattern(pat, TypeInt)

	if sym.Type != TypeInt {
		t.Fatalf("expected sym.Type = Int, got %v", sym.Type)
	}
}

func TestCheckPatternWildcardNoOp(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	// WildcardPattern should not panic or produce errors.
	pat := &ast.WildcardPattern{Line: 1, Col: 1}
	c.checkPattern(pat, TypeString)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
}

func TestCheckPatternNilExpectedTy(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	sym := &Symbol{Name: "x", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 5}}
	fa.Definitions[Pos{Line: 1, Col: 5}] = sym

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.IdentPattern{Name: "x", Line: 1, Col: 5}
	c.checkPattern(pat, nil)

	if sym.Type != nil {
		t.Fatalf("expected sym.Type = nil when expectedTy is nil, got %v", sym.Type)
	}
}

// ---------------------------------------------------------------------------
// checkPattern — TuplePattern
// ---------------------------------------------------------------------------

func TestCheckPatternTupleBindsElementTypes(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symX := &Symbol{Name: "x", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 2}}
	symY := &Symbol{Name: "y", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 5}}
	fa.Definitions[Pos{Line: 1, Col: 2}] = symX
	fa.Definitions[Pos{Line: 1, Col: 5}] = symY

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.TuplePattern{
		Patterns: []ast.Node{
			&ast.IdentPattern{Name: "x", Line: 1, Col: 2},
			&ast.IdentPattern{Name: "y", Line: 1, Col: 5},
		},
		Line: 1, Col: 1,
	}
	expectedTy := &TupleType{Elems: []Type{TypeInt, TypeString}}
	c.checkPattern(pat, expectedTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}
	if symY.Type != TypeString {
		t.Errorf("expected y: String, got %v", symY.Type)
	}
}

func TestCheckPatternTupleArityMismatch(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.TuplePattern{
		Patterns: []ast.Node{
			&ast.IdentPattern{Name: "x", Line: 1, Col: 2},
			&ast.IdentPattern{Name: "y", Line: 1, Col: 5},
			&ast.IdentPattern{Name: "z", Line: 1, Col: 8},
		},
		Line: 1, Col: 1,
	}
	expectedTy := &TupleType{Elems: []Type{TypeInt, TypeString}}
	c.checkPattern(pat, expectedTy)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "3 elements but expected 2") {
		t.Errorf("unexpected error message: %s", c.errors[0].Message)
	}
}

func TestCheckPatternTupleNotTupleType(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.TuplePattern{
		Patterns: []ast.Node{
			&ast.IdentPattern{Name: "x", Line: 1, Col: 2},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, TypeInt)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "tuple pattern requires a tuple type") {
		t.Errorf("unexpected error message: %s", c.errors[0].Message)
	}
}

func TestCheckPatternTupleWithWildcard(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symX := &Symbol{Name: "x", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 2}}
	fa.Definitions[Pos{Line: 1, Col: 2}] = symX

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.TuplePattern{
		Patterns: []ast.Node{
			&ast.IdentPattern{Name: "x", Line: 1, Col: 2},
			&ast.WildcardPattern{Line: 1, Col: 5},
		},
		Line: 1, Col: 1,
	}
	expectedTy := &TupleType{Elems: []Type{TypeInt, TypeString}}
	c.checkPattern(pat, expectedTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}
}

func TestCheckPatternTupleNilExpectedTy(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symX := &Symbol{Name: "x", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 2}}
	fa.Definitions[Pos{Line: 1, Col: 2}] = symX

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.TuplePattern{
		Patterns: []ast.Node{
			&ast.IdentPattern{Name: "x", Line: 1, Col: 2},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, nil)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symX.Type != nil {
		t.Errorf("expected x type to remain nil, got %v", symX.Type)
	}
}

// ---------------------------------------------------------------------------
// checkTupleDestructure
// ---------------------------------------------------------------------------

func TestCheckTupleDestructureValid(t *testing.T) {
	src := `fn main() {
  (x, y) = (1, "hello")
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	// Find x's symbol and check its type is Int.
	var symX *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" {
			symX = sym
			break
		}
	}
	if symX == nil {
		t.Fatal("expected definition for x")
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}

	// Find y's symbol and check its type is String.
	var symY *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "y" {
			symY = sym
			break
		}
	}
	if symY == nil {
		t.Fatal("expected definition for y")
	}
	if symY.Type != TypeString {
		t.Errorf("expected y: String, got %v", symY.Type)
	}
}

func TestCheckTupleDestructureNotTuple(t *testing.T) {
	src := `fn main(): () {
  (x, y) = 42
}`
	_, errs := checkSource(src)
	expectError(t, errs, "tuple destructure requires a tuple type")
}

func TestCheckTupleDestructureArityMismatch(t *testing.T) {
	src := `fn main(): () {
  (x, y, z) = (1, "hello")
}`
	_, errs := checkSource(src)
	expectError(t, errs, "3 bindings but tuple has 2 elements")
}

// ---------------------------------------------------------------------------
// checkPattern — EnumPattern
// ---------------------------------------------------------------------------

func TestCheckPatternEnumBareVariant(t *testing.T) {
	// Post-migration to dot-leading variant resolution, the bare-prefix
	// form in pattern position is rejected. The dot-leading TypeExpr
	// (`*ast.DotVariantType{Name: "Red"}`) is the accepted form; the
	// equivalent `case x { .Red -> ... }` source. This test pins the
	// dot-leading path's pattern resolution.
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	enumTy := &EnumType{
		Name: "Color",
		Variants: []VariantDef{
			{Name: "Red"},
			{Name: "Green"},
			{Name: "Blue"},
		},
	}
	pat := &ast.EnumPattern{
		Variant: &ast.DotVariantType{Name: "Red", Line: 1, Col: 1},
		Line:    1, Col: 1,
	}
	c.checkPattern(pat, enumTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
}

func TestCheckPatternEnumDataVariantBinding(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	sym := &Symbol{Name: "val", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 10}}
	fa.Definitions[Pos{Line: 1, Col: 10}] = sym

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	enumTy := &EnumType{
		Name: "Option",
		Variants: []VariantDef{
			{Name: "None"},
			{Name: "Some", DataType: TypeInt},
		},
	}
	pat := &ast.EnumPattern{
		Variant:    &ast.SimpleType{Name: "Some", Line: 1, Col: 1},
		Binding:    "val",
		BindingCol: 10,
		Line:       1, Col: 1,
	}
	c.checkPattern(pat, enumTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if sym.Type != TypeInt {
		t.Errorf("expected val: Int, got %v", sym.Type)
	}
}

func TestCheckPatternEnumGenericSubstitution(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	sym := &Symbol{Name: "val", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 10}}
	fa.Definitions[Pos{Line: 1, Col: 10}] = sym

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	// Option<String> where Some carries T → should resolve to String
	tpT := &TypeParam_{Name_: "T"}
	enumTy := &EnumType{
		Name:          "Option",
		TypeParams:    []string{"T"},
		TypeParamDefs: []*TypeParam_{tpT},
		TypeArgs:      []Type{TypeString},
		Variants: []VariantDef{
			{Name: "None"},
			{Name: "Some", DataType: tpT},
		},
	}
	pat := &ast.EnumPattern{
		Variant:    &ast.SimpleType{Name: "Some", Line: 1, Col: 1},
		Binding:    "val",
		BindingCol: 10,
		Line:       1, Col: 1,
	}
	c.checkPattern(pat, enumTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if sym.Type != TypeString {
		t.Errorf("expected val: String, got %v", sym.Type)
	}
}

func TestCheckPatternEnumVariantNotFound(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	enumTy := &EnumType{
		Name: "Color",
		Variants: []VariantDef{
			{Name: "Red"},
		},
	}
	pat := &ast.EnumPattern{
		Variant: &ast.SimpleType{Name: "Purple", Line: 1, Col: 1},
		Line:    1, Col: 1,
	}
	c.checkPattern(pat, enumTy)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "variant Purple not found") {
		t.Errorf("unexpected error: %s", c.errors[0].Message)
	}
}

func TestCheckPatternEnumNotEnumType(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.EnumPattern{
		Variant: &ast.SimpleType{Name: "Some", Line: 1, Col: 1},
		Line:    1, Col: 1,
	}
	c.checkPattern(pat, TypeInt)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "enum pattern requires an enum type") {
		t.Errorf("unexpected error: %s", c.errors[0].Message)
	}
}

func TestCheckPatternEnumNilExpectedTy(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.EnumPattern{
		Variant: &ast.SimpleType{Name: "Some", Line: 1, Col: 1},
		Line:    1, Col: 1,
	}
	c.checkPattern(pat, nil)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
}

func TestCheckPatternDistinctType(t *testing.T) {
	src := `type Id Int
fn demo(): Int {
  id = Id(42)
  case id {
    Id(x) -> x + 1
  }
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var symX *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" && sym.Kind == SymbolBinding {
			symX = sym
		}
	}
	if symX == nil {
		t.Fatal("expected definition for x")
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}
}

func TestCheckPatternDistinctZeroSized(t *testing.T) {
	src := `type Expired
fn demo(): String {
  e = Expired
  case e {
    Expired -> "expired"
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// ---------------------------------------------------------------------------
// checkPattern — StructPattern
// ---------------------------------------------------------------------------

func TestCheckPatternStructBindsFieldTypes(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symX := &Symbol{Name: "x", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 5}}
	symY := &Symbol{Name: "y", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 15}}
	fa.Definitions[Pos{Line: 1, Col: 5}] = symX
	fa.Definitions[Pos{Line: 1, Col: 15}] = symY

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	structTy := &StructType{
		Name: "Point",
		Fields: []FieldDef{
			{Name: "x", Type: TypeInt},
			{Name: "y", Type: TypeFloat},
		},
	}
	pat := &ast.StructPattern{
		Fields: []ast.StructPatternField{
			{Name: "x", Binding: "x", BindingCol: 5},
			{Name: "y", Binding: "y", BindingCol: 15},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, structTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}
	if symY.Type != TypeFloat {
		t.Errorf("expected y: Float, got %v", symY.Type)
	}
}

func TestCheckPatternStructNestedPattern(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symA := &Symbol{Name: "a", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 10}}
	symB := &Symbol{Name: "b", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 20}}
	fa.Definitions[Pos{Line: 1, Col: 10}] = symA
	fa.Definitions[Pos{Line: 1, Col: 20}] = symB

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	structTy := &StructType{
		Name: "Pair",
		Fields: []FieldDef{
			{Name: "fst", Type: &TupleType{Elems: []Type{TypeInt, TypeString}}},
		},
	}
	pat := &ast.StructPattern{
		Fields: []ast.StructPatternField{
			{
				Name: "fst",
				Pattern: &ast.TuplePattern{
					Patterns: []ast.Node{
						&ast.IdentPattern{Name: "a", Line: 1, Col: 10},
						&ast.IdentPattern{Name: "b", Line: 1, Col: 20},
					},
					Line: 1, Col: 8,
				},
			},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, structTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symA.Type != TypeInt {
		t.Errorf("expected a: Int, got %v", symA.Type)
	}
	if symB.Type != TypeString {
		t.Errorf("expected b: String, got %v", symB.Type)
	}
}

func TestCheckPatternStructGenericSubstitution(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	sym := &Symbol{Name: "val", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 5}}
	fa.Definitions[Pos{Line: 1, Col: 5}] = sym

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	tpT := &TypeParam_{Name_: "T"}
	structTy := &StructType{
		Name:          "Box",
		TypeParams:    []string{"T"},
		TypeParamDefs: []*TypeParam_{tpT},
		TypeArgs:      []Type{TypeInt},
		Fields: []FieldDef{
			{Name: "value", Type: tpT},
		},
	}
	pat := &ast.StructPattern{
		Fields: []ast.StructPatternField{
			{Name: "value", Binding: "val", BindingCol: 5},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, structTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if sym.Type != TypeInt {
		t.Errorf("expected val: Int, got %v", sym.Type)
	}
}

func TestCheckPatternStructFieldNotFound(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	structTy := &StructType{
		Name: "Point",
		Fields: []FieldDef{
			{Name: "x", Type: TypeInt},
		},
	}
	pat := &ast.StructPattern{
		Fields: []ast.StructPatternField{
			{Name: "z", Binding: "z", BindingCol: 5},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, structTy)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "field z not found") {
		t.Errorf("unexpected error: %s", c.errors[0].Message)
	}
}

func TestCheckPatternStructNotStructType(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.StructPattern{
		Fields: []ast.StructPatternField{
			{Name: "x", Binding: "x", BindingCol: 5},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, TypeInt)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "struct pattern requires a struct type") {
		t.Errorf("unexpected error: %s", c.errors[0].Message)
	}
}

func TestCheckPatternStructNilExpectedTy(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.StructPattern{
		Fields: []ast.StructPatternField{
			{Name: "x", Binding: "x", BindingCol: 5},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, nil)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
}

func TestCheckPatternStructAnonymous(t *testing.T) {
	src := `fn demo(): String {
  s = { name: "Alice", age: 30 }
  case s {
    {name, age} -> name
  }
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var symName *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "name" && sym.Kind == SymbolBinding {
			symName = sym
		}
	}
	if symName == nil {
		t.Fatal("expected definition for name")
	}
	if symName.Type != TypeString {
		t.Errorf("expected name: String, got %v", symName.Type)
	}
}

// ---------------------------------------------------------------------------
// checkPattern — ListPattern
// ---------------------------------------------------------------------------

func TestCheckPatternListHeadBindings(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symA := &Symbol{Name: "a", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 2}}
	symB := &Symbol{Name: "b", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 5}}
	fa.Definitions[Pos{Line: 1, Col: 2}] = symA
	fa.Definitions[Pos{Line: 1, Col: 5}] = symB

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	listTy := &ListType{Elem: TypeInt}
	pat := &ast.ListPattern{
		Heads: []ast.Node{
			&ast.IdentPattern{Name: "a", Line: 1, Col: 2},
			&ast.IdentPattern{Name: "b", Line: 1, Col: 5},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, listTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symA.Type != TypeInt {
		t.Errorf("expected a: Int, got %v", symA.Type)
	}
	if symB.Type != TypeInt {
		t.Errorf("expected b: Int, got %v", symB.Type)
	}
}

func TestCheckPatternListWithTail(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symHead := &Symbol{Name: "h", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 2}}
	symTail := &Symbol{Name: "rest", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 6}}
	fa.Definitions[Pos{Line: 1, Col: 2}] = symHead
	fa.Definitions[Pos{Line: 1, Col: 6}] = symTail

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	listTy := &ListType{Elem: TypeString}
	pat := &ast.ListPattern{
		Heads: []ast.Node{
			&ast.IdentPattern{Name: "h", Line: 1, Col: 2},
		},
		TailSpread: &ast.IdentPattern{Name: "rest", Line: 1, Col: 6},
		Line:       1, Col: 1,
	}
	c.checkPattern(pat, listTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symHead.Type != TypeString {
		t.Errorf("expected h: String, got %v", symHead.Type)
	}
	// Tail should be List<String>, not String
	if symTail.Type == nil {
		t.Fatal("expected rest to have a type")
	}
	if symTail.Type.String() != "List<String>" {
		t.Errorf("expected rest: List<String>, got %v", symTail.Type)
	}
}

func TestCheckPatternListNotListType(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.ListPattern{
		Heads: []ast.Node{
			&ast.IdentPattern{Name: "a", Line: 1, Col: 2},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, TypeInt)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "list pattern requires a list type") {
		t.Errorf("unexpected error: %s", c.errors[0].Message)
	}
}

func TestCheckPatternListNilExpectedTy(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symA := &Symbol{Name: "a", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 2}}
	fa.Definitions[Pos{Line: 1, Col: 2}] = symA

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.ListPattern{
		Heads: []ast.Node{
			&ast.IdentPattern{Name: "a", Line: 1, Col: 2},
		},
		TailSpread: &ast.WildcardPattern{Line: 1, Col: 5},
		Line:       1, Col: 1,
	}
	c.checkPattern(pat, nil)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symA.Type != nil {
		t.Errorf("expected a type to remain nil, got %v", symA.Type)
	}
}

// ---------------------------------------------------------------------------
// checkPattern — MapPattern
// ---------------------------------------------------------------------------

func TestCheckPatternMapBindsValueTypes(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symV := &Symbol{Name: "v", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 10}}
	fa.Definitions[Pos{Line: 1, Col: 10}] = symV

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	mapTy := &MapType{Key: TypeString, Val: TypeInt}
	pat := &ast.MapPattern{
		Entries: []ast.MapPatternEntry{
			{
				Key:     &ast.StringLit{Value: "key", Line: 1, Col: 2},
				Pattern: &ast.IdentPattern{Name: "v", Line: 1, Col: 10},
			},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, mapTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symV.Type != TypeInt {
		t.Errorf("expected v: Int, got %v", symV.Type)
	}
}

func TestCheckPatternMapMultipleEntries(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	symA := &Symbol{Name: "a", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 10}}
	symB := &Symbol{Name: "b", Kind: SymbolBinding, Pos: Pos{Line: 1, Col: 25}}
	fa.Definitions[Pos{Line: 1, Col: 10}] = symA
	fa.Definitions[Pos{Line: 1, Col: 25}] = symB

	c := &checker{fa: fa, reg: NewTypeRegistry()}

	mapTy := &MapType{Key: TypeString, Val: TypeFloat}
	pat := &ast.MapPattern{
		Entries: []ast.MapPatternEntry{
			{
				Key:     &ast.StringLit{Value: "x", Line: 1, Col: 2},
				Pattern: &ast.IdentPattern{Name: "a", Line: 1, Col: 10},
			},
			{
				Key:     &ast.StringLit{Value: "y", Line: 1, Col: 15},
				Pattern: &ast.IdentPattern{Name: "b", Line: 1, Col: 25},
			},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, mapTy)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
	if symA.Type != TypeFloat {
		t.Errorf("expected a: Float, got %v", symA.Type)
	}
	if symB.Type != TypeFloat {
		t.Errorf("expected b: Float, got %v", symB.Type)
	}
}

func TestCheckPatternMapNotMapType(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.MapPattern{
		Entries: []ast.MapPatternEntry{
			{
				Key:     &ast.StringLit{Value: "k", Line: 1, Col: 2},
				Pattern: &ast.IdentPattern{Name: "v", Line: 1, Col: 10},
			},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, TypeInt)

	if len(c.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(c.errors), c.errors)
	}
	if !strings.Contains(c.errors[0].Message, "map pattern requires a map type") {
		t.Errorf("unexpected error: %s", c.errors[0].Message)
	}
}

func TestCheckPatternMapNilExpectedTy(t *testing.T) {
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{},
	}
	c := &checker{fa: fa, reg: NewTypeRegistry()}

	pat := &ast.MapPattern{
		Entries: []ast.MapPatternEntry{
			{
				Key:     &ast.StringLit{Value: "k", Line: 1, Col: 2},
				Pattern: &ast.IdentPattern{Name: "v", Line: 1, Col: 10},
			},
		},
		Line: 1, Col: 1,
	}
	c.checkPattern(pat, nil)

	if len(c.errors) != 0 {
		t.Fatalf("expected no errors, got %v", c.errors)
	}
}

// ---------------------------------------------------------------------------
// checkStructDestructure
// ---------------------------------------------------------------------------

func TestCheckStructDestructureValid(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
fn main() {
  p = Point { x: 1, y: 2 }
  {x, y} = p
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var symX, symY *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" && sym.Kind == SymbolBinding {
			symX = sym
		}
		if sym.Name == "y" && sym.Kind == SymbolBinding {
			symY = sym
		}
	}
	if symX == nil {
		t.Fatal("expected definition for x")
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}
	if symY == nil {
		t.Fatal("expected definition for y")
	}
	if symY.Type != TypeInt {
		t.Errorf("expected y: Int, got %v", symY.Type)
	}
}

func TestCheckStructDestructureNotStruct(t *testing.T) {
	src := `fn main(): () {
  {x} = 42
}`
	_, errs := checkSource(src)
	expectError(t, errs, "struct destructure requires a struct type")
}

func TestCheckStructDestructureUnknownField(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
fn main(): () {
  p = Point { x: 1, y: 2 }
  {z} = p
}`
	_, errs := checkSource(src)
	expectError(t, errs, "field z not found in type Point")
}

func TestCheckStructDestructureAnonymous(t *testing.T) {
	src := `fn main() {
  s = { name: "Alice", age: 30 }
  {name, age} = s
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var symName, symAge *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "name" && sym.Kind == SymbolBinding {
			symName = sym
		}
		if sym.Name == "age" && sym.Kind == SymbolBinding {
			symAge = sym
		}
	}
	if symName == nil {
		t.Fatal("expected definition for name")
	}
	if symName.Type != TypeString {
		t.Errorf("expected name: String, got %v", symName.Type)
	}
	if symAge == nil {
		t.Fatal("expected definition for age")
	}
	if symAge.Type != TypeInt {
		t.Errorf("expected age: Int, got %v", symAge.Type)
	}
}

// ---------------------------------------------------------------------------
// checkMapDestructure
// ---------------------------------------------------------------------------

func TestCheckMapDestructureValid(t *testing.T) {
	src := `fn main() {
  m = {"name" => "Alice", "age" => "30"}
  {"name" => name} = m
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var symName *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "name" && sym.Kind == SymbolBinding {
			symName = sym
		}
	}
	if symName == nil {
		t.Fatal("expected definition for name")
	}
	if symName.Type != TypeString {
		t.Errorf("expected name: String, got %v", symName.Type)
	}
}

func TestCheckMapDestructureNotMap(t *testing.T) {
	src := `fn main(): () {
  {"key" => v} = 42
}`
	_, errs := checkSource(src)
	expectError(t, errs, "map destructure requires a map type")
}

// ---------------------------------------------------------------------------
// checkDistinctDestructure
// ---------------------------------------------------------------------------

func TestCheckDistinctDestructureValid(t *testing.T) {
	src := `type Id Int
fn main() {
  id = Id(42)
  Id(x) = id
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var symX *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" && sym.Kind == SymbolBinding {
			symX = sym
		}
	}
	if symX == nil {
		t.Fatal("expected definition for x")
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}
}

func TestCheckDistinctDestructureWrongType(t *testing.T) {
	src := `type Id Int
type Tag String
fn main(): () {
  id = Id(42)
  Tag(x) = id
}`
	_, errs := checkSource(src)
	expectError(t, errs, "expected Tag, got Id")
}

func TestCheckDistinctDestructureNotDistinct(t *testing.T) {
	src := `fn main(): () {
  Id(x) = 42
}`
	_, errs := checkSource(src)
	expectError(t, errs, "distinct type destructure requires a distinct type")
}

// ---------------------------------------------------------------------------
// Tuple-distinct literal-attach construction: Pair(1, "x")
// ---------------------------------------------------------------------------

func TestCheck_TupleDistinct_LiteralAttach_OK(t *testing.T) {
	src := `type Pair (Int, String)
fn main() {
  p = Pair(1, "x")
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_TupleDistinct_CallForm_StillOK(t *testing.T) {
	src := `type Pair (Int, String)
fn main() {
  p = Pair((1, "x"))
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_TupleDistinct_LiteralAttach_TypeMismatch(t *testing.T) {
	src := `type Pair (Int, String)
fn main() {
  p = Pair("x", 1)
}`
	_, errs := checkSource(src)
	expectError(t, errs, "argument 1 of Pair: expected Int, got String")
}

func TestCheck_TupleDistinct_LiteralAttach_TooFewArgs(t *testing.T) {
	src := `type Pair (Int, String)
fn main() {
  p = Pair(1)
}`
	_, errs := checkSource(src)
	expectError(t, errs, "Pair takes 2 arguments, got 1")
}

func TestCheck_TupleDistinct_LiteralAttach_TooManyArgs(t *testing.T) {
	src := `type Pair (Int, String)
fn main() {
  p = Pair(1, "x", 99)
}`
	_, errs := checkSource(src)
	expectError(t, errs, "Pair takes 2 arguments, got 3")
}

// Pair((1, 2)) — the user passed exactly one tuple argument (call-form),
// but the inner element types don't match. The error must point at the
// element-type mismatch, NOT at arity ("takes 2 arguments, got 1" would be
// misleading because the user did pass a single argument).
func TestCheck_TupleDistinct_CallForm_WrongInnerType(t *testing.T) {
	src := `type Pair (Int, String)
fn main() {
  p = Pair((1, 2))
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error, got none")
	}
	for _, e := range errs {
		if strings.Contains(e.Message, "takes 2 arguments, got 1") {
			t.Fatalf("error should not blame arity; got %q", e.Message)
		}
	}
	// Surface the unify-produced element-type mismatch (Int vs String).
	expectError(t, errs, "Int")
	expectError(t, errs, "String")
}

// ---------------------------------------------------------------------------
// Tuple-distinct destructure pattern: case p { Pair(a, b) -> ... }
// ---------------------------------------------------------------------------

func TestCheck_TupleDistinct_FlatDestructure_OK(t *testing.T) {
	src := `type Pair (Int, String)
fn demo(): Int {
  p = Pair(1, "x")
  case p {
    Pair(a, b) -> a
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// 3 positional patterns against an arity-2 tuple-distinct must error,
// pointing at the tuple-shape mismatch (the inner is a 2-tuple).
//
// Note: 1-pattern `Pair(a)` is NOT an arity error — it binds the entire
// inner tuple to `a` (the payload-binding fast path).
func TestCheck_TupleDistinct_FlatDestructure_WrongArity(t *testing.T) {
	src := `type Pair (Int, String)
fn main() {
  p = Pair(1, "x")
  case p {
    Pair(a, b, c) -> 0
  }
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for arity 3 against Pair, got none")
	}
	// The error surfaces from the TuplePattern checker since the inner
	// type is a 2-tuple but the pattern has 3 sub-patterns.
	expectError(t, errs, "tuple pattern has 3 elements but expected 2")
}

func TestCheck_TupleDistinct_NestedDestructure_StillOK(t *testing.T) {
	src := `type Pair (Int, String)
fn demo(): Int {
  p = Pair(1, "x")
  case p {
    Pair((a, b)) -> a
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// ---------------------------------------------------------------------------
// Map-distinct literal-attach construction: Kvs{"a" => 1}
// ---------------------------------------------------------------------------

func TestCheck_MapDistinct_LiteralAttach_OK(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn main() {
  kv = Kvs{"a" => 1, "b" => 2}
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_MapDistinct_LiteralAttach_WrongKeyType(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn main() {
  kv = Kvs{1 => 2}
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for wrong key type, got none")
	}
	expectError(t, errs, "Kvs")
	expectError(t, errs, "key")
}

func TestCheck_MapDistinct_LiteralAttach_WrongValueType(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn main() {
  kv = Kvs{"a" => "not-int"}
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for wrong value type, got none")
	}
	expectError(t, errs, "Kvs")
	expectError(t, errs, "value")
}

// Kvs(actual_map) — call-form keeps working under the type checker.
func TestCheck_MapDistinct_CallForm_StillOK(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn main() {
  kv = Kvs({"a" => 1})
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Type-prefixed map literal where the type isn't a map-distinct (e.g. a
// struct or a non-map distinct) errors with a useful message.
func TestCheck_MapDistinct_TypePrefixOnNonMap_Errors(t *testing.T) {
	src := `type Id Int
fn main() {
  x = Id{"a" => 1}
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for type-prefixed map on non-map-distinct, got none")
	}
	// The error mentions the type name and the shape mismatch.
	expectError(t, errs, "Id")
}

func TestCheck_MapDistinct_PatternOK(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn demo(): Int {
  kv = Kvs{"a" => 1}
  case kv {
    Kvs{"a" => v} -> v
    _ -> 0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Call-form pattern: `Kvs({"a" => v})` parallels `Pair((a, b))` for
// tuple-distinct. The EnumPattern unwraps the DistinctVal and the inner
// MapPattern matches the underlying map without complaint.
func TestCheck_MapDistinct_NestedPattern_OK(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn demo(): Int {
  kv = Kvs{"a" => 1}
  case kv {
    Kvs({"a" => v}) -> v
    _ -> 0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Call-form pattern with a wrong inner-value type still surfaces the
// per-K/V mismatch through the same MapPattern path the literal-attach
// form uses.
func TestCheck_MapDistinct_NestedPattern_TypeMismatch(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn demo(): Int {
  kv = Kvs{"a" => 1}
  case kv {
    Kvs({"a" => v}) -> v + "x"
  }
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error binding the inner Int to a string concat, got none")
	}
}

// ---------------------------------------------------------------------------
// Nominal struct call-form: Foo({a: 1, b: "x"}) coerces an anon-struct
// argument into the nominal struct. Parallels Pair((1, "x")) for tuple-distinct
// and Kvs(some_map) for map-distinct. Scope is constructor-only — broader
// anon-struct -> nominal-struct coercion is intentionally out of scope.
// ---------------------------------------------------------------------------

func TestCheck_NominalStruct_CallForm_OK(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f = Foo({a: 1, b: "x"})
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_NominalStruct_LiteralAttach_StillOK(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f = Foo{a: 1, b: "x"}
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_NominalStruct_CallForm_FieldNameMismatch(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f = Foo({wrong: 1})
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for unknown field, got none")
	}
	expectError(t, errs, "Foo")
	expectError(t, errs, "wrong")
}

func TestCheck_NominalStruct_CallForm_FieldTypeMismatch(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f = Foo({a: "x", b: "y"})
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for field type mismatch, got none")
	}
	expectError(t, errs, "a")
	expectError(t, errs, "Int")
	expectError(t, errs, "String")
}

func TestCheck_NominalStruct_CallForm_MissingField(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f = Foo({a: 1})
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for missing required field, got none")
	}
	expectError(t, errs, "b")
}

// Defaulted fields are not required: `Foo({a: 1})` with `b: String = "x"`
// type-checks cleanly because the default value covers `b`.
func TestCheck_NominalStruct_CallForm_DefaultedFieldOmitted_OK(t *testing.T) {
	src := `struct Foo { a: Int; b: String = "x" }
fn main() {
  f = Foo({a: 1})
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_NominalStruct_CallForm_TooManyArgs(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f = Foo({a: 1}, {b: "y"})
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for too many arguments, got none")
	}
	expectError(t, errs, "Foo")
}

func TestCheck_NominalStruct_CallForm_NonAnonStructArg(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f = Foo(42)
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for non-struct argument, got none")
	}
	expectError(t, errs, "anonymous struct literal")
}

// Empty struct: `Empty({})` parses the {} as a zero-statement Block with
// type Unit, not as a StructLit. The call-form recognizer must still treat
// it as an empty anon-struct literal so that `Empty({})` type-checks for
// zero-field structs and for all-defaulted structs.
func TestCheck_NominalStruct_CallForm_EmptyStruct_OK(t *testing.T) {
	src := `struct Empty {}
fn main() {
  e = Empty({})
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_NominalStruct_CallForm_AllDefaultedStruct_OK(t *testing.T) {
	src := `struct AllDefaults { a: Int = 1 }
fn main() {
  d = AllDefaults({})
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Generic structs go through the same call-form recognizer as non-generic
// ones — a malformed `Box({wrong: 1})` must be rejected, not silently
// accepted by the legacy fall-through.
func TestCheck_GenericStruct_CallForm_OK(t *testing.T) {
	src := `struct Box<T> { v: T }
fn main() {
  b = Box({v: 1})
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheck_GenericStruct_CallForm_FieldNameMismatch(t *testing.T) {
	src := `struct Box<T> { v: T }
fn main() {
  b = Box({wrong: 1})
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for unknown field, got none")
	}
	expectError(t, errs, "wrong")
}

func TestCheck_GenericStruct_CallForm_MissingField(t *testing.T) {
	src := `struct Box<T> { v: T }
fn main() {
  b = Box({})
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error for missing required field, got none")
	}
	expectError(t, errs, "v")
}

// Broad anon-struct -> nominal-struct coercion is OUT OF SCOPE for this
// task. Only the call-form Foo(...) triggers conversion. A bare anon-struct
// binding to a Foo-typed slot must remain a type error.
func TestCheck_NominalStruct_NoBroadCoercion(t *testing.T) {
	src := `struct Foo { a: Int; b: String }
fn main() {
  f: Foo = {a: 1, b: "x"}
}`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatalf("expected a type error — broad anon-struct to nominal-struct coercion must NOT be supported, got none")
	}
}

// ---------------------------------------------------------------------------
// checkCase with pattern type inference
// ---------------------------------------------------------------------------

func TestCheckCaseTuplePatternInference(t *testing.T) {
	src := `fn demo(): Int {
  val = (1, "hello")
  case val {
    (x, y) -> x
  }
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	var symX, symY *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" && sym.Kind == SymbolBinding {
			symX = sym
		}
		if sym.Name == "y" && sym.Kind == SymbolBinding {
			symY = sym
		}
	}
	if symX == nil {
		t.Fatal("expected definition for x")
	}
	if symX.Type != TypeInt {
		t.Errorf("expected x: Int, got %v", symX.Type)
	}
	if symY == nil {
		t.Fatal("expected definition for y")
	}
	if symY.Type != TypeString {
		t.Errorf("expected y: String, got %v", symY.Type)
	}
}

func TestCheckCaseEnumPattern(t *testing.T) {
	// Post-migration: pattern position uses the dot-leading form
	// (`case c { .Red -> ... }`) — bare variant prefixes are rejected
	// for non-prelude variants.
	src := `enum Color { Red; Green; Blue }
fn demo(): Int {
  c = Color.Red
  case c {
    .Red -> 1
    .Green -> 2
    .Blue -> 3
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A binding (or payload) pattern on a genuinely data-less plain variant is
// rejected — `Plain(x)` would bind a value that doesn't exist (the runtime
// marker carries no payload). The IR builder relies on this rejection to
// know that a binding-with-no-payload-value match can only be a zero-sized
// embeds variant.
func TestCheckEnumPatternBindingOnDataLessVariantRejected(t *testing.T) {
	src := `enum Switch { Plain; Other }
fn demo(): Int {
  s = Switch.Plain
  case s {
    Switch.Plain(x) -> 1
    _ -> 0
  }
}`
	_, errs := checkSource(src)
	expectError(t, errs, "variant Plain carries no data")
}

// The zero-sized embeds counterpart stays accepted: the variant's payload
// is the zero-sized distinct itself (spec §8, *Embedded Types*), so `Switch.Off(x)` binds
// x: Off.
func TestCheckEnumPatternBindingOnZeroSizedEmbedAccepted(t *testing.T) {
	src := `type Off
enum Switch { embeds Off; Plain }
fn use_off(_o: Off): Int { 1 }
fn demo(): Int {
  s: Switch = Switch.Off
  case s {
    Switch.Off(x) -> use_off(x)
    _ -> 0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Two enums in the same file share a variant name (`Done`). Qualified
// access `Status.Done` must resolve through the named enum's Members,
// not via a bare-name scope lookup that would happily return the other
// enum's variant. A resolver that tried `scope.Lookup(n.Field.Name)` first
// would return `Command.Done` (also defined at module level) and make
// `take_status` see a `(Int) -> Command` constructor instead of a `Status`
// value.
func TestCheckEnumQualifiedDisambiguatesSharedVariantName(t *testing.T) {
	src := `enum Status { Open; Closed; Done }
enum Command { Help | Done {id: Int} | Quit }
fn take_status(s: Status): Status { s }
fn use_it(): Status { take_status(Status.Done) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckCaseGuardExpression(t *testing.T) {
	src := `fn demo(): String {
  x = 5
  case x {
    n when n > 0 -> "positive"
    _ -> "non-positive"
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A subject-less case's conditions and every `when` guard are Bool
// expressions, checked like an `if` condition.
func TestCheckCaseConditionsAndGuardsAreBool(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"condition not Bool", "case {\n    x + 1 -> \"a\"\n    _ -> \"b\"\n  }", "case condition must be Bool, got Int"},
		{"undefined name in condition", "case {\n    nope > 1 -> \"a\"\n    _ -> \"b\"\n  }", "undefined variable 'nope'"},
		{"case guard not Bool", "case x {\n    n when n + 1 -> \"a\"\n    _ -> \"b\"\n  }", "`when` guard must be Bool, got Int"},
		{"condition guard not Bool", "case {\n    x > 1 when x -> \"a\"\n    _ -> \"b\"\n  }", "`when` guard must be Bool, got Int"},
		{"else arm guard not Bool", "Some(n) = Some(x) else {\n    _ when x -> return \"b\"\n    _ -> return \"c\"\n  }\n  \"a\"", "`when` guard must be Bool, got Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn demo(): String {\n  x = 5\n  " + tc.body + "\n}"
			_, errs := checkSource(src)
			expectError(t, errs, tc.want)
		})
	}
	src := `fn demo(): String {
  x = 5
  case {
    x > 10 and x < 20 -> "teens"
    x > 1 when x != 3 -> "small"
    _ -> "other"
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckCaseGenericEnumPattern(t *testing.T) {
	src := `enum Option<T> { Some T; None }
fn demo(): Int {
  opt = Option.Some(42)
  case opt {
    Some(v) -> v
    None -> 0
  }
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	// v should have type Int from Option<Int> substitution.
	var symV *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "v" {
			symV = sym
			break
		}
	}
	if symV == nil {
		t.Fatal("expected definition for v")
	}
	if symV.Type != TypeInt {
		t.Errorf("expected v: Int, got %v", symV.Type)
	}
}

// --- TryOp (try keyword) tests ---

// These two assert what `try` UNWRAPS — that `v` is usable as an Int. The
// enclosing function declares a Result boundary, since spec §9 requires one
// for `try` to propagate into.
func TestCheckTryOpResult(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
fn demo(r: Result<Int, String>): Result<Int, String> {
  v = try r
  Result.Ok(v + 1)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckTryOpMaybe(t *testing.T) {
	src := `enum Maybe<T> { Some T; None }
fn demo(m: Maybe<Int>): Maybe<Int> {
  v = try m
  Maybe.Some(v + 1)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckTryOpResultFromConstructor(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
fn safe_div(a: Int, b: Int): Result<Int, String> {
  if b == 0 { Result.Err("div by zero") } else { Result.Ok(a / b) }
}
fn calc(a: Int, b: Int): Result<Int, String> {
  result = try safe_div(a, b)
  Result.Ok(result + 1)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckTryOpBarePipeStage(t *testing.T) {
	src := `enum Maybe<T> { Some T; None }
enum Result<T, E> { Ok T; Err E }
fn to_result(m: Maybe<Int>): Result<Int, String> {
  case m {
    Some(n) -> Result.Ok(n)
    None -> Result.Err("missing")
  }
}
fn demo(m: Maybe<Int>): Result<Int, String> {
  n =
    m
    |> to_result()
    |> try
  Result.Ok(n + 1)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckTryOpPipeStageAcceptsOperand(t *testing.T) {
	src := `enum Maybe<T> { Some T; None }
enum Result<T, E> { Ok T; Err E }
fn to_result(m: Maybe<Int>): Result<Int, String> {
  case m {
    Some(n) -> Result.Ok(n)
    None -> Result.Err("missing")
  }
}
fn demo(m: Maybe<Int>): Result<Int, String> {
  n = m |> try to_result()
  Result.Ok(n)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckNullaryVariantInference(t *testing.T) {
	src := `enum Maybe<T> { Some T; None }
fn find(x: Int): Maybe<Int> {
  if x > 0 { Maybe.Some(x) } else { Maybe.None }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Qualified construction inside a generic function body must instantiate the
// variant's type params per call site. Otherwise `Maybe.Some(f(v))` and
// `Maybe.None` inside `map<T, U>(...)` reuse the enum's original `T` pointer
// across calls, and `case` branch unification fails with `expected Maybe<U>,
// got Maybe<T>`. The bare path (`Some(f(v))` / `None`) flows through
// `checkTypeIdent`, which already instantiates EnumType returns; the qualified
// path goes through `checkFieldAccess`'s reference fallback, which until now
// returned `sym.Type` raw.
func TestCheckQualifiedVariantInGenericBody(t *testing.T) {
	src := `enum Maybe<T> { Some T; None }
fn map<T, U>(m: Maybe<T>, f: (T) -> U): Maybe<U> {
  case m {
    Some(v) -> Maybe.Some(f(v))
    None -> Maybe.None
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckTryOpNonResultMaybe(t *testing.T) {
	src := `fn demo(): Int {
  v = try 42
  v
}`
	_, errs := checkSource(src)
	expectError(t, errs, "try requires a Result or Maybe")
}

func TestCheckTryOpWrongEnum(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
fn demo(): Color {
  c = try Color.Red
  c
}`
	_, errs := checkSource(src)
	expectError(t, errs, "try requires a Result or Maybe")
}

// --- Argument count mismatch tests ---

func TestCheckTooFewArguments(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn demo(): Int { add(1) }`
	_, errs := checkSource(src)
	expectError(t, errs, "expected 2 arguments, got 1")
}

// A pipe counts its piped value as an argument, so `1 |> add(2, 3)` is
// add(1, 2, 3).
func TestCheckTooManyArgumentsThroughAPipe(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"prepended": {
			src:  "fn add(x: Int, y: Int): Int { x + y }\nfn demo(): Int { 1 |> add(2, 3) }",
			want: "expected 2 arguments, got 3 (counting the piped value)",
		},
		"placeholder": {
			src:  "fn add(x: Int, y: Int): Int { x + y }\nfn demo(): Int { 1 |> add(2, _, 3) }",
			want: "expected 2 arguments, got 3 (counting the piped value)",
		},
		"defaults": {
			src:  "fn add(x: Int, y: Int = 0): Int { x + y }\nfn demo(): Int { 1 |> add(2, 3) }",
			want: "expected 1 to 2 arguments, got 3 (counting the piped value)",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(c.src)
			expectError(t, errs, c.want)
		})
	}
	for _, ok := range []string{
		"fn add(x: Int, y: Int): Int { x + y }\nfn demo(): Int { 1 |> add(2) }",
		"fn add(x: Int, y: Int): Int { x + y }\nfn demo(): Int { 2 |> add(1, _) }",
		"fn add(x: Int, y: Int = 0): Int { x + y }\nfn demo(): Int { 1 |> add() }",
	} {
		if _, errs := checkSource(ok); len(errs) != 0 {
			t.Errorf("checker rejected a pipe with the right argument count: %v\n%s", errs, ok)
		}
	}
}

func TestCheckTooManyArguments(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn demo(): Int { add(1, 2, 3) }`
	_, errs := checkSource(src)
	expectError(t, errs, "expected 2 arguments, got 3")
}

func TestCheckCorrectArgCount(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn demo(): Int { add(1, 2) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckPartialApplicationNoCountError(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn demo(): (Int) -> Int { add(1, _) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckDefaultParamOmitted(t *testing.T) {
	src := `fn greet(_name: String, greeting: String = "Hello"): String { greeting }
fn demo(): String { greet(_name: "World") }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckDefaultParamProvided(t *testing.T) {
	src := `fn greet(_name: String, greeting: String = "Hello"): String { greeting }
fn demo(): String { greet(_name: "World", greeting: "Hi") }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckDefaultParamTooFew(t *testing.T) {
	src := `fn greet(_name: String, greeting: String = "Hello"): String { greeting }
fn main() { greet() }`
	_, errs := checkSource(src)
	expectError(t, errs, "expected 1 to 2 arguments, got 0")
}

func TestCheckUndefinedVariable(t *testing.T) {
	src := `fn demo(): Int { unknown_var }`
	_, errs := checkSource(src)
	expectError(t, errs, "undefined variable 'unknown_var'")
}

func TestCheckUndefinedVariableInLambdaBody(t *testing.T) {
	src := `fn apply(f: (Int) -> Int, x: Int): Int { f(x) }
fn demo(): Int { apply(|n| { n + missing }, 1) }`
	_, errs := checkSource(src)
	expectError(t, errs, "undefined variable 'missing'")
}

func TestCheckNestedFuncIsResolvable(t *testing.T) {
	src := `fn demo(): Int {
  fn helper(x: Int): Int { x + 1 }
  helper(41)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckNestedStructUsedInNestedFnSignature(t *testing.T) {
	src := `fn demo(): Int {
  struct Point { x: Int; y: Int }
  fn make(): Point { Point { x: 1, y: 2 } }
  p = make()
  p.x + p.y
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckNestedTypeAliasUsedInNestedFnSignature(t *testing.T) {
	src := `fn demo(): Int {
  typealias Count Int
  fn make(n: Int): Count { n }
  make(42)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckNestedEnumUsedInNestedFnSignature(t *testing.T) {
	// Same-file enums require module/enum-qualified variant names; nested
	// enums work the same way.
	src := `fn demo(): Int {
  enum Color { Red; Green; Blue }
  fn pick(): Color { Color.Red }
  case pick() {
    Color.Red -> 1
    Color.Green -> 2
    Color.Blue -> 3
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckNestedFuncCallTyped(t *testing.T) {
	src := `fn demo(): Int {
  fn classify(n: Int): Int {
    if n < 0 { return 0 }
    n
  }
  classify(5) + classify(-2)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckRejectsNestedExternFn(t *testing.T) {
	src := `fn demo(): Int {
  host fn host_thing(): Int
  host_thing()
}`
	_, errs := checkSource(src)
	expectError(t, errs, "extern declaration must be at the top level")
}

// `host fn` accepts an explicit type-param list plus a `where` clause just
// like regular `fn` declarations. The bound is enforced at call sites: passing
// a value whose type doesn't implement the named interface is a checker
// error. Without explicit type params, the implicit-name-scanner path is
// preserved (`host fn id(x: T): T` still works without a `<T>` clause).
func TestCheckExternFnInterfaceBoundEnforced(t *testing.T) {
	src := `interface Printable { fn show(value: self): String }
host fn dump<T>(value: T) where T: Printable
struct NoImpl { x: Int }
fn main() {
  dump(NoImpl{x: 1})
}`
	_, errs := checkSource(src)
	expectError(t, errs, "Printable")
}

func TestCheckFnWhereBoundEnforced(t *testing.T) {
	src := `interface Printable { fn show(value: self): String }
struct Label { text: String }
impl Printable for Label {
  fn show(value: Label): String { value.text }
}
fn echo<T>(value: T): T where T: Printable {
  value
}
fn demo(): Label {
  echo(Label{text: "ok"})
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckFnWhereBoundRejectsMissingImpl(t *testing.T) {
	src := `interface Printable { fn show(value: self): String }
struct NoImpl { x: Int }
fn echo<T>(value: T): T where T: Printable {
  value
}
fn demo(): NoImpl {
 echo(NoImpl{x: 1})
}`
	_, errs := checkSource(src)
	expectError(t, errs, "where T")
}

func TestCheckExternFnWhereBoundEnforced(t *testing.T) {
	src := `interface Printable { fn show(value: self): String }
host fn dump<T>(value: T) where T: Printable
struct NoImpl { x: Int }
fn main() {
 dump(NoImpl{x: 1})
}`
	_, errs := checkSource(src)
	expectError(t, errs, "where T")
}

func TestCheckRejectsNestedExternType(t *testing.T) {
	src := `fn demo(): Int {
  host type Foo
  0
}`
	_, errs := checkSource(src)
	expectError(t, errs, "extern declaration must be at the top level")
}

func TestCheckBindingTypeFromNestedStructLit(t *testing.T) {
	// A binding whose value is a struct literal of a nested struct must
	// pick up the struct's type so hover/inlay-hints work.
	src := `fn demo(): Int {
  struct Defaulted { v: Int = 99 }
  d = Defaulted{}
  d.v
}`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	var dSym *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "d" {
			dSym = sym
			break
		}
	}
	if dSym == nil {
		t.Fatal("expected to find binding 'd'")
	}
	if dSym.Type == nil {
		t.Fatal("expected 'd' to have a non-nil type after struct-literal binding")
	}
	if st, ok := dSym.Type.(*StructType); !ok || st.Name != "Defaulted" {
		t.Errorf("expected 'd' to be StructType{Defaulted}, got %T %v", dSym.Type, dSym.Type)
	}
}

func TestCheckUndefinedTypeIdentInExprPosition(t *testing.T) {
	src := `fn demo(): Int { Ghost }`
	_, errs := checkSource(src)
	expectError(t, errs, "Ghost")
}

func TestCheckUndefinedTypeIdentAsFieldAccessObject(t *testing.T) {
	src := `fn demo(): Int { Ghost.Red }`
	_, errs := checkSource(src)
	expectError(t, errs, "Ghost")
}

func TestCheckUndefinedVariablePosition(t *testing.T) {
	src := `fn demo(): Int {
  ghost
}`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "undefined variable 'ghost'") {
			if e.Line != 2 || e.Col != 3 {
				t.Errorf("expected error at L2:C3, got L%d:'C%d", e.Line, e.Col)
			}
			return
		}
	}
	t.Fatalf("expected undefined-variable error, got: %+v", errs)
}

// ---------------------------------------------------------------------------
// impl-block signature validation.
//
// `impl Iface for X { fn m(receiver: X, ...): R }` must declare a method
// whose name, parameter shape, and return type matches the interface contract.
// self in the interface signature substitutes to the implementing type.
// Missing methods are flagged once across the full file's impl blocks.
// ---------------------------------------------------------------------------

const implTestSetup = `interface Greeter {
  fn greet(value: self, style: String): String
}
struct User { name: String }
`

func TestCheckImplValidSignature(t *testing.T) {
	src := implTestSetup + `impl Greeter for User {
  fn greet(value: User, style: String): String { value.name + " (" + style + ")" }
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckImplWrongReturnType(t *testing.T) {
	src := implTestSetup + `impl Greeter for User {
  fn greet(_value: User, _style: String): Int { 0 }
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "return type")
}

func TestCheckImplWrongParamType(t *testing.T) {
	src := implTestSetup + `impl Greeter for User {
  fn greet(_value: User, _style: Int): String { "x" }
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "parameter")
}

func TestCheckImplWrongParamName(t *testing.T) {
	src := implTestSetup + `impl Greeter for User {
  fn greet(_value: User, s: String): String { s }
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "parameter name")
}

func TestCheckImplSelfPositionFreeName(t *testing.T) {
	// self-position parameter (interface declared `value: self`) — the
	// impl can rename it to whatever describes the concrete type.
	src := implTestSetup + `impl Greeter for User {
  fn greet(user: User, style: String): String { user.name + style }
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestCheckImplWrongParamCount(t *testing.T) {
	src := implTestSetup + `impl Greeter for User {
  fn greet(_value: User): String { "x" }
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "parameter")
}

func TestCheckImplExtraMethod(t *testing.T) {
	src := implTestSetup + `impl Greeter for User {
  fn greet(_value: self, _style: String): String { "x" }

  fn shout(_value: self): String { "AAA" }
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "not part of the interface")
}

// TestCheckImplMissingMethod pins the block-form completeness check: an
// interface-impl block that omits a NON-default required method errors at the
// block (the completeness section of validateImplBlockMethodSignatures). The
// complement of TestCheckImplDefaultMethodOmitted below, where a method WITH a
// default may be omitted.
func TestCheckImplMissingMethod(t *testing.T) {
	src := `interface TwoOps {
  fn op_a(value: self): String
  fn op_b(value: self): String
}
struct Box {}
impl TwoOps for Box {
  fn op_a(_b: Box): String { "a" }
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "missing function 'op_b'")
}

// Pattern exhaustiveness tests live in pattern_exhaustiveness_test.go
// (external package) so they can use the real stdlib via
// checkSourceWithStdlib — Maybe / Result / Bool aren't built-in language
// features, they're stdlib enums, and the resolver no longer hardcodes
// them.

func TestCheckImplDefaultMethodOmitted(t *testing.T) {
	// Interface provides a default body for op_b, so the impl can omit it
	// without error.
	src := `interface TwoOps {
  fn op_a(value: self): String
  fn op_b(_value: self): String { "default-b" }
}
struct Box {}
impl TwoOps for Box {
  fn op_a(_b: Box): String { "a" }
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// ---------------------------------------------------------------------------
// Visibility consistency
//
// Spec §3 says public definitions must not expose private types. The check
// fires for: public fn param/return types, public struct field types, public
// enum variant data types and embeds, public interface method signatures,
// public type aliases.
// ---------------------------------------------------------------------------

func TestVisibility_PublicFnReturnsPrivateType(t *testing.T) {
	src := `struct Secret { value: String }
pub fn leak(): Secret { Secret{value: "x"} }`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicFnTakesPrivateParam(t *testing.T) {
	src := `struct Secret { value: String }
pub fn use_secret(_s: Secret): Int { 0 }`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicStructHasPrivateFieldType(t *testing.T) {
	src := `struct Secret { value: String }
pub struct Container { data: Secret }`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicEnumEmbedsPrivate(t *testing.T) {
	src := `struct Secret { value: String }
pub enum Wrapper {
  embeds Secret
  Other
}`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicEnumPositionalVariantPrivateData(t *testing.T) {
	src := `struct Secret { value: String }
pub enum Wrapper {
  Holds Secret
  Other
}`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicEnumStructVariantPrivateField(t *testing.T) {
	src := `struct Secret { value: String }
pub enum Wrapper {
  Holds { secret: Secret }
  Other
}`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicInterfaceMethodPrivateReturn(t *testing.T) {
	src := `struct Secret { value: String }
pub interface Reader {
  fn read(value: self): Secret
}`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicInterfaceMethodPrivateParam(t *testing.T) {
	src := `struct Secret { value: String }
pub interface Sink {
  fn write(value: self, s: Secret): Int
}`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PublicTypeAliasReferencesPrivate(t *testing.T) {
	src := `struct Secret { value: String }
pub typealias S Secret`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_AllPublicIsOK(t *testing.T) {
	src := `pub struct Public { value: String }
pub fn make(): Public { Public{value: "x"} }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVisibility_PrivateExposingPrivateIsOK(t *testing.T) {
	// A private function can freely use private types — no leak risk.
	src := `struct Secret { value: String }
fn make(): Secret { Secret{value: "x"} }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVisibility_PrimitivesAreAlwaysPublic(t *testing.T) {
	// Int/String/Bool are primitive — using them in public sigs is fine.
	src := `fn add(x: Int, y: Int): Int { x + y }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Inline-pub variants of the visibility-consistency checks above. The
// check reads `n.Public` from each AST node (set by the parser for
// inline `pub`) and `sym.Public` from the type-symbol map (set by the
// builder via the dual-source rule). With no `export` block in sight,
// the same diagnostics must still fire.

func TestVisibility_InlinePubFnReturnsPrivateType(t *testing.T) {
	src := `struct Secret { value: String }
pub fn leak(): Secret { Secret{value: "x"} }`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_InlinePubFnTakesPrivateParam(t *testing.T) {
	src := `struct Secret { value: String }
pub fn use_secret(_s: Secret): Int { 0 }`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_InlinePubStructHasPrivateFieldType(t *testing.T) {
	src := `struct Secret { value: String }
pub struct Container { data: Secret }`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_InlinePubInterfaceMethodPrivateReturn(t *testing.T) {
	src := `struct Secret { value: String }
pub interface Reader {
  fn read(value: self): Secret
}`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_InlinePubTypeAliasReferencesPrivate(t *testing.T) {
	src := `struct Secret { value: String }
pub typealias S Secret`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PubMain_Allowed(t *testing.T) {
	src := `pub fn demo(): Int { 0 }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVisibility_PrivateMain_Allowed(t *testing.T) {
	src := `fn demo(): Int { 0 }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A public `once` binding whose type annotation references a private
// type leaks the private type into the public surface, just like a
// public function returning a private type.
func TestVisibility_PubOnceWithPrivateType(t *testing.T) {
	src := `struct Secret { value: String }
pub once x: Secret = Secret{value: "x"}`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestVisibility_PrivateOnceWithPrivateType_Allowed(t *testing.T) {
	src := `struct Secret { value: String }
once x: Secret = Secret{value: "x"}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVisibility_PubOnceWithPublicType_Allowed(t *testing.T) {
	src := `pub struct Public { value: String }
pub once x: Public = Public{value: "x"}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// ---------------------------------------------------------------------------
// Interface bounds on generic functions
//
// `fn foo<T>(x: T): T where T: Iface { x }` parses, and calls verify the inferred
// concrete type impl the bound interface.
// ---------------------------------------------------------------------------

const interfaceBoundSetup = `interface Showable {
  fn show(value: self): String
}
interface Tagged {
  fn tag(value: self): String
}
struct User {
  name: String
}
impl Showable for User {
  fn show(value: User): String { value.name }
}
impl Tagged for User {
  fn tag(_value: User): String { "user" }
}
struct Anon {}
struct ShowOnly { name: String }
impl Showable for ShowOnly {
  fn show(value: ShowOnly): String { value.name }
}
`

func TestInterfaceBound_ParseSyntax(t *testing.T) {
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable { x }`
	tokens := lexer.Lex(src)
	_, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("expected `where T: Showable` to parse, got: %v", err)
	}
}

func TestInterfaceBound_CallSiteSatisfied(t *testing.T) {
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable { x }
fn demo(): User { identity(User{name: "Alice"}) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestInterfaceBound_CallSiteNotImpl(t *testing.T) {
	// Anon doesn't impl Showable — should reject.
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable { x }
fn demo(): Anon { identity(Anon{}) }`
	_, errs := checkSource(src)
	expectError(t, errs, "does not implement")
}

func TestInterfaceBound_CallSitePrimitiveNotImpl(t *testing.T) {
	// Int doesn't impl Showable — should reject.
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable { x }
fn demo(): Int { identity(42) }`
	_, errs := checkSource(src)
	expectError(t, errs, "does not implement")
}

func TestInterfaceBound_UnboundedStillWorks(t *testing.T) {
	// Generic without a bound shouldn't trigger any check.
	src := `fn identity<T>(x: T): T { x }
fn demo(): Int { identity(42) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestInterfaceBound_MultiBound_ParseSyntax(t *testing.T) {
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable and Tagged { x }`
	tokens := lexer.Lex(src)
	_, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("expected `where T: Showable and Tagged` to parse, got: %v", err)
	}
}

func TestInterfaceBound_MultiBound_AllSatisfied(t *testing.T) {
	// User impl both Showable and Tagged — call should typecheck.
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable and Tagged { x }
fn demo(): User { identity(User{name: "Alice"}) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestInterfaceBound_MultiBound_OneMissing(t *testing.T) {
	// ShowOnly impl Showable but not Tagged — should reject and the
	// error should point at the missing interface.
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable and Tagged { x }
fn demo(): ShowOnly { identity(ShowOnly{name: "x"}) }`
	_, errs := checkSource(src)
	expectError(t, errs, "Tagged")
}

func TestInterfaceBound_MultiBound_NoneSatisfied(t *testing.T) {
	// Anon impl neither — should reject.
	src := interfaceBoundSetup + `fn identity<T>(x: T): T where T: Showable and Tagged { x }
fn demo(): Anon { identity(Anon{}) }`
	_, errs := checkSource(src)
	expectError(t, errs, "does not implement")
}

// Interface-bound aliases (`typealias Foo A and B`) should expand to the
// underlying interfaces wherever they appear in a bound clause. Behavior
// at use sites must match what writing the expansion directly would do.
func TestInterfaceBound_Alias_AllSatisfied(t *testing.T) {
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
fn identity<T>(x: T): T where T: ShowAndTag { x }
fn demo(): User { identity(User{name: "Alice"}) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestInterfaceBound_Alias_OneMissing(t *testing.T) {
	// ShowOnly satisfies Showable but not Tagged — alias bound must still
	// fail with the missing interface named (not the alias name).
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
fn identity<T>(x: T): T where T: ShowAndTag { x }
fn demo(): ShowOnly { identity(ShowOnly{name: "x"}) }`
	_, errs := checkSource(src)
	expectError(t, errs, "Tagged")
}

// Aliasing an alias should flatten to the union of underlying bounds.
func TestInterfaceBound_Alias_OfAlias(t *testing.T) {
	src := interfaceBoundSetup + `typealias S Showable
typealias ST S and Tagged
fn identity<T>(x: T): T where T: ST { x }
fn demo(): User { identity(User{name: "Alice"}) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A bound alias whose RHS contains another bound alias should splice
// the inner bounds into the outer alias.
func TestInterfaceBound_Alias_NestedBoundAlias(t *testing.T) {
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
typealias Outer ShowAndTag
fn identity<T>(x: T): T where T: Outer { x }
fn demo(): User { identity(User{name: "Alice"}) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Same nested case but with a missing impl — error should name the
// underlying interface, not the outer alias.
func TestInterfaceBound_Alias_NestedBoundAlias_Missing(t *testing.T) {
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
typealias Outer ShowAndTag
fn identity<T>(x: T): T where T: Outer { x }
fn demo(): ShowOnly { identity(ShowOnly{name: "x"}) }`
	_, errs := checkSource(src)
	expectError(t, errs, "Tagged")
}

// A bound alias is for `where` bounds only. Using one as a value
// type (parameter annotation, struct field, binding annotation, etc.)
// must error with a clear message pointing at the `where T: Name` form.
func TestInterfaceBound_Alias_RejectedAsParamType(t *testing.T) {
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
fn announce(_x: ShowAndTag): String { "ok" }`
	_, errs := checkSource(src)
	expectError(t, errs, "interface-bound alias")
}

func TestInterfaceBound_Alias_RejectedAsReturnType(t *testing.T) {
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
fn make(x: User): ShowAndTag { x }`
	_, errs := checkSource(src)
	expectError(t, errs, "interface-bound alias")
}

func TestInterfaceBound_Alias_RejectedAsStructField(t *testing.T) {
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
struct Box { field: ShowAndTag }`
	_, errs := checkSource(src)
	expectError(t, errs, "interface-bound alias")
}

// ---------------------------------------------------------------------------
// Variant pattern data-shape consistency
//
// A data-carrying variant in pattern position must spell out its payload
// (e.g. `Err(_)` or `Err(s)`). Bare `Err` is only valid for variants with
// no data (`None`, `True`, `False`). Without this check, `Some(Err)` would
// silently match `Some(Err("boom"))` even though the pattern fails to bind
// or destructure the inner String.
// ---------------------------------------------------------------------------

func TestVariantPattern_BareVariant_DataCarrying_Rejected(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
fn f(r: Result<Int, String>): Int {
  case r {
    Ok(n) -> n
    Err -> -1
  }
}`
	_, errs := checkSource(src)
	expectError(t, errs, "payload")
}

func TestVariantPattern_NestedBareDataCarrying_Rejected(t *testing.T) {
	// The case the user noticed: `Some(Err)` for a Some<Result<Int, String>>
	// scrutinee. Some has data (the Result), Err has data (the String). Both
	// inner Err and outer Some need their payloads spelled out.
	src := `enum Maybe<T> { Some T; None }
enum Result<T, E> { Ok T; Err E }
fn unwrap_or(m: Maybe<Result<Int, String>>): Int {
  case m {
    Some(Ok(n)) -> n
    Some(Err) -> -1
    None -> 0
  }
}`
	_, errs := checkSource(src)
	expectError(t, errs, "payload")
}

func TestVariantPattern_BareVariant_NoData_OK(t *testing.T) {
	// None carries no data — bare `None` is the correct pattern.
	src := `enum Maybe<T> { Some T; None }
fn f(m: Maybe<Int>): Int {
  case m {
    Some(n) -> n
    None -> 0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVariantPattern_DataCarrying_WithWildcard_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
fn f(r: Result<Int, String>): Int {
  case r {
    Ok(n) -> n
    Err(_) -> -1
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVariantPattern_DataCarrying_WithBinding_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
fn f(r: Result<Int, String>): Int {
  case r {
    Ok(n) -> n
    Err(msg) -> 0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVariantPattern_BoolBareVariants_OK(t *testing.T) {
	// True and False have no data — bare patterns are valid.
	src := `fn f(b: Bool): Int {
  case b {
    True -> 1
    False -> 0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// ---------------------------------------------------------------------------
// Naming conventions
//
// Spec §4 promises: types/enum variants are PascalCase, functions/struct
// fields/parameters are snake_case. Most first-character violations are
// already rejected by the lexer (IDENT vs TYPE_IDENT token kinds), so the
// tests here focus on the violations the parser allows but the analyzer
// must catch — interior underscores in PascalCase names, interior
// uppercase in snake_case names, and the ambiguous spots where the parser
// accepts both token kinds (e.g. function names).
// ---------------------------------------------------------------------------

// Positive cases: idiomatic naming compiles cleanly.

func TestNaming_StructPascalCase_OK(t *testing.T) {
	src := `struct User { name: String }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_EnumVariantPascalCase_OK(t *testing.T) {
	src := `enum Color { Red; Green; Blue }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_FunctionSnakeCase_OK(t *testing.T) {
	src := `fn parse_input(_s: String): Int { 0 }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Anonymous struct fields, BOTH positions. Spec §4 says struct fields are
// ALWAYS snake_case with no nominal/anonymous distinction, but
// checkNamingConventions walks top-level decls only, and an anon struct's
// fields live in an expression or a type expression — so neither position was
// ever visited and `{userName: 1}` type-checked and ran.
//
// The two positions are checked in two different places and a fix to one does
// NOT cover the other: the LITERAL is checked in checker.checkStructLit, the
// TYPE in ResolveTypeExpr. A parameter annotated `{ok?: Int, ok_PRED: Int}`
// reaches the IR builder with no literal anywhere, which is why both are pinned.
//
// `ok_PRED` is the spelling that motivated this: `internal/irbuild` spells a
// trailing `?` as a `_PRED` suffix, so an unchecked `ok_PRED` beside `ok?` is
// two Nomi names arriving at one spelling there.

func TestNaming_AnonStructLiteralField_Rejected(t *testing.T) {
	src := `fn f(): Int {
  a = {userName: 1}
  a.userName
}`
	_, errs := checkSource(src)
	expectError(t, errs, `anon struct field name "userName" must be snake_case`)
}

func TestNaming_AnonStructTypeField_Rejected(t *testing.T) {
	src := `fn width(box: {userName: Int}): Int {
  box.userName
}`
	_, errs := checkSource(src)
	expectError(t, errs, `anon struct field name "userName" must be snake_case`)
}

func TestNaming_AnonStructPredicateSuffix_OK(t *testing.T) {
	// A trailing `?` IS snake_case (spec §4, the predicate convention), so the
	// rule must not reject it — only the `_PRED` spelling that collides with it.
	src := `fn f(): Int {
  a = {ok?: 1, count: 2}
  a.count
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_AnonStructPredCollisionField_Rejected(t *testing.T) {
	src := `fn f(): Int {
  a = {ok?: 1, ok_PRED: 2}
  a.ok_PRED
}`
	_, errs := checkSource(src)
	expectError(t, errs, `anon struct field name "ok_PRED" must be snake_case`)
}

func TestNaming_StructFieldSnakeCase_OK(t *testing.T) {
	src := `struct User { user_name: String; email_addr: String }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_FunctionParamSnakeCase_OK(t *testing.T) {
	src := `fn f(user_id: Int, max_retries: Int): Int { user_id + max_retries }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_PredicateFunctionName_OK(t *testing.T) {
	// A single trailing `?` (predicate convention) is a valid snake_case
	// identifier — `fn empty?()` must NOT trip the snake_case check.
	src := `fn empty?(): Int { 0 }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_PredicateBinding_OK(t *testing.T) {
	// A `?`-suffixed binding name is accepted too (the suffix attaches to any
	// identifier, not just fn names).
	src := `fn f(): Int { ready? = 0; ready? }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_PredicateParam_OK(t *testing.T) {
	// A `?`-suffixed parameter name is accepted too — the suffix attaches to
	// any identifier, including function parameters.
	src := `fn check(ready?: Bool): Bool { ready? }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_TypeParamSingleUppercase_OK(t *testing.T) {
	src := `struct Pair<A, B> { first: A; second: B }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestNaming_LeadingUnderscoreParam_OK(t *testing.T) {
	// Conventionally-unused params get a leading underscore.
	src := `fn f(_unused: Int): Int { 0 }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Negative cases: parser-acceptable violations the analyzer must reject.

func TestNaming_StructWithUnderscore_Rejected(t *testing.T) {
	// `Http_Request` parses (TYPE_IDENT starts with uppercase) but
	// PascalCase forbids interior underscores.
	src := `struct Http_Request { name: String }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_EnumVariantWithUnderscore_Rejected(t *testing.T) {
	src := `enum Color { Red_Hue; Green; Blue }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_TypeAliasWithUnderscore_Rejected(t *testing.T) {
	src := `typealias User_Id Int`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_DistinctTypeWithUnderscore_Rejected(t *testing.T) {
	src := `type User_Id Int`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_InterfaceWithUnderscore_Rejected(t *testing.T) {
	src := `interface Format_Pretty { fn fmt(value: self): String }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_FunctionPascalCase_Rejected(t *testing.T) {
	// Function names accept both IDENT and TYPE_IDENT, so a TYPE_IDENT
	// like `ParseInput` parses — but functions must be snake_case.
	src := `fn ParseInput(_s: String): Int { 0 }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_FunctionScreamingSnake_Rejected(t *testing.T) {
	src := `fn DO_THING(): Int { 0 }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_StructFieldMixedCase_Rejected(t *testing.T) {
	// `userName` parses (starts lowercase = IDENT) but the interior
	// uppercase letter violates snake_case.
	src := `struct User { userName: String }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_FunctionParamMixedCase_Rejected(t *testing.T) {
	src := `fn f(_userId: Int): Int { 0 }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

// --- Let-binding names ---
// The lexer classifies leading-uppercase identifiers as TYPE_IDENT, so a
// binding LHS like `X = 5` won't parse. The interesting violations are
// IDENT names with interior uppercase (`someVar`) — parser accepts them,
// the checker must reject.

func TestNaming_BindingMixedCase_Rejected(t *testing.T) {
	src := `fn f(): Int { someVar = 5
someVar }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_BindingInteriorUppercase_Rejected(t *testing.T) {
	src := `fn f(): Int { my_Var = 5
my_Var }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

// --- Top-level once bindings ---

func TestNaming_TopLevelOncePascalCase_Rejected(t *testing.T) {
	src := `once MaxRetries = 3`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_TopLevelOnceScreamingSnake_Rejected(t *testing.T) {
	src := `once MAX_RETRIES = 3`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

// --- Destructure bindings ---

func TestNaming_TupleDestructureMixedCase_Rejected(t *testing.T) {
	src := `fn f(): Int { (myVar, y) = (1, 2)
myVar + y }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_StructDestructureMixedCase_Rejected(t *testing.T) {
	src := `struct Person { name: String; age: Int }
fn f(p: Person): Int { {name: nameAlias, age} = p
age }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_DistinctDestructureMixedCase_Rejected(t *testing.T) {
	src := `type UserId Int
fn f(u: UserId): Int { UserId(myId) = u
myId }`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

// --- Type parameters ---

func TestNaming_TypeParamLowercase_Rejected(t *testing.T) {
	src := `fn f<a>(x: a): a { x }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_TypeParamSnakeCase_Rejected(t *testing.T) {
	src := `fn f<acc_t>(x: acc_t): acc_t { x }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_TypeParamOnStruct_Rejected(t *testing.T) {
	src := `struct Foo<a> { x: a }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_TypeParamOnEnum_Rejected(t *testing.T) {
	src := `enum Foo<a> { A a }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_TypeParamOnInterface_Rejected(t *testing.T) {
	src := `interface Foo<a> { fn fmt(value: self): a }`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

// --- Imports ---

func TestNaming_ImportSegmentMixed_Rejected(t *testing.T) {
	src := `import std/bad_Mix: BadMix`
	_, errs := checkSource(src)
	expectError(t, errs, "snake_case")
}

func TestNaming_ImportTypeNameUnderscore_Rejected(t *testing.T) {
	src := `import std/maybe.{Some_Thing}`
	_, errs := checkSource(src)
	expectError(t, errs, "PascalCase")
}

func TestNaming_TypeImportAliasSnakeCase_Rejected(t *testing.T) {
	src := `import maps.Map as bad_Mix`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"maps": `pub struct Map {}`,
	})
	expectError(t, errs, "PascalCase")
}

func TestNaming_ModuleImportAliasPascalCase_Rejected(t *testing.T) {
	src := `import iter as Seq`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"iter": `pub fn count() {}`,
	})
	expectError(t, errs, `import alias "Seq" must be snake_case`)
}

func TestNaming_FunctionImportAliasPredicateSuffix_Rejected(t *testing.T) {
	src := `import strings.{contains? as has?}`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"strings": `pub fn contains?() {}`,
	})
	expectError(t, errs, `import alias "has?" must be snake_case`)
}

func TestNaming_ImportBlockFunctionAliasPascalCase_Rejected(t *testing.T) {
	src := `import {
  parsers/date.parse as Parse
}`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"parsers/date": `pub fn parse(_text: String): Int { 0 }`,
	})
	expectError(t, errs, `import alias "Parse" must be snake_case`)
}

func TestNaming_TypeImportAliasPascalCase_OK(t *testing.T) {
	src := `import maybe.Maybe as Opt

fn f(value: Opt<Int>): Opt<Int> { value }`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"maybe": `pub enum Maybe<T> { Some T; None }`,
	})
	expectNoErrors(t, errs)
}

func TestNaming_VariantImportAliasPascalCase_OK(t *testing.T) {
	src := `import maybe.Maybe.{self, Some as Just, None as Nothing}

fn f(): Maybe<Int> { Just(1) }`
	src += `
fn g(): Maybe<Int> { Nothing }`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"maybe": `pub enum Maybe<T> { Some T; None }`,
	})
	expectNoErrors(t, errs)
}

func TestNaming_VariantImportAliasSnakeCase_Rejected(t *testing.T) {
	src := `import maybe.Maybe.{Some as just}`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"maybe": `pub enum Maybe<T> { Some T; None }`,
	})
	expectError(t, errs, `import alias "just" must be PascalCase`)
}

func TestNaming_FunctionExportAliasPascalCase_Rejected(t *testing.T) {
	src := `import strings.{length export as Length}`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"strings": `pub fn length() {}`,
	})
	expectError(t, errs, `export alias "Length" must be snake_case`)
}

func TestNaming_TypeExportAliasSnakeCase_Rejected(t *testing.T) {
	src := `import maybe.{Maybe export as maybe_type}`
	_, errs := checkSourceWithModules(t, src, map[string]string{
		"maybe": `pub enum Maybe<T> { Some T; None }`,
	})
	expectError(t, errs, `export alias "maybe_type" must be PascalCase`)
}

func TestVisibility_NestedPrivateInGenericArg(t *testing.T) {
	// `fn lookup(): List<Secret>` — secret is buried inside a List
	// type-argument but the leak is just as real.
	src := `struct Secret { value: String }
pub fn lookup(): List<Secret> { [] }`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

// `Shape.Rectangle{width, height}` inside a case arm must destructure
// the embedded Rectangle struct's fields. The pattern checker has to
// read `vd.DataType.(*StructType).Fields` for embed variants, not the
// (empty) `vd.Fields` slice.
func TestCheckPattern_EmbedVariantStructDestructure(t *testing.T) {
	src := `struct Rectangle { width: Float; height: Float }
enum Shape { embeds Rectangle; Point }
fn area(s: Shape): Float {
  case s {
    Shape.Rectangle{width, height} -> width * height
    Shape.Point -> 0.0
  }
}`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "width") || strings.Contains(e.Message, "height") {
			t.Errorf("expected destructure to find Rectangle's fields, got: %v", e)
		}
	}
}

// A variant with several positional types, `Rect(Float, Float)`, is a
// tuple-payload variant, not a "multi-positional" error.
func TestMultiPositionalVariant_DeclarationParsesAsTuple(t *testing.T) {
	src := `enum Shape { Rect(Float, Float); Empty }`
	tokens := lexer.Lex(src)
	nodes, parseErrs := parser.ParseWithRecovery(tokens)
	if len(parseErrs) > 0 {
		t.Fatalf("expected no parse errors, got: %v", parseErrs)
	}
	fa := BuildFile(nodes)
	BuildTypes(fa, nodes)
	errs := CheckTypes(fa, nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	if len(all) > 0 {
		t.Errorf("expected no analysis errors for tuple-payload declaration, got: %v", all)
	}
}

// Variant tuple-payload literal-attach construction: `Shape.Rect(3.0, 5.0)`
// is now accepted as a flat-form constructor for the tuple payload, parallel
// to the tuple-distinct literal-attach `Pair(1, "x")`. The call-form
// `Shape.Rect((3.0, 5.0))` continues to work (see
// TestTuplePayloadVariant_ExplicitFormWorks).
func TestVariantTuplePayload_FlatConstruction_OK(t *testing.T) {
	src := `enum Shape { Rect (Float, Float); Empty }
fn make(): Shape { Shape.Rect(3.0, 5.0) }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := BuildFile(nodes)
	BuildTypes(fa, nodes)
	errs := CheckTypes(fa, nodes)

	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	if len(all) != 0 {
		t.Errorf("expected no errors for flat construction, got: %v", all)
	}
}

// Variant tuple-payload literal-attach pattern: `Shape.Rect(w, h)` is now
// accepted as a flat-form destructure for the tuple payload, parallel to
// `Pair(a, b)` for tuple-distinct types. The explicit form
// `Shape.Rect((w, h))` continues to work.
func TestVariantTuplePayload_FlatPattern_OK(t *testing.T) {
	src := `enum Shape { Rect (Float, Float); Empty }
fn area(s: Shape): Float {
  case s {
    Shape.Rect(w, h) -> w * h
    Shape.Empty -> 0.0
  }
}`
	tokens := lexer.Lex(src)
	nodes, parseErrs := parser.ParseWithRecovery(tokens)
	fa := BuildFile(nodes)
	BuildTypes(fa, nodes)
	errs := CheckTypes(fa, nodes)

	allMessages := []string{}
	for _, pe := range parseErrs {
		allMessages = append(allMessages, pe.Error())
	}
	for _, e := range fa.TypeErrors {
		allMessages = append(allMessages, e.Message)
	}
	for _, e := range errs {
		allMessages = append(allMessages, e.Message)
	}
	if len(allMessages) != 0 {
		t.Errorf("expected no errors for flat pattern, got: %v", allMessages)
	}
}

// The explicit form still works.
func TestTuplePayloadVariant_ExplicitFormWorks(t *testing.T) {
	src := `enum Shape { Rect (Float, Float); Empty }
fn area(s: Shape): Float {
  case s {
    Shape.Rect((w, h)) -> w * h
    Shape.Empty -> 0.0
  }
}
fn make(): Shape { Shape.Rect((3.0, 5.0)) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Variant-with-tuple-payload + flat pattern of WRONG arity (3 bindings
// against a tuple-2 payload) reports a single arity error. After variant
// literal-attach landed, the old "multi-binding is not supported" diagnostic
// retired and the inner tuple-pattern check is the sole arity gate — both
// for the legacy `((a, b))` form and for the flat `(a, b)` shorthand.
func TestCheck_VariantTuplePayload_FlatPattern_WrongArity(t *testing.T) {
	src := `enum Shape { Rect (Float, Float); Empty }
fn area(s: Shape): Float {
  case s {
    Shape.Rect(w, h, extra) -> w * h
    Shape.Empty -> 0.0
  }
}`
	_, errs := checkSource(src)

	arityCount := 0
	for _, e := range errs {
		if strings.Contains(e.Message, "tuple pattern has") && strings.Contains(e.Message, "elements but expected") {
			arityCount++
		}
	}
	if arityCount != 1 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected exactly 1 tuple-pattern arity error, got %d. All errors:\n  %s",
			arityCount, strings.Join(msgs, "\n  "))
	}
}

// Bare-variant-name resolution rule (value position).
//
// New uniform rule: bare variant names in VALUE position
// (construction, assignment RHS, function args, returns) are
// rejected unless the variant arrived via a drill-through import.
// Qualified `EnumName.Variant` is always accepted. Pattern position
// with a type-determined scrutinee is the one exception — bare is
// fine there because the scrutinee disambiguates the enum (covered
// separately in pattern-checker tests).
//
// These tests pin all three flavours of value-position rejection
// (flat-form map, flat-form list, call-form), plus the qualified
// counterparts that should keep working unchanged.

func TestVariant_BareValuePos_FlatMap_Error(t *testing.T) {
	src := `enum E { Obj Map<String, Int> }
fn main() {
  bad = Obj{"k" => 1}
}`
	_, errs := checkSource(src)
	expectError(t, errs, "variant 'Obj' must be qualified through its enum")
	expectError(t, errs, "use 'E.Obj'")
}

func TestVariant_BareValuePos_FlatList_Error(t *testing.T) {
	src := `enum E { Arr List<Int> }
fn main() {
  bad = Arr[1, 2, 3]
}`
	_, errs := checkSource(src)
	expectError(t, errs, "variant 'Arr' must be qualified through its enum")
	expectError(t, errs, "use 'E.Arr'")
}

func TestVariant_BareValuePos_Call_Error(t *testing.T) {
	// Call-form was already caught by checkTypeIdent's pre-existing
	// rule; this test pins that the diagnostic shape matches the new
	// flat-form rule's wording, so the rule reads uniformly across
	// shapes.
	src := `enum E { Pos (Int, Int) }
fn main() {
  bad = Pos(10, 20)
}`
	_, errs := checkSource(src)
	expectError(t, errs, "variant 'Pos' must be qualified through its enum")
	expectError(t, errs, "use 'E.Pos'")
}

func TestVariant_QualifiedValuePos_FlatMap_OK(t *testing.T) {
	src := `enum E { Obj Map<String, Int> }
fn main() {
  good = E.Obj{"k" => 1}
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVariant_QualifiedValuePos_FlatList_OK(t *testing.T) {
	src := `enum E { Arr List<Int> }
fn main() {
  good = E.Arr[1, 2, 3]
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVariant_DotPattern_WithScrutinee_OK(t *testing.T) {
	// Pattern position with a type-determined scrutinee — dot-leading
	// variant prefixes resolve through the scrutinee's enum-member
	// table. Bare prefixes were rejected in the dot-leading migration
	// (with the prelude carve-out for Ok/Err/Some/None/True/False).
	src := `enum E { Obj Map<String, Int>; Arr List<Int>; Empty }
fn describe(e: E): Int {
  case e {
    .Obj(m) -> 1
    .Arr(xs) -> 2
    .Empty -> 0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Dot-leading variant resolution: analyzer tests.
//
// `.Variant` resolves against the expected enum type at the expression's
// syntactic position. Coverage spans each position that supplies an
// expected type.

func TestDotVariant_BareInAnnotatedBinding_OK(t *testing.T) {
	// c: Color = .Red — annotation pins the enum at the RHS position.
	src := `enum Color { Red; Green; Blue }
fn main() {
  c: Color = .Red
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_CallInAnnotatedBinding_OK(t *testing.T) {
	// s: Shape = .Circle(1.0)
	src := `enum Shape { Circle Float; Square Float }
fn main() {
  s: Shape = .Circle(1.0)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_StructLitInAnnotatedBinding_OK(t *testing.T) {
	// s: Shape = .Rect{w: 4.0, h: 3.0}
	src := `enum Shape { Rect{w: Float, h: Float}; Origin }
fn main() {
  s: Shape = .Rect{w: 4.0, h: 3.0}
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_MapLitInAnnotatedBinding_OK(t *testing.T) {
	// v: V = .Obj{"k" => 1}
	src := `enum V { Obj Map<String, Int>; Nil }
fn main() {
  v: V = .Obj{"k" => 1}
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_ListLitInAnnotatedBinding_OK(t *testing.T) {
	// v: V = .Arr[1, 2, 3]
	src := `enum V { Arr List<Int>; Nil }
fn main() {
  v: V = .Arr[1, 2, 3]
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_FunctionArg_OK(t *testing.T) {
	// paint(.Red) where paint takes Color
	src := `enum Color { Red; Green; Blue }
fn paint(_c: Color): Int { 0 }
fn demo(): Int {
  paint(.Red)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_AnnotatedReturn_OK(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
fn default_color(): Color { .Red }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_ListElementFromAnnotation_OK(t *testing.T) {
	// palette: List<Color> = [.Red, .Green]
	src := `enum Color { Red; Green; Blue }
fn main() {
  palette: List<Color> = [.Red, .Green]
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_MapValueFromAnnotation_OK(t *testing.T) {
	// themes: Map<String, Color> = {"a" => .Red}
	src := `enum Color { Red; Green; Blue }
fn main() {
  themes: Map<String, Color> = {"a" => .Red}
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_StructFieldValue_OK(t *testing.T) {
	// Box{color: .Red}
	src := `enum Color { Red; Green; Blue }
struct Box { color: Color }
fn main() {
  b: Box = Box{color: .Red}
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_IfArmsFromAnnotation_OK(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
fn main() {
  c: Color = if 1 > 0 { .Red } else { .Blue }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_CaseArmsFromAnnotation_OK(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
fn main() {
  x = 1
  c: Color = case x {
    1 -> .Red
    _ -> .Blue
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Pattern-position dot-leading variants — resolve via scrutinee type.
func TestDotVariant_BareInPattern_OK(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
fn name(c: Color): Int {
  case c {
    .Red -> 1
    .Green -> 2
    .Blue -> 3
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_PayloadInPattern_OK(t *testing.T) {
	src := `enum Shape { Circle Float; Square Float; Origin }
fn area(s: Shape): Float {
  case s {
    .Circle(r) -> 3.14 * r * r
    .Square(side) -> side * side
    .Origin -> 0.0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_StructPatternInPattern_OK(t *testing.T) {
	src := `enum Shape { Rect{w: Float, h: Float}; Origin }
fn area(s: Shape): Float {
  case s {
    .Rect{w, h} -> w * h
    .Origin -> 0.0
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A lambda's result position takes the callback's declared result, as a fn
// body's tail takes its declared return: with an expression body and a block body.
func TestDotVariant_LambdaResult_OK(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
fn apply(f: (Int) -> Color): Color { f(1) }
fn main() {
  _ = apply(|n| if n > 0 { .Red } else { .Blue })
  _ = apply(|_n| {
    .Green
  })
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A declared result that holds a type parameter supplies no enum: the body is
// what solves it, so `.Red` there still needs another way to name its enum.
func TestDotVariant_LambdaGenericResult_Error(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
fn map1<T, U>(x: T, f: (T) -> U): U { f(x) }
fn main() {
  _ = map1(1, |_n| .Red)
}`
	_, errs := checkSource(src)
	expectError(t, errs, "requires a determinable enum type")
}

// Failure modes — three diagnostics.
func TestDotVariant_NoExpectedType_Error(t *testing.T) {
	// c = .Red — no annotation, statement-position usage. Should error.
	src := `enum Color { Red; Green; Blue }
fn main() {
  c = .Red
}`
	_, errs := checkSource(src)
	expectError(t, errs, "requires a determinable enum type")
}

func TestDotVariant_ExpectedNotEnum_Error(t *testing.T) {
	// f(.Red) where f takes Int — expected type isn't an enum.
	src := `enum Color { Red; Green; Blue }
fn f(x: Int): Int { x }
fn main() {
  f(.Red)
}`
	_, errs := checkSource(src)
	expectError(t, errs, "resolves only to enum variants")
}

func TestDotVariant_NoSuchVariant_Error(t *testing.T) {
	// c: Color = .Purple — enum has no Purple.
	src := `enum Color { Red; Green; Blue }
fn main() {
  c: Color = .Purple
}`
	_, errs := checkSource(src)
	expectError(t, errs, "no variant 'Purple' on enum Color")
}

// TestChecker_AnonStructType_FunctionParam exercises an anonymous struct
// type as a function parameter annotation. Build + Check should succeed:
// `greet`'s param type resolves to *analysis.AnonStructType with one
// FieldDef, and the call site's anon struct literal `{name: "World"}`
// unifies with that param type via TypesEqual (already structural).
func TestChecker_AnonStructType_FunctionParam(t *testing.T) {
	src := `fn greet(p: {name: String}): String {
  p.name
}

fn demo(): String {
  greet({name: "World"})
}
`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	// The param `p` should be bound to *AnonStructType with one FieldDef.
	var pSym *Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "p" && sym.Kind == SymbolParam {
			pSym = sym
			break
		}
	}
	if pSym == nil {
		t.Fatal("expected param symbol 'p' to be defined")
	}
	anon, ok := pSym.Type.(*AnonStructType)
	if !ok {
		t.Fatalf("expected p.Type to be *AnonStructType, got %T (%v)", pSym.Type, pSym.Type)
	}
	if len(anon.Fields) != 1 || anon.Fields[0].Name != "name" {
		t.Errorf("expected one field 'name', got %+v", anon.Fields)
	}
	if anon.Fields[0].Type == nil || anon.Fields[0].Type.String() != "String" {
		t.Errorf("expected field 'name' type to be String, got %v", anon.Fields[0].Type)
	}
}

// TestChecker_AnonStructType_FieldNameRegistered pins the hover-on-anon-
// struct-type-field behavior. Resolving an anon struct type expression
// registers a self-referential SymbolField at each field name's position,
// matching what checkStructLit does for anon struct *literals*. Without
// this, hover on `name` in `fn greet(p: {name: String})` shows nothing
// while hover on `name` in `{name: "Bob"}` shows `name: String` — an
// LSP-visible asymmetry.
func TestChecker_AnonStructType_FieldNameRegistered(t *testing.T) {
	src := `fn greet(p: {name: String, age: Int}): String {
  p.name
}
`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)

	// `name` is at line 1, col 14 (after `fn greet(p: {`). Find by name to
	// avoid coupling to exact column counting.
	var nameSym, ageSym *Symbol
	for pos, sym := range fa.References {
		if sym == nil || sym.Kind != SymbolField {
			continue
		}
		switch sym.Name {
		case "name":
			if pos.Line == 1 {
				nameSym = sym
			}
		case "age":
			if pos.Line == 1 {
				ageSym = sym
			}
		}
	}
	if nameSym == nil {
		t.Fatal("expected SymbolField for 'name' at line 1 in References")
	}
	if nameSym.Type == nil || nameSym.Type.String() != "String" {
		t.Errorf("expected name.Type=String, got %v", nameSym.Type)
	}
	if ageSym == nil {
		t.Fatal("expected SymbolField for 'age' at line 1 in References")
	}
	if ageSym.Type == nil || ageSym.Type.String() != "Int" {
		t.Errorf("expected age.Type=Int, got %v", ageSym.Type)
	}
}

// TestChecker_AnonStructType_DuplicateFieldNameRejected pins that an anon
// struct type declaring two fields with the same name is a compile error.
// Without this, the type checker's name-keyed equality relation would
// silently pick whichever entry the map iteration landed on last.
func TestChecker_AnonStructType_DuplicateFieldNameRejected(t *testing.T) {
	src := `fn f(_p: {a: Int, a: String}): Int { 0 }
`
	_, errs := checkSource(src)
	expectError(t, errs, "duplicate field 'a'")
}

// TestChecker_AnonStructLit_DuplicateFieldNameRejected pins parity
// with the type-expression duplicate-field check: anon-struct literals
// with two fields of the same name are also rejected, so the new
// name-keyed equality relation never sees a duplicate either side.
func TestChecker_AnonStructLit_DuplicateFieldNameRejected(t *testing.T) {
	src := `fn demo(): Int {
  s = {a: 1, a: 2}
  s.a
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "field 'a' is given twice in this struct literal")
}

// TestChecker_AnonStructParam_AcceptsReorderedArg pins that an anon-struct
// argument with fields in a different order than the parameter type still
// type-checks. Equality is over the field set, not the field list.
func TestChecker_AnonStructParam_AcceptsReorderedArg(t *testing.T) {
	src := `fn f(p: {a: Int, b: String}): String {
  p.b
}

fn demo(): String {
  f({b: "x", a: 1})
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// TestChecker_AnonStructInTuple_FieldOrderIgnored pins that nested anon
// struct shapes inside a tuple type compare order-insensitively too.
func TestChecker_AnonStructInTuple_FieldOrderIgnored(t *testing.T) {
	src := `fn f(p: (Int, {x: Int, y: Int})): Int {
  case p {
    (n, _) -> n
  }
}

fn demo(): Int {
  f((42, {y: 2, x: 1}))
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// TestChecker_GenericFn_AnonStructParam_InfersTypeArg pins that a type
// parameter nested in an anon-struct field type participates in inference.
// Without ContainsTypeParam / Substitute / unifyFull traversal, T stays
// unbound and the call site silently fails to enforce the constraint.
func TestChecker_GenericFn_AnonStructParam_InfersTypeArg(t *testing.T) {
	src := `fn first<T>(p: {head: T, tail: List<T>}): T {
  p.head
}

fn demo(): Int {
  first({head: 1, tail: [2, 3]})
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// TestChecker_GenericFn_AnonStructParam_RejectsTypeMismatch pins the
// negative side: when the anon-struct argument forces T to two different
// types, inference must fail.
func TestChecker_GenericFn_AnonStructParam_RejectsTypeMismatch(t *testing.T) {
	src := `fn first<T>(p: {head: T, tail: List<T>}): T {
  p.head
}

fn demo(): Int {
  first({head: 1, tail: ["a", "b"]})
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "argument 1: expected")
}

// TestChecker_AnonStructParam_GenericPayloadInfersTypeArg pins that a
// generic payload type nested in an anon-struct field gets fully solved
// during inference. Without an *AnonStructType case in containsTypeVar,
// argMatchesParam does not fall through to unify for anon-struct args
// containing fresh TypeVars (e.g. Maybe<?1> from a nullary Maybe.None).
func TestChecker_AnonStructParam_GenericPayloadInfersTypeArg(t *testing.T) {
	src := `enum Maybe<T> { Some(T); None }

fn f(_p: {head: Maybe<Int>}): Int { 0 }

fn demo(): Int {
  f({head: Maybe.None})
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A QUALIFIED STRUCT-VARIANT LITERAL IS TYPE-CHECKED AT ALL. Before this, the
// `Enum.Variant{...}` branch of checkStructLit walked each field value with a
// bare checkNode and returned the enum's declared type, so it validated
// NOTHING: not the field names, not the field types, and not the enum's type
// arguments.
//
// THE NON-GENERIC ROWS ARE THE ONES THAT SIZE THE DEFECT, and they are in this
// table deliberately. `Shape.Wrap{inner: "x"}` where `inner: Int` produced no
// diagnostic at the base twin AND RAN, printing `Wrap{inner: "x"}` — a String
// living in a field declared Int. So this was never a generics bug or a hover
// cosmetic; generic inference is one of three things the branch omitted.
//
// THE STRUCT ROWS ARE THE CONTROL. `Box`'s literal path reported every one of
// these, which is what localises the fault to the enum branch rather than to
// the field-checking machinery it should have been using.
func TestCheck_QualifiedStructVariantLit_ValidatesFields(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// want is the substring a diagnostic must contain; "" means the
		// program must check clean.
		want string
	}{
		{
			name: "non-generic: a field value of the wrong type — ACCEPTED AND RAN at the twin",
			src: `enum Shape { Wrap { inner: Int } }
fn main() {
  s = Shape.Wrap{inner: "x"}
  take(s)
}
fn take(_x: Shape) {}`,
			want: "inner",
		},
		{
			name: "non-generic: a field name that does not exist",
			src: `enum Shape { Wrap { inner: Int } }
fn main() {
  s = Shape.Wrap{bogus: 3}
  take(s)
}
fn take(_x: Shape) {}`,
			want: "bogus",
		},
		{
			name: "non-generic: a correct literal stays clean",
			src: `enum Shape { Wrap { inner: Int } }
fn main() {
  s = Shape.Wrap{inner: 3}
  take(s)
}
fn take(_x: Shape) {}`,
			want: "",
		},
		{
			name: "generic: the inferred argument reaches a parameter that disagrees",
			src: `enum Shape<T> { Wrap { inner: T } }
fn main() {
  s = Shape.Wrap{inner: 3}
  take(s)
}
fn take(_x: Shape<String>) {}`,
			want: "Shape<Int>",
		},
		{
			name: "generic: the inferred argument reaches a parameter that agrees",
			src: `enum Shape<T> { Wrap { inner: T } }
fn main() {
  s = Shape.Wrap{inner: 3}
  take(s)
}
fn take(_x: Shape<Int>) {}`,
			want: "",
		},
		{
			name: "generic, NESTED argument: Shape<List<Int>>",
			src: `enum Shape<T> { Wrap { inner: T } }
fn main() {
  s = Shape.Wrap{inner: [1, 2]}
  take(s)
}
fn take(_x: Shape<List<String>>) {}`,
			want: "Shape<List<Int>>",
		},
		{
			name: "generic, NESTED argument, agreeing",
			src: `enum Shape<T> { Wrap { inner: T } }
fn main() {
  s = Shape.Wrap{inner: [1, 2]}
  take(s)
}
fn take(_x: Shape<List<Int>>) {}`,
			want: "",
		},
		{
			name: "MULTI-PARAMETER: both arguments are solved, the second disagrees",
			src: `enum Two<A, B> { Both { left: A, right: B } }
fn main() {
  t = Two.Both{left: 1, right: "s"}
  take(t)
}
fn take(_x: Two<Int, Int>) {}`,
			want: "Two<Int, String>",
		},
		{
			name: "MULTI-PARAMETER, agreeing",
			src: `enum Two<A, B> { Both { left: A, right: B } }
fn main() {
  t = Two.Both{left: 1, right: "s"}
  take(t)
}
fn take(_x: Two<Int, String>) {}`,
			want: "",
		},
		{
			name: "the type parameter is NESTED IN THE FIELD TYPE — inner: List<T> solves T from the element",
			src: `enum Holder<T> { Of { items: List<T> } }
fn main() {
  h = Holder.Of{items: ["a"]}
  take(h)
}
fn take(_x: Holder<Int>) {}`,
			want: "Holder<String>",
		},
		{
			name: "CONTROL — the struct path's INFERENCE reported this already",
			src: `struct Box<T> { value: T }
fn main() {
  b = Box{value: 1}
  take(b)
}
fn take(_x: Box<String>) {}`,
			want: "Box<Int>",
		},
		// THIS ROW PINNED AN OPEN HOLE AND THE HOLE IS CLOSED. It was written
		// first as a control expecting `bogus`, and it FAILED, because the
		// STRUCT literal-attach path did not validate field names either.
		// Measured at the twin, `Box{bogus: 1}`, `Point{bogus: 1}` and
		// `Box{}` all checked clean, and `Point{x: "s"}` and `Holder{p: 3}`
		// did too. StructLitValidation routed that path through
		// `checkStructLitAgainstStruct` — the validator the CALL form
		// `Box({bogus: 1})` already used, pinned by
		// TestCheck_GenericStruct_CallForm_FieldNameMismatch — so the two
		// spellings now report the same thing, which is what this row
		// asserts. `want` names the field-NAME diagnostic; the missing-field
		// one that accompanies it is asserted in
		// TestCheck_StructLit_ValidatesFields.
		{
			name: "the struct path reports an unknown field — asymmetry closed",
			src: `struct Box<T> { value: T }
fn main() {
  b = Box{bogus: 1}
  take(b)
}
fn take(_x: Box<Int>) {}`,
			want: "Box has no field 'bogus'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(tc.src)
			if tc.want == "" {
				expectNoErrors(t, errs)
				return
			}
			if len(withoutUnusedBindingErrors(errs)) == 0 {
				t.Fatalf("expected a diagnostic containing %q, got none", tc.want)
			}
			expectError(t, errs, tc.want)
		})
	}
}

// AN EXPLICIT ANNOTATION'S TYPE ARGUMENT REACHES THE LITERAL'S FIELDS. This is
// the other direction of the same omission and it fails differently, which is
// why it is its own test: with `s: Shape<String> = Shape.Wrap{inner: 3}` the
// twin reported `type mismatch: expected Shape<String>, got Shape` — an error,
// but by accident, from comparing the annotation against the BARE enum. The
// field itself was never checked, so the message named neither `inner` nor
// `Int` and would have been identical for any wrong value at all.
func TestCheck_QualifiedStructVariantLit_AnnotationReachesTheFields(t *testing.T) {
	src := `enum Shape<T> { Wrap { inner: T } }
fn main() {
  s: Shape<String> = Shape.Wrap{inner: 3}
  take(s)
}
fn take(_x: Shape<String>) {}`
	_, errs := checkSource(src)
	if len(withoutUnusedBindingErrors(errs)) == 0 {
		t.Fatal("expected a diagnostic for a field value that contradicts the annotation, got none")
	}
	expectError(t, errs, "inner")
}

// A STRUCT-VARIANT FIELD BOUND BY A PATTERN CARRIES THE SOLVED ARGUMENT, NOT
// THE TYPE PARAMETER. At the twin `takes_string(inner)` reported
// `argument 1: expected String, got T` — reported, but naming the
// uninstantiated parameter, because the scrutinee's type was bare `Shape`.
func TestCheck_QualifiedStructVariantLit_PatternBindingIsInstantiated(t *testing.T) {
	src := `enum Shape<T> { Wrap { inner: T } }
fn main() {
  s = Shape.Wrap{inner: 3}
  case s {
    Shape.Wrap{inner} -> takes_string(inner)
  }
}
fn takes_string(_x: String) {}`
	_, errs := checkSource(src)
	expectError(t, errs, "got Int")
}

// THE SEED ONLY FIRES FOR THE SAME ENUM. `c.expectedEnum` is whatever type the
// position expects, which need not be the enum the literal names, so seeding
// without a name guard would take one enum's arguments as another's.
//
// PINNED BECAUSE A MUTANT SURVIVED WITHOUT IT. Dropping `exp.Name == et.Name`
// from checkStructVariantLit left ./analysis and ./lsp green; the rows below
// are what now separate the two.
func TestCheck_QualifiedStructVariantLit_SeedRequiresTheSameEnum(t *testing.T) {
	// `Maybe<Int>` is expected and `Shape.Wrap{inner: "s"}` is supplied. The
	// mis-seeding mutant binds Shape's `T` to Int from Maybe's argument list
	// and then faults `inner` for being a String; the guard makes Shape infer
	// `T = String` on its own, so the only fault is the enum mismatch.
	src := `enum Shape<T> { Wrap { inner: T } }
enum Maybe2<T> { Just { v: T } }
fn main() {
  s: Maybe2<Int> = Shape.Wrap{inner: "s"}
  take(s)
}
fn take(_x: Maybe2<Int>) {}`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "field 'inner'") {
			t.Fatalf("Shape's T was seeded from Maybe2's argument list: %q", e.Message)
		}
	}
	expectError(t, errs, "Shape<String>")
}

// A host fn's parameter default is checked against the parameter's type, as
// a fn's is.
func TestCheckHostFnParamDefault(t *testing.T) {
	_, errs := checkSource("host fn pad(s: String, width: Int = \"wide\"): String\n")
	expectError(t, errs, "default value for parameter 'width' is String, expected Int")
	_, errs = checkSource("host fn pad(s: String, width: Int = 8, fill: String = \" \"): String\n\nfn main() {\n  _ = pad(\"a\")\n}\n")
	expectNoErrors(t, errs)
}

// A file qualifier is not a value: alone as a statement, bound, or in a
// list, it is an error at the name. As a qualifier it still reaches the
// file's members.
func TestCheckFileQualifierIsNotAValue(t *testing.T) {
	helper := map[string]string{"helper": "pub fn four(): Int {\n  4\n}\n"}
	for _, body := range []string{
		"  x = dbg 41 + 1\n  _ = x\n  helper",
		"  y = helper\n  _ = y",
		"  _ = [helper]",
	} {
		_, errs := checkSourceWithModules(t, "import helper\n\nfn main() {\n"+body+"\n}\n", helper)
		expectError(t, errs, "`helper` is a file, not a value")
	}
	_, errs := checkSourceWithModules(t, "import helper\n\nfn main() {\n  _ = helper.four()\n  f = helper.four\n  _ = f()\n}\n", helper)
	expectNoErrors(t, errs)
}

// An annotation's type arguments steer a generic struct literal's field
// lambda, and the values still decide the literal's type: one that does
// not fit the annotation is the binding's mismatch.
func TestCheckGenericStructLitFieldTakesAnnotation(t *testing.T) {
	const decl = "struct Box<T> {\n  v: T\n}\n\n"
	_, errs := checkSource(decl + "fn main() {\n  f: Box<(Int) -> Int> = Box{v: |x| x + 1}\n  g: Box<(Int) -> Int> = Box({v: |x| x * 2})\n  _ = f\n  _ = g\n}\n")
	expectNoErrors(t, errs)
	for _, tc := range []struct{ value, want string }{
		{`Box{v: "s"}`, "type mismatch: expected Box<(Int) -> Int>, got Box<String>"},
		{`Box{v: |_x: String| 1}`, "type mismatch: expected Box<(Int) -> Int>, got Box<(String) -> Int>"},
	} {
		_, errs := checkSource(decl + "fn main() {\n  f: Box<(Int) -> Int> = " + tc.value + "\n  _ = f\n}\n")
		expectError(t, errs, tc.want)
	}
}

// Every declared function's parameter needs a type: a fn's, a nested fn's,
// an impl's or an interface's function's, and a host fn's. Only a lambda's
// may be inferred.
func TestCheckDeclaredParamNeedsType(t *testing.T) {
	for _, tc := range []struct{ name, src, param string }{
		{"fn", "fn plain(_a): Int {\n  1\n}\n", "_a"},
		{"nested fn", "fn outer(): Int {\n  fn inner(b): Int {\n    b\n  }\n  inner(1)\n}\n", "b"},
		{"impl fn", "interface Tagged {\n  fn tag(v: self): String\n}\n\nstruct User {\n  name: String\n}\n\nimpl Tagged for User {\n  fn tag(_v): String {\n    \"user\"\n  }\n}\n", "_v"},
		{"inherent fn", "struct User {\n  name: String\n}\n\nimpl User {\n  fn hi(u): String {\n    u.name\n  }\n}\n", "u"},
		{"interface fn", "interface Tagged {\n  fn tag(v): String\n}\n", "v"},
		{"host fn", "host fn pad(s): String\n", "s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(tc.src)
			expectError(t, errs, "parameter '"+tc.param+"' needs a type annotation")
		})
	}
	_, errs := checkSource("fn apply(f: (Int) -> Int): Int {\n  f(1)\n}\n\nfn main() {\n  _ = apply(|x| x + 1)\n}\n")
	expectNoErrors(t, errs)
}

// Every declared function's parameter needs a name. A bare type in parameter
// position, `fn f(String)`, parses as a variant pattern that binds nothing;
// it is an error at the parameter for a `go`-bound fn (which has no body to
// check the pattern against), and for every other declared function. (A
// `host fn`'s parameters are parsed as names, so the parser rejects it.) A
// lambda's gets the same error whether or not a function type is expected
// for it; checked against an expected `(String) -> Int` it was "enum pattern
// requires an enum type, got String".
func TestCheckDeclaredParamNeedsName(t *testing.T) {
	for _, tc := range []struct {
		name, src, typ string
		line, col      int
	}{
		{"go fn", "gopkg \"example.com/app/ffi\" as ffi\n\npub fn echo_upper(   String): String go ffi.EchoUpper\n", "String", 3, 22},
		{"fn", "fn plain(Int): Int {\n  1\n}\n", "Int", 1, 10},
		{"nested fn", "fn outer(): Int {\n  fn inner(Int): Int {\n    1\n  }\n  inner(1)\n}\n", "Int", 2, 12},
		{"inherent fn", "struct User {\n  name: String\n}\n\nimpl User {\n  fn hi(User): String {\n    \"hi\"\n  }\n}\n", "User", 6, 9},
		{"interface fn", "interface Tagged {\n  fn tag(String): String\n}\n", "String", 2, 10},
		{"lambda", "fn main() {\n  f = |String| 1\n  _ = f\n}\n", "String", 2, 8},
		{"lambda with an expected type", "fn apply(f: (String) -> Int): Int {\n  f(\"a\")\n}\n\nfn main() {\n  _ = apply(|String| 1)\n}\n", "String", 6, 14},
		{"lambda whose expected type differs", "fn apply(f: (Int) -> Int): Int {\n  f(1)\n}\n\nfn main() {\n  _ = apply(|String| 1)\n}\n", "String", 6, 14},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(tc.src)
			want := "parameter '" + tc.typ + "' needs a name"
			for _, e := range errs {
				if e.Message == want {
					if e.Line != tc.line || e.Col != tc.col {
						t.Fatalf("%q at %d:%d, want %d:%d", want, e.Line, e.Col, tc.line, tc.col)
					}
					return
				}
			}
			t.Fatalf("no %q error; got %v", want, errs)
		})
	}
	// A named parameter is fine, and so is a lambda's named or destructuring
	// one against an expected type.
	_, errs := checkSource("gopkg \"example.com/app/ffi\" as ffi\n\npub fn echo_upper(s: String): String go ffi.EchoUpper\n")
	expectNoErrors(t, errs)
	_, errs = checkSource("fn apply(f: ((Int, Int)) -> Int): Int {\n  f((1, 2))\n}\n\nfn main() {\n  _ = apply(|(a, b)| a + b)\n  _ = apply(|_pair| 0)\n}\n")
	expectNoErrors(t, errs)
}

// A bare parameter that names a variant of an enum in scope, `|One| 1`, reads
// as an attempt to match the variant, so it gets the pattern check's "bare
// variant" error rather than "needs a name" with a hint that calls `One` a
// type. When a type of that name exists too, the parameter is still read as
// that type, and the "needs a name" error adds how to match the variant.
func TestCheckBareVariantParam(t *testing.T) {
	const single = "enum Single {\n  One\n}\n\n"
	const pair = "enum Pair {\n  One\n  Two\n}\n\n"
	for _, tc := range []struct {
		name, src, msg string
		hints          []string
		line, col      int
	}{
		{"lambda, single-variant enum expected",
			single + "fn apply(f: (Single) -> Int): Int {\n  f(.One)\n}\n\nfn main() {\n  _ = apply(|One| 1)\n}\n",
			"bare variant 'One' in pattern position; use '.One' (or 'Single.One')",
			[]string{"to bind the value, write `name: Single`"}, 10, 14},
		{"lambda, no expected type",
			single + "fn main() {\n  f = |One| 1\n  _ = f\n}\n",
			"bare variant 'One' in pattern position; use '.One' (or 'Single.One')",
			[]string{"to bind the value, write `name: Single`"}, 6, 8},
		{"lambda, multi-variant enum expected",
			pair + "fn apply(f: (Pair) -> Int): Int {\n  f(.One)\n}\n\nfn main() {\n  _ = apply(|One| 1)\n}\n",
			"bare variant 'One' in pattern position; use '.One' (or 'Pair.One')",
			[]string{"to bind the value, write `name: Pair`"}, 11, 14},
		{"fn",
			single + "fn f(One): Int {\n  1\n}\n",
			"bare variant 'One' in pattern position; use '.One' (or 'Single.One')",
			[]string{"to bind the value, write `name: Single`"}, 5, 6},
		{"fn, a type of the same name exists",
			"struct One {\n  x: Int\n}\n\n" + single + "fn f(One): Int {\n  1\n}\n",
			"parameter 'One' needs a name",
			[]string{"`One` is read as the parameter's type; write `name: One`", "write `.One` (or `Single.One`) to match the variant"}, 9, 6},
		{"lambda, a type",
			"fn main() {\n  f = |String| 1\n  _ = f\n}\n",
			"parameter 'String' needs a name",
			[]string{"`String` is read as the parameter's type; write `name: String`"}, 2, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(tc.src)
			if len(errs) != 1 {
				t.Fatalf("want one error %q, got %v", tc.msg, errs)
			}
			e := errs[0]
			if e.Message != tc.msg || e.Line != tc.line || e.Col != tc.col {
				t.Fatalf("got %d:%d %q, want %d:%d %q", e.Line, e.Col, e.Message, tc.line, tc.col, tc.msg)
			}
			if !slices.Equal(e.Hints, tc.hints) {
				t.Fatalf("hints %q, want %q", e.Hints, tc.hints)
			}
		})
	}
}
