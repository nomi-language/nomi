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

// SubtypeSite is one literal the checker typed as a subtype, which
// vmhost checks against the subtype's rules before the program runs.
type SubtypeSite struct {
	// Path is the file's path, or an in-memory file's module path.
	Path string
	// Line and Col are the literal's position, EndLine and EndCol one past
	// its end (0 when unknown).
	Line, Col       int
	EndLine, EndCol int
	// Text is the literal as the source writes it.
	Text string
	// Type is the subtype the literal is checked as.
	Type string
	// Value is the literal's run-time value, the argument each step's
	// `try_from` is called with. Nil for a validated struct's literal,
	// whose Build makes it.
	Value any
	// Build, for a literal Value cannot hold (a validated struct's, whose
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
	// Steps are the subtypes whose rules the literal must satisfy, each
	// parent's before its own (analysis.SubtypeChecks), each with its
	// `try_from`'s linkage name.
	Steps []SubtypeSiteStep
	// Unchecked, when set, is why the builder could not make the literal's
	// check ("could not be found (std/process.ExitCode.try_from)"): a
	// compiler bug, which vmhost reports at the literal rather than let
	// the literal through.
	Unchecked string
}

// SubtypeSiteStep is one subtype a literal is checked against.
type SubtypeSiteStep struct {
	// Type is the subtype's name, Rule its rule as written.
	Type, Rule string
	// TryFrom is the linkage name of its `try_from`.
	TryFrom string
	// Check, for a generic subtype, whose `try_from` is an instance
	// per type arguments, is a function of no arguments that builds the
	// literal and answers its `try_from` at the literal's type arguments
	// (analysis.SubtypeStep.Check), in place of TryFrom.
	Check *ir.Func
	// Via are the names of the subtypes between the literal's type and
	// this one (analysis.SubtypeStep.Via), which a diagnostic names.
	Via []string
	// Key, for a generic subtype, names its declaration
	// (genericSubtypeKey), so vmhost reports a rule with effects once,
	// at the declaration, and not again at each literal.
	Key string
}

// genericSubtypeKey names the declaration of the subtype t, a type
// of it or the declaration itself: its declaring file and its name.
func genericSubtypeKey(t analysis.Type) string {
	if dt, ok := t.(*analysis.DistinctType); ok {
		return dt.Origin + "\x00" + dt.Name
	}
	return ""
}

// SubtypeRuleSite is one subtype a user file declares, whose rule
// vmhost checks is pure before the program runs, whether or not a literal
// is checked against it.
type SubtypeRuleSite struct {
	// Path, Line and Col are the `when` keyword's position.
	Path      string
	Line, Col int
	// Type is the subtype's name, Rule its rule as written.
	Type, Rule string
	// TryFrom is the linkage name of its `try_from`.
	TryFrom string
	// Check, for a generic subtype, whose `try_from` is an instance per
	// type arguments, is a function of the base's value at the first type
	// arguments its declaration was checked at that answers that
	// instance's `try_from` of it (analysis.SubtypeRuleCheck), in place
	// of TryFrom.
	Check *ir.Func
	// Key, for a generic subtype, names its declaration
	// (genericSubtypeKey), as the steps of its literals' sites do.
	Key string
	// Instances, for a generic subtype, is the prefix of the linkage
	// name of every instance of its `try_from`: each instance the program
	// lowers, at whatever type arguments, is checked as Check is.
	Instances string
	// Bounds, for a generic subtype, are the interfaces its parameters'
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

// subtypeRules are the subtypes this unit's file declares.
func (g *gen) subtypeRules() []SubtypeRuleSite {
	fa := g.srcFA()
	if fa == nil || g.stdModule != "" {
		return nil
	}
	path := fa.FilePath
	if path == "" {
		path = g.nomiPath
	}
	var out []SubtypeRuleSite
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
			rc := fa.SubtypeRuleChecks[td]
			if rc == nil {
				// The checker records no rule check where the declaration
				// has no base to check at (a parent that names no type, a
				// declaration cycle), each an error where it is declared.
				continue
			}
			site := SubtypeRuleSite{Path: path, Line: line, Col: col, Type: name, Rule: analysis.RuleText(rule), Key: genericSubtypeKey(rc.Type)}
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
				if check := g.subtypeRuleCheckBuild(td, try); site.Check == nil {
					site.Check = check
				}
			}
			out = append(out, site)
			continue
		}
		recv := kindInvalid
		if td, isType := n.(*ast.TypeDef); isStruct(n) || (isType && !td.Subtype) {
			// A struct with a rule, or a validated type, is a receiver
			// kind of its own: the kind of the checker's type for the
			// declaration.
			dl, dc := nodePos(n)
			if sym := fa.Definitions[analysis.Pos{Line: dl, Col: dc}]; sym != nil {
				recv = g.project(sym.Type)
			}
		}
		site := SubtypeRuleSite{Path: path, Line: line, Col: col, Type: name, Rule: analysis.RuleText(rule), TryFrom: tryFrom(recv)}
		if site.TryFrom == "" || plantedMissingCheck(name) {
			site.TryFrom, site.Unchecked = "", fmt.Sprintf("could not be found (%s)", declaredTryFromName(fa, n, name))
		}
		out = append(out, site)
	}
	return out
}

