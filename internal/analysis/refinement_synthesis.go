package analysis

import (
	"fmt"
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
)

// The functions every refinement has (spec §4 *Refinement types*), written
// by the front end as an inherent `impl` block in the declaring file, so
// they check, lower, link and show in an editor as functions written there:
//
//	impl Port
//	    pub fn try_from(value: Int) -> Maybe<Port>
//	        rule: (Int) -> Bool = <the declared rule>
//	        case <Int's check of value>
//	            Some(parent) -> if <rule(parent), False when it traps> then Some(<Port from parent>) else None
//	            _ -> None
//	        end
//	    end
//
//	    fn unchecked(value: Int) -> Port
//	        <Port from value>
//	    end
//	end
//
// `try_from` is as public as the type. `unchecked` is private to the
// declaring file. Its parameter is the refinement's base
// (ast.RefinementBaseType), which the declaring file need not import.
//
// The parent's check (ast.RefinementParentCheck) is the parent's own
// `try_from` when the parent is a refinement, so a chain checks every rule
// from the base up, and `Some(value)` when it is the base; which one is the
// checker's to say, since a parent declared in another file is no syntax
// here. A struct refinement has no parent: its `try_from` builds the value
// from the record of its fields and checks the rule on it.
//
// The rule node is the declaration's own, placed in the binding's value, so
// a diagnostic about it, its hover and its lowering are at its source
// position. Every other node is in the synthesized line band, at the slot of
// the declaration's universal Debug (synthKindAutoDebug), past the lines
// that impl uses: a distinct refinement has no Debug of its own, and a
// struct refinement's Debug takes few.

// refinementSynthOffset is how far into the declaration's synthKindAutoDebug
// slot the refinement's functions start.
const refinementSynthOffset = synthLineStride / 2

// synthesizeRefinements answers the impl block of `try_from` and
// `unchecked` for each refinement among nodes, a file's top-level
// declarations, indexed as SynthesizeUniversalDebug indexes them.
func synthesizeRefinements(nodes []ast.Node, index []int) []ast.Node {
	done := map[string]bool{}
	for _, n := range nodes {
		// Idempotent: a block this pass wrote before is in nodes already.
		if ib, ok := n.(*ast.ImplBlock); ok && ib.Interface == nil && ib.SynthOriginLine != 0 && IsSynthesizedLine(ib.Line) {
			done[TypeExprBaseName(ib.Receiver)] = true
		}
	}
	var out []ast.Node
	for i, n := range nodes {
		if name, ok := typeDeclName(n); !ok || done[name] {
			continue
		}
		var block *ast.ImplBlock
		switch d := n.(type) {
		case *ast.TypeDef:
			if d.Rule == nil || d.InnerTypeExpr == nil || len(d.TypeParams) > 0 {
				continue
			}
			block = refinementImpl(d.Name, d.Public, d.Rule, d.InnerTypeExpr, synthSiteOf(index[i], n))
		case *ast.StructDef:
			if d.Rule == nil || len(d.TypeParams) > 0 {
				continue
			}
			block = refinementImpl(d.Name, d.Public, d.Rule, nil, synthSiteOf(index[i], n))
		default:
			continue
		}
		if block != nil {
			out = append(out, block)
		}
	}
	return out
}

// isDistinctRefinementDecl reports whether n declares a distinct refinement.
func isDistinctRefinementDecl(n ast.Node) bool {
	td, ok := n.(*ast.TypeDef)
	return ok && td.Rule != nil && td.InnerTypeExpr != nil
}

// refinementImpl builds the block for the refinement name with rule and,
// for a distinct refinement, the parent it refines.
func refinementImpl(name string, public bool, rule ast.Node, parent ast.TypeExpr, site synthSite) *ast.ImplBlock {
	line := site.base(synthKindAutoDebug) + refinementSynthOffset
	col := 1
	names := freshRefinementNames(rule)
	tryFrom := refinementTryFrom(name, public, parent, names, line, col)
	if tryFrom == nil {
		return nil
	}
	unchecked := &ast.FuncDef{
		Name:           "unchecked",
		Params:         []ast.Param{{Name: names.value, TypeAnnotation: &ast.RefinementBaseType{Refinement: name, Line: line, Col: col}, Line: line, Col: col}},
		ReturnTypeExpr: &ast.SimpleType{Name: name, Line: line, Col: col},
		Body: &ast.Block{
			Stmts: []ast.Node{exprStmt(&ast.UncheckedRefinement{Refinement: name, Value: identExpr(names.value, line, col), Line: line, Col: col}, line, col)},
			Line:  line,
			Col:   col,
		},
		Line: line,
		Col:  col,
	}
	// The binding's value is a placeholder while positions are handed out,
	// so the rule keeps its own.
	binding := tryFrom.Body.Stmts[0].(*ast.Binding)
	uniquifyPositions(tryFrom, line)
	uniquifyPositions(unchecked, line+1)
	binding.Value = rule
	return &ast.ImplBlock{
		Receiver:        &ast.SimpleType{Name: name, Line: line, Col: col},
		Items:           []ast.Node{tryFrom, unchecked},
		Line:            line,
		Col:             col,
		SynthOriginLine: site.line,
		SynthOriginCol:  site.col,
	}
}

// refinementNames are the local names a generated `try_from` binds, chosen
// so none is a name the rule reads: the rule is checked inside the function,
// and a parameter named as a constant it reads would shadow it.
type refinementNames struct {
	value, parent, rule string
}

// freshRefinementNames picks refinementNames that rule does not mention.
func freshRefinementNames(rule ast.Node) refinementNames {
	used := map[string]bool{}
	ast.Inspect(rule, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			used[id.Name] = true
		}
		return true
	})
	fresh := func(base string) string {
		name := base
		for i := 2; used[name]; i++ {
			name = base + strconv.Itoa(i)
		}
		used[name] = true
		return name
	}
	return refinementNames{value: fresh("value"), parent: fresh("parent"), rule: fresh("rule")}
}

