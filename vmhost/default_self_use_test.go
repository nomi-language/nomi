package vmhost_test

import "testing"

// What an interface default can do with `self`, lowered once per implementor:
// pass it to a generic function, enter an interface type its `where self`
// names, use an operator that bound gives it, and run from a file other than
// the interface's.

// A default that passes `self` to a generic function instantiates it at the
// implementor: directly, piped, through a result, inside a lambda, and into a
// bounded parameter.
func TestDefaultSelf_IntoAGenericFunctionRuns(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

fn insp<T>(x: T) -> String {
    Debug.inspect(x)
}

fn same<T>(x: T) -> T {
    x
}

fn shown<T>(x: T) -> String where T: Display {
    Display.to_string(x)
}

interface Scale {
    fn label(scale: self) -> String

    fn direct(scale: self) -> String {
        "${label(scale)} ${insp(scale)}"
    }

    fn piped(scale: self) -> String {
        scale |> insp()
    }

    fn round(scale: self) -> String {
        label(same(scale))
    }

    fn lam(scale: self) -> String {
        [1, 2] |> Iter.map(fn n -> "${n}${insp(scale)}") |> String.join(",")
    }

    fn bounded(scale: self) -> String where self: Display {
        shown(scale)
    }
}

type Grams Int

impl Display for Grams {
    fn to_string(Grams(n)) -> String {
        "${n} g"
    }
}

impl Scale for Grams {
    fn label(_g: Grams) -> String {
        "grams"
    }
}

type Cups Int

impl Scale for Cups {
    fn label(_c: Cups) -> String {
        "cups"
    }

    fn bounded(_c: Cups) -> String {
        "no display"
    }
}

fn main() {
    io.print(Scale.direct(Grams(1)))
    io.print(Scale.piped(Cups(2)))
    io.print(Scale.round(Grams(3)))
    io.print(Scale.lam(Cups(4)))
    io.print(Scale.bounded(Grams(5)))
    io.print(Scale.bounded(Cups(6)))
}
`})
	if want := "grams Grams(1)\nCups(2)\ngrams\n1Cups(4),2Cups(4)\n5 g\nno display\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// Under `where self: Display` a `self` value conforms to Display: it enters a
// Display-typed parameter and a List<Display>, boxed at the implementor.
// `where self: Add<self, self>` gives `self` the operator.
func TestDefaultSelf_IntoAnInterfaceTypeRuns(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

fn shown(x: Display) -> String {
    Display.to_string(x)
}

interface Scale {
    fn label(scale: self) -> String

    fn format(scale: self) -> String where self: Display {
        "${label(scale)}=${shown(scale)}"
    }

    fn many(scale: self) -> String where self: Display {
        items: List<Display> = [scale, 1]
        items |> Iter.map(fn d -> Display.to_string(d)) |> String.join(",")
    }

    fn doubled(scale: self) -> String where self: Add<self, self> and Display {
        "${scale + scale}"
    }
}

type Grams Int

impl Display for Grams {
    fn to_string(Grams(n)) -> String {
        "${n} g"
    }
}

impl Add<Grams, Grams> for Grams {
    fn add(Grams(a), Grams(b)) -> Grams {
        Grams(a + b)
    }
}

impl Scale for Grams {
    fn label(_g: Grams) -> String {
        "grams"
    }
}

fn main() {
    io.print(Scale.format(Grams(250)))
    io.print(Scale.many(Grams(5)))
    io.print(Scale.doubled(Grams(2)))
}
`})
	if want := "grams=250 g\n5 g,1\n4 g\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// Only `self`, and only under the bound, conforms: a `self` with no bound,
// and another value of the interface's own type, stay errors.
func TestDefaultSelf_OnlyABoundedSelfEntersAnInterfaceType(t *testing.T) {
	const shown = "fn shown(x: Display) -> String {\n    Display.to_string(x)\n}\n\n"
	rejects(t, shown+`interface Scale {
    fn format(scale: self) -> String {
        shown(scale)
    }
}

fn main() {
    Unit
}
`, "main.nomi:7:15: argument 1: expected Display, got Scale")
	rejects(t, shown+`interface Scale {
    fn format(scale: self, other: Scale) -> String where self: Display {
        shown(other)
    }
}

fn main() {
    Unit
}
`, "main.nomi:7:15: argument 1: expected Display, got Scale")
	rejects(t, `interface Scale {
    fn doubled(scale: self) -> String where self: Display {
        "${scale + scale}"
    }
}

fn main() {
    Unit
}
`, "main.nomi:3:18: binary + operands must use a left-hand type that implements Add, got Scale")
}

// A default declared in one file runs for an implementor in a second, called
// from a third: interface-qualified, type-qualified, through a bounded type
// parameter and through an existential. The default's parameter is named
// after its own file, `scale`, which the implementing file imports, and the
// plain default calls its sibling `label` by its bare name.
func TestDefaultSelf_CrossFileDefaultRuns(t *testing.T) {
	main := `import {
    std/io
    grams.Grams
    scale.Scale
}

fn show<T>(x: T) -> String where T: Scale {
    T.format(x)
}

fn main() {
    io.print(Scale.format(Grams(1)))
    io.print(Grams.format(Grams(2)))
    io.print(show(Grams(3)))
    all: List<Scale> = [Grams(4)]
    all |> Iter.each(fn s -> io.print(Scale.format(s)))
}
`
	for _, tc := range []struct {
		name, scale, grams, want string
	}{
		{
			name: "plain default",
			scale: `pub interface Scale {
    fn label(scale: self) -> String

    fn format(scale: self) -> String {
        "[${label(scale)}]"
    }
}
`,
			grams: `import scale.Scale

pub type Grams Int

impl Scale for Grams {
    fn label(Grams(n)) -> String {
        "${n} g"
    }
}
`,
			want: "[1 g]\n[2 g]\n[3 g]\n[4 g]\n",
		},
		{
			name: "where self",
			scale: `pub interface Scale {
    fn label(scale: self) -> String

    fn format(scale: self) -> String where self: Display {
        "${label(scale)}=${scale}"
    }
}
`,
			grams: `import scale.Scale

pub type Grams Int

impl Display for Grams {
    fn to_string(Grams(n)) -> String {
        "${n} g"
    }
}

impl Scale for Grams {
    fn label(_g: Grams) -> String {
        "grams"
    }
}
`,
			want: "grams=1 g\ngrams=2 g\ngrams=3 g\ngrams=4 g\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runSiblingProgram(t, map[string]string{"scale.nomi": tc.scale, "grams.nomi": tc.grams, "main.nomi": main})
			if got != tc.want {
				t.Fatalf("output %q, want %q", got, tc.want)
			}
		})
	}
}