// genericTryFromInstances is the prefix every instance's linkage name of
// the `try_from` of name, a generic subtype this unit's file declares,
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

// subtypeSites are the sites of every literal of this unit's file the
// checker typed as a distinct subtype, in source order.
//
// Every literal the checker recorded becomes a site. One whose check the
// builder cannot make (its value does not lower, or a step's `try_from`
// has no linkage name here) keeps its site with Unchecked saying why, which
// vmhost reports at the literal as an internal error: a literal is never
// let through unchecked.
func (g *gen) subtypeSites() []SubtypeSite {
	fa := g.srcFA()
	// A std module's own literals are not checked per program: Result's
	// sites are the user units' files', and std's literals are fixed when
	// the binary is built.
	if fa == nil || len(fa.SubtypeLiterals) == 0 || g.stdModule != "" {
		return nil
	}
	// In source order, not the map's: subtypeBuild lowers the literal,
	// and a typed literal (`Duration"15m"`) declares its cell in this unit's
	// module when it is first reached, so the order the builds run in is the
	// order of the module's cells and functions.
	lits := slices.SortedFunc(maps.Keys(fa.SubtypeLiterals), compareNodePos)
	var out []SubtypeSite
	for _, n := range lits {
		rec := fa.SubtypeLiterals[n]
		line, col := nodePos(n)
		site := SubtypeSite{Path: fa.FilePath, Line: line, Col: col, Text: renderNode(n), Type: analysis.Elaborate(rec.Type).String(), Wraps: analysis.IsCheckedDistinct(rec.Type)}
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
			if site.Build = g.subtypeBuild(lit, &ast.StructLit{Fields: lit.Fields, Line: lit.Line, Col: lit.Col}); site.Build == nil {
				unchecked("could not be built: the record of its fields does not lower")
			}
		case *ast.ListLit, *ast.MapLit, *ast.RangeLit:
			site.Compound = true
			if site.Build = g.subtypeBuild(lit, lit); site.Build == nil {
				unchecked("could not be built: the literal does not lower")
			}
		default:
			if v, ok := subtypeLiteralValue(n); ok {
				site.Value = v
				break
			}
			// A typed literal (`Duration"15m"`), a Decimal, a codepoint, and
			// any of these grouped or negated: built by the code their
			// literal lowers to.
			if site.Build = g.subtypeBuild(n, n); site.Build == nil {
				unchecked("could not be built: the literal does not lower")
			}
		}
		for _, step := range rec.Steps {
			var via []string
			for _, v := range step.Via {
				via = append(via, analysis.Elaborate(v).String())
			}
			ss := SubtypeSiteStep{
				Type: analysis.Elaborate(step.Type).String(),
				Rule: analysis.SubtypeRule(step.Type),
				Via:  via,
			}
			if step.Check != nil {
				ss.Key = genericSubtypeKey(step.Type)
				if ss.Check = g.subtypeCheckBuild(n, step.Check); ss.Check == nil {
					unchecked(fmt.Sprintf("could not be built (%s at the literal's type arguments does not lower)", tryFromName(step.Type)))
				}
				site.Steps = append(site.Steps, ss)
				continue
			}
			recv := kindInvalid
			if analysis.IsValidatedStruct(step.Type) || analysis.IsCheckedDistinct(step.Type) {
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
			// SubtypeChecks answers the literal's own type last, so a
			// recorded literal with no step is a checker bug.
			unchecked("could not be found (the checker recorded no rule to check it against)")
		}
		out = append(out, site)
	}
	return out
}

