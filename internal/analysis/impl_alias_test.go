package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An impl belongs to the interface its header's name resolves to, not to
// the header's spelling. A header that names an interface through an
// import alias (`import std::resources.{Release as Freeable}`, then `impl
// Freeable for Lock`) is that interface's impl in every table that keys
// an impl by its interface, so the release rules, bounds, qualified calls,
// duplicate impls and a check against interfaces all see it.

// errorsOfProgram is every diagnostic of the program by module key, as
// `file: message` lines.
func errorsOfProgram(cp *analysis.CheckedProgram) string {
	var b strings.Builder
	for key, errs := range cp.Errors {
		for _, e := range errs {
			b.WriteString(key + ": " + e.Message + "\n")
		}
	}
	return b.String()
}

// lockFile declares a Release type through an alias of std's Release, and
// the acquisition that returns one.
const lockFile = `import
    std::io
    std::resources.{Release as Freeable}
end

pub struct Lock
    id: Int
end

impl Freeable for Lock
    fn release(l: Lock)
        Lock.unlock(l)
    end
end

impl Lock
    pub fn unlock(l: Lock)
        io.print("unlocked ${l.id}")
    end
end

pub fn lock() -> Lock
    Lock{id: 1}
end
`

// A type whose Release impl names the interface through an alias is a
// Release type: a lock another file acquires and never releases is the
// unreleased-resource error, naming the impl's release, and checked
// against interfaces it is the same, so the interface keeps the impl's
// body as it keeps a plain `impl Release`'s.
func TestImplAlias_AReleaseImplUnderAnotherNameHoldsItsAcquisitions(t *testing.T) {
	p := incrementalProject{name: "release alias", entry: "main", texts: map[string]string{
		"lock": lockFile,
		"main": `import
    std::io
    lock.{Lock, lock}
end

fn main()
    l = lock()
    io.print(l.id)
end
`,
	}}
	full := fullCheck(p, p.texts)
	var leak *analysis.TypeError
	for i, e := range full.Errors["main"] {
		if e.Code == analysis.UnreleasedResourceCode {
			leak = &full.Errors["main"][i]
		}
	}
	if leak == nil {
		t.Fatalf("a lock acquired and never released is accepted:\n%s", errorsOfProgram(full))
	}
	if want := "`l` is never released; write `defer Lock.unlock(l)` after it, or return it"; leak.Message != want {
		t.Errorf("the leak reads %q, want %q", leak.Message, want)
	}
	if diff := fromInterfacesDiff(p, p.texts); diff != "" {
		t.Errorf("checked against interfaces, the program differs:\n%s", diff)
	}

	p.texts["main"] = strings.Replace(p.texts["main"], "    l = lock()\n", "    l = lock()\n    defer Lock.unlock(l)\n", 1)
	if errs := errorsOfProgram(fullCheck(p, p.texts)); errs != "" {
		t.Fatalf("a lock released by its impl's release is refused:\n%s", errs)
	}
	if diff := fromInterfacesDiff(p, p.texts); diff != "" {
		t.Errorf("released, checked against interfaces, the program differs:\n%s", diff)
	}
}

// A program's own interface imported under another name is implemented
// by an impl that names the alias: a bound on the interface, in the file
// that declares it and in the file that aliases it, a qualified call and
// an interface value all take the type.
func TestImplAlias_AProgramInterfaceUnderAnotherName(t *testing.T) {
	p := incrementalProject{name: "program interface alias", entry: "main", texts: map[string]string{
		"shapes": `pub interface Shape
    fn area(value: self) -> Int
end

pub fn total<T>(xs: List<T>) -> Int where T: Shape
    Iter.reduce(xs, 0, fn acc, x -> acc + T.area(x))
end
`,
		"main": `import
    std::io
    shapes.{Shape as Figure, total}
end

struct Sq
    side: Int
end

impl Figure for Sq
    fn area(s: Sq) -> Int
        s.side * s.side
    end
end

fn show<T>(x: T) -> Int where T: Figure
    Figure.area(x)
end

fn main()
    f: Figure = Sq{side: 4}
    io.print(show(Sq{side: 3}) + total([Sq{side: 1}]) + Figure.area(f))
end
`,
	}}
	if errs := errorsOfProgram(fullCheck(p, p.texts)); errs != "" {
		t.Fatalf("an impl of the program's interface under another name is refused:\n%s", errs)
	}
	if diff := fromInterfacesDiff(p, p.texts); diff != "" {
		t.Errorf("checked against interfaces, the program differs:\n%s", diff)
	}
}

// Two impls of one interface for one type, one header naming it and one
// an alias of it, are the duplicate-impl error.
func TestImplAlias_TwoNamesOfOneInterfaceAreADuplicateImpl(t *testing.T) {
	p := incrementalProject{name: "duplicate under an alias", entry: "main", texts: map[string]string{
		"main": `import
    std::io
    std::equatable.{Equatable as Eq}
end

struct P
    x: Int
end

impl Eq for P
    fn equal(a: P, b: P) -> Bool
        a.x == b.x
    end
end

impl Equatable for P
    fn equal(a: P, b: P) -> Bool
        a.x != b.x
    end
end

fn main()
    io.print(P{x: 1} == P{x: 1})
end
`,
	}}
	errs := errorsOfProgram(fullCheck(p, p.texts))
	if want := "duplicate impl: `Equatable` is implemented for `P`"; !strings.Contains(errs, want) {
		t.Fatalf("two impls of Equatable for P are accepted, want %q among:\n%s", want, errs)
	}
}

// A program's interface named like a std one and imported under another
// name is not std's: an impl of it supplies no bound, call or value of
// std's Localize, which the errors name through its file, and the impl
// serves its own interface's calls.
func TestImplAlias_AProgramInterfaceNamedLikeStdsIsNotStds(t *testing.T) {
	p := incrementalProject{name: "localize alias", entry: "main", texts: map[string]string{
		"words": `pub interface Localize
    fn localize(value: self, locale: Locale) -> String
end
`,
		"main": `import
    std::io
    std::localize.{Localize as StdLocalize}
    words.{Localize as Speak}
end

struct P
    x: Int
end

impl Speak for P
    fn localize(p: P, _locale: Locale) -> String
        "p${p.x}"
    end
end

fn std_text<T>(x: T) -> String where T: StdLocalize
    StdLocalize.localize(x, Locale"en")
end

fn main()
    io.print(Speak.localize(P{x: 1}, Locale"en"))
    io.print(std_text(P{x: 2}))
end
`,
	}}
	full := fullCheck(p, p.texts)
	errs := errorsOfProgram(full)
	if want := "main: P does not implement localize.Localize (required by `where T: localize.Localize`)\n"; errs != want {
		t.Fatalf("the program's Localize answers for std's, want only %q, got:\n%s", want, errs)
	}
	if diff := fromInterfacesDiff(p, p.texts); diff != "" {
		t.Errorf("checked against interfaces, the program differs:\n%s", diff)
	}
}
