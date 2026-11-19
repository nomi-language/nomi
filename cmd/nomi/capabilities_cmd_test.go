package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
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

// What capabilitiesProject reaches, as the report's lists and modules
// print it.
const capabilitiesReached = `std            std::fs, std::io
std_in_tests   std::instant
go             (none)
go_in_tests    (none)

modules
  example.com/app (this project)
    std::instant (tests only), std::io
  example.com/pricing
    std::fs
  example.com/pure
    nothing
`

// The table --write writes for capabilitiesProject.
const capabilitiesTable = `[capabilities]
std = ["std::fs", "std::io"]
std_in_tests = ["std::instant"]

[capabilities.modules]
"example.com/app" = ["std::io"]
"example.com/pricing" = ["std::fs"]
"example.com/pure" = []
`

// With no table, the command prints what the program reaches and says
// nothing holds it; --write writes the tightest table at the end of
// nomi.toml, which nomi check then passes, and a second --write writes
// nothing and says the table is current.
func TestCapabilities_CommandWritesATableTheCheckPasses(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule)
	plain := runNomiIn(t, app, "off", "capabilities")
	want := capabilitiesReached + "\nnomi.toml has no [capabilities] table; nomi capabilities --write writes this one\n"
	if plain.exit != 0 || plain.stdout != want {
		t.Errorf("nomi capabilities:\n%s\nwant stdout:\n%s", plain, want)
	}

	write := runNomiIn(t, app, "off", "capabilities", "--write")
	if want := "wrote nomi.toml's [capabilities] table:\n\n" + capabilitiesTable; write.exit != 0 || write.stdout != want {
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
		t.Errorf("nomi check with the table written:\n%s", check)
	}
	again := runNomiIn(t, app, "off", "capabilities", "--write")
	if again.exit != 0 || again.stdout != "nomi.toml's [capabilities] table is current\n" {
		t.Errorf("a second --write:\n%s", again)
	}
	if after, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(after) != string(data) {
		t.Errorf("a second --write changed nomi.toml:\n%s", after)
	}
	plain = runNomiIn(t, app, "off", "capabilities")
	if want := capabilitiesReached + "\nnomi.toml's [capabilities] table allows exactly this\n"; plain.stdout != want {
		t.Errorf("nomi capabilities with the table written:\n%s\nwant stdout:\n%s", plain, want)
	}
}

// A table looser than the program needs passes, and the report says which
// names nothing needs; one tighter is refused by check, and the report
// says what is missing. --write prints each name it adds and removes, so
// a widening is never silent.
func TestCapabilities_CommandComparesWithTheTable(t *testing.T) {
	loose := capabilitiesModule + "\n[capabilities]\nstd = [\"std::fs\", \"std::io\", \"std::net\"]\nstd_in_tests = [\"std::instant\", \"std::process\"]\n"
	app := capabilitiesProject(t, loose)
	plain := runNomiIn(t, app, "off", "capabilities")
	want := capabilitiesReached + "\nnomi.toml's [capabilities] table differs:\n" +
		"  missing  modules \"example.com/app\"      std::io\n" +
		"  missing  modules \"example.com/pricing\"  std::fs\n" +
		"  missing  modules \"example.com/pure\"     (an empty entry)\n" +
		"  unused   std                            std::net\n" +
		"  unused   std_in_tests                   std::process\n" +
		"nomi check passes; an unused name allows what nothing here reaches\n"
	if plain.exit != 0 || plain.stdout != want {
		t.Errorf("nomi capabilities with a looser table:\n%s\nwant stdout:\n%s", plain, want)
	}
	write := runNomiIn(t, app, "off", "capabilities", "--write")
	if !strings.HasSuffix(write.stdout, capabilitiesTable+"\n"+
		"  added    modules \"example.com/app\"      std::io\n"+
		"  added    modules \"example.com/pricing\"  std::fs\n"+
		"  added    modules \"example.com/pure\"     (an empty entry)\n"+
		"  removed  std                            std::net\n"+
		"  removed  std_in_tests                   std::process\n") {
		t.Errorf("nomi capabilities --write over a looser table:\n%s", write)
	}

	tight := capabilitiesModule + "\n[capabilities]\nstd = [\"std::io\"]\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), tight)
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 1 {
		t.Fatalf("nomi check with a tighter table passes:\n%s", check)
	}
	plain = runNomiIn(t, app, "off", "capabilities")
	if !strings.Contains(plain.stdout, fmt.Sprintf("  missing  %-29s  std::fs\n", "std")) ||
		!strings.HasSuffix(plain.stdout, "nomi check refuses 2 calls; nomi capabilities --write writes the table above\n") {
		t.Errorf("nomi capabilities with a tighter table:\n%s", plain)
	}
	write = runNomiIn(t, app, "off", "capabilities", "--write")
	if !strings.Contains(write.stdout, fmt.Sprintf("  added    %-29s  std::fs\n", "std")) || !strings.Contains(write.stdout, fmt.Sprintf("  added    %-29s  std::instant\n", "std_in_tests")) {
		t.Errorf("nomi capabilities --write over a tighter table:\n%s", write)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check after --write:\n%s", check)
	}
}

