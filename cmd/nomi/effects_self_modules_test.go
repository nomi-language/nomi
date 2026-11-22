package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// modulesProject is a project of four modules: main prints what pricing
// computes and what store loads, store reads a file, pricing reaches
// nothing, and the test file pricing_test reads the clock. manifest is its
// nomi.toml.
func modulesProject(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), manifest)
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import\n    std::io\n    pricing\n    store\nend\n\n"+
		"fn main()\n    io.print(\"${pricing.double(2)} ${store.load(\"prices.txt\")}\")\nend\n")
	mustWrite(t, filepath.Join(dir, "pricing.nomi"), "pub fn double(n: Int) -> Int\n    n * 2\nend\n")
	mustWrite(t, filepath.Join(dir, "store.nomi"), "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")
	mustWrite(t, filepath.Join(dir, "pricing_test.nomi"), "import\n    std::instant.{Instant}\n    pricing\nend\n\n"+
		"test \"doubles\"\n    _ = Instant.now()\n    assert pricing.double(1) == 2\nend\n")
	return dir
}

// modulesSelf is modulesProject's tightest [effects.self].
const modulesSelf = effectsModule + "\n[effects.self]\nstd = [\"std::fs\", \"std::io\"]\nstd_in_tests = [\"std::instant\"]\n"

// modulesEntries is the [effects.self.modules] --write writes for
// modulesProject.
const modulesEntries = "[effects.self.modules]\n\"main\" = [\"std::io\"]\n\"pricing\" = []\n\"pricing_test\" = [\"std::instant\"]\n\"store\" = [\"std::fs\"]\n"

// --write writes [effects.self.modules] when nomi.toml has the table
// or entries_required under [effects.self]: an entry for each module
// of the project its programs load, which nomi check then passes, and a
// second --write writes nothing. With neither it writes no such table.
func TestEffects_WriteSelfModulesWhenAsked(t *testing.T) {
	dir := modulesProject(t, modulesSelf)
	if r := runNomiIn(t, dir, "off", "effects", "--write"); r.exit != 0 {
		t.Fatalf("nomi effects --write:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "nomi.toml")); strings.Contains(string(data), "[effects.self.modules]") {
		t.Errorf("--write wrote a table of entries nomi.toml did not ask for:\n%s", data)
	}

	for name, manifest := range map[string]string{
		"the table":        modulesSelf + "\n[effects.self.modules]\n",
		"entries_required": strings.Replace(modulesSelf, "[effects.self]\n", "[effects.self]\nentries_required = true\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := modulesProject(t, manifest)
			write := runNomiIn(t, dir, "off", "effects", "--write")
			if write.exit != 0 || !strings.Contains(write.stdout, modulesEntries) ||
				!strings.Contains(write.stdout, `added    self.modules "pricing"       (an empty entry)`) {
				t.Errorf("nomi effects --write:\n%s\nwant it to write:\n%s", write, modulesEntries)
			}
			data, _ := os.ReadFile(filepath.Join(dir, "nomi.toml"))
			if !strings.Contains(string(data), "\n"+modulesEntries+"\n[effects.deps]\n") {
				t.Errorf("nomi.toml:\n%s\nwant it to hold, before [effects.deps]:\n%s", data, modulesEntries)
			}
			if check := runNomiIn(t, dir, "off", "check", "."); check.exit != 0 {
				t.Errorf("nomi check after --write:\n%s", check)
			}
			if again := runNomiIn(t, dir, "off", "effects", "--write"); again.stdout != "nomi.toml's [effects] tables are current\n" {
				t.Errorf("a second --write:\n%s", again)
			}
		})
	}
}

