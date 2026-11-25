package vmhost

import (
	"cmp"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// effectDiagnostics loads entry from a project of files and answers
// what `nomi check` holds it to (checkedRoots), with the project's
// directory left out of each path. The program must load.
func effectDiagnostics(t *testing.T, entry string, files map[string]string, opts ...Option) (string, Diagnostics) {
	t.Helper()
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, filepath.FromSlash(entry)), opts...)
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	err = p.effectError(p.checkedRoots()...)
	if err == nil {
		t.Fatal("the program is held to its lists and passes; want a refusal")
	}
	var ds Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("the refusal is %T, not diagnostics: %v", err, err)
	}
	return inProject(ds.Error(), dir), ds
}

// inProject is text, a refusal's, with the project's directory dir left out
// of each path and every path written with `/`, as the tests' wants are: on
// Windows a diagnostic names `app\main.nomi`.
func inProject(text, dir string) string {
	return filepath.ToSlash(strings.ReplaceAll(text, dir+string(filepath.Separator), ""))
}

const effectManifest = "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\n"

// Each kind of edge that makes a call count is one exact diagnostic: at
// the call, the headline naming who reaches what and what the list
// allows, the chain from a root in words, and the list to add the name to.
func TestEffectDiagnostic_EachKindOfReach(t *testing.T) {
	cases := []struct {
		name, main, want string
	}{
		{"a direct call", `import
    std::fs
    std::io
end

fn main()
    io.print(fs.read_text("notes.txt"))
end
`, `main.nomi:7:14: this package's own code has the effect std::fs, and [effects.self] allows it only std::io
main.nomi:7:14: help: reached from ` + "`main`" + `
main.nomi:7:14: help: add "std::fs" to std under [effects.self] in nomi.toml, or remove the call`},
		{"a function passed as a value", `import
    std::io
    std::process.{self, Command}
end

fn ls(name: String) -> String
    "${process.run(Command{program: name})}"
end

fn ignore(_f: (String) -> String) -> Int
    1
end

fn main()
    io.print(ignore(ls))
end
`, "main.nomi:7:8: this package's own code has the effect std::process, and [effects.self] allows it only std::io\n" +
			"main.nomi:7:8: help: reached from `main`; `main` uses `ls` as a value, which counts whether or not it is called\n" +
			"main.nomi:7:8: help: add \"std::process\" to std under [effects.self] in nomi.toml, or remove the call"},
		{"an interface dispatch", `import
    std::io
    std::process.{self, Command}
end

interface Runner
    fn go(r: self) -> String
end

struct Harmless
    x: Int
end

struct Shell
    program: String
end

impl Runner for Harmless
    fn go(r: Harmless) -> String
        "harmless ${r.x}"
    end
end

impl Runner for Shell
    fn go(r: Shell) -> String
        "${process.run(Command{program: r.program})}"
    end
end

fn use(r: Runner) -> String
    Runner.go(r)
end

fn main()
    io.print(use(Harmless{x: 1}))
end
`, "main.nomi:26:12: this package's own code has the effect std::process, and [effects.self] allows it only std::io\n" +
			"main.nomi:26:12: help: reached from `main`; `main` calls `use`; `use` calls `go` through an interface, and any implementation of it counts, including `Runner.go for Shell`\n" +
			"main.nomi:26:12: help: add \"std::process\" to std under [effects.self] in nomi.toml, or remove the call"},
		{"a Display body", `import
    std::io
    std::process.{self, Command}
end

struct Job
    program: String
end

impl Display for Job
    fn to_string(j: Job) -> String
        "${process.run(Command{program: j.program})}"
    end
end

fn main()
    io.print(show(Job{program: "ls"}))
end

fn show(d: Display) -> String
    "${d}"
end
`, "main.nomi:12:12: this package's own code has the effect std::process, and [effects.self] allows it only std::io\n" +
			"main.nomi:12:12: help: reached from `main`; `main` calls `show`; `show` displays a value that may be or hold a `Job`, so `impl Display for Job` counts\n" +
			"main.nomi:12:12: help: add \"std::process\" to std under [effects.self] in nomi.toml, or remove the call"},
		{"a once", `import
    std::fs
    std::io
end

fn load() -> String
    fs.read_text("config.txt") |> Result.with_default("")
end

once config = load()

fn main()
    io.print(config)
end
`, "main.nomi:7:5: this package's own code has the effect std::fs, and [effects.self] allows it only std::io\n" +
			"main.nomi:7:5: help: reached from `main`; `main` reads `once config`, so its initializer counts; `once config` calls `load`\n" +
			"main.nomi:7:5: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"},
		{"a boot that takes a Startup", `import std::io

struct App
    name: String
end

fn boot(startup: Startup) -> Result<App, String>
    Ok(App{name: List.first(startup.args) |> Option.with_default("app")})
end

fn main()
    io.print(App.name)
end
`, "main.nomi:7:4: this package's own code has the effect std::startup, and [effects.self] allows it only std::io\n" +
			"main.nomi:7:4: help: reached from the boot `boot`\n" +
			"main.nomi:7:4: help: add \"std::startup\" to std under [effects.self] in nomi.toml, or have the boot take no Startup"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ds := effectDiagnostics(t, "main.nomi", map[string]string{"nomi.toml": effectManifest, "main.nomi": c.main})
			if got != c.want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, c.want)
			}
			for _, d := range ds {
				if d.Code != EffectNotAllowedCode || len(d.Args) != 4 || d.Args[1] != "self" || d.Args[2] != "app" || d.Args[3] != "std::io" {
					t.Errorf("code %q args %q; want %q, the name, self, app and std::io", d.Code, d.Args, EffectNotAllowedCode)
				}
			}
		})
	}
}

