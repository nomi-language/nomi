package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Tests for the derive synthesis pass. These first tests exercise the
// validation gate (unknown protocol, duplicate, valid path); the sections
// below test the emitted impls per protocol.

// TestDeriveUnsupportedInterfaceErrors checks that `derive Widget for Foo`,
// where no derivable interface is named Widget, is an error that names it.
func TestDeriveUnsupportedInterfaceErrors(t *testing.T) {
	// The `derive Iface` validation (unknown protocol) now lives in
	// LowerDerives' second return value, not fa.TypeErrors.
	src := `struct Foo
    x: Int
end
derive Widget for Foo`

	tokens := lexer.Lex(src)
	nodes, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, errs := LowerDerives(nodes)
	if len(errs) == 0 {
		t.Fatal("expected error: derive Widget is not a supported protocol")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "`Widget` is not an interface derive can write") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected an error saying Widget is not derivable, got: %s", strings.Join(msgs, "\n  "))
	}
}

// TestDeriveDuplicateInterfaceErrors checks that `@derive Equatable, Equatable`
// on the same decl errors. Detects the duplicate within one decorator's args
// list — the stacked-decorator form (`@derive Eq @derive Eq`) shares the same
// per-decl seenIfaces map and is covered by the same code path.
func TestDeriveDuplicateInterfaceErrors(t *testing.T) {
	// Two `derive Equatable` lines on one type — the duplicate
	// conformance check lives in LowerDerives (message: "duplicate
	// `impl Equatable` conformance line").
	src := `struct Foo
    x: Int
end
derive Equatable for Foo
derive Equatable for Foo`

	tokens := lexer.Lex(src)
	nodes, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, errs := LowerDerives(nodes)
	if len(errs) == 0 {
		t.Fatal("expected error: duplicate derive Equatable")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "duplicate") && strings.Contains(e.Message, "Equatable") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected 'duplicate ... Equatable' error, got: %s", strings.Join(msgs, "\n  "))
	}
}

// TestDeriveAllFourValidatesClean checks that listing all four supported
// protocols on one decl produces no analyzer errors from the synthesis pass.
// The per-protocol sections below verify the emitted impls. This test pins
// the validation gate's happy path: validation accepts the four canonical names without complaint.
func TestDeriveAllFourValidatesClean(t *testing.T) {
	src := `struct Foo
    x: Int
end
derive Equatable for Foo
derive Hashable for Foo
derive Comparable for Foo
derive Debug for Foo`

	tokens := lexer.Lex(src)
	nodes, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, errs := LowerDerives(nodes)
	for _, e := range errs {
		// The four canonical protocols must validate cleanly: LowerDerives
		// emits no derive-validation error for any of them.
		if strings.Contains(e.Message, "derive impl") || strings.Contains(e.Message, "derivable") {
			t.Errorf("unexpected derive-pass error: %s", e.Error())
		}
	}
}

// ---------------------------------------------------------------------------
// Equatable synthesis tests
// ---------------------------------------------------------------------------

// synthMethodFromNode returns the FuncDef for an impl method matching (method,
// iface, typeName) when `n` is a block-form `impl Iface for T { fn method(...) }`
// carrying it — the only impl shape (`@derive`/universal-Debug synthesis and
// hand-written code both emit blocks). Interface/receiver matching is by base
// name so generic receivers (`Box<T>`) match typeName "Box". Returns nil when
// `n` doesn't match.
func synthMethodFromNode(n ast.Node, method, iface, typeName string) *ast.FuncDef {
	block, ok := n.(*ast.ImplBlock)
	if !ok || block.Interface == nil {
		return nil
	}
	if TypeExprBaseName(block.Interface) != iface {
		return nil
	}
	for _, item := range block.Items {
		fn, ok := item.(*ast.FuncDef)
		if ok && fn.Name == method && len(fn.Params) > 0 && TypeExprBaseName(block.Receiver) == typeName {
			return fn
		}
	}
	return nil
}

