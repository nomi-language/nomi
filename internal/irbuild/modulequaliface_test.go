package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A function of an interface named through the module that declares it runs
// as the bare imported name's call does: a program module's interface
// (`shapes.Shape.area(s)`) and its default (`shapes.Shape.label(s)`), through
// an alias (`sh.Shape`), over an existential and over a concrete receiver, as
// a capture, beside a local interface of the same name; a std module lowered
// with the program (`storage.Store.exists`, a default, and `sql.Conn.dialect`);
// and a shared std module's (`matcher.Matcher.is_contained_in`). The owner
// `shapes.Shape` named no type mirror, so the call was read as a call through
// a value and declined at the module qualifier.
func TestModuleQualifiedInterfaceCall_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import std::io
import std::matcher
import std::sql
import std::sql_testing.Fake
import std::storage as st
import std::storage_testing.Memory
import shapes
import shapes.Square

interface Shape {
    fn area(s: self) -> Int
}

struct Dot {}

impl Shape for Dot {
    fn area(_: Dot) -> Int {
        0
    }
}

fn area_of(s: shapes.Shape) -> Int {
    shapes.Shape.area(s)
}

fn main() {
    sq: shapes.Shape = Square{side: 3}
    io.print("area ${shapes.Shape.area(sq)}")
    io.print("label ${shapes.Shape.label(sq)}")
    io.print("direct ${shapes.Shape.area(Square{side: 4})}")
    io.print("label direct ${shapes.Shape.label(Square{side: 2})}")
    io.print("fn ${area_of(Square{side: 5})}")
    io.print("mapped ${[sq] |> Iter.map(&shapes.Shape.area(&1)) |> Iter.to_list()}")
    io.print("local ${Shape.area(Dot{})}")
    s: st.Store = Memory.new()
    io.print("exists ${st.Store.exists(s, "x")}")
    io.print("put ${Debug.inspect(st.Store.put(s, "x", Bytes.from_list([]), "text/plain"))}")
    io.print("exists ${st.Store.exists(s, "x")}")
    io.print("exists memory ${st.Store.exists(Memory.new(), "y")}")
    c: sql.Conn = Fake.new()
    io.print("dialect ${sql.Conn.dialect(c)}")
    io.print("contained ${matcher.Matcher.is_contained_in("an", "banana")}")
}
`,
		"shapes.nomi": `pub interface Shape {
    fn area(s: self) -> Int

    fn label(s: self) -> String {
        "shape of ${area(s)}"
    }
}

pub struct Square {
    side: Int
}

impl Shape for Square {
    fn area(s: Square) -> Int {
        s.side * s.side
    }
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = "area 9\nlabel shape of 9\ndirect 16\nlabel direct shape of 4\nfn 25\nmapped [9]\nlocal 0\n" +
		"exists Ok(False)\nput Ok(Unit)\nexists Ok(True)\nexists memory Ok(False)\ndialect fake\ncontained True\n"
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
