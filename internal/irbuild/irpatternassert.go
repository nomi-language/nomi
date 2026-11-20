package irbuild

// `assert pattern = value` in a test body, and the "defined as:" block of an
// assertion whose subject is a bare name.
//
// The value is evaluated outside any assertion subject, so it records no
// `values:` rows; the pattern's bindings land in the test's scope; and a
// pattern that does not match fails the case with the reason "pattern did
// not match", the unmatched value as `actual`, and, when the value is a bare
// name an ordinary binding introduced, that binding's "defined as:" block.
//
// The pattern is tested as a `case` arm's pattern is, and as a binding's is
// (patternValueTest). The success edge binds the pattern's names and the
// rest of the body continues there, so later statements read them. The
// failure edge is one `ir.NewAssertMismatch`, which always fails, followed by
// an unreachable Unit return. A pattern whose test builds no failure edge
// (a name, `_`, a tuple or struct of names) only binds, and its mismatch
// block, which nothing reaches, holds the return alone. A pattern name may
// rebind a name the body already bound: it is a fresh identity. A bare-name
// value bound by a pipe whose `pipeline values:` stages were not recorded
// declines.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// patternAssert lowers one `assert pattern = value` statement.
func (bl *irScalarBuilder) patternAssert(t *ast.PatternDestructure) bool {
	if isNilNode(t.Value) || isNilNode(t.Pattern) {
		panic("irbuild: patternAssert: the parser always sets the assert's Value and Pattern")
	}
	subj, sk, _, ok := bl.lower(t.Value)
	if !ok {
		return false
	}
	def, ok := bl.assertDefinedAs(t.Value, subj)
	if !ok {
		return false
	}
	pos := bl.g.irPos(t.Line, t.Col)
	matched := bl.f.NewBlock(bl.g.irNodePos(t.Pattern), "pattern matched")
	failed := bl.f.NewBlock(pos, "pattern mismatch")
	if !bl.patternValueTest(t.Pattern, subj, sk, matched, failed) {
		return false
	}
	if irBlockTargeted(bl.f, failed.ID()) {
		text := renderNode(&ast.PatternDestructure{Pattern: t.Pattern, Value: t.Value, Line: t.Line, Col: t.Col})
		a := ir.NewAssertMismatch(pos, subj, text)
		if def != nil {
			a.WithBinding(*def)
		}
		failed.Append(a)
	}
	failed.SetTerm(ir.NewReturnUnit(pos))
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
	held, isBound := bl.bound[id.Name]
	b := bl.testDefs[held]
	if !isBound || b == nil {
		// A name a pattern or a context pattern bound: debug information
		// is recorded only for an ordinary binding.
		return nil, true
	}
	def := &ir.AssertBinding{Name: id.Name, Expr: renderNode(b.Value), Val: val}
	if pipe, isBinary := b.Value.(*ast.Binary); isBinary && pipe.Op == "|>" {
		stages, recorded := bl.testStages[held]
		if !recorded {
			bl.g.dc.note("a defined-as block over a pipe binding")
			return nil, false
		}
		def.Stages = stages
	}
	return def, true
}

// irBlockTargeted reports whether any block of f branches, jumps or faults
// to id.
func irBlockTargeted(f *ir.Func, id ir.BlockID) bool {
	var succ []ir.BlockID
	for _, b := range f.Blocks() {
		if h, faults := b.Fault(); faults && h == id {
			return true
		}
		if t := b.Term(); t != nil {
			succ = t.AppendSuccessors(succ[:0])
			for _, s := range succ {
				if s == id {
					return true
				}
			}
		}
	}
	return false
}
