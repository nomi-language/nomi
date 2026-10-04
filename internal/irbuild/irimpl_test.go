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

// TestIRImpl_CompleteProgramRunsOnTheVM retains every impl body
// of a program and requires the expected output from the VM, both in process
// and through the `nomi` command.
func TestIRImpl_CompleteProgramRunsOnTheVM(t *testing.T) {
	const src = `import std/io

struct Left { n: Int }
struct Right { n: Int }

impl Left {
  fn read(p: Left): Int { p.n + 1 }
  fn label(p: Left): String { "left ${Left.read(p)}" }
}
impl Right {
  fn read(p: Right): Int { p.n + 10 }
}

fn main() {
  io.print(Left.label(Left{n: 2}))
  io.print(Right.read(Right{n: 2}))
}
`
	const want = "left 3\n12\n"
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	var c irFuncRetentionCount
	restore := c.observe(t, irFromImpl)
	defer restore()
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	// The three source bodies and the two auto-synthesized Debug bodies are
	// all read back.
	if len(c.readBack) != 5 || len(c.unreadSource) != 0 {
		t.Fatalf("impl bodies retained %v, read back %v; want all five bodies", c.retained, c.readBack)
	}
	var entry *ir.Module
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				entry = m
			}
		}
	}
	if entry == nil {
		t.Fatal("the program's main was not retained")
	}
	var out bytes.Buffer
	if _, err := vm.NewProgram(entry, res.IRModules(), &out).Run("main"); err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Fatalf("VM output %q; want %q", out.String(), want)
	}
	if got := vmReference(path); got.exit != 0 || got.stdout != want || got.stderr != "" {
		t.Fatalf("vm command: %s; want %q", got, want)
	}
}

func TestIRImpl_SpecializationsHaveDistinctDeclarations(t *testing.T) {
	const src = `import std/io
interface Read { fn read(p: self): Int }
struct Box<T> {
  item: T
  n: Int
}

impl Read for Box<T> {
  fn read(_p: Box<T>): Int { 7 }
}
fn main() {
  io.print(Read.read(Box{item: 1, n: 3}))
  io.print(Read.read(Box{item: "x", n: 7}))
}
`
	var funcs []*ir.Func
	prev := irFuncObserved
	irFuncObserved = func(origin irFuncOrigin, name string, f *ir.Func, read bool) {
		// Read's specializations only: each Box instance also retains its
		// synthesized Debug.
		if origin == irFromImpl && f != nil && strings.HasSuffix(name, ".read") {
			if !read {
				t.Error("retained specialization did not read back")
			}
			funcs = append(funcs, f)
		}
	}
	defer func() { irFuncObserved = prev }()
	lowerIROnly(t, src)
	if len(funcs) != 2 {
		t.Fatalf("retained %d specialized impl bodies; want 2", len(funcs))
	}
	if funcs[0].Sym() == funcs[1].Sym() {
		t.Fatal("two receiver specializations share a declaration identity")
	}
}
