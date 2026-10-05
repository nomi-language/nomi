package irbuild

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

func TestIRVariantExhaustive_InvalidVariantReachesNoMatch(t *testing.T) {
	p, err := AnalyzeSource("signals.nomi", `enum Signal {
  Idle
  Value Int
}
fn number(s: Signal): Int {
  case s {
    .Idle -> 0
    .Value(n) -> n
  }
}
fn main() { _ = number(Signal.Idle) }
`)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() != "number" {
				continue
			}
			machine := vm.NewProgram(mod, res.IRModules(), io.Discard)
			_, err := vmRunSymV(machine, fn.Sym(), &probeVariant{enum: "signals.nomi.Signal", variant: "Missing"})
			if err == nil || !strings.Contains(err.Error(), "line 6: no matching case branch") {
				t.Fatalf("invalid variant did not take the failure edge: %v", err)
			}
			return
		}
	}
	t.Fatal("number was not retained")
}

func TestIRVariantExhaustive_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"bare variants", `import std/io
enum Color {
  Red
  Green
  Blue
}
fn number(c: Color): Int {
  case c {
    .Red -> 1
    .Green -> 2
    .Blue -> 3
  }
}
fn main() {
  io.print(number(Color.Red))
  io.print(number(Color.Green))
  io.print(number(Color.Blue))
}`, "1\n2\n3\n"},
		{"literal and bound payload", `import std/io
enum Signal {
  Idle
  Value Int
}
fn number(s: Signal): Int {
  case s {
    .Value(0) -> 10
    .Idle -> 20
    .Value(n) -> n + 1
  }
}
fn main() {
  io.print(number(Signal.Value(0)))
  io.print(number(Signal.Idle))
  io.print(number(Signal.Value(41)))
}`, "10\n20\n42\n"},
		{"nested case and captured payload", `import std/io
enum Signal {
  Idle
  Value Int
}
fn main() {
  n = 100
  choose = |s: Signal| {
    case s {
      .Idle -> |x: Int| n - x
      .Value(n) -> |x: Int| {
        case x {
          0 -> n
          _ -> n + x
        }
      }
    }
  }
  first = choose(Signal.Value(3))
  second = choose(Signal.Idle)
  io.print(first(0)) io.print(first(10)) io.print(second(10)) io.print(n)
}`, "3\n13\n90\n100\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