// --check holds the entries under [effects.self.modules] as it holds
// those under [effects.deps.modules]: a name an entry holds that its
// module does not reach is unused, and fails; a module the table gives no
// entry is a choice, unless entries_required asks every module for one.
// --format json names the list self.modules and the entry's module.
func TestEffects_CheckSelfModules(t *testing.T) {
	exact := modulesSelf + "\n" + modulesEntries + "\n[effects.deps]\nstd = []\n"
	dir := modulesProject(t, exact)
	if r := runNomiIn(t, dir, "off", "effects", "--check"); r.exit != 0 || r.stdout != "nomi.toml's [effects] tables allow exactly the effects the program has\n" {
		t.Errorf("--check with the tightest tables:\n%s", r)
	}

	mustWrite(t, filepath.Join(dir, "nomi.toml"), strings.Replace(exact, "\"pricing\" = []", "\"pricing\" = [\"std::fs\"]", 1))
	r := runNomiIn(t, dir, "off", "effects", "--check")
	want := "nomi.toml's [effects] tables differ:\n" +
		"  unused   self.modules \"pricing\"  std::fs\n" +
		"nomi check passes; an unused name allows an effect nothing here has\n"
	if r.exit != 1 || r.stdout != want {
		t.Errorf("--check with an unused name in an entry:\n%s\nwant stdout:\n%s", r, want)
	}
	r = runNomiIn(t, dir, "off", "effects", "--check", "--format", "json")
	var got effectsJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("--check --format json is not JSON: %v\n%s", err, r.stdout)
	}
	if r.exit != 1 || got.Table == nil || len(got.Table.Changes) != 1 ||
		got.Table.Changes[0] != (analysis.EffectChange{Change: "remove", List: "self.modules", Module: "pricing", Name: "std::fs"}) {
		t.Errorf("--check --format json with an unused name in an entry:\n%s", r)
	}

	withoutStore := strings.Replace(exact, "\"store\" = [\"std::fs\"]\n", "", 1)
	mustWrite(t, filepath.Join(dir, "nomi.toml"), withoutStore)
	if r := runNomiIn(t, dir, "off", "effects", "--check"); r.exit != 0 {
		t.Errorf("--check with no entry for store:\n%s", r)
	}
	mustWrite(t, filepath.Join(dir, "nomi.toml"), strings.Replace(withoutStore, "[effects.self]\n", "[effects.self]\nentries_required = true\n", 1))
	r = runNomiIn(t, dir, "off", "effects", "--check")
	if r.exit != 1 || !strings.Contains(r.stdout, "  missing  self.modules \"store\"  std::fs\n") ||
		!strings.Contains(r.stdout, "[effects.self] entries_required: store has no entry") {
		t.Errorf("--check with entries_required and no entry for store:\n%s", r)
	}
}

// A call over its module's entry is refused by check and run at the call,
// naming the module and its entry, and check --format json carries the
// code, the list self.modules and the module's key, and the fix that adds
// the name to the entry. --why names the module's calls by its key.
func TestEffects_AModuleOverItsEntry(t *testing.T) {
	dir := modulesProject(t, modulesSelf+"\n[effects.self.modules]\n\"store\" = []\n")
	refusal := "error: `store` has the effect std::fs, and its entry under [effects.self.modules] allows it no effects\n" +
		" --> store.nomi:4:5\n" +
		"  |\n" +
		"4 |     fs.read_text(path) |> Result.with_default(\"\")\n" +
		"  |     ^^\n" +
		"  = help: reached from `main`; `main` calls `load`\n" +
		"  = help: add \"std::fs\" to \"store\" under [effects.self.modules] in nomi.toml, or remove the call\n"
	if run := runNomiIn(t, dir, "off", "run", "main.nomi"); run.exit != 1 || run.stdout != "" || run.stderr != refusal {
		t.Errorf("nomi run: want exit 1, no output and the refusal\n%s\nwant stderr:\n%s", run, refusal)
	}
	check := runNomiIn(t, dir, "off", "check", "--format", "json", "main.nomi")
	if check.exit != 1 || !strings.Contains(check.stdout, `"code":"effect-not-allowed","args":["std::fs","self.modules","store"],"message":"`+"`store`"+` has the effect std::fs, and its entry under [effects.self.modules] allows it no effects"`) ||
		!strings.Contains(check.stdout, `"title":"Allow std::fs for store in nomi.toml"`) {
		t.Errorf("nomi check --format json: want the code, args and fix\n%s", check)
	}
	why := runNomiIn(t, dir, "off", "effects", "--why", "store")
	want := "1 call in store has an effect:\n  store.nomi:4:5: std::fs\n    reached from `main`; `main` calls `load`\n"
	if why.exit != 0 || why.stdout != want {
		t.Errorf("nomi effects --why store:\n%s\nwant stdout:\n%s", why, want)
	}
}
