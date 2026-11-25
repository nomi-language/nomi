package vmhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/parser"
)

// subtreeFiles is an app with two dependencies, fetch and reports:
// fetch depends on core, which reads a file, and prints too; reports
// depends on core and calls it only when reportsUsesCore is set. The app's
// [dependencies] entries end with fetchKeys and reportsKeys (`effects =
// [...]`), and its own nomi.toml with tables.
func subtreeFiles(fetchKeys, reportsKeys, tables string, reportsUsesCore bool) map[string]string {
	entry := func(name, keys string) string {
		if keys == "" {
			return name + " = { dir = \"../" + name + "\" }\n"
		}
		return name + " = { dir = \"../" + name + "\", " + keys + " }\n"
	}
	daily, reportsToml := "pub fn daily() -> String\n    \"daily\"\nend\n", "[package]\nname = \"reports\"\n"
	if reportsUsesCore {
		daily = "import core::files\n\npub fn daily() -> String\n    files.read(\"daily.csv\")\nend\n"
		reportsToml += "\n[dependencies]\ncore = { dir = \"../core\" }\n"
	}
	return map[string]string{
		"app/nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\n" +
			entry("fetch", fetchKeys) + entry("reports", reportsKeys) + tables,
		"app/main.nomi": `import
    std::io
    fetch::client
    reports::daily
end

fn main()
    io.print(client.get("x"))
    io.print(daily.daily())
end
`,
		"fetch/nomi.toml": "[package]\nname = \"fetch\"\n\n[dependencies]\ncore = { dir = \"../core\" }\n",
		"fetch/client.nomi": `import
    std::io
    core::files
end

pub fn get(path: String) -> String
    io.print("get")
    files.read(path)
end
`,
		"reports/nomi.toml":  reportsToml,
		"reports/daily.nomi": daily,
		"core/nomi.toml":     "[package]\nname = \"core\"\n",
		"core/files.nomi": `import std::fs

pub fn read(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
	}
}

// throughLines is each call the project of files reaches from the roots
// `nomi check` holds that its nomi.toml's lists do not allow (Reach.NotAllowed):
// its name, the list and package, the entries the package is reached
// through, its site and what the list allows.
func throughLines(t *testing.T, files map[string]string) []string {
	t.Helper()
	r, err := Reached(filepath.Join(writeProject(t, files), "app", "main.nomi"))
	if err != nil {
		t.Fatalf("the program does not check: %v", err)
	}
	var out []string
	for _, n := range r.NotAllowed(r.Lists) {
		out = append(out, n.Name+" | "+string(n.List)+" "+n.Package+" through "+strings.Join(n.Through, ",")+" | "+n.Site+" | ["+strings.Join(n.Allowed, " ")+"]")
	}
	return out
}

// A [dependencies] entry's effects hold the package it names and every
// package the project reaches through it, a dependency of a dependency
// among them. A package reached through two entries may have what either
// allows, and one reached through an entry with no effects key is held by
// [effects.dependencies] alone on that path, so its entries hold nothing.
func TestEffectCallsNotAllowed_AnEntryCoversWhatItLeadsTo(t *testing.T) {
	cases := []struct {
		name                   string
		fetch, reports, tables string
		reportsUsesCore        bool
		want                   []string
	}{
		{name: "a dependency of a dependency, held by the entry that leads to it",
			fetch: `effects = ["std::io"]`, reports: `effects = []`,
			want: []string{"std::fs | dependencies core through fetch | files.nomi:4:17 | [std::io]"}},
		{name: "the entry allows it", fetch: `effects = ["std::io", "std::fs"]`, reports: `effects = []`},
		{name: "two entries, together allowing it",
			fetch: `effects = ["std::io", "std::fs"]`, reports: `effects = []`, reportsUsesCore: true},
		{name: "two entries, neither allowing it",
			fetch: `effects = ["std::io"]`, reports: `effects = []`, reportsUsesCore: true,
			want: []string{"std::fs | dependencies core through fetch,reports | files.nomi:4:17 | [std::io]"}},
		{name: "one of two entries with no effects key",
			fetch: `effects = ["std::io"]`, reportsUsesCore: true},
		{name: "an entry with no effects key, held by the ceiling",
			reports: `effects = []`, tables: "\n[effects.dependencies]\neffects = [\"std::io\"]\n",
			want: []string{"std::fs | effects.dependencies core through fetch | files.nomi:4:17 | [std::io]"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := throughLines(t, subtreeFiles(c.fetch, c.reports, c.tables, c.reportsUsesCore)); strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("not allowed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

// An entry's effects_in_tests adds to its effects for what the test cases
// reach in the code it covers, and for nothing main reaches.
func TestEffectCallsNotAllowed_AnEntrysEffectsInTests(t *testing.T) {
	files := subtreeFiles(`effects = ["std::io"], effects_in_tests = ["std::fs"]`, `effects = []`, "", false)
	files["app/main.nomi"] = strings.Replace(files["app/main.nomi"], "    io.print(client.get(\"x\"))\n", "", 1) +
		"\ntest \"gets\"\n    assert client.get(\"x\") == \"\"\nend\n"
	if got := throughLines(t, files); len(got) != 0 {
		t.Errorf("the tests are refused:\n%s\nwant nothing: effects_in_tests allows it", strings.Join(got, "\n"))
	}
	files["app/main.nomi"] += "\nfn later() -> String\n    client.get(\"y\")\nend\n"
	files["app/main.nomi"] = strings.Replace(files["app/main.nomi"], "    io.print(daily.daily())\n", "    io.print(daily.daily())\n    io.print(later())\n", 1)
	if got := throughLines(t, files); strings.Join(got, "\n") != "std::fs | dependencies core through fetch | files.nomi:4:17 | [std::io]" {
		t.Errorf("main's call is not held to effects alone:\n%s", strings.Join(got, "\n"))
	}
}

// A call refused over the entries a package is reached through names the
// package whose file holds it, the entries that lead to it and the entry
// the fix adds to.
func TestEffectDiagnostic_APackageReachedThroughEntries(t *testing.T) {
	got, ds := effectDiagnostics(t, "app/main.nomi", subtreeFiles(`effects = ["std::io"]`, `effects = []`, "", false))
	want := "core/files.nomi:4:5: core has the effect std::fs, and the entry for fetch under [dependencies], through which the project reaches it, allows it only std::io\n" +
		"core/files.nomi:4:5: help: reached from `main`; `main` calls `get`; `get` calls `read`\n" +
		"core/files.nomi:4:5: help: add \"std::fs\" to effects of fetch under [dependencies] in nomi.toml, or remove the call\n" +
		"app/main.nomi:8:14: note: your code has the effect std::fs in core here: `main` calls `get`"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Fix == nil || len(ds[0].Fix.Edits) != 1 ||
		ds[0].Fix.Edits[0].Text != "fetch = { dir = \"../fetch\", effects = [\"std::io\", \"std::fs\"] }\n" {
		t.Errorf("the fix: %#v; want the name added to fetch's effects", ds[0].Fix)
	}
	got, _ = effectDiagnostics(t, "app/main.nomi", subtreeFiles(`effects = ["std::io"]`, `effects = []`, "", true))
	want = "core/files.nomi:4:5: core has the effect std::fs, and the entries for fetch and reports under [dependencies], through which the project reaches it, allow it only std::io\n" +
		"core/files.nomi:4:5: help: reached from `main`; `main` calls `daily`; `daily` calls `read`\n" +
		"core/files.nomi:4:5: help: the project reaches core through fetch and reports, and core may have what any of their entries allows\n" +
		"core/files.nomi:4:5: help: add \"std::fs\" to effects of fetch under [dependencies] in nomi.toml, or remove the call\n"
	if !strings.HasPrefix(got, want) {
		t.Errorf("diagnostics:\n%s\nwant them to start:\n%s", got, want)
	}
}

// entries_required under [effects.dependencies] asks each [dependencies]
// entry the program loads files through for an effects key. Its fix
// writes the key with what the code the entry covers has, and with the fix
// applied the lists have no problem and the program passes them.
func TestEffectLists_EntriesRequiredFixWritesTheKey(t *testing.T) {
	files := subtreeFiles("", `effects = []`, "\n[effects.dependencies]\nentries_required = true\n", false)
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	ds := p.effectListProblems()
	want := "app/nomi.toml:10: [effects.dependencies] entries_required: the [dependencies] entry fetch has no effects key\n" +
		"app/nomi.toml:10: help: add effects = [...] to fetch under [dependencies] in nomi.toml, or give fetch an [effects.self] table in its own nomi.toml"
	if got := inProject(ds.Error(), dir); got != want {
		t.Fatalf("problems:\n%s\nwant:\n%s", got, want)
	}
	fix := ds[0].Fix
	if fix == nil || fix.Title != `Write effects = ["std::fs", "std::io"] on fetch under [dependencies]` {
		t.Fatalf("the fix: %#v", fix)
	}
	manifest := filepath.Join(dir, "app", "nomi.toml")
	text, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	fixed := parser.ApplyEdits(string(text), fix.Edits)
	if !strings.Contains(fixed, "fetch = { dir = \"../fetch\", effects = [\"std::fs\", \"std::io\"] }\n") {
		t.Fatalf("the fix wrote:\n%s", fixed)
	}
	if err := os.WriteFile(manifest, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Reached(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatalf("the program does not check with the fix applied: %v", err)
	}
	if len(r.ListProblems) > 0 || len(r.NotAllowed(r.Lists)) > 0 {
		t.Errorf("with the fix applied, %d list problems and %d calls refused; want none", len(r.ListProblems), len(r.NotAllowed(r.Lists)))
	}
}

// A dependency's own [dependencies] entries' effects keys are its limits
// for its own builds: a project that holds its dependencies does not read
// them, so a call they would refuse passes.
func TestEffectCallsNotAllowed_ADependencysOwnEntriesAreNotRead(t *testing.T) {
	files := subtreeFiles(`effects = ["std::io", "std::fs"]`, `effects = []`, "", false)
	files["fetch/nomi.toml"] = "[package]\nname = \"fetch\"\n\n[dependencies]\ncore = { dir = \"../core\", effects = [] }\n"
	if got := throughLines(t, files); len(got) != 0 {
		t.Errorf("refused:\n%s\nwant nothing: fetch's own entry for core is not the project's", strings.Join(got, "\n"))
	}
}
