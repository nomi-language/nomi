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

// capabilitiesProject is an app, example.com/app by its go.mod, whose main
// prints and reads a file through a dependency, example.com/pricing, and
// whose test reads the clock; a second dependency, example.com/pure, is
// imported and reaches nothing. manifest is the app's nomi.toml.
func capabilitiesProject(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	mustWrite(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.27.0\n\nrequire (\n\texample.com/pricing v0.0.0\n\texample.com/pure v0.0.0\n)\n\n"+
		"replace (\n\texample.com/pricing => ../pricing\n\texample.com/pure => ../pure\n)\n")
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
	mustWrite(t, filepath.Join(dir, "pricing", "go.mod"), "module example.com/pricing\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(dir, "pricing", "nomi.toml"), "[module]\nname = \"pricing\"\n")
	mustWrite(t, filepath.Join(dir, "pricing", "rules.nomi"), "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")
	mustWrite(t, filepath.Join(dir, "pure", "go.mod"), "module example.com/pure\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(dir, "pure", "nomi.toml"), "[module]\nname = \"pure\"\n")
	mustWrite(t, filepath.Join(dir, "pure", "calc.nomi"), "pub fn double(n: Int) -> Int\n    n * 2\nend\n")
	return app
}

const capabilitiesModule = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"

// What capabilitiesProject reaches, as the report prints the tightest
// tables: self's lists, then deps' with what each dependency reaches.
const capabilitiesReached = `self: example.com/app
  std            std::io
  std_in_tests   std::instant
  go             false
  go_in_tests    false

deps
  std            std::fs
  std_in_tests   (none)
  go             (none)
  go_in_tests    (none)
  modules
    example.com/pricing
      std::fs
    example.com/pure
      nothing
`

// The tables --write writes for capabilitiesProject.
const capabilitiesTable = `[capabilities.self]
std = ["std::io"]
std_in_tests = ["std::instant"]

[capabilities.deps]
std = ["std::fs"]

[capabilities.deps.modules]
"example.com/pricing" = ["std::fs"]
"example.com/pure" = []
`

// changeLine is one line of the report's differences, its label padded to
// the width of `deps.modules "example.com/pricing"`, the longest
// capabilitiesProject's reports print.
func changeLine(word, label, name string) string {
	return fmt.Sprintf("  %-8s %-34s  %s\n", word, label, name)
}

// With no tables, the command prints what the program reaches and says
// nothing holds it; --write writes the tightest tables at the end of
// nomi.toml, which nomi check then passes, and a second --write writes
// nothing and says the tables are current.
func TestCapabilities_CommandWritesATableTheCheckPasses(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule)
	plain := runNomiIn(t, app, "off", "capabilities")
	want := capabilitiesReached + "\nnomi.toml has no [capabilities.self] or [capabilities.deps] table; nomi capabilities --write writes these\n"
	if plain.exit != 0 || plain.stdout != want {
		t.Errorf("nomi capabilities:\n%s\nwant stdout:\n%s", plain, want)
	}

	write := runNomiIn(t, app, "off", "capabilities", "--write")
	if want := "wrote nomi.toml's [capabilities] tables:\n\n" + capabilitiesTable; write.exit != 0 || write.stdout != want {
		t.Errorf("nomi capabilities --write:\n%s\nwant stdout:\n%s", write, want)
	}
	data, err := os.ReadFile(filepath.Join(app, "nomi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := capabilitiesModule + "\n" + capabilitiesTable; string(data) != want {
		t.Errorf("nomi.toml:\n%s\nwant:\n%s", data, want)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check with the tables written:\n%s", check)
	}
	again := runNomiIn(t, app, "off", "capabilities", "--write")
	if again.exit != 0 || again.stdout != "nomi.toml's [capabilities] tables are current\n" {
		t.Errorf("a second --write:\n%s", again)
	}
	if after, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(after) != string(data) {
		t.Errorf("a second --write changed nomi.toml:\n%s", after)
	}
	plain = runNomiIn(t, app, "off", "capabilities")
	if want := capabilitiesReached + "\nnomi.toml's [capabilities] tables allow exactly this\n"; plain.stdout != want {
		t.Errorf("nomi capabilities with the tables written:\n%s\nwant stdout:\n%s", plain, want)
	}
}

