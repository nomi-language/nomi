package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

// Tuple-state default with first-line destructure should type-check WITHOUT
// an explicit annotation. `(0, [])` should be typed as `(Int, List<TypeVar>)`
// — concrete enough to destructure, with the empty-list element type a fresh
// inference variable that body unifications can solve.
func TestCheckTupleStateDestructureNoAnnotation(t *testing.T) {
	src := `fn f(xs: List<Int>): (Int, List<Int>) {
  Iter.reduce(xs, |state = (0, []), item| {
    (count, acc) = state
    if count >= 2 { break (count, acc) } else { (count + 1, [item, ..acc]) }
  })
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Inline binding annotation drives polymorphic inference. Without the
// annotation, `Map.empty()` would yield `Map<?α, ?β>`. With the declared
// type, the binding resolves to a concrete `Map<String, Int>` so hover
// (and downstream uses) see the user's written shape.
//
// Three independent assertions witness the production behavior:
//
//  1. Control: WITHOUT an annotation, `Map.empty()`'s RHS leaves the
//     polymorphic Map<K, V> as `Map<?α, ?β>` — the symbol's Type is a
//     *MapType whose Key/Val are unresolved *TypeVars. This pins down the
//     baseline (no bidirectional driver) so the contrast with case (2) is
//     load-bearing rather than tautological.
//
//  2. Positive: WITH the annotation, the symbol's Type ends up
//     Map<String, Int>. The implementation attaches the declared annotation
//     directly to the symbol, so on its own this assertion would pass even
//     if `unify` were a no-op — case (3) closes that gap.
//
//  3. Witnesses unify: a deliberately mismatched annotation must surface
//     a "type mismatch: expected Map<String, Int>, got Map<String, String>"
//     error. The mismatch detection lives ONLY inside the unify call in
//     checkBinding — replacing unify with `func(...) error { return nil }`
//     makes this case silently pass type-checking, failing this assertion.
//     This is the tight witness that unify actually fires on the RHS.
func TestCheckBinding_AnnotationDrivesInference(t *testing.T) {
	// (1) Control: no annotation on a wholly-unresolved RHS (Map.empty() →
	// Map<?, ?>) is now rejected by the locally-determined-binding rule —
	// which is exactly what makes the annotation in (2) necessary.
	control := `fn main() {
  m = Map.empty()
}`
	_, ctrlErrs := checkSourceWithStdlib(control)
	foundLD := false
	for _, e := range ctrlErrs {
		if strings.Contains(e.Message, "not locally determined") {
			foundLD = true
		}
	}
	if !foundLD {
		t.Fatalf("control: expected a 'not locally determined' error for unannotated `m = Map.empty()`, got: %v", ctrlErrs)
	}

	// (2) Positive: annotation present → m.Type is Map<String, Int>.
	positive := `fn main() {
  m: Map<String, Int> = Map.empty()
}`
	faPos, errs := checkSourceWithStdlib(positive)
	expectNoStdlibErrors(t, errs)

	mSym := findBindingSymbol(t, faPos, "m")
	mt, ok := mSym.Type.(*analysis.MapType)
	if !ok {
		t.Fatalf("positive: m type: got %T (%s), want *MapType", mSym.Type, mSym.Type)
	}
	if mt.Key != analysis.TypeString {
		t.Errorf("positive: m key type: got %s, want String", mt.Key)
	}
	if mt.Val != analysis.TypeInt {
		t.Errorf("positive: m val type: got %s, want Int", mt.Val)
	}

	// (3) Witnesses unify: mismatched annotation must produce the unify-
	// authored "expected X, got Y" error. The RHS Iter.to_map([("a","b")])
	// resolves to Map<String, String>; declared is Map<String, Int>. The
	// only producer of this specific error message is the unify call in
	// checkBinding, so this assertion fails if unify is replaced with a
	// no-op.
	mismatch := `import std/maps: Map
fn main() {
  m: Map<String, Int> = Iter.to_map([("a", "b")])
}`
	_, mismatchErrs := checkSourceWithStdlib(mismatch)
	if len(mismatchErrs) == 0 {
		t.Fatal("mismatch: expected a type-mismatch error, got none — unify in checkBinding likely did not fire")
	}
	var foundMismatch bool
	for _, e := range mismatchErrs {
		if strings.Contains(e.Message, "expected Map<String, Int>") &&
			strings.Contains(e.Message, "got Map<String, String>") {
			foundMismatch = true
			break
		}
	}
	if !foundMismatch {
		msgs := make([]string, len(mismatchErrs))
		for i, e := range mismatchErrs {
			msgs[i] = e.Error()
		}
		t.Fatalf("mismatch: expected unify-authored \"expected Map<String, Int>, got Map<String, String>\" error; got:\n  %s",
			strings.Join(msgs, "\n  "))
	}
}

// Variant constructors (`Ok(42)`, `Some(7)`) carry free TypeParam_s in their
// return type that args alone cannot solve — `Ok: (T) -> Result<T, E>` binds
// T from the argument but leaves E free. Without driving the binding's
// declared type into the unify subs, valTy ends up as `Result<Int, E>` and
// surfaces a spurious "type mismatch: expected Result<Int, String>, got
// Result<Int, E>" diagnostic. This test pins down that the binding-side
// unify uses a real subs map, substituting the bound TypeParam out of valTy.
func TestCheckBinding_VariantConstructorAnnotation(t *testing.T) {
	src := `fn main() {
  res: Result<Int, String> = Ok(42)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)

	resSym := findBindingSymbol(t, fa, "res")
	et, ok := resSym.Type.(*analysis.EnumType)
	if !ok {
		t.Fatalf("res type: got %T (%s), want *EnumType", resSym.Type, resSym.Type)
	}
	if et.Name != "Result" {
		t.Errorf("res enum name: got %s, want Result", et.Name)
	}
	if len(et.TypeArgs) != 2 {
		t.Fatalf("res TypeArgs: got %d, want 2", len(et.TypeArgs))
	}
	if et.TypeArgs[0] != analysis.TypeInt {
		t.Errorf("res T: got %s, want Int", et.TypeArgs[0])
	}
	if et.TypeArgs[1] != analysis.TypeString {
		t.Errorf("res E: got %s, want String", et.TypeArgs[1])
	}
}

