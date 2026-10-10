package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A user generic enum's list- and map-payload variants build and match in
// literal-attach form in every spelling: qualified (`shapes.Bag.Items[1,
// 2]`), same-file (`Bag.Items[1, 2]`), imported by name (`Items[3, 4]`),
// aliased (`I[6]`), empty under an expected type (`Items[]`) and nested.
// The literal lowered as a call with no type of its own, so a generic
// enum's instance was unknown and it declined; an empty payload declined
// in the imported-variant call. Bare list and map patterns of an imported
// variant (`Items[x, y]`, `N{"k" => v}`) match.
func TestAttachedGenericVariant_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import std/io
import shapes
import shapes.Bag.{Items, Named}
import shapes.Bag.{Items as I, Named as N}

fn first(b: shapes.Bag<Int>): String {
    case b {
        Items[x, y] -> "items ${x} ${y}"
        Named{"k" => v} -> "named ${v}"
        I[x] -> "one ${x}"
        _ -> "other"
    }
}

fn label(b: shapes.Bag<String>): String {
    case b {
        shapes.Bag.Items[s, .._] -> s
        N{"k" => v} -> v
        _ -> "-"
    }
}

fn main() {
    io.print(first(shapes.Bag.Items[1, 2]))
    io.print(first(shapes.Bag.Named{"k" => 4}))
    io.print(first(Items[3, 4]))
    io.print(first(Named{"k" => 5}))
    io.print(first(I[6]))
    io.print(first(N{"k" => 7}))
    g: shapes.Bag<Int> = Items[]
    io.print(first(g))
    io.print(Int.to_string(shapes.count(Items[])))
    io.print(Int.to_string(shapes.count(Items[1, 2, 3])))
    io.print(label(Items["s", "u"]))
    io.print(label(N{"k" => "t"}))
    nested = Items[[1], [2, 3]]
    io.print(Debug.inspect(nested))
    io.print(Debug.inspect(Named{"a" => Items["x"]}))
    io.print(shapes.local())
}
`,
		"shapes.nomi": `pub enum Bag<T> {
    Items List<T>
    Named Map<String, T>
    Nothing
}

pub fn count(b: Bag<Int>): Int {
    case b {
        .Items(xs) -> Iter.count(xs)
        .Named(m) -> Iter.count(m)
        .Nothing -> 0
    }
}

pub fn local(): String {
    a = Bag.Items[1, 2]
    b: Bag<String> = Bag.Named{"k" => "v"}
    e: Bag<Int> = Bag.Items[]
    x = case a {
        Bag.Items[p, q] -> p + q
        _ -> 0
    }
    y = case b {
        Bag.Named{"k" => v} -> v
        _ -> ""
    }
    "${x} ${y} ${count(e)}"
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
	const want = "items 1 2\nnamed 4\nitems 3 4\nnamed 5\none 6\nnamed 7\nother\n0\n3\ns\nt\n" +
		"Items([[1], [2, 3]])\nNamed({\"a\" => Items([\"x\"])})\n3 v 0\n"
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", got.exit, got.stdout, got.stderr)
	}
}
