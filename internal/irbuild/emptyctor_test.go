package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// The empty literals of a generic body (`#[]`, `%[]`, `%{}` in a function
// over T) lower and run wherever the call's own type is not what the context
// asks for: as an `Iter` source, in a pipe. The empty literal the builder
// emits feeds a retyping Copy, and both temporaries carry a stored type
// (`ir.Lint`).
func TestIREmptyCtor_GenericEmptyLiteralsRun(t *testing.T) {
	src := `import std/io

struct P {
  x: Int
}

fn vec<T>() -> Vector<T> {
  #[]
}

fn set<T>() -> Set<T> {
  %[]
}

fn map<K, V>() -> Map<K, V> {
  %{}
}

fn main() {
  io.inspect(Iter.to_list(vec<Int>()))
  io.inspect(vec<String>())
  io.inspect(Iter.count(vec<P>()))
  io.inspect(vec<Int>() |> Vector.push(1))
  io.inspect(Iter.to_list(set<Int>()))
  io.inspect(set<String>() |> Set.insert("a"))
  io.inspect(Iter.to_list(map<String, Int>()))
  v: Vector<Int> = #[]
  io.inspect(Iter.to_list(v))
}
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
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
	var entry *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			entry = m
		}
	}
	if entry == nil {
		t.Fatal("no module for the program")
	}
	var out bytes.Buffer
	booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := booted.Run("main"); err != nil {
		t.Fatal(err)
	}
	const want = `[]
#[]
0
#[1]
[]
%["a"]
[]
[]
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}