// Generic args inside a binding annotation must each land in fa.References
// so hover on `Map`, `String`, and `Int` in `m: Map<String, Int> = …` works.
// Without walking the annotation, none of those positions resolve.
func TestCheckBinding_AnnotationGenericArgsAreReferenced(t *testing.T) {
	src := `fn main() {
  m: Map<String, Int> = Map.empty()
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)

	want := map[string]bool{"Map": false, "String": false, "Int": false}
	for _, sym := range fa.References {
		if sym == nil {
			continue
		}
		if _, ok := want[sym.Name]; ok {
			want[sym.Name] = true
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("no Reference recorded for `%s` in `Map<String, Int>` annotation", name)
		}
	}
}

// findBindingSymbol locates the local binding symbol with the given name and
// fails the test if it isn't found. Used by the binding-annotation tests
// to share lookup logic across multiple sub-scenarios.
func findBindingSymbol(t *testing.T, fa *analysis.FileAnalysis, name string) *analysis.Symbol {
	t.Helper()
	for _, sym := range fa.Definitions {
		if sym.Name == name && sym.Kind == analysis.SymbolBinding {
			if sym.Type == nil {
				t.Fatalf("%s has no type", name)
			}
			return sym
		}
	}
	t.Fatalf("no %s binding symbol found", name)
	return nil
}

// Empty Map default via `Map.empty()` should similarly type-check without
// an annotation. The body's `Map.put(...)` references constrain K and V
// once unification has a real placeholder to bind.
func TestCheckMapAccumulatorNoAnnotation(t *testing.T) {
	src := `fn f(pairs: List<(String, Int)>): Map<String, Int> {
  Iter.reduce(pairs, |acc = Map.empty(), (k, v)| Map.put(acc, k, v))
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// `break value` inside an iter-callback must match the callback's declared
// return type. For `Iter.loop(|n = 5| …)` the state type is inferred as `Int`
// from the default; `break "hello"` should therefore be a type error.
func TestCheckBreakValueMustMatchCallbackReturn(t *testing.T) {
	src := `fn main() {
  Iter.loop(|n = 5| {
    if n == 0 { break "hello" }
    n - 1
  })
}`
	_, errs := checkSourceWithStdlib(src)
	var found bool
	for _, e := range errs {
		// Expected message shape — the exact wording will be finalised by
		// the implementation; assert the specific type pair.
		if strings.Contains(e.Message, "Int") && strings.Contains(e.Message, "String") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected a break-value type mismatch (Int vs String), got %d errors:\n  %s",
			len(errs), strings.Join(msgs, "\n  "))
	}
}

// A zero-arg lambda `||` passed where `(S) -> S` is expected is coerced to
// silently accept-and-ignore the arg. `S` defaults to `Unit` when no
// `break <value>` pins it, so `Iter.loop(|| break)` resolves to a call
// returning `Unit` without asking the caller to write `|_ = 0| break`.
func TestCheckIterLoopWithZeroArgLambda(t *testing.T) {
	src := `
fn main() {
  x = Iter.loop(|| {
    break
  })
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)

	var xSym *analysis.Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "x" && sym.Kind == analysis.SymbolBinding {
			xSym = sym
			break
		}
	}
	if xSym == nil {
		t.Fatal("no x binding symbol found")
	}
	if xSym.Type == nil {
		t.Fatal("x has no inferred type")
	}
	if got := xSym.Type.String(); got != "Unit" {
		t.Errorf("x type: got %q, want %q", got, "Unit")
	}
}

// `loop` should return the state type S inferred from the callback's
// default value — `countdown = Iter.loop(|n = 5| ...)` should give
// `countdown: Int`. Without a declared signature on loop, `countdown`
// would have a nil type and hover would show just the name.
func TestCheckIterLoopReturnTypeFromDefault(t *testing.T) {
	src := `
fn main() {
  countdown = Iter.loop(|n = 5| {
    if n == 0 { break n }
    n - 1
  })
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)

	var countdownSym *analysis.Symbol
	for _, sym := range fa.Definitions {
		if sym.Name == "countdown" && sym.Kind == analysis.SymbolBinding {
			countdownSym = sym
			break
		}
	}
	if countdownSym == nil {
		t.Fatal("no countdown binding symbol found")
	}
	if countdownSym.Type == nil {
		t.Fatal("countdown has no inferred type — hover would show just the name")
	}
	if got := countdownSym.Type.String(); got != "Int" {
		t.Errorf("countdown type: got %q, want %q", got, "Int")
	}
}

func checkSourceWithStdlib(src string) (*analysis.FileAnalysis, []analysis.TypeError) {
	src = withStdlibTestImports(src)
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	// Lower type-body items and `for` blocks into
	// top-level impl blocks so the BuildTypes / CheckTypes / CheckDeriveBounds
	// passes below see the impls. BuildFileWithStdlib lowers on its own internal
	// copy; the passes receive this same slice, so it must be lowered too.
	nodes, _ = analysis.LowerDerives(nodes)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	// AttachStdlibProjectImpls builds a stdlib-only ProjectImplIndex
	// from lib.Files and assigns it as fa.ProjectImpls. Post-stdlib-
	// globals-retirement (Task 8), the checker's implsContext /
	// typeImplementsInterface consult ProjectImpls for cross-file
	// stdlib conformances; without this attach the single-file path
	// would miss "Int impl Display" etc. and surface spurious
	// interface-bound errors on routine io.inspect calls.
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	// Re-run CheckDeriveBounds now that ProjectImpls is attached. The
	// first call (inside BuildFileWithStdlib's buildModule) saw a nil
	// ProjectImpls and short-circuited; the re-run lands the @derive
	// payload conformance recordings (e.g. (Float, Comparable) from
	// `@derive Comparable enum Shape { Circle Float }`) on
	// fa.ImplManifest so downstream consumers see them.
	_ = analysis.CheckDeriveBounds(fa, nodes)
	// Collect builder-phase errors first, then the per-pass returns.
	// Omitting fa.TypeErrors here previously made this helper blind to
	// redeclaration and other builder-phase diagnostics.
	all := append([]analysis.TypeError(nil), fa.TypeErrors...)
	all = append(all, analysis.BuildTypes(fa, nodes)...)
	all = append(all, analysis.CheckTypes(fa, nodes)...)
	all = append(all, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	return fa, all
}

func withStdlibTestImports(src string) string {
	imports := make([]string, 0, 8)
	addModule := func(name string) {
		prefix := "std/" + name
		if hasQualifierUse(src, name) && !hasStdlibModuleNamespaceImport(src, name) {
			imports = append(imports, "import "+prefix)
		}
	}
	addModule("float")
	addModule("int")
	addModule("iter")
	addModule("maps")
	addModule("maybe")
	addModule("ranges")
	addModule("results")
	addModule("sets")
	addModule("strings")
	addModule("vectors")
	if strings.Contains(src, "codepoints.") && !strings.Contains(src, "import std/codepoints") {
		imports = append(imports, "import std/codepoints")
	}
	if len(imports) == 0 {
		return src
	}
	return strings.Join(imports, "\n") + "\n\n" + src
}

func hasStdlibModuleNamespaceImport(src, name string) bool {
	modulePath := "std/" + name
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "import ")
		switch {
		case line == modulePath:
			return true
		case strings.HasPrefix(line, modulePath+" as "):
			return true
		case strings.HasPrefix(line, modulePath+".{self"):
			return true
		}
	}
	return false
}

