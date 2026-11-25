package analysis

import (
	"fmt"
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
)

// The functions every subtype, validated struct, validated type and
// `type` over a subtype has (spec §4 *Subtypes*, *Validated types*), written
// by the front end as an inherent `impl` block in the declaring file, so
// they check, lower, link and show in an editor as functions written there:
//
//	impl Port
//	    pub fn try_from(value: Int) -> Option<Port>
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
// `try_from` has the type's visibility. `unchecked` is private to the
// declaring file. Its parameter is the subtype's base
// (ast.SubtypeBaseType), which the declaring file need not import. A
// `type`'s `try_from` is instead its `impl TryFrom<base> for T` (tryFromImpl),
// and the blocks of a `type` with no rule of its own are withheld until
// names resolve (releaseWithheldSubtype).
//
// The parent's check (ast.SubtypeParentCheck) is the parent's own
// `try_from` when the parent is a subtype, so a chain checks every rule
// from the base up, and `Some(value)` when it is the base; a subtype of
// several parents, `subtype PositiveInt NonNegativeInt and NonZeroInt when
// ...`, checks each in the order written, nesting a `case` per parent
// after the first, and its rule takes the first parent's value. A rule two
// parents share through their own parents runs once for each, since a
// rule is reached only through its type's `try_from`; it is pure, so that
// costs time and changes nothing else; which one is the
// checker's to say, since a parent declared in another file is no syntax
// here. A validated struct has no parent: its `try_from` builds the value
// from the record of its fields and checks the rule on it.
//
// The rule node is the declaration's own, placed in the binding's value, so
// a diagnostic about it, its hover and its lowering are at its source
// position. Every other node is in the synthesized line band, at the slot of
// the declaration's universal Debug (synthKindAutoDebug), past the lines
// that impl uses: a distinct subtype has no Debug of its own, and a
// validated struct's Debug takes few.

// subtypeSynthOffset is how far into the declaration's synthKindAutoDebug
// slot the subtype's functions start.
const subtypeSynthOffset = synthLineStride / 2

// synthesizeSubtypes answers the impl block of `try_from` and
// `unchecked` for each subtype among nodes, a file's top-level
// declarations, indexed as SynthesizeUniversalDebug indexes them.
func synthesizeSubtypes(nodes []ast.Node, index []int) []ast.Node {
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
			if d.InnerTypeExpr == nil {
				continue
			}
			// A `type` with no rule of its own has a rule in its chain
			// only when its inner type is a subtype, which only the
			// names it resolves to can say: its blocks are withheld,
			// and the analyzer releases them once names resolve
			// (releaseWithheldSubtype).
			withhold := !d.Subtype && d.Rule == nil
			if withhold && !couldNameSubtype(d.InnerTypeExpr, d.TypeParams) {
				continue
			}
			parents := append([]ast.TypeExpr{d.InnerTypeExpr}, d.MoreParents...)
			if conj, ok := d.InnerTypeExpr.(*ast.ConjunctionType); ok && !d.Subtype {
				// `type Strict NonNegativeInt and NonZeroInt`: each member
				// is checked as a subtype's parent is.
				parents = conj.Members
			}
			block = subtypeImpl(subtypeHead{name: d.Name, typeParams: d.TypeParams, where: d.WhereClauses}, d.Visibility, d.Rule, parents, synthSiteOf(index[i], n))
			if block != nil && !d.Subtype {
				// The type implements `TryFrom` of its base because of the
				// rule in its chain: `try_from` is that impl's, and
				// `unchecked` stays the type's own.
				impl := tryFromImpl(block, d)
				if withhold {
					impl.Withhold()
					block.Withhold()
				}
				out = append(out, impl)
			}
		case *ast.StructDef:
			if d.Rule == nil || len(d.TypeParams) > 0 {
				continue
			}
			block = subtypeImpl(subtypeHead{name: d.Name}, d.Visibility, d.Rule, nil, synthSiteOf(index[i], n))
		default:
			continue
		}
		if block != nil {
			out = append(out, block)
		}
	}
	return out
}

// builtinTypeNames are the prelude types no `subtype` declares, so a
// `type` over one wraps no subtype.
var builtinTypeNames = map[string]bool{
	"Int": true, "Float": true, "Decimal": true, "String": true, "Bool": true,
	"Byte": true, "Bytes": true, "Unit": true, "Codepoint": true, "Duration": true,
	"List": true, "Map": true, "Set": true, "Vector": true, "Option": true,
	"Result": true, "Range": true, "Iter": true,
}

