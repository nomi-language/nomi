package irbuild

// `assert pattern = value` in a test body, and the "defined as:" block of an
// assertion whose subject is a bare name.
//
// The value is evaluated outside any assertion subject, so it records no
// `values:` rows; the pattern's bindings land in the test's scope; and a pattern that does not match fails the case with the reason
// "pattern did not match", the unmatched value as `actual`, and, when the
// value is a bare name an ordinary binding introduced, that binding's
// "defined as:" block.
//
// The graph branches on the pattern's tests. The success edge binds the
// pattern's names and the rest of the body continues there, so later
// statements read them. The failure edge is one `ir.NewAssertMismatch`, which
// always fails, followed by an unreachable Unit return. Identifier and
// wildcard patterns have no failure edge.
//
// The patterns are the ones a retained `case` already tests: a scalar
// literal, an enum variant with a flat payload (irvariantpattern.go), a list
// (irlistpattern.go) and a tuple (irtuplepattern.go). A pattern name that
// rebinds a name the body already bound, and a bare-name value bound by a
// pipe whose `pipeline values:` stages were not recorded, decline.
//
// NEITHER FORM IS READ BACK. The test-body reader spells one straight block
// and passes no binding to the site it writes, so a body holding either is
// retained for the VM only (`testWalkOnly`).

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// patternAssert lowers one `assert pattern = value` statement.
func (bl *irScalarBuilder) patternAssert(t *ast.PatternDestructure) bool {
	if isNilNode(t.Value) || isNilNode(t.Pattern) {
		return false
	}
	subj, sk, _, ok := bl.lower(t.Value)
	if !ok {
		return false
	}
	def, ok := bl.assertDefinedAs(t.Value, subj)
	if !ok {
		return false
	}
	before := make(map[string]ir.Temp, len(bl.bound))
	for name, temp := range bl.bound {
		before[name] = temp
	}
	pos := bl.g.irPos(t.Line, t.Col)
	var matched, failed *ir.Block
	refutable := true
	tupleBinds := irRetainedTupleKind(sk) && tupleCaseIrrefutable(t.Pattern, sk)
	switch t.Pattern.(type) {
	case *ast.WildcardPattern, *ast.IdentPattern:
		refutable = false
	default:
		if tupleBinds {
			// A tuple of names and wildcards cannot fail.
			refutable = false
			break
		}
		if irNominalCaseKind(sk) && irNominalIrrefutable(t.Pattern) {
			// `Point{x, y}`, `{name, age}` and `UserId(id)` only bind: the
			// subject's type is the pattern's, so nothing can fail.
			refutable = false
			matched = bl.f.NewBlock(bl.g.irNodePos(t.Pattern), "pattern matched")
			break
		}
		// The two edges exist only for a pattern that can fail.
		matched = bl.f.NewBlock(bl.g.irNodePos(t.Pattern), "pattern matched")
		failed = bl.f.NewBlock(pos, "pattern mismatch")
	}
	switch p := t.Pattern.(type) {
	case *ast.WildcardPattern:
	case *ast.TuplePattern:
		if !tupleBinds {
			if !bl.tupleCaseTest(p, subj, sk, matched, failed) {
				irDeclineNote("an `assert pattern = value` tuple pattern outside the retained case tests")
				return false
			}
			break
		}
		bl.tupleCaseBindings(p, subj, sk)
	case *ast.IdentPattern:
		bl.patternBinding(p, p.Name, subj, sk)
	case *ast.IntLit, *ast.StringLit, *ast.CodepointLit:
		lit, lk, _, lok := bl.litFor(p, sk)
		if !lok || lk != sk {
			return false
		}
		m := ir.NewMatchLitInto(bl.g.irNodePos(p), bl.f.NewTemp(), subj, lit)
		bl.b.Append(m)
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(p), m.Dst(), matched.ID(), failed.ID()))
		bl.b = matched
	default:
		var tested bool
		switch {
		case sk == kindBool || (sk.tag == tagNamed && irRetainedEnumKind(sk.def)):
			tested = bl.enumCaseTest(p, subj, sk, matched, failed)
		case irRetainedListKind(sk) || irListTransportKind(sk):
			// Nominal elements too: the body is retained for the VM only,
			// whose list projections do not depend on the element.
			tested = bl.listCaseTest(p, subj, sk, matched, failed)
		case irRetainedTupleKind(sk):
			tested = bl.tupleCaseTest(p, subj, sk, matched, failed)
		case sk.tag == tagMap && irRetainedMapKind(sk):
			tested = bl.mapCaseTest(p, subj, sk, matched, failed)
		case irNominalCaseKind(sk):
			// A struct, record or distinct pattern, as a `case` arm tests it.
			next := failed
			if !refutable {
				next = matched
			}
			tested = bl.nominalCaseTest(p, subj, sk, matched, next)
		}
		if !tested {
			irDeclineNote("an `assert pattern = value` pattern outside the retained case tests: " + sk.nomi())
			return false
		}
	}
	for name, temp := range before {
		if bl.bound[name] != temp {
			irDeclineNote("an `assert pattern = value` that rebinds " + name)
			return false
		}
	}
	if refutable {
		text := renderNode(&ast.PatternDestructure{Pattern: t.Pattern, Value: t.Value, Line: t.Line, Col: t.Col})
		a := ir.NewAssertMismatch(pos, subj, text)
		if def != nil {
			a.WithBinding(*def)
		}
		failed.Append(a)
		failed.SetTerm(ir.NewReturnUnit(pos))
	}
	return true
}

// assertDefinedAs is the "defined as:" block for an assertion whose subject
// or pattern value is at: the
// binding this body wrote for a bare name, or nil for any other value. A name
// bound by a pipe declines, because its block also prints the pipe's stages.
func (bl *irScalarBuilder) assertDefinedAs(at ast.Node, val ir.Temp) (*ir.AssertBinding, bool) {
	id, bare := at.(*ast.Ident)
	if !bare {
		return nil, true
	}
	b := bl.testDefs[id.Name]
	if b == nil {
		// A name a pattern or a context pattern bound: debug information
		// is recorded only for an ordinary binding.
		return nil, true
	}
	def := &ir.AssertBinding{Name: id.Name, Expr: renderNode(b.Value), Val: val}
	if pipe, isBinary := b.Value.(*ast.Binary); isBinary && pipe.Op == "|>" {
		stages, recorded := bl.testStages[id.Name]
		if !recorded {
			irDeclineNote("a defined-as block over a pipe binding")
			return nil, false
		}
		def.Stages = stages
	}
	return def, true
}

// irNominalIrrefutable reports whether a struct, record or distinct pattern
// only binds names: no sub-pattern it holds can fail.
func irNominalIrrefutable(pattern ast.Node) bool {
	binds := func(n ast.Node) bool {
		switch n.(type) {
		case nil, *ast.IdentPattern, *ast.WildcardPattern:
			return true
		}
		return false
	}
	switch p := pattern.(type) {
	case *ast.StructPattern:
		for _, f := range p.Fields {
			if !binds(f.Pattern) {
				return false
			}
		}
		return true
	case *ast.EnumPattern:
		return binds(p.Payload)
	}
	return false
}
