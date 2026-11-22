package vmhost

import (
	"bytes"
	"path/filepath"
	"slices"
	"testing"
)

// genericLibrary is a library with no main and no tests whose [effects.self]
// allows std, holding src.
func genericLibrary(std, src string) map[string]string {
	return map[string]string{
		"nomi.toml":   "[module]\nname = \"genlib\"\n\n[effects.self]\nstd = " + std + "\n",
		"genlib.nomi": src,
	}
}

// readShown is a public generic function with one bounded type parameter
// that nothing calls, and that reads a file.
const readShown = `import
    std::fs
    std::io
end

pub fn read_shown<T>(tag: T, path: String) -> Result<String, io.Error>
where T: Display
    text = fs.read_text(path)?
    Ok("${tag}: ${text}")
end
`

// A public generic function nothing instantiates is a root of its library's
// check through its abstract body: the body with its type parameter held as
// its bound's interface, so a library cannot publish a list its generic code
// breaks. The read is refused under an [effects.self] that allows nothing,
// reached from the function, and allowed once std names std::fs.
func TestEffectRoots_AGenericFunctionIsWalkedThroughItsAbstractBody(t *testing.T) {
	got, ds := effectDiagnostics(t, "genlib.nomi", genericLibrary("[]", readShown))
	want := "genlib.nomi:8:12: this module's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"genlib.nomi:8:12: help: reached from `read_shown`\n" +
		"genlib.nomi:8:12: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Code != EffectNotAllowedCode {
		t.Errorf("diagnostics %v; want one %s", ds, EffectNotAllowedCode)
	}
	checkedRootsPass(t, "genlib.nomi", genericLibrary(`["std::fs"]`, readShown))
}

// A public generic function whose effects at every type no body says is an
// error under [effects.self], at its name, whatever the list allows: one
// whose abstract body does not lower, here at a planted decline.
func TestEffectRoots_AGenericWithNoBodyForEveryTypeIsNotDetermined(t *testing.T) {
	plantDecline(t)
	cases := []struct {
		name, src, fn, want string
	}{
		{"a body that declines", `pub fn tally<T>(x: T) -> (T, Int)
    (x, planted_decline())
end

fn planted_decline() -> Int
    1
end
`, "tally", "genlib.nomi:1:8: the effects of `tally` for every type it may be called at are not known, so [effects.self] cannot hold it\n" +
			"genlib.nomi:1:8: help: its body, with its type parameters held as their bounds, does not lower: " + plantedReason + "\n" +
			"genlib.nomi:1:8: help: a call that gives it types is checked at those types"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, std := range []string{"[]", `["std::fs"]`} {
				got, ds := effectDiagnostics(t, "genlib.nomi", genericLibrary(std, c.src))
				if got != c.want {
					t.Errorf("std = %s: diagnostics:\n%s\nwant:\n%s", std, got, c.want)
				}
				if len(ds) != 1 || ds[0].Code != EffectsNotDeterminedCode || !slices.Equal(ds[0].Args, []string{c.fn, "self"}) {
					t.Errorf("std = %s: diagnostics %v; want one %s with Args [%s self]", std, ds, EffectsNotDeterminedCode, c.fn)
				}
			}
		})
	}
}

