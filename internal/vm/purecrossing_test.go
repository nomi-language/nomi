package vm

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
	"github.com/nomi-language/nomi/rt"
)

// TestPureCrossing_ReadsTheDeclarations holds pureCrossing to the
// declarations it reads: an intrinsic is pure exactly when its table entry
// says so, a stdlib adapter exactly when its row is marked Pure, and a
// crossing a project's own table answers is an effect whatever its name.
func TestPureCrossing_ReadsTheDeclarations(t *testing.T) {
	user := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{"mylib.double": func(*rt.Frame, []rt.Value) (rt.Value, error) { return nil, nil }}, nil
	}
	m := New(ir.NewModule("pure"), io.Discard).WithHosts(user)
	for name, in := range m.hosts {
		if m.pureCrossing(name) != in.pure {
			t.Errorf("intrinsic %s: pureCrossing says %v, its table entry %v", name, !in.pure, in.pure)
		}
	}
	funcs, err := stdlibadapters.Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	pure := 0
	for name := range funcs {
		if _, isIntrinsic := m.hosts[name]; isIntrinsic {
			t.Errorf("%s is both an intrinsic and a stdlib adapter", name)
		}
		if stdlibadapters.Pure(name) {
			pure++
		}
		if m.pureCrossing(name) != stdlibadapters.Pure(name) {
			t.Errorf("adapter %s: pureCrossing disagrees with its row", name)
		}
	}
	if pure == 0 {
		t.Fatal("no stdlib adapter is marked Pure")
	}
	for _, name := range []string{
		// Output, tasks, channels and the Context.
		"io.print", "Task.spawn", "Sender.send", "Context.value",
		// The clock, randomness and files.
		"instant.Instant.now", "random.os_state", "fs/host.read",
		// Pure, but each call's cost follows an operand's value.
		"strings.String.repeat", "decimal.Decimal.divide",
		// A project's own `go` binding.
		"mylib.double",
	} {
		if m.pureCrossing(name) {
			t.Errorf("%s counts as pure", name)
		}
	}
}
