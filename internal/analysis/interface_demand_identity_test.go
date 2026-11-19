package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// twoShowFiles are two files that each declare an interface named Show.
var twoShowFiles = map[string]string{
	"a.nomi": `pub interface Show
    fn show(value: self) -> String
end
`,
	"b.nomi": `pub interface Show
    fn show(value: self) -> String
end
`,
}

// showUser is a program that declares User with `impl a.Show for User`,
// with decls before it and body as main's.
func showUser(imports, decls, body string) string {
	return `import
    std::io
` + imports + `end
` + decls + `
struct User
    name: String
end

impl a.Show for User
    fn show(value: User) -> String
        "a ${value.name}"
    end
end

fn main()
    u = User{name: "ann"}
` + body + `
end
`
}

// identityDiags checks the project of main.nomi beside files and answers
// every diagnostic, each with its hints.
func identityDiags(t *testing.T, src string, files map[string]string) []analysis.TypeError {
	t.Helper()
	fa, nodes := implIdentityProject(t, src, files)
	errs := append([]analysis.TypeError{}, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	return append(errs, analysis.FinalizeCoherence(fa)...)
}

func identityDiagTexts(errs []analysis.TypeError) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Message+" | "+strings.Join(e.Hints, " | "))
	}
	return out
}

// A call, bound or value of an interface finds only impls of that
// interface's declaration, whichever way the program spells it: with only
// `impl a.Show for User`, b's Show, a file's own Show and std's ToJson
// read as a program's own are each unimplemented. Where the type has an
// impl of another interface of the name, the message names the interface
// through its file. An interface declared inside a body has no impl at
// all, and its help says so.
func TestMissingImpl_ACallFindsOnlyItsInterfacesImpl(t *testing.T) {
	ownToJson := `
interface ToJson
    fn to_json(value: self) -> String
end
`
	for _, c := range []struct {
		name, src string
		want      []string
	}{
		{"a module-qualified call", showUser("    a\n    b\n", "", `    io.print(b.Show.show(u))`),
			[]string{"no impl of `b.Show` for `User` (required via call site)", "write an `impl b.Show for User` block"}},
		{"a bare call of a selectively imported interface", showUser("    a\n    b.{Show}\n", "", `    io.print(Show.show(u))`),
			[]string{"no impl of `b.Show` for `User` (required via call site)", "write an `impl b.Show for User` block"}},
		{"a bare call of the file's own interface", showUser("    a\n", "\ninterface Show\n    fn show(value: self) -> String\nend\n", `    io.print(Show.show(u))`),
			[]string{"no impl of `Show` for `User` (required via call site)", "write an `impl Show for User` block"}},
		{"a pipe", showUser("    a\n    b\n", "", `    io.print(u |> b.Show.show())`),
			[]string{"no impl of `b.Show` for `User` (required via call site)"}},
		{"a call through a bound of another Show", showUser("    a\n    b\n", "\nfn render<T>(x: T) -> String where T: a.Show\n    b.Show.show(x)\nend\n", `    io.print(render(u))`),
			[]string{"`x` is a `T`, and `render` does not require b.Show of `T`"}},
		{"a bound", showUser("    a\n    b\n", "\nfn render<T>(x: T) -> String where T: b.Show\n    b.Show.show(x)\nend\n", `    io.print(render(u))`),
			[]string{"User does not implement b.Show (required by `where T: b.Show`)", "write an `impl b.Show for User` block"}},
		{"a value", showUser("    a\n    b\n", "", `    s: b.Show = u
    io.print(b.Show.show(s))`),
			[]string{"type mismatch: expected b.Show, got User", "write an `impl b.Show for User` block"}},
		{"std's ToJson read as the file's own", `import
    std::io
    std::json
end
` + ownToJson + `
struct User
    name: String
end

impl json.ToJson for User
    fn to_json(value: User) -> json.Json
        json.Json.String(value.name)
    end
end

fn main()
    io.print(ToJson.to_json(User{name: "ann"}))
end
`, []string{"no impl of `ToJson` for `User` (required via call site)", "write an `impl ToJson for User` block"}},
		{"a bound of the file's own ToJson, with std's implemented", `import
    std::io
    std::json
end
` + ownToJson + `
struct User
    name: String
end

impl json.ToJson for User
    fn to_json(value: User) -> json.Json
        json.Json.String(value.name)
    end
end

fn render<T>(x: T) -> String where T: ToJson
    ToJson.to_json(x)
end

fn main()
    io.print(render(User{name: "ann"}))
    j: ToJson = User{name: "bob"}
    io.print(ToJson.to_json(j))
end
`, []string{"User does not implement ToJson (required by `where T: ToJson`)", "type mismatch: expected ToJson, got User",
			"write an `impl ToJson for User` block with `fn to_json(value: User) -> String`"}},
		{"a body interface's call", `import std::io

interface Show
    fn show(value: self) -> String
end

struct User
    name: String
end

impl Show for User
    fn show(value: User) -> String
        "module ${value.name}"
    end
end

fn main()
    interface Show
        fn show(value: self) -> String
    end

    io.print(Show.show(User{name: "ann"}))
end
`, []string{"no impl of `Show` for `User` (required via call site)", "`Show` is declared inside a body, where no impl can name it; declare it at module level to write an `impl Show for User` block"}},
		{"a body interface's bound", `import std::io

interface Show
    fn show(value: self) -> String
end

struct User
    name: String
end

impl Show for User
    fn show(value: User) -> String
        "module ${value.name}"
    end
end

fn main()
    interface Show
        fn show(value: self) -> String
    end

    fn render<T>(x: T) -> String where T: Show
        Show.show(x)
    end

    io.print(render(User{name: "ann"}))
end
`, []string{"User does not implement Show (required by `where T: Show`)", "`Show` is declared inside a body, where no impl can name it; declare it at module level to write an `impl Show for User` block"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := identityDiagTexts(identityDiags(t, c.src, twoShowFiles))
			if len(got) == 0 {
				t.Fatal("the program is admitted")
			}
			for _, want := range c.want {
				found := false
				for _, m := range got {
					if strings.Contains(m, want) {
						found = true
					}
				}
				if !found {
					t.Errorf("no diagnostic says %q; diagnostics: %q", want, got)
				}
			}
			if strings.Contains(c.name, "the file's own ToJson") {
				// derive writes std's ToJson, which the type has: the
				// help offers the file's interface's impl only.
				for _, m := range got {
					if strings.Contains(m, "derive") {
						t.Errorf("a diagnostic offers a derive: %q", m)
					}
				}
			}
		})
	}
}

