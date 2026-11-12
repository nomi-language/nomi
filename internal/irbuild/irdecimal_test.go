package irbuild

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

func TestIRDecimal_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn sum(a: Decimal, b: Decimal) -> Decimal
    a + b
end
fn divide(a: Decimal, b: Decimal) -> Decimal
    Decimal.divide_exact(a, b) |> Maybe.with_default(0d)
end
fn main()
  io.print(sum(0.1d, 0.2d) == 0.3d)
  io.print(1.50d == 1.5d)
  io.print(1.50d != 1.5d)
  io.inspect(sum(19.99d, 5.00d) + 2.50d + 0.99d)
  io.inspect(divide(1.0d, 8d))
  io.inspect(-1.50d)
  io.inspect(999999999999999999999999999999d + 1d)
  io.inspect(1.25d * 4d - 0.5d)
  io.print("amount ${1.50d}")
end
`, "True\nTrue\nFalse\n28.48d\n0.125d\n-1.50d\n1000000000000000000000000000000d\n4.50d\namount 1.50\n")
}

func TestIRDecimal_EffectsAndBranchValues(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(label: String, d: Decimal) -> Decimal
    io.print(label)
    d
end
fn choose(b: Bool) -> Decimal
    if b then 1.50d else 2.25d
end
fn main()
  answer = mark("left", choose(True)) + mark("right", choose(False))
  io.print(answer)
  io.print(mark("a", 1.5d) == mark("b", 1.50d))
  f = fn d: Decimal -> -d
  io.inspect(f(answer))
end
`, "left\nright\n3.75\na\nb\nTrue\n-3.75d\n")
}

// `/` on Decimal rounds to 34 significant digits and keeps an exact
// quotient's smallest scale, through the operator, `Divide.divide` and
// `Decimal.divide` with two arguments.
func TestIRDecimal_Division(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn quotient(a: Decimal, b: Decimal) -> Decimal
    a / b
end
fn half<T>(x: T, two: T) -> T where T: Divide<T, T>
    x / two
end
fn main()
  io.inspect(quotient(10d, 3d))
  io.inspect(quotient(10.00d, 4d))
  io.inspect(Divide.divide(1d, 4d))
  io.inspect(Decimal.divide(6d, 3d))
  io.inspect(half(-7d, 2d))
end
`, "3.333333333333333333333333333333333d\n2.5d\n0.25d\n2d\n-3.5d\n")
}

// A zero divisor of `/`, `Divide.divide` and `Decimal.divide_exact` traps
// with Int's `/` text at the operator's or the call's line.
func TestIRDecimal_DivisionFaults(t *testing.T) {
	for _, tc := range []struct{ name, call, want string }{
		{"divide_exact", "Decimal.divide_exact(n, 0d) |> Maybe.with_default(0d)", "line 3: division by zero"},
		{"operator", "n / 0d", "line 3: division by zero"},
		{"Divide.divide", "Divide.divide(n, 0.00d)", "line 3: division by zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.nomi")
			src := "fn divide() -> Decimal\n  n = 1d\n  " + tc.call + "\nend\nfn main()\n  _ = divide()\nend\n"
			if err := os.WriteFile(path, []byte(src), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := Analyze(path)
			if err != nil {
				t.Fatal(err)
			}
			res, _, err := GenerateIR(p)
			if err != nil {
				t.Fatal(err)
			}
			retained := false
			for _, mod := range res.IR {
				for _, fn := range mod.Funcs() {
					if fn.Name() != "main" {
						continue
					}
					retained = true
					_, err := vmRunSymV(vm.NewProgram(mod, res.IRModules(), io.Discard), fn.Sym())
					var fault *vm.Fault
					if !errors.As(err, &fault) || err.Error() != tc.want {
						t.Fatalf("VM fault: %v; want %q", err, tc.want)
					}
				}
			}
			if !retained {
				t.Fatal("faulting program was not retained")
			}
			for via, got := range map[string]observation{"vm command": vmReference(path)} {
				if got.exit == 0 || strings.TrimSpace(got.stderr) != tc.want || got.stdout != "" {
					t.Fatalf("%s: %v; want %q", via, got, tc.want)
				}
			}
		})
	}
}