func hasQualifierUse(src, name string) bool {
	needle := name + "."
	for offset := 0; ; {
		idx := strings.Index(src[offset:], needle)
		if idx < 0 {
			return false
		}
		idx += offset
		if idx == 0 || (!isIdentChar(src[idx-1]) && src[idx-1] != '/') {
			return true
		}
		offset = idx + len(needle)
	}
}

func isIdentChar(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func expectNoStdlibErrors(t *testing.T, errs []analysis.TypeError) {
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

func withoutUnusedBindingErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		out = append(out, e)
	}
	return out
}

func expectStdlibError(t *testing.T, errs []analysis.TypeError, substr string) {
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

func TestCheckIterLoopInference(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): Int {
    Iter.loop(|n = 5| {
        if n == 0 { break n }
        n - 1
    })
}
`)
	expectNoStdlibErrors(t, errs)
}

// Generic inference on Iter.map plus an explicit materialize step.
func TestCheckIterMapInference(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<Int> {
    Iter.map([1, 2, 3], |x| x * 2) |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
}

// Break-in-lambda behavior on Iter.map (a callback-slot function).
func TestCheckIterMapWithBreak(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<Int> {
    Iter.map([1, 2, 3, 4, 5], |x| {
        if x == 4 { break }
        x * 10
    })
    |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckIterReduceWithDefault(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): Int {
    Iter.reduce([1, 2, 3], |total = 0, item| {
        total + item
    })
}
`)
	expectNoStdlibErrors(t, errs)
}

