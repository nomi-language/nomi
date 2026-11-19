package vmhost

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// goTable stands in for the FFI wrapper's table, answering each of keys
// with a function that returns its argument, so no Go is built.
func goTable(keys ...string) HostTable {
	return func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		out := map[string]hostadapt.Func{}
		for _, k := range keys {
			out[k] = func(_ *rt.Frame, args []rt.Value) (rt.Value, error) { return args[0], nil }
		}
		return out, nil
	}
}

// goProject is an app whose main and test print what example.com/pg, a
// module of Nomi over Go's strings package, answers, with app as the
// project's nomi.toml and pgTable added to pg's own.
func goProject(app, pgTable string) map[string]string {
	return map[string]string{
		"app/go.mod":    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pg v0.0.0\n\nreplace example.com/pg => ../pg\n",
		"app/nomi.toml": app,
		"app/main.nomi": `import
    std::io
    pg::db
end

fn main()
    io.print(db.upper("hi"))
end

test "upper"
    assert db.upper("a") == "a"
end
`,
		"pg/go.mod":    "module example.com/pg\n\ngo 1.27.0\n",
		"pg/nomi.toml": "[module]\nname = \"pg\"\n" + pgTable,
		"pg/db.nomi": `gopkg "strings" as gostrings

pub fn upper(s: String) -> String go gostrings.ToUpper
`,
	}
}

// goKeys are the keys goProject's binding registers under.
var goKeys = []string{"db.upper", "upper", "pg::db.upper"}

const goApp = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nstd = [\"std::io\"]\n"

// heldBy loads app/main.nomi from files and answers what `nomi check`
// holds it to, the refusal's text with the project's directory left out,
// and its diagnostics; "" and nil when the lists allow it.
func heldBy(t *testing.T, files map[string]string, opts ...Option) (string, Diagnostics) {
	t.Helper()
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, "app", "main.nomi"), append([]Option{WithHosts(goTable(goKeys...))}, opts...)...)
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	err = p.capabilityError(p.checkedRoots()...)
	if err == nil {
		return "", nil
	}
	var ds Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("the refusal is %T, not diagnostics: %v", err, err)
	}
	return strings.ReplaceAll(ds.Error(), dir+string(filepath.Separator), ""), ds
}

// A module whose Nomi code binds Go is allowed when the project's go lists
// it, whichever Go package it binds; without it each binding the roots
// reach is refused at the binding, naming the module and the package,
// coded capability-not-allowed with the name and the module as args, and
// the help names the module to add to go. A binding main and a test both
// reach is reported once, from main.
func TestCapabilityGo_TheProjectsGoListsTheModule(t *testing.T) {
	if got, _ := heldBy(t, goProject(goApp+"go = [\"example.com/pg\"]\n", "")); got != "" {
		t.Errorf("go lists example.com/pg, and the program is refused:\n%s", got)
	}
	got, ds := heldBy(t, goProject(goApp, ""))
	want := "pg/db.nomi:3:8: example.com/pg binds Go through go:strings, and nomi.toml lets no module bind Go\n" +
		"pg/db.nomi:3:8: help: reached from `main`; `main` calls `upper`\n" +
		"pg/db.nomi:3:8: help: add \"example.com/pg\" to go under [capabilities] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) == 0 || ds[0].Code != CapabilityNotAllowedCode || !slices.Equal(ds[0].Args, []string{"go:strings", "example.com/pg"}) {
		t.Errorf("diagnostics %v; want the first coded %s with args go:strings, example.com/pg", ds, CapabilityNotAllowedCode)
	}

	// Another module in go does not let pg bind Go, and the args name
	// the modules go lists.
	got, ds = heldBy(t, goProject(goApp+"go = [\"example.com/app\"]\n", ""))
	if !strings.HasPrefix(got, "pg/db.nomi:3:8: example.com/pg binds Go through go:strings, and nomi.toml lets only example.com/app bind Go\n") ||
		!slices.Equal(ds[0].Args, []string{"go:strings", "example.com/pg", "example.com/app"}) {
		t.Errorf("with only the project in go:\n%s\nargs %q", got, ds[0].Args)
	}
}