// A bound-free generic function a test instantiates is answered by that
// instance: a bound-free type parameter has no operation of its own, so the
// read is refused at its call and nothing else is reported.
func TestEffectRoots_ABoundFreeGenericACallInstantiatesIsDetermined(t *testing.T) {
	src := `import
    std::fs
    std::io
end

pub fn read_tagged<T>(tag: T, path: String) -> Result<(T, String), io.Error>
    text = fs.read_text(path)?
    Ok((tag, text))
end

test "a missing file is an error"
    assert Result.is_err(read_tagged(1, "/nonexistent"))
end
`
	got, _ := effectDiagnostics(t, "genlib.nomi", genericLibrary("[]", src))
	want := "genlib.nomi:7:12: this module's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"genlib.nomi:7:12: help: reached from `read_tagged`\n" +
		"genlib.nomi:7:12: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// An abstract body is linked into nothing a program runs: lowering the
// abstract bodies, and walking the public functions through them, leaves
// the program's image byte for byte as a load that lowers none builds, and
// no module of theirs is among the program's.
func TestAbstractBodies_AreInNoImage(t *testing.T) {
	src := `import std::io

interface Describe
    fn describe(value: self) -> String
end

pub struct Loud
    n: Int
end

impl Describe for Loud
    fn describe(_l: Loud) -> String
        io.print("LOUD")
        "loud"
    end
end

pub fn tell<T>(x: T) -> String
where T: Describe
    Describe.describe(x)
end

pub fn shout<T>(x: T) -> String
where T: Describe
    io.print("shout")
    Describe.describe(x)
end

fn main()
    io.print(tell(Loud{n: 1}))
end
`
	dir := writeProject(t, map[string]string{
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\n",
		"main.nomi": src,
	})
	entry := filepath.Join(dir, "main.nomi")
	image := func(walk bool) ([]byte, *Program) {
		t.Helper()
		p, err := Load(entry)
		if err != nil {
			t.Fatalf("the program does not load: %v", err)
		}
		if walk {
			if err := p.effectError(p.checkedRoots()...); err != nil {
				t.Fatalf("the lists refuse the program: %v", err)
			}
		}
		img, _, err := p.BuildImage()
		if err != nil {
			t.Fatalf("the image does not build: %v", err)
		}
		return img, p
	}
	plain, _ := image(false)
	walked, p := image(true)
	if !bytes.Equal(plain, walked) {
		t.Errorf("the image of a program whose abstract bodies were lowered differs from one whose were not (%d and %d bytes)", len(walked), len(plain))
	}
	abstract, declines := p.res.Abstract()
	if len(declines) != 0 {
		t.Errorf("declines %v; want none", declines)
	}
	if len(abstract) == 0 {
		t.Fatal("no abstract module; want tell's and shout's")
	}
	names := map[string]bool{}
	for _, m := range abstract {
		if slices.Contains(p.res.IRModules(), m) {
			t.Errorf("abstract module %s is among the program's modules", m.Name())
		}
		for _, f := range m.Funcs() {
			names[f.Name()] = true
		}
	}
	for _, want := range []string{"tell", "shout"} {
		if !names[want] {
			t.Errorf("no abstract body of %s among %v", want, names)
		}
	}
}

// A public generic function nothing instantiates is marked and has its
// hover lines from its abstract body, as a public function of an interface
// value has: its name, its own call to io.print, and its call through its
// bound, which may act, since one implementation of it does and one does
// not, whichever walk reaches the implementations first.
func TestEffectMarks_AnUninstantiatedGenericFunction(t *testing.T) {
	marksCase{
		src: marksStores(true) + `
pub fn shout<T>(x: T) -> String
where T: UserStore
    io.print("shout")
    UserStore.find(x, 1)
end
`,
		marked: []string{"17:lookup", "18:UserStore.find", "21:main", "22:lookup", "23:lookup", "31:find", "32:io.print",
			"37:shout", "39:io.print", "40:UserStore.find"},
		hovers: map[string]string{"UserStore.find(x": "May have effects: `UserStore.find` is not pure in every implementation.", "shout<T>": ioLine},
	}.run(t)
}

// Generic code nothing instantiates is marked and has its hover lines from
// its abstract bodies, private or not: a private generic function, and a
// function of an impl block over a generic type.
func TestEffectMarks_GenericCodeNothingInstantiates(t *testing.T) {
	marksCase{
		src: `import std::io

struct Box<T>
    value: T
end

impl Box<T>
    fn shout(b: Box<T>) -> T
        io.print("box")
        b.value
    end
end

fn echo<T>(x: T) -> T
    io.print("echo")
    x
end

fn main()
    io.print("main")
end
`,
		marked: []string{"8:shout", "9:io.print", "14:echo", "15:io.print", "19:main", "20:io.print"},
		hovers: map[string]string{"echo<T>": ioLine, "shout(b": ioLine},
	}.run(t)
}
