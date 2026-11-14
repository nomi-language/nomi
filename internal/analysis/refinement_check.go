package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// Checking values into refinements (refinement.go). A literal of the base
// written where a distinct refinement is expected is that refinement, and
// the compiler evaluates the rules on it when it builds the program; any
// other value of the base is an error that names `try_from`.

// RefinedLiteral is a literal checked as a refinement
// (FileAnalysis.RefinedLiterals).
type RefinedLiteral struct {
	// Type is the refinement the literal is.
	Type Type
	// Steps are the refinements of Type's chain, from the one that refines
	// the base up to Type: the order a diagnostic finds the first rule
	// that fails in. A struct refinement is its own one step.
	Steps []RefinementStep
}

// RefinementStep is one refinement of a RefinedLiteral's chain and its
// generated `try_from`, whose Some or None says whether the literal
// satisfies every rule up to it.
type RefinementStep struct {
	Type    Type
	TryFrom ast.Node
}

// refinementLiteral reports whether n, ungrouped, is a literal a refinement
// position checks at compile time: a number, a negated number, a string
// with no `${...}`, a codepoint, `True` or `False`.
func refinementLiteral(n ast.Node) bool {
	switch v := ungroupExpr(n).(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.StringLit, *ast.CodepointLit:
		return true
	case *ast.Unary:
		if v.Op != "-" {
			return false
		}
		switch v.Right.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit:
			return true
		}
	case *ast.TypeIdent:
		return v.Name == "True" || v.Name == "False"
	}
	return false
}

// checkRefinementLiteral checks node, a literal, where the refinement dt is
// expected: against dt's base, as the literal it is, and records it for the
// compile-time check of dt's rules (FileAnalysis.RefinedLiterals). The
// literal is a dt.
func (c *checker) checkRefinementLiteral(node ast.Node, dt *DistinctType) Type {
	base := RefinementBase(dt)
	ty := c.checkNodeExpecting(node, base)
	if ty != nil && !c.argMatchesParam(ty, base, c.recPos(startOf(node)), RecordingKindCallSite) {
		line, col := startOf(node)
		c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(dt), formatTypeForError(ty)))
		return dt
	}
	c.recordRefinedLiteral(node, dt)
	return dt
}

// recordRefinedLiteral notes that the literal node is checked as the
// refinement t, and makes a hover on it say so.
func (c *checker) recordRefinedLiteral(node ast.Node, t Type) {
	if c.fa.RefinedLiterals == nil {
		c.fa.RefinedLiterals = map[ast.Node]*RefinedLiteral{}
	}
	rec := &RefinedLiteral{Type: t}
	chain := RefinementChain(t)
	if _, isStruct := t.(*StructType); isStruct {
		chain = append(chain, nil)
	}
	// From the base's child up to t, so the first rule that fails is the
	// one the diagnostic names.
	for i := len(chain) - 2; i >= 0; i-- {
		step := RefinementStep{Type: chain[i]}
		if origin, nominal := nominalOrigin(chain[i]); nominal {
			if sym := c.lookupTypeMethodSymbol(chain[i].String(), "try_from", origin); sym != nil {
				step.TryFrom = sym.Node
			}
		}
		rec.Steps = append(rec.Steps, step)
	}
	c.fa.RefinedLiterals[node] = rec
	line, col := startOf(node)
	switch node.(type) {
	case *ast.StructLit, *ast.ListLit, *ast.MapLit:
		// A literal of many tokens has the hover of its type.
		return
	}
	if line <= 0 || IsSynthesizedLine(line) {
		return
	}
	c.fa.References[Pos{Line: line, Col: col}] = &Symbol{
		Name: refinementLiteralText(node),
		Kind: SymbolLiteral,
		Pos:  Pos{Line: line, Col: col},
		Type: t,
		Doc:  fmt.Sprintf("checked as %s %s when the program is compiled: `%s`", articleFor(typeName(t)), typeName(t), RefinementRule(t)),
		Span: len(refinementLiteralText(node)),
	}
}

// typeName is t's name as a diagnostic spells it.
func typeName(t Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}

// refinementLiteralText is how a diagnostic and a hover spell the literal.
func refinementLiteralText(n ast.Node) string {
	return RenderLiteral(n)
}

// RenderLiteral spells a literal as the source writes it.
func RenderLiteral(n ast.Node) string {
	return strings.Join(strings.Fields(format.RenderNode(n)), " ")
}

