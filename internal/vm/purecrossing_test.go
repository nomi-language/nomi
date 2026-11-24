package vm

import (
	"io"
	"strings"
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
		// Output, tasks and channels.
		"io.print", "Task.spawn", "Sender.send",
		// The clock, randomness and files.
		"instant.Instant.now", "context.Context.with_timeout", "random.os_state", "fs/host.read",
		// A project's own `host fn`, which an embedder answers.
		"mylib.double",
	} {
		if m.pureCrossing(name) {
			t.Errorf("%s counts as pure", name)
		}
	}
	for _, name := range []string{
		// A Context's values and deadline are read from the value passed.
		"Context.value", "Context.with_value", "context.Context.root", "context.Context.with_deadline",
		// Parsers and checks over their operands.
		"strings.String.is_upper", "signatures/host.public_from_pem", "http/host.parse_cookie",
		// A seeded generator's draw is a function of the seed.
		"random.between_state",
		// Pure, though each call's cost follows an operand's value.
		"strings.String.repeat", "decimal.Decimal.divide",
	} {
		if !m.pureCrossing(name) {
			t.Errorf("%s does not count as pure", name)
		}
	}
}

// TestUnboundedCrossing_ReadsTheDeclarations holds unboundedCrossing to the
// rows it reads: a stdlib adapter is unbounded exactly when its row is
// marked Unbounded, which only a Pure row is, and no intrinsic is.
func TestUnboundedCrossing_ReadsTheDeclarations(t *testing.T) {
	m := New(ir.NewModule("unbounded"), io.Discard)
	for name := range m.hosts {
		if m.unboundedCrossing(name) {
			t.Errorf("intrinsic %s counts as unbounded", name)
		}
	}
	funcs, err := stdlibadapters.Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	unbounded := 0
	for name := range funcs {
		if m.unboundedCrossing(name) != stdlibadapters.Unbounded(name) {
			t.Errorf("adapter %s: unboundedCrossing disagrees with its row", name)
		}
		if stdlibadapters.Unbounded(name) {
			unbounded++
			if !m.pureCrossing(name) {
				t.Errorf("adapter %s is unbounded and not pure", name)
			}
		}
	}
	for _, name := range []string{"strings.String.repeat", "decimal.Decimal.divide", "decimal.Decimal.pow", "hash.pbkdf2_sha256", "compress/host.decode"} {
		if !m.unboundedCrossing(name) {
			t.Errorf("%s does not count as unbounded", name)
		}
	}
	if unbounded == 0 {
		t.Fatal("no stdlib adapter is marked Unbounded")
	}
}

// TestSeededCrossing_ReadsTheDeclarations holds seededCrossing to the
// declarations it reads: an intrinsic is seeded exactly when its table
// entry says so, a stdlib adapter exactly when its row is marked Seeded,
// and each seeded crossing is pure.
func TestSeededCrossing_ReadsTheDeclarations(t *testing.T) {
	m := New(ir.NewModule("seeded"), io.Discard)
	for name, in := range m.hosts {
		if m.seededCrossing(name) != in.seeded {
			t.Errorf("intrinsic %s: seededCrossing says %v, its table entry %v", name, !in.seeded, in.seeded)
		}
		if in.seeded && !in.pure {
			t.Errorf("intrinsic %s is seeded and not pure", name)
		}
	}
	funcs, err := stdlibadapters.Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	for name := range funcs {
		if m.seededCrossing(name) != stdlibadapters.Seeded(name) {
			t.Errorf("adapter %s: seededCrossing disagrees with its row", name)
		}
		if stdlibadapters.Seeded(name) && !m.pureCrossing(name) {
			t.Errorf("adapter %s is seeded and not pure", name)
		}
	}
	for _, name := range []string{hashValueName, "strings.String.hash", "bytes.Bytes.hash", "decimal.Decimal.hash", "dynamic.Dynamic.hash"} {
		if !m.seededCrossing(name) {
			t.Errorf("%s does not count as seeded", name)
		}
	}
	for _, name := range []string{"Map.put", "Set.insert", "List.contains", "strings.String.repeat", "hash.digest_by", "hash.Hasher.update", "checksum.crc32_update"} {
		if m.seededCrossing(name) {
			t.Errorf("%s counts as seeded", name)
		}
	}
}

// TestCrossingName_NamesWhatTheProgramWrites holds the names a refusal
// gives the intrinsics it can name, each one an effect or seeded
// (crossingEffect), to what a program writes: no `vm.` key, which is the
// machine's own name, reaches a message. `vm.hash_value` is named
// `Hashable.hash`.
func TestCrossingName_NamesWhatTheProgramWrites(t *testing.T) {
	m := New(ir.NewModule("names"), io.Discard)
	named := 0
	for key := range m.hosts {
		call, kind := m.crossingEffect(key, "f")
		if kind == refuseNothing {
			continue
		}
		named++
		if strings.HasPrefix(strings.TrimPrefix(call, "f calls "), "vm.") {
			t.Errorf("a refusal names the intrinsic %s %q, the machine's own name", key, call)
		}
	}
	if named == 0 {
		t.Fatal("a refusal names no intrinsic")
	}
	if call, kind := m.crossingEffect(hashValueName, "f"); kind != refuseSeeded || call != "f calls Hashable.hash" {
		t.Errorf("a refusal names %s %q (%v), want %q, refused as seeded", hashValueName, call, kind, "f calls Hashable.hash")
	}
}