// refinementTryFrom builds `try_from` with a placeholder for the rule. A
// struct refinement (parent nil) checks the value it builds from the record
// of its fields; a distinct refinement checks its parent's rules first.
func refinementTryFrom(name string, public bool, parent ast.TypeExpr, names refinementNames, line, col int) *ast.FuncDef {
	self := func() ast.TypeExpr { return &ast.SimpleType{Name: name, Line: line, Col: col} }
	maybeSelf := &ast.GenericType{Name: "Maybe", Params: []ast.TypeExpr{self()}, Line: line, Col: col}
	ruleParam := self()
	if parent != nil {
		cloned, ok := cloneRefinementParent(parent, line, col)
		if !ok {
			return nil
		}
		ruleParam = cloned
	}
	binding := &ast.Binding{
		Name:           names.rule,
		TypeAnnotation: &ast.FuncType{Params: []ast.TypeExpr{ruleParam}, Return: &ast.SimpleType{Name: "Bool", Line: line, Col: col}, Line: line, Col: col},
		Value:          identExpr(names.rule, line, col),
		Line:           line,
		Col:            col,
	}
	some := func(v ast.Node) ast.Node {
		return &ast.Call{Func: &ast.TypeIdent{Name: "Some", Line: line, Col: col}, Args: []ast.Node{v}, Line: line, Col: col}
	}
	none := func() ast.Node { return &ast.TypeIdent{Name: "None", Line: line, Col: col} }
	block := func(e ast.Node) *ast.Block {
		return &ast.Block{Stmts: []ast.Node{exprStmt(e, line, col)}, Line: line, Col: col}
	}
	checked := func(v ast.Node) ast.Node {
		return &ast.If{
			Cond:   &ast.RefinementRule{Rule: identExpr(names.rule, line, col), Value: v, Line: line, Col: col},
			Then:   block(some(&ast.UncheckedRefinement{Refinement: name, Value: identExpr(names.parent, line, col), Line: line, Col: col})),
			Else:   block(none()),
			Inline: true,
			Line:   line,
			Col:    col,
		}
	}
	var stmts []ast.Node
	if parent == nil {
		// candidate = <S from value>; if rule(candidate) then Some(candidate) else None
		stmts = []ast.Node{
			binding,
			&ast.Binding{Name: names.parent, Value: &ast.UncheckedRefinement{Refinement: name, Value: identExpr(names.value, line, col), Line: line, Col: col}, Line: line, Col: col},
			exprStmt(&ast.If{
				Cond:   &ast.RefinementRule{Rule: identExpr(names.rule, line, col), Value: identExpr(names.parent, line, col), Line: line, Col: col},
				Then:   block(some(identExpr(names.parent, line, col))),
				Else:   block(none()),
				Inline: true,
				Line:   line,
				Col:    col,
			}, line, col),
		}
	} else {
		parentCheck := &ast.RefinementParentCheck{
			Parent:  mustCloneRefinementParent(parent, line, col),
			Refined: refinedParentCall(parent, names.value, line, col),
			Plain:   some(identExpr(names.value, line, col)),
			Line:    line,
			Col:     col,
		}
		stmts = []ast.Node{
			binding,
			exprStmt(&ast.Case{
				Value: parentCheck,
				Branches: []ast.CaseBranch{
					{Pattern: enumBindPattern("Some", names.parent, line, col), Body: checked(identExpr(names.parent, line, col)), Line: line, Col: col},
					{Pattern: &ast.WildcardPattern{Line: line, Col: col}, Body: none(), Line: line, Col: col},
				},
				Line: line,
				Col:  col,
			}, line, col),
		}
	}
	return &ast.FuncDef{
		Name:           "try_from",
		Doc:            fmt.Sprintf("`Some` of the value when it satisfies %s's rule and the rule of each type %s refines, `None` when it does not.", name, name),
		Public:         public,
		Params:         []ast.Param{{Name: names.value, TypeAnnotation: &ast.RefinementBaseType{Refinement: name, Line: line, Col: col}, Line: line, Col: col}},
		ReturnTypeExpr: maybeSelf,
		Body:           &ast.Block{Stmts: stmts, Line: line, Col: col},
		Line:           line,
		Col:            col,
	}
}

// refinedParentCall is `Parent.try_from(value)`, or nil for a parent that
// names no type that could be a refinement (a generic, tuple or function
// type), which the checker then reads as the base.
func refinedParentCall(parent ast.TypeExpr, value string, line, col int) ast.Node {
	var owner ast.Node
	switch p := parent.(type) {
	case *ast.SimpleType:
		owner = &ast.TypeIdent{Name: p.Name, Line: line, Col: col}
	case *ast.QualifiedType:
		member, ok := p.Member.(*ast.SimpleType)
		if !ok {
			return nil
		}
		owner = fieldAccess(identExpr(p.Module, line, col), member.Name, line, col)
	default:
		return nil
	}
	return &ast.Call{Func: fieldAccess(owner, "try_from", line, col), Args: []ast.Node{identExpr(value, line, col)}, Line: line, Col: col}
}

// cloneRefinementParent copies a refinement's parent type expression at the
// synthesized position.
func cloneRefinementParent(te ast.TypeExpr, line, col int) (ast.TypeExpr, bool) {
	return cloneTypeExpr(te, line, col)
}

func mustCloneRefinementParent(te ast.TypeExpr, line, col int) ast.TypeExpr {
	c, _ := cloneRefinementParent(te, line, col)
	return c
}
