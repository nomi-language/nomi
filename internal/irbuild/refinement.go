package irbuild

import (
	"fmt"
	"math/big"
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// RefinementSite is one literal the checker typed as a refinement, which
// vmhost checks against the refinement's rules before the program runs.
type RefinementSite struct {
	// Path is the file's path, or an in-memory file's module path.
	Path string
	// Line and Col are the literal's position, EndLine and EndCol one past
	// its end (0 when unknown).
	Line, Col       int
	EndLine, EndCol int
	// Text is the literal as the source writes it.
	Text string
	// Type is the refinement the literal is checked as.
	Type string
	// Value is the literal's run-time value, the argument each step's
	// `try_from` is called with. Nil for a struct refinement's literal,
	// whose Build makes it.
	Value any
	// Build, for a struct refinement's literal, answers the record of its
	// fields its `try_from` is called with.
	Build *ir.Func
	// Steps are the refinements of the literal's chain from the base's
	// child up, each with its `try_from`'s linkage name.
	Steps []RefinementSiteStep
}

// RefinementSiteStep is one refinement a literal is checked against.
type RefinementSiteStep struct {
	// Type is the refinement's name, Rule its rule as written.
	Type, Rule string
	// TryFrom is the linkage name of its `try_from`.
	TryFrom string
}

// RefinementRuleSite is one refinement a user file declares, whose rule
// vmhost checks is pure before the program runs, whether or not a literal
// is checked against it.
type RefinementRuleSite struct {
	// Path, Line and Col are the `when` keyword's position.
	Path      string
	Line, Col int
	// Type is the refinement's name, Rule its rule as written.
	Type, Rule string
	// TryFrom is the linkage name of its `try_from`.
	TryFrom string
}

// refinementRules are the refinements this unit's file declares.
func (g *gen) refinementRules() []RefinementRuleSite {
	fa := g.srcFA()
	if fa == nil || g.stdModule != "" {
		return nil
	}
	path := fa.FilePath
	if path == "" {
		path = g.nomiPath
	}
	var out []RefinementRuleSite
	for _, n := range g.nodes {
		var name string
		var line, col int
		var rule ast.Node
		switch d := n.(type) {
		case *ast.TypeDef:
			name, rule, line, col = d.Name, d.Rule, d.RuleLine, d.RuleCol
		case *ast.StructDef:
			name, rule, line, col = d.Name, d.Rule, d.RuleLine, d.RuleCol
		}
		if rule == nil {
			continue
		}
		for _, m := range g.nodes {
			ib, isBlock := m.(*ast.ImplBlock)
			if !isBlock || ib.Interface != nil || ib.SynthOriginLine == 0 || analysis.TypeExprBaseName(ib.Receiver) != name {
				continue
			}
			for _, item := range ib.Items {
				if fd, isFn := item.(*ast.FuncDef); isFn && fd.Name == "try_from" {
					recv := kindInvalid
					if _, isStruct := n.(*ast.StructDef); isStruct {
						recv = g.typeOf(&ast.SimpleType{Name: name})
					}
					if link := g.implFuncLink(fd, recv); link != "" {
						out = append(out, RefinementRuleSite{Path: path, Line: line, Col: col, Type: name, Rule: analysis.RuleText(rule), TryFrom: link})
					}
				}
			}
		}
	}
	return out
}

// refinementSites are the sites of every literal of this unit's file the
// checker typed as a distinct refinement, in source order.
func (g *gen) refinementSites() []RefinementSite {
	fa := g.srcFA()
	if fa == nil || len(fa.RefinedLiterals) == 0 || g.stdModule != "" {
		return nil
	}
	var out []RefinementSite
	for n, rec := range fa.RefinedLiterals {
		var value any
		var build *ir.Func
		switch lit := n.(type) {
		case *ast.StructLit:
			if build = g.refinementBuild(lit, &ast.StructLit{Fields: lit.Fields, Line: lit.Line, Col: lit.Col}); build == nil {
				continue
			}
		case *ast.ListLit, *ast.MapLit:
			if build = g.refinementBuild(lit, lit); build == nil {
				continue
			}
		default:
			v, ok := refinementLiteralValue(n)
			if !ok {
				continue
			}
			value = v
		}
		line, col := nodePos(n)
		text := renderNode(n)
		site := RefinementSite{Path: fa.FilePath, Line: line, Col: col, Text: text, Type: rec.Type.String(), Value: value, Build: build}
		if site.Path == "" {
			site.Path = g.nomiPath
		}
		if hs, isSpan := n.(ast.HasSpan); isSpan && !hs.GetSpan().IsZero() {
			site.EndLine, site.EndCol = hs.GetSpan().EndLine, hs.GetSpan().EndCol
		}
		for _, step := range rec.Steps {
			recv := kindInvalid
			if analysis.IsRefinement(step.Type) && !analysis.IsDistinctRefinement(step.Type) {
				recv = g.project(step.Type)
			}
			link := g.implFuncLink(step.TryFrom, recv)
			if link == "" {
				site.Steps = nil
				break
			}
			site.Steps = append(site.Steps, RefinementSiteStep{
				Type:    step.Type.String(),
				Rule:    analysis.RefinementRule(step.Type),
				TryFrom: link,
			})
		}
		if len(site.Steps) > 0 {
			out = append(out, site)
		}
	}
	slices.SortFunc(out, func(a, b RefinementSite) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Col - b.Col
	})
	return out
}

