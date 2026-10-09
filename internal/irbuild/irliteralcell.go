package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A BACKTICK TYPED LITERAL WHOSE HANDLER CAN FAIL.
//
// `Regex`\d+`` is checked at compile time: its body never interpolates, so
// its handler's input is fixed text, and the checker types the literal as the
// handler's success type (analysis.RawLiteralType). The handler still runs at
// run time, since its value (a compiled Go regexp) cannot be written into an
// IR image, and it runs once per literal site.
//
// Each site is a lazy cell, as a `once` is, whose initializer is the
// handler's call and holds its Result. The site reads the cell and takes the
// Ok payload. vmhost evaluates every cell this lowering declared before the
// program runs (vmhost's checkLiterals): an Err there is the compile error at
// the literal, so the run's own read always finds Ok. The Err edge is a
// NoMatch no execution reaches, since the handler is the same pure call on
// the same text.

// LiteralSite is one backtick typed literal checked at compile time: where it
// is, its source text, and the lazy cell whose initializer calls its handler.
type LiteralSite struct {
	// Path is the declaring file's path, "" for an in-memory program.
	Path string
	// Line and Col are the tag's position; EndLine and EndCol are one past
	// the closing backtick, or 0 when the parser recorded no extent.
	Line, Col       int
	EndLine, EndCol int
	// Text is the literal as `nomi fmt` renders it.
	Text string
	// Cell is the literal's lazy cell. Its initializer answers the handler's
	// Result.
	Cell *ir.Cell
	// Render takes the Err payload and answers its Display rendering (its
	// Debug rendering when the error type has no Display), as a failed
	// `fn main`'s error is rendered; nil for a String error, which is its
	// own message, and when neither rendering lowers.
	Render *ir.Func
}

// literalRenderKey is the identity of a literal's Render function.
type literalRenderKey struct{ t *ast.TaggedString }

// literalCellKey is a literal cell's identity in the unit's ir.Table.
type literalCellKey struct{ t *ast.TaggedString }

// irCheckedLiteralKind reports whether k, a typed literal handler's result,
// is a Result: the handler of a backtick literal checked at compile time.
func irCheckedLiteralKind(k kind) bool {
	d := k.def
	return d != nil && d.preludeOf != nil && d.preludeOf.spec == preludeSpecFor("std/results", "Result") &&
		len(d.preludeArgs) == 2 && irRetainedEnumKind(d)
}

// checkedLiteral lowers a backtick literal whose handler returns k, a Result:
// the Ok payload of the site's cell.
func (bl *irScalarBuilder) checkedLiteral(t *ast.TaggedString, k kind) (ir.Temp, kind, bool, bool) {
	no := func(why string) (ir.Temp, kind, bool, bool) {
		irDeclineNote("a backtick typed literal: " + why)
		return ir.NoTemp, kindInvalid, false, false
	}
	g := bl.g
	d := k.def
	okV := d.variant("Ok")
	if okV == nil || len(okV.payloads) != 1 {
		return no("a Result without an Ok payload")
	}
	pk := okV.payloads[0].k
	ty := g.irTypeOf(pk)
	if ty == nil {
		return no("an Ok payload with no IR type: " + pk.nomi())
	}
	sym, ok := g.literalCell(t, k)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	pos := g.irNodePos(t)
	ref := ir.NewRefOnce(pos, bl.f.NewTemp(), sym)
	bl.b.Append(ref)
	bl.side(ref.Dst(), irScalarSide{k: k})
	slot := bl.f.NewTemp()
	bl.b.Append(ir.NewSlot(pos, slot, ty))
	bl.side(slot, irScalarSide{k: pk})
	match := bl.variantTest(t, ref.Dst(), d, okV)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	checked := bl.f.NewBlock(pos, "literal checked")
	failed := bl.f.NewBlock(pos, "literal failed")
	cont := bl.f.NewBlock(pos, "literal value")
	bl.b.SetTerm(ir.NewBranch(pos, match.Dst(), checked.ID(), failed.ID()))
	bl.b = checked
	payload := bl.variantPayload(t, ref.Dst(), d, okV)
	bl.b.Append(ir.NewCopy(pos, slot, payload))
	bl.b.SetTerm(ir.NewJump(pos, cont.ID()))
	failed.Append(ir.NewNoMatch(pos))
	failed.SetTerm(ir.NewJump(pos, cont.ID()))
	bl.b = cont
	return slot, pk, false, true
}

// literalCell is the symbol of t's lazy cell, holding k, declared in this
// unit's module the first time a body reaches t. A site in a generic body
// instantiated twice reads one cell: the literal names no type parameter.
func (g *gen) literalCell(t *ast.TaggedString, k kind) (*ir.Symbol, bool) {
	if sym := g.literalCells[t]; sym != nil {
		return sym, true
	}
	ty := g.irTypeOf(k)
	if ty == nil {
		irDeclineNote("a backtick typed literal whose Result has no IR type: " + k.nomi())
		return nil, false
	}
	text := renderNode(t)
	tagCol := max(t.Col-len(t.Tag), 1)
	name := fmt.Sprintf("literal %s at %d:%d", text, t.Line, tagCol)
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
	val, got, _, ok := cb.taggedLiteral(t)
	if !ok {
		return nil, false
	}
	if got != k {
		irDeclineNote("a backtick typed literal whose handler call is not its Result: " + got.nomi())
		return nil, false
	}
	cb.resultCopy(t, val)
	cb.side(sh.result, irScalarSide{k: got})
	cb.b.SetTerm(ir.NewReturn(at, sh.result))
	if err := ir.Lint(f); err != nil {
		irDeclineNote("a backtick typed literal cell that does not lint: " + err.Error())
		return nil, false
	}
	sym := g.irTypes().Symbol(literalCellKey{t}, name)
	cell := g.irModule().DeclareLazyCell(sym, ty, f)
	if g.literalCells == nil {
		g.literalCells = map[*ast.TaggedString]*ir.Symbol{}
	}
	g.literalCells[t] = sym
	site := LiteralSite{Line: t.Line, Col: tagCol, Text: text, Cell: cell, Render: g.literalRender(t, name, k.def.preludeArgs[1])}
	if g.fa != nil {
		site.Path = g.fa.FilePath
	}
	if !t.Span.IsZero() {
		// The span starts at the opening backtick; the diagnostic starts at
		// the tag.
		site.EndLine, site.EndCol = t.Span.EndLine, t.Span.EndCol
	}
	g.literalSites = append(g.literalSites, site)
	return sym, true
}

// literalRender builds the Render function of the literal t, whose cell is
// named name, over its error kind errK, or answers nil. It is built while the
// enclosing body is being lowered, so a rendering that does not lower is no
// decline of that body: the census is set aside meanwhile.
func (g *gen) literalRender(t *ast.TaggedString, name string, errK kind) *ir.Func {
	if errK == kindString || !irCallOperandKind(errK) {
		// A String is its own message (vmhost's literalMessage).
		return nil
	}
	var fn *ir.Func
	irDeclineAside(func() {
		rname := name + " failure"
		sym := g.irCalleeSym(literalRenderKey{t}, rname)
		f, ok := g.irErrorRenderFunc(t, rname, sym, errK, t, false)
		if !ok {
			f, ok = g.irErrorRenderFunc(t, rname, sym, errK, t, true)
		}
		if !ok {
			return
		}
		g.irModule().AddFunc(f)
		if err := ir.LintModuleAdded(g.irModule()); err != nil {
			panic("irbuild: literal failure: " + err.Error())
		}
		fn = f
	})
	return fn
}
