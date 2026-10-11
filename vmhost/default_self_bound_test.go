package vmhost_test

import "testing"

// An interface default runs once per implementor, so what its body needs of
// `self` must hold for each implementor that gets it. `where self: X` on the
// default says so: an implementor with X gets the default, and one without
// it writes the function itself.

const defaultSelfBoundScale = `interface Scale {
    fn label(scale: self): String

    fn format(scale: self): String where self: Display {
        "${label(scale)}=${scale}"
    }
}
`

func TestDefaultSelfBound_Runs(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": "import std/io\n\n" + defaultSelfBoundScale + `
type Shown Int

impl Display for Shown {
    fn to_string(_s: Shown): String {
        "shown"
    }
}

impl Scale for Shown {
    fn label(_s: Shown): String {
        "a"
    }
}

type Plain Int

impl Scale for Plain {
    fn label(_p: Plain): String {
        "b"
    }

    fn format(_p: Plain): String {
        "plain"
    }
}

fn show<T>(x: T): String where T: Scale {
    T.format(x)
}

fn main() {
    io.print(Scale.format(Shown(1)))
    io.print(Shown.format(Shown(1)))
    io.print(Scale.format(Plain(2)))
    io.print(show(Shown(3)))
    io.print(show(Plain(4)))
    all: List<Scale> = [Shown(5), Plain(6)]
    all |> Iter.each(|s| io.print(Scale.format(s)))
}
`})
	if want := "a=shown\na=shown\nplain\na=shown\nplain\na=shown\nplain\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// Inside the default, each bound in `where self: A and B` holds of `self`:
// interpolation, a call, a list of `self` and an ordering operator.
func TestDefaultSelfBound_SeveralBoundsRun(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

interface Scale {
    fn c(scale: self): String where self: Display and Hashable and Comparable {
        "${scale} ${Hashable.hash(scale)} ${[scale]} ${scale < scale}"
    }
}

type Shown Int

impl Display for Shown {
    fn to_string(Shown(n)): String {
        "s${n}"
    }
}

impl Hashable for Shown {
    fn hash(Shown(n)): Int {
        n
    }
}

derive Equatable for Shown
derive Comparable for Shown

impl Scale for Shown

fn main() {
    io.print(Scale.c(Shown(3)))
}
`})
	if want := "s3 3 [s3] False\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// A default that uses only its own interface's functions and Debug needs no
// bound, and a marker interface still bounds a generic function.
func TestDefaultSelfBound_UnboundedDefaultsStayAdmitted(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

interface Named {
    fn name(n: self): String

    fn greet(n: self): String {
        "hi ${Named.name(n)} ${Debug.inspect(n)} ${n == n}"
    }
}

interface Marker {}

type Bare Int

impl Named for Bare {
    fn name(_b: Bare): String {
        "bare"
    }
}

impl Marker for Bare

fn marked<T>(x: T): String where T: Marker and Named {
    T.greet(x)
}

fn main() {
    io.print(Named.greet(Bare(1)))
    io.print(marked(Bare(2)))
}
`})
	if want := "hi bare Bare(1) True\nhi bare Bare(2) True\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

func TestDefaultSelfBound_ImplWithoutTheBoundMustWriteTheFunction(t *testing.T) {
	rejects(t, "import std/io\n\n"+defaultSelfBoundScale+`
type Plain Int

impl Scale for Plain {
    fn label(_p: Plain): String {
        "b"
    }
}

fn main() {
    io.print(Scale.format(Plain(2)))
}
`,
		"main.nomi:13:1: `Plain` does not implement Display, which `Scale.format`'s default requires (`where self: Display`)",
		"implement Display for Plain, or write `fn format` in this impl")
}

// An implementor that has the bound gets the default, and the default is
// final unless it is `open`.
func TestDefaultSelfBound_ImplWithTheBoundCannotOverrideAFinalDefault(t *testing.T) {
	rejects(t, "import std/io\n\n"+defaultSelfBoundScale+`
type Shown Int

impl Display for Shown {
    fn to_string(_s: Shown): String {
        "shown"
    }
}

impl Scale for Shown {
    fn label(_s: Shown): String {
        "a"
    }

    fn format(_s: Shown): String {
        "own"
    }
}

fn main() {
    io.print(Scale.format(Shown(1)))
}
`,
		"main.nomi:24:8: impl function 'format': 'format' is a final default on interface 'Scale'")
}

func TestDefaultSelfBound_OnlyOnADefaultFunction(t *testing.T) {
	rejects(t, `interface Scale {
    fn label(scale: self): String where self: Display
}

fn main() {
    Unit
}
`,
		"main.nomi:2:41: interface function 'label': `where self` is only allowed on a default function, and 'label' is a required function",
		"drop it, or give `fn label` a default body that uses Display")
	rejects(t, `fn top(x: Int): Int where self: Display {
    x
}

fn main() {
    _ = top(1)
}
`,
		"main.nomi:1:27: function 'top': `where self` is only allowed on an interface's default function")
}

// What a default body demands of `self` beyond its own interface and Debug
// is an error at the use unless `where self` names it.
func TestDefaultSelfBound_UnboundedUseIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name, body, ret, want, iface string
	}{
		{"interface call", "Display.to_string(scale)", "String", "main.nomi:3:27: `scale` is a `self`", "Display"},
		{"pipe", "scale |> Display.to_string()", "String", "main.nomi:3:9: `scale` is a `self`", "Display"},
		{"interpolation", `"${scale}"`, "String", "main.nomi:3:12: `scale` is a `self`", "Display"},
		{"ordering", "scale < scale", "Bool", "main.nomi:3:9: `scale` is a `self`", "Comparable"},
		{"hash", "Hashable.hash(scale)", "Int", "main.nomi:3:23: `scale` is a `self`", "Hashable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "interface Scale {\n    fn format(scale: self): " + tc.ret + " {\n        " + tc.body +
				"\n    }\n}\n\nfn main() {\n    Unit\n}\n"
			rejects(t, src, tc.want,
				", and Scale does not require "+tc.iface+" of its implementors",
				"`fn format(scale: self): "+tc.ret+" where self: "+tc.iface+"`")
			bounded := "interface Scale {\n    fn format(scale: self): " + tc.ret + " where self: " + tc.iface + " {\n        " + tc.body +
				"\n    }\n}\n\nfn main() {\n    Unit\n}\n"
			admits(t, bounded)
		})
	}
}
