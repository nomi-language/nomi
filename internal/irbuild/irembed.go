package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// embeddedFiles lowers the files an `embed` declaration holds (spec §18
// *Embedded files*): a `List<(String, Int, Int)>` of each file's path,
// handle and size, every one a constant, which std/embed's private
// constructor makes the declaration's `Files` of. The handle is an
// ir.ConstEmbedded, which names where the contents are and carries none of
// them: the program reads a file's contents through its handle when it
// reads the file. The `once` the declaration expands into is lowered only
// when the program reaches it, so a pruned image holds the files of the
// embeds it uses alone.
func (bl *irScalarBuilder) embeddedFiles(t *ast.EmbeddedFiles) (ir.Temp, kind, bool, bool) {
	// An `embed` whose patterns' bases do not exist yet holds no file
	// (analysis.EmbedInfo.Missing); its list is empty.
	pos := bl.g.irNodePos(t)
	elem := bl.g.tupleKind([]kind{kindString, kindInt, kindInt})
	values := make([]ir.Temp, len(t.Files))
	for i, f := range t.Files {
		p := ir.NewString(pos, bl.f.NewTemp(), f.Path)
		bl.b.Append(p)
		handle := ir.NewEmbedded(pos, bl.f.NewTemp(), ir.EmbeddedRef{Source: f.Source, Size: f.Size, Sum: f.Sum})
		bl.b.Append(handle)
		size := ir.NewInt(pos, bl.f.NewTemp(), f.Size)
		bl.b.Append(size)
		tuple := ir.NewMakeTuple(pos, bl.f.NewTemp(), []ir.Temp{p.Dst(), handle.Dst(), size.Dst()})
		bl.b.Append(tuple)
		bl.side(tuple.Dst(), irScalarSide{k: elem})
		values[i] = tuple.Dst()
	}
	k := bl.g.listKind(elem)
	n := ir.NewMakeList(pos, bl.f.NewTemp(), values, ir.NoTemp)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}
