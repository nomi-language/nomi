package analysis

import "github.com/nomi-language/nomi/internal/ast"

// WrittenFor answers where the source declares what blk was written for,
// when blk is an impl block the front end wrote (a derive's, a subtype's
// `try_from` and `unchecked`, an opaque type's Debug): its own position is
// in the synthesized band (IsSynthesizedLine) and names no line of the
// file. A block a `derive` wrote answers the interface that derive names
// (derived is true), since the block's header holds the interface as the
// derive wrote it. Any other answers the name of the type whose declaration
// the block comes with (ast.ImplBlock.SynthOriginLine). span is the length
// of the name there.
//
// The builder records the answer on each function of the block
// (Symbol.DefinitionLine), which is what a reader holding a symbol reads. A
// reader holding the block, as go-to-implementation holds a derived block
// that writes no function, calls this.
func WrittenFor(blk *ast.ImplBlock) (at Pos, span int, derived, ok bool) {
	if blk == nil || blk.SynthOriginLine <= 0 || !IsSynthesizedLine(blk.Line) {
		return Pos{}, 0, false, false
	}
	if blk.Interface != nil {
		if p, name, named := typeNamePos(blk.Interface); named && p.Line > 0 && !IsSynthesizedLine(p.Line) {
			return p, len(name), true, true
		}
	}
	return Pos{Line: blk.SynthOriginLine, Col: blk.SynthOriginCol}, len(TypeExprBaseName(blk.Receiver)), false, true
}

// recordWrittenFor gives sym, the symbol of an item of the impl block blk,
// the source position WrittenFor answers for blk as the position its
// definition is at. A block the source writes leaves sym as it is.
func recordWrittenFor(sym *Symbol, blk *ast.ImplBlock) {
	at, span, _, ok := WrittenFor(blk)
	if !ok {
		return
	}
	sym.DefinitionLine, sym.DefinitionCol, sym.DefinitionSpan = at.Line, at.Col, span
}
