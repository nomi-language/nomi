package vmhost_test

import "testing"

// An interface default runs once per implementor with `self` the
// implementor's type, so a default may return `self`, a composite holding
// it, or take further `self` values, and may call a required function that
// has no `self` parameter. Each runs through the implementing type, through
// a type parameter bounded by the interface, and interface-qualified on a
// concrete value.
func TestDefaultSelfResult_Runs(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

interface Sized {
    fn size(x: self): Int
    fn make(n: Int): self

    fn same(x: self): self {
        x
    }

    fn pair(x: self): (self, self) {
        (x, Sized.grown(x))
    }

    fn twice(x: self, y: self): List<self> {
        [x, y]
    }

    fn grown(x: self): self {
        Sized.make(Sized.size(x) + 1)
    }

    fn zero(): self {
        Sized.make(0)
    }

    fn shown(x: self): self where self: Display {
        io.print("showing ${x}")
        x
    }
}

struct Pot {
    n: Int
}

impl Sized for Pot {
    fn size(p: Pot): Int {
        p.n
    }

    fn make(n: Int): Pot {
        Pot { n: n }
    }
}

impl Display for Pot {
    fn to_string(p: Pot): String {
        "pot ${p.n}"
    }
}

struct Word {
    text: String
}

impl Sized for Word {
    fn size(w: Word): Int {
        String.length(w.text)
    }

    fn make(n: Int): Word {
        Word { text: Int.to_string(n) }
    }
}

impl Display for Word {
    fn to_string(w: Word): String {
        "word ${w.text}"
    }
}

fn total<T>(t: T): Int where T: Sized {
    (a, b) = T.pair(T.same(t))
    sizes = T.twice(a, b) |> Iter.map(|x| Sized.size(x)) |> Iter.to_list()
    Sized.size(T.grown(a)) + Sized.size(b) + Iter.count(sizes)
}

fn main() {
    io.print(Pot.same(Pot { n: 3 }).n)
    (a, b) = Pot.pair(Pot { n: 3 })
    io.print("${a.n} ${b.n}")
    io.print(Pot.twice(a, b) |> Iter.map(|p| p.n) |> Iter.to_list())
    io.print(Pot.zero().n)
    io.print(Word.zero().text)
    io.print(Word.grown(Word { text: "abc" }).text)
    io.print(Sized.same(Pot { n: 7 }).n)
    io.print(total(Pot { n: 1 }))
    io.print(total(Word { text: "hello" }))
    io.print(Pot.shown(Pot { n: 9 }).n)
}
`})
	want := "3\n3 4\n[3, 4]\n0\n0\n4\n7\n6\n4\nshowing pot 9\n9\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// The same defaults declared in another file run for an implementor here.
func TestDefaultSelfResult_RunsFromAnotherFile(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{
		"shapes.nomi": `pub interface Grow {
    fn size(g: self): Int
    fn make(n: Int): self

    fn grown(g: self): self {
        Grow.make(Grow.size(g) + 1)
    }

    fn both(g: self): (self, self) {
        (g, Grow.grown(g))
    }

    fn zero(): self {
        Grow.make(0)
    }
}
`,
		"main.nomi": `import std/io
import shapes.Grow

struct Pot {
    n: Int
}

impl Grow for Pot {
    fn size(p: Pot): Int {
        p.n
    }

    fn make(n: Int): Pot {
        Pot { n: n }
    }
}

fn bigger<T>(t: T): Int where T: Grow {
    (a, b) = T.both(t)
    Grow.size(a) + Grow.size(b)
}

fn main() {
    io.print(Pot.grown(Pot { n: 1 }).n)
    io.print(bigger(Pot { n: 5 }))
    io.print(Pot.zero().n)
}
`})
	if want := "2\n11\n0\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A value of interface type names no implementor, so an interface-qualified
// call on one is an error when the signature needs the implementor's own
// type beyond the receiver, and so is a call of a function with no `self`
// parameter that nothing else names an implementor for. The hint's type
// parameter form is admitted.
func TestDefaultSelfResult_ExistentialCallIsAnError(t *testing.T) {
	const iface = `interface Sized {
    fn size(x: self): Int
    fn make(n: Int): self

    fn same(x: self): self {
        x
    }

    fn pair(x: self): (self, self) {
        (x, x)
    }

    fn both(x: self, y: self): Int {
        Sized.size(x) + Sized.size(y)
    }

    fn sum(x: self, xs: List<self>): Int {
        Sized.size(x) + Iter.count(xs)
    }
}

`
	for _, tc := range []struct {
		name, bad, good, want, help string
	}{
		{
			name: "self result",
			bad:  "fn f(e: Sized): Int {\n    Sized.size(Sized.same(e))\n}\n",
			good: "fn f<T>(e: T): Int where T: Sized {\n    Sized.size(Sized.same(e))\n}\n",
			want: "main.nomi:25:27: `Sized.same` cannot be called on `e`, a `Sized`: its result is `self`, the implementor's own type, and a `Sized` value does not say which implementor it holds",
			help: "take a type parameter bounded by Sized instead, so the implementor is known: `fn f<T>(e: T): Int where T: Sized`",
		},
		{
			name: "piped self result",
			bad:  "fn f(e: Sized): Int {\n    e |> Sized.same() |> Sized.size()\n}\n",
			good: "fn f<T>(e: T): Int where T: Sized {\n    e |> Sized.same() |> Sized.size()\n}\n",
			want: "main.nomi:25:5: `Sized.same` cannot be called on `e`, a `Sized`: its result is `self`",
			help: "`fn f<T>(e: T): Int where T: Sized`",
		},
		{
			name: "self inside the result",
			bad:  "fn f(e: Sized): Int {\n    (a, _) = Sized.pair(e)\n    Sized.size(a)\n}\n",
			good: "fn f<T>(e: T): Int where T: Sized {\n    (a, _) = Sized.pair(e)\n    Sized.size(a)\n}\n",
			want: "main.nomi:25:25: `Sized.pair` cannot be called on `e`, a `Sized`: its result is `self`",
			help: "`fn f<T>(e: T): Int where T: Sized`",
		},
		{
			name: "two self arguments",
			bad:  "fn f(a: Sized, b: Sized): Int {\n    Sized.both(a, b)\n}\n",
			good: "fn f<T>(a: T, b: T): Int where T: Sized {\n    Sized.both(a, b)\n}\n",
			want: "main.nomi:25:16: `Sized.both` cannot be called on `a`, a `Sized`: its 2 `self` arguments must be one implementor, and `Sized` values may hold different ones",
			help: "`fn f<T>(a: T, b: T): Int where T: Sized`",
		},
		{
			name: "self inside a parameter",
			bad:  "fn f(e: Sized): Int {\n    Sized.sum(e, [])\n}\n",
			good: "fn f<T>(e: T): Int where T: Sized {\n    Sized.sum(e, [])\n}\n",
			want: "main.nomi:25:15: `Sized.sum` cannot be called on `e`, a `Sized`: its parameter `xs` holds `self` values, which must be the implementor's own type",
			help: "`fn f<T>(e: T): Int where T: Sized`",
		},
		{
			name: "no self parameter",
			bad:  "fn f(): Int {\n    Sized.size(Sized.make(1))\n}\n",
			good: "fn f<T>(_witness: T): Int where T: Sized {\n    Sized.size(T.make(1))\n}\n",
			want: "main.nomi:25:16: `Sized.make` has no `self` parameter, so this call does not say which implementor of Sized runs",
			help: "call it on the implementing type (`Type.make(...)`), or on a type parameter bounded by Sized (`T.make(...)` with `where T: Sized`)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := func(decl string) string {
				return "import std/io\n\n" + iface + decl + "\nfn main() {\n    io.print(1)\n}\n"
			}
			rejects(t, src(tc.bad), tc.want, tc.help)
			admits(t, src(tc.good))
		})
	}
}