// A dependency over its entry under [effects.deps.packages] is
// reported at the call in its file, naming the module; a call over
// [effects.deps]' ceiling too says so, and the help names both lists.
// A list that allows nothing says so.
func TestEffectDiagnostic_ADependencyOverItsEntry(t *testing.T) {
	got, ds := effectDiagnostics(t, "app/main.nomi", dependencyFiles(effectManifest+
		"\n[effects.deps]\nstd = [\"std::io\"]\n\n[effects.deps.packages]\n\"pricing\" = []\n", ""))
	want := "pricing/rules.nomi:4:5: pricing has the effect std::fs, and its entry under [effects.deps.packages] allows it no effects\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n" +
		"pricing/rules.nomi:4:5: help: std under [effects.deps] does not allow std::fs either\n" +
		"pricing/rules.nomi:4:5: help: add \"std::fs\" to \"pricing\" under [effects.deps.packages] and to std under [effects.deps] in nomi.toml, or remove the call" +
		"\napp/main.nomi:7:14: note: your code has the effect std::fs in pricing here: `main` calls `load`"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || !slices.Equal(ds[0].Args, []string{"std::fs", "deps.packages", "pricing"}) {
		t.Errorf("args %q, want the name, deps.packages and the module", ds[0].Args)
	}
}

