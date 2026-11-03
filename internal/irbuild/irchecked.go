package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
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
// a lazy cell like a backtick literal's (irliteralcell.go), whose initializer
// calls the impl's `check` over the literal's statics and holds its
// Result<Unit, LiteralError>. vmhost's checkLiterals evaluates every site
// before the program runs, so an Err is a compile error at the place the
// LiteralError names. Nothing reads the cell at run time.

// checkedLiteralKey is a checked literal's check cell's identity.
type checkedLiteralKey struct{ t *ast.TaggedString }

// checkedLiteralCheck is the `check` of the CheckedLiteral impl whose `build`
// is t's handler, or nil.
func (g *gen) checkedLiteralCheck(t *ast.TaggedString) *ast.FuncDef {
	if g.fa == nil || g.fa.Program().ImplIndex == nil {
		return nil
	}
	pos := analysis.Pos{Line: t.Line, Col: max(t.Col-len(t.Tag), 1)}
	sym := g.srcFA().References[pos]
	if sym == nil {
		return nil
	}
	build, isFn := sym.DispatchImpl.(*ast.FuncDef)
	if !isFn {
		return nil
	}
	idx := g.fa.Program().ImplIndex
	recv, q := idx.ReceiverOf(build), idx.QualifiedReceiver[build]
	for _, fn := range idx.IfaceMethodImpls["CheckedLiteral"]["check"] {
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
		irDeclineNote("a checked literal with no check")
		return false
	}
	h, found := g.literalImplFor(t, fd)
	if !found || h.rivalled {
		irDeclineNote("a checked literal whose check does not resolve")
		return false
	}
	callee, why := g.literalCallee(h, fd)
	if callee == nil {
		if why != "" {
			irDeclineNote("a checked literal's check: " + why)
		}
		return false
	}
	k := h.result
	if !irCheckedLiteralKind(k) || len(h.params) != 1 || !irListTransportKind(h.params[0]) {
		irDeclineNote("a checked literal's check outside the retained domain: " + k.nomi())
		return false
	}
	ty := g.irTypeOf(k)
	if ty == nil {
		irDeclineNote("a checked literal's check Result with no IR type: " + k.nomi())
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
	call := ir.NewCall(at, f.NewTemp(), ir.OrdinaryCall, callee, list.Dst())
	cb.b.Append(call)
	cb.side(call.Dst(), irScalarSide{k: k})
	cb.resultCopy(t, call.Dst())
	cb.side(sh.result, irScalarSide{k: k})
	cb.b.SetTerm(ir.NewReturn(at, sh.result))
	if err := g.irLint(f); err != nil {
		irDeclineNote("a checked literal's check site that does not lint: " + err.Error())
		return false
	}
	sym := g.irTypes().Symbol(checkedLiteralKey{t}, name)
	cell := g.irModule().DeclareLazyCell(sym, ty, f)
	site := LiteralSite{
		Line: t.Line, Col: tagCol, Text: renderNode(t), Cell: cell,
		Render:  g.literalRender(t, name, k.def.preludeArgs[1]),
		Checked: true, Tag: t.Tag, Statics: statics, Slots: slots,
		Check: fd, CheckName: t.Tag + ".check",
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
	g.literalSites = append(g.literalSites, site)
	return true
}
