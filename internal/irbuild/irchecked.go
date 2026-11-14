package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A CHECKED LITERAL (spec §18 *Checked literals*).
//
// A typed literal whose tag implements std/literals' CheckedLiteral is typed
// as its `build`'s result in every quote style, and the checker records it in
// FileAnalysis.CheckedLiterals. At run time it lowers as any typed literal
// does, a call to `build` over its fragments (taggedLiteral); the checker
// pointed the tag's reference at `build`, so nothing there differs.
//
// What differs is the check. Each checked literal a body reaches gets a site,
// a lazy cell like a literal without slots has (irliteralcell.go), whose initializer
// calls the impl's `check` over the literal's statics and holds its
// Result<Unit, LiteralError>. vmhost's checkLiterals evaluates every site
// before the program runs, so an Err is a compile error at the place the
// LiteralError names. Nothing reads the cell at run time.

// checkedLiteralKey is a checked literal's check cell's identity.
type checkedLiteralKey struct{ t *ast.TaggedString }

// checkedLiteralCheck is the function the compiler runs to check t: the
// `check_type` of the CheckedLiteral impl whose `build` is t's handler when
// the impl writes one, and its `check` otherwise, which is what the
// interface's default `check_type` calls. Nil when there is neither.
func (g *gen) checkedLiteralCheck(t *ast.TaggedString) *ast.FuncDef {
	if fd := g.checkedLiteralImplFunc(t, "check_type"); fd != nil {
		return fd
	}
	return g.checkedLiteralImplFunc(t, "check")
}

// checkedLiteralImplFunc is the function named method of the CheckedLiteral
// impl whose `build` is t's handler, or nil.
func (g *gen) checkedLiteralImplFunc(t *ast.TaggedString, method string) *ast.FuncDef {
	if g.fa == nil || g.fa.Program().ImplIndex == nil {
		return nil
	}
	build := g.literalDispatch(t)
	if build == nil {
		return nil
	}
	idx := g.fa.Program().ImplIndex
	recv, q := idx.ReceiverOf(build), idx.QualifiedReceiver[build]
	for _, fn := range idx.IfaceMethodImpls["CheckedLiteral"][method] {
		if fn == nil || idx.ReceiverOf(fn) != recv || idx.QualifiedReceiver[fn] != q {
			continue
		}
		if twin, ok := g.twins[fn].(*ast.FuncDef); ok {
			return twin
		}
		return fn
	}
	return nil
}

// checkedLiteralSite declares t's check site the first time a body reaches t,
// and reports whether it lowered. A literal whose check does not lower is not
// checked, so the body that reaches it declines rather than running an
// unchecked literal.
func (g *gen) checkedLiteralSite(t *ast.TaggedString) bool {
	if done, seen := g.checkSites[t]; seen {
		return done
	}
	if g.checkSites == nil {
		g.checkSites = map[*ast.TaggedString]bool{}
	}
	ok := g.declareCheckSite(t)
	g.checkSites[t] = ok
	return ok
}

