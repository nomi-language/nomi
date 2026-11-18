package vm_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestVMTupleDebug_ComposesDebugAndRejectsUnsupportedChildren(t *testing.T) {
	at := ir.At("tuple-debug.nomi", 1, 1)
	f := ir.NewFunc(at, "render")
	arg := f.AddParam(ir.NewSymbol("tuple"), ir.ValTuple)
	b := f.NewBlock(at, "entry")
	render := ir.NewRenderDebug(at, f.NewTemp(), arg)
	b.Append(render)
	b.SetTerm(ir.NewReturn(at, render.Dst()))
	mod := ir.NewModule("tuple-debug.nomi")
	mod.AddFunc(f)
	for _, tc := range []struct {
		name  string
		items []any
		want  string
	}{
		{"empty", nil, "()"},
		{"singleton", []any{int64(1)}, "(1)"},
		{"nested and escaping", []any{rtTuple("a\nb", "a\"b"), int64(42)}, "((\"a\\nb\", \"a\\\"b\"), 42)"},
		{"unsupported child", []any{int64(1), rtStruct("Point")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vm.New(mod, io.Discard).Run("render", rtTuple(tc.items...))
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "Debug on an unrepresented receiver (Point)") {
					t.Fatalf("unsupported tuple child: result=%v error=%v", got, err)
				}
				return
			}
			if err != nil || got != any(tc.want) {
				t.Fatalf("result=%v error=%v; want %q", got, err, tc.want)
			}
		})
	}
}
