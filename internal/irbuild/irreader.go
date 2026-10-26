package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// readerFromChunks lowers `Reader.from_chunks(chunks)` and
// `Reader.from_text_chunks(chunks)`: any Iter of Bytes, or of String for
// the text form, viewed as the sequence rt takes, and a Reader that pulls
// it a chunk at a time (internal/vm/readers.go). The two are std/io `host
// fn`s only the VM answers, as `Supervisor.spawn_all` is: a parameter of
// type `Iter<T>` has no shared lowering, so every call is this crossing.
func (bl *irScalarBuilder) readerFromChunks(t *ast.Call, name string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.rowsUnrecorded(t) || namedArgNode(t.Args) != nil || len(t.Args) != 1 {
		return no()
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok {
		return no()
	}
	view, elem, ok := bl.iterPlainView(argExpr(t.Args[0]), args.temps[0], args.kinds[0])
	if !ok {
		return no()
	}
	want := irBytesKind()
	if name == "from_text_chunks" {
		want = kindString
	}
	if elem != want {
		return no()
	}
	return bl.concHostEmit(t, "io.Reader."+name, stdHostOriginKind("std/io", "Reader"), view)
}
