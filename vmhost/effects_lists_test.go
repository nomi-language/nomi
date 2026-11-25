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

// Each name of the lists that names nothing a list can allow is a
// diagnostic at its line in nomi.toml: a name that is no std module, in a
// table or in a [dependencies] entry's effects keys. A valid name nothing
// reaches is not one.
func TestEffectLists_EntriesThatNameNothing(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"app/nomi.toml": `[package]
name = "app"
entry_points = ["main"]

[effects.self]
effects = ["std::fss", "std::process"]
effects_in_tests = ["std::list"]

[effects.dependencies]

[dependencies]
pricing = { dir = "../pricing", effects = ["std::nope"], effects_in_tests = ["std::fs::testin"] }
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
		"nomi.toml:6: [effects.self] effects names std::fss, which is not a std module",
		`nomi.toml:6: help: did you mean "std::fs"?`,
		"nomi.toml:7: [effects.self] effects_in_tests names std::list, which is not a std module",
		`nomi.toml:7: help: did you mean "std::lists"?`,
		"nomi.toml:12: [dependencies] pricing.effects names std::nope, which is not a std module",
		"nomi.toml:12: help: name a std module as an import writes it, such as std::fs",
		"nomi.toml:12: [dependencies] pricing.effects_in_tests names std::fs::testin, which is not a std module",
		`nomi.toml:12: help: did you mean "std::fs::testing"?`,
	}, "\n")
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
}