// What keeps working: an impl in one file of another file's interface,
// reached by a qualified call, a selective import, a re-export, a bound and
// a value; std's impls of std's interfaces; and a user generic interface
// whose name another file declares too, beside std's generic Add.
func TestMissingImpl_AnImplOfTheNamedInterfaceSupplies(t *testing.T) {
	files := map[string]string{
		"a.nomi": twoShowFiles["a.nomi"],
		"b.nomi": twoShowFiles["b.nomi"],
		"users.nomi": `import a

pub struct User
    name: String
end

impl a.Show for User
    fn show(value: User) -> String
        "a ${value.name}"
    end
end
`,
		"shapes.nomi": `pub import a.{Show}
`,
		"scale_a.nomi": `pub interface Scale<F>
    fn scale(x: self, by: F) -> self
end
`,
		"scale_b.nomi": `pub interface Scale<F>
    fn scale(x: self, by: F) -> self
end
`,
	}
	for _, c := range []struct{ name, src string }{
		{"a qualified call of another file's impl", `import
    std::io
    a
    users.{User}
end

fn main()
    io.print(a.Show.show(User{name: "ann"}))
end
`},
		{"a selective import", `import
    std::io
    a.{Show}
    users.{User}
end

fn render<T>(x: T) -> String where T: Show
    Show.show(x)
end

fn main()
    u = User{name: "ann"}
    s: Show = u
    io.print(Show.show(u))
    io.print(render(u))
    io.print(Show.show(s))
end
`},
		{"a re-export", `import
    std::io
    shapes.{Show}
    users.{User}
end

fn main()
    io.print(Show.show(User{name: "ann"}))
end
`},
		{"std's impls of std's interfaces", `import
    std::io
    std::json
end

fn main()
    io.print(json.ToJson.to_json(5))
    io.print(Display.to_string(5))
    io.print(Comparable.compare(1, 2))
end
`},
		{"a generic interface of a name two files declare", `import
    std::io
    scale_a.{Scale}
    scale_b
end

struct Money
    cents: Int
end

impl Scale<Int> for Money
    fn scale(x: Money, by: Int) -> Money
        Money{cents: x.cents * by}
    end
end

impl Add<Money, Money> for Money
    fn add(lhs: Money, rhs: Money) -> Money
        Money{cents: lhs.cents + rhs.cents}
    end
end

fn twice<T>(x: T) -> T where T: Scale<Int>
    T.scale(x, 2)
end

fn main()
    m = twice(Money{cents: 3}) + Money{cents: 1}
    io.print(m.cents)
    _ = Scale.scale(m, 2)
end
`},
		{"a module-qualified generic interface in an impl header", `import
    std::io
    scale_a
end

interface Scale<F>
    fn scale(x: self, by: F) -> self
end

struct Money
    cents: Int
end

impl scale_a.Scale<Int> for Money
    fn scale(x: Money, by: Int) -> Money
        Money{cents: x.cents * by}
    end
end

fn twice<T>(x: T) -> T where T: scale_a.Scale<Int>
    T.scale(x, 2)
end

fn main()
    io.print(twice(Money{cents: 3}).cents)
    _ = scale_a.Scale.scale(Money{cents: 1}, 2)
end
`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := identityDiagTexts(identityDiags(t, c.src, files)); len(got) != 0 {
				t.Fatalf("the program is rejected: %q", got)
			}
		})
	}
	t.Run("the other generic interface of the name", func(t *testing.T) {
		src := `import
    std::io
    scale_a.{Scale}
    scale_b
end

struct Money
    cents: Int
end

impl Scale<Int> for Money
    fn scale(x: Money, by: Int) -> Money
        Money{cents: x.cents * by}
    end
end

fn main()
    _ = scale_b.Scale.scale(Money{cents: 3}, 2)
end
`
		got := identityDiagTexts(identityDiags(t, src, files))
		want := "no impl of `scale_b.Scale` for `Money`"
		for _, m := range got {
			if strings.Contains(m, want) {
				return
			}
		}
		t.Fatalf("no diagnostic says %q; diagnostics: %q", want, got)
	})
}

