package vm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// The benchmark programs' hot functions compile to typed opcodes only: no
// instruction runs through its boxed handler, except a `case`'s no-match arm,
// which is never taken, and a struct construction, which builds an rt record.
// A typed opcode that stops being chosen — an operand whose stored type moved
// to another bank, a call whose arguments stopped matching the callee's
// parameter banks — leaves every output right and costs speed, so this reads
// the bytecode rather than a timing.
func TestBytecode_BenchmarkHotPathsAreTyped(t *testing.T) {
	for _, tc := range []struct {
		program string
		funcs   []string
		// want are opcodes each function's listing must contain.
		want map[string][]string
	}{
		{"fib", []string{"fib"}, map[string][]string{"fib": {"call", "cmpi", "addi", "subi"}}},
		{"variants", []string{"area", "pick"}, map[string][]string{
			"area": {"matchv", "projp", "muli"},
			"pick": {"remi", "eqw", "make"},
		}},
		{"structs", []string{"step"}, map[string][]string{"step": {"projf", "addi", "cmpi", "make"}}},
	} {
		src, err := os.ReadFile(filepath.Join("..", "..", "benchmarks", "engines", tc.program+".nomi"))
		if err != nil {
			t.Fatal(err)
		}
		p, err := vmhost.LoadSource(tc.program, string(src))
		if err != nil {
			t.Fatalf("%s: %v", tc.program, err)
		}
		for _, name := range tc.funcs {
			listing, err := p.Disassemble(name)
			if err != nil {
				t.Fatalf("%s.%s: %v", tc.program, name, err)
			}
			for _, line := range strings.Split(listing, "\n") {
				fields := strings.Fields(line)
				if len(fields) > 1 && fields[1] == "ir" && !strings.HasSuffix(line, "; nomatch") {
					t.Errorf("%s.%s runs an instruction through its boxed handler:\n%s\n\nlisting:\n%s",
						tc.program, name, line, listing)
				}
			}
			for _, op := range tc.want[name] {
				if !strings.Contains(listing, " "+op+" ") {
					t.Errorf("%s.%s has no %s opcode:\n%s", tc.program, name, op, listing)
				}
			}
		}
	}
}

// A call whose value is the caller's result is a transfer, and the listing
// says so: `tail` rather than `call`.
func TestBytecode_TailCallsCompileToTail(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std/io

fn count(n: Int, acc: Int): Int {
  if n == 0 { acc } else { count(n - 1, acc + 1) }
}

fn main() {
  count(10, 0) |> io.print()
}
`)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.Disassemble("count")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, " tail ") || strings.Contains(listing, " call ") {
		t.Errorf("count's self call is not a tail transfer:\n%s", listing)
	}
}