// Generic inference on Iter.filter plus an explicit materialize step.
func TestCheckIterFilterInference(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<Int> {
    Iter.filter([1, 2, 3], |x| x > 1) |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
}

// Migrated from Iter.each — tests same continue-in-lambda behavior on lists.each.
func TestCheckIterEachWithContinue(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): Unit {
    Iter.each([1, 2, 3], |x| {
        if x == 2 { continue }
    })
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckIterLoopBindingType(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): Int {
    countdown = Iter.loop(|n = 5| {
        if n == 0 { break n }
        n - 1
    })
    countdown
}
`)
	expectNoStdlibErrors(t, errs)
}

// Pipe-chain generic inference on the lazy Iter.filter / Iter.map adapters
// feeding the Iter.reduce terminal.
func TestGenericCallSite_PipeChainStdlib(t *testing.T) {
	fa, errs := checkSourceWithStdlib(`
fn f(): Int {
    result = [1, 2, 3]
        |> Iter.filter(|x| x > 1)
        |> Iter.map(|x| x * 10)
        |> Iter.reduce(|acc = 0, x| acc + x)
    result
}
`)
	expectNoStdlibErrors(t, errs)

	// Check that Iter.filter has instantiated CallType
	var filterSym *analysis.Symbol
	for _, sym := range fa.References {
		if sym.Name == "filter" {
			filterSym = sym
			break
		}
	}
	if filterSym == nil {
		// List all references for debugging
		for pos, sym := range fa.References {
			if sym.Name == "filter" {
				t.Logf("found filter ref at line=%d col=%d", pos.Line, pos.Col)
			}
		}
		t.Fatal("expected reference to filter on line 4")
	}
	if filterSym.CallType == nil {
		t.Fatal("expected CallType on piped Iter.filter call")
	}
	ft, ok := filterSym.CallType.(*analysis.FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", filterSym.CallType)
	}
	if ft.Return.String() != "Iter<Int>" {
		t.Errorf("expected return Iter<Int>, got %v", ft.Return)
	}
}

// Nested enum patterns with tuple destructure should type-check cleanly:
// `Some((n, s))` must bind `n: Int` and `s: String` against `Maybe<(Int, String)>`.
func TestCheckerAcceptsNestedEnumPatternTupleDestructure(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(x: Maybe<(Int, String)>): String {
    case x {
        Some((n, s)) -> s
        None -> ""
    }
}
`)
	expectNoStdlibErrors(t, errs)
}

// Variant-inside-variant patterns: `Some(Ok(n))` against `Maybe<Result<Int, String>>`.
func TestCheckerAcceptsNestedEnumPatternVariantInsideVariant(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(x: Maybe<Result<Int, String>>): Int {
    case x {
        Some(Ok(n)) -> n
        Some(Err(_)) -> -1
        None -> 0
    }
}
`)
	expectNoStdlibErrors(t, errs)
}

// Literal patterns inside variants: `Some(42)` matches a literal against the
// inner Int payload — must type-check without error.
func TestCheckerAcceptsNestedEnumPatternLiteral(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(x: Maybe<Int>): String {
    case x {
        Some(42) -> "match"
        Some(_) -> "other"
        None -> "none"
    }
}
`)
	expectNoStdlibErrors(t, errs)
}

// Removed: TestMapOnHashmapDoesNotCorruptCallType
//
// This test asserted that Iter.map coerced Map<K, V> to List<(K, V)> when
// inferring its generic param, and that a failing second call site didn't
// corrupt the first call's resolved CallType. With pure-Nomi iter.* retired
// in Phase C, lists.map takes List<T> strictly — there is no map-to-list
// coercion to test, so the test's premise is moot. The general "don't
// corrupt CallType across call sites" property is covered by other generic
// inference tests in the checker suite.

// Interface-typed struct fields (e.g. ProbeIter { source: Iter<T>, ... }) —
// the checker validates that the value assigned to an interface-typed field
// comes from a concrete type that actually impl the interface.

func TestCheckerRejectsNonIterValueInIterField(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
struct ProbeIter {
    source: Iter<Int>
}

fn broken(): ProbeIter {
    ProbeIter{source: 42}
}
`)
	if len(errs) == 0 {
		t.Fatal("expected type error about Int not implementing iter")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "iter") || strings.Contains(e.Message, "source") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected error mentioning iter or source; got:\n  %s", strings.Join(msgs, "\n  "))
	}
}

func TestCheckerAcceptsValidMapIterConstruction(t *testing.T) {
	// List impl Iter, so this should be accepted in an interface-typed field.
	_, errs := checkSourceWithStdlib(`
struct ProbeIter {
    source: Iter<Int>
}

fn good(): ProbeIter {
    ProbeIter{source: [1, 2, 3]}
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerAcceptsIterFieldWithWrapperSource(t *testing.T) {
	// Iter.map returns an Iter, which can itself fill an Iter-typed field.
	_, errs := checkSourceWithStdlib(`
struct ProbeIter {
    source: Iter<Int>
}

fn nested(): ProbeIter {
    inner = Iter.map([1, 2, 3], |x| x * 2)
    ProbeIter{source: inner}
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerRejectsInterfaceReturnTypeArgMismatch(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn broken<T, U>(source: Iter<T>): Iter<U> {
    source
}

fn explicit_broken<T, U>(source: Iter<T>): Iter<U> {
    return source
}
`)
	if len(errs) == 0 {
		t.Fatal("expected return type mismatch for Iter<T> returned as Iter<U>")
	}
	foundImplicit := false
	foundExplicit := false
	for _, e := range errs {
		if strings.Contains(e.Message, "expected Iter<U>, got Iter<T>") {
			if e.Line == 3 { // the body's tail expression, `source`
				foundImplicit = true
			}
			if e.Line == 7 {
				foundExplicit = true
			}
		}
	}
	if !foundImplicit || !foundExplicit {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected implicit and explicit Iter<T>/Iter<U> return errors; got:\n  %s", strings.Join(msgs, "\n  "))
	}
}

func TestCheckerRejectsIterToMapOnNonPairElements(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): Map<Int, Int> {
    Iter.to_map([1, 2, 3])
}
`)
	if len(errs) == 0 {
		t.Fatal("expected type error about Int elements vs (K, V) requirement")
	}
}

func TestCheckerRejectsStringJoinOnNonStringElements(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): String {
    String.join([1, 2, 3], ", ")
}
`)
	// The literal is checked against the parameter's Iter<String>, so the
	// error names the element that does not fit.
	if len(errs) == 0 {
		t.Fatal("String.join over List<Int> was admitted; want an element type error")
	}
	if !strings.Contains(errs[0].Message, "list element type mismatch: expected String, got Int") {
		t.Fatalf("String.join over List<Int>: got %q, want the element type error", errs[0].Message)
	}
	_, errs = checkSourceWithStdlib(`
fn f(xs: List<Int>): String {
    String.join(xs, ", ")
}
`)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "expected Iter<String>, got List<Int>") {
		t.Fatalf("String.join over a List<Int> binding: got %v, want the Iter<String> argument error", errs)
	}
}