// couldNameSubtype reports whether te, the inner type of a `type` with no
// rule of its own, is written as a name that could resolve to a subtype:
// a name, generic or not, other than a builtin type or one of the type's
// own parameters, a qualified name, or an intersection. A tuple or
// function type is none. Which of these names a subtype is the
// analyzer's to say once names resolve (releaseWithheldSubtype).
func couldNameSubtype(te ast.TypeExpr, params []ast.TypeParam) bool {
	var name string
	switch t := te.(type) {
	case *ast.SimpleType:
		name = t.Name
	case *ast.GenericType:
		name = t.Name
	case *ast.QualifiedType, *ast.ConjunctionType:
		return true
	default:
		return false
	}
	if builtinTypeNames[name] {
		return false
	}
	for _, tp := range params {
		if tp.Name == name {
			// `type Wrap<T> T`: a type argument a subtype could stand for
			// is checked where it is built, as the inner type's own.
			return false
		}
	}
	return true
}

// tryFromImpl moves the `try_from` of block, the generated functions of
// the `type` d, into `impl TryFrom<base(d)> for d`, which the type has
// because of its rule (spec §4 *Validated types*), and leaves `unchecked`
// in block.
func tryFromImpl(block *ast.ImplBlock, d *ast.TypeDef) *ast.ImplBlock {
	var tryFrom *ast.FuncDef
	var rest []ast.Node
	for _, item := range block.Items {
		if fd, ok := item.(*ast.FuncDef); ok && fd.Name == "try_from" {
			tryFrom = fd
			continue
		}
		rest = append(rest, item)
	}
	block.Items = rest
	// The header on a line of its own, past the two uniquifyPositions
	// gives the functions, so its names' references collide with none.
	line := tryFrom.Line + 2
	tryFrom.Visibility = ast.Internal
	tryFrom.Doc = fmt.Sprintf("`TryFrom.try_from` for %s, which its rule supplies: `Some` of the value when it satisfies %s's rules, `None` when it does not.", d.Name, d.Name)
	if p := &tryFrom.Params[0]; p.Name != tryFromParam {
		// The rule names `value`, which a parameter of that name would
		// shadow inside the function. The parameter keeps the interface's
		// name, which an impl's parameter must have and a call's named
		// argument uses, and binds the value under the fresh one.
		p.Destructure = &ast.IdentPattern{Name: p.Name, Line: p.Line, Col: p.Col}
		p.Name = tryFromParam
	}
	// A generic type's impl is generic over its parameters, which the
	// function then names as the block's rather than declaring them.
	where := tryFrom.WhereClauses
	tryFrom.TypeParams, tryFrom.WhereClauses = nil, nil
	head := subtypeHead{name: d.Name, typeParams: d.TypeParams, where: d.WhereClauses}
	return &ast.ImplBlock{
		Interface:       &ast.GenericType{Name: "TryFrom", Params: []ast.TypeExpr{head.base(line, 2)}, Line: line, Col: 1},
		Receiver:        head.self(line, 3),
		WhereClauses:    where,
		Items:           []ast.Node{tryFrom},
		Line:            line,
		Col:             1,
		SynthOriginLine: block.SynthOriginLine,
		SynthOriginCol:  block.SynthOriginCol,
	}
}

// providedConversion answers fn as one of the functions this pass writes,
// a `try_from` or an `unchecked`, and nil for any other function. Each
// builds a value of its type from something else, so for an opaque type it
// is construction, private to the defining file as the type's call form
// is (refuseProvidedConversion). A `try_from` the file writes itself, in
// an `impl TryFrom<E>` of its own, is the file's public function.
func providedConversion(fn ast.Node) *ast.FuncDef {
	fd, ok := fn.(*ast.FuncDef)
	if !ok || !IsSynthesizedLine(fd.Line) || (fd.Name != "try_from" && fd.Name != "unchecked") {
		return nil
	}
	return fd
}

// refuseProvidedConversion reports, at pos, a use of fn when fn is a
// conversion this pass provides (providedConversion) for an opaque type
// this file may not build (refuseOpaqueConstruction). recv is the type the
// checker called the function for, nil where it records none; then the
// built type is read from fnType, the function's type, whose result is the
// type for `unchecked` and an `Option` of it for `try_from`.
func (c *checker) refuseProvidedConversion(pos Pos, fn ast.Node, recv, fnType Type) {
	fd := providedConversion(fn)
	if fd == nil {
		return
	}
	built := recv
	if built == nil {
		ft, ok := resolveTypeVar(fnType).(*FuncType)
		if !ok {
			return
		}
		built = resolveTypeVar(ft.Return)
		if m, isOption := built.(*EnumType); isOption && fd.Name == "try_from" && len(m.TypeArgs) == 1 {
			built = m.TypeArgs[0]
		}
	}
	c.refuseOpaqueConstruction(pos.Line, pos.Col, built, fd.Name)
}

