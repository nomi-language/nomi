package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Two destructuring-parameter shapes.
//
// lambda.go's header says what a destructuring parameter is: one value, several
// names, and no runtime checks because the pattern is IRREFUTABLE. It routes a
// distinct `Duration(x)`, a typed `Point{x, y}` and a single-variant enum in
// both its tuple and brace spellings. This file handles the other two:
//
//	{x, y}: Point       an ANONYMOUS STRUCT pattern over a NAMED struct.
//	(a, b): (Int, Int)  a TUPLE pattern, over an interned tagTuple whose
//	                    components are addressed by position, as `case`'s
//	                    tupleArm matches one.
//
// Both are ordinary Nomi rather than corners. Spec §5's self-typing rule is why
// each carries an annotation and `Point{x, y}` does not: a pattern whose head
// NAMES a type is its own annotation, and neither of these has a head, so Nomi
// requires `: Type` and patternParamKind reads the kind straight off it.
//
// # Why the anonymous-struct spelling is a different feature from a record
//
// destructureAnonStruct reads its field list from the interned RECORD kind, so
// the pattern's field set is the whole of the value's type. Over a NAMED struct
// the pattern's field set is a SUBSET of a DECLARATION's — `{x}: Point` is legal
// and names one of two fields — and the match is against a type the pattern does
// not name. Same node, two field sources, dispatched on src.k.
//
// So this reuses destructureFields, which case.go's structFieldArms and the
// typed `Point{x, y}` parameter already share: it walks the PATTERN's fields and
// looks each one up on the def BY NAME, which is exactly the subset rule.
// Teaching destructureAnonStruct about named defs instead would make a second
// field-identity implementation beside the one `case` uses.
//
// # Reading by NAME versus by POSITION
//
// A named struct's fields are found by name and a tuple's by position, and in
// DECLARATION ORDER those two derivations coincide, so a by-position lookup over
// a named struct survives every fixture written in declaration order. Every
// multi-field pattern in testdata/fn_destructure_shapes.nomi is therefore
// PERMUTED against its declaration and read back through a non-commutative
// expression, as fn_destructure_variant.nomi does for the variant shapes.

// destructureNamedFields binds an anonymous struct pattern's fields from a
// value of a NAMED struct type.
//
// The type check is rule (1)'s: destructureStruct's `p.TypeName == nil` arm
// routes on src.k, and the SIBLINGS of "a named struct" at that position are a
// named enum, a distinct type, and every scalar and structural kind. Each must
// refuse here rather than fall into the struct route, where `d.field(name)`
// answers nil and the refusal would read `unknown struct field` about a type
// that has no fields at all.
func (g *gen) destructureNamedFields(p *ast.StructPattern, src expr, at ast.Node) bool {
	d := src.k.def
	if src.k.tag != tagNamed || d == nil || d.isDistinct || d.isEnum {
		g.reject("pattern type mismatch",
			"an anonymous struct pattern against "+src.k.nomi(), at)
		g.probeStructFields(p)
		return false
	}
	return g.destructureFields(d, src, p, at)
}

// destructureTuple binds `(a, b)` in a destructuring parameter — case.go's
// tupleArm with the arm-abandoning removed, since an irrefutable pattern has no
// arm to abandon.
//
// RULE (arity is fixed by the type), which tupleArm states and this reproduces:
// a tuple pattern of the right arity always matches structurally, so there is no
// test to emit. Here the sub-patterns cannot introduce one either — the checker
// rejects a refutable parameter pattern outright — so nothing this emits opens a
// block, and the bindings land straight in the enclosing one.
//
// The arity check is a REFUSAL rather than an assertion for tupleArm's reason:
// reaching it means the front end let through a shape this builder would have to
// guess about.
func (g *gen) destructureTuple(p *ast.TuplePattern, src expr, at ast.Node) bool {
	if src.k.tag != tagTuple {
		g.reject("pattern type mismatch",
			"a tuple pattern against "+src.k.nomi(), at)
		g.probePattern(p)
		return false
	}
	parts := src.k.comp.parts
	if len(parts) != len(p.Patterns) {
		g.reject("tuple pattern arity",
			fmt.Sprintf("%d element(s) against %s", len(p.Patterns), src.k.nomi()), at)
		g.probePattern(p)
		return false
	}
	// Held for the same reason tupleArm holds: the value is read once per
	// element. A top-level destructuring parameter's src IS a Go identifier, so
	// hold is a no-op there and the temporary only appears for a NESTED tuple,
	// whose src is a field access.
	held := g.hold(src)
	ok := true
	for i, sub := range p.Patterns {
		part := g.irProjSlot(at, held, i, parts[i])
		if !g.destructure(sub, part, at) {
			ok = false
		}
	}
	return ok
}
