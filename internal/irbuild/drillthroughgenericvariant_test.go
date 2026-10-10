package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A user generic enum's variants imported by name from another file
// (`import shapes.Holder.{Of as O, Wrap as W, Empty}`) build, match and
// pass as function values as the qualified spellings do. The builder
// resolved a bare imported variant only for a std enum: the struct literal
// declined, and the positional call lowered its callee as a function value
// whose body was the same call, which recursed until the stack overflowed.
// An aliased bare pattern declined because the head was read by its local
// spelling. The variant imported into the module's other file runs too, and
// so does a non-generic enum's positional variant, which declined.
func TestDrillThroughGenericVariant_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import std/io
import same
import shapes
import shapes.Holder.{Of as O, Wrap as W, Empty}
import shapes.Pair.{Both}
import shapes.Shape.{Sq}

fn show(h: shapes.Holder<Int>): String {
    case h {
        O{value} -> "of ${value}"
        W(v) -> "wrap ${v}"
        Empty -> "empty"
    }
}

fn main() {
    io.print(show(O{value: 1}))
    io.print(show(W(2)))
    io.print(show(Empty))
    e: shapes.Holder<String> = Empty
    io.print(Debug.inspect(e))
    ws = [3, 4] |> Iter.map(W) |> Iter.map(show) |> Iter.to_list()
    io.print(String.join(ws, ","))
    p = Both{left: "x", right: 5}
    io.print(Debug.inspect(p))
    io.print(Int.to_string(same.make()))
    io.print(Debug.inspect(Sq(9)))
}
`,
		"same.nomi": `import shapes.Holder.{Of}

pub fn make(): Int {
    h = Of{value: 8}
    case h {
        Of{value} -> value
        _ -> 0
    }
}
`,
		"shapes.nomi": `pub enum Holder<T> {
    Of {value: T}
    Wrap T
    Empty
}

pub enum Pair<A, B> {
    Both {left: A, right: B}
}

pub enum Shape {
    Sq Int
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
	const want = "of 1\nwrap 2\nempty\nEmpty\nwrap 3,wrap 4\nBoth{left: \"x\", right: 5}\n8\nSq(9)\n"
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", got.exit, got.stdout, got.stderr)
	}
}