// Tables looser than the program needs pass, and the report says which
// names nothing needs; ones tighter are refused by check, and the report
// says what is missing. --write prints each name it adds and removes, so
// a widening is never silent.
func TestCapabilities_CommandComparesWithTheTable(t *testing.T) {
	loose := capabilitiesModule + "\n[capabilities.self]\nstd = [\"std::io\", \"std::net\"]\nstd_in_tests = [\"std::instant\", \"std::process\"]\n\n" +
		"[capabilities.deps]\nstd = [\"std::fs\", \"std::io\"]\n"
	app := capabilitiesProject(t, loose)
	plain := runNomiIn(t, app, "off", "capabilities")
	want := capabilitiesReached + "\nnomi.toml's [capabilities] tables differ:\n" +
		changeLine("missing", `deps.modules "example.com/pricing"`, "std::fs") +
		changeLine("missing", `deps.modules "example.com/pure"`, "(an empty entry)") +
		changeLine("unused", "self.std", "std::net") +
		changeLine("unused", "self.std_in_tests", "std::process") +
		changeLine("unused", "deps.std", "std::io") +
		"nomi check passes; an unused name allows what nothing here reaches\n"
	if plain.exit != 0 || plain.stdout != want {
		t.Errorf("nomi capabilities with looser tables:\n%s\nwant stdout:\n%s", plain, want)
	}
	write := runNomiIn(t, app, "off", "capabilities", "--write")
	if !strings.HasSuffix(write.stdout, capabilitiesTable+"\n"+
		changeLine("added", `deps.modules "example.com/pricing"`, "std::fs")+
		changeLine("added", `deps.modules "example.com/pure"`, "(an empty entry)")+
		changeLine("removed", "self.std", "std::net")+
		changeLine("removed", "self.std_in_tests", "std::process")+
		changeLine("removed", "deps.std", "std::io")) {
		t.Errorf("nomi capabilities --write over looser tables:\n%s", write)
	}

	tight := capabilitiesModule + "\n[capabilities.self]\nstd = [\"std::io\"]\n\n[capabilities.deps]\nstd = []\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), tight)
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 1 {
		t.Fatalf("nomi check with tighter tables passes:\n%s", check)
	}
	plain = runNomiIn(t, app, "off", "capabilities")
	if !strings.Contains(plain.stdout, changeLine("missing", "deps.std", "std::fs")) ||
		!strings.HasSuffix(plain.stdout, "nomi check refuses 2 calls; nomi capabilities --write writes the tables above\n") {
		t.Errorf("nomi capabilities with tighter tables:\n%s", plain)
	}
	write = runNomiIn(t, app, "off", "capabilities", "--write")
	if !strings.Contains(write.stdout, changeLine("added", "deps.std", "std::fs")) ||
		!strings.Contains(write.stdout, changeLine("added", "self.std_in_tests", "std::instant")) {
		t.Errorf("nomi capabilities --write over tighter tables:\n%s", write)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check after --write:\n%s", check)
	}
}