// synthMethodForSource parses + lowers the derives of src and returns the
// first synthesized impl method matching (method, iface, typeName), in either
// the block or decorator shape (see synthMethodFromNode).
func synthMethodForSource(t *testing.T, src, method, iface, typeName string) *ast.FuncDef {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Lower `derive Iface for T` into its impl block.
	nodes, lowerErrs := LowerDerives(nodes)
	if len(lowerErrs) > 0 {
		t.Fatalf("unexpected lowering errors: %v", lowerErrs)
	}
	for _, n := range nodes {
		if fn := synthMethodFromNode(n, method, iface, typeName); fn != nil {
			return fn
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Comparable synthesis tests
// ---------------------------------------------------------------------------

// synthCompareForSource locates the synthesized `impl Comparable for T { fn compare }`
// FuncDef whose first param's declared type matches typeName. Mirrors
// synthMethodForSource.
func synthCompareForSource(t *testing.T, src, typeName string) *ast.FuncDef {
	t.Helper()
	return synthMethodForSource(t, src, "compare", "Comparable", typeName)
}

// TestDeriveComparableOnStructSynthesizes pins the struct path: `@derive
// Comparable struct Point { x: Int; y: Int }` produces an `impl Comparable for Point {
// fn compare(a: Point, b: Point) -> Ordering }` whose body is the
// nested-case-on-Ordering shape:
//
//	case Comparable.compare(a.x, b.x) {
//	  Less -> Less
//	  Greater -> Greater
//	  Equal -> Comparable.compare(a.y, b.y)
//	}
//
// assertOrderingPattern checks that a synthesized Ordering pattern is the
// qualified `Ordering.<want>` form (a QualifiedType), not a bare variant —
// the checker rejects bare variants in pattern position.
func assertOrderingPattern(t *testing.T, variant ast.TypeExpr, want string) {
	t.Helper()
	qt, ok := variant.(*ast.QualifiedType)
	if !ok {
		t.Errorf("expected QualifiedType Ordering.%s pattern, got %T %v", want, variant, variant)
		return
	}
	if qt.Module != "Ordering" {
		t.Errorf("expected pattern module Ordering, got %q", qt.Module)
	}
	member, ok := qt.Member.(*ast.SimpleType)
	if !ok || member.Name != want {
		t.Errorf("expected pattern member %s, got %v", want, qt.Member)
	}
}

// assertOrderingValue checks that a synthesized value-position Ordering
// reference is the qualified `Ordering.<want>` form (FieldAccess on the
// `Ordering` TypeIdent), not a bare variant. Qualifying through the
// (prelude-exported) `Ordering` type is what keeps the variants themselves
// out of the prelude — synthesized derive code resolves them via the type,
// not a bare prelude binding.
func assertOrderingValue(t *testing.T, node ast.Node, want string) {
	t.Helper()
	fa, ok := node.(*ast.FieldAccess)
	if !ok {
		t.Errorf("expected FieldAccess Ordering.%s value, got %T %v", want, node, node)
		return
	}
	ti, ok := fa.Object.(*ast.TypeIdent)
	if !ok || ti.Name != "Ordering" {
		t.Errorf("expected value object TypeIdent(Ordering), got %T %v", fa.Object, fa.Object)
	}
	if fa.Field == nil || fa.Field.Name != want {
		t.Errorf("expected value field %s, got %v", want, fa.Field)
	}
}

func TestDeriveComparableOnStructSynthesizes(t *testing.T) {
	src := `struct Point
    x: Int
    y: Int
end
derive Comparable for Point`

	fn := synthCompareForSource(t, src, "Point")
	if fn == nil {
		t.Fatal("no synthesized Comparable impl (fn compare) found for Point")
	}
	if len(fn.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(fn.Params))
	}
	ret, ok := fn.ReturnTypeExpr.(*ast.SimpleType)
	if !ok || ret.Name != "Ordering" {
		t.Errorf("expected return type Ordering, got %v", fn.ReturnTypeExpr)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	// Outer Case scrutinee: Comparable.compare(a.x, b.x).
	outer, ok := es.Expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected outer Case, got %T", es.Expr)
	}
	c, ok := outer.Value.(*ast.Call)
	if !ok {
		t.Fatalf("outer scrutinee: expected Call, got %T", outer.Value)
	}
	fa, ok := c.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "compare" {
		t.Errorf("outer scrutinee callee: expected FieldAccess(compare), got %T %v", c.Func, c.Func)
	}
	ti, ok := fa.Object.(*ast.TypeIdent)
	if !ok || ti.Name != "Comparable" {
		t.Errorf("outer scrutinee callee object: expected TypeIdent(Comparable), got %T %v", fa.Object, fa.Object)
	}
	// Three branches: Less, Greater, Equal — Equal recurses to
	// Comparable.compare(a.y, b.y).
	if len(outer.Branches) != 3 {
		t.Fatalf("expected 3 branches (Less/Greater/Equal), got %d", len(outer.Branches))
	}
	// First two branches: patterns are QUALIFIED `Ordering.Less` /
	// `Ordering.Greater` (the checker rejects bare variants in pattern
	// position; bare patterns here failed only at `nomi run` time); bodies
	// are likewise qualified value-position `Ordering.Less` / `Ordering.Greater`.
	for i, want := range []string{"Less", "Greater"} {
		br := outer.Branches[i]
		ep, ok := br.Pattern.(*ast.EnumPattern)
		if !ok {
			t.Errorf("branch %d: expected EnumPattern, got %T", i, br.Pattern)
			continue
		}
		assertOrderingPattern(t, ep.Variant, want)
		assertOrderingValue(t, br.Body, want)
	}
	// Third branch: Equal -> Comparable.compare(a.y, b.y).
	last := outer.Branches[2]
	ep, ok := last.Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("Equal branch: expected EnumPattern, got %T", last.Pattern)
	}
	assertOrderingPattern(t, ep.Variant, "Equal")
	// 1-pair recurse → bare Comparable.compare call (no nested case).
	c2, ok := last.Body.(*ast.Call)
	if !ok {
		t.Fatalf("Equal branch body: expected Call (Comparable.compare), got %T", last.Body)
	}
	fa2, ok := c2.Func.(*ast.FieldAccess)
	if !ok || fa2.Field == nil || fa2.Field.Name != "compare" {
		t.Errorf("Equal branch body callee: expected FieldAccess(compare), got %T %v", c2.Func, c2.Func)
	}
}

// TestDeriveComparableOnEmptyStructIsEqual pins the 0-field special case:
// `@derive Comparable struct Empty {}` yields a body of bare `Equal`.
func TestDeriveComparableOnEmptyStructIsEqual(t *testing.T) {
	src := `struct Empty end
derive Comparable for Empty`

	fn := synthCompareForSource(t, src, "Empty")
	if fn == nil {
		t.Fatal("no synthesized fn for Empty")
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	assertOrderingValue(t, es.Expr, "Equal")
}

// TestDeriveComparableOnEnumSynthesizes pins the enum path: the outer case
// dispatches on `a` with one branch per variant; each branch body is a
// nested case on `b` whose arms yield Greater for j < i, compare-payloads
// for j == i, and a wildcard `_ -> Less` for j > i (omitted on the last
// variant where the explicit arms are exhaustive).
func TestDeriveComparableOnEnumSynthesizes(t *testing.T) {
	src := `enum Color
    Red
    Green
    Blue
end
derive Comparable for Color`

	fn := synthCompareForSource(t, src, "Color")
	if fn == nil {
		t.Fatal("no synthesized fn for Color")
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	outer, ok := es.Expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected outer Case, got %T", es.Expr)
	}
	id, ok := outer.Value.(*ast.Ident)
	if !ok || id.Name != "a" {
		t.Errorf("outer case scrutinee: expected Ident(a), got %T %v", outer.Value, outer.Value)
	}
	if len(outer.Branches) != 3 {
		t.Fatalf("expected 3 outer branches (one per variant), got %d", len(outer.Branches))
	}
	// First variant Red (i=0): inner case has 1 explicit arm (Red == Red →
	// Equal payload, here bare → Equal) plus wildcard `_ -> Less` for the
	// remaining 2 variants.
	red, ok := outer.Branches[0].Body.(*ast.Case)
	if !ok {
		t.Fatalf("Red branch body: expected nested Case, got %T", outer.Branches[0].Body)
	}
	if len(red.Branches) != 2 {
		t.Errorf("Red inner case: expected 2 branches (Red + wildcard), got %d", len(red.Branches))
	}
	if _, ok := red.Branches[1].Pattern.(*ast.WildcardPattern); !ok {
		t.Errorf("Red inner last branch: expected WildcardPattern, got %T", red.Branches[1].Pattern)
	}
	assertOrderingValue(t, red.Branches[1].Body, "Less")
	// Second variant Green (i=1): inner case has Red->Greater (j<i),
	// Green->Equal (j==i, bare), wildcard->Less (j>i).
	green, ok := outer.Branches[1].Body.(*ast.Case)
	if !ok {
		t.Fatalf("Green branch body: expected nested Case, got %T", outer.Branches[1].Body)
	}
	if len(green.Branches) != 3 {
		t.Errorf("Green inner case: expected 3 branches (Red + Green + wildcard), got %d", len(green.Branches))
	}
	assertOrderingValue(t, green.Branches[0].Body, "Greater")
	// Third variant Blue (i=2, last): no wildcard — 3 explicit arms cover
	// everything.
	blue, ok := outer.Branches[2].Body.(*ast.Case)
	if !ok {
		t.Fatalf("Blue branch body: expected nested Case, got %T", outer.Branches[2].Body)
	}
	if len(blue.Branches) != 3 {
		t.Errorf("Blue inner case: expected 3 explicit branches (Red + Green + Blue, no wildcard), got %d", len(blue.Branches))
	}
	for i, want := range []string{"Greater", "Greater", "Equal"} {
		assertOrderingValue(t, blue.Branches[i].Body, want)
	}
}

// TestDeriveComparableOnDistinctTypeSynthesizes pins the distinct-type
// path: `type Id Int` produces unwrap-then-compare:
// `Id(av) = a; Id(bv) = b; Comparable.compare(av, bv)`.
func TestDeriveComparableOnDistinctTypeSynthesizes(t *testing.T) {
	src := `type Id Int
derive Comparable for Id`

	fn := synthCompareForSource(t, src, "Id")
	if fn == nil {
		t.Fatal("no synthesized fn for Id")
	}
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("expected 3 stmts (2 destructures + 1 compare), got %d", len(fn.Body.Stmts))
	}
	for i := 0; i < 2; i++ {
		if head := distinctUnwrapHead(fn.Body.Stmts[i]); head != "Id" {
			t.Errorf("stmt %d: expected `Id(...) = ...`, got %#v", i, fn.Body.Stmts[i])
		}
	}
	es, ok := fn.Body.Stmts[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt 2: expected ExprStmt, got %T", fn.Body.Stmts[2])
	}
	c, ok := es.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("stmt 2 expr: expected Call, got %T", es.Expr)
	}
	fa, ok := c.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "compare" {
		t.Errorf("stmt 2 callee: expected FieldAccess(compare), got %T %v", c.Func, c.Func)
	}
	ti, ok := fa.Object.(*ast.TypeIdent)
	if !ok || ti.Name != "Comparable" {
		t.Errorf("stmt 2 callee object: expected TypeIdent(Comparable), got %T %v", fa.Object, fa.Object)
	}
}

