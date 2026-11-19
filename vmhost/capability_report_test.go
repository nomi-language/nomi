package vmhost

import (
	"cmp"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// capabilityDiagnostics loads entry from a project of files and answers
// what `nomi check` holds it to (checkedRoots), with the project's
// directory left out of each path. The program must load.
func capabilityDiagnostics(t *testing.T, entry string, files map[string]string, opts ...Option) (string, Diagnostics) {
	t.Helper()
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, filepath.FromSlash(entry)), opts...)
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	err = p.capabilityError(p.checkedRoots()...)
	if err == nil {
		t.Fatal("the program is held to its lists and passes; want a refusal")
	}
	var ds Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("the refusal is %T, not diagnostics: %v", err, err)
	}
	return strings.ReplaceAll(ds.Error(), dir+string(filepath.Separator), ""), ds
}

const capabilityManifest = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\nstd = [\"std::io\"]\n"

// Each kind of edge that makes a call count is one exact diagnostic: at
// the call, the headline naming who reaches what and what the list
// allows, the chain from a root in words, and the list to add the name to.
func TestCapabilityDiagnostic_EachKindOfReach(t *testing.T) {
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
`, `main.nomi:7:14: this module's own code reaches std::fs, and [capabilities.self] allows it only std::io
main.nomi:7:14: help: reached from ` + "`main`" + `
main.nomi:7:14: help: add "std::fs" to std under [capabilities.self] in nomi.toml, or remove the call`},
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
`, "main.nomi:7:8: this module's own code reaches std::process, and [capabilities.self] allows it only std::io\n" +
			"main.nomi:7:8: help: reached from `main`; `main` uses `ls` as a value, which counts whether or not it is called\n" +
			"main.nomi:7:8: help: add \"std::process\" to std under [capabilities.self] in nomi.toml, or remove the call"},
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
`, "main.nomi:26:12: this module's own code reaches std::process, and [capabilities.self] allows it only std::io\n" +
			"main.nomi:26:12: help: reached from `main`; `main` calls `use`; `use` calls `go` through an interface, and any implementation of it counts, including `Shell.go`\n" +
			"main.nomi:26:12: help: add \"std::process\" to std under [capabilities.self] in nomi.toml, or remove the call"},
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
    io.print([1, 2])
end
`, "main.nomi:12:12: this module's own code reaches std::process, and [capabilities.self] allows it only std::io\n" +
			"main.nomi:12:12: help: reached from `main`; `main` displays a value, and any value may be displayed, so `impl Display for Job` counts\n" +
			"main.nomi:12:12: help: add \"std::process\" to std under [capabilities.self] in nomi.toml, or remove the call"},
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
`, "main.nomi:7:5: this module's own code reaches std::fs, and [capabilities.self] allows it only std::io\n" +
			"main.nomi:7:5: help: reached from `main`; `main` reads `once config`, so its initializer counts; `once config` calls `load`\n" +
			"main.nomi:7:5: help: add \"std::fs\" to std under [capabilities.self] in nomi.toml, or remove the call"},
		{"a boot that takes a Startup", `import std::io

struct App
    name: String
end

fn boot(startup: Startup) -> Result<App, String>
    Ok(App{name: List.first(startup.args) |> Maybe.with_default("app")})
end

fn main()
    io.print(App.name)