// refinementBuild is a function of no arguments that answers value, the
// value a refinement literal lit checks its rules on, which the first
// step's `try_from` is called with; nil when it does not lower. For a
// struct refinement's literal value is the record of its fields, each a
// literal the checker typed; for a collection refinement's it is the
// literal itself, of the base's kind.
func (g *gen) refinementBuild(lit, value ast.Node) *ir.Func {
	at := g.irNodePos(lit)
	line, col := nodePos(lit)
	name := fmt.Sprintf("refinement literal at %d:%d", line, col)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	cb := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	cb.openWithScope(true)
	var val ir.Temp
	var k kind
	ok := false
	if rec, isRecord := value.(*ast.StructLit); isRecord {
		val, k, _, ok = cb.recordMakeOf(rec, kindInvalid)
	} else {
		val, k, _, ok = cb.lower(value)
	}
	if !ok || g.irTypeOf(k) == nil {
		return nil
	}
	cb.returnKind = k
	cb.b.SetTerm(ir.NewReturn(at, val))
	return f
}

// implFuncLink is the linkage name of fd, a function of an inherent block
// over recv, kindInvalid for an owner block (ownerimpl.go), which has no
// receiver: its block's name, the receiver and its own name. "" when no
// module of the program or std declares it.
func (g *gen) implFuncLink(fd ast.Node, recv kind) string {
	fn, ok := fd.(*ast.FuncDef)
	if !ok {
		return ""
	}
	find := func(names map[ast.Node]declName) string {
		for n, d := range names {
			ib, isBlock := n.(*ast.ImplBlock)
			if !isBlock || !slices.Contains(ib.Items, ast.Node(fn)) {
				continue
			}
			return d.String() + "[" + portableArgsKey([]kind{recv}) + "]." + fn.Name
		}
		return ""
	}
	if links := g.programLinks(); links != nil {
		if l := find(links); l != "" {
			return l
		}
	}
	// The shared std lowering names a function of an impl block by its
	// declaration alone (stdFuncLink).
	if d, ok := stdLinkNames()[fd]; ok {
		return d.String()
	}
	return ""
}