// Iter.to_string concatenated strings while every other to_string is Display,
// so it was removed for String.join. The removed member must stay an error.
func TestCheckerRejectsTheRemovedIterToString(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): String {
    Iter.to_string(["a", "b"])
}

fn g(): String {
    ["a"] |> Iter.to_string()
}
`)
	if len(errs) != 2 {
		t.Fatalf("Iter.to_string: got %d errors, want 2: %v", len(errs), errs)
	}
	for _, e := range errs {
		if e.Message != "type 'Iter' has no member 'to_string'" {
			t.Fatalf("Iter.to_string: got %q, want the unknown-member error", e.Message)
		}
	}
}

// The generic conversions live on Iter (Iter.to_list, Iter.to_vector,
// Iter.to_set, Iter.to_map), so the collection owners have no conversion of
// their own, called directly or as a pipe stage.
func TestCheckerRejectsOwnerCollectionConversions(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"Vector.to_list", "fn f(): List<Int> {\n    Vector.to_list(#[1])\n}\n", "type 'Vector' has no member 'to_list'"},
		{"Vector.from_list", "fn f(): Vector<Int> {\n    Vector.from_list([1])\n}\n", "type 'Vector' has no member 'from_list'"},
		{"Set.from_list", "fn f(): Set<Int> {\n    Set.from_list([1])\n}\n", "type 'Set' has no member 'from_list'"},
		{"Map.from_list", "fn f(): Map<String, Int> {\n    Map.from_list([(\"a\", 1)])\n}\n", "type 'Map' has no member 'from_list'"},
		{"String.graphemes", "fn f(): List<String> {\n    String.graphemes(\"ab\")\n}\n", "type 'String' has no member 'graphemes'"},
		{"piped Set.from_list", "fn f(): Set<Int> {\n    [1] |> Set.from_list()\n}\n", "type 'Set' has no member 'from_list'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(c.src)
			if len(errs) == 0 {
				t.Fatalf("%s is accepted; want %q", c.name, c.want)
			}
			for _, e := range errs {
				if e.Message == c.want {
					return
				}
			}
			t.Fatalf("%s: got %v, want %q", c.name, errs, c.want)
		})
	}
}

func TestCheckerAcceptsIterToMapOnValidPairs(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): Map<String, Int> {
    Iter.to_map([("a", 1), ("b", 2)])
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerAcceptsStringJoinOverAnyIterOfStrings(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<String> {
    [
        String.join(["a", "b", "c"], ", "),
        String.join(["a", "b"]),
        #{"a"} |> String.join("-"),
        #["a", "b"] |> String.join(),
        "héllo" |> Iter.map(String.to_upper) |> String.join(),
        1..=3 |> Iter.map(Int.to_string) |> String.join("-"),
    ]
}
`)
	expectNoStdlibErrors(t, errs)
}

// ---------------------------------------------------------------------------
// Import aliasing
// ---------------------------------------------------------------------------