// refuseProvidedConversionCalls is refuseProvidedConversion at each call
// this check recorded, of the function its callee reaches once the call's
// impl is chosen (settleCallees): `Kelvin.try_from(1.0)` reaches the
// provided `TryFrom<Float>` impl where Kelvin's file writes `impl
// TryFrom<Celsius>` too. A call through the interface,
// `TryFrom.try_from(7)` where an `Option<Pct>` is expected, demands Pct's
// impl, which outside Pct's file it does not have (opaque_bounds.go).
func (c *checker) refuseProvidedConversionCalls() {
	for _, n := range c.recordedCalls {
		call, ok := n.(*ast.Call)
		if !ok {
			continue
		}
		pos, named := calleeKey(call.Func)
		rc := c.fa.Calls[n]
		if !named || rc == nil {
			continue
		}
		if rc.Callee.Copy != nil {
			// A call through a local copy, `f(3)` after `f =
			// Pct.try_from`: the copy was refused where it was made.
			continue
		}
		if acc, ok := call.Func.(*ast.FieldAccess); ok && c.fa.qualifierOf(acc.Object).Kind == QualInterface {
			// `TryFrom.try_from(7)` demands Pct's TryFrom impl, which
			// does not count outside Pct's file (opaque_bounds.go).
			continue
		}
		recv := rc.Callee.For
		var fnType Type
		if ref := rc.Callee.Ref; ref != nil {
			if recv == nil {
				recv = ref.DispatchReceiver
			}
			fnType = ref.Type
		}
		c.refuseProvidedConversion(pos, rc.Callee.Decl, recv, fnType)
	}
}

// tryFromParam is the name `TryFrom.try_from` gives its parameter
// (std/convert.nomi), which an impl's parameter must have.
const tryFromParam = "value"

// isDistinctSubtypeDecl reports whether n declares a subtype.
func isDistinctSubtypeDecl(n ast.Node) bool {
	td, ok := n.(*ast.TypeDef)
	return ok && td.Subtype && td.InnerTypeExpr != nil
}

// subtypeHead is what a subtype's generated functions are generic
// over: its name, and a generic subtype's type parameters and their
// bounds, which each function declares as its own, as std's functions on a
// generic type do (`impl List<T>` with `fn length<T>(...)`), so a call
// instantiates it.
type subtypeHead struct {
	name       string
	typeParams []ast.TypeParam
	where      []ast.WhereConstraint
}

// self is the subtype at its own type parameters: `NonEmptyList<T>`.
func (h subtypeHead) self(line, col int) ast.TypeExpr {
	if len(h.typeParams) == 0 {
		return &ast.SimpleType{Name: h.name, Line: line, Col: col}
	}
	return &ast.GenericType{Name: h.name, Params: h.args(line, col), Line: line, Col: col}
}

// args are the type parameters as type arguments.
func (h subtypeHead) args(line, col int) []ast.TypeExpr {
	var out []ast.TypeExpr
	for _, tp := range h.typeParams {
		out = append(out, &ast.SimpleType{Name: tp.Name, Line: line, Col: col})
	}
	return out
}

// base is the type the subtype's values are built from, at its type
// parameters.
func (h subtypeHead) base(line, col int) ast.TypeExpr {
	return &ast.SubtypeBaseType{Subtype: h.name, Params: h.args(line, col), Line: line, Col: col}
}

// generic gives fd the subtype's type parameters and bounds as its own.
func (h subtypeHead) generic(fd *ast.FuncDef, line, col int) {
	for _, tp := range h.typeParams {
		fd.TypeParams = append(fd.TypeParams, ast.TypeParam{Name: tp.Name, Line: line, Col: col})
	}
	for _, wc := range h.where {
		c := ast.WhereConstraint{Name: wc.Name, Line: line, Col: col}
		for _, b := range wc.Bounds {
			if cloned, ok := cloneTypeExpr(b, line, col); ok {
				c.Bounds = append(c.Bounds, cloned)
			}
		}
		fd.WhereClauses = append(fd.WhereClauses, c)
	}
}

