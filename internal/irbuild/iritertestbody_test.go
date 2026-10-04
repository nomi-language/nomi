package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// `Iter` calls and callbacks inside test bodies the builder reads back. Rows
// are recorded after an `Iter.` call from its lowered operands, as
// `gen.iterCall` records them, and a call through a function value records its
// arguments too. Some cases fail on purpose, so the report text itself is
// compared between the VM and the golden record.
const irIterTestBodySource = `fn double(n: Int): Int {
  n * 2
}

test "a count in a subject" {
  xs = [1, 2, 3]
  assert Iter.count(xs) == 3
}

test "a pipeline bound before the assertion" {
  total = [1, 2, 3, 4] |> Iter.filter(|x| x % 2 == 0) |> Iter.reduce(|a = 0, x| a + x)
  assert total == 6
}

test "a predicate call records its source" {
  xs = [2, 4, 5]
  assert Iter.all?(xs, |x| x % 2 == 0)
}

test "a multi-line call leaves the cursor on its last line" {
  assert double(
    3
  ) == 7
}

test "a call through a function value shows its argument" {
  small? = |n: Int| n > 90
  w = 5
  assert small?(w)
}

test "a folded count in a subject" {
  assert Iter.count(
    [1, 2] |> Iter.map(|x| x + 1)
  ) == 2
}
`

func TestIRIterTestBody_MatchesItsGoldenRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iter_test.nomi")
	if err := os.WriteFile(path, []byte(irIterTestBodySource), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != 6 {
		t.Fatalf("retained %v; want all 6 cases", module)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	interp := goldenReference(t, path)
	if interp.exit == 0 {
		t.Fatal("no case failed, so the failure reports are not compared")
	}
	if out.String() != interp.stdout || exit != interp.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			exit, out.String(), interp.exit, interp.stdout)
	}
}
