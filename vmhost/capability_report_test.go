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

const capabilityManifest = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nallow = [\"std::io\"]\n"

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
`, `main.nomi:7:14: this program reaches std::fs, and nomi.toml allows only std::io
main.nomi:7:14: help: reached from ` + "`main`" + `
main.nomi:7:14: help: add "std::fs" to allow under [capabilities] in nomi.toml, or remove the call`},
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
`, "main.nomi:7:8: this program reaches std::process, and nomi.toml allows only std::io\n" +
			"main.nomi:7:8: help: reached from `main`; `main` uses `ls` as a value, which counts whether or not it is called\n" +
			"main.nomi:7:8: help: add \"std::process\" to allow under [capabilities] in nomi.toml, or remove the call"},
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
`, "main.nomi:26:12: this program reaches std::process, and nomi.toml allows only std::io\n" +
			"main.nomi:26:12: help: reached from `main`; `main` calls `use`; `use` calls `go` through an interface, and any implementation of it counts, including `Shell.go`\n" +
			"main.nomi:26:12: help: add \"std::process\" to allow under [capabilities] in nomi.toml, or remove the call"},
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
`, "main.nomi:12:12: this program reaches std::process, and nomi.toml allows only std::io\n" +
			"main.nomi:12:12: help: reached from `main`; `main` displays a value, and any value may be displayed, so `impl Display for Job` counts\n" +
			"main.nomi:12:12: help: add \"std::process\" to allow under [capabilities] in nomi.toml, or remove the call"},
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
`, "main.nomi:7:5: this program reaches std::fs, and nomi.toml allows only std::io\n" +
			"main.nomi:7:5: help: reached from `main`; `main` reads `once config`, so its initializer counts; `once config` calls `load`\n" +
			"main.nomi:7:5: help: add \"std::fs\" to allow under [capabilities] in nomi.toml, or remove the call"},
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
`, "main.nomi:7:4: this program reaches std::startup, and nomi.toml allows only std::io\n" +
			"main.nomi:7:4: help: reached from the boot `boot`\n" +
			"main.nomi:7:4: help: add \"std::startup\" to allow under [capabilities] in nomi.toml, or have the boot take no Startup"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ds := capabilityDiagnostics(t, "main.nomi", map[string]string{"nomi.toml": capabilityManifest, "main.nomi": c.main})
			if got != c.want {
				t.Errorf("diagnostics:\n%s\nwant:\n%s", got, c.want)
			}
			for _, d := range ds {
				if d.Code != CapabilityNotAllowedCode || len(d.Args) != 3 || d.Args[1] != "" || d.Args[2] != "std::io" {
					t.Errorf("code %q args %q; want %q, the name, \"\" and std::io", d.Code, d.Args, CapabilityNotAllowedCode)
				}
			}
		})
	}
}

// A dependency over its own list is reported at the call in its file,
// naming the module; a call over the program's list too says so, and the
// help names both lists. A list that allows nothing says so.
func TestCapabilityDiagnostic_ADependencyOverItsList(t *testing.T) {
	got, ds := capabilityDiagnostics(t, "app/main.nomi", map[string]string{
		"app/go.mod":    "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml": capabilityManifest + "\n[capabilities.modules]\n\"example.com/pricing\" = []\n",
		"app/main.nomi": `import
    std::io
    pricing::rules
end

fn main()
    io.print(rules.load("prices.txt"))
end
`,
		"pricing/go.mod":    "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml": "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": `import std::fs

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
	})
	want := "pricing/rules.nomi:4:5: example.com/pricing reaches std::fs, and nomi.toml allows it nothing outside the program\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n" +
		"pricing/rules.nomi:4:5: help: the program's own list, allow under [capabilities], does not allow std::fs either\n" +
		"pricing/rules.nomi:4:5: help: add \"std::fs\" to \"example.com/pricing\" under [capabilities.modules] in nomi.toml and to allow under [capabilities], or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || !slices.Equal(ds[0].Args, []string{"std::fs", "example.com/pricing"}) {
		t.Errorf("args %q, want the name and the module", ds[0].Args)
	}
}

// A Go binding is named by the package its gopkg imports. The table stands
// in for the FFI wrapper's, so no Go is built.
func TestCapabilityDiagnostic_AGoBinding(t *testing.T) {
	table := HostTable(func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{"upper": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) { return args[0], nil }}, nil
	})
	got, ds := capabilityDiagnostics(t, "main.nomi", map[string]string{
		"go.mod":    "module app\n\ngo 1.27.0\n",
		"nomi.toml": capabilityManifest,
		"main.nomi": `import std::io

gopkg "app/ffi" as ffi

fn upper(s: String) -> String go ffi.Upper

fn main()
    io.print(upper("hi"))
end
`,
	}, WithHosts(table))
	want := "main.nomi:5:4: this program reaches go:app/ffi, and nomi.toml allows only std::io\n" +
		"main.nomi:5:4: help: reached from `main`; `main` calls `upper`\n" +
		"main.nomi:5:4: help: add \"go:app/ffi\" to allow under [capabilities] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Args[0] != "go:app/ffi" {
		t.Errorf("args %q, want go:app/ffi first", ds[0].Args)
	}
}

// Tests are held to allow and allow_in_tests, and the help names
// allow_in_tests.
func TestCapabilityDiagnostic_FromATest(t *testing.T) {
	got, _ := capabilityDiagnostics(t, "main.nomi", map[string]string{
		"nomi.toml": capabilityManifest + "allow_in_tests = [\"std::fs\"]\n",
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
	want := "main.nomi:11:9: this program's tests reach std::process, and nomi.toml allows them only std::io, std::fs\n" +
		"main.nomi:11:9: help: reached from test \"runs\"\n" +
		"main.nomi:11:9: help: add \"std::process\" to allow_in_tests in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}