// subtypeImpl builds the block for the subtype head names with rule
// and, for a distinct subtype, the parents it narrows, its first first.
func subtypeImpl(head subtypeHead, vis ast.Visibility, rule ast.Node, parents []ast.TypeExpr, site synthSite) *ast.ImplBlock {
	name := head.name
	line := site.base(synthKindAutoDebug) + subtypeSynthOffset
	col := 1
	names := freshSubtypeNames(rule)
	tryFrom := subtypeTryFrom(head, vis, rule != nil, parents, names, line, col)
	if tryFrom == nil {
		return nil
	}
	unchecked := &ast.FuncDef{
		Name:           "unchecked",
		Visibility:     ast.Private,
		Params:         []ast.Param{{Name: names.value, TypeAnnotation: head.base(line, col), Line: line, Col: col}},
		ReturnTypeExpr: head.self(line, col),
		Body: &ast.Block{
			Stmts: []ast.Node{exprStmt(&ast.UncheckedSubtype{Subtype: name, Value: identExpr(names.value, line, col), Line: line, Col: col}, line, col)},
			Line:  line,
			Col:   col,
		},
		Line: line,
		Col:  col,
	}
	// The binding's value is a placeholder while positions are handed out,
	// so the rule keeps its own.
	binding, _ := tryFrom.Body.Stmts[0].(*ast.Binding)
	head.generic(tryFrom, line, col)
	head.generic(unchecked, line, col)
	uniquifyPositions(tryFrom, line)
	uniquifyPositions(unchecked, line+1)
	if rule != nil && binding != nil {
		binding.Value = rule
	}
	return &ast.ImplBlock{
		Receiver:        head.self(line, col),
		Items:           []ast.Node{tryFrom, unchecked},
		Line:            line,
		Col:             col,
		SynthOriginLine: site.line,
		SynthOriginCol:  site.col,
	}
}

// subtypeNames are the local names a generated `try_from` binds, chosen
// so none is a name the rule reads: the rule is checked inside the function,
// and a parameter named as a constant it reads would shadow it.
type subtypeNames struct {
	value, parent, rule string
}

// freshSubtypeNames picks subtypeNames that rule does not mention.
func freshSubtypeNames(rule ast.Node) subtypeNames {
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
	return subtypeNames{value: fresh("value"), parent: fresh("parent"), rule: fresh("rule")}
}

