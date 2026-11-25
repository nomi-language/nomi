package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// effectsProject is an app whose main
// prints and reads a file through a dependency, pricing, and
// whose test reads the clock; a second dependency, pure, is
// imported and reaches nothing. manifest is the app's nomi.toml, which names
// both (effectsApp).
func effectsProject(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest)
	mustWrite(t, filepath.Join(app, "main.nomi"), `import
    std::instant.{Instant}
    std::io
    pricing::rules
    pure::calc
end

fn main()
    io.print(rules.load("prices.txt"))
    io.print("${calc.double(2)}")
end

test "the clock moves"
    _ = Instant.now()
    assert 1 == 1
end
`)
	mustWrite(t, filepath.Join(dir, "pricing", "nomi.toml"), "[package]\nname = \"pricing\"\n")
	mustWrite(t, filepath.Join(dir, "pricing", "rules.nomi"), "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")
	mustWrite(t, filepath.Join(dir, "pure", "nomi.toml"), "[package]\nname = \"pure\"\n")
	mustWrite(t, filepath.Join(dir, "pure", "calc.nomi"), "pub fn double(n: Int) -> Int\n    n * 2\nend\n")
	return app
}

const effectsModule = "[package]\nname = \"app\"\nentry_points = [\"main\"]\n"

// effectsApp is effectsProject's nomi.toml with no effects lists: the
// package, and its two dependencies.
const effectsApp = effectsModule + "\n[dependencies]\npricing = { dir = \"../pricing\" }\npure = { dir = \"../pure\" }\n"

// What effectsProject reaches, as the report prints the tightest
// tables: self's lists, then the dependencies' with what the code of each
// [dependencies] entry reaches.
const effectsReached = `self: app
  effects            std::io
  effects_in_tests   std::instant

dependencies
  effects            std::fs
  effects_in_tests   (none)
  entries
    pricing
      std::fs
    pure
      no effects
`

// effectsEntries is effectsApp's [dependencies] with the effects keys
// --write writes on its entries for effectsProject.
const effectsEntries = `[dependencies]
pricing = { dir = "../pricing", effects = ["std::fs"] }
pure = { dir = "../pure", effects = [] }
`

// The tables --write writes for effectsProject.
const effectsTables = `[effects.self]
effects = ["std::io"]
effects_in_tests = ["std::instant"]

[effects.dependencies]
effects = ["std::fs"]
`

// effectsWritten is what --write prints of what it wrote for
// effectsProject, and effectsTight effectsProject's nomi.toml after it.
const (
	effectsWritten = effectsEntries + "\n" + effectsTables
	effectsTight   = effectsModule + "\n" + effectsWritten
)

// changeLine is one line of the report's differences, its label padded to
// the width of `dependencies.effects "pricing"`, the longest label
// effectsProject's reports print, which those that change an entry's
// effects key have.
func changeLine(word, label, name string) string {
	return changeLineIn(30, word, label, name)
}

// changeLineIn is changeLine with its label padded to width, the longest
// label of its report.
func changeLineIn(width int, word, label, name string) string {
	return fmt.Sprintf("  %-8s %-*s  %s\n", word, width, label, name)
}

