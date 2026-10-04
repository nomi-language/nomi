package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestIRIterBody_TourMap(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 [1, 2, 3]
 |> Iter.map(|n| n * n)
 |> Iter.to_list()
 |> io.inspect()
}
`, "[1, 4, 9]\n")
}

func TestIRIterBody_CallbackFaultKeepsItsPosition(t *testing.T) {
	verifyIterFault(t, `import std/io
fn main() {
 xs = Iter.map([1, 0, 2], |n| {
  io.print(n)
  10 / n
 })
 io.print("created")
 io.inspect(Iter.to_list(xs))
}
`, "line 5: division by zero", "created\n1\n0\n")
}

func verifyIterFault(t *testing.T, src, fault, output string) {
	t.Helper()
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
	for _, mod := range res.IR {
		for _, f := range mod.Funcs() {
			if f.Name() == "main" {
				entry = mod
			}
		}
	}
	if entry == nil {
		t.Fatal("main was not retained")
	}
	var out bytes.Buffer
	_, err = vm.NewProgram(entry, res.IRModules(), &out).Run("main")
	if err == nil || !strings.Contains(err.Error(), fault) || out.String() != output {
		t.Fatalf("VM fault: %v; output %q", err, out.String())
	}
	if got := goldenReference(t, path); got.exit == 0 || got.stdout != out.String() || !strings.Contains(got.stderr, fault) {
		t.Fatalf("callback fault mismatch: %s", got)
	}
}
func TestIRIterBody_LazyRepeatableAndCaptured(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(): List<Int> { io.print("source"); [1, 2] }
fn main() {
 offset = 10
 xs = Iter.map(source(), |n| { io.print(n); n + offset })
 io.print("created")
 io.inspect(Iter.to_list(xs))
 io.inspect(Iter.to_list(xs))
}
`, "source\ncreated\n1\n2\n[11, 12]\n1\n2\n[11, 12]\n")
}

func TestIRIterBody_ListIdentityAndEmptyMap(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): List<Int> { [] }
fn size(_xs: List<Int>, n: Int): Int { n }
fn after(): Int { io.print("after"); 9 }
fn main() {
 xs = [1, 2]
 io.print(size(Iter.to_list(xs), after()))
 io.inspect(Iter.to_list(Iter.map(empty(), |n| { io.print(n); n + 1 })))
 io.inspect(Iter.to_list(Iter.map(Iter.map(xs, |n| n + 1), |n| "item ${n}")))
}
`, "after\n9\n[]\n[\"item 2\", \"item 3\"]\n")
}

// A Map whose values are Maps is an iteration source like any other map.
func TestIRIterBody_NestedMapSourceRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() { io.inspect(Iter.to_list(Iter.map({1 => {2 => 3}}, |p| p.0 + 1))) }
`, "[2]\n")
}

func TestIRIterBody_CallbackReturnKeepsMapping(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 mapped = [1, 2, 3, 4, 5]
 |> Iter.map(|x| {
  if x == 3 { return 300 }
  x * 10
 })
 |> Iter.to_list()
 io.inspect(mapped)
}
`, "[10, 20, 300, 40, 50]\n")
}
