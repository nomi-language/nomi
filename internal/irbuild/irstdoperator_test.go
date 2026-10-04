package irbuild

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

func TestIRStdOperator_LinkedFunctionRunsOnTheVM(t *testing.T) {
	for _, tc := range []struct {
		name, op, rhsType, rhsSource string
		rhs                          any
		want                         int64
	}{
		{"add", "+", "Duration", "Duration.nanoseconds(21)", durationVMValue(21), 27},
		{"subtract", "-", "Duration", "Duration.nanoseconds(21)", durationVMValue(21), -15},
		{"multiply", "*", "Int", "3", int64(3), 18},
		{"divide", "/", "Int", "3", int64(3), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(`import { std/io std/duration.Duration }
fn left(a: Duration): Duration { io.print("operand") a }
fn exercise(a: Duration, b: %s): Duration {
  left(
    a
  ) %s b
}
fn main() {
  exercise(Duration.nanoseconds(6), %s) |> Duration.as_nanos() |> io.print()
}`, tc.rhsType, tc.op, tc.rhsSource)
			path := filepath.Join(t.TempDir(), "main.nomi")
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
			var entry *ir.Module
			for _, m := range res.IR {
				for _, f := range m.Funcs() {
					if f.Name() == "exercise" {
						entry = m
					}
				}
			}
			if entry == nil {
				t.Fatal("exercise was not retained")
			}
			args := []any{durationVMValue(6), tc.rhs}
			if _, err := vmRunV(vm.New(entry, io.Discard), "exercise", args...); err == nil || !strings.Contains(err.Error(), "did not retain") {
				t.Fatalf("unlinked execution should name its missing declaration: %v", err)
			}
			var out bytes.Buffer
			got, err := vmRunV(vm.NewProgram(entry, res.IRModules(), &out), "exercise", args...)
			if err != nil {
				t.Fatal(err)
			}
			if !rt.Equal(got, vmOperand(durationVMValue(tc.want))) {
				t.Fatalf("VM result = %#v, want Duration(%d)", got, tc.want)
			}
			if out.String() != "operand\n" {
				t.Fatalf("VM operand effects: %q", out.String())
			}
			want := fmt.Sprintf("operand\n%d\n", tc.want)
			if got := vmReference(path); got.exit != 0 || got.stdout != want || got.stderr != "" {
				t.Fatalf("vm command: %s", got)
			}
		})
	}
}

func durationVMValue(n int64) any {
	return &probeDistinct{name: "duration.Duration", inner: n}
}