// --write changes only the lines of the two tables, from each header to
// its last key: comments around them, other tables, a table named for a
// required module, and require_module_lists stay as they were. A list
// that changes keeps the names it still holds in its order, then the new
// ones, and the comment on its line; a key the file lacks goes after the
// keys it has.
func TestCapabilities_WriteKeepsTheRestOfNomiToml(t *testing.T) {
	before := "# The app.\n[module]\nname = \"app\"   # its name\nentry_points = [\"main\"]\n\n" +
		"# What it may reach.\n[capabilities]\nstd = [\"std::io\"]  # too few\nrequire_module_lists = true # every module\n\n" +
		"# The tables of the tools.\n[pricing]\nrates = \"rates.csv\"\n\n" +
		"[capabilities.modules]\n\"example.com/pricing\" = []\n# trailing comment\n"
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
		"# What it may reach.\n[capabilities]\nstd = [\"std::io\", \"std::fs\"]  # too few\nrequire_module_lists = true # every module\nstd_in_tests = [\"std::instant\"]\n\n" +
		"# The tables of the tools.\n[pricing]\nrates = \"rates.csv\"\n\n" +
		"[capabilities.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n\"example.com/app\" = [\"std::io\"]\n\"example.com/pure\" = []\n# trailing comment\n"
	if string(data) != want {
		t.Errorf("nomi.toml:\n%s\nwant:\n%s", data, want)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check after --write:\n%s", check)
	}
}