// --write changes only the lines of the three tables, from each header to
// its last key: comments around them, other tables, a table named for a
// required module, entries_required, and a flag that keeps its value
// stay as they were. A list that changes keeps the names it still holds
// in its order, then the new ones, and the comment on its line; a key the
// file lacks goes after the keys it has.
func TestCapabilities_WriteKeepsTheRestOfNomiToml(t *testing.T) {
	before := "# The app.\n[module]\nname = \"app\"   # its name\nentry_points = [\"main\"]\n\n" +
		"# What it may reach.\n[capabilities.self]\nstd = [\"std::io\", \"std::net\"]  # too many\ngo = false # none of ours\n\n" +
		"# The tables of the tools.\n[pricing]\nrates = \"rates.csv\"\n\n" +
		"[capabilities.deps]\nentries_required = true # every dependency\nstd = []\n\n" +
		"[capabilities.deps.modules]\n\"example.com/pricing\" = []\n# trailing comment\n"
	app := capabilitiesProject(t, before)
	write := runNomiIn(t, app, "off", "capabilities", "--write")
	if write.exit != 0 {
		t.Fatalf("nomi capabilities --write:\n%s", write)
	}
	data, err := os.ReadFile(filepath.Join(app, "nomi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# The app.\n[module]\nname = \"app\"   # its name\nentry_points = [\"main\"]\n\n" +
		"# What it may reach.\n[capabilities.self]\nstd = [\"std::io\"]  # too many\ngo = false # none of ours\nstd_in_tests = [\"std::instant\"]\n\n" +
		"# The tables of the tools.\n[pricing]\nrates = \"rates.csv\"\n\n" +
		"[capabilities.deps]\nentries_required = true # every dependency\nstd = [\"std::fs\"]\n\n" +
		"[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n\"example.com/pure\" = []\n# trailing comment\n"
	if string(data) != want {
		t.Errorf("nomi.toml:\n%s\nwant:\n%s", data, want)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check after --write:\n%s", check)
	}
}

// annotatedManifest is the tooling reference's example of the tables with
// a comment on each key, for annotatedProject.
const annotatedManifest = `[module]
name = "app"
entry_points = ["main"]

[capabilities.self]
# what this module's own code may reach
std = ["std::io", "std::fs"]
# added to std for its tests
std_in_tests = ["std::process"]
# its own code binds no Go
go = false

[capabilities.deps]
# every dependency must have an entry below
entries_required = true
# no dependency may reach more than this
std = ["std::fs", "std::http"]
# the dependencies whose Go may run
go = ["lib.example/pg"]

[capabilities.deps.modules]
# computes, and reaches nothing
"lib.example/pricing" = []
# reads and writes files
"lib.example/reports" = ["std::fs"]
# the one dependency that may make HTTP requests
"lib.example/fetch" = ["std::http"]
# Nomi code over a Go package; its Nomi code also reads files
"lib.example/pg" = ["std::fs"]
`

// annotatedProject is the project annotatedManifest describes,
// example.com/app, with that manifest: its main prints and reads a file,
// and its test starts a process; pricing computes, reports writes a file,
// fetch makes an HTTP request, and pg binds Go and reads a file. With
// reports false, main does not call reports.
func annotatedProject(t *testing.T, reports bool) string {
	t.Helper()
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	deps := []string{"fetch", "pg", "pricing", "reports"}
	var require, replace strings.Builder
	for _, d := range deps {
		fmt.Fprintf(&require, "\tlib.example/%s v0.0.0\n", d)
		fmt.Fprintf(&replace, "\tlib.example/%s => ../%s\n", d, d)
		mustWrite(t, filepath.Join(dir, d, "go.mod"), "module lib.example/"+d+"\n\ngo 1.27.0\n")
		mustWrite(t, filepath.Join(dir, d, "nomi.toml"), "[module]\nname = \""+d+"\"\n")
	}
	mustWrite(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.27.0\n\nrequire (\n"+require.String()+")\n\nreplace (\n"+replace.String()+")\n")
	mustWrite(t, filepath.Join(app, "nomi.toml"), annotatedManifest)
	save, use := "", ""
	if reports {
		save, use = "    reports::summary\n", "    summary.save(\"report.txt\", \"${total}\")\n"
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::fs\n    std::io\n    std::process.{self, Command}\n    fetch::rates\n    pg::db\n    pricing::calc\n"+save+"end\n\n"+
		"fn main()\n    total = calc.total(3)\n"+use+"    io.print(\"${total} ${rates.today()} ${db.upper(\"hi\")} ${db.config()}\")\n    _ = fs.read_text(\"notes.txt\")\nend\n\n"+
		"test \"the tool starts\"\n    _ = process.run(Command{program: \"true\"})\n    assert 1 == 1\nend\n")
	mustWrite(t, filepath.Join(dir, "pricing", "calc.nomi"), "pub fn total(n: Int) -> Int\n    n * 2\nend\n")
	mustWrite(t, filepath.Join(dir, "reports", "summary.nomi"), "import std::fs\n\npub fn save(path: String, text: String)\n    _ = fs.write_text(path, text)\nend\n")
	mustWrite(t, filepath.Join(dir, "fetch", "rates.nomi"), "import\n    std::http\n    std::url.Url\nend\n\n"+
		"pub fn today() -> String\n    case http.get(Url\"https://rates.example.com/today\")\n        Ok(response) -> \"${response.status}\"\n        Err(_)       -> \"offline\"\n    end\nend\n")
	mustWrite(t, filepath.Join(dir, "pg", "db.nomi"), "import std::fs\n\ngopkg \"strings\" as gostrings\n\npub fn upper(s: String) -> String go gostrings.ToUpper\n\n"+
		"pub fn config() -> String\n    fs.read_text(\"pg.conf\") |> Result.with_default(\"\")\nend\n")
	return app
}

// --write keeps the comments of the tables it changes: the annotated
// example the program needs all of is current and stays byte for byte; an
// entry the program no longer needs goes with its comment, leaving the
// rest as they were; and an entry written new has no comment, since go
// under [capabilities.deps] says which dependencies bind Go.
func TestCapabilities_WriteKeepsComments(t *testing.T) {
	app := annotatedProject(t, true)
	if r := runNomiIn(t, app, "off", "capabilities", "--write"); r.exit != 0 || r.stdout != "nomi.toml's [capabilities] tables are current\n" {
		t.Errorf("nomi capabilities --write over the annotated example:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != annotatedManifest {
		t.Errorf("current tables changed nomi.toml:\n%s", data)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check of the annotated example:\n%s", check)
	}

	app = annotatedProject(t, false)
	if r := runNomiIn(t, app, "off", "capabilities", "--write"); r.exit != 0 || !strings.HasSuffix(r.stdout, "  removed  deps.modules \"lib.example/reports\"  std::fs\n") {
		t.Errorf("nomi capabilities --write with reports unused:\n%s", r)
	}
	want := strings.Replace(annotatedManifest, "# reads and writes files\n\"lib.example/reports\" = [\"std::fs\"]\n", "", 1)
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != want {
		t.Errorf("nomi.toml with reports unused:\n%s\nwant:\n%s", data, want)
	}

	app = annotatedProject(t, true)
	withoutPG := strings.TrimSuffix(annotatedManifest, "# Nomi code over a Go package; its Nomi code also reads files\n\"lib.example/pg\" = [\"std::fs\"]\n")
	mustWrite(t, filepath.Join(app, "nomi.toml"), withoutPG)
	if r := runNomiIn(t, app, "off", "capabilities", "--write"); r.exit != 0 {
		t.Errorf("nomi capabilities --write with no entry for pg:\n%s", r)
	}
	want = withoutPG + "\"lib.example/pg\" = [\"std::fs\"]\n"
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != want {
		t.Errorf("nomi.toml with pg's entry written:\n%s\nwant:\n%s", data, want)
	}
}

// --format json gives the same facts as data: self's lists and flags,
// deps' lists with each dependency's std names, those only tests reach,
// and whether it binds Go, and how nomi.toml's tables differ.
func TestCapabilities_CommandJSON(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule+"\n[capabilities.self]\nstd = [\"std::io\"]\n")
	r := runNomiIn(t, app, "off", "capabilities", "--format", "json")
	if r.exit != 0 {
		t.Fatalf("nomi capabilities --format json:\n%s", r)
	}
	var got capabilitiesJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, r.stdout)
	}
	if got.Project != "example.com/app" || strings.Join(got.Self.Std, " ") != "std::io" || strings.Join(got.Self.StdInTests, " ") != "std::instant" ||
		got.Self.Go || got.Self.GoInTests || strings.Join(got.Deps.Std, " ") != "std::fs" || len(got.Deps.StdInTests) != 0 ||
		len(got.Deps.Go) != 0 || len(got.Deps.GoInTests) != 0 || got.Written {
		t.Errorf("lists: %+v", got)
	}
	if m := got.Deps.Modules; len(m) != 2 || m[0].Path != "example.com/pricing" || strings.Join(m[0].Std, " ") != "std::fs" ||
		len(m[0].StdTestsOnly) != 0 || m[0].BindsGo || m[1].Path != "example.com/pure" || len(m[1].Std) != 0 {
		t.Errorf("modules: %+v", m)
	}
	wantChanges := []analysis.CapabilityChange{
		{Change: "add", List: "self.std_in_tests", Name: "std::instant"},
		{Change: "add", List: "deps"},
		{Change: "add", List: "deps.std", Name: "std::fs"},
		{Change: "add", List: "deps.modules", Module: "example.com/pricing", Name: "std::fs"},
		{Change: "add", List: "deps.modules", Module: "example.com/pure"},
	}
	if got.Table == nil || got.Table.Refused != 1 || fmt.Sprint(got.Table.Changes) != fmt.Sprint(wantChanges) {
		t.Errorf("table: %+v", got.Table)
	}
	for _, field := range []string{`"self": {`, `"deps": {`, `"std_tests_only"`, `"binds_go"`, `"declared_less": []`, `"problems": []`} {
		if !strings.Contains(r.stdout, field) {
			t.Errorf("the output has no %s:\n%s", field, r.stdout)
		}
	}
}

// --check holds nomi.toml's tables to the tightest ones in both
// directions: it exits 0 with one line for exact tables, and for no
// tables, which hold nothing; it exits 1 for narrower tables, which nomi
// check refuses, and for wider ones, which it passes, printing the
// missing and unused lines the plain report prints. A table, a ceiling or
// an entry the file leaves out is a choice, not a difference. --format
// json gives the report's object and the same exit status.
func TestCapabilities_Check(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule)
	none := runNomiIn(t, app, "off", "capabilities", "--check")
	if want := "nomi.toml has no [capabilities.self] or [capabilities.deps] table, so there is nothing to hold; nomi capabilities --write writes them\n"; none.exit != 0 || none.stdout != want {
		t.Errorf("--check with no tables:\n%s\nwant stdout:\n%s", none, want)
	}
	if r := runNomiIn(t, app, "off", "capabilities", "--check", "--format", "json"); r.exit != 0 || !strings.Contains(r.stdout, `"table": null`) {
		t.Errorf("--check --format json with no tables:\n%s", r)
	}

	exactly := "nomi.toml's [capabilities] tables allow exactly what the program reaches\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), capabilitiesModule+"\n"+capabilitiesTable)
	if exact := runNomiIn(t, app, "off", "capabilities", "--check"); exact.exit != 0 || exact.stdout != exactly {
		t.Errorf("--check with the tightest tables:\n%s\nwant stdout:\n%s", exact, exactly)
	}

	// Tables that leave out the dependencies' entries, their ceiling, or
	// all of [capabilities.deps], hold them by what is left, which --check
	// does not count as a difference.
	listsOnly, _, _ := strings.Cut(capabilitiesTable, "[capabilities.deps.modules]")
	selfOnly, _, _ := strings.Cut(capabilitiesTable, "[capabilities.deps]")
	for name, tables := range map[string]string{
		"no entries":    listsOnly,
		"no ceiling":    strings.Replace(capabilitiesTable, "[capabilities.deps]\nstd = [\"std::fs\"]\n", "[capabilities.deps]\n", 1),
		"no deps":       selfOnly,
		"no self":       strings.TrimPrefix(capabilitiesTable, strings.TrimSuffix(selfOnly, "\n")),
		"an empty deps": "[capabilities.deps]\n",
	} {
		mustWrite(t, filepath.Join(app, "nomi.toml"), capabilitiesModule+"\n"+tables)
		if r := runNomiIn(t, app, "off", "capabilities", "--check"); r.exit != 0 || r.stdout != exactly {
			t.Errorf("--check with %s:\n%s", name, r)
		}
	}

	wider := strings.Replace(capabilitiesTable, `std = ["std::io"]`, `std = ["std::io", "std::net"]`, 1)
	mustWrite(t, filepath.Join(app, "nomi.toml"), capabilitiesModule+"\n"+wider)
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Fatalf("nomi check refuses wider tables:\n%s", check)
	}
	r := runNomiIn(t, app, "off", "capabilities", "--check")
	want := "nomi.toml's [capabilities] tables differ:\n" +
		"  unused   self.std  std::net\n" +
		"nomi check passes; an unused name allows what nothing here reaches\n"
	if r.exit != 1 || r.stdout != want || r.stderr != "" {
		t.Errorf("--check with wider tables:\n%s\nwant stdout:\n%s", r, want)
	}

	narrower := strings.Replace(capabilitiesTable, `std = ["std::fs"]`, `std = []`, 1)
	mustWrite(t, filepath.Join(app, "nomi.toml"), capabilitiesModule+"\n"+narrower)
	r = runNomiIn(t, app, "off", "capabilities", "--check")
	want = "nomi.toml's [capabilities] tables differ:\n" +
		"  missing  deps.std  std::fs\n" +
		"nomi check refuses 1 call; nomi capabilities --write writes the tightest tables\n"
	if r.exit != 1 || r.stdout != want {
		t.Errorf("--check with narrower tables:\n%s\nwant stdout:\n%s", r, want)
	}
	r = runNomiIn(t, app, "off", "capabilities", "--check", "--format", "json")
	var got capabilitiesJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("--check --format json is not JSON: %v\n%s", err, r.stdout)
	}
	if r.exit != 1 || got.Table == nil || got.Table.Refused != 1 || len(got.Table.Changes) != 1 ||
		got.Table.Changes[0] != (analysis.CapabilityChange{Change: "add", List: "deps.std", Name: "std::fs"}) {
		t.Errorf("--check --format json with narrower tables:\n%s", r)
	}

	for _, args := range [][]string{{"--check", "--write"}, {"--why", "std::fs", "--check"}} {
		r := runNomiIn(t, app, "off", append([]string{"capabilities"}, args...)...)
		if r.exit != 1 || !strings.Contains(r.stderr, "do not go together") {
			t.Errorf("nomi capabilities %s:\n%s", strings.Join(args, " "), r)
		}
	}
}

