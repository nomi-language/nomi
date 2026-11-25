package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// CheckForeignDeriveBounds is CheckDeriveBounds for the impls fa's file
// derives for a type another file declares (a REPL input's for an earlier
// input's type). The parts are read in the declaring file's analysis,
// which fileOf finds by its path, since their types are named there, and
// reported as a derive's in that file are.
func CheckForeignDeriveBounds(fa *FileAnalysis, nodes []ast.Node, fileOf declFiles) []TypeError {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	var errs []TypeError
	seen := map[Pos]bool{}
	for _, n := range nodes {
		blk, ok := n.(*ast.ImplBlock)
		if !ok {
			continue
		}
		iface := derivedInterface(blk)
		if iface == nil || !derivesStdInterface(fa, iface) {
			continue
		}
		line, col := typeExprPos(iface, 0, 0)
		derivePos := Pos{File: fa.FilePath, Line: line, Col: col}
		if seen[derivePos] {
			continue
		}
		seen[derivePos] = true
		sym := fa.ModuleScope.Lookup(TypeExprBaseName(blk.Receiver))
		if sym != nil && sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym == nil || sym.Node == nil || sym.SourceFile == "" || sym.SourceFile == fa.FilePath {
			continue
		}
		declFA := fileOf(sym.SourceFile, sym)
		if declFA == nil {
			continue
		}
		if name, ok := typeDeclName(sym.Node); ok {
			errs = append(errs, checkJsonDeriveParts(declFA, fileOf, sym.Node, name, TypeExprBaseName(iface))...)
		}
	}
	return errs
}

// derivedInterface answers the interface as written of blk when blk is an
// impl a derive lowered to, and nil for any other block. Such a block sits
// in the synthesized band, and its header names the interface at the
// derive's own position (lowerTopLevelDerive): `ToJson`, or `json.ToJson`
// through a module. The universal Debug's header sits in the band too.
func derivedInterface(blk *ast.ImplBlock) ast.TypeExpr {
	if blk == nil || blk.Interface == nil || blk.SynthOriginLine <= 0 || !IsSynthesizedLine(blk.Line) {
		return nil
	}
	if IsSynthesizedLine(blk.Interface.LineNum()) || !deriveSupported[TypeExprBaseName(blk.Interface)] {
		return nil
	}
	return blk.Interface
}

// derivesByShape reports whether blk is a derive's block that writes no
// function: `derive Equatable`'s, `derive Hashable`'s, `derive Debug`'s and
// `derive Display`'s (derivedShapeBlock). A use of the interface at its type is answered by
// the type's shape (interfaceUse), a hole of it renders so
// (displayRenderOf), and the block is complete without the functions the
// interface requires.
func derivesByShape(blk *ast.ImplBlock) bool {
	return len(blk.Items) == 0 && derivedInterface(blk) != nil
}

// importStmts are the imports among nodes, each entry of a block import
// (`import { ... }`) one of them.
func importStmts(nodes []ast.Node) []*ast.ImportStmt {
	var out []*ast.ImportStmt
	for _, n := range nodes {
		switch imp := n.(type) {
		case *ast.ImportStmt:
			out = append(out, imp)
		case *ast.ImportBlock:
			out = append(out, imp.Entries...)
		}
	}
	return out
}