end
`, "main.nomi:7:4: this module's own code reaches std::startup, and [capabilities.self] allows it only std::io\n" +
			"main.nomi:7:4: help: reached from the boot `boot`\n" +
			"main.nomi:7:4: help: add \"std::startup\" to std under [capabilities.self] in nomi.toml, or have the boot take no Startup"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ds := capabilityDiagnostics(t, "main.nomi", map[string]string{"nomi.toml": capabilityManifest, "main.nomi": c.main})
			if got != c.want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, c.want)
			}
			for _, d := range ds {
				if d.Code != CapabilityNotAllowedCode || len(d.Args) != 4 || d.Args[1] != "self" || d.Args[2] != "app" || d.Args[3] != "std::io" {
					t.Errorf("code %q args %q; want %q, the name, self, app and std::io", d.Code, d.Args, CapabilityNotAllowedCode)
				}
			}
		})
	}
}

// A dependency over its entry under [capabilities.deps.modules] is
// reported at the call in its file, naming the module; a call over
// [capabilities.deps]' ceiling too says so, and the help names both lists.
// A list that allows nothing says so.
func TestCapabilityDiagnostic_ADependencyOverItsEntry(t *testing.T) {
	got, ds := capabilityDiagnostics(t, "app/main.nomi", dependencyFiles(capabilityManifest+
		"\n[capabilities.deps]\nstd = [\"std::io\"]\n\n[capabilities.deps.modules]\n\"example.com/pricing\" = []\n", ""))
	want := "pricing/rules.nomi:4:5: example.com/pricing reaches std::fs, and its entry under [capabilities.deps.modules] allows it nothing outside the program\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n" +
		"pricing/rules.nomi:4:5: help: std under [capabilities.deps] does not allow std::fs either\n" +
		"pricing/rules.nomi:4:5: help: add \"std::fs\" to \"example.com/pricing\" under [capabilities.deps.modules] and to std under [capabilities.deps] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || !slices.Equal(ds[0].Args, []string{"std::fs", "deps.modules", "example.com/pricing"}) {
		t.Errorf("args %q, want the name, deps.modules and the module", ds[0].Args)
	}
}

// A dependency over [capabilities.deps]' ceiling is reported at the call in
// its file, naming the module and what the ceiling allows dependencies,
// and the help names deps' std, or std_in_tests for a test.
func TestCapabilityDiagnostic_ADependencyOverTheCeiling(t *testing.T) {
	got, ds := capabilityDiagnostics(t, "app/main.nomi", dependencyFiles(capabilityManifest+"\n[capabilities.deps]\nstd = [\"std::io\"]\n", ""))
	want := "pricing/rules.nomi:4:5: example.com/pricing reaches std::fs, and [capabilities.deps] allows dependencies only std::io\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n" +
		"pricing/rules.nomi:4:5: help: add \"std::fs\" to std under [capabilities.deps] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || !slices.Equal(ds[0].Args, []string{"std::fs", "deps", "example.com/pricing", "std::io"}) {
		t.Errorf("args %q, want the name, deps, the module and std::io", ds[0].Args)
	}
	files := dependencyFiles(capabilityManifest+"\n[capabilities.deps]\nstd = [\"std::io\"]\n", "")
	files["app/main.nomi"] += "\ntest \"loads\"\n    assert rules.load(\"x\") == \"\"\nend\n"
	files["app/main.nomi"] = strings.Replace(files["app/main.nomi"], "    io.print(rules.load(\"prices.txt\"))", "    io.print(\"hi\")", 1)
	got, _ = capabilityDiagnostics(t, "app/main.nomi", files)
	if !strings.Contains(got, `help: add "std::fs" to std_in_tests under [capabilities.deps] in nomi.toml, or remove the call`) {
		t.Errorf("a test's call over the ceiling does not name std_in_tests:\n%s", got)
	}
}

// Tests are held to self's std and std_in_tests, and the help names
// std_in_tests.
func TestCapabilityDiagnostic_FromATest(t *testing.T) {
	got, _ := capabilityDiagnostics(t, "main.nomi", map[string]string{
		"nomi.toml": capabilityManifest + "std_in_tests = [\"std::fs\"]\n",
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
	want := "main.nomi:11:9: this module's tests reach std::process, and [capabilities.self] allows them only std::io, std::fs\n" +
		"main.nomi:11:9: help: reached from test \"runs\"\n" +
		"main.nomi:11:9: help: add \"std::process\" to std_in_tests under [capabilities.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// dependencyFiles is an app whose main prints what a dependency, which
// reads a file, answers, with app as the project's nomi.toml and table
// added to the dependency's own.
func dependencyFiles(app, table string) map[string]string {
	return map[string]string{
		"app/go.mod":    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml": app,
		"app/main.nomi": `import
    std::io
    pricing::rules
end

fn main()
    io.print(rules.load("prices.txt"))
end
`,
		"pricing/go.mod":    "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml": "[module]\nname = \"pricing\"\n" + table,
		"pricing/rules.nomi": `import std::fs

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
	}
}

