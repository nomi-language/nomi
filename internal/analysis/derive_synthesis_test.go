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

// TestDeriveUnsupportedInterfaceErrors checks that `@derive Foo` where Foo
// is not one of the four supported protocols produces an error message
// naming the offending interface.
func TestDeriveUnsupportedInterfaceErrors(t *testing.T) {
	// The `derive Iface` validation (unknown protocol) now lives in
	// LowerDerives' second return value, not fa.TypeErrors.
	src := `struct Foo { x: Int }
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
		if strings.Contains(e.Message, "Widget") && strings.Contains(e.Message, "unknown derivable protocol") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected 'unknown derivable protocol' error mentioning 'iter', got: %s", strings.Join(msgs, "\n  "))
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
	src := `struct Foo { x: Int }
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
	src := `struct Foo { x: Int }
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

// synthMethodForSource parses + runs SynthesizeDerives over src and returns the
// first synthesized impl method matching (method, iface, typeName), in either
// the block or decorator shape (see synthMethodFromNode).
func synthMethodForSource(t *testing.T, src, method, iface, typeName string) *ast.FuncDef {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, err := parser.Parse(tokens)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Lower type-body `derive Iface` entries into the synthetic
	// `@derive` decorators SynthesizeDerives consumes.
	nodes, lowerErrs := LowerDerives(nodes)
	if len(lowerErrs) > 0 {
		t.Fatalf("unexpected lowering errors: %v", lowerErrs)
	}
	nodes, errs := SynthesizeDerives(nodes)
	if len(errs) > 0 {
		t.Fatalf("unexpected synth errors: %v", errs)
	}
	for _, n := range nodes {
		if fn := synthMethodFromNode(n, method, iface, typeName); fn != nil {
			return fn
		}
	}
	return nil
}

func synthEqualsForSource(t *testing.T, src, typeName string) *ast.FuncDef {
	t.Helper()
	return synthMethodForSource(t, src, "equal?", "Equatable", typeName)
}

// TestDeriveEquatableOnStructSynthesizes pins the struct path: a non-empty
// `@derive Equatable struct ...` produces a single `impl Equatable for T { fn
// equals(a: T, b: T): Bool { ... } }` whose body is the AND-chain of
// pairwise field equality.
func TestDeriveEquatableOnStructSynthesizes(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
derive Equatable for Point`

	fn := synthEqualsForSource(t, src, "Point")
	if fn == nil {
		t.Fatal("no synthesized Equatable impl (fn equals) found for Point")
	}
	if len(fn.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(fn.Params))
	}
	ret, ok := fn.ReturnTypeExpr.(*ast.SimpleType)
	if !ok || ret.Name != "Bool" {
		t.Errorf("expected return type Bool, got %v", fn.ReturnTypeExpr)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	// Body shape: ExprStmt(Binary{and, Call, Call}). One per field, AND-chained.
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	bin, ok := es.Expr.(*ast.Binary)
	if !ok || bin.Op != "and" {
		t.Fatalf("expected Binary(and), got %T %v", es.Expr, bin)
	}
	// Both sides must be Calls to Equatable.equal?.
	for _, side := range []ast.Node{bin.Left, bin.Right} {
		c, ok := side.(*ast.Call)
		if !ok {
			t.Fatalf("expected Call on each side of the and-chain, got %T", side)
		}
		fa, ok := c.Func.(*ast.FieldAccess)
		if !ok {
			t.Fatalf("expected FieldAccess as Call.Func, got %T", c.Func)
		}
		if fa.Field == nil || fa.Field.Name != "equal?" {
			t.Errorf("expected FieldAccess.Field.Name == 'equals', got %v", fa.Field)
		}
		ti, ok := fa.Object.(*ast.TypeIdent)
		if !ok || ti.Name != "Equatable" {
			t.Errorf("expected TypeIdent(Equatable), got %T %v", fa.Object, fa.Object)
		}
	}
}

// TestDeriveEquatableOnEmptyStructIsTrue pins the 0-field special case:
// `@derive Equatable struct Empty {}` yields a body that's just `True`.
func TestDeriveEquatableOnEmptyStructIsTrue(t *testing.T) {
	src := `struct Empty {}
derive Equatable for Empty`

	fn := synthEqualsForSource(t, src, "Empty")
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
	ti, ok := es.Expr.(*ast.TypeIdent)
	if !ok || ti.Name != "True" {
		t.Errorf("expected body == TypeIdent(True), got %T %v", es.Expr, es.Expr)
	}
}

// TestDeriveEquatableOnEnumSynthesizes pins the enum path: a bare-variant
// enum produces a `case a { ... }` outer dispatch plus per-variant inner
// `case b` with a wildcard False arm.
func TestDeriveEquatableOnEnumSynthesizes(t *testing.T) {
	src := `enum Color { Red; Green; Blue }
derive Equatable for Color`

	fn := synthEqualsForSource(t, src, "Color")
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
		t.Errorf("expected 3 outer branches (one per variant), got %d", len(outer.Branches))
	}
	for _, br := range outer.Branches {
		// Each branch body is the inner case-on-b with a wildcard tail.
		inner, ok := br.Body.(*ast.Case)
		if !ok {
			t.Fatalf("inner branch body: expected nested Case, got %T", br.Body)
		}
		bId, ok := inner.Value.(*ast.Ident)
		if !ok || bId.Name != "b" {
			t.Errorf("inner case scrutinee: expected Ident(b), got %T %v", inner.Value, inner.Value)
		}
		if len(inner.Branches) != 2 {
			t.Errorf("inner case: expected 2 branches (variant + wildcard), got %d", len(inner.Branches))
		}
		// Last branch must be wildcard → False.
		last := inner.Branches[len(inner.Branches)-1]
		if _, ok := last.Pattern.(*ast.WildcardPattern); !ok {
			t.Errorf("inner last branch pattern: expected WildcardPattern, got %T", last.Pattern)
		}
		ti, ok := last.Body.(*ast.TypeIdent)
		if !ok || ti.Name != "False" {
			t.Errorf("inner last branch body: expected TypeIdent(False), got %T %v", last.Body, last.Body)
		}
	}
}

// TestDeriveEquatableOnDistinctTypeSynthesizes pins the distinct-type
// path: `type Id Int` produces the unwrap-then-equals shape:
// `Id(av) = a; Id(bv) = b; Equatable.equal?(av, bv)`.
func TestDeriveEquatableOnDistinctTypeSynthesizes(t *testing.T) {
	src := `type Id Int
derive Equatable for Id`

	fn := synthEqualsForSource(t, src, "Id")
	if fn == nil {
		t.Fatal("no synthesized fn for Id")
	}
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("expected 3 stmts (2 destructures + 1 equals), got %d", len(fn.Body.Stmts))
	}
	for i := 0; i < 2; i++ {
		dd, ok := fn.Body.Stmts[i].(*ast.DistinctDestructure)
		if !ok {
			t.Errorf("stmt %d: expected DistinctDestructure, got %T", i, fn.Body.Stmts[i])
			continue
		}
		if dd.TypeName != "Id" {
			t.Errorf("stmt %d: expected destructure of Id, got %s", i, dd.TypeName)
		}
	}
	// Final stmt is ExprStmt wrapping a Call to Equatable.equal?.
	es, ok := fn.Body.Stmts[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt 2: expected ExprStmt, got %T", fn.Body.Stmts[2])
	}
	c, ok := es.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("stmt 2 expr: expected Call, got %T", es.Expr)
	}
	fa, ok := c.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "equal?" {
		t.Errorf("stmt 2 callee: expected FieldAccess(equals), got %T %v", c.Func, c.Func)
	}
	ti, ok := fa.Object.(*ast.TypeIdent)
	if !ok || ti.Name != "Equatable" {
		t.Errorf("stmt 2 callee object: expected TypeIdent(Equatable), got %T %v", fa.Object, fa.Object)
	}
}

// TestDeriveEquatableOnZeroSizedDistinctIsTrue pins zero-sized distincts:
// `type Expired` (no inner) yields a body of bare `True`.
func TestDeriveEquatableOnZeroSizedDistinctIsTrue(t *testing.T) {
	src := `type Expired
derive Equatable for Expired`

	fn := synthEqualsForSource(t, src, "Expired")
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
	ti, ok := es.Expr.(*ast.TypeIdent)
	if !ok || ti.Name != "True" {
		t.Errorf("expected body == TypeIdent(True), got %T %v", es.Expr, es.Expr)
	}
}

// TestDeriveEquatableOnPositionalArity2VariantUsesNestedTuple pins the
// arity-≥2 positional-variant case: the synthesized variant pattern's
// payload must be a *ast.TuplePattern with Flat == false. The flat form
// (`Variant(a, b)`) is rejected by the analyzer's checkPattern with
// "variant pattern '...': multi-binding is not supported"; canonical
// form is the nested-tuple shape `Variant((a, b))`. Regression for
// the synthesizer originally emitting Flat: true.
func TestDeriveEquatableOnPositionalArity2VariantUsesNestedTuple(t *testing.T) {
	src := `enum Pair { Both(Int, Int); None }
derive Equatable for Pair`

	fn := synthEqualsForSource(t, src, "Pair")
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
		t.Fatalf("expected outer Case on a, got %T", es.Expr)
	}
	// Find the outer branch whose pattern is `Both(...)` on the a-side,
	// then drill into its inner case-on-b for the matching `Both(...)` arm.
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
	// Outer Both(a-side) payload check.
	outerEnumPat, ok := bothBranch.Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected outer Both pattern as EnumPattern, got %T", bothBranch.Pattern)
	}
	outerTup, ok := outerEnumPat.Payload.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("expected outer Both payload as *ast.TuplePattern, got %T", outerEnumPat.Payload)
	}
	if outerTup.Flat {
		t.Errorf("outer Both payload TuplePattern.Flat == true; want false (canonical nested tuple form)")
	}
	if len(outerTup.Patterns) != 2 {
		t.Errorf("outer Both payload arity: expected 2 bindings, got %d", len(outerTup.Patterns))
	}
	// Inner case-on-b → Both(...) arm.
	inner, ok := bothBranch.Body.(*ast.Case)
	if !ok {
		t.Fatalf("outer Both branch body: expected nested Case-on-b, got %T", bothBranch.Body)
	}
	if len(inner.Branches) < 1 {
		t.Fatalf("inner case has no branches")
	}
	innerBoth := inner.Branches[0]
	innerEnumPat, ok := innerBoth.Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("inner Both pattern: expected EnumPattern, got %T", innerBoth.Pattern)
	}
	innerTup, ok := innerEnumPat.Payload.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("inner Both payload: expected *ast.TuplePattern, got %T", innerEnumPat.Payload)
	}
	if innerTup.Flat {
		t.Errorf("inner Both payload TuplePattern.Flat == true; want false (canonical nested tuple form)")
	}
	if len(innerTup.Patterns) != 2 {
		t.Errorf("inner Both payload arity: expected 2 bindings, got %d", len(innerTup.Patterns))
	}
}

// TestSynthesizeDerivesIsIdempotent — the front end runs SynthesizeDerives
// twice on overlapping slices (once in internal/frontend's Checker.Prepare,
// once again inside BuildProject). The second call
// must NOT add a duplicate FuncDef; otherwise the analyzer's Sweep A
// emits "already defined" errors.
func TestSynthesizeDerivesIsIdempotent(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
derive Equatable for Point`
	tokens := lexer.Lex(src)
	nodes, _ := parser.Parse(tokens)
	// Lower the `derive Equatable` body line into the synthetic
	// `@derive` decorator SynthesizeDerives consumes.
	nodes, _ = LowerDerives(nodes)
	once, errs := SynthesizeDerives(nodes)
	if len(errs) > 0 {
		t.Fatalf("first synth: unexpected errors %v", errs)
	}
	twice, errs := SynthesizeDerives(once)
	if len(errs) > 0 {
		t.Fatalf("second synth: unexpected errors %v", errs)
	}
	if len(twice) != len(once) {
		t.Errorf("idempotency violated: first synth len=%d, second synth len=%d", len(once), len(twice))
	}
}

// ---------------------------------------------------------------------------
// Hashable synthesis tests
// ---------------------------------------------------------------------------

// synthHashForSource locates the synthesized `impl Hashable for T { fn hash }`
// FuncDef whose first param's declared type matches typeName. Mirrors
// synthEqualsForSource above.
func synthHashForSource(t *testing.T, src, typeName string) *ast.FuncDef {
	t.Helper()
	return synthMethodForSource(t, src, "hash", "Hashable", typeName)
}

// TestDeriveHashableOnStructSynthesizes pins the struct path: `@derive
// Hashable struct Point { x: Int; y: Int }` produces an `impl Hashable for Point { fn
// hash(value: Point): Int { Hashable.hash(value.x) * 31 + Hashable.hash(value.y) } }`.
// The body is a left-fold mix expressed as Binary(+, Binary(*, Hashable.hash(.x), 31), Hashable.hash(.y)).
func TestDeriveHashableOnStructSynthesizes(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
derive Hashable for Point`

	fn := synthHashForSource(t, src, "Point")
	if fn == nil {
		t.Fatal("no synthesized Hashable impl (fn hash) found for Point")
	}
	if len(fn.Params) != 1 {
		t.Errorf("expected 1 param, got %d", len(fn.Params))
	}
	ret, ok := fn.ReturnTypeExpr.(*ast.SimpleType)
	if !ok || ret.Name != "Int" {
		t.Errorf("expected return type Int, got %v", fn.ReturnTypeExpr)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	// Body shape (2 fields): Binary(+, Binary(*, Call(Hashable.hash, value.x), 31), Call(Hashable.hash, value.y))
	plus, ok := es.Expr.(*ast.Binary)
	if !ok || plus.Op != "+" {
		t.Fatalf("expected outer Binary(+), got %T %v", es.Expr, es.Expr)
	}
	mul, ok := plus.Left.(*ast.Binary)
	if !ok || mul.Op != "*" {
		t.Fatalf("expected left == Binary(*), got %T %v", plus.Left, plus.Left)
	}
	intLit, ok := mul.Right.(*ast.IntLit)
	if !ok || intLit.Value != 31 {
		t.Errorf("expected mix multiplier 31 as IntLit, got %T %v", mul.Right, mul.Right)
	}
	// Both Hashable.hash calls live at mul.Left and plus.Right.
	for _, side := range []ast.Node{mul.Left, plus.Right} {
		c, ok := side.(*ast.Call)
		if !ok {
			t.Fatalf("expected Call (Hashable.hash) on each hash position, got %T", side)
		}
		fa, ok := c.Func.(*ast.FieldAccess)
		if !ok {
			t.Fatalf("expected FieldAccess as Call.Func, got %T", c.Func)
		}
		if fa.Field == nil || fa.Field.Name != "hash" {
			t.Errorf("expected FieldAccess.Field.Name == 'hash', got %v", fa.Field)
		}
		ti, ok := fa.Object.(*ast.TypeIdent)
		if !ok || ti.Name != "Hashable" {
			t.Errorf("expected TypeIdent(Hashable), got %T %v", fa.Object, fa.Object)
		}
	}
}

// TestDeriveHashableOnEmptyStructIsZero pins the 0-field special case:
// `@derive Hashable struct Empty {}` yields a body of bare `IntLit(0)`.
func TestDeriveHashableOnEmptyStructIsZero(t *testing.T) {
	src := `struct Empty {}
derive Hashable for Empty`

	fn := synthHashForSource(t, src, "Empty")
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
	il, ok := es.Expr.(*ast.IntLit)
	if !ok || il.Value != 0 {
		t.Errorf("expected body == IntLit(0), got %T %v", es.Expr, es.Expr)
	}
}

// TestDeriveHashableOnEnumSynthesizes pins the enum path: bare and
// payload variants produce a `case value { ... }` outer dispatch with one
// branch per variant. Bare branches are bare IntLit(index); payload
// branches are mix(index, payload-hash).
func TestDeriveHashableOnEnumSynthesizes(t *testing.T) {
	src := `enum Shape { Circle Float; None }
derive Hashable for Shape`

	fn := synthHashForSource(t, src, "Shape")
	if fn == nil {
		t.Fatal("no synthesized fn for Shape")
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
	if !ok || id.Name != "value" {
		t.Errorf("outer case scrutinee: expected Ident(value), got %T %v", outer.Value, outer.Value)
	}
	if len(outer.Branches) != 2 {
		t.Fatalf("expected 2 outer branches (one per variant), got %d", len(outer.Branches))
	}
	// First variant Circle (positional): body is mix(0, Hashable.hash(v0)).
	circleBody, ok := outer.Branches[0].Body.(*ast.Binary)
	if !ok || circleBody.Op != "+" {
		t.Fatalf("Circle branch body: expected Binary(+), got %T %v", outer.Branches[0].Body, outer.Branches[0].Body)
	}
	// Second variant None (bare): body is bare IntLit(1).
	noneBody, ok := outer.Branches[1].Body.(*ast.IntLit)
	if !ok || noneBody.Value != 1 {
		t.Errorf("None branch body: expected IntLit(1), got %T %v", outer.Branches[1].Body, outer.Branches[1].Body)
	}
}

// TestDeriveHashableOnDistinctTypeSynthesizes pins the primitive distinct
// path: `type Id Int` produces `Id(inner) = value; Hashable.hash(inner)`.
func TestDeriveHashableOnDistinctTypeSynthesizes(t *testing.T) {
	src := `type Id Int
derive Hashable for Id`

	fn := synthHashForSource(t, src, "Id")
	if fn == nil {
		t.Fatal("no synthesized fn for Id")
	}
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("expected 2 stmts (1 destructure + 1 hash), got %d", len(fn.Body.Stmts))
	}
	dd, ok := fn.Body.Stmts[0].(*ast.DistinctDestructure)
	if !ok {
		t.Fatalf("stmt 0: expected DistinctDestructure, got %T", fn.Body.Stmts[0])
	}
	if dd.TypeName != "Id" {
		t.Errorf("stmt 0: expected destructure of Id, got %s", dd.TypeName)
	}
	es, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt 1: expected ExprStmt, got %T", fn.Body.Stmts[1])
	}
	c, ok := es.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("stmt 1 expr: expected Call, got %T", es.Expr)
	}
	fa, ok := c.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "hash" {
		t.Errorf("stmt 1 callee: expected FieldAccess(hash), got %T %v", c.Func, c.Func)
	}
	ti, ok := fa.Object.(*ast.TypeIdent)
	if !ok || ti.Name != "Hashable" {
		t.Errorf("stmt 1 callee object: expected TypeIdent(Hashable), got %T %v", fa.Object, fa.Object)
	}
}

// TestDeriveHashableOnPositionalArity2VariantUsesNestedTuple pins the
// arity-≥2 positional-variant case for Hashable: same Flat: false invariant
// as the Equatable test. The synthesized variant pattern's payload must be
// a *ast.TuplePattern with Flat == false; the analyzer rejects flat-form
// variant patterns. The Hashable synthesizer reuses enumVariantPattern and must
// inherit the canonical nested-tuple shape.
func TestDeriveHashableOnPositionalArity2VariantUsesNestedTuple(t *testing.T) {
	src := `enum Pair { Both(Int, Int); None }
derive Hashable for Pair`

	fn := synthHashForSource(t, src, "Pair")
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
	if tup.Flat {
		t.Errorf("Both payload TuplePattern.Flat == true; want false (canonical nested tuple form)")
	}
	if len(tup.Patterns) != 2 {
		t.Errorf("Both payload arity: expected 2 bindings, got %d", len(tup.Patterns))
	}
}

// ---------------------------------------------------------------------------
// Comparable synthesis tests
// ---------------------------------------------------------------------------

// synthCompareForSource locates the synthesized `impl Comparable for T { fn compare }`
// FuncDef whose first param's declared type matches typeName. Mirrors
// synthEqualsForSource / synthHashForSource above.
func synthCompareForSource(t *testing.T, src, typeName string) *ast.FuncDef {
	t.Helper()
	return synthMethodForSource(t, src, "compare", "Comparable", typeName)
}

// TestDeriveComparableOnStructSynthesizes pins the struct path: `@derive
// Comparable struct Point { x: Int; y: Int }` produces an `impl Comparable for Point {
// fn compare(a: Point, b: Point): Ordering }` whose body is the
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
	src := `struct Point { x: Int; y: Int }
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
	src := `struct Empty {}
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
	src := `enum Color { Red; Green; Blue }
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
		dd, ok := fn.Body.Stmts[i].(*ast.DistinctDestructure)
		if !ok {
			t.Errorf("stmt %d: expected DistinctDestructure, got %T", i, fn.Body.Stmts[i])
			continue
		}
		if dd.TypeName != "Id" {
			t.Errorf("stmt %d: expected destructure of Id, got %s", i, dd.TypeName)
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
// arity-≥2 positional-variant case for Comparable: same Flat: false
// invariant as the Equatable / Hashable tests. The synthesized variant
// pattern's payload must be a *ast.TuplePattern with Flat == false; the
// analyzer rejects flat-form variant patterns. Comparable reuses
// enumVariantPattern (same helper as Equatable / Hashable), so the
// canonical nested-tuple shape is inherited — this test pins it.
func TestDeriveComparableOnPositionalArity2VariantUsesNestedTuple(t *testing.T) {
	src := `enum Pair { Both(Int, Int); None }
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
	if tup.Flat {
		t.Errorf("Both payload TuplePattern.Flat == true; want false (canonical nested tuple form)")
	}
	if len(tup.Patterns) != 2 {
		t.Errorf("Both payload arity: expected 2 bindings, got %d", len(tup.Patterns))
	}
}

// ---------------------------------------------------------------------------
// Debug synthesis tests
// ---------------------------------------------------------------------------

// synthDebugForSource locates the synthesized `impl Debug for T { fn inspect }`
// FuncDef whose first param's declared type matches typeName. Mirrors the
// per-protocol helpers above.
func synthDebugForSource(t *testing.T, src, typeName string) *ast.FuncDef {
	t.Helper()
	return synthMethodForSource(t, src, "inspect", "Debug", typeName)
}

// debugStringPartsContain returns true if the StringInterp's Parts contain
// a StringText whose Value contains substr. Used to spot-check that the
// rendered template carries the expected static fragments (type name,
// braces, separators).
func debugStringPartsContain(parts []ast.StringPart, substr string) bool {
	for _, p := range parts {
		if st, ok := p.(ast.StringText); ok && strings.Contains(st.Value, substr) {
			return true
		}
	}
	return false
}

// TestDeriveDebugOnStructSynthesizes pins the struct path: a non-empty
// `@derive Debug struct ...` produces a single `impl Debug for T { fn
// to_string(value: T): String { ... } }` whose body is a StringInterp
// containing the type name, `{`, each field name + `: ` static fragments,
// `, ` separators, `}`, and one Debug.to_string Call per field as
// dynamic StringExpr parts.
func TestDeriveDebugOnStructSynthesizes(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
derive Debug for Point`

	fn := synthDebugForSource(t, src, "Point")
	if fn == nil {
		t.Fatal("no synthesized Debug impl (fn inspect) found for Point")
	}
	if len(fn.Params) != 1 {
		t.Errorf("expected 1 param, got %d", len(fn.Params))
	}
	ret, ok := fn.ReturnTypeExpr.(*ast.SimpleType)
	if !ok || ret.Name != "String" {
		t.Errorf("expected return type String, got %v", fn.ReturnTypeExpr)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1-stmt body, got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body.Stmts[0])
	}
	si, ok := es.Expr.(*ast.StringInterp)
	if !ok {
		t.Fatalf("expected StringInterp body, got %T", es.Expr)
	}
	// Static fragments must include the type-and-first-field opener
	// `Point{x: ` and the closing `}`. The intermediate `, y: ` is the
	// separator between the two fields.
	for _, want := range []string{"Point{x: ", ", y: ", "}"} {
		if !debugStringPartsContain(si.Parts, want) {
			t.Errorf("StringInterp parts missing static fragment %q; parts: %v", want, si.Parts)
		}
	}
	// Two dynamic slots — one per field — each a Debug.to_string Call.
	dynCount := 0
	for _, p := range si.Parts {
		se, ok := p.(ast.StringExpr)
		if !ok {
			continue
		}
		dynCount++
		c, ok := se.Expr.(*ast.Call)
		if !ok {
			t.Fatalf("dynamic part: expected Call, got %T", se.Expr)
		}
		fa, ok := c.Func.(*ast.FieldAccess)
		if !ok || fa.Field == nil || fa.Field.Name != "inspect" {
			t.Errorf("dynamic part callee: expected FieldAccess(inspect), got %T %v", c.Func, c.Func)
		}
		ti, ok := fa.Object.(*ast.TypeIdent)
		if !ok || ti.Name != "Debug" {
			t.Errorf("dynamic part callee object: expected TypeIdent(Debug), got %T %v", fa.Object, fa.Object)
		}
	}
	if dynCount != 2 {
		t.Errorf("expected 2 dynamic StringExpr slots (one per field), got %d", dynCount)
	}
}

// TestDeriveDebugOnEmptyStructIsBareName pins the 0-field special case:
// the body is a static StringLit `"TypeName{}"` with no interpolation
// slots, since there are no fields to render.
func TestDeriveDebugOnEmptyStructIsBareName(t *testing.T) {
	src := `struct Empty {}
derive Debug for Empty`

	fn := synthDebugForSource(t, src, "Empty")
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
	sl, ok := es.Expr.(*ast.StringLit)
	if !ok {
		t.Fatalf("expected body == StringLit, got %T %v", es.Expr, es.Expr)
	}
	if sl.Value != "Empty{}" {
		t.Errorf("expected StringLit.Value == \"Empty{}\", got %q", sl.Value)
	}
}

// TestDeriveDebugOnEnumSynthesizes pins the enum path: bare and payload
// variants produce a `case value { ... }` outer dispatch with one branch
// per variant. Bare branches' bodies are static StringLit; payload
// branches are StringInterp with Debug.to_string calls in dynamic slots.
// Variant names in the rendered text must be BARE (not enum-qualified).
func TestDeriveDebugOnEnumSynthesizes(t *testing.T) {
	src := `enum Shape { Circle Float; None }
derive Debug for Shape`

	fn := synthDebugForSource(t, src, "Shape")
	if fn == nil {
		t.Fatal("no synthesized fn for Shape")
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
	if !ok || id.Name != "value" {
		t.Errorf("outer case scrutinee: expected Ident(value), got %T %v", outer.Value, outer.Value)
	}
	if len(outer.Branches) != 2 {
		t.Fatalf("expected 2 outer branches (one per variant), got %d", len(outer.Branches))
	}
	// First variant Circle (positional): body is StringInterp containing
	// `Circle(`, `)`, plus one dynamic Debug.to_string slot.
	circleBody, ok := outer.Branches[0].Body.(*ast.StringInterp)
	if !ok {
		t.Fatalf("Circle branch body: expected StringInterp, got %T %v", outer.Branches[0].Body, outer.Branches[0].Body)
	}
	for _, want := range []string{"Circle(", ")"} {
		if !debugStringPartsContain(circleBody.Parts, want) {
			t.Errorf("Circle StringInterp missing static %q; parts: %v", want, circleBody.Parts)
		}
	}
	// Variant names must be BARE (not enum-qualified) — `Circle(`, never
	// `Shape.Circle(`.
	for _, p := range circleBody.Parts {
		if st, ok := p.(ast.StringText); ok && strings.Contains(st.Value, "Shape.") {
			t.Errorf("Circle variant rendered with enum qualifier (%q); Debug expects bare variant names", st.Value)
		}
	}
	// Second variant None (bare): body is bare StringLit("None").
	noneBody, ok := outer.Branches[1].Body.(*ast.StringLit)
	if !ok {
		t.Fatalf("None branch body: expected StringLit, got %T %v", outer.Branches[1].Body, outer.Branches[1].Body)
	}
	if noneBody.Value != "None" {
		t.Errorf("None branch body: expected StringLit(\"None\"), got %q", noneBody.Value)
	}
}

// TestDeriveDebugOnDistinctTypeSynthesizes pins the primitive-distinct
// path: `type Id Int` produces `Id(inner) = value;
// "Id(${Debug.to_string(inner)})"`.
func TestDeriveDebugOnDistinctTypeSynthesizes(t *testing.T) {
	src := `type Id Int
derive Debug for Id`

	fn := synthDebugForSource(t, src, "Id")
	if fn == nil {
		t.Fatal("no synthesized fn for Id")
	}
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("expected 2 stmts (1 destructure + 1 interp), got %d", len(fn.Body.Stmts))
	}
	dd, ok := fn.Body.Stmts[0].(*ast.DistinctDestructure)
	if !ok {
		t.Fatalf("stmt 0: expected DistinctDestructure, got %T", fn.Body.Stmts[0])
	}
	if dd.TypeName != "Id" {
		t.Errorf("stmt 0: expected destructure of Id, got %s", dd.TypeName)
	}
	es, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt 1: expected ExprStmt, got %T", fn.Body.Stmts[1])
	}
	si, ok := es.Expr.(*ast.StringInterp)
	if !ok {
		t.Fatalf("stmt 1 expr: expected StringInterp, got %T", es.Expr)
	}
	for _, want := range []string{"Id(", ")"} {
		if !debugStringPartsContain(si.Parts, want) {
			t.Errorf("Id StringInterp missing static %q; parts: %v", want, si.Parts)
		}
	}
	// Dynamic slot must be a Debug.to_string call.
	dynCount := 0
	for _, p := range si.Parts {
		se, ok := p.(ast.StringExpr)
		if !ok {
			continue
		}
		dynCount++
		c, ok := se.Expr.(*ast.Call)
		if !ok {
			t.Fatalf("dynamic part: expected Call, got %T", se.Expr)
		}
		fa, ok := c.Func.(*ast.FieldAccess)
		if !ok || fa.Field == nil || fa.Field.Name != "inspect" {
			t.Errorf("dynamic part callee: expected FieldAccess(inspect), got %T %v", c.Func, c.Func)
		}
		ti, ok := fa.Object.(*ast.TypeIdent)
		if !ok || ti.Name != "Debug" {
			t.Errorf("dynamic part callee object: expected TypeIdent(Debug), got %T %v", fa.Object, fa.Object)
		}
	}
	if dynCount != 1 {
		t.Errorf("expected 1 dynamic slot, got %d", dynCount)
	}
}

// TestDeriveDebugOnZeroSizedDistinctIsBareName pins zero-sized distincts:
// `type Expired` (no inner) yields a body of bare `StringLit("Expired")`.
func TestDeriveDebugOnZeroSizedDistinctIsBareName(t *testing.T) {
	src := `type Expired
derive Debug for Expired`

	fn := synthDebugForSource(t, src, "Expired")
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
	sl, ok := es.Expr.(*ast.StringLit)
	if !ok {
		t.Fatalf("expected body == StringLit, got %T %v", es.Expr, es.Expr)
	}
	if sl.Value != "Expired" {
		t.Errorf("expected StringLit.Value == \"Expired\", got %q", sl.Value)
	}
}

// TestDeriveDebugOnPositionalArity2VariantUsesNestedTuple pins the
// arity-≥2 positional-variant case for Debug: same Flat: false invariant
// the other synthesizers test. The synthesized variant pattern's payload
// must be a *ast.TuplePattern with Flat == false; the analyzer rejects
// flat-form variant patterns.
func TestDeriveDebugOnPositionalArity2VariantUsesNestedTuple(t *testing.T) {
	src := `enum Pair { Both(Int, Int); None }
derive Debug for Pair`

	fn := synthDebugForSource(t, src, "Pair")
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
	if tup.Flat {
		t.Errorf("Both payload TuplePattern.Flat == true; want false (canonical nested tuple form)")
	}
	if len(tup.Patterns) != 2 {
		t.Errorf("Both payload arity: expected 2 bindings, got %d", len(tup.Patterns))
	}
	// Both branch body should be a StringInterp rendering
	// `Both(<dbg>, <dbg>)` — bare variant name, comma-separated dbg
	// slots, no extra outer parens.
	si, ok := bothBranch.Body.(*ast.StringInterp)
	if !ok {
		t.Fatalf("Both branch body: expected StringInterp, got %T", bothBranch.Body)
	}
	for _, want := range []string{"Both(", ", ", ")"} {
		if !debugStringPartsContain(si.Parts, want) {
			t.Errorf("Both StringInterp missing static %q; parts: %v", want, si.Parts)
		}
	}
}

// ---------------------------------------------------------------------------
// Manual impl block + derive collision detection (spec §38.1, *Manual +
// derive collision*)
// ---------------------------------------------------------------------------

// TestDeriveCollidesWithManualImplErrors pins spec §38.1: combining
// `@derive Iface` with a manual `impl Iface for T { fn ... }` block for the
// same type must surface a compile error rather than silently dropping the
// synthesized impl in favour of the manual one. The diagnostic must name
// both `@derive Equatable` and the manual impl so the user can resolve
// it without further hunting.
func TestDeriveCollidesWithManualImplErrors(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
derive Equatable for Point

impl Equatable for Point {
  fn equal?(_a: Point, _b: Point): Bool {
    False
  }
}`

	_, errs := buildTypesFromSource(src)
	if len(errs) == 0 {
		t.Fatal("expected error: derive Equatable for Point + manual impl Equatable for Point")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "derive Equatable for Point") &&
			strings.Contains(e.Message, "conflicts") &&
			strings.Contains(e.Message, "Point") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected collision error naming derive Equatable for Point conflicting with the manual impl Equatable on Point; got:\n  %s", strings.Join(msgs, "\n  "))
	}
}

// TestDeriveComparableCollidesWithManualImpl mirrors the Equatable
// collision test for Comparable: same shape, same expectations. Pins
// that the collision check is routed through the same code path for
// every supported derivable protocol, not hard-wired to Equatable.
func TestDeriveComparableCollidesWithManualImpl(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
derive Comparable for Point

impl Comparable for Point {
  fn compare(_a: Point, _b: Point): Ordering {
    Equal
  }
}`

	_, errs := buildTypesFromSource(src)
	if len(errs) == 0 {
		t.Fatal("expected error: derive Comparable for Point + manual impl Comparable for Point")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "derive Comparable for Point") &&
			strings.Contains(e.Message, "conflicts") &&
			strings.Contains(e.Message, "Point") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected collision error naming derive Comparable for Point conflicting with the manual impl Comparable on Point; got:\n  %s", strings.Join(msgs, "\n  "))
	}
}

// ---------------------------------------------------------------------------
// Implicit generic interface bounds (spec §38.1, *Generic types*)
// ---------------------------------------------------------------------------

// synthFnBy locates the synthesized FuncDef whose `impl <ifaceName> for T`
// block targets the named first-param type. Mirrors synthEqualsForSource
// but parameterizes over interface and method name so the Gap-2 tests can
// pin Hashable / Comparable / Debug bounds with the same helper. Returns
// nil when no matching fn is found.
func synthFnBy(t *testing.T, src, fnName, ifaceName, typeName string) *ast.FuncDef {
	t.Helper()
	return synthMethodForSource(t, src, fnName, ifaceName, typeName)
}

// TestDeriveOnGenericStructAddsInterfaceBound pins the implicit bound: `@derive Equatable
// struct Box<T> { value: T }` synthesizes `fn equal?(a: Box<T>, b: Box<T>):
// Bool` whose TypeParams carry an implicit `T: Equatable` bound. Without
// this bound, calling `Equatable.equal?(Box{value: NoEqType{}}, ...)`
// would fall through to a runtime "no impl found" error instead of a
// compile-time type-check failure (spec §38.1).
func TestDeriveOnGenericStructAddsInterfaceBound(t *testing.T) {
	src := `struct Box<T> { value: T }
derive Equatable for Box<T>`

	fn := synthFnBy(t, src, "equal?", "Equatable", "Box")
	if fn == nil {
		t.Fatal("no synthesized Equatable impl (fn equals) for Box found")
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
	if !ok || bound.Name != "Equatable" {
		t.Errorf("expected bound SimpleType(Equatable), got %T %v", tp.Bounds[0], tp.Bounds[0])
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
// `@derive Hashable struct Pair<A, B> { left: A; right: B }` produces both
// `A: Hashable` and `B: Hashable` bounds on the synthesized hash fn.
func TestDeriveOnMultiParamGenericStruct(t *testing.T) {
	src := `struct Pair<A, B> { left: A; right: B }
derive Hashable for Pair<A, B>`

	fn := synthFnBy(t, src, "hash", "Hashable", "Pair")
	if fn == nil {
		t.Fatal("no synthesized Hashable impl (fn hash) for Pair found")
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
		if !ok || bound.Name != "Hashable" {
			t.Errorf("TypeParam %d: expected bound SimpleType(Hashable), got %T %v", i, tp.Bounds[0], tp.Bounds[0])
		}
	}
}

// TestDeriveOnGenericEnum pins the generic-enum case: `@derive Equatable
// enum Maybe<T> { Some(T); None }` produces `T: Equatable` on the
// synthesized equals fn. Mirrors the struct test but exercises the EnumDef
// branch of typeDeclTypeParams.
func TestDeriveOnGenericEnum(t *testing.T) {
	src := `enum Maybe<T> { Some T; None }
derive Equatable for Maybe<T>`

	fn := synthFnBy(t, src, "equal?", "Equatable", "Maybe")
	if fn == nil {
		t.Fatal("no synthesized Equatable impl (fn equals) for Maybe found")
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
	if !ok || bound.Name != "Equatable" {
		t.Errorf("expected bound SimpleType(Equatable), got %T %v", tp.Bounds[0], tp.Bounds[0])
	}
}

// TestDeriveOnGenericStructDebugAndComparable spot-checks that the bound
// propagation routes through every per-protocol synthesizer, not just
// Equatable + Hashable. `@derive Debug struct Box<T>` and `@derive
// Comparable struct Box<T>` should both stamp `T: <Iface>` onto their
// synthesized fns.
func TestDeriveOnGenericStructDebugAndComparable(t *testing.T) {
	srcDebug := `struct Box<T> { value: T }
derive Debug for Box<T>`
	fn := synthFnBy(t, srcDebug, "inspect", "Debug", "Box")
	if fn == nil {
		t.Fatal("no synthesized Debug impl (fn inspect) for Box found")
	}
	if len(fn.TypeParams) != 1 || len(fn.TypeParams[0].Bounds) != 1 {
		t.Fatalf("Debug: expected 1 TypeParam with 1 bound, got %v", fn.TypeParams)
	}
	if b, ok := fn.TypeParams[0].Bounds[0].(*ast.SimpleType); !ok || b.Name != "Debug" {
		t.Errorf("Debug: expected bound Debug, got %T %v", fn.TypeParams[0].Bounds[0], fn.TypeParams[0].Bounds[0])
	}

	srcComparable := `struct Box<T> { value: T }
derive Comparable for Box<T>`
	fn2 := synthFnBy(t, srcComparable, "compare", "Comparable", "Box")
	if fn2 == nil {
		t.Fatal("no synthesized Comparable impl (fn compare) for Box found")
	}
	if len(fn2.TypeParams) != 1 || len(fn2.TypeParams[0].Bounds) != 1 {
		t.Fatalf("Comparable: expected 1 TypeParam with 1 bound, got %v", fn2.TypeParams)
	}
	if b, ok := fn2.TypeParams[0].Bounds[0].(*ast.SimpleType); !ok || b.Name != "Comparable" {
		t.Errorf("Comparable: expected bound Comparable, got %T %v", fn2.TypeParams[0].Bounds[0], fn2.TypeParams[0].Bounds[0])
	}
}

// TestDeriveOnNonGenericStructHasNoTypeParams pins the negative case: the
// non-generic path must NOT invent TypeParams. Regression guard against an
// over-eager helper that returns a stub TypeParam slice for empty input.
func TestDeriveOnNonGenericStructHasNoTypeParams(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
derive Equatable for Point`

	fn := synthFnBy(t, src, "equal?", "Equatable", "Point")
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
