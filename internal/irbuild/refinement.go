package irbuild

import (
	"cmp"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/std"
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
	// Build, for a literal Value cannot hold (a struct refinement's, whose
	// record of fields its `try_from` is called with, a collection, a
	// typed literal or a Decimal), answers the value.
	Build *ir.Func
	// Compound is set for a struct or collection literal, which a
	// diagnostic names as "this ... literal" rather than by its text.
	Compound bool
	// Wraps is set when Type is a `type` over a subtype or with a rule of
	// its own (analysis.IsCheckedDistinct): the first of a step's Via is
	// the type it wraps, which a diagnostic says it wraps rather than is a
	// subtype of.
	Wraps bool
	// Steps are the refinements whose rules the literal must satisfy, each
	// parent's before its own (analysis.RefinementChecks), each with its
	// `try_from`'s linkage name.
	Steps []RefinementSiteStep
	// Unchecked, when set, is why the builder could not make the literal's
	// check ("could not be found (std/process.ExitCode.try_from)"): a
	// compiler bug, which vmhost reports at the literal rather than let
	// the literal through.
	Unchecked string
}

// RefinementSiteStep is one refinement a literal is checked against.
type RefinementSiteStep struct {
	// Type is the refinement's name, Rule its rule as written.
	Type, Rule string
	// TryFrom is the linkage name of its `try_from`.
	TryFrom string
	// Check, for a generic refinement, whose `try_from` is an instance
	// per type arguments, is a function of no arguments that builds the
	// literal and answers its `try_from` at the literal's type arguments
	// (analysis.RefinementStep.Check), in place of TryFrom.
	Check *ir.Func
	// Via are the names of the refinements between the literal's type and
	// this one (analysis.RefinementStep.Via), which a diagnostic names.
	Via []string
	// Key, for a generic refinement, names its declaration
	// (genericRefinementKey), so vmhost reports a rule with effects once,
	// at the declaration, and not again at each literal.
	Key string
}

// genericRefinementKey names the declaration of the refinement t, a type
// of it or the declaration itself: its declaring file and its name.
func genericRefinementKey(t analysis.Type) string {
	if dt, ok := t.(*analysis.DistinctType); ok {
		return dt.Origin + "\x00" + dt.Name
	}
	return ""
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
	// Check, for a generic refinement, whose `try_from` is an instance per
	// type arguments, is a function of the base's value at the first type
	// arguments its declaration was checked at that answers that
	// instance's `try_from` of it (analysis.RefinementRuleCheck), in place
	// of TryFrom.
	Check *ir.Func
	// Key, for a generic refinement, names its declaration
	// (genericRefinementKey), as the steps of its literals' sites do.
	Key string
	// Instances, for a generic refinement, is the prefix of the linkage
	// name of every instance of its `try_from`: each instance the program
	// lowers, at whatever type arguments, is checked as Check is.
	Instances string
	// Bounds, for a generic refinement, are the interfaces its parameters'
	// bounds name, sorted: every function of every impl of them the
	// program has is checked as Check is.
	Bounds []string
	// Unchecked, when set, is why the builder could not name the rule's
	// `try_from`: a compiler bug, which vmhost reports at the `when`.
	Unchecked string
}