// A dependency over [effects.deps]' ceiling is reported at the call in
// its file, naming the package and what the ceiling allows dependencies,
// and the help names deps' std, or std_in_tests for a test.
func TestEffectDiagnostic_ADependencyOverTheCeiling(t *testing.T) {
	got, ds := effectDiagnostics(t, "app/main.nomi", dependencyFiles(effectManifest+"\n[effects.deps]\nstd = [\"std::io\"]\n", ""))
	want := "pricing/rules.nomi:4:5: pricing has the effect std::fs, and [effects.deps] allows dependencies only std::io\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n" +
		"pricing/rules.nomi:4:5: help: add \"std::fs\" to std under [effects.deps] in nomi.toml, or remove the call" +
		"\napp/main.nomi:7:14: note: your code has the effect std::fs in pricing here: `main` calls `load`"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || !slices.Equal(ds[0].Args, []string{"std::fs", "deps", "pricing", "std::io"}) {
		t.Errorf("args %q, want the name, deps, the module and std::io", ds[0].Args)
	}
	files := dependencyFiles(effectManifest+"\n[effects.deps]\nstd = [\"std::io\"]\n", "")
	files["app/main.nomi"] += "\ntest \"loads\"\n    assert rules.load(\"x\") == \"\"\nend\n"
	files["app/main.nomi"] = strings.Replace(files["app/main.nomi"], "    io.print(rules.load(\"prices.txt\"))", "    io.print(\"hi\")", 1)
	got, _ = effectDiagnostics(t, "app/main.nomi", files)
	if !strings.Contains(got, `help: add "std::fs" to std_in_tests under [effects.deps] in nomi.toml, or remove the call`) {
		t.Errorf("a test's call over the ceiling does not name std_in_tests:\n%s", got)
	}
}