// With entries_required, --check holds the tables to an entry for each
// dependency, and a missing one is a difference; nomi check fails on it
// too.
func TestCapabilities_CheckWithEntriesRequired(t *testing.T) {
	listsOnly, _, _ := strings.Cut(capabilitiesTable, "[capabilities.deps.modules]")
	app := capabilitiesProject(t, capabilitiesModule+"\n"+strings.Replace(listsOnly, "[capabilities.deps]\n", "[capabilities.deps]\nentries_required = true\n", 1))
	r := runNomiIn(t, app, "off", "capabilities", "--check")
	if r.exit != 1 || !strings.Contains(r.stdout, `  missing  deps.modules "example.com/pricing"  std::fs`) {
		t.Errorf("--check with entries_required and no entries:\n%s", r)
	}
}

// A problem nomi check finds in the tables themselves (a misspelled name,
// a dependency entries_required finds with no entry) is printed as nomi
// check prints it, and the report says nomi check fails on it rather than
// that it passes; --check fails on it too.
func TestCapabilities_ListProblems(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule)
	misspelled := strings.Replace(capabilitiesTable, `std = ["std::io"]`, `std = ["std::io", "std::fss"]`, 1)
	mustWrite(t, filepath.Join(app, "nomi.toml"), capabilitiesModule+"\n"+misspelled)
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 1 || !strings.Contains(check.stdout+check.stderr, "std::fss, which is not a std module") {
		t.Fatalf("nomi check with a misspelled name:\n%s", check)
	}
	problem := "nomi.toml:6: [capabilities.self] std names std::fss, which is not a std module\n" +
		"nomi.toml:6: help: did you mean \"std::fs\"?\n"
	want := "nomi.toml's [capabilities] tables differ:\n" +
		"  unused   self.std  std::fss\n" +
		problem +
		"nomi check fails on the problem above\n"
	if r := runNomiIn(t, app, "off", "capabilities"); r.exit != 0 || !strings.HasSuffix(r.stdout, "\n"+want) {
		t.Errorf("the report with a misspelled name:\n%s\nwant it to end:\n%s", r, want)
	}
	if r := runNomiIn(t, app, "off", "capabilities", "--check"); r.exit != 1 || r.stdout != want {
		t.Errorf("--check with a misspelled name:\n%s\nwant stdout:\n%s", r, want)
	}
	r := runNomiIn(t, app, "off", "capabilities", "--check", "--format", "json")
	var got capabilitiesJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("--check --format json is not JSON: %v\n%s", err, r.stdout)
	}
	if r.exit != 1 || got.Table == nil || len(got.Table.Problems) != 1 || got.Table.Problems[0]+"\n" != problem {
		t.Errorf("--check --format json with a misspelled name:\n%s", r)
	}

	// The tightest lists, with no entries, which entries_required asks of
	// each dependency.
	listsOnly, _, _ := strings.Cut(capabilitiesTable, "[capabilities.deps.modules]")
	mustWrite(t, filepath.Join(app, "nomi.toml"), capabilitiesModule+"\n"+strings.Replace(listsOnly, "[capabilities.deps]\n", "[capabilities.deps]\nentries_required = true\n", 1))
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 1 || !strings.Contains(check.stdout+check.stderr, "entries_required: example.com/pricing has no entry") {
		t.Fatalf("nomi check with entries_required:\n%s", check)
	}
	r = runNomiIn(t, app, "off", "capabilities")
	if r.exit != 0 || strings.Contains(r.stdout, "nomi check passes") ||
		!strings.Contains(r.stdout, "entries_required: example.com/pricing has no entry\n") ||
		!strings.HasSuffix(r.stdout, "nomi check fails on the 2 problems above\n") {
		t.Errorf("the report with entries_required and no entries:\n%s", r)
	}
}

