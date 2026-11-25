package vmhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProject writes files under a fresh directory and answers it.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Each entry of the tables that names nothing a list can allow is a
// diagnostic at its line in nomi.toml: a name that is no std module, a
// key under [effects.deps.packages] that is not a dependency's name, and
// the project's own name there, whose code [effects.self] holds. A valid
// name nothing reaches is not one.
func TestEffectLists_EntriesThatNameNothing(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"app/nomi.toml": `[package]
name = "app"
entry_points = ["main"]

[effects.self]
std = ["std::fss", "std::process"]
std_in_tests = ["std::list"]

[effects.deps]

[effects.deps.packages]
"app" = ["std::io"]
"missing" = []
"pricing" = ["std::nope"]

[dependencies]
pricing = { dir = "../pricing" }
`,
		"app/main.nomi":      "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n",
		"pricing/nomi.toml":  "[package]\nname = \"pricing\"\n",
		"pricing/rules.nomi": "pub fn add(a: Int, b: Int) -> Int\n    a + b\nend\n",
	})
	p, err := Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	got := inProject(p.effectListProblems().Error(), filepath.Join(dir, "app"))
	want := strings.Join([]string{
		"nomi.toml:6: [effects.self] std names std::fss, which is not a std module",
		`nomi.toml:6: help: did you mean "std::fs"?`,
		"nomi.toml:7: [effects.self] std_in_tests names std::list, which is not a std module",
		`nomi.toml:7: help: did you mean "std::lists"?`,
		"nomi.toml:12: [effects.deps.packages] names app, which is this project",
		"nomi.toml:12: help: [effects.deps] holds the packages it depends on; its own code is held by [effects.self]",
		"nomi.toml:13: [effects.deps.packages] names missing, which is not a dependency of this project",
		"nomi.toml:13: help: its dependencies are pricing",
		`nomi.toml:14: [effects.deps.packages] "pricing" names std::nope, which is not a std module`,
		"nomi.toml:14: help: name a std module as an import writes it, such as std::fs",
	}, "\n")
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
}

// A dependency's entry is keyed by its name in the program, the key the
// project's [dependencies] gives it. An entry keyed by the package's path,
// or by its own [package].name when that differs, is an error at its line
// whose help names the key to write.
func TestEffectLists_AnEntryKeyedByAModulePath(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"app/nomi.toml": `[package]
name = "app"
entry_points = ["main"]

[effects.deps.packages]
"example.com/pricing" = []
"rules" = []
"prices" = []

[dependencies]
prices = { repo = "example.com/pricing", version = "v1.0.0", dir = "../pricing" }
`,
		"app/main.nomi":      "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n",
		"pricing/nomi.toml":  "[package]\nname = \"rules\"\n",
		"pricing/rules.nomi": "pub fn add(a: Int, b: Int) -> Int\n    a + b\nend\n",
	})
	p, err := Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	got := inProject(p.effectListProblems().Error(), filepath.Join(dir, "app"))
	want := strings.Join([]string{
		"nomi.toml:6: [effects.deps.packages] names example.com/pricing, which is not a dependency's name in this project",
		`nomi.toml:6: help: did you mean "prices"? a dependency is named by its key under [dependencies]`,
		"nomi.toml:7: [effects.deps.packages] names rules, which is not a dependency's name in this project",
		`nomi.toml:7: help: did you mean "prices"? a dependency is named by its key under [dependencies]`,
	}, "\n")
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
}