// refinementCtorArg checks the one argument of a distinct refinement's call
// form, `Port(8080)`: a literal is checked as a dt; any other value is an
// error naming `try_from`, since its rules would have to run when the
// program does.
func (c *checker) refinementCtorArg(arg ast.Node, dt *DistinctType, label string) {
	if refinementLiteral(arg) {
		c.checkRefinementLiteral(arg, dt)
		return
	}
	switch ungroupExpr(arg).(type) {
	case *ast.ListLit, *ast.MapLit:
		// `Pair([3, 4])`: a collection literal builds the refinement as
		// it does where one is expected, and checkCollectionRefinementLit
		// records it or reports its first part that is no literal.
		argTy := c.checkNodeExpecting(arg, dt)
		if argTy != nil && !TypeAssignable(dt, argTy) {
			line, col := startOf(arg)
			c.report(refinementNeedsCheck(line, col, dt, argTy, label+"(...)"))
		}
		return
	}
	argTy := c.checkNode(arg)
	if argTy != nil && TypeAssignable(dt, argTy) {
		// `Port(port)`: already a Port.
		return
	}
	line, col := startOf(arg)
	c.report(refinementNeedsCheck(line, col, dt, argTy, label+"(...)"))
}

// refinementNeedsCheck is the error for a value of a refinement's base, or
// of an ancestor, where the refinement is expected: its rules are checked
// only on a literal, so the value goes through `try_from`.
func refinementNeedsCheck(line, col int, dt *DistinctType, got Type, what string) TypeError {
	msg := fmt.Sprintf("%s takes a literal: %s is a refinement, and %s %s computed when the program runs may not satisfy its rule",
		what, dt.Name, articleFor(formatTypeForError(got)), formatTypeForError(got))
	if what == "" {
		msg = fmt.Sprintf("type mismatch: expected %s, got %s", dt.Name, formatTypeForError(got))
	}
	return TypeError{Line: line, Col: col, Message: msg}.WithHint(fmt.Sprintf(
		"check the value with `%s.try_from(...)`, which is `Some` when it satisfies `%s` and `None` when not", dt.Name, dt.Rule))
}

// refinementMismatchHint is the help for `type mismatch: expected Port, got
// Int`: a value of a refinement's base or ancestor goes through try_from.
func refinementMismatchHint(want, have Type) (string, bool) {
	dt := refinementOf(want)
	if dt == nil || have == nil || containsTypeVar(have) {
		return "", false
	}
	for _, anc := range RefinementChain(dt)[1:] {
		if TypesIdentical(anc, have) {
			return fmt.Sprintf("%s is a refinement of %s; check the value with `%s.try_from(...)`, which is `Some` when it satisfies `%s` and `None` when not",
				dt.Name, formatTypeForError(dt.Inner), dt.Name, dt.Rule), true
		}
	}
	return "", false
}

// checkUncheckedRefinement types a generated `unchecked`'s body: the value,
// of the refinement's base, as the refinement.
func (c *checker) checkUncheckedRefinement(n *ast.UncheckedRefinement) Type {
	t := c.reg.Lookup(n.Refinement)
	base := refinementBaseOf(t)
	if base == nil {
		c.checkNode(n.Value)
		return nil
	}
	have := c.checkNodeExpecting(n.Value, base)
	if have != nil && c.unifyInto(base, have, nil) != nil {
		line, col := startOf(n.Value)
		c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(base), formatTypeForError(have)))
	}
	return t
}

// checkRefinementRule types a generated `try_from`'s call of its rule: the
// rule, a `(P) -> Bool`, applied to the value, a P, is a Bool.
func (c *checker) checkRefinementRule(n *ast.RefinementRule) Type {
	rule := c.checkNode(n.Rule)
	ft, ok := rule.(*FuncType)
	if !ok || len(ft.Params) != 1 {
		c.checkNode(n.Value)
		return TypeBool
	}
	have := c.checkNodeExpecting(n.Value, ft.Params[0])
	if have != nil && c.unifyInto(ft.Params[0], have, nil) != nil {
		line, col := startOf(n.Value)
		c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(ft.Params[0]), formatTypeForError(have)))
	}
	return TypeBool
}

// checkRefinementParentCheck types a generated `try_from`'s check against
// the refinement's parent: the parent's own `try_from` when the parent is a
// refinement, and `Some(value)` when it is the base. Only the one that
// applies is checked, and which one is recorded for the IR builder.
func (c *checker) checkRefinementParentCheck(n *ast.RefinementParentCheck, expected Type) Type {
	refined := false
	if n.Parent != nil && n.Refined != nil {
		if parent, err := ResolveTypeExpr(n.Parent, c.reg, nil, nil); err == nil && refinementOf(parent) != nil {
			refined = true
		}
	}
	if c.fa.RefinementParents == nil {
		c.fa.RefinementParents = map[*ast.RefinementParentCheck]bool{}
	}
	c.fa.RefinementParents[n] = refined
	if refined {
		return c.checkNodeExpecting(n.Refined, expected)
	}
	return c.checkNodeExpecting(n.Plain, expected)
}

