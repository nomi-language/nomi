package analysis_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/frontend"
)

// A derive names its interface by identity (spec §12 *Derive*,
// derive_identity.go): bare or through its module, it must mean the std
// interface of its name, and a file's own interface of the name is one
// error at the derive.

// derivableForms is each derivable interface, the import its module-
// qualified spelling needs, and a type it derives for.
var derivableForms = []struct{ iface, module, decl string }{
	{"Equatable", "equatable", "struct P\n    x: Int\nend\n"},
	{"Hashable", "hashable", "struct P\n    x: Int\nend\n"},
	{"Comparable", "comparable", "struct P\n    x: Int\nend\n"},
	{"Debug", "debug", "struct P\n    x: Int\nend\n"},
	{"Display", "display", "struct P\n    x: Int\nend\n"},
	{"ToJson", "json", "struct P\n    x: Int\nend\n"},
	{"FromJson", "json", "struct P\n    x: Int\nend\n"},
	{"Variants", "variants", "enum P\n    A\n    B\nend\n"},
}

// Every derivable interface derives bare and through its module.
func TestDeriveIdentity_EveryInterfaceBareAndQualified(t *testing.T) {
	if got := len(derivableForms); got != len(analysis.DerivableInterfaces()) {
		t.Fatalf("the test names %d derivable interfaces, and derive writes %d", got, len(analysis.DerivableInterfaces()))
	}
	for _, f := range derivableForms {
		bareImport := ""
		if f.module == "json" {
			bareImport = "import std::json." + f.iface + "\n\n"
		}
		t.Run(f.iface+"/bare", func(t *testing.T) {
			src := bareImport + f.decl + "\nderive " + f.iface + " for P\n\nfn main() end\n"
			expectDeriveClean(t, deriveCheck(t, src, nil))
		})
		t.Run(f.iface+"/qualified", func(t *testing.T) {
			src := "import std::" + f.module + "\n\n" + f.decl + "\nderive " + f.module + "." + f.iface + " for P\n\nfn main() end\n"
			expectDeriveClean(t, deriveCheck(t, src, nil))
		})
	}
}

// localJsonInterfaces declares a file's own ToJson and FromJson, unlike
// std's, with impls of them for User.
const localJsonInterfaces = `interface ToJson
    fn to_json(value: self) -> String
end

interface FromJson
    fn from_json(text: String) -> self
end

struct User
    name: String
end

impl ToJson for User
    fn to_json(value: User) -> String
        "user ${value.name}"
    end
end

impl FromJson for User
    fn from_json(text: String) -> User
        User{name: text}
    end
end
`

// `derive json.ToJson` beside a file's own ToJson, and an impl of it, is
// std's: two interfaces, no conflict. The same for FromJson, and for a
// generic type.
func TestDeriveIdentity_QualifiedBesideALocalInterfaceOfItsName(t *testing.T) {
	src := "import std::json\n\n" + localJsonInterfaces + `
derive json.ToJson for User
derive json.FromJson for User

struct Box<T>
    item: T
end

derive json.ToJson for Box
derive json.FromJson for Box

fn main()
    u = User{name: "ann"}
    a: String = ToJson.to_json(u)
    b: String = json.Json.encode_as(u)
    c: String = json.Json.encode_as(Box{item: u})
    dbg (a, b, c)
end
`
	expectDeriveClean(t, deriveCheck(t, src, nil))
}

// A derive of std's ToJson beside an impl of another file's ToJson for the
// type is no conflict.
func TestDeriveIdentity_BesideAnImplOfASiblingsInterface(t *testing.T) {
	src := `import std::json.{Json, ToJson}
import a

struct User
    name: String
end

impl a.ToJson for User
    fn to_json(value: User) -> String
        "a ${value.name}"
    end
end

derive ToJson for User

fn main()
    dbg (a.ToJson.to_json(User{name: "x"}), Json.encode_as(User{name: "y"}))
end
`
	sibling := "pub interface ToJson\n    fn to_json(value: self) -> String\nend\n"
	expectDeriveClean(t, deriveCheck(t, src, map[string]string{"a": sibling}))
}

// deriveCheck checks src as the front end does a program's entry, beside
// siblings by module name, and answers its diagnostics, one per line, or
// nil when it is accepted.
func deriveCheck(t *testing.T, src string, siblings map[string]string) []string {
	t.Helper()
	_, err := frontend.New(frontend.Config{VirtualFiles: siblings}).CheckSource("main", src, frontend.Mode{})
	if err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}

// expectDeriveClean requires the program to be accepted.
func expectDeriveClean(t *testing.T, errs []string) {
	t.Helper()
	for _, e := range errs {
		t.Errorf("unexpected diagnostic: %s", e)
	}
}