// Tests are held to self's std and std_in_tests, and the help names
// std_in_tests.
func TestEffectDiagnostic_FromATest(t *testing.T) {
	got, _ := effectDiagnostics(t, "main.nomi", map[string]string{
		"nomi.toml": effectManifest + "std_in_tests = [\"std::fs\"]\n",
		"main.nomi": `import
    std::io
    std::process.{self, Command}
end

fn main()
    io.print("hi")
end

test "runs"
    _ = process.run(Command{program: "ls"})
    assert 1 == 1
end
`,
	})
	want := "main.nomi:11:9: this package's tests have the effect std::process, and [effects.self] allows them only std::io, std::fs\n" +
		"main.nomi:11:9: help: reached from test \"runs\"\n" +
		"main.nomi:11:9: help: add \"std::process\" to std_in_tests under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// dependencyFiles is an app whose main prints what a dependency, which
// reads a file, answers, with app as the project's nomi.toml and table
// added to the dependency's own.
func dependencyFiles(app, table string) map[string]string {
	return map[string]string{
		"app/nomi.toml": app + "\n[dependencies]\npricing = { dir = \"../pricing\" }\n",
		"app/main.nomi": `import
    std::io
    pricing::rules
end

fn main()
    io.print(rules.load("prices.txt"))
end
`,
		"pricing/nomi.toml": "[package]\nname = \"pricing\"\n" + table,
		"pricing/rules.nomi": `import std::fs

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
	}
}

// A dependency over the [effects.self] its own nomi.toml declares is
// reported at the call in its file, coded effect-not-declared: the
// headline says the list is the module's own, and the help says the fix
// is in that package, at its nomi.toml, and, when the project's tables
// allow the call, nothing about them. When they do not allow it either,
// the help says which. Its own [effects.deps] does not widen it.
func TestEffectDiagnostic_ADependencyOverItsOwnList(t *testing.T) {
	ownTable := "\n[effects.self]\nstd = [\"std::io\"]\n"
	deps := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.deps]\nstd = [\"std::io\", \"std::fs\"]\n"
	own := "pricing/rules.nomi:4:5: pricing has the effect std::fs, and its own [effects.self] allows it only std::io\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n"
	fix := "pricing/rules.nomi:4:5: help: pricing declares fewer effects than its code has, and the fix is in that package: add \"std::fs\" to std under [effects.self] in pricing/nomi.toml, or remove the call" +
		"\napp/main.nomi:7:14: note: your code has the effect std::fs in pricing here: `main` calls `load`"
	cases := []struct {
		name, app, want string
		own             string
	}{
		{name: "the project allows it", app: deps, want: own + fix},
		{name: "its own [effects.deps] does not widen it", app: deps,
			own: ownTable + "\n[effects.deps]\nstd = [\"std::fs\"]\n", want: own + fix},
		{name: "the project's tables deny it too",
			app: effectManifest + "\n[effects.deps]\nstd = [\"std::io\"]\n\n[effects.deps.packages]\n\"pricing\" = [\"std::io\"]\n",
			want: own +
				"pricing/rules.nomi:4:5: help: the project's nomi.toml does not allow std::fs to it either; to allow it there, add \"std::fs\" to \"pricing\" under [effects.deps.packages] and to std under [effects.deps]\n" +
				fix},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The sibling directory's nomi.toml is named by its absolute
			// path, which effectDiagnostics cuts to pricing/nomi.toml.
			got, ds := effectDiagnostics(t, "app/main.nomi", dependencyFiles(c.app, cmp.Or(c.own, ownTable)))
			if got != c.want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, c.want)
			}
			if len(ds) != 1 || ds[0].Code != EffectNotDeclaredCode || !slices.Equal(ds[0].Args, []string{"std::fs", "self", "pricing", "std::io"}) {
				t.Errorf("%d diagnostics, the first coded %q with args %q; want one %s with the name, self, the module and std::io", len(ds), ds[0].Code, ds[0].Args, EffectNotDeclaredCode)
			}
		})
	}
}

// listProblems loads app/main.nomi from a project of files and answers its
// lists' problems, with the project's directory left out of each path.
func listProblems(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	ds := p.effectListProblems()
	if len(ds) == 0 {
		return ""
	}
	return inProject(ds.Error(), dir)
}

// A name in a dependency's own [effects.self] std that names nothing
// is reported at its line in the dependency's nomi.toml, as one in the
// project's is; with no project [effects.deps] the dependency's is not
// read at all.
func TestEffectLists_ADependencysOwnEntries(t *testing.T) {
	table := "\n[effects.self]\nstd = [\"std::fss\"]\n"
	got := listProblems(t, dependencyFiles(effectManifest+"\n[effects.deps]\n", table))
	want := "pricing/nomi.toml:5: [effects.self] std names std::fss, which is not a std module\n" +
		`pricing/nomi.toml:5: help: did you mean "std::fs"?`
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
	for _, app := range []string{"[package]\nname = \"app\"\nentry_points = [\"main\"]\n", effectManifest} {
		if got := listProblems(t, dependencyFiles(app, table)); got != "" {
			t.Errorf("with no project [effects.deps], problems:\n%s\nwant none", got)
		}
	}
}

// entries_required makes each dependency the program's code comes from have
// an entry: a dependency with neither an entry under the project's
// [effects.deps.packages] nor an [effects.self] of its own is an
// error at the key, naming both fixes. Either satisfies it, its own
// [effects.deps] does not, and the key false or absent requires
// nothing.
func TestEffectLists_EntriesRequired(t *testing.T) {
	app := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.deps]\nstd = [\"std::io\", \"std::fs\"]\nentries_required = true\n"
	want := "app/nomi.toml:7: [effects.deps] entries_required: pricing has no entry\n" +
		"app/nomi.toml:7: help: add \"pricing\" under [effects.deps.packages] in nomi.toml, or give pricing an [effects.self] table in its own nomi.toml"
	for _, table := range []string{"", "\n[effects.deps]\nstd = [\"std::fs\"]\n"} {
		if got := listProblems(t, dependencyFiles(app, table)); got != want {
			t.Errorf("problems with %q in its own nomi.toml:\n%s\nwant:\n%s", table, got, want)
		}
	}
	for _, c := range []struct{ name, app, table string }{
		{"a project entry", app + "\n[effects.deps.packages]\n\"pricing\" = [\"std::fs\"]\n", ""},
		{"its own table", app, "\n[effects.self]\nstd = [\"std::fs\"]\n"},
		{"the key false", strings.Replace(app, "true", "false", 1), ""},
		{"the key absent", strings.Replace(app, "entries_required = true\n", "", 1), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := listProblems(t, dependencyFiles(c.app, c.table)); got != "" {
				t.Errorf("problems:\n%s\nwant none", got)
			}
		})
	}
}