// RefinementParentChoice answers the expression a generated `try_from`'s
// parent check stands for (checkRefinementParentCheck).
func RefinementParentChoice(fa *FileAnalysis, n *ast.RefinementParentCheck) ast.Node {
	if fa != nil && fa.RefinementParents[n] {
		return n.Refined
	}
	return n.Plain
}

// refinementPipedArg checks the piped value of `8080 |> Port()`, already
// typed as pipedTy, as refinementCtorArg checks a prefix call's argument.
func (c *checker) refinementPipedArg(piped ast.Node, pipedTy Type, dt *DistinctType, label string) {
	if pipedTy == nil {
		return
	}
	if TypeAssignable(dt, pipedTy) {
		return
	}
	line, col := startOf(piped)
	if refinementLiteral(piped) {
		if base := RefinementBase(dt); !c.argMatchesParam(pipedTy, base, c.recPos(line, col), RecordingKindCallSite) {
			c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(base), formatTypeForError(pipedTy)))
			return
		}
		c.recordRefinedLiteral(piped, dt)
		return
	}
	c.report(refinementNeedsCheck(line, col, dt, pipedTy, label+"()"))
}

// validateRefinementDecls reports the refinement declarations among a
// file's nodes that the language rules out: an opaque one, whose point is
// that its base is visible; a generic one; one written inside a body; and an
// enum variant that embeds one (spec §4 *Refinement types*).
func (b *builder) validateRefinementDecls(nodes []ast.Node) {
	add := func(line, col int, msg string) {
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{Line: line, Col: col, Message: msg})
	}
	refinements := map[string]bool{}
	for _, node := range nodes {
		switch d := node.(type) {
		case *ast.TypeDef:
			if d.Rule == nil {
				continue
			}
			refinements[d.Name] = true
			if d.Opaque {
				add(d.Line, d.Col, fmt.Sprintf("a refinement cannot be opaque: `%s`'s values are values of the type it refines, which every file sees; drop `opaque`, or declare `opaque type %s ...` without `when` and check values in its constructor", d.Name, d.Name))
			}
			if len(d.TypeParams) > 0 {
				add(d.TypeParams[0].Line, d.TypeParams[0].Col, fmt.Sprintf("a refinement takes no type parameters: `%s`'s rule is checked on values of one type", d.Name))
			}
		case *ast.StructDef:
			if d.Rule == nil {
				continue
			}
			if len(d.TypeParams) > 0 {
				add(d.TypeParams[0].Line, d.TypeParams[0].Col, fmt.Sprintf("a refinement takes no type parameters: `%s`'s rule is checked on values of one type", d.Name))
			}
		}
	}
	for _, node := range nodes {
		switch d := node.(type) {
		case *ast.EnumDef:
			for _, v := range d.Variants {
				if v.Kind == "embedded" && refinements[TypeExprBaseName(v.EmbeddedTypeExpr)] {
					add(v.Line, v.Col, fmt.Sprintf("an enum cannot embed the refinement `%s`: its values are values of the type it refines; embed that type, or declare a variant over it", TypeExprBaseName(v.EmbeddedTypeExpr)))
				}
			}
		case *ast.FuncDef, *ast.TestDecl, *ast.ImplBlock:
			ast.Inspect(node, func(n ast.Node) bool {
				switch inner := n.(type) {
				case *ast.TypeDef:
					if inner.Rule != nil {
						add(inner.RuleLine, inner.RuleCol, fmt.Sprintf("a refinement is declared at the top of a file: move `%s` out of the body", inner.Name))
					}
				case *ast.StructDef:
					if inner.Rule != nil {
						add(inner.RuleLine, inner.RuleCol, fmt.Sprintf("a refinement is declared at the top of a file: move `%s` out of the body", inner.Name))
					}
				}
				return true
			})
		}
	}
}