// subtypeTryFrom builds `try_from` with a placeholder for the rule. A
// validated struct (no parents) checks the value it builds from the record
// of its fields; a distinct subtype checks each parent's rules first, in
// the order they are written, and its rule takes its first parent.
func subtypeTryFrom(head subtypeHead, vis ast.Visibility, hasRule bool, parents []ast.TypeExpr, names subtypeNames, line, col int) *ast.FuncDef {
	name := head.name
	self := func() ast.TypeExpr { return head.self(line, col) }
	optionSelf := &ast.GenericType{Name: "Option", Params: []ast.TypeExpr{self()}, Line: line, Col: col}
	ruleParam := self()
	if len(parents) > 0 {
		cloned, ok := cloneSubtypeParent(parents[0], line, col)
		if !ok {
			return nil
		}
		ruleParam = cloned
		for _, p := range parents[1:] {
			if _, ok := cloneSubtypeParent(p, line, col); !ok {
				return nil
			}
		}
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
		if !hasRule {
			// A subtype with no rule of its own: its parents' checks
			// are all there is.
			return some(&ast.UncheckedSubtype{Subtype: name, Value: identExpr(names.parent, line, col), Line: line, Col: col})
		}
		return &ast.If{
			Cond:   &ast.SubtypeRule{Rule: identExpr(names.rule, line, col), Value: v, Line: line, Col: col},
			Then:   block(some(&ast.UncheckedSubtype{Subtype: name, Value: identExpr(names.parent, line, col), Line: line, Col: col})),
			Else:   block(none()),
			Inline: true,
			Line:   line,
			Col:    col,
		}
	}
	parentCheck := func(parent ast.TypeExpr) ast.Node {
		return &ast.SubtypeParentCheck{
			Parent:  mustCloneSubtypeParent(parent, line, col),
			Subtype: subtypeParentCall(parent, names.value, line, col),
			Plain:   some(identExpr(names.value, line, col)),
			Line:    line,
			Col:     col,
		}
	}
	var stmts []ast.Node
	if len(parents) == 0 {
		// candidate = <S from value>; if rule(candidate) then Some(candidate) else None
		stmts = []ast.Node{
			binding,
			&ast.Binding{Name: names.parent, Value: &ast.UncheckedSubtype{Subtype: name, Value: identExpr(names.value, line, col), Line: line, Col: col}, Line: line, Col: col},
			exprStmt(&ast.If{
				Cond:   &ast.SubtypeRule{Rule: identExpr(names.rule, line, col), Value: identExpr(names.parent, line, col), Line: line, Col: col},
				Then:   block(some(identExpr(names.parent, line, col))),
				Else:   block(none()),
				Inline: true,
				Line:   line,
				Col:    col,
			}, line, col),
		}
	} else {
		// The parents after the first, innermost last: each must answer
		// Some before the rule runs on the first's value.
		body := checked(identExpr(names.parent, line, col))
		for i := len(parents) - 1; i >= 1; i-- {
			body = &ast.Case{
				Value: parentCheck(parents[i]),
				Branches: []ast.CaseBranch{
					{Pattern: &ast.EnumPattern{Variant: &ast.SimpleType{Name: "Some", Line: line, Col: col}, Payload: &ast.WildcardPattern{Line: line, Col: col}, Line: line, Col: col}, Body: body, Line: line, Col: col},
					{Pattern: &ast.WildcardPattern{Line: line, Col: col}, Body: none(), Line: line, Col: col},
				},
				Line: line,
				Col:  col,
			}
		}
		stmts = []ast.Node{
			exprStmt(&ast.Case{
				Value: parentCheck(parents[0]),
				Branches: []ast.CaseBranch{
					{Pattern: enumBindPattern("Some", names.parent, line, col), Body: body, Line: line, Col: col},
					{Pattern: &ast.WildcardPattern{Line: line, Col: col}, Body: none(), Line: line, Col: col},
				},
				Line: line,
				Col:  col,
			}, line, col),
		}
	}
	if hasRule && len(parents) > 0 {
		stmts = append([]ast.Node{binding}, stmts...)
	}
	return &ast.FuncDef{
		Name:           "try_from",
		Doc:            fmt.Sprintf("`Some` of the value when it satisfies %s's rules, `None` when it does not.", name),
		Visibility:     vis,
		Params:         []ast.Param{{Name: names.value, TypeAnnotation: head.base(line, col), Line: line, Col: col}},
		ReturnTypeExpr: optionSelf,
		Body:           &ast.Block{Stmts: stmts, Line: line, Col: col},
		Line:           line,
		Col:            col,
	}
}

// subtypeParentCall is `Parent.try_from(value)`, or nil for a parent that
// names no type that could be a subtype (a generic, tuple or function
// type), which the checker then reads as the base.
func subtypeParentCall(parent ast.TypeExpr, value string, line, col int) ast.Node {
	var owner ast.Node
	switch p := parent.(type) {
	case *ast.SimpleType:
		owner = &ast.TypeIdent{Name: p.Name, Line: line, Col: col}
	case *ast.GenericType:
		// `NonEmptyList<T>`: its `try_from` is generic, and the value
		// solves its type arguments.
		owner = &ast.TypeIdent{Name: p.Name, Line: line, Col: col}
	case *ast.QualifiedType:
		var memberName string
		switch m := p.Member.(type) {
		case *ast.SimpleType:
			memberName = m.Name
		case *ast.GenericType:
			memberName = m.Name
		default:
			return nil
		}
		owner = fieldAccess(identExpr(p.Module, line, col), memberName, line, col)
	default:
		return nil
	}
	return &ast.Call{Func: fieldAccess(owner, "try_from", line, col), Args: []ast.Node{identExpr(value, line, col)}, Line: line, Col: col}
}

// cloneSubtypeParent copies a subtype's parent type expression at the
// synthesized position.
func cloneSubtypeParent(te ast.TypeExpr, line, col int) (ast.TypeExpr, bool) {
	return cloneTypeExpr(te, line, col)
}

func mustCloneSubtypeParent(te ast.TypeExpr, line, col int) ast.TypeExpr {
	c, _ := cloneSubtypeParent(te, line, col)
	return c
}

