package vm_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

const entriesSource = `import std/io

fn count_down(n: Int) -> Int {
  if n <= 0 {
    0
  } else {
    count_down(n - 1)
  }
}

fn twice(n: Int) -> Int {
  count_down(n) + count_down(n)
}

fn same(d: Decimal) -> Decimal {
  d
}

fn main() {
  io.print(Int.to_string(twice(2)))
}
`

func entriesFunc(t *testing.T, mod *ir.Module, name string) *ir.Func {
	t.Helper()
	for _, f := range mod.Funcs() {
		if f.Name() == name {
			return f
		}
	}
	t.Fatalf("%s was not retained, so this test has no subject", name)
	return nil
}

// TestEntries_AMachineWithoutCountingCompilesNoEnter holds a plain machine's
// bytecode to having no `enter`, and a counting machine's to one at pc 0.
func TestEntries_AMachineWithoutCountingCompilesNoEnter(t *testing.T) {
	mod := dbgLoweredModule(t, entriesSource)
	for _, name := range []string{"count_down", "twice", "same", "main"} {
		plain, err := vm.New(mod, io.Discard).Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plain, " enter ") {
			t.Errorf("%s: a plain machine compiled an entry counter:\n%s", name, plain)
		}
		counted, err := vm.New(mod, io.Discard).WithEntryCounts().Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		first := ""
		if lines := strings.Split(counted, "\n"); len(lines) > 1 {
			first = strings.TrimSpace(lines[1])
		}
		if !strings.HasPrefix(first, "0  enter") {
			t.Errorf("%s: a counting machine's first instruction is not `enter`:\n%s", name, counted)
		}
	}
}

// TestEntries_CountEveryEntryAndOnlyEntries counts a call from Go, calls from
// bytecode and tail transfers, shares the count with a view, and counts no
// entry for an argument the parameter refuses.
func TestEntries_CountEveryEntryAndOnlyEntries(t *testing.T) {
	mod := dbgLoweredModule(t, entriesSource)
	m := vm.New(mod, io.Discard).WithEntryCounts()
	view := m.WithLimits(vm.Limits{Steps: 1_000_000})
	v, err := view.Run("twice", int64(3))
	if err != nil {
		t.Fatal(err)
	}
	if v != int64(0) {
		t.Fatalf("twice(3) = %v, want 0", v)
	}
	if got := m.Entries(entriesFunc(t, mod, "twice")); got != 1 {
		t.Errorf("twice entered %d time(s), want 1", got)
	}
	// Each count_down(3) enters for 3, 2, 1 and 0.
	if got := m.Entries(entriesFunc(t, mod, "count_down")); got != 8 {
		t.Errorf("count_down entered %d time(s), want 8", got)
	}
	if got := m.Entries(entriesFunc(t, mod, "main")); got != 0 {
		t.Errorf("main entered %d time(s) without being called", got)
	}

	same := entriesFunc(t, mod, "same")
	_, err = m.Run("same", int64(1))
	want := "parameter d is Decimal and received int64"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("a counting machine handed an Int for a Decimal parameter answered %v, want %q", err, want)
	}
	if got := m.Entries(same); got != 0 {
		t.Errorf("same entered %d time(s) on a refused argument", got)
	}
	if _, err := m.Run("same", rt.Decimal{}); err != nil {
		t.Fatal(err)
	}
	if got := m.Entries(same); got != 1 {
		t.Errorf("same entered %d time(s), want 1", got)
	}

	plain := vm.New(mod, io.Discard)
	if _, err := plain.Run("twice", int64(3)); err != nil {
		t.Fatal(err)
	}
	if got := plain.Entries(entriesFunc(t, mod, "twice")); got != 0 {
		t.Errorf("a plain machine counted %d entries", got)
	}
}
