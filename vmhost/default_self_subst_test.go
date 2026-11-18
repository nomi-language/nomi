package vmhost_test

import "testing"

// Inside an interface default the checker records `self` in terms of the
// default: a call's For names the interface's self parameter, and the type
// of a `self` value (an operand, a call's type argument, a hole's
// rendering) is the interface marked as the default's self. Lowered for one
// implementor, each is the implementor's type: these run what reads each
// record at the instance.

// `self` enters a generic function that calls an interface function on its
// parameter, a list of `self` enters one, and the default renders `self` by
// Debug, compares it with `==` and calls a sibling default on it.
func TestDefaultSelf_RecordsAtTheImplementorRun(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std::io

fn named<T>(x: T) -> String where T: Scale
    Scale.label(x)
end

fn count<T>(xs: List<T>) -> String
    xs |> Iter.map(fn x -> Debug.inspect(x)) |> String.join("+")
end

interface Scale
    fn label(scale: self) -> String

    fn via(scale: self) -> String
        named(scale)
    end

    fn listed(scale: self) -> String
        count([scale, scale])
    end

    fn seen(scale: self) -> String
        "${Debug.inspect(scale)} ${scale == scale}"
    end

    fn both(scale: self) -> String
        "${Scale.via(scale)}/${seen(scale)}"
    end
end

type Grams Int

impl Scale for Grams
    fn label(_g: Grams) -> String
        "grams"
    end
end

fn main()
    io.print(Scale.via(Grams(1)))
    io.print(Scale.listed(Grams(2)))
    io.print(Scale.seen(Grams(3)))
    io.print(Scale.both(Grams(4)))
end
`})
	if want := "grams\nGrams(2)+Grams(2)\nGrams(3) True\ngrams/Grams(4) True\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// Under `where self: Display`, a hole of `self` and of a list of `self`, and
// `self` into a generic function bounded by Display, render by the
// implementor's impl, with the interface in one file and the implementor in
// another.
func TestDefaultSelf_RecordsAtTheImplementorRunAcrossFiles(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{
		"scale.nomi": `pub fn shown<T>(x: T) -> String where T: Display
    Display.to_string(x)
end

pub interface Scale
    fn label(scale: self) -> String

    fn format(scale: self) -> String where self: Display
        "${label(scale)} ${scale} ${[scale]} ${shown(scale)} ${Debug.inspect(scale)}"
    end
end
`,
		"grams.nomi": `import scale.Scale

pub type Grams Int

impl Display for Grams
    fn to_string(Grams(n)) -> String
        "${n} g"
    end
end

impl Scale for Grams
    fn label(_g: Grams) -> String
        "grams"
    end
end
`,
		"main.nomi": `import
    std::io
    grams.Grams
    scale.Scale
end

fn main()
    io.print(Scale.format(Grams(7)))
end
`,
	})
	if want := "grams 7 g [7 g] 7 g Grams(7)\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A generic interface's default passes `self` to a generic function and
// renders it, at each instance of the interface an implementor has.
func TestDefaultSelf_GenericInterfaceRecordsAtTheImplementorRun(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std::io

fn insp<T>(x: T) -> String
    Debug.inspect(x)
end

interface Holds<T>
    fn item(h: self) -> T

    fn show(h: self) -> String where T: Display
        "${insp(h)} ${Holds.item(h)} ${h == h}"
    end
end

struct Box
    n: Int
end

impl Holds<Int> for Box
    fn item(b: Box) -> Int
        b.n
    end
end

fn main()
    io.print(Holds.show(Box{n: 3}))
end
`})
	if want := "Box{n: 3} 3 True\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