// declaredTryFromName is tryFromName for the subtype decl, which fa
// declares as name: the checker's type of the declaration.
func declaredTryFromName(fa *analysis.FileAnalysis, decl ast.Node, name string) string {
	line, col := nodePos(decl)
	if sym := fa.Definitions[analysis.Pos{Line: line, Col: col}]; sym != nil && sym.Type != nil {
		return tryFromName(sym.Type)
	}
	return name + ".try_from"
}

// tryFromName names the `try_from` of the subtype t for a diagnostic:
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

// subtypeBuild is a function of no arguments that answers value, the
// value a subtype literal lit checks its rules on, which the first
// step's `try_from` is called with; nil when it does not lower. For a
// validated struct's literal value is the record of its fields, each a
// literal the checker typed; for a collection subtype's it is the
// literal itself, of the base's kind.
func (g *gen) subtypeBuild(lit, value ast.Node) *ir.Func {
	at := g.irNodePos(lit)
	line, col := nodePos(lit)
	name := fmt.Sprintf("subtype literal at %d:%d", line, col)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, params: map[string]ir.Temp{}}
	sh.entry = f.NewBlock(at, "entry")
	cb := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry}
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

// subtypeCheckBuild is a function of no arguments that builds the
// literal lit and answers check, a generic subtype's `try_from` called
// on it (analysis.SubtypeStep.Check), whose argument names the literal
// as analysis.SubtypeLiteralName; nil when it does not lower.
func (g *gen) subtypeCheckBuild(lit, check ast.Node) *ir.Func {
	at := g.irNodePos(lit)
	line, col := nodePos(lit)
	name := fmt.Sprintf("subtype check at %d:%d", line, col)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, params: map[string]ir.Temp{}}
	sh.entry = f.NewBlock(at, "entry")
	cb := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry}
	cb.openWithScope(true)
	val, k, _, ok := cb.lower(lit)
	if !ok || g.irTypeOf(k) == nil {
		return nil
	}
	cb.declareKey(subtypeLiteralKey(check), val, k)
	res, rk, _, ok := cb.lower(check)
	if !ok || g.irTypeOf(rk) == nil {
		return nil
	}
	cb.returnKind = rk
	cb.b.SetTerm(ir.NewReturn(at, res))
	return f
}

// subtypeRuleCheckBuild is a function of one argument, the base's value
// at one try's type arguments, that answers the generic subtype td's
// `try_from` of it (analysis.SubtypeRuleTry); nil when it does not
// lower. It is never run: vmhost reads what it can reach.
func (g *gen) subtypeRuleCheckBuild(td *ast.TypeDef, rc analysis.SubtypeRuleTry) *ir.Func {
	at := g.irNodePos(td)
	name := fmt.Sprintf("subtype rule of %s", td.Name)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, params: map[string]ir.Temp{}}
	sh.entry = f.NewBlock(at, "entry")
	cb := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry}
	cb.openWithScope(true)
	k := g.project(rc.Arg)
	if g.irTypeOf(k) == nil {
		return nil
	}
	cb.declareKey(subtypeLiteralKey(rc.Check), g.irAddParam(f, ir.NewSymbol("value"), k), k)
	res, rk, _, ok := cb.lower(rc.Check)
	if !ok || g.irTypeOf(rk) == nil {
		return nil
	}
	cb.returnKind = rk
	cb.b.SetTerm(ir.NewReturn(at, res))
	return f
}