// With no lists, the command prints what the program reaches and says
// nothing holds it; --write writes the tightest lists, the [dependencies]
// entries' effects keys and the tables at the end of nomi.toml, which nomi
// check then passes, and a second --write writes nothing and says the
// tables are current.
func TestEffects_CommandWritesATableTheCheckPasses(t *testing.T) {
	app := effectsProject(t, effectsApp)
	plain := runNomiIn(t, app, "off", "effects")
	want := effectsReached + "\nnomi.toml has no [effects.self] or [effects.dependencies] table; nomi effects --write writes these\n"
	if plain.exit != 0 || plain.stdout != want {
		t.Errorf("nomi effects:\n%s\nwant stdout:\n%s", plain, want)
	}

	write := runNomiIn(t, app, "off", "effects", "--write")
	if want := "wrote nomi.toml's [effects] tables:\n\n" + effectsWritten; write.exit != 0 || write.stdout != want {
		t.Errorf("nomi effects --write:\n%s\nwant stdout:\n%s", write, want)
	}
	data, err := os.ReadFile(filepath.Join(app, "nomi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != effectsTight {
		t.Errorf("nomi.toml:\n%s\nwant:\n%s", data, effectsTight)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check with the tables written:\n%s", check)
	}
	again := runNomiIn(t, app, "off", "effects", "--write")
	if again.exit != 0 || again.stdout != "nomi.toml's [effects] tables are current\n" {
		t.Errorf("a second --write:\n%s", again)
	}
	if after, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(after) != string(data) {
		t.Errorf("a second --write changed nomi.toml:\n%s", after)
	}
	plain = runNomiIn(t, app, "off", "effects")
	if want := effectsReached + "\nnomi.toml's [effects] tables allow exactly this\n"; plain.stdout != want {
		t.Errorf("nomi effects with the tables written:\n%s\nwant stdout:\n%s", plain, want)
	}
}

// Tables looser than the program needs pass, and the report says which
// names nothing needs; ones tighter are refused by check, and the report
// says what is missing. A [dependencies] entry with no effects key is a
// choice, as in --check, and nothing in the report. --write prints each
// name it adds and removes, the entries' keys among them, so a widening is
// never silent.
func TestEffects_CommandComparesWithTheTable(t *testing.T) {
	loose := effectsApp + "\n[effects.self]\neffects = [\"std::io\", \"std::net\"]\neffects_in_tests = [\"std::instant\", \"std::process\"]\n\n" +
		"[effects.dependencies]\neffects = [\"std::fs\", \"std::io\"]\n"
	app := effectsProject(t, loose)
	plain := runNomiIn(t, app, "off", "effects")
	want := effectsReached + "\nnomi.toml's [effects] tables differ:\n" +
		changeLineIn(29, "unused", "effects.self.effects", "std::net") +
		changeLineIn(29, "unused", "effects.self.effects_in_tests", "std::process") +
		changeLineIn(29, "unused", "effects.dependencies.effects", "std::io") +
		"nomi check passes; an unused name allows an effect nothing here has\n"
	if plain.exit != 0 || plain.stdout != want {
		t.Errorf("nomi effects with looser tables:\n%s\nwant stdout:\n%s", plain, want)
	}
	write := runNomiIn(t, app, "off", "effects", "--write")
	if !strings.HasSuffix(write.stdout, effectsWritten+"\n"+
		changeLine("added", `dependencies.effects "pricing"`, "std::fs")+
		changeLine("added", `dependencies.effects "pure"`, "(an empty entry)")+
		changeLine("removed", "effects.self.effects", "std::net")+
		changeLine("removed", "effects.self.effects_in_tests", "std::process")+
		changeLine("removed", "effects.dependencies.effects", "std::io")) {
		t.Errorf("nomi effects --write over looser tables:\n%s", write)
	}

	tight := effectsApp + "\n[effects.self]\neffects = [\"std::io\"]\n\n[effects.dependencies]\neffects = []\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), tight)
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 1 {
		t.Fatalf("nomi check with tighter tables passes:\n%s", check)
	}
	plain = runNomiIn(t, app, "off", "effects")
	if !strings.Contains(plain.stdout, changeLineIn(29, "missing", "effects.dependencies.effects", "std::fs")) ||
		!strings.HasSuffix(plain.stdout, "nomi check refuses 2 calls; nomi effects --write writes the tables above\n") {
		t.Errorf("nomi effects with tighter tables:\n%s", plain)
	}
	write = runNomiIn(t, app, "off", "effects", "--write")
	if !strings.Contains(write.stdout, changeLine("added", "effects.dependencies.effects", "std::fs")) ||
		!strings.Contains(write.stdout, changeLine("added", "effects.self.effects_in_tests", "std::instant")) {
		t.Errorf("nomi effects --write over tighter tables:\n%s", write)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check after --write:\n%s", check)
	}
}

// --write changes only the lines of the tables, from each header to its
// last key, and the [dependencies] entries whose effects keys change:
// comments around them, other tables, a table named for a required module
// and entries_required stay as they were. A list that changes keeps the
// names it still holds in its order, then the new ones, and the comment on
// its line; a key the file lacks goes after the keys it has; an entry
// written again keeps the comment after it.
func TestEffects_WriteKeepsTheRestOfNomiToml(t *testing.T) {
	before := "# The app.\n[package]\nname = \"app\"   # its name\nentry_points = [\"main\"]\n\n" +
		"[dependencies]\npricing = { dir = \"../pricing\", effects = [] } # priced\npure = { dir = \"../pure\" }\n\n" +
		"# What it may reach.\n[effects.self]\neffects = [\"std::io\", \"std::net\"]  # too many\n\n" +
		"# The tables of the tools.\n[pricing]\nrates = \"rates.csv\"\n\n" +
		"[effects.dependencies]\nentries_required = true # every dependency\neffects = []\n# trailing comment\n"
	app := effectsProject(t, before)
	write := runNomiIn(t, app, "off", "effects", "--write")
	if write.exit != 0 {
		t.Fatalf("nomi effects --write:\n%s", write)
	}
	data, err := os.ReadFile(filepath.Join(app, "nomi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# The app.\n[package]\nname = \"app\"   # its name\nentry_points = [\"main\"]\n\n" +
		"[dependencies]\npricing = { dir = \"../pricing\", effects = [\"std::fs\"] } # priced\npure = { dir = \"../pure\", effects = [] }\n\n" +
		"# What it may reach.\n[effects.self]\neffects = [\"std::io\"]  # too many\neffects_in_tests = [\"std::instant\"]\n\n" +
		"# The tables of the tools.\n[pricing]\nrates = \"rates.csv\"\n\n" +
		"[effects.dependencies]\nentries_required = true # every dependency\neffects = [\"std::fs\"]\n# trailing comment\n"
	if string(data) != want {
		t.Errorf("nomi.toml:\n%s\nwant:\n%s", data, want)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check after --write:\n%s", check)
	}
}

// annotatedManifest is the tooling reference's example of the lists with
// a comment on each key, for annotatedProject.
const annotatedManifest = `[package]
name = "app"
entry_points = ["main"]

[dependencies]
# the one dependency that may make HTTP requests
fetch = { dir = "../fetch", effects = ["std::http"] }
# reads its configuration file
pg = { dir = "../pg", effects = ["std::fs"] }
# computes, and has no effects
pricing = { dir = "../pricing", effects = [] }
# reads and writes files
reports = { dir = "../reports", effects = ["std::fs"] }

[effects.self]
# the effects this package's own code may have
effects = ["std::io", "std::fs"]
# added to effects for its tests
effects_in_tests = ["std::process"]

[effects.self.modules]
# prints and reads files
"main" = ["std::io", "std::fs"]
# computes, and has no effects
"totals" = []
# the test file, which starts a process
"main_test" = ["std::process"]

[effects.dependencies]
# every [dependencies] entry must have an effects key
entries_required = true
# no dependency may have more effects than this
effects = ["std::fs", "std::http"]
`

// annotatedProject is the project annotatedManifest describes,
// app, with that manifest: its main prints and reads a file,
// totals computes, and its test file starts a process; pricing computes,
// reports writes a file, fetch makes an HTTP request, and pg reads a file. With reports false, main does not call reports.
func annotatedProject(t *testing.T, reports bool) string {
	t.Helper()
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	deps := []string{"fetch", "pg", "pricing", "reports"}
	for _, d := range deps {
		mustWrite(t, filepath.Join(dir, d, "nomi.toml"), "[package]\nname = \""+d+"\"\n")
	}
	mustWrite(t, filepath.Join(app, "nomi.toml"), annotatedManifest)
	save, use := "", ""
	if reports {
		save, use = "    reports::summary\n", "    summary.save(\"report.txt\", \"${total}\")\n"
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::fs\n    std::io\n    fetch::rates\n    pg::db\n    pricing::calc\n    totals\n"+save+"end\n\n"+
		"fn main()\n    total = totals.twice(calc.total(3))\n"+use+"    io.print(\"${total} ${rates.today()} ${db.upper(\"hi\")} ${db.config()}\")\n    _ = fs.read_text(\"notes.txt\")\nend\n")
	mustWrite(t, filepath.Join(app, "totals.nomi"), "pub fn twice(n: Int) -> Int\n    n * 2\nend\n")
	mustWrite(t, filepath.Join(app, "main_test.nomi"), "import std::process.{self, Command}\n\n"+
		"test \"the tool starts\"\n    _ = process.run(Command{program: \"true\"})\n    assert 1 == 1\nend\n")
	mustWrite(t, filepath.Join(dir, "pricing", "calc.nomi"), "pub fn total(n: Int) -> Int\n    n * 2\nend\n")
	mustWrite(t, filepath.Join(dir, "reports", "summary.nomi"), "import std::fs\n\npub fn save(path: String, text: String)\n    _ = fs.write_text(path, text)\nend\n")
	mustWrite(t, filepath.Join(dir, "fetch", "rates.nomi"), "import\n    std::http\n    std::url.Url\nend\n\n"+
		"pub fn today() -> String\n    case http.get(Url\"https://rates.example.com/today\")\n        Ok(response) -> \"${response.status}\"\n        Err(_)       -> \"offline\"\n    end\nend\n")
	mustWrite(t, filepath.Join(dir, "pg", "db.nomi"), "import std::fs\n\npub fn upper(s: String) -> String\n    String.uppercase(s)\nend\n\n"+
		"pub fn config() -> String\n    fs.read_text(\"pg.conf\") |> Result.with_default(\"\")\nend\n")
	return app
}

// --write keeps the comments of the lists it changes: the annotated
// example the program needs all of is current and stays byte for byte; the
// effects key of an entry the program no longer loads files through goes,
// and the entry and its comment stay; and a key written new has no
// comment.
func TestEffects_WriteKeepsComments(t *testing.T) {
	app := annotatedProject(t, true)
	if r := runNomiIn(t, app, "off", "effects", "--write"); r.exit != 0 || r.stdout != "nomi.toml's [effects] tables are current\n" {
		t.Errorf("nomi effects --write over the annotated example:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != annotatedManifest {
		t.Errorf("current tables changed nomi.toml:\n%s", data)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check of the annotated example:\n%s", check)
	}

	app = annotatedProject(t, false)
	if r := runNomiIn(t, app, "off", "effects", "--write"); r.exit != 0 || !strings.HasSuffix(r.stdout, "  removed  dependencies.effects \"reports\"  std::fs\n") {
		t.Errorf("nomi effects --write with reports unused:\n%s", r)
	}
	want := strings.Replace(annotatedManifest, `reports = { dir = "../reports", effects = ["std::fs"] }`, `reports = { dir = "../reports" }`, 1)
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != want {
		t.Errorf("nomi.toml with reports unused:\n%s\nwant:\n%s", data, want)
	}

	app = annotatedProject(t, true)
	withoutPG := strings.Replace(annotatedManifest, `pg = { dir = "../pg", effects = ["std::fs"] }`, `pg = { dir = "../pg" }`, 1)
	mustWrite(t, filepath.Join(app, "nomi.toml"), withoutPG)
	if r := runNomiIn(t, app, "off", "effects", "--write"); r.exit != 0 {
		t.Errorf("nomi effects --write with no effects key for pg:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != annotatedManifest {
		t.Errorf("nomi.toml with pg's effects key written:\n%s\nwant:\n%s", data, annotatedManifest)
	}
}

// --format json gives the same facts as data: self's lists, the
// dependencies' lists with what the code of each [dependencies] entry
// reaches from main and only from the tests, and how nomi.toml's lists
// differ.
func TestEffects_CommandJSON(t *testing.T) {
	app := effectsProject(t, effectsApp+"\n[effects.self]\neffects = [\"std::io\"]\n")
	r := runNomiIn(t, app, "off", "effects", "--format", "json")
	if r.exit != 0 {
		t.Fatalf("nomi effects --format json:\n%s", r)
	}
	var got effectsJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, r.stdout)
	}
	if got.Project != "app" || strings.Join(got.Self.Effects, " ") != "std::io" || strings.Join(got.Self.EffectsInTests, " ") != "std::instant" ||
		strings.Join(got.Dependencies.Effects, " ") != "std::fs" || len(got.Dependencies.EffectsInTests) != 0 || got.Written {
		t.Errorf("lists: %+v", got)
	}
	if m := got.Dependencies.Entries; len(m) != 2 || m[0].Key != "pricing" || strings.Join(m[0].Effects, " ") != "std::fs" ||
		len(m[0].EffectsInTests) != 0 || m[1].Key != "pure" || len(m[1].Effects) != 0 {
		t.Errorf("entries: %+v", m)
	}
	// The [effects.dependencies] table the file leaves out holds nothing,
	// so its names are no difference, and left_out names it.
	wantChanges := []analysis.EffectChange{
		{Change: "add", List: "effects.self.effects_in_tests", Name: "std::instant"},
	}
	if got.Table == nil || got.Table.Refused != 1 || fmt.Sprint(got.Table.Changes) != fmt.Sprint(wantChanges) ||
		strings.Join(got.Table.LeftOut, " ") != "effects.dependencies" {
		t.Errorf("table: %+v", got.Table)
	}
	// An entry names its [dependencies] key a package, since the key is a
	// dependency.
	for _, field := range []string{`"self": {`, `"dependencies": {`, `"entries": [`, `"effects_in_tests"`, `"declared_less": []`, `"problems": []`, `"package": "pricing"`} {
		if !strings.Contains(r.stdout, field) {
			t.Errorf("the output has no %s:\n%s", field, r.stdout)
		}
	}
	if strings.Contains(r.stdout, `"module"`) {
		t.Errorf("a dependency's entry is named a module:\n%s", r.stdout)
	}
}

// A dependency whose own [effects.self] allows less than its code has is
// in --format json's declared_less, named as the package it is.
func TestEffects_CommandJSONNamesTheDependencyThatDeclaresLess(t *testing.T) {
	app := effectsProject(t, effectsApp)
	mustWrite(t, filepath.Join(filepath.Dir(app), "pricing", "nomi.toml"), "[package]\nname = \"pricing\"\n\n[effects.self]\neffects = []\n")
	r := runNomiIn(t, app, "off", "effects", "--format", "json")
	var got struct {
		DeclaredLess []map[string]string `json:"declared_less"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, r)
	}
	if len(got.DeclaredLess) != 1 || got.DeclaredLess[0]["package"] != "pricing" || got.DeclaredLess[0]["name"] != "std::fs" ||
		got.DeclaredLess[0]["module"] != "" {
		t.Errorf("declared_less: %v\n%s", got.DeclaredLess, r)
	}
}

// --check holds nomi.toml's lists to the tightest ones in both
// directions: it exits 0 with one line for exact lists, and for no
// lists, which hold nothing; it exits 1 for narrower lists, which nomi
// check refuses, and for wider ones, which it passes, printing the
// missing and unused lines the plain report prints. A table, a ceiling or
// an entry's effects key the file leaves out is a choice, not a
// difference. --format json gives the report's object and the same exit
// status.
func TestEffects_Check(t *testing.T) {
	app := effectsProject(t, effectsApp)
	none := runNomiIn(t, app, "off", "effects", "--check")
	if want := "nomi.toml has no [effects.self] or [effects.dependencies] table, so there is nothing to hold; nomi effects --write writes them\n"; none.exit != 0 || none.stdout != want {
		t.Errorf("--check with no tables:\n%s\nwant stdout:\n%s", none, want)
	}
	if r := runNomiIn(t, app, "off", "effects", "--check", "--format", "json"); r.exit != 0 || !strings.Contains(r.stdout, `"table": null`) {
		t.Errorf("--check --format json with no tables:\n%s", r)
	}

	exactly := "nomi.toml's [effects] tables allow exactly the effects the program has\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), effectsTight)
	if exact := runNomiIn(t, app, "off", "effects", "--check"); exact.exit != 0 || exact.stdout != exactly {
		t.Errorf("--check with the tightest lists:\n%s\nwant stdout:\n%s", exact, exactly)
	}

	// Lists that leave out the entries' effects keys, the ceiling, or all
	// of [effects.dependencies], hold the dependencies by what is left,
	// which --check does not count as a difference, and the report counts
	// none either: it lists nothing missing, and says in one line which
	// table the file leaves out holds code with effects.
	selfTable, depsTable, _ := strings.Cut(effectsTables, "\n[effects.dependencies]")
	depsTable = "[effects.dependencies]" + depsTable
	const (
		noSelf = "nomi.toml has no [effects.self] table, so the project's own code is not held; nomi effects --write writes one\n"
		noDeps = "nomi.toml has no [effects.dependencies] table and no [dependencies] entry with an effects key, so the dependencies are not held; nomi effects --write writes them\n"
	)
	for name, c := range map[string]struct{ manifest, leftOut string }{
		"no entries":                      {effectsApp + "\n" + effectsTables, ""},
		"no ceiling":                      {strings.Replace(effectsTight, "[effects.dependencies]\neffects = [\"std::fs\"]\n", "[effects.dependencies]\n", 1), ""},
		"no deps":                         {effectsApp + "\n" + selfTable, noDeps},
		"entries and no deps table":       {effectsModule + "\n" + effectsEntries + "\n" + selfTable, ""},
		"no self":                         {effectsModule + "\n" + effectsEntries + "\n" + depsTable, noSelf},
		"an empty [effects.dependencies]": {effectsApp + "\n[effects.dependencies]\n", noSelf},
	} {
		mustWrite(t, filepath.Join(app, "nomi.toml"), c.manifest)
		if r := runNomiIn(t, app, "off", "effects", "--check"); r.exit != 0 || r.stdout != exactly {
			t.Errorf("--check with %s:\n%s", name, r)
		}
		want := "nomi.toml's [effects] tables allow exactly this\n" + c.leftOut
		if r := runNomiIn(t, app, "off", "effects"); r.exit != 0 || !strings.HasSuffix(r.stdout, "\n\n"+want) {
			t.Errorf("nomi effects with %s:\n%s\nwant it to end:\n%s", name, r, want)
		}
	}

	wider := strings.Replace(effectsTight, `effects = ["std::io"]`, `effects = ["std::io", "std::net"]`, 1)
	mustWrite(t, filepath.Join(app, "nomi.toml"), wider)
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Fatalf("nomi check refuses wider lists:\n%s", check)
	}
	r := runNomiIn(t, app, "off", "effects", "--check")
	want := "nomi.toml's [effects] tables differ:\n" +
		"  unused   effects.self.effects  std::net\n" +
		"nomi check passes; an unused name allows an effect nothing here has\n"
	if r.exit != 1 || r.stdout != want || r.stderr != "" {
		t.Errorf("--check with wider lists:\n%s\nwant stdout:\n%s", r, want)
	}

	narrower := strings.Replace(effectsTight, "[effects.dependencies]\neffects = [\"std::fs\"]", "[effects.dependencies]\neffects = []", 1)
	mustWrite(t, filepath.Join(app, "nomi.toml"), narrower)
	r = runNomiIn(t, app, "off", "effects", "--check")
	want = "nomi.toml's [effects] tables differ:\n" +
		"  missing  effects.dependencies.effects  std::fs\n" +
		"nomi check refuses 1 call; nomi effects --write writes the tightest tables\n"
	if r.exit != 1 || r.stdout != want {
		t.Errorf("--check with narrower lists:\n%s\nwant stdout:\n%s", r, want)
	}
	r = runNomiIn(t, app, "off", "effects", "--check", "--format", "json")
	var got effectsJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("--check --format json is not JSON: %v\n%s", err, r.stdout)
	}
	if r.exit != 1 || got.Table == nil || got.Table.Refused != 1 || len(got.Table.Changes) != 1 ||
		got.Table.Changes[0] != (analysis.EffectChange{Change: "add", List: "effects.dependencies.effects", Name: "std::fs"}) {
		t.Errorf("--check --format json with narrower lists:\n%s", r)
	}

	for _, args := range [][]string{{"--check", "--write"}, {"--why", "std::fs", "--check"}} {
		r := runNomiIn(t, app, "off", append([]string{"effects"}, args...)...)
		if r.exit != 1 || !strings.Contains(r.stderr, "do not go together") {
			t.Errorf("nomi effects %s:\n%s", strings.Join(args, " "), r)
		}
	}
}

// entriesRequiredApp is effectsApp, its entries with no effects keys, with
// the tightest tables and entries_required under [effects.dependencies].
const entriesRequiredApp = effectsApp + "\n[effects.self]\neffects = [\"std::io\"]\neffects_in_tests = [\"std::instant\"]\n\n" +
	"[effects.dependencies]\nentries_required = true\neffects = [\"std::fs\"]\n"

// With entries_required, --check holds the lists to an effects key on each
// [dependencies] entry, and a missing one is a difference; nomi check
// fails on it too.
func TestEffects_CheckWithEntriesRequired(t *testing.T) {
	app := effectsProject(t, entriesRequiredApp)
	r := runNomiIn(t, app, "off", "effects", "--check")
	if r.exit != 1 || !strings.Contains(r.stdout, `  missing  dependencies.effects "pricing"  std::fs`) {
		t.Errorf("--check with entries_required and no effects keys:\n%s", r)
	}
}

// A problem nomi check finds in the lists themselves (a misspelled name,
// an entry entries_required finds with no effects key) is printed as nomi
// check prints it, and the report says nomi check fails on it rather than
// that it passes; --check fails on it too.
func TestEffects_ListProblems(t *testing.T) {
	app := effectsProject(t, effectsApp)
	misspelled := strings.Replace(effectsTight, `effects = ["std::io"]`, `effects = ["std::io", "std::fss"]`, 1)
	mustWrite(t, filepath.Join(app, "nomi.toml"), misspelled)
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 1 || !strings.Contains(check.stdout+check.stderr, "std::fss, which is not a std module") {
		t.Fatalf("nomi check with a misspelled name:\n%s", check)
	}
	problem := "nomi.toml:10: [effects.self] effects names std::fss, which is not a std module\n" +
		"nomi.toml:10: help: did you mean \"std::fs\"?\n"
	want := "nomi.toml's [effects] tables differ:\n" +
		"  unused   effects.self.effects  std::fss\n" +
		problem +
		"nomi check fails on the problem above\n"
	if r := runNomiIn(t, app, "off", "effects"); r.exit != 0 || !strings.HasSuffix(r.stdout, "\n"+want) {
		t.Errorf("the report with a misspelled name:\n%s\nwant it to end:\n%s", r, want)
	}
	if r := runNomiIn(t, app, "off", "effects", "--check"); r.exit != 1 || r.stdout != want {
		t.Errorf("--check with a misspelled name:\n%s\nwant stdout:\n%s", r, want)
	}
	r := runNomiIn(t, app, "off", "effects", "--check", "--format", "json")
	var got effectsJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("--check --format json is not JSON: %v\n%s", err, r.stdout)
	}
	if r.exit != 1 || got.Table == nil || len(got.Table.Problems) != 1 || got.Table.Problems[0]+"\n" != problem {
		t.Errorf("--check --format json with a misspelled name:\n%s", r)
	}

	// The tightest tables, with no effects keys on the entries, which
	// entries_required asks of each.
	mustWrite(t, filepath.Join(app, "nomi.toml"), entriesRequiredApp)
	missing := "entries_required: the [dependencies] entry pricing has no effects key"
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 1 || !strings.Contains(check.stdout+check.stderr, missing) {
		t.Fatalf("nomi check with entries_required:\n%s", check)
	}
	r = runNomiIn(t, app, "off", "effects")
	if r.exit != 0 || strings.Contains(r.stdout, "nomi check passes") ||
		!strings.Contains(r.stdout, missing+"\n") ||
		!strings.HasSuffix(r.stdout, "nomi check fails on the 2 problems above\n") {
		t.Errorf("the report with entries_required and no effects keys:\n%s", r)
	}
}

// --why prints each call that reaches a std name, or that a module's files
// make, at its site with the chain a refusal's help gives; a name nothing
// reaches is an error that says what the program reaches.
func TestEffects_Why(t *testing.T) {
	app := effectsProject(t, effectsApp)
	fs := runNomiIn(t, app, "off", "effects", "--why", "std::fs")
	want := "1 call has the effect std::fs:\n  rules.nomi:4:5 in pricing\n    reached from `main`; `main` calls `load`\n"
	if fs.exit != 0 || fs.stdout != want {
		t.Errorf("--why std::fs:\n%s\nwant stdout:\n%s", fs, want)
	}
	// In JSON each call names the package charged for it: here the
	// dependency pricing, whose file holds it.
	fsJSON := runNomiIn(t, app, "off", "effects", "--why", "std::fs", "--format", "json")
	var why struct {
		Calls []map[string]any `json:"calls"`
	}
	if err := json.Unmarshal([]byte(fsJSON.stdout), &why); err != nil || fsJSON.exit != 0 || len(why.Calls) != 1 ||
		why.Calls[0]["package"] != "pricing" || why.Calls[0]["module"] != nil {
		t.Errorf("--why std::fs --format json (%v):\n%s", err, fsJSON)
	}
	module := runNomiIn(t, app, "off", "effects", "--why", "app")
	want = "3 calls in this project's own files have effects:\n" +
		"  main.nomi:9:5: std::io\n    reached from `main`\n" +
		"  main.nomi:10:5: std::io\n    reached from `main`\n" +
		"  main.nomi:14:9: std::instant\n    reached from test \"the clock moves\"\n"
	if module.exit != 0 || module.stdout != want {
		t.Errorf("--why app:\n%s\nwant stdout:\n%s", module, want)
	}
	unknown := runNomiIn(t, app, "off", "effects", "--why", "std::net")
	if unknown.exit != 1 || unknown.stderr != "nomi effects: this program does not have the effect std::net; it has the effects std::fs, std::instant and std::io, "+
		"and its code comes from app, pricing, pure\n" {
		t.Errorf("--why std::net:\n%s", unknown)
	}
}

// --why names an impl block's function in a chain by its interface and
// the type it is for, whichever use of it the program lowers first.
func TestEffects_WhyNamesTheTypeOfAnImpl(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), effectsModule)
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import
    std::fs
    std::io
end

struct Loud
    path: String
end

impl Display for Loud
    fn to_string(l: Loud) -> String
        load(l.path)
    end
end

fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn main()
    io.print("${Loud{path: "a.txt"}}")
    shown: Display = Loud{path: "b.txt"}
    io.print(Display.to_string(shown))
end
`)
	why := runNomiIn(t, dir, "off", "effects", "--why", "std::fs")
	want := "1 call has the effect std::fs:\n  main.nomi:17:5\n" +
		"    reached from `main`; `main` calls `Display.to_string for Loud`; `Display.to_string for Loud` calls `load`\n"
	if why.exit != 0 || why.stdout != want {
		t.Errorf("--why std::fs:\n%s\nwant stdout:\n%s", why, want)
	}
}

// A program that does not check is reported as nomi check reports it,
// and the command writes nothing.
func TestEffects_AProgramThatDoesNotCheck(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), effectsModule)
	mustWrite(t, filepath.Join(dir, "main.nomi"), "fn main()\n    x = undefined_name\nend\n")
	r := runNomiIn(t, dir, "off", "effects", "--write")
	if r.exit != 1 || !strings.Contains(r.stderr, "undefined_name") || r.stdout != "" {
		t.Errorf("nomi effects --write on a program that does not check:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "nomi.toml")); string(data) != effectsModule {
		t.Errorf("nomi.toml changed:\n%s", data)
	}
}

// check prints one refusal per name and list, the first call with a help
// line counting the rest; --all-calls prints every call, on check, run,
// test and build alike, and --format json has a record per call either
// way.
func TestEffects_RefusalsAreGrouped(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), effectsModule+"\n[effects.self]\n")
	// The test reaches main's calls, which check reports once, from main.
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import std::io\n\nfn main()\n    io.print(\"a\")\n    io.print(\"b\")\n    io.print(\"c\")\nend\n\n"+
		"test \"prints\"\n    main()\n    assert 1 == 1\nend\n")
	check := runNomiIn(t, dir, "off", "check", "--format", "short", "main.nomi")
	want := "main.nomi:4:5: this package's own code has the effect std::io, and [effects.self] allows it no effects\n" +
		"main.nomi:4:5: help: reached from `main`\n" +
		"main.nomi:4:5: help: add \"std::io\" to effects under [effects.self] in nomi.toml, or remove the call\n" +
		"main.nomi:4:5: help: and 2 more calls have the effect std::io; --all-calls lists them\n"
	if check.exit != 1 || !strings.HasSuffix(check.stderr, want) || strings.Count(check.stderr, "this package's own code has") != 1 {
		t.Errorf("nomi check:\n%s\nwant stderr ending:\n%s", check, want)
	}
	for _, args := range [][]string{
		{"check", "--format", "short", "--all-calls", "main.nomi"},
		{"run", "--all-calls", "main.nomi"},
		{"test", "--all-calls", "main.nomi"},
		{"build", "--all-calls", "main.nomi", "-o", exe(filepath.Join(t.TempDir(), "app"))},
	} {
		r := runNomiIn(t, dir, "off", args...)
		if r.exit != 1 || strings.Count(r.stdout+r.stderr, "std::io, and [effects.self] allows") != 3 || strings.Contains(r.stdout+r.stderr, "more calls") {
			t.Errorf("nomi %s: want every call refused\n%s", strings.Join(args, " "), r)
		}
	}
	json := runNomiIn(t, dir, "off", "check", "--format", "json", "main.nomi")
	if json.exit != 1 || strings.Count(json.stdout, `"code":"effect-not-allowed"`) != 3 || strings.Contains(json.stdout, "more calls") {
		t.Errorf("nomi check --format json: want a record per call\n%s", json)
	}
}