// derivesOnly requires errs to be exactly one diagnostic, at line:col,
// saying want. A diagnostic's related locations (its `note:` lines) are
// part of it.
func derivesOnly(t *testing.T, errs []string, line, col int, want string) {
	t.Helper()
	errs = slices.DeleteFunc(slices.Clone(errs), func(e string) bool { return strings.Contains(e, ": note: ") })
	if len(errs) == 0 {
		t.Fatalf("the program was accepted; want the error %q", want)
	}
	if len(errs) != 1 {
		t.Fatalf("want one error, got %d:\n%s", len(errs), strings.Join(errs, "\n"))
	}
	if got, w := errs[0], fmt.Sprintf("main.nomi:%d:%d: %s", line, col, want); got != w {
		t.Fatalf("got  %s\nwant %s", got, w)
	}
}

// `derive ToJson` where `ToJson` is the file's own interface, not std's,
// is one error at the derive that says how to derive std's.
func TestDeriveIdentity_AFilesOwnInterfaceIsOneError(t *testing.T) {
	for _, c := range []struct{ iface, fn string }{
		{"ToJson", "fn to_json(value: self) -> String"},
		{"FromJson", "fn from_json(text: String) -> self"},
	} {
		t.Run(c.iface, func(t *testing.T) {
			src := "interface " + c.iface + "\n    " + c.fn + "\nend\n\nstruct User\n    name: String\nend\n\nderive " + c.iface + " for User\n\nfn main() end\n"
			derivesOnly(t, deriveCheck(t, src, nil), 9, 8,
				"`derive "+c.iface+" for User`: `"+c.iface+"` here is the interface this file declares, and derive writes only std's `"+c.iface+"` (`std::json`); to derive std's, write `derive json."+c.iface+" for User` with `import std::json`")
		})
	}
}

// A qualified derive whose module names another file's interface of the
// name is the same error, naming that file.
func TestDeriveIdentity_AQualifiedSiblingInterfaceIsOneError(t *testing.T) {
	src := `import a

struct User
    name: String
end

derive a.ToJson for User

fn main() end
`
	sibling := "pub interface ToJson\n    fn to_json(value: self) -> String\nend\n"
	derivesOnly(t, deriveCheck(t, src, map[string]string{"a": sibling}), 7, 8,
		"`derive a.ToJson for User`: `a.ToJson` here is the interface `a` declares, and derive writes only std's `ToJson` (`std::json`); to derive std's, write `derive json.ToJson for User` with `import std::json`")
}

// A qualified derive whose module the file does not import is the scope
// error, with the import to add.
func TestDeriveIdentity_AQualifiedInterfaceMustBeInScope(t *testing.T) {
	src := `struct User
    name: String
end

derive json.ToJson for User

fn main() end
`
	derivesOnly(t, deriveCheck(t, src, nil), 5, 8, "`json.ToJson` is not in scope — import `std::json`")
}

// A derive beside a hand-written impl of the same interface for the type
// is the conflict, whichever spelling each names the interface by. It is
// reported at the hand-written block's header.
func TestDeriveIdentity_ADeriveBesideAHandWrittenImpl(t *testing.T) {
	for _, c := range []struct{ name, imports, impl, derive string }{
		{"same spelling", "import std::json.{Json, ToJson}", "ToJson", "ToJson"},
		{"qualified impl", "import std::json\nimport std::json.{Json, ToJson}", "json.ToJson", "ToJson"},
		{"qualified derive", "import std::json\nimport std::json.{Json, ToJson}", "ToJson", "json.ToJson"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := c.imports + `

struct User
    name: String
end

impl ` + c.impl + ` for User
    fn to_json(value: User) -> Json
        Json.String(value.name)
    end
end

derive ` + c.derive + ` for User

fn main() end
`
			line := strings.Count(src[:strings.Index(src, "\nimpl ")], "\n") + 2
			derivesOnly(t, deriveCheck(t, src, nil), line, 1,
				"`derive ToJson for User` conflicts with a hand-written `impl ToJson for User` block — a derived impl is synthesized whole, so it cannot coexist with a manual impl; drop one")
		})
	}
	src := `struct P
    x: Int
end

derive Comparable for P

impl Comparable for P
    fn compare(a: P, b: P) -> Ordering
        Int.compare(a.x, b.x)
    end
end

fn main() end
`
	derivesOnly(t, deriveCheck(t, src, nil), 7, 1,
		"`derive Comparable for P` conflicts with a hand-written `impl Comparable for P` block — a derived impl is synthesized whole, so it cannot coexist with a manual impl; drop one")
}

// A derive of no derivable interface says so, naming it.
func TestDeriveIdentity_ANonDerivableInterface(t *testing.T) {
	src := `interface Assertable
    fn describe(value: self) -> String
end

struct User
    name: String
end

derive Assertable for User

fn main() end
`
	derivesOnly(t, deriveCheck(t, src, nil), 9, 8,
		"`derive Assertable for User`: `Assertable` is not an interface derive can write; derive writes Equatable, Hashable, Comparable, Debug, Display, ToJson, FromJson, Variants")
}