// TestDeriveComparableOnZeroSizedDistinctIsEqual pins zero-sized distincts:
// `type Expired` (no inner) yields a body of bare `Equal`.
func TestDeriveComparableOnZeroSizedDistinctIsEqual(t *testing.T) {
	src := `type Expired
derive Comparable for Expired`

	fn := synthCompareForSource(t, src, "Expired")
	if fn == nil {
		t.Fatal("no synthesized fn for Expired")
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	assertOrderingValue(t, es.Expr, "Equal")
}

// TestDeriveComparableOnPositionalArity2VariantUsesNestedTuple pins the
// arity-≥2 positional-variant case for Comparable. The synthesized
// variant pattern's payload must be a *ast.TuplePattern with Flat ==
// false; the analyzer rejects flat-form variant patterns. Comparable uses
// enumVariantPattern, so the canonical nested-tuple shape is inherited —
// this test pins it.
func TestDeriveComparableOnPositionalArity2VariantUsesNestedTuple(t *testing.T) {
	src := `enum Pair
    Both(Int, Int)
    None
end
derive Comparable for Pair`

	fn := synthCompareForSource(t, src, "Pair")
	if fn == nil {
		t.Fatal("no synthesized fn for Pair")
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	outer, ok := es.Expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected outer Case, got %T", es.Expr)
	}
	var bothBranch *ast.CaseBranch
	for i := range outer.Branches {
		br := &outer.Branches[i]
		ep, ok := br.Pattern.(*ast.EnumPattern)
		if !ok {
			continue
		}
		qt, ok := ep.Variant.(*ast.QualifiedType)
		if !ok {
			continue
		}
		mem, ok := qt.Member.(*ast.SimpleType)
		if !ok || mem.Name != "Both" {
			continue
		}
		bothBranch = br
		break
	}
	if bothBranch == nil {
		t.Fatal("outer Case has no branch for variant Both")
	}
	bothPat, ok := bothBranch.Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("Both pattern: expected EnumPattern, got %T", bothBranch.Pattern)
	}
	tup, ok := bothPat.Payload.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("Both payload: expected *ast.TuplePattern, got %T", bothPat.Payload)
	}
	if len(tup.Patterns) != 2 {
		t.Errorf("Both payload arity: expected 2 bindings, got %d", len(tup.Patterns))
	}
}

