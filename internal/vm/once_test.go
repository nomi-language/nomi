package vm

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func onceTestModule() (*ir.Module, *ir.Symbol, *ir.Func) {
	at := ir.At("once.nomi", 1, 1)
	sym := ir.NewSymbol("seed")
	mod := ir.NewModule("once.nomi")
	reader := ir.NewFunc(at, "read")
	b := reader.NewBlock(at, "entry")
	ref := ir.NewRefOnce(at, reader.NewTemp(), sym)
	reader.SetType(ref.Dst(), ir.IntType)
	b.Append(ref)
	b.SetTerm(ir.NewReturn(at, ref.Dst()))
	mod.AddFunc(reader)
	return mod, sym, reader
}

func TestOnceConcurrentReadsForceExactlyOnce(t *testing.T) {
	mod, sym, _ := onceTestModule()
	at := ir.At("once.nomi", 2, 1)
	init := ir.NewFunc(at, "initializer")
	b := init.NewBlock(at, "entry")
	text := ir.NewString(at, init.NewTemp(), "initialize")
	b.Append(text)
	print := ir.NewHostCall(at, init.NewTemp(), ir.OrdinaryCall, ir.NewSymbol("io.print"), text.Dst())
	init.SetType(print.Dst(), ir.UnitType)
	b.Append(print)
	n := ir.NewInt(at, init.NewTemp(), 42)
	b.Append(n)
	b.SetTerm(ir.NewReturn(at, n.Dst()))
	ty, _ := ir.NewTable().Concrete("Int", "Int")
	ty.SetVal(ir.IntType)
	mod.DeclareLazyCell(sym, ty, init)
	if err := ir.LintModule(mod); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	m := New(mod, &out)
	if out.Len() != 0 {
		t.Fatal("initializer ran during machine construction")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := m.Run("read")
			if err != nil {
				errs <- err
			} else if v != any(int64(42)) {
				errs <- fmt.Errorf("answer %v", v)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if out.String() != "initialize\n" {
		t.Fatalf("initializer effects: %q", out.String())
	}
}

func TestOnceCycleThroughHostCallbackKeepsLineage(t *testing.T) {
	mod, sym, reader := onceTestModule()
	at := ir.At("once.nomi", 2, 1)
	init := ir.NewFunc(at, "initializer")
	b := init.NewBlock(at, "entry")
	call := ir.NewHostCall(at, init.NewTemp(), ir.OrdinaryCall, ir.NewSymbol("callback"))
	b.Append(call)
	b.SetTerm(ir.NewReturn(at, call.Dst()))
	ty, _ := ir.NewTable().Concrete("Int", "Int")
	mod.DeclareLazyCell(sym, ty, init)
	m := New(mod, io.Discard)
	m.hosts["callback"] = func(m *Machine, _ ir.Pos, _ []any) (any, error) { return m.call(reader, nil) }
	_, err := m.Run("read")
	if err == nil || err.Error() != "cyclic 'once' binding: 'seed' depends on itself" {
		t.Fatalf("cycle: %v", err)
	}
}

func TestOnceMissingInitializerIsAnError(t *testing.T) {
	mod, _, _ := onceTestModule()
	if _, err := New(mod, io.Discard).Run("read"); err == nil {
		t.Fatal("missing initializer accepted")
	}
}

func TestOnceFailedInitializerDoesNotCacheAValue(t *testing.T) {
	mod, sym, _ := onceTestModule()
	at := ir.At("once.nomi", 2, 1)
	init := ir.NewFunc(at, "initializer")
	b := init.NewBlock(at, "entry")
	call := ir.NewHostCall(at, init.NewTemp(), ir.OrdinaryCall, ir.NewSymbol("initialize"))
	b.Append(call)
	b.SetTerm(ir.NewReturn(at, call.Dst()))
	ty, _ := ir.NewTable().Concrete("Int", "Int")
	mod.DeclareLazyCell(sym, ty, init)
	m := New(mod, io.Discard)
	count := 0
	m.hosts["initialize"] = func(_ *Machine, _ ir.Pos, _ []any) (any, error) {
		count++
		if count == 1 {
			return nil, fmt.Errorf("initializer failed")
		}
		return int64(42), nil
	}
	if _, err := m.Run("read"); err == nil || err.Error() != "initializer failed" {
		t.Fatalf("first force: %v", err)
	}
	for i := 0; i < 2; i++ {
		if v, err := m.Run("read"); err != nil || v != any(int64(42)) {
			t.Fatalf("later force: %v, %v", v, err)
		}
	}
	if count != 2 {
		t.Fatalf("initializer ran %d times", count)
	}
}