// isStruct reports whether n declares a struct.
func isStruct(n ast.Node) bool {
	_, ok := n.(*ast.StructDef)
	return ok
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
		tryFrom := func(recv kind) string {
			for _, m := range g.nodes {
				ib, isBlock := m.(*ast.ImplBlock)
				if !isBlock || (ib.Interface != nil && analysis.TypeExprBaseName(ib.Interface) != "TryFrom") || ib.SynthOriginLine == 0 || analysis.TypeExprBaseName(ib.Receiver) != name {
					continue
				}
				for _, item := range ib.Items {
					if fd, isFn := item.(*ast.FuncDef); isFn && fd.Name == "try_from" {
						return g.implFuncLink(fd, recv)
					}
				}
			}
			return ""
		}
		if td, isType := n.(*ast.TypeDef); isType && len(td.TypeParams) > 0 {
			rc := fa.RefinementRuleChecks[td]
			if rc == nil {
				// The checker records no rule check where the declaration
				// has no base to check at (a parent that names no type, a
				// declaration cycle), each an error where it is declared.
				continue
			}
			site := RefinementRuleSite{Path: path, Line: line, Col: col, Type: name, Rule: analysis.RuleText(rule), Key: genericRefinementKey(rc.Type)}
			if prefix := g.genericTryFromInstances(name, td.Subtype); prefix != "" && !plantedMissingCheck(name) {
				site.Instances = prefix
			} else {
				site.Unchecked = fmt.Sprintf("could not be found (%s)", tryFromName(rc.Type))
			}
			for _, tp := range rc.Type.TypeParamDefs {
				for _, b := range tp.Bounds {
					if !slices.Contains(site.Bounds, b.Name) {
						site.Bounds = append(site.Bounds, b.Name)
					}
				}
			}
			slices.Sort(site.Bounds)
			for _, try := range rc.Tries {
				// Each lowers its instance of the rule's `try_from` into
				// this unit's module, which Instances finds: one the
				// machine cannot run is passed over there.
				if check := g.refinementRuleCheckBuild(td, try); site.Check == nil {
					site.Check = check
				}
			}
			out = append(out, site)
			continue
		}
		recv := kindInvalid
		if td, isType := n.(*ast.TypeDef); isStruct(n) || (isType && !td.Subtype) {
			// A struct with a rule, or a validated type, is a receiver
			// kind of its own.
			recv = g.typeOf(&ast.SimpleType{Name: name})
		}
		site := RefinementRuleSite{Path: path, Line: line, Col: col, Type: name, Rule: analysis.RuleText(rule), TryFrom: tryFrom(recv)}
		if site.TryFrom == "" || plantedMissingCheck(name) {
			site.TryFrom, site.Unchecked = "", fmt.Sprintf("could not be found (%s)", g.declaredTryFromName(fa, name))
		}
		out = append(out, site)
	}
	return out
}

// genericTryFromInstances is the prefix every instance's linkage name of
// the `try_from` of name, a generic refinement this unit's file declares,
// starts with, or "" when the builder names no such function. A subtype's
// `try_from` is in an inherent block, whose receiver key is the same at
// every instance (`main.Small<T>[0].try_from<`). A validated type's is in
// `impl TryFrom<base(Short)> for Short<T>`, whose receiver and interface
// arguments are each instance's own
// (`main.impl TryFrom<base(Short)> for Short<T>[6@main.Short<2>|9(:2)].try_from<{T=2}>`),
// so its prefix ends at the block's declaration: the block declares
// `try_from` alone.
func (g *gen) genericTryFromInstances(name string, subtype bool) string {
	links := g.programLinks()
	for _, m := range g.nodes {
		ib, isBlock := m.(*ast.ImplBlock)
		if !isBlock || (ib.Interface != nil && analysis.TypeExprBaseName(ib.Interface) != "TryFrom") || ib.SynthOriginLine == 0 || analysis.TypeExprBaseName(ib.Receiver) != name {
			continue
		}
		for _, item := range ib.Items {
			fd, isFn := item.(*ast.FuncDef)
			if !isFn || fd.Name != "try_from" {
				continue
			}
			if subtype {
				if link := g.implFuncLink(fd, kindInvalid); link != "" {
					return link + "<"
				}
				return ""
			}
			if d, named := links[ib]; named && len(ib.Items) == 1 {
				return d.String() + "["
			}
			return ""
		}
	}
	return ""
}

