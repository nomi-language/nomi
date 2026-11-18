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

// Each entry of the lists that names nothing a list can allow is a
// diagnostic at its line in nomi.toml: a name that is no std module, a Go
// package no binding imports, a module go.mod does not require. A valid
// name nothing reaches is not one.
func TestCapabilityLists_EntriesThatNameNothing(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"app/go.mod": "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml": `[module]
name = "app"
entry_points = ["main"]

[capabilities]
allow = ["std::fss", "std::process", "std::list"]
allow_in_tests = ["go:example.com/nothing"]

[capabilities.modules]
"example.com/missing" = []
"example.com/pricing" = ["std::nope"]
`,
		"app/main.nomi":      "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n",
		"pricing/go.mod":     "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": "pub fn add(a: Int, b: Int) -> Int\n    a + b\nend\n",
	})
	p, err := Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(p.capabilityListProblems().Error(), filepath.Join(dir, "app")+string(filepath.Separator), "")
	want := strings.Join([]string{
		"nomi.toml:6: [capabilities] allow names std::fss, which is not a std module",
		`nomi.toml:6: help: did you mean "std::fs"?`,
		"nomi.toml:6: [capabilities] allow names std::list, which is not a std module",
		`nomi.toml:6: help: did you mean "std::lists"?`,
		"nomi.toml:7: [capabilities] allow_in_tests names go:example.com/nothing, which no `gopkg` declaration in the program or its dependencies imports",
		"nomi.toml:7: help: the program and its dependencies bind no Go package",
		"nomi.toml:10: [capabilities.modules] names example.com/missing, which go.mod does not require",
		"nomi.toml:10: help: go.mod requires example.com/pricing",
		`nomi.toml:11: [capabilities] modules "example.com/pricing" names std::nope, which is not a std module`,
		"nomi.toml:11: help: name a std module as an import writes it, such as std::fs",
	}, "\n")
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
}