// checkRefinementImpl reports an interface impl whose receiver is a
// distinct refinement: the refinement's value is its base's at run time,
// so it has its base's impls and none of its own (refinement.go). An
// inherent block, and the functions generated for it, are allowed.
func (c *checker) checkRefinementImpl(n *ast.ImplBlock) {
	if n.Interface == nil && !IsSynthesizedLine(n.Line) && n.Receiver != nil {
		// The generated functions' names are taken.
		if recv, err := ResolveTypeExpr(n.Receiver, c.reg, nil, nil); err == nil && IsRefinement(recv) {
			for _, item := range n.Items {
				if fd, ok := item.(*ast.FuncDef); ok && (fd.Name == "try_from" || fd.Name == "unchecked") {
					c.addError(fd.Line, fd.Col, fmt.Sprintf("`%s.%s` is generated for the refinement `%s`; name this function otherwise", recv, fd.Name, recv))
				}
			}
		}
	}
	if n.Interface == nil || n.Receiver == nil {
		return
	}
	line, col := n.Line, n.Col
	if IsSynthesizedLine(line) && n.SynthOriginLine > 0 {
		line, col = n.SynthOriginLine, n.SynthOriginCol
	}
	iface := TypeExprBaseName(n.Interface)
	if gt, generic := n.Interface.(*ast.GenericType); generic {
		// `impl Divide<Port, Int> for Int`: the impl would be selected at
		// run time by Port's base, where the base's own impl already is.
		for _, arg := range gt.Params {
			if at, err := ResolveTypeExpr(arg, c.reg, nil, nil); err == nil && refinementOf(at) != nil {
				c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
					"an impl's interface cannot take the refinement `%s` as an argument: `%s` is %s %s at run time, so the impl is %s's",
					at, at, articleFor(formatTypeForError(RefinementBase(at))), formatTypeForError(RefinementBase(at)), formatTypeForError(RefinementBase(at)))}.WithHint(fmt.Sprintf(
					"a %s passes wherever %s %s is expected, so the impl at %s serves it already",
					at, articleFor(formatTypeForError(RefinementBase(at))), formatTypeForError(RefinementBase(at)), formatTypeForError(RefinementBase(at)))))
				return
			}
		}
	}
	recv, err := ResolveTypeExpr(n.Receiver, c.reg, nil, nil)
	if err != nil {
		return
	}
	dt := refinementOf(recv)
	if dt == nil {
		return
	}
	c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
		"a refinement implements no interface of its own: `%s` is %s %s at run time and has %s's impls",
		dt.Name, articleFor(formatTypeForError(RefinementBase(dt))), formatTypeForError(RefinementBase(dt)), formatTypeForError(RefinementBase(dt)))}.WithHint(fmt.Sprintf(
		"to give `%s` behavior of its own, write functions in `impl %s`; for a type with its own `%s`, declare a distinct type, `type %s %s`, and check values in its constructor",
		dt.Name, dt.Name, iface, dt.Name, formatTypeForError(dt.Inner))))
}

// refinementLiteralsTakeExpected reports whether a call's arguments hold a
// literal that the expected type, which has a refinement in it, would check
// as that refinement: `Some(22)` expected to be a `Maybe<Port>`. The call
// seeds its type parameters from the expected type, as it does for a Byte
// literal (byteLiteralsTakeExpected).
func refinementLiteralsTakeExpected(args []ast.Node, expected Type) bool {
	if !refinementInType(expected, 0) {
		return false
	}
	for _, a := range args {
		if holdsRefinementLiteral(a) {
			return true
		}
	}
	return false
}

// holdsRefinementLiteral reports whether n is a literal a refinement
// position checks, or a collection, tuple or map literal or a call's
// argument list with one inside.
func holdsRefinementLiteral(n ast.Node) bool {
	if refinementLiteral(n) {
		return true
	}
	switch v := ungroupExpr(n).(type) {
	case *ast.NamedArg:
		return holdsRefinementLiteral(v.Value)
	case *ast.ListLit:
		for _, item := range v.Items {
			if holdsRefinementLiteral(item) {
				return true
			}
		}
	case *ast.TupleLit:
		for _, item := range v.Items {
			if holdsRefinementLiteral(item) {
				return true
			}
		}
	case *ast.MapLit:
		for _, e := range v.Entries {
			if holdsRefinementLiteral(e.Key) || holdsRefinementLiteral(e.Value) {
				return true
			}
		}
	case *ast.Call:
		for _, a := range v.Args {
			if holdsRefinementLiteral(a) {
				return true
			}
		}
	}
	return false
}

// refinementInType reports whether ty is a distinct refinement or has one
// among its element or type arguments.
func refinementInType(ty Type, depth int) bool {
	if depth > 8 || ty == nil {
		return false
	}
	switch t := resolveTypeVar(ty).(type) {
	case *DistinctType:
		if t.Rule != "" {
			return true
		}
		return anyRefinementInTypes(t.TypeArgs, depth)
	case *ListType:
		return refinementInType(t.Elem, depth+1)
	case *MapType:
		return refinementInType(t.Key, depth+1) || refinementInType(t.Val, depth+1)
	case *TupleType:
		return anyRefinementInTypes(t.Elems, depth)
	case *EnumType:
		return anyRefinementInTypes(t.TypeArgs, depth)
	case *StructType:
		return anyRefinementInTypes(t.TypeArgs, depth)
	}
	return false
}

