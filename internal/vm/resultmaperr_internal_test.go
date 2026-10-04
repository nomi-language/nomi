package vm

import (
	"io"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestResultMapErrCallbackKeepsOnceLineage(t *testing.T) {
	mod, sym, _ := onceTestModule()
	at := ir.At("result-once.nomi", 1, 1)
	callback := ir.NewFunc(at, "callback")
	callback.AddParam(ir.NewSymbol("item"), ir.ValInt)
	cb := callback.NewBlock(at, "entry")
	ref := ir.NewRefOnce(at, callback.NewTemp(), sym)
	cb.Append(ref)
	cb.SetTerm(ir.NewReturn(at, ref.Dst()))
	drive := ir.NewFunc(at, "drive")
	xs := drive.AddParam(ir.NewSymbol("xs"), ir.ValVariant)
	b := drive.NewBlock(at, "entry")

	fn := ir.NewFuncValue(at, drive.NewTemp(), callback)
	mapped := ir.NewHostCall(at, drive.NewTemp(), ir.OrdinaryCall, ir.NewSymbol("Result.map_err"), xs, fn.Dst())
	b.Append(fn)
	b.Append(mapped)
	b.SetTerm(ir.NewReturn(at, mapped.Dst()))
	mod.AddFunc(drive)
	init := ir.NewFunc(at, "initializer")
	ib := init.NewBlock(at, "entry")
	call := ir.NewHostCall(at, init.NewTemp(), ir.OrdinaryCall, ir.NewSymbol("drive"))
	ib.Append(call)
	ib.SetTerm(ir.NewReturn(at, call.Dst()))
	ty, _ := ir.NewTable().Concrete("Int", "Int")
	mod.DeclareLazyCell(sym, ty, init)
	m := New(mod, io.Discard)
	m.hosts["drive"] = func(m *Machine, _ ir.Pos, _ []any) (any, error) {
		return m.call(drive, []any{errValue(int64(1))})
	}
	done := make(chan error, 1)
	go func() { _, err := m.Run("read"); done <- err }()
	select {
	case err := <-done:
		if err == nil || err.Error() != "cyclic 'once' binding: 'seed' depends on itself" {
			t.Fatalf("Result.map_err lost cyclic initializer fault: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Result.map_err callback lost forcing lineage and deadlocked")
	}
}