// A dependency over the [capabilities.self] its own nomi.toml declares is
// reported at the call in its file, coded capability-not-declared: the
// headline says the list is the module's own, and the help says the fix
// is in that module, at its nomi.toml, and, when the project's tables
// allow the call, nothing about them. When they do not allow it either,
// the help says which. Its own [capabilities.deps] does not widen it.
func TestCapabilityDiagnostic_ADependencyOverItsOwnList(t *testing.T) {
	ownTable := "\n[capabilities.self]\nstd = [\"std::io\"]\n"
	deps := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.deps]\nstd = [\"std::io\", \"std::fs\"]\n"
	own := "pricing/rules.nomi:4:5: example.com/pricing reaches std::fs, and its own [capabilities.self] allows it only std::io\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n"
	fix := "pricing/rules.nomi:4:5: help: example.com/pricing declares less than its code reaches, and the fix is in that module: add \"std::fs\" to std under [capabilities.self] in pricing/nomi.toml, or remove the call"
	cases := []struct {
		name, app, want string
		own             string
	}{
		{name: "the project allows it", app: deps, want: own + fix},
		{name: "its own [capabilities.deps] does not widen it", app: deps,
			own: ownTable + "\n[capabilities.deps]\nstd = [\"std::fs\"]\n", want: own + fix},
		{name: "the project's tables deny it too",
			app: capabilityManifest + "\n[capabilities.deps]\nstd = [\"std::io\"]\n\n[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::io\"]\n",
			want: own +
				"pricing/rules.nomi:4:5: help: the project's nomi.toml does not allow std::fs to it either; to allow it there, add \"std::fs\" to \"example.com/pricing\" under [capabilities.deps.modules] and to std under [capabilities.deps]\n" +
				fix},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The sibling directory's nomi.toml is named by its absolute
			// path, which capabilityDiagnostics cuts to pricing/nomi.toml.
			got, ds := capabilityDiagnostics(t, "app/main.nomi", dependencyFiles(c.app, cmp.Or(c.own, ownTable)))
			if got != c.want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, c.want)
			}
			if len(ds) != 1 || ds[0].Code != CapabilityNotDeclaredCode || !slices.Equal(ds[0].Args, []string{"std::fs", "self", "example.com/pricing", "std::io"}) {
				t.Errorf("%d diagnostics, the first coded %q with args %q; want one %s with the name, self, the module and std::io", len(ds), ds[0].Code, ds[0].Args, CapabilityNotDeclaredCode)
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
	ds := p.capabilityListProblems()
	if len(ds) == 0 {
		return ""
	}
	return strings.ReplaceAll(ds.Error(), dir+string(filepath.Separator), "")
}

// A name in a dependency's own [capabilities.self] std that names nothing
// is reported at its line in the dependency's nomi.toml, as one in the
// project's is; with no project [capabilities.deps] the dependency's is not
// read at all.
func TestCapabilityLists_ADependencysOwnEntries(t *testing.T) {
	table := "\n[capabilities.self]\nstd = [\"std::fss\"]\n"
	got := listProblems(t, dependencyFiles(capabilityManifest+"\n[capabilities.deps]\n", table))
	want := "pricing/nomi.toml:5: [capabilities.self] std names std::fss, which is not a std module\n" +
		`pricing/nomi.toml:5: help: did you mean "std::fs"?`
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
	for _, app := range []string{"[module]\nname = \"app\"\nentry_points = [\"main\"]\n", capabilityManifest} {
		if got := listProblems(t, dependencyFiles(app, table)); got != "" {
			t.Errorf("with no project [capabilities.deps], problems:\n%s\nwant none", got)
		}
	}
}

// entries_required makes each dependency the program's code comes from have
// an entry: a dependency with neither an entry under the project's
// [capabilities.deps.modules] nor a [capabilities.self] of its own is an
// error at the key, naming both fixes. Either satisfies it, its own
// [capabilities.deps] does not, and the key false or absent requires
// nothing.
func TestCapabilityLists_EntriesRequired(t *testing.T) {
	app := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.deps]\nstd = [\"std::io\", \"std::fs\"]\nentries_required = true\n"
	want := "app/nomi.toml:7: [capabilities.deps] entries_required: example.com/pricing has no entry\n" +
		"app/nomi.toml:7: help: add \"example.com/pricing\" under [capabilities.deps.modules] in nomi.toml, or give example.com/pricing a [capabilities.self] table in its own nomi.toml"
	for _, table := range []string{"", "\n[capabilities.deps]\nstd = [\"std::fs\"]\n"} {
		if got := listProblems(t, dependencyFiles(app, table)); got != want {
			t.Errorf("problems with %q in its own nomi.toml:\n%s\nwant:\n%s", table, got, want)
		}
	}
	for _, c := range []struct{ name, app, table string }{
		{"a project entry", app + "\n[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n", ""},
		{"its own table", app, "\n[capabilities.self]\nstd = [\"std::fs\"]\n"},
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