func anyRefinementInTypes(ts []Type, depth int) bool {
	for _, t := range ts {
		if refinementInType(t, depth+1) {
			return true
		}
	}
	return false
}

// checkCollectionRefinementLit checks n, a list, vector, set or map
// literal the checker typed as ty, when ty is a distinct refinement: one
// whose every element, key and value is a literal, through nested
// collection and tuple literals, is recorded for the compile-time check of
// its rules, as a struct refinement's literal is; any other is an error
// naming `try_from`, since its rules would have to run when the program
// does. ty is the answer either way.
func (c *checker) checkCollectionRefinementLit(n ast.Node, ty Type) Type {
	dt := refinementOf(ty)
	if dt == nil {
		return ty
	}
	if line, _ := startOf(n); IsSynthesizedLine(line) {
		return ty
	}
	if bad := nonLiteralPart(n); bad != nil {
		line, col := startOf(bad)
		c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
			"%s is a refinement: a literal of it is checked when the program is compiled, so each of its elements is a literal, and `%s` is not",
			dt.Name, RenderLiteral(bad))}.WithHint(fmt.Sprintf(
			"build a %s from values known only when the program runs with `%s.try_from(...)`, a `Maybe<%s>`, which checks `%s`",
			dt.Name, dt.Name, dt.Name, dt.Rule)))
		return ty
	}
	c.recordRefinedLiteral(n, dt)
	return ty
}

// nonLiteralPart is the first part of the collection literal n that is no
// literal a refinement position checks (refinementLiteral), looking through
// nested list, vector, set, map and tuple literals, or nil when every part
// is one. A spread is never one.
func nonLiteralPart(n ast.Node) ast.Node {
	if refinementLiteral(n) {
		return nil
	}
	switch v := ungroupExpr(n).(type) {
	case *ast.ListLit:
		if len(v.Spreads) > 0 {
			return v.Spreads[0].Head
		}
		for _, item := range v.Items {
			if bad := nonLiteralPart(item); bad != nil {
				return bad
			}
		}
		return nil
	case *ast.TupleLit:
		for _, item := range v.Items {
			if bad := nonLiteralPart(item); bad != nil {
				return bad
			}
		}
		return nil
	case *ast.MapLit:
		if len(v.Spreads) > 0 {
			return v.Spreads[0].Head
		}
		for _, e := range v.Entries {
			if bad := nonLiteralPart(e.Key); bad != nil {
				return bad
			}
			if bad := nonLiteralPart(e.Value); bad != nil {
				return bad
			}
		}
		return nil
	}
	return n
}

// checkStructRefinementLit checks a literal of the struct refinement st:
// one whose every field is written as a literal is recorded for the
// compile-time check of its rule; any other is an error naming `try_from`,
// as is a patch, whose result could break the rule.
func (c *checker) checkStructRefinementLit(lit *ast.StructLit, st *StructType, patch bool) {
	if IsSynthesizedLine(lit.Line) {
		return
	}
	hint := fmt.Sprintf("build a %s from values known only when the program runs with `%s.try_from({...})`, a `Maybe<%s>`, which checks `%s`",
		st.Name, st.Name, st.Name, st.Rule)
	if patch {
		c.report(TypeError{Line: lit.Line, Col: lit.Col, Message: fmt.Sprintf(
			"a patch of the refinement %s could break its rule `%s`", st.Name, st.Rule)}.WithHint(hint))
		return
	}
	written := map[string]bool{}
	for _, f := range lit.Fields {
		written[f.Name] = true
		if !refinementLiteral(f.Value) {
			line, col := startOf(f.Value)
			c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
				"%s is a refinement: a literal of it is checked when the program is compiled, so each field is a literal, and `%s` is not", st.Name, f.Name)}.WithHint(hint))
			return
		}
	}
	for _, f := range st.Fields {
		if !written[f.Name] {
			c.report(TypeError{Line: lit.Line, Col: lit.Col, Message: fmt.Sprintf(
				"%s is a refinement: a literal of it writes every field, so its rule can be checked when the program is compiled; `%s` is missing", st.Name, f.Name)}.WithHint(hint))
			return
		}
	}
	c.recordRefinedLiteral(lit, st)
}
