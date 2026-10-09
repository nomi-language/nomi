package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// tupleCaseComponents validates the complete pattern before changing the graph.
func tupleCaseComponents(pattern ast.Node, k kind) ([]int, bool) {
	pat, ok := pattern.(*ast.TuplePattern)
	if !ok || !irRetainedTupleKind(k) || len(pat.Patterns) != len(k.comp.parts) {
		return nil, false
	}
	var tested []int
	for i, component := range pat.Patterns {
		switch component.(type) {
		case *ast.WildcardPattern:
		case *ast.IdentPattern:
		case *ast.IntLit:
			if k.comp.parts[i] != kindInt {
				return nil, false
			}
			tested = append(tested, i)
		case *ast.StringLit:
			if k.comp.parts[i] != kindString {
				return nil, false
			}
			tested = append(tested, i)
		default:
			// A nested pattern (`(Some(id), _)`) is a component test that
			// patternValueTest builds.
			if !irFieldSubPattern(component, k.comp.parts[i]) {
				return nil, false
			}
			tested = append(tested, i)
		}
	}
	return tested, true
}

func tupleCaseIrrefutable(pattern ast.Node, k kind) bool {
	tested, ok := tupleCaseComponents(pattern, k)
	return ok && len(tested) == 0
}

func (bl *irScalarBuilder) tupleCaseTest(pattern ast.Node, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	tested, ok := tupleCaseComponents(pattern, k)
	if !ok || len(tested) == 0 {
		irDeclineNote("a `case` tuple pattern outside flat Int/String tests with bindings and wildcards")
		return false
	}
	pat := pattern.(*ast.TuplePattern)
	j := 0
	for i, component := range pat.Patterns {
		if _, wildcard := component.(*ast.WildcardPattern); wildcard {
			continue
		}
		part := bl.tupleProjection(pat, subj, i, k.comp.parts[i])
		if id, binding := component.(*ast.IdentPattern); binding {
			bl.tuplePatternBinding(id, part, k.comp.parts[i])
			continue
		}
		success := arm
		if j != len(tested)-1 {
			success = bl.f.NewBlock(bl.g.irNodePos(component), "next component")
		}
		j++
		switch component.(type) {
		case *ast.IntLit, *ast.StringLit:
		default:
			if !bl.patternValueTest(component, part, k.comp.parts[i], success, next) {
				return false
			}
			continue
		}
		lit, _, _, ok := bl.litFor(component, k.comp.parts[i])
		if !ok {
			return false
		}
		m := ir.NewMatchLitInto(bl.g.irNodePos(component), bl.f.NewTemp(), part, lit)
		bl.b.Append(m)
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(component), m.Dst(), success.ID(), next.ID()))
		bl.b = success
	}
	return true
}

// tupleCaseBindings introduces the names of a validated flat tuple pattern.
// The caller selects the destination block and owns the lexical scope.
func (bl *irScalarBuilder) tupleCaseBindings(pattern ast.Node, subj ir.Temp, k kind) {
	pat := pattern.(*ast.TuplePattern)
	for i, component := range pat.Patterns {
		if id, binding := component.(*ast.IdentPattern); binding {
			part := bl.tupleProjection(pat, subj, i, k.comp.parts[i])
			bl.tuplePatternBinding(id, part, k.comp.parts[i])
		}
	}
}

func (bl *irScalarBuilder) tuplePatternBinding(id *ast.IdentPattern, part ir.Temp, k kind) {
	bl.patternBinding(id, id.Name, part, k)
}

// bindAsNames binds the name of each `as` around pattern to value, the value
// the whole pattern matched, innermost first, in the current block. A site
// that tests ast.WithoutAs(pattern) itself calls it on the success edge.
func (bl *irScalarBuilder) bindAsNames(pattern ast.Node, value ir.Temp, k kind) {
	var chain []*ast.AsPattern
	for a, ok := pattern.(*ast.AsPattern); ok; a, ok = a.Pattern.(*ast.AsPattern) {
		chain = append(chain, a)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		bl.patternBinding(chain[i], chain[i].Name, value, k)
	}
}

func (bl *irScalarBuilder) patternBinding(at ast.Node, name string, part ir.Temp, k kind) {
	if ast.IsDiscardName(name) {
		return
	}
	// Pattern declarations belong to this arm, including when they shadow an
	// enclosing declaration. Its caller owns the cloned lexical symbol tables.
	sym := ir.NewSymbol(name)
	bl.sh.syms[name] = sym
	bind := ir.NewBind(bl.g.irNodePos(at), bl.f.NewTemp(), part, sym)
	bl.b.Append(bind)
	bl.side(bind.Dst(), irScalarSide{k: k})
	bl.bound[name], bl.boundK[name] = bind.Dst(), k
}