func TestCheckerResolvesSelectiveImportAlias(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
import std/maybe.{Maybe as Opt}
import std/maybe.Maybe.{Some as Just, None as Nothing}

fn f(): Opt<Int> {
    Just(42)
}

fn g(): Opt<Int> {
    Nothing
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerMixedPartialAliases(t *testing.T) {
	// Some imported names are aliased, others are not, in one import list.
	// Aliased variant imports stay reachable at construction sites and
	// pattern sites (`Just(1)` constructs Some; `Just(n)` matches Some).
	_, errs := checkSourceWithStdlib(`
import std/maybe.Maybe.{Some as Just}

fn f(): Maybe<Int> {
    case Just(1) {
        Just(n) -> Some(n)
        None -> None
    }
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerAcceptsImportedVariantPattern(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
import std/comparable: Ordering.{Equal, Greater, Less}

fn f(ordering: Ordering): Int {
    case ordering {
        Less -> -1
        Equal -> 0
        Greater -> 1
    }
}
`)
	expectNoStdlibErrors(t, errs)
}

// A top-level definition that shares a name with an imported module is a
// TestCheckerFieldAccessFallback_StructFieldStillWins: when the shadowing
// binding is a struct with the requested field, the checker must NOT fall
// back to the module — the struct field type (Int here) must win over the
// module export (a function).
func TestCheckerFieldAccessFallback_StructFieldStillWins(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
import std/maps

struct Callbacks { empty: Int }

fn f(): Int {
    maps = Callbacks { empty: 99 }
    maps.empty
}
`)
	expectNoStdlibErrors(t, errs)
}

// TestStdlibFilesTypeCheck ensures stdlib .nomi files pass both BuildTypes
// and CheckTypes with no errors. Regression guard for the fold+reverse /
// to_list type-inference issues fixed in range, string, and map — the LSP
// runs CheckTypes on every opened file, so broken stdlib shows as red
// squiggles in the editor.
func TestStdlibFilesTypeCheck(t *testing.T) {
	lib := std.Load()
	modules := []string{"maybe", "result", "iterator", "iter", "list", "vector", "string", "map", "range", "io", "struct"}
	for _, name := range modules {
		content, ok := std.ReadFile(name)
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			tokens := lexer.Lex(string(content))
			nodes, _ := parser.ParseWithRecovery(tokens)
			fa := analysis.BuildFileWithStdlibAtPath(nodes, lib.Primitives, lib.Modules, "", nil, "std/"+name+".nomi")
			// AttachStdlibProjectImpls — see checker_stdlib_test.go's
			// checkSourceWithStdlib for the rationale.
			analysis.AttachStdlibProjectImpls(fa, lib.Files)
			typeErrs := analysis.BuildTypes(fa, nodes)
			checkErrs := analysis.CheckTypes(fa, nodes)
			iterErrs := analysis.AnalyzeIterSensitivity(fa, nodes)
			errs := append(typeErrs, checkErrs...)
			errs = append(errs, iterErrs...)
			if len(errs) > 0 {
				msgs := make([]string, len(errs))
				for i, e := range errs {
					msgs[i] = e.Error()
				}
				t.Fatalf("stdlib/%s.nomi has %d type errors:\n  %s", name, len(errs), strings.Join(msgs, "\n  "))
			}
		})
	}
}

// TestRangeToListReturnsListInt is the focused regression for the original
// bug report (Iter.to_list body returning List<T> instead of List<Int>).
func TestRangeToListReturnsListInt(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<Int> { Iter.to_list(0..5) }
`)
	expectNoStdlibErrors(t, errs)
}

func TestGenericRangeLiterals(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn ints(): Range<Int> { 0..5 }
fn strings(): Range<String> { "a".."z" }
fn floats(): Range<Float> { 0.0..=1.0 }
fn contains_string(): Bool { Range.contains?("a".."m", "h") }
fn from_bindings(a: Int, b: Int): Range<Int> { a..b }
`)
	expectNoStdlibErrors(t, errs)
}

func TestNonIntRangeDoesNotMaterializeToList(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<String> { Iter.to_list("a".."z") }
`)
	if len(errs) == 0 {
		t.Fatalf("expected Range<String> to fail Iter materialization")
	}
	if !strings.Contains(errs[0].Message, "does not implement Iter") {
		t.Fatalf("expected Iter error, got: %v", errs)
	}
}

func TestCodepointRangeMaterializesToList(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(start: Codepoint, end: Codepoint): List<Codepoint> {
  Iter.to_list(start..=end)
}
`)
	expectNoStdlibErrors(t, errs)
}

// TestStringToListReturnsListString — same pattern, concrete element type: a
// String iterates by grapheme cluster, so its list is List<String>.
func TestStringToListReturnsListString(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<String> { Iter.to_list("abc") }
`)
	expectNoStdlibErrors(t, errs)
}

// TestMapKeysValuesToListInfer — Map.keys/values/to_list each return a
// materialized list with correctly inferred element types.
func TestMapKeysValuesToListInfer(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn ks(m: Map<String, Int>): List<String> { Map.keys(m) }
fn vs(m: Map<String, Int>): List<Int> { Map.values(m) }
fn es(m: Map<String, Int>): List<(String, Int)> { Iter.to_list(m) }
`)
	expectNoStdlibErrors(t, errs)
}

// TestListMapFilterZipInfer — Iter.map, Iter.filter, Iter.zip return properly
// typed lists once materialized with Iter.to_list().
func TestListMapFilterZipInfer(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn m(): List<Int> { Iter.map([1, 2, 3], |x| x * 2) |> Iter.to_list() }
fn f(): List<Int> { Iter.filter([1, 2, 3], |x| x > 1) |> Iter.to_list() }
fn z(): List<(Int, String)> { Iter.zip([1, 2], ["a", "b"]) |> Iter.to_list() }
`)
	expectNoStdlibErrors(t, errs)
}

// TestListGroupByTypeInfers — lists.group_by previously failed to type-check
// because the `None -> []` branch in a case expression had an unbound element
// type. Rewriting with `Maybe.with_default(..., [])` gives the checker the
// expected type context (T from Maybe<T>) to resolve the empty literal.
func TestListGroupByTypeInfers(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): Map<Int, List<Int>> { Iter.group_by([1, 2, 3], |it| it % 2) }
`)
	expectNoStdlibErrors(t, errs)
}

// TestListZipTypeInfers — Iter.zip |> Iter.to_list() infers its element type.
// The public return type is Iter<(T, U)>, so the element type should flow
// through without exposing the private ZipIter wrapper.
func TestListZipTypeInfers(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<(Int, String)> { Iter.zip([1, 2, 3], ["a", "b"]) |> Iter.to_list() }
`)
	expectNoStdlibErrors(t, errs)
}

