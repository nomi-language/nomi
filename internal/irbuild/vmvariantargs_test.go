package irbuild

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

func TestVMCrossModuleDistinctArguments(t *testing.T) {
	p, err := AnalyzeSource("forward.nomi", `import std/instant.Instant
fn seconds(t: Instant): Int { Instant.to_seconds(t) }
fn main() { _ = seconds(Instant.from_seconds(1)) }
`)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() != "seconds" {
				continue
			}
			args := vmArgShapes(mod, fn, res.IRModules()...)[0]
			if r, ok := args[0].(*rt.Record); !ok || r.Desc.Kind != rt.KindDistinct {
				t.Fatalf("forwarded argument is %T, want distinct", args[0])
			}
			if _, err := vmRunSymV(vm.NewProgram(mod, res.IRModules(), io.Discard), fn.Sym(), args...); err != nil {
				t.Fatal(err)
			}
			checked++
		}
	}
	if checked != 1 {
		t.Fatalf("checked %d forwarding functions, want 1", checked)
	}
}

func TestVMTagOnlyEnumArgumentsAndForwarding(t *testing.T) {
	p, err := AnalyzeSource("signals.nomi", `enum Signal {
  Idle
  Ready
}
fn number(s: Signal): Int {
  case s {
    .Idle -> 42
    .Ready -> 43
  }
}
fn relay(s: Signal): Int { number(s) }
fn main() { _ = relay(Signal.Idle) }
`)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() != "number" && fn.Name() != "relay" {
				continue
			}
			for _, args := range vmArgShapes(mod, fn) {
				_, enum, variant, ok := vmRecord(args[0])
				if !ok || enum != "signals.nomi.Signal" || variant != "Idle" {
					t.Fatalf("%s received invalid enum argument: %#v", fn.Name(), args[0])
				}
				answer, err := vmRunSymV(vm.NewProgram(mod, res.IRModules(), io.Discard), fn.Sym(), args...)
				if err != nil || answer != any(int64(42)) {
					t.Fatalf("%s: answer=%v, error=%v", fn.Name(), answer, err)
				}
			}
			checked++
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d functions; want direct test and forwarding caller", checked)
	}
}