// refinementSites are the sites of every literal of this unit's file the
// checker typed as a distinct refinement, in source order.
//
// Every literal the checker recorded becomes a site. One whose check the
// builder cannot make (its value does not lower, or a step's `try_from`
// has no linkage name here) keeps its site with Unchecked saying why, which
// vmhost reports at the literal as an internal error: a literal is never
// let through unchecked.
func (g *gen) refinementSites() []RefinementSite {
	fa := g.srcFA()
	// A std module's own literals are not checked per program: Result's
	// sites are the user units' files', and std's literals are fixed when
	// the binary is built.
	if fa == nil || len(fa.RefinedLiterals) == 0 || g.stdModule != "" {
		return nil
	}
	// In source order, not the map's: refinementBuild lowers the literal,
	// and a typed literal (`Duration"15m"`) declares its cell in this unit's
	// module when it is first reached, so the order the builds run in is the
	// order of the module's cells and functions.
	lits := slices.SortedFunc(maps.Keys(fa.RefinedLiterals), compareNodePos)
	var out []RefinementSite
	for _, n := range lits {
		rec := fa.RefinedLiterals[n]
		line, col := nodePos(n)
		site := RefinementSite{Path: fa.FilePath, Line: line, Col: col, Text: renderNode(n), Type: analysis.Elaborate(rec.Type).String(), Wraps: analysis.IsCheckedDistinct(rec.Type)}
		if site.Path == "" {
			site.Path = g.nomiPath
		}
		if hs, isSpan := n.(ast.HasSpan); isSpan && !hs.GetSpan().IsZero() {
			site.EndLine, site.EndCol = hs.GetSpan().EndLine, hs.GetSpan().EndCol
		}
		unchecked := func(why string) {
			if site.Unchecked == "" {
				site.Unchecked = why
			}
		}
		switch lit := n.(type) {
		case *ast.StructLit:
			site.Compound = true
			if site.Build = g.refinementBuild(lit, &ast.StructLit{Fields: lit.Fields, Line: lit.Line, Col: lit.Col}); site.Build == nil {
				unchecked("could not be built: the record of its fields does not lower")
			}
		case *ast.ListLit, *ast.MapLit, *ast.RangeLit:
			site.Compound = true
			if site.Build = g.refinementBuild(lit, lit); site.Build == nil {
				unchecked("could not be built: the literal does not lower")
			}
		default:
			if v, ok := refinementLiteralValue(n); ok {
				site.Value = v
				break
			}
			// A typed literal (`Duration"15m"`), a Decimal, a codepoint, and
			// any of these grouped or negated: built by the code their
			// literal lowers to.
			if site.Build = g.refinementBuild(n, n); site.Build == nil {
				unchecked("could not be built: the literal does not lower")
			}
		}
		for _, step := range rec.Steps {
			var via []string
			for _, v := range step.Via {
				via = append(via, analysis.Elaborate(v).String())
			}
			ss := RefinementSiteStep{
				Type: analysis.Elaborate(step.Type).String(),
				Rule: analysis.RefinementRule(step.Type),
				Via:  via,
			}
			if step.Check != nil {
				ss.Key = genericRefinementKey(step.Type)
				if ss.Check = g.refinementCheckBuild(n, step.Check); ss.Check == nil {
					unchecked(fmt.Sprintf("could not be built (%s at the literal's type arguments does not lower)", tryFromName(step.Type)))
				}
				site.Steps = append(site.Steps, ss)
				continue
			}
			recv := kindInvalid
			if analysis.IsStructRefinement(step.Type) || analysis.IsCheckedDistinct(step.Type) {
				recv = g.project(step.Type)
			}
			// implFuncLink answers "" for a nil TryFrom too: a step the
			// checker found no generated `try_from` for.
			ss.TryFrom = g.implFuncLink(step.TryFrom, recv)
			if link, built := g.programBuiltStdTryFrom(step.TryFrom); built {
				ss.TryFrom = link
			}
			if ss.TryFrom == "" || plantedMissingCheck(ss.Type) {
				ss.TryFrom = ""
				unchecked(fmt.Sprintf("could not be found (%s)", tryFromName(step.Type)))
			}
			site.Steps = append(site.Steps, ss)
		}
		if len(site.Steps) == 0 {
			// RefinementChecks answers the literal's own type last, so a
			// recorded literal with no step is a checker bug.
			unchecked("could not be found (the checker recorded no rule to check it against)")
		}
		out = append(out, site)
	}
	return out
}

// declaredTryFromName is tryFromName for the refinement fa declares as
// name.
func (g *gen) declaredTryFromName(fa *analysis.FileAnalysis, name string) string {
	if sym := resolvedTypeSymbol(fa, name); sym != nil && sym.Type != nil {
		return tryFromName(sym.Type)
	}
	return name + ".try_from"
}

// tryFromName names the `try_from` of the refinement t for a diagnostic:
// the module that declares it, then the type's name.
func tryFromName(t analysis.Type) string {
	name := analysis.Elaborate(t).String()
	if dt, ok := t.(*analysis.DistinctType); ok && strings.HasPrefix(dt.Origin, "std/") {
		return strings.TrimSuffix(dt.Origin, ".nomi") + "." + name + ".try_from"
	}
	return name + ".try_from"
}