// A module listed in go_in_tests binds Go in the test cases alone: `nomi
// test` passes, and `nomi run` and `nomi build`, which hold main, refuse.
// A binding only tests reach is refused with help naming go_in_tests.
func TestCapabilityGo_GoInTestsHoldsOnlyTests(t *testing.T) {
	dir := writeProject(t, goProject(goApp+"go_in_tests = [\"example.com/pg\"]\n", ""))
	p, err := Load(filepath.Join(dir, "app", "main.nomi"), WithHosts(goTable(goKeys...)))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.capabilityError(Roots{Tests: &TestOptions{}}); err != nil {
		t.Errorf("the tests are refused: %v", err)
	}
	err = p.capabilityError(Roots{})
	if err == nil || !strings.Contains(err.Error(), "example.com/pg binds Go through go:strings, and nomi.toml lets no module bind Go") {
		t.Errorf("main is not refused: %v", err)
	}

	dir = writeProject(t, goProject(goApp, ""))
	p, err = Load(filepath.Join(dir, "app", "main.nomi"), WithHosts(goTable(goKeys...)))
	if err != nil {
		t.Fatal(err)
	}
	err = p.capabilityError(Roots{Tests: &TestOptions{}})
	if err == nil || !strings.Contains(err.Error(), `help: add "example.com/pg" to go_in_tests under [capabilities] in nomi.toml, or remove the call`) {
		t.Errorf("the tests are not refused with go_in_tests in the help: %v", err)
	}
}

// The dependency's own go never grants: a module that lists itself in
// its own nomi.toml is still refused when the project's go does not list
// it, as the project's list's refusal.
func TestCapabilityGo_ADependencysOwnGoDoesNotGrant(t *testing.T) {
	got, ds := heldBy(t, goProject(goApp, "\n[capabilities]\nstd = []\ngo = [\"example.com/pg\"]\n"))
	if got == "" {
		t.Fatal("the program is admitted; the project's go does not list example.com/pg")
	}
	if ds[0].Code != CapabilityNotAllowedCode || !strings.HasPrefix(got, "pg/db.nomi:3:8: example.com/pg binds Go through go:strings, and nomi.toml lets no module bind Go\n") {
		t.Errorf("diagnostics %q:\n%s\nwant the project's list's refusal", ds[0].Code, got)
	}
}

// A dependency with a [capabilities] table that binds Go and does not list
// itself in its own go declares less than its code reaches: coded
// capability-not-declared, with the help in that module, though the
// project's go lists it.
func TestCapabilityGo_ADependencyThatDoesNotDeclareItsGo(t *testing.T) {
	got, ds := heldBy(t, goProject(goApp+"go = [\"example.com/pg\"]\n", "\n[capabilities]\nstd = []\ngo = [\"example.com/other\"]\n"))
	want := "pg/db.nomi:3:8: example.com/pg binds Go through go:strings, and its own nomi.toml lets only example.com/other bind Go\n" +
		"pg/db.nomi:3:8: help: reached from `main`; `main` calls `upper`\n" +
		"pg/db.nomi:3:8: help: example.com/pg declares less than its code reaches, and the fix is in that module: add \"example.com/pg\" to go under [capabilities] in pg/nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Code != CapabilityNotDeclaredCode || !slices.Equal(ds[0].Args, []string{"go:strings", "example.com/pg", "example.com/other"}) {
		t.Errorf("%d diagnostics, the first %q with args %q; want one %s", len(ds), ds[0].Code, ds[0].Args, CapabilityNotDeclaredCode)
	}
}