// --why prints each call that reaches a std name, or that a module's files
// make, at its site with the chain a refusal's help gives; a name nothing
// reaches is an error that says what the program reaches.
func TestCapabilities_Why(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule)
	fs := runNomiIn(t, app, "off", "capabilities", "--why", "std::fs")
	want := "1 call reaches std::fs:\n  rules.nomi:4:5 in example.com/pricing\n    reached from `main`; `main` calls `load`\n"
	if fs.exit != 0 || fs.stdout != want {
		t.Errorf("--why std::fs:\n%s\nwant stdout:\n%s", fs, want)
	}
	module := runNomiIn(t, app, "off", "capabilities", "--why", "example.com/app")
	want = "3 calls in this project's own files reach outside the machine:\n" +
		"  main.nomi:9:5: std::io\n    reached from `main`\n" +
		"  main.nomi:10:5: std::io\n    reached from `main`\n" +
		"  main.nomi:14:9: std::instant\n    reached from test \"the clock moves\"\n"
	if module.exit != 0 || module.stdout != want {
		t.Errorf("--why example.com/app:\n%s\nwant stdout:\n%s", module, want)
	}
	unknown := runNomiIn(t, app, "off", "capabilities", "--why", "std::net")
	if unknown.exit != 1 || unknown.stderr != "nomi capabilities: nothing this program reaches is std::net; it reaches std::fs, std::instant, std::io, "+
		"and its code comes from example.com/app, example.com/pricing, example.com/pure\n" {
		t.Errorf("--why std::net:\n%s", unknown)
	}
}