// releaseWithheldSubtype decides the blocks synthesizeSubtypes
// withheld for n, a `type` with no rule of its own: they exist when its
// inner type resolves to a subtype, or to an intersection with one among
// its members, since then the type has a rule in its chain (spec §4
// *Validated types*). It runs in the annotation pass, when the file's
// declarations have resolved their names and every file's have their
// symbols, so a subtype another file declares, imports, or an alias of
// one, is known. A kept block is stubbed here, which the stub pass left
// to it; a block a build of the same nodes kept earlier and this one
// drops loses the symbols that pass gave it.
func (b *builder) releaseWithheldSubtype(n *ast.ImplBlock, scope *Scope) {
	if !n.Withheld() {
		return
	}
	var td *ast.TypeDef
	if sym := scope.LookupLocal(TypeExprBaseName(n.Receiver)); sym != nil && b.declaresHere(sym) {
		td, _ = sym.Node.(*ast.TypeDef)
	}
	keep := td != nil && b.resolvesToSubtype(td.InnerTypeExpr, b.file, 0)
	was := n.Released()
	if was && !keep {
		b.undefineImplBlockStub(n)
	}
	n.Release(keep)
	if keep && !was {
		b.defineImplBlockStub(n, scope)
	}
}

// resolvesToSubtype reports whether te, a type expression of the file fa
// whose names the builder has resolved (References), names a subtype or
// an intersection with one among its members.
func (b *builder) resolvesToSubtype(te ast.TypeExpr, fa *FileAnalysis, depth int) bool {
	if depth > 32 {
		return false // an alias cycle, which its declaration reports
	}
	if conj, ok := te.(*ast.ConjunctionType); ok {
		for _, m := range conj.Members {
			if b.resolvesToSubtype(m, fa, depth+1) {
				return true
			}
		}
		return false
	}
	at, name, ok := typeNamePos(te)
	if !ok {
		return false
	}
	sym := fa.References[at]
	if sym == nil && fa.ModuleScope != nil {
		// An alias in a file whose annotations have not run yet.
		if _, qualified := te.(*ast.QualifiedType); !qualified {
			sym = fa.ModuleScope.Lookup(name)
		}
	}
	for sym != nil && sym.Resolved != nil && sym.Resolved != sym {
		sym = sym.Resolved
	}
	if sym == nil {
		return false
	}
	switch d := sym.Node.(type) {
	case *ast.TypeDef:
		return d.Subtype && d.InnerTypeExpr != nil
	case *ast.TypeAlias:
		if d.TargetTypeExpr == nil {
			return false
		}
		if home := b.declaringFile(sym); home != nil {
			return b.resolvesToSubtype(d.TargetTypeExpr, home, depth+1)
		}
	}
	return sym.Type != nil && subtypeOf(sym.Type) != nil
}

// typeNamePos answers where te writes the name it resolves by, and the
// name: a name's own position, a generic's head, a qualified name's
// member.
func typeNamePos(te ast.TypeExpr) (Pos, string, bool) {
	switch t := te.(type) {
	case *ast.SimpleType:
		return Pos{Line: t.Line, Col: t.Col}, t.Name, true
	case *ast.GenericType:
		return Pos{Line: t.Line, Col: t.Col}, t.Name, true
	case *ast.QualifiedType:
		return typeNamePos(t.Member)
	}
	return Pos{}, "", false
}

// declaringFile answers the analysis of the file that declares sym, a
// top-level symbol: this builder's file, a file of the build's, or a
// stdlib module's.
func (b *builder) declaringFile(sym *Symbol) *FileAnalysis {
	if b.declaresHere(sym) {
		return b.file
	}
	for _, files := range []map[string]*FileAnalysis{b.cache, b.stdlibFAs} {
		for _, fa := range files {
			if fa != nil && fa.Definitions[sym.Pos] == sym {
				return fa
			}
		}
	}
	return nil
}

// undefineImplBlockStub takes back what defineImplBlockStub registered for
// n's items.
func (b *builder) undefineImplBlockStub(n *ast.ImplBlock) {
	recv := TypeExprBaseName(n.Receiver)
	var owner *Symbol
	if b.fileScope != nil {
		if owner = b.fileScope.LookupLocal(recv); owner != nil && owner.Resolved != nil {
			owner = owner.Resolved
		}
	}
	for _, item := range n.Items {
		fd, ok := item.(*ast.FuncDef)
		if !ok {
			continue
		}
		pos := Pos{Line: fd.Line, Col: fd.Col}
		sym := b.file.Definitions[pos]
		if sym == nil {
			continue
		}
		delete(b.file.Definitions, pos)
		if b.file.TypeMethods[recv][fd.Name] == sym {
			delete(b.file.TypeMethods[recv], fd.Name)
			delete(b.inherentTypeMethods, typeMethodSlot{recv: recv, name: fd.Name})
		}
		if owner != nil && owner.Members[fd.Name] == sym {
			delete(owner.Members, fd.Name)
		}
	}
}