// The project's own bindings are charged to its own module path, and a
// binding to Go's standard library counts as one to any other package.
func TestCapabilityGo_TheProjectsOwnBindings(t *testing.T) {
	files := map[string]string{
		"app/go.mod": "module example.com/app\n\ngo 1.27.0\n",
		"app/main.nomi": `import std::io

gopkg "strings" as gostrings
gopkg "example.com/app/ffi" as ffi

fn upper(s: String) -> String go gostrings.ToUpper

fn lower(s: String) -> String go ffi.Lower

fn main()
    io.print(upper(lower("hi")))
end
`,
	}
	opts := []Option{WithHosts(goTable("upper", "lower"))}
	files["app/nomi.toml"] = goApp + "go = [\"example.com/app\"]\n"
	if got, _ := heldBy(t, files, opts...); got != "" {
		t.Errorf("go lists example.com/app, and the program is refused:\n%s", got)
	}
	files["app/nomi.toml"] = goApp
	got, ds := heldBy(t, files, opts...)
	want := "app/main.nomi:6:4: example.com/app binds Go through go:strings, and nomi.toml lets no module bind Go\n" +
		"app/main.nomi:6:4: help: reached from `main`; `main` calls `upper`\n" +
		"app/main.nomi:6:4: help: add \"example.com/app\" to go under [capabilities] in nomi.toml, or remove the call\n" +
		"app/main.nomi:8:4: example.com/app binds Go through go:example.com/app/ffi, and nomi.toml lets no module bind Go\n" +
		"app/main.nomi:8:4: help: reached from `main`; `main` calls `lower`\n" +
		"app/main.nomi:8:4: help: add \"example.com/app\" to go under [capabilities] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 2 || !slices.Equal(ds[1].Args, []string{"go:example.com/app/ffi", "example.com/app"}) {
		t.Errorf("args %v; want go:example.com/app/ffi and example.com/app", ds)
	}
}

// require_module_lists asks for a std list from each module; a module in
// go with no std list still has none.
func TestCapabilityGo_BeingInGoIsNotAStdList(t *testing.T) {
	dir := writeProject(t, goProject(goApp+"go = [\"example.com/pg\"]\nrequire_module_lists = true\n", ""))
	p, err := Load(filepath.Join(dir, "app", "main.nomi"), WithHosts(goTable(goKeys...)))
	if err != nil {
		t.Fatal(err)
	}
	ds := p.capabilityListProblems()
	if len(ds) != 1 || !strings.Contains(ds.Error(), "[capabilities] require_module_lists: example.com/pg has no list") {
		t.Errorf("problems %v; want example.com/pg's missing list", ds)
	}
}

// With WithCapabilities, a Go binding is refused unless WithGo lists its
// module; WithGo alone holds only Go; a program LoadSource loads is named
// by the name it is given; and WithGo's names are held as go's entries.
func TestWithGo(t *testing.T) {
	src := "gopkg \"strings\" as gostrings\n\nfn upper(s: String) -> String go gostrings.ToUpper\n\nfn main()\n    _ = upper(\"hi\")\nend\n"
	table := WithHosts(goTable("upper"))
	if _, err := LoadSource("rule", src, table, WithCapabilities(), WithGo("rule")); err != nil {
		t.Errorf("WithGo lists the program, and the load refuses: %v", err)
	}
	_, err := LoadSource("rule", src, table, WithCapabilities())
	var ce *CapabilityError
	if !errors.As(err, &ce) || firstLine(err) != "rule.nomi:3:4: rule binds Go through go:strings, and the host lets no module bind Go" ||
		!strings.Contains(err.Error(), `help: add "rule" to the names the host passes to vmhost.WithGo, or remove the call`) {
		t.Errorf("WithCapabilities without WithGo: %v", err)
	}
	if _, err := LoadSource("rule", "import std::io\n\n"+strings.Replace(src, "    _ = upper", "    io.print(\"x\")\n    _ = upper", 1), table, WithGo("rule")); err != nil {
		t.Errorf("WithGo alone holds a std call: %v", err)
	}
	_, err = LoadSource("rule", src, table, WithGo())
	if !errors.As(err, &ce) || len(ce.Calls) != 1 || ce.Calls[0].Name != "go:strings" || ce.Calls[0].Binder != "rule" {
		t.Errorf("WithGo with no modules: %v", err)
	}
	for _, c := range []struct {
		modules []string
		want    string
	}{
		{[]string{"example.com/nothing"}, "vmhost.WithGo names example.com/nothing, which is neither this project nor a module go.mod requires (this project is rule)"},
		{[]string{"go:strings"}, `vmhost.WithGo: "go:strings" names a Go package; name the module that binds it, as go.mod writes it, such as "example.com/pg"`},
		{[]string{"rule", "rule"}, "vmhost.WithGo lists rule twice"},
	} {
		_, err := LoadSource("rule", src, table, WithGo(c.modules...))
		if err == nil || err.Error() != c.want {
			t.Errorf("WithGo(%q): %v\nwant: %s", c.modules, err, c.want)
		}
	}
}

// firstLine is the first line of err's text.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