// refinementLiteralValue is the run-time value of a literal the checker
// typed as a refinement: an Int, a Float, a String or a Bool, negated
// where the source negates it.
func refinementLiteralValue(n ast.Node) (any, bool) {
	neg := false
	if g, isGroup := n.(*ast.GroupedExpr); isGroup {
		return refinementLiteralValue(g.Expr)
	}
	if u, isUnary := n.(*ast.Unary); isUnary && u.Op == "-" {
		neg, n = true, u.Right
	}
	switch lit := n.(type) {
	case *ast.IntLit:
		if lit.Big != nil {
			z := new(big.Int).Set(lit.Big)
			if neg {
				z.Neg(z)
			}
			return rt.IntFromBig(z), true
		}
		if neg {
			return -lit.Value, true
		}
		return lit.Value, true
	case *ast.FloatLit:
		if neg {
			return -lit.Value, true
		}
		return lit.Value, true
	case *ast.StringLit:
		return lit.Value, !neg
	case *ast.TypeIdent:
		return lit.Name == "True", !neg && (lit.Name == "True" || lit.Name == "False")
	}
	return nil, false
}

// namesRefinement reports whether a pattern's type name, written `Port` or
// `net.Port` (expr then holds the qualified spelling), is a distinct
// refinement.
func (g *gen) namesRefinement(name string, expr ast.TypeExpr) bool {
	if qt, ok := expr.(*ast.QualifiedType); ok {
		_, isRefinement := g.qualifiedRefinementKind(qt)
		return isRefinement
	}
	_, isRefinement := g.refinementDecl(name)
	return isRefinement
}

// refinementCall lowers a distinct refinement's call form, `Port(8080)` or
// `net.Port(8080)`, which the checker admits only on a literal or a value
// already of the type: the argument itself. handled is false for any other
// call.
func (bl *irScalarBuilder) refinementCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	if len(t.Args) != 1 {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch fn := t.Func.(type) {
	case *ast.TypeIdent:
		if _, isRefinement := bl.g.refinementDecl(fn.Name); !isRefinement {
			return ir.NoTemp, kindInvalid, false, false, false
		}
	case *ast.FieldAccess:
		mod, isModule := fn.Object.(*ast.Ident)
		if !isModule || fn.Field == nil {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		qt := &ast.QualifiedType{Module: mod.Name, Member: &ast.SimpleType{Name: fn.Field.Name}}
		if _, isRefinement := bl.g.qualifiedRefinementKind(qt); !isRefinement {
			return ir.NoTemp, kindInvalid, false, false, false
		}
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	v, k, mobile, ok = bl.lower(t.Args[0])
	return v, k, mobile, ok, true
}

// uncheckedRefinement lowers the body of a refinement's `unchecked`: a
// distinct refinement's value is the base value itself, and a struct
// refinement is built from the record of its fields as its call form
// builds it.
func (bl *irScalarBuilder) uncheckedRefinement(t *ast.UncheckedRefinement) (ir.Temp, kind, bool, bool) {
	if _, isDistinct := bl.g.refinementDecl(t.Refinement); isDistinct {
		return bl.lower(t.Value)
	}
	// The struct's call form, `Span({low: 1, high: 2})`, which builds it
	// from the record.
	return bl.lower(&ast.Call{Func: &ast.TypeIdent{Name: t.Refinement, Line: t.Line, Col: t.Col}, Args: []ast.Node{t.Value}, Line: t.Line, Col: t.Col})
}

// irRefinementRuleHost is the VM intrinsic a generated `try_from` calls its
// rule through: it applies the rule to the value and answers False when the
// rule traps (vm's refinementRuleHost).
const irRefinementRuleHost = "vm.refinement_rule"

// refinementRule lowers a generated `try_from`'s call of its rule,
// ast.RefinementRule, as a call of irRefinementRuleHost on the rule's
// function value and the value.
func (bl *irScalarBuilder) refinementRule(t *ast.RefinementRule) (ir.Temp, kind, bool, bool) {
	rule, _, _, ok := bl.lower(t.Rule)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	v, _, _, ok := bl.lower(t.Value)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	at := bl.g.irNodePos(t)
	call := ir.NewHostCall(at, bl.f.NewTemp(), ir.OrdinaryCall, bl.g.irCalleeSym(irRefinementRuleHost, irRefinementRuleHost), rule, v)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: kindBool})
	return call.Dst(), kindBool, false, true
}