// ---------------------------------------------------------------------------
// Derives that write no function
// ---------------------------------------------------------------------------

// TestDeriveByShapeWritesABlockWithNoFunction: `derive Equatable`,
// `derive Hashable`, `derive Debug` and `derive Display` lower to an impl
// block of the interface that writes no function, for every shape a derive
// takes (a struct, an empty struct, an enum, a distinct, a marker, a
// generic struct, a host type), in the synthesized band, with its header
// at the derive and the receiver applied to a generic type's parameters,
// each bounded by the interface where the derive implies the bound
// (Equatable, Hashable and Display). The interface answers for such a type
// by its shape (derivesByShape).
func TestDeriveByShapeWritesABlockWithNoFunction(t *testing.T) {
	for _, iface := range []string{"Equatable", "Hashable", "Debug", "Display"} {
		for _, c := range []struct{ name, src, recv string }{
			{"Point", "struct Point\n    x: Int\nend\n", "Point"},
			{"Empty", "struct Empty\nend\n", "Empty"},
			{"Shape", "enum Shape\n    Dot\n    Circle(Float)\n    Rect{w: Int}\nend\n", "Shape"},
			{"Id", "type Id Int\n", "Id"},
			{"Expired", "type Expired\n", "Expired"},
			{"Box", "struct Box<T>\n    value: T\nend\n", "Box<T>"},
			{"Tok", "pub host type Tok\n", "Tok"},
		} {
			nodes, err := parser.Parse(lexer.Lex(c.src + "derive " + iface + " for " + c.name))
			if err != nil {
				t.Fatalf("%s %s: parse: %v", iface, c.name, err)
			}
			nodes, errs := LowerDerives(nodes)
			if len(errs) > 0 {
				t.Fatalf("%s %s: %v", iface, c.name, errs)
			}
			var blocks []*ast.ImplBlock
			for _, n := range nodes {
				if blk, ok := n.(*ast.ImplBlock); ok && TypeExprBaseName(blk.Interface) == iface {
					blocks = append(blocks, blk)
				}
			}
			if len(blocks) != 1 {
				t.Fatalf("%s %s: %d %s blocks, want 1", iface, c.name, len(blocks), iface)
			}
			blk := blocks[0]
			if len(blk.Items) != 0 {
				t.Errorf("%s %s: the derived block writes %d items, want none", iface, c.name, len(blk.Items))
			}
			if !IsSynthesizedLine(blk.Line) || IsSynthesizedLine(blk.Interface.LineNum()) {
				t.Errorf("%s %s: the block is at line %d with its header at line %d, want the band and the derive", iface, c.name, blk.Line, blk.Interface.LineNum())
			}
			if got := blk.Receiver.TypeString(); got != c.recv {
				t.Errorf("%s %s: receiver %s, want %s", iface, c.name, got, c.recv)
			}
			if !derivesByShape(blk) {
				t.Errorf("%s %s: derivesByShape does not answer for the derived block", iface, c.name)
			}
			for _, tp := range blk.Generics {
				bounded := len(tp.Bounds) == 1 && TypeExprBaseName(tp.Bounds[0]) == iface
				if want := iface != "Debug"; bounded != want {
					t.Errorf("%s %s: parameter %s has bounds %v, want the interface %v", iface, c.name, tp.Name, tp.Bounds, want)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Implicit generic interface bounds (spec §12, *Generic types*)
// ---------------------------------------------------------------------------

// synthFnBy locates the synthesized FuncDef whose `impl <ifaceName> for T`
// block targets the named first-param type. It parameterizes over interface and method name so the Gap-2 tests can
// pin Comparable bounds with the same helper. Returns
// nil when no matching fn is found.
func synthFnBy(t *testing.T, src, fnName, ifaceName, typeName string) *ast.FuncDef {
	t.Helper()
	return synthMethodForSource(t, src, fnName, ifaceName, typeName)
}

// TestDeriveOnGenericStructAddsInterfaceBound pins the implicit bound:
// `derive Comparable for Box<T>` synthesizes `fn compare(a: Box<T>, b:
// Box<T>) -> Ordering` whose TypeParams carry an implicit `T: Comparable`
// bound, so a call over a T with no order is a compile-time error (spec
// §12 *Generic types*).
func TestDeriveOnGenericStructAddsInterfaceBound(t *testing.T) {
	src := `struct Box<T>
    value: T
end
derive Comparable for Box<T>`

	fn := synthFnBy(t, src, "compare", "Comparable", "Box")
	if fn == nil {
		t.Fatal("no synthesized Comparable impl (fn compare) for Box found")
	}
	if len(fn.TypeParams) != 1 {
		t.Fatalf("expected 1 TypeParam, got %d", len(fn.TypeParams))
	}
	tp := fn.TypeParams[0]
	if tp.Name != "T" {
		t.Errorf("expected TypeParam.Name == 'T', got %q", tp.Name)
	}
	if len(tp.Bounds) != 1 {
		t.Fatalf("expected 1 bound on T, got %d", len(tp.Bounds))
	}
	bound, ok := tp.Bounds[0].(*ast.SimpleType)
	if !ok || bound.Name != "Comparable" {
		t.Errorf("expected bound SimpleType(Comparable), got %T %v", tp.Bounds[0], tp.Bounds[0])
	}
	// Param types must reference Box<T> (GenericType), not bare Box.
	for i := 0; i < 2; i++ {
		gt, ok := fn.Params[i].TypeAnnotation.(*ast.GenericType)
		if !ok {
			t.Fatalf("param %d: expected GenericType(Box<T>), got %T", i, fn.Params[i].TypeAnnotation)
		}
		if gt.Name != "Box" || len(gt.Params) != 1 {
			t.Errorf("param %d: expected Box<T>, got %s with %d params", i, gt.Name, len(gt.Params))
		}
	}
}

// TestDeriveOnMultiParamGenericStruct pins the multi-arg generic case:
// `derive Comparable for Pair<A, B>` over `left: A; right: B` produces both
// `A: Comparable` and `B: Comparable` bounds on the synthesized compare fn.
func TestDeriveOnMultiParamGenericStruct(t *testing.T) {
	src := `struct Pair<A, B>
    left: A
    right: B
end
derive Comparable for Pair<A, B>`

	fn := synthFnBy(t, src, "compare", "Comparable", "Pair")
	if fn == nil {
		t.Fatal("no synthesized Comparable impl (fn compare) for Pair found")
	}
	if len(fn.TypeParams) != 2 {
		t.Fatalf("expected 2 TypeParams, got %d", len(fn.TypeParams))
	}
	for i, want := range []string{"A", "B"} {
		tp := fn.TypeParams[i]
		if tp.Name != want {
			t.Errorf("TypeParam %d: expected name %q, got %q", i, want, tp.Name)
		}
		if len(tp.Bounds) != 1 {
			t.Fatalf("TypeParam %d: expected 1 bound, got %d", i, len(tp.Bounds))
		}
		bound, ok := tp.Bounds[0].(*ast.SimpleType)
		if !ok || bound.Name != "Comparable" {
			t.Errorf("TypeParam %d: expected bound SimpleType(Comparable), got %T %v", i, tp.Bounds[0], tp.Bounds[0])
		}
	}
}

// TestDeriveOnGenericEnum pins the generic-enum case: `derive Comparable
// for Option<T>` produces `T: Comparable` on the synthesized compare fn. Mirrors the struct test but exercises the EnumDef
// branch of typeDeclTypeParams.
func TestDeriveOnGenericEnum(t *testing.T) {
	src := `enum Option<T>
    Some T
    None
end
derive Comparable for Option<T>`

	fn := synthFnBy(t, src, "compare", "Comparable", "Option")
	if fn == nil {
		t.Fatal("no synthesized Comparable impl (fn compare) for Option found")
	}
	if len(fn.TypeParams) != 1 {
		t.Fatalf("expected 1 TypeParam, got %d", len(fn.TypeParams))
	}
	tp := fn.TypeParams[0]
	if tp.Name != "T" {
		t.Errorf("expected TypeParam.Name == 'T', got %q", tp.Name)
	}
	if len(tp.Bounds) != 1 {
		t.Fatalf("expected 1 bound on T, got %d", len(tp.Bounds))
	}
	bound, ok := tp.Bounds[0].(*ast.SimpleType)
	if !ok || bound.Name != "Comparable" {
		t.Errorf("expected bound SimpleType(Comparable), got %T %v", tp.Bounds[0], tp.Bounds[0])
	}
}

// TestDeriveOnNonGenericStructHasNoTypeParams pins the negative case: the
// non-generic path must NOT invent TypeParams. Regression guard against an
// over-eager helper that returns a stub TypeParam slice for empty input.
func TestDeriveOnNonGenericStructHasNoTypeParams(t *testing.T) {
	src := `struct Point
    x: Int
    y: Int
end
derive Comparable for Point`

	fn := synthFnBy(t, src, "compare", "Comparable", "Point")
	if fn == nil {
		t.Fatal("no synthesized fn for Point found")
	}
	if len(fn.TypeParams) != 0 {
		t.Errorf("expected 0 TypeParams on non-generic Point, got %d: %v", len(fn.TypeParams), fn.TypeParams)
	}
	// The param type for the non-generic case must remain *ast.SimpleType,
	// not a degenerate GenericType{Name: "Point", Params: nil}.
	if _, ok := fn.Params[0].TypeAnnotation.(*ast.SimpleType); !ok {
		t.Errorf("non-generic param type: expected SimpleType, got %T", fn.Params[0].TypeAnnotation)
	}
}

// distinctUnwrapHead is the type a derived body's `T(x) = v` names, or ""
// when stmt is no such statement.
func distinctUnwrapHead(stmt ast.Node) string {
	pb, ok := stmt.(*ast.PatternBinding)
	if !ok || pb.Else != nil {
		return ""
	}
	ep, ok := pb.Pattern.(*ast.EnumPattern)
	if !ok || ep.Binding == "" {
		return ""
	}
	return ep.Variant.TypeString()
}