func (g *gen) declareCheckSite(t *ast.TaggedString) bool {
	fd := g.checkedLiteralCheck(t)
	if fd == nil {
		g.dc.note("a checked literal with no check")
		return false
	}
	h, found := g.literalImplFor(t, fd)
	if !found || h.rivalled {
		g.dc.note("a checked literal whose check does not resolve")
		return false
	}
	callee, why := g.literalCallee(h, fd)
	if callee == nil {
		if why != "" {
			g.dc.note("a checked literal's check: " + why)
		}
		return false
	}
	k := h.result
	typed := fd.Name == "check_type"
	arity := 1
	if typed {
		arity = 2
	}
	// CheckedLiteral.check's and check_type's signatures, `(List<String>)`
	// and `(List<String>, List<String>)` to `Result<Unit, LiteralError>`,
	// which the impl's conformance holds.
	if !irCheckedLiteralKind(k) || len(h.params) != arity || !irRetainedListKind(h.params[0]) || typed && !irRetainedListKind(h.params[1]) {
		panic("irbuild: a CheckedLiteral check whose signature is not the interface's: " + k.nomi())
	}
	ty := g.irTypeOf(k)
	if ty == nil {
		g.dc.note("a checked literal's check Result with no IR type: " + k.nomi())
		return false
	}
	var statics []string
	var slots []SlotPos
	for _, part := range fragmentParts(t.Parts) {
		switch p := part.(type) {
		case ast.StringText:
			statics = append(statics, p.Value)
		case ast.StringExpr:
			line, col := nodePos(p.Expr)
			slots = append(slots, SlotPos{Line: line, Col: col})
		}
	}
	// The tag and position name the site; the literal's text may span lines,
	// and a function name is one line wherever it is printed.
	tagCol := max(t.Col-len(t.Tag), 1)
	name := fmt.Sprintf("check %s literal at %d:%d", t.Tag, t.Line, tagCol)
	at := g.irNodePos(t)
	f := ir.NewFunc(at, name)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	sh.result = f.NewTemp()
	sh.slot = ir.NewSlot(at, sh.result, ty)
	sh.entry.Append(sh.slot)
	sh.prologue = 1
	cb := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry, returnKind: k,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}, literalCell: t}
	cb.openWithScope(true)
	items := make([]ir.Temp, len(statics))
	for i, s := range statics {
		c := ir.NewString(at, f.NewTemp(), s)
		cb.b.Append(c)
		cb.side(c.Dst(), irScalarSide{k: kindString})
		items[i] = c.Dst()
	}
	list := ir.NewMakeList(at, f.NewTemp(), items, ir.NoTemp)
	cb.b.Append(list)
	cb.side(list.Dst(), irScalarSide{k: h.params[0]})
	operands := []ir.Temp{list.Dst()}
	if typed {
		// check_type's second operand: the literal's type arguments, each
		// as Nomi spells it.
		var spellings []ir.Temp
		for _, a := range g.checkedLiteralArgs(t) {
			c := ir.NewString(at, f.NewTemp(), a)
			cb.b.Append(c)
			cb.side(c.Dst(), irScalarSide{k: kindString})
			spellings = append(spellings, c.Dst())
		}
		args := ir.NewMakeList(at, f.NewTemp(), spellings, ir.NoTemp)
		cb.b.Append(args)
		cb.side(args.Dst(), irScalarSide{k: h.params[1]})
		operands = append(operands, args.Dst())
	}
	call := ir.NewCall(at, f.NewTemp(), ir.OrdinaryCall, callee, operands...)
	cb.b.Append(call)
	cb.side(call.Dst(), irScalarSide{k: k})
	cb.resultCopy(t, call.Dst())
	cb.side(sh.result, irScalarSide{k: k})
	cb.b.SetTerm(ir.NewReturn(at, sh.result))
	g.irRequireLint(f, "a checked literal's check site")
	sym := g.irTypes().Symbol(checkedLiteralKey{t}, name)
	cell := g.irModule().DeclareLazyCell(sym, ty, f)
	site := LiteralSite{
		Line: t.Line, Col: tagCol, Text: renderNode(t), Cell: cell,
		Render:  g.literalRender(t, name, k.def.preludeArgs[1]),
		Checked: true, Tag: t.Tag, Statics: statics, Slots: slots,
		Check: fd, CheckName: t.Tag + "." + fd.Name,
	}
	if h.std == nil && h.unit < 0 {
		// The impl is in this file, so its check can be reported there.
		site.CheckLine, site.CheckCol = fd.Line, fd.Col
	}
	if g.fa != nil {
		site.Path = g.fa.FilePath
	}
	if site.Path == "" {
		site.Path = g.nomiPath
	}
	if !t.Span.IsZero() {
		site.EndLine, site.EndCol = t.Span.EndLine, t.Span.EndCol
	}
	if g.srcFA().TargetLiteralBuilds[t] != nil {
		// Written with no tag: the quote is at the literal's position, and
		// a diagnostic shows the literal as it was written.
		site.Tag, site.Type, site.Text = "", t.Tag, site.Text[len(t.Tag):]
	}
	g.literalSites = append(g.literalSites, site)
	return true
}

// checkedLiteralArgs is the spelling of each type argument of t's type, as
// the checker solved it and this instance binds it.
func (g *gen) checkedLiteralArgs(t *ast.TaggedString) []string {
	ty := g.srcFA().ExprTypes[t]
	if ty == nil {
		return nil
	}
	k := g.project(ty)
	if k.tag != tagNamed || k.def == nil {
		return nil
	}
	d := k.def
	if len(d.genericArgs) == 0 && d.instOrigin != nil {
		// A mirror of another file's instance carries its arguments on
		// the instance it mirrors.
		d = d.instOrigin
	}
	out := make([]string, len(d.genericArgs))
	for i, a := range d.genericArgs {
		out[i] = kindSpelling(a)
	}
	return out
}

// kindSpelling is k as Nomi writes the type: a named type with its
// arguments, `Maybe<Int>`, and a structural one as it is spelled.
func kindSpelling(k kind) string {
	if k.tag != tagNamed || k.def == nil {
		return k.nomi()
	}
	d := k.def
	var args []kind
	switch {
	case len(d.genericArgs) > 0:
		args = d.genericArgs
	case len(d.preludeArgs) > 0:
		args = d.preludeArgs
	case len(d.genStructArgs) > 0:
		args = d.genStructArgs
	case len(d.genHostArgs) > 0:
		args = d.genHostArgs
	}
	name := d.nomi
	if i := strings.IndexByte(name, '<'); i >= 0 {
		return name
	}
	if len(args) == 0 {
		return name
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = kindSpelling(a)
	}
	return name + "<" + strings.Join(parts, ", ") + ">"
}