// annotatedManifest is the tooling reference's example of a table with a
// comment on each key, for annotatedProject.
const annotatedManifest = `[module]
name = "app"
entry_points = ["main"]

[capabilities]
# std modules the program may call into, from any module's code
std = ["std::io", "std::fs", "std::http"]

# added to std for test cases: a test may reach these as well
std_in_tests = ["std::process"]

# modules whose code may bind Go. The compiler does not see into Go.
go = ["lib.example/pg"]

# every module the program's code comes from must have a list below
require_module_lists = true

[capabilities.modules]
# the project being built, example.com/app: its own code may print and
# use files, but not the network
"example.com/app" = ["std::io", "std::fs"]

# the four modules it depends on, each held to what its own code reaches:

# computes, and reaches nothing
"lib.example/pricing" = []

# reads and writes files
"lib.example/reports" = ["std::fs"]

# the one module that may make HTTP requests
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

// --write keeps the comments of a table it changes: the annotated example
// the program needs all of is current and stays byte for byte; an entry
// the program no longer needs goes with its comment and the blank line
// before it, leaving the rest as they were; and an entry written new for a
// module that binds Go says so.
func TestCapabilities_WriteKeepsComments(t *testing.T) {
	app := annotatedProject(t, true)
	if r := runNomiIn(t, app, "off", "capabilities", "--write"); r.exit != 0 || r.stdout != "nomi.toml's [capabilities] table is current\n" {
		t.Errorf("nomi capabilities --write over the annotated example:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != annotatedManifest {
		t.Errorf("a current table changed nomi.toml:\n%s", data)
	}
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Errorf("nomi check of the annotated example:\n%s", check)
	}

	app = annotatedProject(t, false)
	if r := runNomiIn(t, app, "off", "capabilities", "--write"); r.exit != 0 || !strings.HasSuffix(r.stdout, "  removed  modules \"lib.example/reports\"  std::fs\n") {
		t.Errorf("nomi capabilities --write with reports unused:\n%s", r)
	}
	want := strings.Replace(annotatedManifest, "# reads and writes files\n\"lib.example/reports\" = [\"std::fs\"]\n\n", "", 1)
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != want {
		t.Errorf("nomi.toml with reports unused:\n%s\nwant:\n%s", data, want)
	}

	app = annotatedProject(t, true)
	mustWrite(t, filepath.Join(app, "nomi.toml"), strings.TrimSuffix(annotatedManifest, "\n# Nomi code over a Go package; its Nomi code also reads files\n\"lib.example/pg\" = [\"std::fs\"]\n"))
	if r := runNomiIn(t, app, "off", "capabilities", "--write"); r.exit != 0 {
		t.Errorf("nomi capabilities --write with no entry for pg:\n%s", r)
	}
	want = strings.Replace(annotatedManifest, "# Nomi code over a Go package; its Nomi code also reads files\n\"lib.example/pg\" = [\"std::fs\"]\n", "\"lib.example/pg\" = [\"std::fs\"]  # binds Go\n", 1)
	if data, _ := os.ReadFile(filepath.Join(app, "nomi.toml")); string(data) != want {
		t.Errorf("nomi.toml with pg's entry written:\n%s\nwant:\n%s", data, want)
	}
}

// replaceCapabilityTables keeps each comment that belongs to a key it
// keeps, and drops one that belongs to a key it removes: a comment line
// directly above a key, one on its line, and in a list written one name
// per line, one above or on a name's line. A comment no key owns stays,
// and so does a run of blank lines, unless removing a key leaves two runs
// together. A key the table lacks is added after the keys it has, with no
// comment.
func TestCapabilities_WriteRulesForComments(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule)
	tight := func() *vmhost.Capabilities {
		return &vmhost.Capabilities{
			Std: []string{"std::fs", "std::io"}, StdInTests: []string{"std::instant"},
			Modules: map[string][]string{"example.com/app": {"std::io"}, "example.com/pricing": {"std::fs"}, "example.com/pure": {}},
		}
	}
	withoutPure := tight()
	delete(withoutPure.Modules, "example.com/pure")
	withProcess := tight()
	withProcess.Std = []string{"std::fs", "std::io", "std::process"}
	// caps and mods are the two tables as tight writes them.
	caps := "[capabilities]\nstd = [\"std::fs\", \"std::io\"]\nstd_in_tests = [\"std::instant\"]\n\n"
	mods := "\n[capabilities.modules]\n\"example.com/app\" = [\"std::io\"]\n\"example.com/pricing\" = [\"std::fs\"]\n\"example.com/pure\" = []\n"
	cases := []struct {
		name, before, after string
		c                   *vmhost.Capabilities
	}{{
		name: "a list one name per line",
		before: "[capabilities]\nstd = [\n    # through pricing\n    \"std::fs\",\n    \"std::io\",  # printing\n\n    # to remove\n    \"std::net\", # gone\n]\n" +
			"std_in_tests = [\"std::instant\"]  # the clock\n" + mods,
		after: "[capabilities]\nstd = [\n    # through pricing\n    \"std::fs\",\n    \"std::io\",  # printing\n    \"std::process\",\n]\n" +
			"std_in_tests = [\"std::instant\"]  # the clock\n" + mods,
		c: withProcess,
	}, {
		name: "a list that no longer holds a name",
		before: "[capabilities]\nstd = [\"std::fs\", \"std::io\"]\n# the clock\nstd_in_tests = [\"std::instant\"]\n\n# ours\ngo = [\"example.com/app\"]\n" +
			"require_module_lists = false\n" + mods,
		after: "[capabilities]\nstd = [\"std::fs\", \"std::io\"]\n# the clock\nstd_in_tests = [\"std::instant\"]\n\nrequire_module_lists = false\n" + mods,
		c:     tight(),
	}, {
		name:   "a comment on a key's line",
		before: "[capabilities]\nstd = [\"std::io\"]  # what it does\nstd_in_tests = [\"std::instant\", \"std::net\"] # tests\n" + mods,
		after:  "[capabilities]\nstd = [\"std::io\", \"std::fs\"]  # what it does\nstd_in_tests = [\"std::instant\"] # tests\n" + mods,
		c:      tight(),
	}, {
		name: "a comment no key owns",
		before: caps + "[capabilities.modules]\n# the project\n\"example.com/app\" = [\"std::io\"]\n\n# Dependencies, by path.\n\n" +
			"# computes\n\"example.com/pricing\" = []\n# reaches nothing\n\"example.com/pure\" = []\n",
		after: caps + "[capabilities.modules]\n# the project\n\"example.com/app\" = [\"std::io\"]\n\n# Dependencies, by path.\n\n" +
			"# computes\n\"example.com/pricing\" = [\"std::fs\"]\n",
		c: withoutPure,
	}, {
		name:   "blank lines a removed key leaves together",
		before: caps + "[capabilities.modules]\n\"example.com/app\" = [\"std::io\"]\n\n\"example.com/pure\" = []\n\n\"example.com/pricing\" = [\"std::fs\"]\n",
		after:  caps + "[capabilities.modules]\n\"example.com/app\" = [\"std::io\"]\n\n\"example.com/pricing\" = [\"std::fs\"]\n",
		c:      withoutPure,
	}, {
		name:   "a key added",
		before: caps + "[capabilities.modules]\n# the project\n\"example.com/app\" = [\"std::io\"]\n\n# computes\n\"example.com/pricing\" = [\"std::fs\"]\n",
		after:  caps + "[capabilities.modules]\n# the project\n\"example.com/app\" = [\"std::io\"]\n\n# computes\n\"example.com/pricing\" = [\"std::fs\"]\n\n\"example.com/pure\" = []\n",
		c:      tight(),
	}, {
		name:   "a list grown past the width",
		before: "[capabilities]\nstd = [\"std::fs\", \"std::io\"] # all of it\n",
		after: "[capabilities]\nstd = [ # all of it\n    \"std::fs\",\n    \"std::io\",\n    \"std::http\",\n    \"std::instant\",\n" +
			"    \"std::process\",\n    \"std::random\",\n]\n",
		c: &vmhost.Capabilities{Std: []string{"std::fs", "std::http", "std::instant", "std::io", "std::process", "std::random"}},
	}, {
		name:   "CRLF line endings",
		before: "# app\r\n[module]\r\nname = \"app\"\r\n\r\n[capabilities]\r\n# all\r\nstd = [\"std::io\"]\r\n\r\n[capabilities.modules]\r\n\"example.com/pure\" = [] # none\r\n",
		after: "# app\r\n[module]\r\nname = \"app\"\r\n\r\n[capabilities]\r\n# all\r\nstd = [\"std::io\", \"std::fs\"]\r\nstd_in_tests = [\"std::instant\"]\r\n\r\n" +
			"[capabilities.modules]\r\n\"example.com/pure\" = [] # none\r\n\"example.com/app\" = [\"std::io\"]\r\n\"example.com/pricing\" = [\"std::fs\"]\r\n",
		c: tight(),
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := c.before
			if !strings.HasPrefix(before, "# app") {
				before = capabilitiesModule + "\n" + before
			}
			got, _, err := replaceCapabilityTables(app, []byte(before), c.c)
			if err != nil {
				t.Fatal(err)
			}
			want := c.after
			if !strings.HasPrefix(want, "# app") {
				want = capabilitiesModule + "\n" + want
			}
			if string(got) != want {
				t.Errorf("written:\n%q\nwant:\n%q", got, want)
			}
		})
	}
}

// --format json gives the same facts as data: the four lists, each
// module's std names with those only tests reach, whether it binds Go, and
// how nomi.toml's table differs.
func TestCapabilities_CommandJSON(t *testing.T) {
	app := capabilitiesProject(t, capabilitiesModule+"\n[capabilities]\nstd = [\"std::io\"]\n")
	r := runNomiIn(t, app, "off", "capabilities", "--format", "json")
	if r.exit != 0 {
		t.Fatalf("nomi capabilities --format json:\n%s", r)
	}
	var got capabilitiesJSON
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, r.stdout)
	}
	if got.Project != "example.com/app" || strings.Join(got.Std, " ") != "std::fs std::io" || strings.Join(got.StdInTests, " ") != "std::instant" ||
		len(got.Go) != 0 || len(got.GoInTests) != 0 || got.Written {
		t.Errorf("lists: %+v", got)
	}
	if len(got.Modules) != 3 || !got.Modules[0].Project || strings.Join(got.Modules[0].Std, " ") != "std::instant std::io" ||
		strings.Join(got.Modules[0].StdTestsOnly, " ") != "std::instant" || got.Modules[2].Path != "example.com/pure" || len(got.Modules[2].Std) != 0 {
		t.Errorf("modules: %+v", got.Modules)
	}
	if got.Table == nil || got.Table.Refused != 2 || len(got.Table.Changes) != 5 ||
		got.Table.Changes[0] != (tableChange{Change: "add", List: "std", Name: "std::fs"}) {
		t.Errorf("table: %+v", got.Table)
	}
	for _, field := range []string{`"std_tests_only"`, `"binds_go"`, `"declared_less": []`} {
		if !strings.Contains(r.stdout, field) {
			t.Errorf("the output has no %s:\n%s", field, r.stdout)
		}
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

// A program with Go bindings is reported without its Go built: a module
// that binds Go is in go, and --why names each binding.
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
	if plain.exit != 0 || !strings.HasPrefix(plain.stdout, "std            std::io\nstd_in_tests   (none)\ngo             example.com/pg\n") ||
		!strings.Contains(plain.stdout, "  example.com/pg\n    binds Go\n") {
		t.Errorf("nomi capabilities:\n%s", plain)
	}
	why := runNomiIn(t, app, "off", "capabilities", "--why", "example.com/pg")
	want := "1 call in example.com/pg reaches outside the machine:\n  db.nomi:3:8: go:strings\n    reached from `main`; `main` calls `upper`\n"
	if why.exit != 0 || why.stdout != want {
		t.Errorf("--why example.com/pg:\n%s\nwant stdout:\n%s", why, want)
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
	mustWrite(t, filepath.Join(dir, "nomi.toml"), capabilitiesModule+"\n[capabilities]\n")
	// The test reaches main's calls, which check reports once, from main.
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import std::io\n\nfn main()\n    io.print(\"a\")\n    io.print(\"b\")\n    io.print(\"c\")\nend\n\n"+
		"test \"prints\"\n    main()\n    assert 1 == 1\nend\n")
	check := runNomiIn(t, dir, "off", "check", "--format", "short", "main.nomi")
	want := "main.nomi:4:5: this program reaches std::io, and nomi.toml allows nothing outside the program\n" +
		"main.nomi:4:5: help: reached from `main`\n" +
		"main.nomi:4:5: help: add \"std::io\" to std under [capabilities] in nomi.toml, or remove the call\n" +
		"main.nomi:4:5: help: and 2 more calls reach std::io; --all-calls lists them\n"
	if check.exit != 1 || !strings.HasSuffix(check.stderr, want) || strings.Count(check.stderr, "this program reaches") != 1 {
		t.Errorf("nomi check:\n%s\nwant stderr ending:\n%s", check, want)
	}
	for _, args := range [][]string{
		{"check", "--format", "short", "--all-calls", "main.nomi"},
		{"run", "--all-calls", "main.nomi"},
		{"test", "--all-calls", "main.nomi"},
		{"build", "--all-calls", "main.nomi", "-o", exe(filepath.Join(t.TempDir(), "app"))},
	} {
		r := runNomiIn(t, dir, "off", args...)
		if r.exit != 1 || strings.Count(r.stdout+r.stderr, "std::io, and nomi.toml allows") != 3 || strings.Contains(r.stdout+r.stderr, "more calls") {
			t.Errorf("nomi %s: want every call refused\n%s", strings.Join(args, " "), r)
		}
	}
	json := runNomiIn(t, dir, "off", "check", "--format", "json", "main.nomi")
	if json.exit != 1 || strings.Count(json.stdout, `"code":"capability-not-allowed"`) != 3 || strings.Contains(json.stdout, "more calls") {
		t.Errorf("nomi check --format json: want a record per call\n%s", json)
	}
}