// TestIterToListOnZipIter — Iter.to_list on Iter.zip's Iter return must
// type-check to List<(Int, Int)>.
func TestIterToListOnZipIter(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<(Int, Int)> {
    Iter.to_list(Iter.zip(Iter.from(0), Iter.from(10)))
}
`)
	expectNoStdlibErrors(t, errs)
}

// TestIterToListOnMapIter — Iter.to_list on Iter.map's Iter return must
// type-check to List<U> (the output type of the map function).
func TestIterToListOnMapIter(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<Int> {
    Iter.to_list(Iter.map([1, 2, 3], |it| it * 2))
}
`)
	expectNoStdlibErrors(t, errs)
}

// TestIterToListOnFilterIter — Iter.to_list on Iter.filter's Iter return must
// type-check to List<T>.
func TestIterToListOnFilterIter(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<Int> {
    Iter.to_list(Iter.filter([1, 2, 3], |it| it > 1))
}
`)
	expectNoStdlibErrors(t, errs)
}

// TestCheckerUserDefinedIterElementType — a user-defined iterator type
// (non-stdlib) with `impl Iter for T` must have its element type resolved
// via file-local FileAnalysis.ImplTypeArgs, not just the project-level
// fa.ProjectImpls.ImplTypeArgs union.
func TestCheckerUserDefinedIterElementType(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
struct Counter { n: Int }

impl Iter for Counter {
    fn each_while(c: Counter, yield: (Int) -> Bool): Bool {
        if c.n <= 0 {
            True
        } else {
            if yield(c.n) {
                each_while(Counter{n: c.n - 1}, yield)
            } else {
                False
            }
        }
    }
}

fn f(): List<Int> {
    Iter.to_list(Counter{n: 5})
}
`)
	expectNoStdlibErrors(t, errs)
}

// TestCheckerUserDefinedGenericIterElementType — a generic user-defined
// iterator exercises the substitution path: the impl's formal type params must
// be substituted with the use-site's concrete TypeArgs.
func TestCheckerUserDefinedGenericIterElementType(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
struct Boxed<T> { val: T; rest: Int }

impl Iter for Boxed<T> {
    fn each_while(b: Boxed<T>, yield: (T) -> Bool): Bool {
        if b.rest <= 0 {
            True
        } else {
            if yield(b.val) {
                each_while(Boxed{val: b.val, rest: b.rest - 1}, yield)
            } else {
                False
            }
        }
    }
}

fn f(): List<String> {
    Iter.to_list(Boxed{val: "x", rest: 3})
}
`)
	expectNoStdlibErrors(t, errs)
}

// TestCheckerUserDefinedIterElementTypeMismatch — when the element type of
// a user-defined iterator is solved via file-local ImplTypeArgs, assigning its
// to_list result to the wrong List<…> must error. A missing template would fall
// back to the permissive no-binding path, silently accepting mismatches.
func TestCheckerUserDefinedIterElementTypeMismatch(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
struct Counter { n: Int }

impl Iter for Counter {
    fn each_while(c: Counter, yield: (Int) -> Bool): Bool {
        if c.n <= 0 {
            True
        } else {
            if yield(c.n) {
                each_while(Counter{n: c.n - 1}, yield)
            } else {
                False
            }
        }
    }
}

fn f(): List<String> {
    Iter.to_list(Counter{n: 5})
}
`)
	if len(errs) == 0 {
		t.Fatalf("expected type error for List<String> = to_list(counter) (Int elements), got none")
	}
}