// An impl header that qualifies its interface with a module names that
// module's interface only: where the module declares none, the header is
// undefined, and this file's own interface of the name gets no impl.
func TestMissingImpl_AQualifiedHeaderNamesOnlyItsModulesInterface(t *testing.T) {
	files := map[string]string{"util.nomi": `pub fn hello() -> String
    "hi"
end
`}
	src := `import
    std::io
    util
end

interface Show
    fn show(value: self) -> String
end

struct User
    name: String
end

impl util.Show for User
    fn show(value: User) -> String
        "util ${value.name} ${util.hello()}"
    end
end

fn main()
    io.print(Show.show(User{name: "ann"}))
end
`
	got := identityDiagTexts(identityDiags(t, src, files))
	if len(got) == 0 {
		t.Fatal("the program is admitted")
	}
	for _, want := range []string{"impl block: undefined interface 'util.Show'", "no impl of `Show` for `User`"} {
		found := false
		for _, m := range got {
			found = found || strings.Contains(m, want)
		}
		if !found {
			t.Errorf("no diagnostic says %q; diagnostics: %q", want, got)
		}
	}
}

// Spec §12 *Function Name Collisions*: a type-qualified call is ambiguous
// when the type implements two interfaces that each declare the function,
// and two interfaces of one name from two files are two. The message names
// each through its file only where both share the name; two interfaces of
// different names keep their bare names.
func TestCollision_TwoInterfacesOfOneNameAreTwo(t *testing.T) {
	both := `
impl b.Show for User
    fn show(value: User) -> String
        "b ${value.name}"
    end
end
`
	for _, c := range []struct {
		name, src, want string
	}{
		{"a.Show and b.Show", showUser("    a\n    b\n", both, `    io.print(User.show(u))`),
			"type 'User' impl 'a.Show' and 'b.Show', which each declare 'show' — type-qualified 'User.show(...)' is ambiguous; qualify by interface, e.g. 'a.Show.show(...)'"},
		{"the file's own ToJson and std's", `import
    std::io
    std::json
end

interface ToJson
    fn to_json(value: self) -> String
end

struct User
    name: String
end

impl ToJson for User
    fn to_json(value: User) -> String
        "user ${value.name}"
    end
end

impl json.ToJson for User
    fn to_json(value: User) -> json.Json
        json.Json.String(value.name)
    end
end

fn main()
    io.print(User.to_json(User{name: "ann"}))
end
`, "type 'User' impl 'ToJson' and 'json.ToJson', which each declare 'to_json' — type-qualified 'User.to_json(...)' is ambiguous; qualify by interface, e.g. 'ToJson.to_json(...)'"},
		{"two interfaces of different names", showUser("    a\n", `
interface Speaker
    fn show(value: self) -> String
end

impl Speaker for User
    fn show(value: User) -> String
        "speaker ${value.name}"
    end
end
`, `    io.print(User.show(u))`),
			"type 'User' impl 'Show' and 'Speaker', which each declare 'show' — type-qualified 'User.show(...)' is ambiguous; qualify by interface, e.g. 'Show.show(...)'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := identityDiagTexts(identityDiags(t, c.src, twoShowFiles))
			if len(got) == 0 {
				t.Fatal("the program is admitted")
			}
			for _, m := range got {
				if strings.Contains(m, c.want) {
					return
				}
			}
			t.Fatalf("no diagnostic says %q; diagnostics: %q", c.want, got)
		})
	}
	t.Run("each interface-qualified call is not ambiguous", func(t *testing.T) {
		src := showUser("    a\n    b\n", both, `    io.print(a.Show.show(u))
    io.print(b.Show.show(u))`)
		if got := identityDiagTexts(identityDiags(t, src, twoShowFiles)); len(got) != 0 {
			t.Fatalf("the program is rejected: %q", got)
		}
	})
}
