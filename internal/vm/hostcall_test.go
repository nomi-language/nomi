package vm

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// hostModule is a module whose function `f` passes its one parameter, of
// shape param, to the crossing named host and returns the answer.
func hostModule(host string, param ir.ValShape) *ir.Module {
	at := ir.At("host.nomi", 1, 1)
	mod := ir.NewModule("host")
	f := ir.NewFunc(at, "f")
	x := f.AddParam(ir.NewSymbol("x"), param)
	b := f.NewBlock(at, "entry")
	call := ir.NewHostCall(at, f.NewTemp(), ir.OrdinaryCall, ir.NewSymbol(host), x)
	b.Append(call)
	b.SetTerm(ir.NewReturn(at, call.Dst()))
	mod.AddFunc(f)
	return mod
}

// TestHostCall_AStdlibCrossingIsAnAdapterCall: a crossing a stdlib adapter
// answers compiles to opHost and runs the generated adapter over the
// machine's own values.
func TestHostCall_AStdlibCrossingIsAnAdapterCall(t *testing.T) {
	m := New(hostModule("strings.String.trim", ir.ValString), io.Discard)
	got, err := m.run("f", []any{"  padded\t"})
	if err != nil || got != "padded" {
		t.Fatalf("String.trim answered %#v, %v", got, err)
	}
	text, err := m.Disassemble("f")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "host") || !strings.Contains(text, "strings.String.trim") {
		t.Fatalf("the crossing did not compile to opHost:\n%s", text)
	}
}

// TestHostCall_AWrongOperandIsRefused: an operand of the wrong type is the
// adapter's refusal, an error rather than a panic.
func TestHostCall_AWrongOperandIsRefused(t *testing.T) {
	m := New(hostModule("strings.String.trim", ir.ValInt), io.Discard)
	_, err := m.run("f", []any{int64(1)})
	if err == nil || !strings.Contains(err.Error(), "expected String") {
		t.Fatalf("an Int operand to String.trim: %v", err)
	}
}

// TestHostCall_ATableCallsBackIntoTheMachine: a table a host adds with
// WithHosts is bound against the machine's Env, whose Invoker calls a VM
// function value — the callback path a project's Go binding takes.
func TestHostCall_ATableCallsBackIntoTheMachine(t *testing.T) {
	at := ir.At("callback.nomi", 1, 1)
	mod := ir.NewModule("callback")
	id := ir.NewFunc(at, "id")
	x := id.AddParam(ir.NewSymbol("x"), ir.ValInt)
	id.NewBlock(at, "entry").SetTerm(ir.NewReturn(at, x))
	drive := ir.NewFunc(at, "drive")
	n := drive.AddParam(ir.NewSymbol("n"), ir.ValInt)
	b := drive.NewBlock(at, "entry")
	fn := ir.NewFuncValue(at, drive.NewTemp(), id)
	call := ir.NewHostCall(at, drive.NewTemp(), ir.OrdinaryCall, ir.NewSymbol("demo.apply_twice"), fn.Dst(), n)
	b.Append(fn)
	b.Append(call)
	b.SetTerm(ir.NewReturn(at, call.Dst()))
	mod.AddFunc(drive)

	table := func(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{
			"demo.apply_twice": func(fr *rt.Frame, args []rt.Value) (rt.Value, error) {
				sum := int64(0)
				for range 2 {
					v, err := env.Call(fr, args[0], []rt.Value{args[1]})
					if err != nil {
						return nil, err
					}
					sum += v.(int64)
				}
				return sum, nil
			},
		}, nil
	}
	got, err := New(mod, io.Discard).WithHosts(table).run("drive", []any{int64(21)})
	if err != nil || got != int64(42) {
		t.Fatalf("apply_twice answered %#v, %v", got, err)
	}
}

// TestHostCall_TwoTablesMayNotAnswerOneName: a table that answers a name the
// stdlib table already answers fails binding rather than picking one.
func TestHostCall_TwoTablesMayNotAnswerOneName(t *testing.T) {
	table := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{"strings.String.trim": func(*rt.Frame, []rt.Value) (rt.Value, error) { return "", nil }}, nil
	}
	_, err := New(hostModule("strings.String.trim", ir.ValString), io.Discard).WithHosts(table).run("f", []any{"x"})
	if err == nil || !strings.Contains(err.Error(), "two tables answer strings.String.trim") {
		t.Fatalf("a name two tables answer: %v", err)
	}
}

// TestHostCall_AnRtTrapIsAFault: a Go host function that traps reports a
// Nomi fault with rt's text, as a trap anywhere else in an activation does.
func TestHostCall_AnRtTrapIsAFault(t *testing.T) {
	table := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{"demo.trap": func(*rt.Frame, []rt.Value) (rt.Value, error) {
			rt.Trap("the host trapped")
			return nil, nil
		}}, nil
	}
	_, err := New(hostModule("demo.trap", ir.ValInt), io.Discard).WithHosts(table).run("f", []any{int64(1)})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Error() != "the host trapped" {
		t.Fatalf("a trapping host: %v", err)
	}
}

// TestHostCall_AssertionFailureFormatReadsTheMachinesValues: the generated
// adapter for `AssertionFailure.format` reads the AssertionFailure values this
// machine builds (`testing.check`'s Err) and renders them as rt's formatter
// renders the Go struct they came from.
func TestHostCall_AssertionFailureFormatReadsTheMachinesValues(t *testing.T) {
	stages := rtSliceList([]rt.NomiAssertionPipelineStage{{Expression: "xs", Value: "[1, 2]"}, {Expression: "Iter.count()", Value: "2"}})
	samples := []rt.NomiAssertionFailure{
		{Line: 3, Keyword: "assert", Expression: "x == 2", Reason: "assertion failed",
			Actual: rt.None[string](), Binding: rt.None[rt.NomiAssertionBinding]()},
		{Line: 7, Keyword: "check", Expression: "xs |> Iter.count() == 3", Reason: "values differ",
			Actual:  rt.Some("2"),
			Binding: rt.Some(rt.NomiAssertionBinding{Name: "n", Expression: "xs |> Iter.count()", Value: "2", Pipeline: stages}),
			Values:  rtSliceList([]rt.NomiAssertionValue{{Expression: "left", Value: "2", Pipeline: stages}, {Expression: "right", Value: "3"}}),
			Details: rtSliceList([]rt.NomiAssertionDetail{{Label: "expected", Value: "3"}, {Label: "é\n", Value: "\"q\""}})},
	}
	m := New(ir.NewModule("format"), io.Discard)
	format, err := m.adapter("assertions.AssertionFailure.format")
	if err != nil || format == nil {
		t.Fatalf("no adapter for AssertionFailure.format: %v", err)
	}
	for i, f := range samples {
		got, err := format(nil, []rt.Value{nomiFailureValue(f)})
		if want := rt.FormatNomiAssertionFailure(f); err != nil || got != want {
			t.Errorf("sample %d: adapter answered %q, %v; rt renders %q", i, got, err, want)
		}
	}
}