// --why names an impl block's function in a chain by its interface and
// the type it is for, whichever use of it the program lowers first.
func TestCapabilities_WhyNamesTheTypeOfAnImpl(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), capabilitiesModule)
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
	why := runNomiIn(t, dir, "off", "capabilities", "--why", "std::fs")
	want := "1 call reaches std::fs:\n  main.nomi:17:5\n" +
		"    reached from `main`; `main` calls `Display.to_string for Loud`; `Display.to_string for Loud` calls `load`\n"
	if why.exit != 0 || why.stdout != want {
		t.Errorf("--why std::fs:\n%s\nwant stdout:\n%s", why, want)
	}
}

// A program with Go bindings is reported without its Go built: a
// dependency that binds Go is in deps' go, and --why names each binding.
func TestCapabilities_GoBindings(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	mustWrite(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pg v0.0.0\n\nreplace example.com/pg => ../pg\n")
	mustWrite(t, filepath.Join(app, "nomi.toml"), capabilitiesModule)
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    pg::db\nend\n\nfn main()\n    io.print(db.upper(\"hi\"))\nend\n")
	mustWrite(t, filepath.Join(dir, "pg", "go.mod"), "module example.com/pg\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(dir, "pg", "nomi.toml"), "[module]\nname = \"pg\"\n")
	mustWrite(t, filepath.Join(dir, "pg", "db.nomi"), "gopkg \"strings\" as gostrings\n\npub fn upper(s: String) -> String go gostrings.ToUpper\n")

	plain := runNomiIn(t, app, "off", "capabilities")
	if plain.exit != 0 || !strings.HasPrefix(plain.stdout, "self: example.com/app\n  std            std::io\n  std_in_tests   (none)\n  go             false\n  go_in_tests    false\n\n"+
		"deps\n  std            (none)\n  std_in_tests   (none)\n  go             example.com/pg\n") ||
		!strings.Contains(plain.stdout, "    example.com/pg\n      binds Go\n") {
		t.Errorf("nomi capabilities:\n%s", plain)
	}
	why := runNomiIn(t, app, "off", "capabilities", "--why", "example.com/pg")
	want := "1 call in example.com/pg reaches outside the machine:\n  db.nomi:3:8: go:strings\n    reached from `main`; `main` calls `upper`\n"
	if why.exit != 0 || why.stdout != want {
		t.Errorf("--why example.com/pg:\n%s\nwant stdout:\n%s", why, want)
	}
	write := runNomiIn(t, app, "off", "capabilities", "--write")
	if write.exit != 0 || !strings.Contains(write.stdout, "[capabilities.deps]\nstd = []\ngo = [\"example.com/pg\"]\n\n[capabilities.deps.modules]\n\"example.com/pg\" = []\n") {
		t.Errorf("nomi capabilities --write with a dependency that binds Go:\n%s", write)
	}
}

// A program that does not check is reported as nomi check reports it,
// and the command writes nothing.
func TestCapabilities_AProgramThatDoesNotCheck(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), capabilitiesModule)
	mustWrite(t, filepath.Join(dir, "main.nomi"), "fn main()\n    x = undefined_name\nend\n")
	r := runNomiIn(t, dir, "off", "capabilities", "--write")
	if r.exit != 1 || !strings.Contains(r.stderr, "undefined_name") || r.stdout != "" {
		t.Errorf("nomi capabilities --write on a program that does not check:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "nomi.toml")); string(data) != capabilitiesModule {
		t.Errorf("nomi.toml changed:\n%s", data)
	}
}

// check prints one refusal per name and list, the first call with a help
// line counting the rest; --all-calls prints every call, on check, run,
// test and build alike, and --format json has a record per call either
// way.
func TestCapabilities_RefusalsAreGrouped(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), capabilitiesModule+"\n[capabilities.self]\n")
	// The test reaches main's calls, which check reports once, from main.
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import std::io\n\nfn main()\n    io.print(\"a\")\n    io.print(\"b\")\n    io.print(\"c\")\nend\n\n"+
		"test \"prints\"\n    main()\n    assert 1 == 1\nend\n")
	check := runNomiIn(t, dir, "off", "check", "--format", "short", "main.nomi")
	want := "main.nomi:4:5: this module's own code reaches std::io, and [capabilities.self] allows it nothing outside the program\n" +
		"main.nomi:4:5: help: reached from `main`\n" +
		"main.nomi:4:5: help: add \"std::io\" to std under [capabilities.self] in nomi.toml, or remove the call\n" +
		"main.nomi:4:5: help: and 2 more calls reach std::io; --all-calls lists them\n"
	if check.exit != 1 || !strings.HasSuffix(check.stderr, want) || strings.Count(check.stderr, "this module's own code reaches") != 1 {
		t.Errorf("nomi check:\n%s\nwant stderr ending:\n%s", check, want)
	}
	for _, args := range [][]string{
		{"check", "--format", "short", "--all-calls", "main.nomi"},
		{"run", "--all-calls", "main.nomi"},
		{"test", "--all-calls", "main.nomi"},
		{"build", "--all-calls", "main.nomi", "-o", exe(filepath.Join(t.TempDir(), "app"))},
	} {
		r := runNomiIn(t, dir, "off", args...)
		if r.exit != 1 || strings.Count(r.stdout+r.stderr, "std::io, and [capabilities.self] allows") != 3 || strings.Contains(r.stdout+r.stderr, "more calls") {
			t.Errorf("nomi %s: want every call refused\n%s", strings.Join(args, " "), r)
		}
	}
	json := runNomiIn(t, dir, "off", "check", "--format", "json", "main.nomi")
	if json.exit != 1 || strings.Count(json.stdout, `"code":"capability-not-allowed"`) != 3 || strings.Contains(json.stdout, "more calls") {
		t.Errorf("nomi check --format json: want a record per call\n%s", json)
	}
}
