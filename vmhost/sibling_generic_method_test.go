package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// siblingGenericLeaf declares impl functions with their own type parameters,
// which the programs below call from another file.
const siblingGenericLeaf = `pub struct Box {
    value: Int
}

impl Box {
    pub fn ident<T>(x: T): T {
        x
    }

    pub fn pick<T>(c: Bool, a: T, b: T): T {
        if c { a } else { b }
    }

    pub fn pad<T>(x: T, n: Int = 2): (T, Int) {
        (x, n)
    }

    pub fn twice<T>(x: T): (T, T) {
        (Box.ident(x), Box.ident(x))
    }
}

pub struct Wrap<T> {
    v: T
}

impl<T> Wrap<T> {
    pub fn map<U>(w: Wrap<T>, f: (T) -> U): Wrap<U> {
        Wrap { v: f(w.v) }
    }
}

pub interface Pairs {
    fn pair<U>(b: self, u: U): (self, U)

    fn second<U>(_b: self, u: U): U {
        u
    }
}

impl Pairs for Box {
    fn pair<U>(b: Box, u: U): (Box, U) {
        (b, u)
    }
}
`

// runSiblingProgram writes files into a module whose entry is main.nomi,
// checks and runs it, and returns what it printed.
func runSiblingProgram(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["nomi.toml"] = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "main.nomi")
	if err := vmhost.Check(path); err != nil {
		t.Fatalf("check: %v", err)
	}
	prog, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out bytes.Buffer
	if err := prog.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String()
}

// A generic impl function another file declares runs from this one, as it
// does in its own file: the declaring file builds each instance. Called
// through the file qualifier and through an imported owner, on a generic
// owner's instance, as an interface impl's function and an inherited
// interface default, with a default omitted, from a generic caller, as a
// value and partially applied.
func TestSiblingGenericImplFunction_Runs(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			name: "file-qualified call",
			body: "    io.inspect(leaf.Box.ident(6))\n",
			want: "6\n",
		},
		{
			name: "imported owner",
			body: "    io.inspect(Box.ident(\"s\"))\n    io.inspect(Box.pick(False, 1, 2))\n",
			want: "\"s\"\n2\n",
		},
		{
			name: "generic owner instance",
			body: "    w = Wrap { v: 3 }\n    io.inspect(Wrap.map(w, |n| n + 1))\n    io.inspect(leaf.Wrap.map(w, |n| \"${n}\"))\n",
			want: "Wrap{v: 4}\nWrap{v: \"3\"}\n",
		},
		{
			name: "interface impl function",
			body: "    io.inspect(Pairs.pair(Box { value: 1 }, \"x\"))\n    io.inspect(leaf.Box.pair(Box { value: 1 }, 2))\n",
			want: "(Box{value: 1}, \"x\")\n(Box{value: 1}, 2)\n",
		},
		{
			name: "inherited interface default",
			body: "    io.inspect(Pairs.second(Box { value: 1 }, 2.5))\n",
			want: "2.5\n",
		},
		{
			name: "omitted default",
			body: "    io.inspect(Box.pad(\"p\"))\n    io.inspect(leaf.Box.pad(n: 5, x: True))\n",
			want: "(\"p\", 2)\n(True, 5)\n",
		},
		{
			name: "member calling a member",
			body: "    io.inspect(Box.twice(Wrap { v: 1 }))\n",
			want: "(Wrap{v: 1}, Wrap{v: 1})\n",
		},
		{
			name: "generic caller",
			body: "    io.inspect(both(1))\n    io.inspect(both(\"a\"))\n",
			want: "(1, 1)\n(\"a\", \"a\")\n",
		},
		{
			name: "value",
			body: "    f: (Int) -> Int = leaf.Box.ident\n    io.inspect(f(7))\n    io.inspect([1, 2] |> Iter.map(Box.ident) |> Iter.to_list())\n" +
				"    m: (Wrap<Int>, (Int) -> Bool) -> Wrap<Bool> = Wrap.map\n    io.inspect(m(Wrap { v: 3 }, |n| n > 2))\n",
			want: "7\n[1, 2]\nWrap{v: True}\n",
		},
		{
			name: "partial application",
			body: "    h: (Int) -> Int = Box.pick(True, _, 4)\n    io.inspect(h(9))\n    k = leaf.Box.pick(False, \"a\", _)\n    io.inspect(k(\"b\"))\n",
			want: "9\n\"b\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main := "import {\n    std/io\n    leaf\n    leaf.{Box, Wrap, Pairs}\n}\n\n" +
				"fn uses<P>(x: P): P where P: Pairs {\n    x\n}\n\n" +
				"fn both<T>(x: T): (T, T) {\n    leaf.Box.twice(x)\n}\n\n" +
				"fn main() {\n    _ = uses(Box { value: 0 })\n    _ = both(Wrap { v: 0 })\n" + tc.body + "}\n"
			got := runSiblingProgram(t, map[string]string{"leaf.nomi": siblingGenericLeaf, "main.nomi": main})
			if got != tc.want {
				t.Fatalf("output %q, want %q", got, tc.want)
			}
		})
	}
}

// Two files in an import cycle call each other's generic impl functions, and
// a third calls both.
func TestSiblingGenericImplFunction_ImportCycle(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{
		"a.nomi": `import b.{Pod}

pub struct Cell<T> {
    v: T
}

impl<T> Cell<T> {
    pub fn pair<U>(c: Cell<T>, u: U): Pod<(T, U)> {
        Pod { item: (c.v, u) }
    }
}

pub fn round(c: Cell<Int>): Cell<(Int, String)> {
    Pod.cell(Pod { item: c.v }, "r")
}
`,
		"b.nomi": `import a.{Cell}

pub struct Pod<T> {
    item: T
}

impl<T> Pod<T> {
    pub fn cell<U>(p: Pod<T>, u: U): Cell<(T, U)> {
        Cell { v: (p.item, u) }
    }
}

pub fn back(p: Pod<Int>): Pod<(Int, Bool)> {
    Cell.pair(Cell { v: p.item }, True)
}
`,
		"main.nomi": `import {
    std/io
    a
    a.{Cell}
    b
    b.{Pod}
}

fn main() {
    c = Cell { v: 1 }
    p = Pod { item: 2 }
    io.inspect(Pod.cell(p, True))
    io.inspect(Cell.pair(c, "x"))
    io.inspect(a.round(c))
    io.inspect(b.back(p))
}
`,
	})
	want := "Cell{v: (2, True)}\nPod{item: (1, \"x\")}\nCell{v: (1, \"r\")}\nPod{item: (2, True)}\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A std owner's function the VM owns (`List.head`, `Iter.find`), called or
// named as a value through its module's qualifier, runs as the unqualified
// spelling does.
func TestStdModuleQualifiedOwnFamily_Runs(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import {
    std/io
    std/iter
    std/lists
    std/maps
    std/maybe
}

fn main() {
    io.inspect(lists.List.head([4, 5]))
    io.inspect(iter.Iter.find([1, 2, 3], |x| x > 1))
    io.inspect(maps.Map.get({1 => "z"}, 1))
    io.inspect(maybe.Maybe.with_default(None, 3))
    f: (List<String>) -> Maybe<String> = lists.List.head
    io.inspect(f(["a"]))
    g: (Maybe<Int>, (Int) -> Int) -> Maybe<Int> = maybe.Maybe.map
    io.inspect(g(Some(2), |x| x * 3))
}
`})
	want := "Some(4)\nSome(2)\nSome(\"z\")\n3\nSome(\"a\")\nSome(6)\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