// compareNodePos orders two nodes of one file by where they start, then by
// where they end.
func compareNodePos(a, b ast.Node) int {
	al, ac := nodePos(a)
	bl, bc := nodePos(b)
	if c := cmp.Compare(al, bl); c != 0 {
		return c
	}
	if c := cmp.Compare(ac, bc); c != 0 {
		return c
	}
	var ae, be ast.Span
	if hs, ok := a.(ast.HasSpan); ok {
		ae = hs.GetSpan()
	}
	if hs, ok := b.(ast.HasSpan); ok {
		be = hs.GetSpan()
	}
	if c := cmp.Compare(ae.EndLine, be.EndLine); c != 0 {
		return c
	}
	return cmp.Compare(ae.EndCol, be.EndCol)
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

// refinementCheckBuild is a function of no arguments that builds the
// literal lit and answers check, a generic refinement's `try_from` called
// on it (analysis.RefinementStep.Check), whose argument names the literal
// as analysis.RefinementLiteralName; nil when it does not lower.
func (g *gen) refinementCheckBuild(lit, check ast.Node) *ir.Func {
	at := g.irNodePos(lit)
	line, col := nodePos(lit)
	name := fmt.Sprintf("refinement check at %d:%d", line, col)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	cb := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	cb.openWithScope(true)
	val, k, _, ok := cb.lower(lit)
	if !ok || g.irTypeOf(k) == nil {
		return nil
	}
	cb.bound[analysis.RefinementLiteralName] = val
	cb.boundK[analysis.RefinementLiteralName] = k
	res, rk, _, ok := cb.lower(check)
	if !ok || g.irTypeOf(rk) == nil {
		return nil
	}
	cb.returnKind = rk
	cb.b.SetTerm(ir.NewReturn(at, res))
	return f
}

// refinementRuleCheckBuild is a function of one argument, the base's value
// at one try's type arguments, that answers the generic refinement td's
// `try_from` of it (analysis.RefinementRuleTry); nil when it does not
// lower. It is never run: vmhost reads what it can reach.
func (g *gen) refinementRuleCheckBuild(td *ast.TypeDef, rc analysis.RefinementRuleTry) *ir.Func {
	at := g.irNodePos(td)
	name := fmt.Sprintf("refinement rule of %s", td.Name)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	cb := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	cb.openWithScope(true)
	k := g.project(rc.Arg)
	if g.irTypeOf(k) == nil {
		return nil
	}
	cb.bound[analysis.RefinementLiteralName] = g.irAddParam(f, ir.NewSymbol("value"), k)
	cb.boundK[analysis.RefinementLiteralName] = k
	res, rk, _, ok := cb.lower(rc.Check)
	if !ok || g.irTypeOf(rk) == nil {
		return nil
	}
	cb.returnKind = rk
	cb.b.SetTerm(ir.NewReturn(at, res))
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
	blockLink := func(d declName) string {
		return d.String() + "[" + portableArgsKey([]kind{recv}) + "]." + fn.Name
	}
	if links := g.programLinks(); links != nil {
		for n, d := range links {
			if ib, isBlock := n.(*ast.ImplBlock); isBlock && slices.Contains(ib.Items, ast.Node(fn)) {
				gt, explicit := ifaceHeaderArgs(ib.Interface)
				if !explicit {
					return blockLink(d)
				}
				// `impl TryFrom<base(Email)> for Email`, a validated type's:
				// the interface's written arguments are the block's
				// identity among the receiver's impls of it, as
				// implItemLink spells them.
				args := make([]kind, len(gt))
				for i, te := range gt {
					// kindInvalid: lookup — an argument this builder cannot name declined the block, which then has no function to name.
					if args[i] = g.typeOf(te); args[i] == kindInvalid {
						return ""
					}
				}
				return d.String() + "[" + portableArgsKey([]kind{recv}) + "|" + portableArgsKey(args) + "]." + fn.Name
			}
		}
	}
	d, ok := stdLinkNames()[fd]
	if !ok {
		return ""
	}
	module := strings.TrimPrefix(d.module, "std/")
	if !programStdModules[module] {
		// The shared std lowering names a function of an impl block by
		// its declaration alone (stdFuncLink).
		return d.String()
	}
	// A listed module is lowered as a program is (programstdlink.go), so
	// it names the function by its block, as implItemLink does. The
	// program's own names skip it (programLinkNames); std's hold its
	// block.
	names := stdLinkNames()
	for _, n := range std.Shared().Nodes[module] {
		if ib, isBlock := n.(*ast.ImplBlock); isBlock && slices.Contains(ib.Items, ast.Node(fn)) {
			if bd, named := names[ib]; named {
				return blockLink(bd)
			}
		}
	}
	return ""
}

// programBuiltStdTryFrom is the linkage name of the instance a program
// builds of fd, a std `try_from` the shared std lowering could not retain
// because its body reaches a generic std function (a std subtype whose
// parent is a generic subtype: FiniteRange over BoundedRange<Float> calls
// `BoundedRange.try_from`), built here as a call site would build it
// (stdInstCallAt): an instance with no arguments. built is false for a
// `try_from` the shared lowering retains, and link is "" when the instance
// does not build, which the caller reports at the literal.
func (g *gen) programBuiltStdTryFrom(fd ast.Node) (link string, built bool) {
	fn, isFunc := fd.(*ast.FuncDef)
	if !isFunc || g.stdInsts == nil {
		return "", false
	}
	d, inStd := stdLinkNames()[fn]
	if !inStd || programStdModules[strings.TrimPrefix(d.module, "std/")] {
		return "", false
	}
	s := g.stdInsts
	for _, f := range s.std.byKey {
		if f.decl != fn {
			continue
		}
		if !s.stdUnretainedMonoBody(f) {
			return "", false
		}
		inst, _ := s.instantiate(f, nil, nil, g)
		if inst == nil {
			return "", true
		}
		return inst.sym.Link(), true
	}
	return "", false
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
	if dt, ok := sym.Type.(*analysis.DistinctType); ok && dt.Subtype {
		return sym, true
	}
	return nil, false
}

// refinementTypeSym is the symbol name resolves to as a type in this
// file's scope, or nil.
func (g *gen) refinementTypeSym(name string) *analysis.Symbol {
	return resolvedTypeSymbol(g.fa, name)
}

// genericRefinementKind is the kind of a generic distinct refinement sym
// names at the type arguments params, `NonEmptyList<Int>`: its base's at
// those arguments, List<Int>'s. isRefinement is false when sym is no
// generic refinement.
func (g *gen) genericRefinementKind(sym *analysis.Symbol, params []ast.TypeExpr) (kind, bool) {
	if sym == nil {
		return kindInvalid, false
	}
	dt, ok := sym.Type.(*analysis.DistinctType)
	if !ok || !dt.Subtype || len(dt.TypeParamDefs) == 0 {
		return kindInvalid, false
	}
	if len(params) != len(dt.TypeParamDefs) {
		return kindInvalid, true
	}
	args := make([]kind, len(params))
	for i, p := range params {
		args[i] = g.typeOf(p)
		// kindInvalid: propagates — typeOf reports the argument it refuses, and the refinement's kind passes that kindlessness on.
		if args[i] == kindInvalid {
			return kindInvalid, true
		}
	}
	return g.refinementBaseAt(dt, args), true
}

// refinementBaseAt is the kind of the generic refinement dt's base with its
// type parameters standing for args.
func (g *gen) refinementBaseAt(dt *analysis.DistinctType, args []kind) kind {
	frame := make(map[string]kind, len(args))
	for i, def := range dt.TypeParamDefs {
		frame[def.Name_] = args[i]
	}
	g.genericSubst = append(g.genericSubst, frame)
	defer func() { g.genericSubst = g.genericSubst[:len(g.genericSubst)-1] }()
	return g.project(analysis.RefinementBase(dt))
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
	if dt, isDistinct := sym.Type.(*analysis.DistinctType); isDistinct && dt.Subtype {
		return g.project(dt), true
	}
	return kindInvalid, false
}

// refinementBaseKind is the kind of what the refinement name's values are
// built from (ast.RefinementBaseType): a distinct refinement's base, or the
// record of a struct refinement's fields.
func (g *gen) refinementBaseKind(t *ast.RefinementBaseType) kind {
	if len(t.Params) > 0 {
		if k, ok := g.genericRefinementKind(g.refinementTypeSym(t.Refinement), t.Params); ok {
			return k
		}
	}
	if k, ok := g.refinementKind(t.Refinement); ok {
		return k
	}
	sym := resolvedTypeSymbol(g.fa, t.Refinement)
	if sym == nil {
		return kindInvalid
	}
	if dt, ok := sym.Type.(*analysis.DistinctType); ok && !dt.Subtype && dt.Inner != nil {
		// A validated type, or a type over a subtype: its `try_from` takes
		// the base of what it wraps, a generic one's at the parameters
		// its impl names.
		if len(dt.TypeParamDefs) == 0 {
			return g.project(analysis.RefinementBase(analysis.DistinctInner(dt)))
		}
		if len(t.Params) != len(dt.TypeParamDefs) {
			return kindInvalid
		}
		frame := make(map[string]kind, len(t.Params))
		for i, def := range dt.TypeParamDefs {
			// kindInvalid: propagates — typeOf reports the argument it refuses.
			if frame[def.Name_] = g.typeOf(t.Params[i]); frame[def.Name_] == kindInvalid {
				return kindInvalid
			}
		}
		g.genericSubst = append(g.genericSubst, frame)
		defer func() { g.genericSubst = g.genericSubst[:len(g.genericSubst)-1] }()
		return g.project(analysis.RefinementBase(analysis.DistinctInner(dt)))
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
	return ok && td.Subtype && td.InnerTypeExpr != nil
}

// stdRefinementKind is the kind a std signature's type names when it names
// a std distinct refinement (`NonZeroInt`) or a refinement's base
// (ast.RefinementBaseType): the base's, a scalar or a type std declares. Resolved
// through the module's own analysis, as the names it declares are.
func stdRefinementKind(te ast.TypeExpr, anchors stdAnchors) (kind, bool) {
	var name string
	var generic *ast.GenericType
	switch t := te.(type) {
	case *ast.RefinementBaseType:
		name = t.Refinement
	case *ast.SimpleType:
		name = t.Name
	case *ast.GenericType:
		name, generic = t.Name, t
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
	if !ok || !dt.Subtype {
		return kindInvalid, false
	}
	if generic != nil {
		return stdGenericRefinementKind(dt, generic, anchors), true
	}
	base := analysis.RefinementBase(dt)
	// kindInvalid: lookup — a base with no scalar kind is not a failure here; the lines below look it up as a named std type.
	if k := scalarKindOfType(base); k != kindInvalid {
		return k, true
	}
	// A base std declares, a `host type` (`Decimal`) or an opaque newtype
	// (`Duration`): the kind its name has in a std signature.
	if named, ok := base.(*analysis.DistinctType); ok && !named.Subtype && len(named.TypeArgs) == 0 {
		return stdTypeKind(&ast.SimpleType{Name: named.Name}, anchors), true
	}
	// Any other base (a collection or a range at fixed arguments,
	// `subtype Ratios List<Int>`, `subtype FiniteRange Range<Float>`; a
	// struct or an enum std declares, `subtype FieldBits Bits`) is the kind
	// of the subtype's first parent as its declaration writes it, which
	// names only types std declares.
	if td, ok := sym.Node.(*ast.TypeDef); ok && td.Subtype && td.InnerTypeExpr != nil {
		return stdTypeKind(td.InnerTypeExpr, anchors), true
	}
	return kindInvalid, true
}

// stdGenericRefinementKind is the kind of a generic std subtype at the
// type arguments a std signature writes, `BoundedRange<Int>` in
// `Generator.int`: its base's kind at those arguments, `Range<Int>`. The
// base is found through the subtype's parents with each type parameter
// replaced by a marker, so the base's arguments say which written argument
// each one is; a base whose arguments are anything but the subtype's own
// parameters declines.
func stdGenericRefinementKind(dt *analysis.DistinctType, gt *ast.GenericType, anchors stdAnchors) kind {
	if len(dt.TypeParamDefs) == 0 || len(dt.TypeParamDefs) != len(gt.Params) {
		return kindInvalid
	}
	inst := *dt
	inst.TypeArgs = make([]analysis.Type, len(dt.TypeParamDefs))
	markers := make(map[*analysis.TypeParam_]int, len(dt.TypeParamDefs))
	for i, def := range dt.TypeParamDefs {
		m := &analysis.TypeParam_{Name_: def.Name_}
		inst.TypeArgs[i], markers[m] = m, i
	}
	var baseName string
	var baseArgs []analysis.Type
	switch b := analysis.RefinementBase(&inst).(type) {
	case *analysis.StructType:
		baseName, baseArgs = b.Name, b.TypeArgs
	case *analysis.EnumType:
		baseName, baseArgs = b.Name, b.TypeArgs
	case *analysis.DistinctType:
		baseName, baseArgs = b.Name, b.TypeArgs
	default:
		return kindInvalid
	}
	if len(baseArgs) == 0 {
		return kindInvalid
	}
	params := make([]ast.TypeExpr, len(baseArgs))
	for i, a := range baseArgs {
		m, isMarker := a.(*analysis.TypeParam_)
		at, ok := markers[m]
		if !isMarker || !ok {
			return kindInvalid
		}
		params[i] = gt.Params[at]
	}
	return stdTypeKind(&ast.GenericType{Name: baseName, Params: params, Line: gt.Line, Col: gt.Col}, anchors)
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
	case analysis.TypeDecimal:
		return decimalKind()
	}
	if et, ok := t.(*analysis.EnumType); ok && et.Name == "Bool" && len(et.TypeArgs) == 0 {
		return kindBool
	}
	return kindInvalid
}