func TestCheckerExpandsTypeAliasForLambdaInference(t *testing.T) {
	_, errs := checkSourceWithStdlib(`


typealias Handler (String, String) -> Result<String, String>

fn run(handler: Handler): Result<String, String> {
    handler("request", "/home")
}

fn main() {
    result = run(|method, path| Ok(method + " " + path))
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerExpandsTypeAliasInCall(t *testing.T) {
	// Pass a named fn that matches the alias's underlying type — should also work.
	_, errs := checkSourceWithStdlib(`


typealias Validator (String) -> Result<Int, String>

fn validate_length(_s: String): Result<Int, String> {
    Ok(0)
}

fn run_validator(v: Validator): Result<Int, String> {
    v("hello")
}

fn main(): Result<Int, String> {
    run_validator(validate_length)
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerNestedEnumDistinctTypeParams(t *testing.T) {
	// Maybe<T> and Result<T, E> both name their first type param "T".
	// Nesting one in the other must not conflate them.
	_, errs := checkSourceWithStdlib(`



fn f(): Maybe<Result<Int, String>> {
    Some(Err("boom"))
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestCheckerCaseOnNestedEnumBranchIsInt(t *testing.T) {
	_, errs := checkSourceWithStdlib(`



fn f(): Int {
    case Some(Err("boom")) {
        Some(Ok(n)) -> n
        Some(Err(_)) -> -1
        None -> 0
    }
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestPipeTakeStringJoin(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): String {
    Iter.repeat("ab") |> Iter.take(3) |> String.join()
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestDirectTakeStringJoin(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): String {
    x = Iter.take(Iter.repeat("ab"), 3)
    String.join(x)
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestPipeTakeIterToList(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<String> {
    Iter.repeat("ab") |> Iter.take(3) |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
}

// ---------------------------------------------------------------------------
// iter-sensitivity: static validation of break/continue context.
// ---------------------------------------------------------------------------

// Direct break in a lambda passed to Iter.map — VALID.
func TestCheckerBreakInMapLambda(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn f(): List<Int> {
    Iter.map([1, 2, 3], |x| if x > 10 { break x } else { x * 2 }) |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
}

// Break in a helper called from Iter.map — VALID (transitive).
func TestCheckerBreakInHelperCalledFromMap(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn helper(x: Int): Int {
    if x > 10 { break x } else { x * 2 }
}
fn f(): List<Int> {
    Iter.map([1, 2, 3], helper) |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
}

// Break in a helper called from non-iter context — INVALID.
func TestCheckerBreakInHelperCalledFromNonIter(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn helper(x: Int): Int {
    if x > 10 { break x } else { x * 2 }
}
fn f(): Int {
    helper(100)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected compile error: break/continue used outside iteration context")
	}
}

// Break in lists.sort_with's comparator — INVALID (comparator is not iter-callback).
func TestCheckerBreakInSortComparator(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
import std/comparable.{Ordering}
import std/comparable.Ordering.{Equal, Greater, Less}
fn f(): List<Int> {
    Iter.sort_with([3, 1, 2], |a, _b| if a > 100 { break Equal } else { Less })
}
`)
	if len(errs) == 0 {
		t.Fatal("expected compile error for break in sort comparator")
	}
}

// Break at top level — INVALID.
func TestCheckerBreakAtTopLevel(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn main() {
    break 0
}
`)
	if len(errs) == 0 {
		t.Fatal("expected compile error for break at top level")
	}
}

// Transitive: helper A calls helper B, B has break, A is called from iter — VALID.
func TestCheckerBreakInTransitiveHelper(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn inner(x: Int): Int {
    if x > 10 { break x } else { x + 1 }
}
fn outer(x: Int): Int {
    inner(x)
}
fn f(): List<Int> {
    Iter.map([1, 2, 3], outer) |> Iter.to_list()
}
`)
	expectNoStdlibErrors(t, errs)
}

// Transitive error: B has break, A calls B, A is called from non-iter — INVALID.
func TestCheckerBreakInTransitiveHelperCalledFromNonIter(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn inner(x: Int): Int {
    if x > 10 { break x } else { x + 1 }
}
fn outer(x: Int): Int {
    inner(x)
}
fn f(): Int {
    outer(5)
}
`)
	if len(errs) == 0 {
		t.Fatal("expected compile error (transitive)")
	}
}

func TestCheckNestedImportIsAllowed(t *testing.T) {
	src := `
fn use_list(): Int {
    Iter.count([1, 2, 3])
}

fn main(): Int { use_list() }
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// TestChecker_BareContextWithoutAppFieldErrors_WithStdlib: even when the
// stdlib's `Context` type is loaded (so ContextType is non-nil),
// referencing `context` bare must error. Context is read through an
// app field, `^context`.
func TestChecker_BareContextWithoutAppFieldErrors_WithStdlib(t *testing.T) {
	src := `
struct Config { context: Context }

fn main(): Context {
  context
}
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "context")
}

// TestChecker_AcceptsAppContextField: an application type's Context field is
// read as `App.context`, in a file with no boot as well, since the read
// names its type.
func TestChecker_AcceptsAppContextField(t *testing.T) {
	src := `

struct App {
  context: Context
}

fn read(): Context {
  App.context
}
`
	fa, _ := checkSourceWithStdlib(src)
	if len(fa.TypeErrors) != 0 {
		t.Fatalf("expected no type errors for App.context, got: %v", fa.TypeErrors)
	}
}

// An interface-impl method must not collide with a same-name ordinary
// function brought into scope by a selective import. Universal Debug
// synthesizes `impl Debug for A { fn inspect }` for every declared type;
// importing io's `inspect` selectively (`import std/io.inspect`) puts a
// function named `inspect` in the same scope, and the impl method's
// scope-define spuriously reported a redeclaration. They occupy different
// modules: bare `inspect(x)` -> the IO function; `Type.inspect(x)` /
// `Debug.inspect(x)` -> the method via the dispatch tables.
func TestCheckImplMethodDoesNotCollideWithImportedFunction(t *testing.T) {
	// Synthesized Debug (no explicit impl): universal Debug emits
	// `impl Debug for A { fn inspect }`, whose method name must not collide
	// with the selectively-imported io.inspect.
	src := `import std/io.inspect

struct A { n: Int }

fn main() {
  a = A{n: 1}
  inspect(a)
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)

	// Hand-written impl, and a non-Debug interface whose method name
	// collides with an imported function — both must be tolerated.
	src2 := `import std/io.{print}

interface Speak {
  fn print(s: self): String
}

struct Dog {
  name: String
}

impl Speak for Dog {
  fn print(s: Dog): String {
    s.name
  }
}

fn main() {
  print("hi")
}
`
	_, errs2 := checkSourceWithStdlib(src2)
	expectNoStdlibErrors(t, errs2)
}

func TestCheckBareImportedFunctionDoesNotBecomeDispatch(t *testing.T) {
	src := `import std/io.inspect

interface Greeting {
  fn greet(value: self): String
}

struct Guest {
  name: String
}

impl Greeting for Guest {
  fn greet(guest: Guest): String {
    "Welcome, " + guest.name + "!"
  }
}

fn main() {
  result = 42
  inspect(result)

  Guest{name: "Bob"}
  |> Greeting.greet()
  |> inspect()
}

`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}