// refinementDecl answers the TypeDef that declares the distinct refinement
// name resolves to in this file's scope, when it does.
func (g *gen) refinementDecl(name string) (*analysis.Symbol, bool) {
	sym := resolvedTypeSymbol(g.fa, name)
	if sym == nil {
		return nil, false
	}
	if dt, ok := sym.Type.(*analysis.DistinctType); ok && dt.Rule != "" {
		return sym, true
	}
	return nil, false
}

// refinementKind is the kind an annotation naming a distinct refinement
// stands for, its base's, reporting whether the name was one.
func (g *gen) refinementKind(name string) (kind, bool) {
	sym, ok := g.refinementDecl(name)
	if !ok {
		return kindInvalid, false
	}
	return g.project(sym.Type), true
}

// qualifiedRefinementKind is refinementKind for `float.NonNegativeFloat`.
func (g *gen) qualifiedRefinementKind(t *ast.QualifiedType) (kind, bool) {
	qualifier, name, ok := qualifiedTypeParts(t)
	if !ok {
		return kindInvalid, false
	}
	sym := resolvedScopeSymbol(g.moduleScope(qualifier), name)
	if sym == nil {
		return kindInvalid, false
	}
	if dt, isDistinct := sym.Type.(*analysis.DistinctType); isDistinct && dt.Rule != "" {
		return g.project(dt), true
	}
	return kindInvalid, false
}

// refinementBaseKind is the kind of what the refinement name's values are
// built from (ast.RefinementBaseType): a distinct refinement's base, or the
// record of a struct refinement's fields.
func (g *gen) refinementBaseKind(t *ast.RefinementBaseType) kind {
	if k, ok := g.refinementKind(t.Refinement); ok {
		return k
	}
	sym := resolvedTypeSymbol(g.fa, t.Refinement)
	if sym == nil {
		return kindInvalid
	}
	st, ok := sym.Type.(*analysis.StructType)
	if !ok || st.Rule == "" {
		return kindInvalid
	}
	return g.project(&analysis.AnonStructType{Fields: st.Fields})
}

// isDistinctRefinementDecl reports whether n declares a distinct
// refinement, which the builder gives no def.
func isDistinctRefinementDecl(n ast.Node) bool {
	td, ok := n.(*ast.TypeDef)
	return ok && td.Rule != nil && td.InnerTypeExpr != nil
}

// stdRefinementKind is the kind a std signature's type names when it names
// a std distinct refinement (`NonZeroInt`) or a refinement's base
// (ast.RefinementBaseType): the base's, which in std is a scalar. Resolved
// through the module's own analysis, as the names it declares are.
func stdRefinementKind(te ast.TypeExpr, anchors stdAnchors) (kind, bool) {
	var name string
	switch t := te.(type) {
	case *ast.RefinementBaseType:
		name = t.Refinement
	case *ast.SimpleType:
		name = t.Name
	default:
		return kindInvalid, false
	}
	if anchors.fa == nil {
		return kindInvalid, false
	}
	sym := resolvedTypeSymbol(anchors.fa, name)
	if sym == nil {
		return kindInvalid, false
	}
	dt, ok := sym.Type.(*analysis.DistinctType)
	if !ok || dt.Rule == "" {
		return kindInvalid, false
	}
	return scalarKindOfType(analysis.RefinementBase(dt)), true
}

// scalarKindOfType is the kind of a scalar analysis type, kindInvalid for
// any other.
func scalarKindOfType(t analysis.Type) kind {
	switch t {
	case analysis.TypeInt:
		return kindInt
	case analysis.TypeFloat:
		return kindFloat
	case analysis.TypeString:
		return kindString
	}
	if et, ok := t.(*analysis.EnumType); ok && et.Name == "Bool" && len(et.TypeArgs) == 0 {
		return kindBool
	}
	return kindInvalid
}