// subtypeLiteralKey is the key of the value a subtype's check reads
// (analysis.SubtypeLiteralName): the checker's symbol for it stands at
// the check call's argument, the read itself.
func subtypeLiteralKey(check ast.Node) analysis.Pos {
	call, isCall := check.(*ast.Call)
	if !isCall || len(call.Args) != 1 {
		panic("irbuild: a subtype's check that is not a call of one argument")
	}
	line, col := nodePos(call.Args[0])
	return analysis.Pos{Line: line, Col: col}
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

// subtypeLiteralValue is the run-time value of a literal the checker
// typed as a subtype: an Int, a Float, a String or a Bool, negated
// where the source negates it.
func subtypeLiteralValue(n ast.Node) (any, bool) {
	neg := false
	if g, isGroup := n.(*ast.GroupedExpr); isGroup {
		return subtypeLiteralValue(g.Expr)
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

// subtypeCall lowers a distinct subtype's call form, `Port(8080)` or
// `net.Port(8080)`, which the checker admits only on a literal or a value
// already of the type: the argument itself. handled is false for any other
// call.
func (bl *irScalarBuilder) subtypeCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	if len(t.Args) != 1 {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch fn := t.Func.(type) {
	case *ast.TypeIdent:
		if !bl.g.subtypeAt(fn.Line, fn.Col) {
			return ir.NoTemp, kindInvalid, false, false, false
		}
	case *ast.FieldAccess:
		// `float.NonNegativeFloat(x)`.
		_, isModule := fn.Object.(*ast.Ident)
		if !isModule || fn.Field == nil || !bl.g.subtypeAt(fn.Field.Line, fn.Field.Col) {
			return ir.NoTemp, kindInvalid, false, false, false
		}
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	v, k, mobile, ok = bl.lower(t.Args[0])
	return v, k, mobile, ok, true
}

// uncheckedSubtype lowers the body of a subtype's `unchecked`, by the
// type the checker gave it: a subtype's value is the base value itself, a
// validated type is built from it as its constructor builds it, at the
// instance the checker solved for a generic one, and a validated struct is
// built from the record of its fields as its call form builds it. The
// constructors are tried as a written call tries them, and the body
// declines here when neither builds the value.
func (bl *irScalarBuilder) uncheckedSubtype(t *ast.UncheckedSubtype) (ir.Temp, kind, bool, bool) {
	ty := analysis.ResolveClosed(bl.g.checkedExprType(t))
	if dt, ok := ty.(*analysis.DistinctType); ok && dt.Subtype {
		return bl.lower(t.Value)
	}
	if d := bl.g.nominalDef(ty); d != nil && d.isDistinct {
		if dst, k, mobile, ok := bl.distinctMakeOf(t, []ast.Node{t.Value}, bl.g.distinctInstanceAt(d, t)); ok {
			return dst, k, mobile, true
		}
	} else if dst, k, mobile, ok := bl.structCallFormOf(t, t.Value, d); ok {
		return dst, k, mobile, true
	}
	bl.g.dc.note("a subtype's `unchecked` whose type's constructor does not build its value: " + ty.String())
	return ir.NoTemp, kindInvalid, false, false
}

// irSubtypeRuleHost is the VM intrinsic a generated `try_from` calls its
// rule through: it applies the rule to the value and answers False when the
// rule traps (vm's subtypeRuleHost).
const irSubtypeRuleHost = "vm.when_rule"

// subtypeRule lowers a generated `try_from`'s call of its rule,
// ast.SubtypeRule, as a call of irSubtypeRuleHost on the rule's
// function value and the value.
func (bl *irScalarBuilder) subtypeRule(t *ast.SubtypeRule) (ir.Temp, kind, bool, bool) {
	rule, _, _, ok := bl.lower(t.Rule)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	v, _, _, ok := bl.lower(t.Value)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	at := bl.g.irNodePos(t)
	call := ir.NewHostCall(at, bl.f.NewTemp(), ir.OrdinaryCall, bl.g.irCalleeSym(irSubtypeRuleHost, irSubtypeRuleHost), rule, v)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: kindBool})
	return call.Dst(), kindBool, false, true
}

// subtypeAt reports whether the checker resolved the type name written
// at line:col to a distinct subtype.
func (g *gen) subtypeAt(line, col int) bool {
	sym := g.checkedRef(line, col)
	if sym == nil {
		return false
	}
	dt, isDistinct := sym.Type.(*analysis.DistinctType)
	return isDistinct && dt.Subtype
}

// isDistinctSubtypeDecl reports whether n declares a distinct
// subtype, which the builder gives no def.
func isDistinctSubtypeDecl(n ast.Node) bool {
	td, ok := n.(*ast.TypeDef)
	return ok && td.Subtype && td.InnerTypeExpr != nil
}
