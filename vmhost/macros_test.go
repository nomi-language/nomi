package vmhost

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// fixtureMacros is the fixture: module csvdata declares the
// `table` macro, written against std/macro alone, and module
// rates uses it.
const fixtureMacros = "../testdata/macros"

// copyFixture copies the fixture's modules into a fresh directory and
// answers it, so a test may edit them.
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir(fixtureMacros, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixtureMacros, path)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// macroModules writes a module `ext` whose ext.nomi is extSrc, and a
// module `app` that requires it, holding files; it answers app's root.
func macroModules(t *testing.T, extSrc string, files map[string]string) string {
	t.Helper()
	return macroModulesNamed(t, "ext", extSrc, files)
}

// macroModulesNamed is macroModules with the macro's module named name.
func macroModulesNamed(t *testing.T, name, extSrc string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, name, "nomi.toml"), "[module]\nname = \""+name+"\"\n")
	writeFile(t, filepath.Join(dir, name, "go.mod"), "module "+name+"\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(dir, name, name+".nomi"), extSrc)
	app := filepath.Join(dir, "app")
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	writeFile(t, filepath.Join(app, "go.mod"), "module app\n\ngo 1.27.0\n\nrequire "+name+" v0.0.0\n\nreplace "+name+" => ../"+name+"\n")
	for name, text := range files {
		writeFile(t, filepath.Join(app, name), text)
	}
	return app
}

// genMacro is a macro `gen` whose expand is body, with helpers
// after it. It imports what body and helpers name, and binds the fields of
// expand's record that body reads.
func genMacro(body, helpers string) string {
	uses := body + helpers
	var names []string
	for _, n := range []string{"Declaration", "ExpandCall", "Expansion", "Input", "Macro", "Request", "RequestCall", "Site", "Source"} {
		switch n {
		case "Declaration", "Input", "Site", "Source":
			if !strings.Contains(uses, n) {
				continue
			}
		}
		names = append(names, n)
	}
	imports := "    std/macro.{" + strings.Join(names, ", ") + "}\n"
	if strings.Contains(uses, "io.") {
		imports += "    std/io\n"
	}
	bindings := ""
	for _, p := range []string{"decl", "inputs", "sites"} {
		if strings.Contains(body, p) {
			bindings += "        " + p + " = call." + p + "\n"
		}
	}
	if bindings == "" {
		bindings = "        _ = call\n"
	}
	return "import {\n" + imports + `}

pub macro gen: Gen

type Gen

impl Macro for Gen {
    fn request(call: RequestCall) -> Request {
        _ = call
        #Request{files: [], sites: ["gen"]}
    }

    fn expand(call: ExpandCall) -> Expansion {
` + bindings + body + `
    }
}
` + helpers
}

const genMain = "import {\n    ext.gen\n    std/io\n}\n\ngen! answer\n\nfn main() {\n    io.print(answer())\n}\n"

// macroLoadOrderProgram is a module `app` that uses a macro of
// module `ext`, beside it: the load-order test's case of a program whose
// front end builds and runs another program (TestLoadOrderDoesNotChangeAProgram).
const macroLoadOrderProgram = `// FILE: main.nomi
import {
    ext.gen
    std/io
}

gen! answer ` + "`41`" + `

fn main() {
    io.print(answer() + 1)
}
// FILE: nomi.toml
[module]
name = "app"
entry_points = ["main"]
// FILE: go.mod
module app

go 1.27.0

require ext v0.0.0

replace ext => ./ext
// FILE: ext/nomi.toml
[module]
name = "ext"
// FILE: ext/go.mod
module ext

go 1.27.0
// FILE: ext/ext.nomi
import {
    std/macro.{Declaration, ExpandCall, Expansion, Macro, Source}
}

pub macro gen: Gen

type Gen

impl Macro for Gen {
    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl

        case decl.body {
            Some(body) ->
                Source.new()
                |> Source.add("fn ${decl.name}() -> Int {\n    ")
                |> Source.mapped(body.text, body.span)
                |> Source.add("\n}\n")
                |> Source.expansion()

            None -> Declaration.error(decl, "no value")
        }
    }
}
`

// The load-order test's macro program runs: its expansion is the
// function the program calls, with the body's text as its value.
func TestMacro_LoadOrderProgramRuns(t *testing.T) {
	entry, err := writeInput(t.TempDir(), macroLoadOrderProgram)
	if err != nil {
		t.Fatal(err)
	}
	if got := runText(t, entry); got != "42\n" {
		t.Errorf("got %q, want 42", got)
	}
}

// checkText is the text of what checking the program at path reports, or
// "" when it checks.
func checkText(t *testing.T, path string) string {
	t.Helper()
	err := Check(path)
	if err == nil {
		return ""
	}
	return err.Error()
}

func runText(t *testing.T, path string) string {
	t.Helper()
	var out bytes.Buffer
	p, err := Load(path, WithOutput(&out))
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run %s: %v", path, err)
	}
	return out.String()
}

// The fixture's tables expand to lookup functions, which run.
func TestMacro_FixtureRuns(t *testing.T) {
	got := runText(t, filepath.Join(fixtureMacros, "rates", "main.nomi"))
	want := "9200\nPound \"sterling\"\nYen ${not interpolated}\nFalse\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if text := checkText(t, filepath.Join(fixtureMacros, "csvdata", "csvdata.nomi")); text != "" {
		t.Errorf("the macro module does not check: %s", text)
	}
}

// A declaration may name a type: cafe's `pub choices.one_of! Size
// `small medium large“ declares `pub enum Size`, which main.nomi imports
// and matches on.
func TestMacro_ADeclarationNamesAType(t *testing.T) {
	if got := runText(t, filepath.Join(fixtureMacros, "cafe", "main.nomi")); got != "12\n" {
		t.Errorf("got %q, want 12", got)
	}
	if text := checkText(t, filepath.Join(fixtureMacros, "choices", "choices.nomi")); text != "" {
		t.Errorf("the macro module does not check: %s", text)
	}

	dir := copyFixture(t)
	sizes := filepath.Join(dir, "cafe", "sizes.nomi")
	main := filepath.Join(dir, "cafe", "main.nomi")
	// An item import brings the keyword in bare, and the name is still a
	// type's.
	writeFile(t, sizes, "import choices.one_of\n\n/// How big a coffee is.\npub one_of! Size `small medium large`\n")
	if got := runText(t, main); got != "12\n" {
		t.Errorf("with the keyword imported, got %q, want 12", got)
	}

	// A type of the file with the declaration's name collides, and the
	// file's own type keeps its meaning.
	writeFile(t, sizes, "import choices\n\npub choices.one_of! Size `small medium large`\n\npub enum Size {\n    Tall\n}\n")
	text := checkText(t, main)
	if !strings.Contains(text, "`Size` is declared here and by `choices.one_of! Size` on line 3") || !strings.Contains(text, "sizes.nomi:5:10") {
		t.Errorf("the collision is not reported at the file's type:\n%s", text)
	}
	if strings.Contains(text, "main.nomi") {
		t.Errorf("the collision is not reported alone:\n%s", text)
	}

	// The macro sees the case of the name itself.
	writeFile(t, sizes, "import choices\n\npub choices.one_of! size `small medium large`\n")
	text = checkText(t, main)
	if !strings.Contains(text, "a `one_of` declares a type, so its name is a type name: `Size`") || !strings.Contains(text, "sizes.nomi:3:21") {
		t.Errorf("a lowercase name is not the macro's error at the name:\n%s", text)
	}
}

// What `nomi expand` prints: the source the macro wrote.
func TestMacro_Expansions(t *testing.T) {
	exps, err := Expansions(filepath.Join(fixtureMacros, "rates", "tables.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(exps) != 2 || exps[0].Name != "rate" || exps[1].Name != "currency_name" || exps[0].Macro != "table" || exps[0].Module != "csvdata" {
		t.Fatalf("got %+v", exps)
	}
	if !strings.Contains(exps[0].Source, "pub fn rate(code: String) -> Maybe<Int> {") || !strings.Contains(exps[0].Source, `"usd" -> Some(9200)`) {
		t.Errorf("rate expands to:\n%s", exps[0].Source)
	}
	if !strings.Contains(exps[1].Source, `"jpy" -> Some("Yen \${not interpolated}")`) {
		t.Errorf("currency_name expands to:\n%s", exps[1].Source)
	}
}

// A value of the wrong type is the checker's error at its CSV cell, with
// the macro's hint; a bad row and a repeated key are the macro's
// own diagnostics, in the CSV.
func TestMacro_DiagnosticsLandInTheCSV(t *testing.T) {
	dir := copyFixture(t)
	csv := filepath.Join(dir, "rates", "data", "rates.csv")
	main := filepath.Join(dir, "rates", "main.nomi")

	writeFile(t, csv, "code,basis_points\nusd,9200\ngbp,1.5\n")
	text := checkText(t, main)
	for _, want := range []string{
		"case branch type mismatch: expected Maybe<Int>, got Maybe<Float>",
		csv + ":3:5",
		"values of `rate` are of type `Int`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the check does not say %q:\n%s", want, text)
		}
	}

	writeFile(t, csv, "code,basis_points\nusd,9200\neur,1,2\nusd,9300\n")
	text = checkText(t, main)
	for _, want := range []string{
		csv + ":3:1",
		"this row has 3 cells; a table row is a key and a value",
		csv + ":4:1",
		"duplicate key usd: line 2 has it already",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the check does not say %q:\n%s", want, text)
		}
	}

	os.Remove(csv)
	text = checkText(t, main)
	if !strings.Contains(text, "no file data/rates.csv") || !strings.Contains(text, "tables.nomi:4:54") {
		t.Errorf("a missing table is not reported at the body:\n%s", text)
	}
}

// The macro's ProgramCheck sees every declaration of its keyword:
// two tables reading one file is an error at the second.
func TestMacro_ProgramCheck(t *testing.T) {
	dir := copyFixture(t)
	writeFile(t, filepath.Join(dir, "rates", "more.nomi"), "import csvdata.table\n\npub table! again(code: String) -> Maybe<Int> `rates.csv`\n")
	writeFile(t, filepath.Join(dir, "rates", "main.nomi"), "import {\n    more\n    std/io\n    tables\n}\n\nfn main() {\n    io.print(Maybe.with_default(tables.rate(\"usd\"), 0) + Maybe.with_default(more.again(\"usd\"), 0))\n}\n")
	text := checkText(t, filepath.Join(dir, "rates", "main.nomi"))
	if !strings.Contains(text, "`again` reads data/rates.csv too; each file is read by one table") || !strings.Contains(text, "tables.nomi:4:54") {
		t.Errorf("the ProgramCheck's error is missing:\n%s", text)
	}
}

// A macro is a pure function of what it is given: one that reaches
// an effect, loops, allocates without bound or fails is an error at the
// declaration, saying so.
func TestMacro_Sandbox(t *testing.T) {
	steps, memory := macroSteps, macroMemory
	macroSteps, macroMemory = 2_000_000, 64<<20
	t.Cleanup(func() { macroSteps, macroMemory = steps, memory })
	cases := []struct {
		name, body, helpers, want string
	}{
		{"effect", "        io.print(\"expanding\")\n        Declaration.error(decl, \"no\")", "",
			"the macro `gen` (module ext) could not expand `gen! answer`: its `expand` has effects, which a macro may not have while a program is checked ("},
		{"loop", "        spin(0)", "\nfn spin(n: Int) -> Expansion {\n    spin(n + 1)\n}\n",
			"could not expand `gen! answer`: `expand` did not finish within its budget of 2,000,000 steps (calls, backward branches and iteration elements)"},
		{"memory", "        grow(\"x\")", "\nfn grow(s: String) -> Expansion {\n    grow(s + s)\n}\n",
			"could not expand `gen! answer`: `expand` allocated more than its memory budget of 64 MiB"},
		{"fault", "        _ = Iter.reduce([1], fn a, b -> a / (b - 1), from: 0)\n        Declaration.error(decl, \"no\")", "",
			"could not expand `gen! answer`: `expand` failed: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			MacroCacheForTest()
			app := macroModules(t, genMacro(c.body, c.helpers), map[string]string{"main.nomi": genMain})
			text := checkText(t, filepath.Join(app, "main.nomi"))
			if !strings.Contains(text, c.want) || !strings.Contains(text, "main.nomi:6:1") {
				t.Errorf("got:\n%s\nwant it to hold %q at the declaration", text, c.want)
			}
			// The declaration declares nothing, and the refusal is
			// reported alone: no error for the call of `answer`.
			errs := 0
			for _, line := range strings.Split(text, "\n") {
				if !strings.Contains(line, ": help: ") {
					errs++
				}
			}
			if errs != 1 {
				t.Errorf("the refusal is not reported alone:\n%s", text)
			}
		})
	}
}

// The steps and the memory decide whether an expansion succeeds, never
// the clock: a run past its steps fails with the same text under a hang
// guard of an hour and under the default one. A run the hang guard stops
// (a nanosecond here, standing in for a machine too slow for any guard) is
// an internal failure that says so, is not cached, and runs again at the
// next check, which then answers what the steps decide.
func TestMacro_HangGuard(t *testing.T) {
	steps, guard := macroSteps, macroHangGuard
	t.Cleanup(func() { macroSteps, macroHangGuard = steps, guard })
	macroSteps = 200_000
	loop := genMacro("        spin(0)", "\nfn spin(n: Int) -> Expansion {\n    spin(n + 1)\n}\n")
	const stepsText = "the macro `gen` (module ext) could not expand `gen! answer`: `expand` did not finish within its budget of 200,000 steps (calls, backward branches and iteration elements)"
	const guardText = "`expand` was still running after 1ns, so the compiler stopped it. This is an internal failure, not the macro's answer"

	app := macroModules(t, loop, map[string]string{"main.nomi": genMain})
	main := filepath.Join(app, "main.nomi")
	check := func(g time.Duration) string {
		macroHangGuard = g
		return checkText(t, main)
	}
	MacroCacheForTest()
	long := check(time.Hour)
	if !strings.Contains(long, stepsText) || !strings.Contains(long, "main.nomi:6:1") {
		t.Fatalf("under an hour's guard the check says:\n%s\nwant %q at the declaration", long, stepsText)
	}
	if got := check(guard); got != long {
		t.Errorf("under the default guard the check says:\n%s\nunder an hour's:\n%s", got, long)
	}

	before := ExpansionRunsForTest()
	for range 2 {
		text := check(time.Nanosecond)
		if !strings.Contains(text, guardText) || !strings.Contains(text, "Nothing was cached, and the next check runs the macro again") {
			t.Fatalf("under a guard of 1ns the check says:\n%s\nwant %q", text, guardText)
		}
		if strings.Contains(text, "budget of 200,000 steps (calls") {
			t.Errorf("the hang guard's failure reads as the step budget's:\n%s", text)
		}
	}
	if runs := ExpansionRunsForTest() - before; runs != 2 {
		t.Errorf("two checks stopped by the guard ran %d expansions, want 2: a stopped run was cached", runs)
	}
	if got := check(time.Hour); got != long {
		t.Errorf("after the guard stopped a run, the check says:\n%s\nwant:\n%s", got, long)
	}

	// A run that succeeds is cached, and the cached answer is the one the
	// steps decide: a check under a guard of 1ns finds it and runs nothing.
	ok := genMacro("        Source.new() |> Source.add(\"fn ${decl.name}() -> Int {\\n    42\\n}\\n\") |> Source.expansion()", "")
	app = macroModules(t, ok, map[string]string{"main.nomi": genMain})
	main = filepath.Join(app, "main.nomi")
	if text := check(time.Hour); text != "" {
		t.Fatalf("the program does not check:\n%s", text)
	}
	before = ExpansionRunsForTest()
	if text := check(time.Nanosecond); text != "" {
		t.Errorf("a cached expansion fails under a guard of 1ns:\n%s", text)
	}
	if runs := ExpansionRunsForTest() - before; runs != 0 {
		t.Errorf("a check of a cached expansion ran %d expansions", runs)
	}
}

// An expansion writes Nomi source; its imports are its own, and a
// request's sites are the module's declarations of a keyword.
func TestMacro_SourceImportsAndSites(t *testing.T) {
	body := `        names = sites
            |> Iter.map(fn site -> "\"${site.name}\"")
            |> String.join(", ")
        visibility = if decl.public { "pub " } else { "" }
        Source.new()
        |> Source.add("import std/strings\n\n${visibility}fn ${decl.name}() -> String {\n    strings.String.to_upper([${names}] |> String.join(\"+\"))\n}\n")
        |> Source.expansion()`
	src := genMacro(body, "")
	app := macroModules(t, src, map[string]string{
		"main.nomi":  "import {\n    ext.gen\n    other\n    std/io\n}\n\ngen! answer\n\nfn main() {\n    io.print(answer())\n    io.print(other.question())\n}\n",
		"other.nomi": "import ext.gen\n\npub gen! question\n",
	})
	if got, want := runText(t, filepath.Join(app, "main.nomi")), "ANSWER+QUESTION\nANSWER+QUESTION\n"; got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An expansion that copies a type the declaration names (`gen! origin ->
// Point`) uses the declaring file's import of it: the import is not
// unused. An import neither the file nor the expansion names still is.
func TestMacro_ExpansionUsesTheDeclaringFilesImports(t *testing.T) {
	body := `        result = decl.result |> Maybe.map(fn r -> r.text) |> Maybe.with_default("Int")
        Source.new()
        |> Source.add("fn ${decl.name}() -> ${result} {\n    #${result}{x: 7}\n}\n")
        |> Source.expansion()`
	main := "import {\n    ext.gen\n    shapes.{Point, Size}\n    std/io\n}\n\ngen! origin -> Point\n\nfn main() {\n    io.print(origin().x)\n}\n"
	shapes := "pub struct Point {\n    x: Int\n}\n\npub struct Size {\n    w: Int\n}\n"
	app := macroModules(t, genMacro(body, ""), map[string]string{"main.nomi": main, "shapes.nomi": shapes})
	text := checkText(t, filepath.Join(app, "main.nomi"))
	if strings.Contains(text, "'Point' is unused") {
		t.Fatalf("the import the expansion uses is reported unused:\n%s", text)
	}
	if !strings.Contains(text, "imported name 'Size' is unused") {
		t.Fatalf("the import nothing uses is not reported:\n%s", text)
	}
	writeFile(t, filepath.Join(app, "main.nomi"), strings.Replace(main, "{Point, Size}", "Point", 1))
	if got := runText(t, filepath.Join(app, "main.nomi")); got != "7\n" {
		t.Errorf("got %q", got)
	}
}

// A `once` an expansion declares is lowered as one the file wrote.
func TestMacro_ExpansionDeclaresAOnce(t *testing.T) {
	body := `        Source.new()
        |> Source.add("once ${decl.name}: List<Int> = [1, 2, 3] |> Iter.map(fn n -> n * 2) |> Iter.to_list()\n")
        |> Source.expansion()`
	main := "import {\n    ext.gen\n    std/io\n}\n\ngen! doubled\n\nfn main() {\n    io.print(doubled)\n}\n"
	app := macroModules(t, genMacro(body, ""), map[string]string{"main.nomi": main})
	if text := checkText(t, filepath.Join(app, "main.nomi")); text != "" {
		t.Fatalf("the program does not check:\n%s", text)
	}
	if got := runText(t, filepath.Join(app, "main.nomi")); got != "[2, 4, 6]\n" {
		t.Errorf("got %q", got)
	}
}

// Declaring, importing and staging.
func TestMacro_DeclaringAndImporting(t *testing.T) {
	ok := genMacro("        Source.new() |> Source.add(\"fn ${decl.name}() -> Int {\\n    42\\n}\\n\") |> Source.expansion()", "")
	cases := []struct {
		name string
		ext  string
		main string
		want string
	}{
		{"imported", ok, genMain, ""},
		{"aliased", ok, strings.Replace(strings.Replace(genMain, "ext.gen", "ext.{gen as constant}", 1), "gen! answer", "constant! answer", 1), ""},
		{"not imported", ok, strings.Replace(genMain, "    ext.gen\n", "", 1), "unknown declaration keyword `gen!`"},
		{"private", strings.Replace(ok, "pub macro gen", "macro gen", 1), "import ext.gen\n\ngen! answer\n\nfn main() {\n}\n", "'gen' is private and cannot be imported"},
		{"no impl", strings.Replace(ok, "impl Macro for Gen", "impl Gen", 1), genMain, "`Gen` implements no `Macro` in this file, so the macro `gen` has nothing to run"},
		{"unknown type", strings.Replace(ok, "pub macro gen: Gen", "pub macro gen: Missing", 1), genMain, "Missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := macroModules(t, c.ext, map[string]string{"main.nomi": c.main})
			text := checkText(t, filepath.Join(app, "main.nomi"))
			if c.want == "" {
				if text != "" {
					t.Fatalf("the program does not check:\n%s", text)
				}
				if got := runText(t, filepath.Join(app, "main.nomi")); got != "42\n" {
					t.Errorf("got %q", got)
				}
				return
			}
			if !strings.Contains(text, c.want) {
				t.Errorf("got:\n%s\nwant it to hold %q", text, c.want)
			}
		})
	}
}

// A module cannot use a macro it declares: its code runs before the
// module is checked.
func TestMacro_Staging(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"ext\"\nentry_points = [\"main\"]\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module ext\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(dir, "gen.nomi"), genMacro("        Source.expansion(Source.new())", ""))
	writeFile(t, filepath.Join(dir, "main.nomi"), strings.Replace(genMain, "ext.gen", "gen.gen", 1))
	text := checkText(t, filepath.Join(dir, "main.nomi"))
	want := "`gen!` is a macro this module declares, and a module cannot use its own macros"
	if !strings.Contains(text, want) {
		t.Errorf("got:\n%s\nwant it to hold %q", text, want)
	}
}

// A request may name only files under the declaring module's root.
func TestMacro_RequestStaysInTheModule(t *testing.T) {
	src := strings.Replace(genMacro("        Source.expansion(Source.new())", ""), `#Request{files: [], sites: ["gen"]}`, `#Request{files: ["../ext/ext.nomi"], sites: []}`, 1)
	app := macroModules(t, src, map[string]string{"main.nomi": "import ext.gen\n\ngen! answer\n\nfn main() {\n}\n"})
	text := checkText(t, filepath.Join(app, "main.nomi"))
	if !strings.Contains(text, "asks to read \"../ext/ext.nomi\", which is not under the module root") {
		t.Errorf("got:\n%s", text)
	}
}

// An expansion is cached by everything it read: checking again runs
// nothing, and an edit to the table runs it again.
func TestMacro_Cache(t *testing.T) {
	MacroCacheForTest()
	dir := copyFixture(t)
	main := filepath.Join(dir, "rates", "main.nomi")
	before := ExpansionRunsForTest()
	if text := checkText(t, main); text != "" {
		t.Fatal(text)
	}
	first := ExpansionRunsForTest() - before
	if first != 2 {
		t.Fatalf("the first check ran %d expansions, want 2", first)
	}
	if text := checkText(t, main); text != "" {
		t.Fatal(text)
	}
	if again := ExpansionRunsForTest() - before - first; again != 0 {
		t.Errorf("a second check ran %d expansions, want 0", again)
	}
	writeFile(t, filepath.Join(dir, "rates", "data", "rates.csv"), "code,basis_points\nusd,9300\n")
	if got := runText(t, main); !strings.HasPrefix(got, "9300\n") {
		t.Errorf("after the edit the program prints:\n%s", got)
	}
	if edited := ExpansionRunsForTest() - before - first; edited != 1 {
		t.Errorf("an edit to one table ran %d expansions, want 1", edited)
	}
}

// The step limit is set from what a realistic expansion takes: the
// fixture's table over tables of 100 and 1,000 rows. The log reports
// the measurement.
func TestMacroExpansionCost(t *testing.T) {
	dir := copyFixture(t)
	tables := filepath.Join(dir, "rates", "tables.nomi")
	writeFile(t, tables, "import csvdata.table\n\npub table! rate(code: String) -> Maybe<Int> `rates.csv`\n")
	perRow := map[int]int64{}
	for _, rows := range []int{1000, 10000} {
		var b strings.Builder
		b.WriteString("code,basis_points\n")
		for i := range rows {
			fmt.Fprintf(&b, "c%d,%d\n", i, i)
		}
		writeFile(t, filepath.Join(dir, "rates", "data", "rates.csv"), b.String())
		MacroCacheForTest()
		start := time.Now()
		exps, err := Expansions(tables)
		if err != nil || len(exps) != 1 || len(exps[0].Errors) > 0 {
			t.Fatalf("%v %+v", err, exps)
		}
		steps := lastExpansionSteps.Load()
		perRow[rows] = steps / int64(rows)
		t.Logf("table over %d rows: %d steps, %d per row, %s with the build", rows, steps, perRow[rows], time.Since(start).Round(time.Millisecond))
	}
	if perRow[10000]*500_000 > macroSteps {
		t.Errorf("a 500,000-row table would take %d steps, more than the limit %d", perRow[10000]*500_000, macroSteps)
	}
}

// bandPosition matches a position in the synthesized band (line 2^30 and
// up), which no file has and a diagnostic must never show.
var bandPosition = regexp.MustCompile(`:\d{7,}:\d+`)

// A related location in an expansion's code moves to what the span map
// says it was made from, and one the map does not cover to the
// declaration, saying it is in the code the macro generated: never to a
// line of the band.
func TestMacro_RelatedLocationsInTheExpansionMove(t *testing.T) {
	cases := []struct {
		name, result, want string
	}{
		{"unmapped", `Source.add("Int")`, "the return type is declared here (in the code the macro `gen` generated for `gen! answer`; `nomi expand` prints it)"},
		{"mapped", `Source.mapped("Int", decl.name_span)`, "the return type is declared here"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `        Source.new()
        |> Source.add("fn ${decl.name}() -> ")
        |> ` + c.result + `
        |> Source.add(" {\n    \"text\"\n}\n")
        |> Source.expansion()`
			app := macroModules(t, genMacro(body, ""), map[string]string{"main.nomi": genMain})
			text := checkText(t, filepath.Join(app, "main.nomi"))
			if !strings.Contains(text, "return type mismatch: expected Int, got String") {
				t.Fatalf("the mismatch is not reported:\n%s", text)
			}
			if !strings.Contains(text, filepath.Join(app, "main.nomi")+":6:6: note: "+c.want) {
				t.Errorf("the return type is not placed at the declaration's name with %q:\n%s", c.want, text)
			}
			if c.name == "mapped" && strings.Contains(text, "in the code the macro") {
				t.Errorf("a mapped return type is said to be in generated code:\n%s", text)
			}
			if bandPosition.MatchString(text) {
				t.Errorf("a position in the band reaches the user:\n%s", text)
			}
		})
	}
}

// A macro that could not expand a declaration declared nothing, so
// what the rest of the program says follows from that: its errors are
// reported alone. One that expanded a declaration and reported an error
// about it (a function that keeps its signature) is not a failure, and
// the program's own errors are reported beside it.
func TestMacro_AFailedExpansionIsReportedAlone(t *testing.T) {
	body := `        case decl.name {
            "broken" -> Declaration.error(decl, "cannot expand broken")
            "stale" -> {
                e = Source.new() |> Source.add("pub fn stale() -> Int {\n    0\n}\n") |> Source.expansion()
                #{..e, diagnostics: Declaration.error(decl, "stale is out of date").diagnostics}
            }
            _ -> Source.new() |> Source.add("pub fn ${decl.name}() -> Int {\n    42\n}\n") |> Source.expansion()
        }`
	other := "import ext.gen\n\npub gen! broken\n\npub gen! stale\n"
	main := "import {\n    other\n    std/io\n}\n\nfn main() {\n    io.print(other.broken() + other.stale())\n    io.print(1 + \"x\")\n}\n"
	app := macroModules(t, genMacro(body, ""), map[string]string{"main.nomi": main, "other.nomi": other})
	text := checkText(t, filepath.Join(app, "main.nomi"))
	for _, want := range []string{"cannot expand broken", "other.nomi:3:5", "stale is out of date"} {
		if !strings.Contains(text, want) {
			t.Errorf("the check does not say %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"has no member 'broken'", "main.nomi"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("the check says %q beside the failed expansion:\n%s", unwanted, text)
		}
	}

	writeFile(t, filepath.Join(app, "other.nomi"), "import ext.gen\n\npub gen! fixed\n\npub gen! stale\n")
	writeFile(t, filepath.Join(app, "main.nomi"), strings.Replace(main, "other.broken()", "other.fixed()", 1))
	text = checkText(t, filepath.Join(app, "main.nomi"))
	for _, want := range []string{"stale is out of date", "main.nomi:8:"} {
		if !strings.Contains(text, want) {
			t.Errorf("with no failed expansion the check does not say %q:\n%s", want, text)
		}
	}
}

// A macro whose module does not check declares nothing, and the program
// reports the macro module's own errors, in its file, and the
// declarations' failures, alone: main.nomi's calls of what the
// declarations would have declared are not errors too.
func TestMacro_AModuleThatDoesNotCheckIsReportedAlone(t *testing.T) {
	ext := genMacro(`        bad: Int = "x"
        _ = bad
        Declaration.error(decl, "no")`, "")
	other := "import ext.gen\n\npub gen! first\n\npub gen! second\n"
	main := "import {\n    other\n    std/io\n}\n\nfn main() {\n    io.print(other.first() + other.second())\n}\n"
	app := macroModules(t, ext, map[string]string{"main.nomi": main, "other.nomi": other})
	text := checkText(t, filepath.Join(app, "main.nomi"))
	extFile := filepath.Join(filepath.Dir(app), "ext", "ext.nomi")
	for _, want := range []string{
		"the macro `gen` (module ext) could not expand `gen! first`: its module does not check: ",
		"could not expand `gen! second`",
		"other.nomi:3:5",
		"other.nomi:5:5",
		extFile + ":",
		"type mismatch",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the check does not say %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"has no member", "main.nomi"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("the check says %q beside the macro module's errors:\n%s", unwanted, text)
		}
	}
}

// A type a keyword declaration writes is the file's use of its import,
// even when the expansion does not name it.
func TestMacro_TypesADeclarationWritesAreUses(t *testing.T) {
	body := `        e = Source.new() |> Source.add("fn origin() -> Point {\n    #Point{x: 7}\n}\n") |> Source.expansion()
        #{..e, diagnostics: Declaration.error(decl, "the shape is not described yet").diagnostics}`
	main := "import {\n    ext.gen\n    shapes.{Point, Size}\n    std/io\n}\n\ngen! origin -> Point\n\nfn main() {\n    io.print(origin().x)\n}\n"
	shapes := "pub struct Point {\n    x: Int\n}\n\npub struct Size {\n    w: Int\n}\n"
	app := macroModules(t, genMacro(body, ""), map[string]string{"main.nomi": main, "shapes.nomi": shapes})
	text := checkText(t, filepath.Join(app, "main.nomi"))
	if !strings.Contains(text, "the shape is not described yet") {
		t.Fatalf("the macro's error is missing:\n%s", text)
	}
	if strings.Contains(text, "'Point' is unused") {
		t.Errorf("the import the declaration names is reported unused:\n%s", text)
	}
	if !strings.Contains(text, "imported name 'Size' is unused") {
		t.Errorf("the import nothing uses is not reported:\n%s", text)
	}
}

// A macro's keyword is marked with `!` and written qualified by the
// file API object of a whole-file import (`csvdata.table!`, under an
// alias too) or bare after an item import (`import csvdata.table`),
// as a function is. A qualified keyword the file does not declare, a
// qualifier no import binds, a bare keyword no import brings and a keyword
// with no `!` are errors that say so, and a body's tag reaches the
// macro, which may refuse it.
// A misspelled type in a declaration is one error, at the result type the
// declaration writes: the generated function returns an unknown type, not
// Unit, so neither its body nor the calls of it are mismatches too.
func TestMacro_AMisspelledTypeIsOneError(t *testing.T) {
	dir := copyFixture(t)
	tables := filepath.Join(dir, "rates", "tables.nomi")
	src, err := os.ReadFile(tables)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, tables, strings.Replace(string(src), "-> Maybe<Int> `rates.csv`", "-> Maybe<Itn> `rates.csv`", 1))
	text := checkText(t, filepath.Join(dir, "rates", "main.nomi"))
	var errs []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, ": help: ") {
			errs = append(errs, line)
		}
	}
	if len(errs) != 1 || !strings.HasSuffix(errs[0], "tables.nomi:4:42: unknown type \"Itn\"") {
		t.Errorf("want the unknown type alone, got:\n%s", text)
	}
}

func TestMacro_QualifiedKeywords(t *testing.T) {
	dir := copyFixture(t)
	tables := filepath.Join(dir, "rates", "tables.nomi")
	main := filepath.Join(dir, "rates", "main.nomi")
	const names = "\n/// What each currency is called.\npub csvdata.table! currency_name(code: String) -> Maybe<String> `names.csv`\n"

	for _, src := range []string{
		"import csvdata as ck\n\npub ck.table! rate(code: String) -> Maybe<Int> `rates.csv`\n\npub ck.table! currency_name(code: String) -> Maybe<String> `names.csv`\n",
		"import csvdata.table\n\npub table! rate(code: String) -> Maybe<Int> `rates.csv`\n\npub table! currency_name(code: String) -> Maybe<String> `names.csv`\n",
		"import csvdata.{table as csv}\n\npub csv! rate(code: String) -> Maybe<Int> `rates.csv`\n\npub csv! currency_name(code: String) -> Maybe<String> `names.csv`\n",
	} {
		writeFile(t, tables, src)
		if text := checkText(t, main); text != "" {
			t.Fatalf("the check of\n%s\nfails:\n%s", src, text)
		}
	}

	for _, c := range []struct{ src, want, hint string }{
		{"import csvdata\n\npub csvdata.tabel! rate(code: String) -> Maybe<Int> `rates.csv`\n" + names,
			"tables.nomi:3:5: file `csvdata` has no macro `tabel`", "its macro is `table!`"},
		{"import csvdata\n\npub kit.table! rate(code: String) -> Maybe<Int> `rates.csv`\n" + names,
			"tables.nomi:3:5: `kit` names no imported file, so `kit.table!` names no macro", "imported whole: `import kit`"},
		{"pub table! rate(code: String) -> Maybe<Int> `rates.csv`\n\npub table! currency_name(code: String) -> Maybe<String> `names.csv`\n",
			"tables.nomi:1:5: unknown declaration keyword `table!`", "help: a declaration that starts with a keyword and a name is written with a keyword Nomi knows, `message` or `noun`, or with a macro's, marked with `!` and qualified by the file that declares it, `mylib.table!` after `import mylib`"},
		{"import csvdata\n\npub csvdata.table rate(code: String) -> Maybe<Int> `rates.csv`\n" + names,
			"tables.nomi:3:5: `csvdata.table` is a macro, so it is written `csvdata.table!`", "the `!` tells a reader that a library expands the declaration"},
		{"import csvdata.table\n\npub table rate(code: String) -> Maybe<Int> `rates.csv`\n" + names,
			"tables.nomi:3:5: `table` is a macro, so it is written `table!`", ""},
		{"import csvdata\n\npub csvdata.table! rate(code: String) -> Maybe<Int> Csv`rates.csv`\n" + names,
			"tables.nomi:3:53: a table's body is the path of its CSV file and takes no tag: remove `Csv`", ""},
		{"import csvdata\n\npub csvdata.table! rate(code: String) -> Maybe<Int> Csv`\n    rates.csv\n    `\n" + names,
			"tables.nomi:3:53: a table's body is the path of its CSV file and takes no tag: remove `Csv`", ""},
		{"import csvdata\n\npub csvdata.table! rate(code: String) -> Int `rates.csv`\n" + names,
			"tables.nomi:3:42: a table answers `None` for a missing key, so write `-> Maybe<Int>`", ""},
		{"import csvdata\n\npub csvdata.table! rate(code: String) `rates.csv`\n" + names,
			"tables.nomi:3:13: a table names its result type: `-> Maybe<Int>`", ""},
	} {
		writeFile(t, tables, c.src)
		text := checkText(t, main)
		if !strings.Contains(text, c.want) || !strings.Contains(text, c.hint) {
			t.Errorf("the check of\n%s\nsays\n%s\nwant %q and %q", c.src, text, c.want, c.hint)
		}
	}
}

// A CSV cell holds any text: a tab, a carriage return, a backslash, a
// quote, a lone `$` and a `${` all reach the program as they are in the
// file, since the table writes each as a string literal
// (String.to_literal). The table's own quoting missed the lone `$`, which
// it wrote as the invalid escape `\$`.
func TestMacro_ACellHoldsAnyText(t *testing.T) {
	dir := copyFixture(t)
	cells := []string{"US\tdollar", "costs $5", "${x} \\ \"q\"", "a\rb"}
	var csv strings.Builder
	csv.WriteString("code,name\n")
	for i, c := range cells {
		fmt.Fprintf(&csv, "c%d,%s\n", i, c)
	}
	writeFile(t, filepath.Join(dir, "rates", "data", "names.csv"), csv.String())
	var main strings.Builder
	main.WriteString("import {\n    std/io\n    tables\n}\n\nfn main() {\n")
	for i := range cells {
		fmt.Fprintf(&main, "    io.print(Maybe.with_default(tables.currency_name(\"c%d\"), \"?\"))\n", i)
	}
	main.WriteString("}\n")
	writeFile(t, filepath.Join(dir, "rates", "main.nomi"), main.String())
	if got, want := runText(t, filepath.Join(dir, "rates", "main.nomi")), strings.Join(cells, "\n")+"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A macro that reads nothing writes `expand` alone, and one that offers
// navigation writes only the functions it answers: std/macro's defaults
// give the rest, and the drivers reach them. The macro here also names
// nothing of std/macro beyond what it uses, so the defaults' own names
// (`Request`) are not the macro file's.
func TestMacro_DefaultsFillWhatAMacroLeavesOut(t *testing.T) {
	src := `import std/macro.{ActionsCall, Action, ExpandCall, Expansion, Macro, Navigation, Source}

pub macro gen: Gen

type Gen

impl Macro for Gen {
    fn expand(call: ExpandCall) -> Expansion {
        Source.new()
        |> Source.add("fn ${call.decl.name}() -> Int {\n    42\n}\n")
        |> Source.expansion()
    }
}

impl Navigation for Gen {
    fn actions(call: ActionsCall) -> List<Action> {
        [#Action{title: "fix ${call.decl.name}"}]
    }
}
`
	app := macroModules(t, src, map[string]string{"main.nomi": genMain})
	if got := runText(t, filepath.Join(app, "main.nomi")); got != "42\n" {
		t.Fatalf("got %q, want 42", got)
	}
	nav := navigateFirst(t, filepath.Join(app, "main.nomi"))
	if a := nav("hover"); a == nil || a.Hover != "" {
		t.Errorf("hover's default answers %+v, want nothing", a)
	}
	if a := nav("complete"); a == nil || len(a.Completions) != 0 {
		t.Errorf("complete's default answers %+v, want nothing", a)
	}
	if a := nav("actions"); a == nil || len(a.Actions) != 1 || a.Actions[0].Title != "fix answer" || len(a.Actions[0].Edits) != 0 {
		t.Errorf("actions answers %+v, want the macro's one action", a)
	}
}

// navigateFirst expands the macro declarations of the file at path and
// answers a function asking the first one's macro op, as the language
// server does.
func navigateFirst(t *testing.T, path string) func(op string) *analysis.MacroAnswer {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parser.Parse(lexer.Lex(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	root := analysis.ProjectRoot(path, "")
	m, _ := analysis.LoadManifest(root)
	site := analysis.SiteFor(root, path, m, nil)
	site.Macros = Macros()
	for _, n := range analysis.ExpandDeclarations(nodes, site) {
		if kd, ok := n.(*ast.KeywordDecl); ok {
			if _, _, isMacro := analysis.MacroOf(kd); isMacro {
				return func(op string) *analysis.MacroAnswer {
					diag := &analysis.TypeError{Line: kd.Line, Col: kd.Col, Message: "m"}
					return analysis.MacroNavigate(kd, op, kd.Line, kd.Col, diag)
				}
			}
		}
	}
	t.Fatalf("%s has no macro declaration", path)
	return nil
}

// Navigation is given the configuration too, as request and expand are.
func TestMacro_NavigationIsGivenTheConfiguration(t *testing.T) {
	src := `import {
    std/json.Json
    std/macro.{ExpandCall, Expansion, Macro, Navigation, NavigationCall, Source}
}

pub macro gen: Gen

type Gen

impl Macro for Gen {
    fn expand(call: ExpandCall) -> Expansion {
        Source.new()
        |> Source.add("fn ${call.decl.name}() -> Int {\n    42\n}\n")
        |> Source.expansion()
    }
}

impl Navigation for Gen {
    fn hover(call: NavigationCall) -> Maybe<String> {
        Some(Json.encode(call.config))
    }
}
`
	app := macroModules(t, src, map[string]string{"main.nomi": genMain})
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[ext]\nmode = \"strict\"\n")
	if a := navigateFirst(t, filepath.Join(app, "main.nomi"))("hover"); a == nil || a.Hover != `{"mode":"strict"}` {
		t.Errorf("hover answers %+v, want the [ext] table", a)
	}
}

// Declaration.name_span is where the parser read the name, however many
// spaces follow the keyword: an error the checker reports in code mapped
// to it lands on the name.
func TestMacro_TheNameSpanIsWhereTheNameIs(t *testing.T) {
	body := `        Source.new()
        |> Source.add("fn ")
        |> Source.mapped(decl.name, decl.name_span)
        |> Source.add("() -> Int {\n    ")
        |> Source.mapped("\"text\"", decl.name_span)
        |> Source.add("\n}\n")
        |> Source.expansion()`
	main := strings.Replace(genMain, "gen! answer", "gen!    answer", 1)
	app := macroModules(t, genMacro(body, ""), map[string]string{"main.nomi": main})
	text := checkText(t, filepath.Join(app, "main.nomi"))
	if !strings.Contains(text, "return type mismatch: expected Int, got String") || !strings.Contains(text, filepath.Join(app, "main.nomi")+":6:9:") {
		t.Errorf("the mismatch is not at the name, column 9:\n%s", text)
	}
}

// Spans a macro makes from offsets land on the right column: choices'
// one_of places a repeated variant's error at the second word, past runs
// of spaces, and csvdata a value's error past a key of several bytes a
// character.
func TestMacro_OffsetSpansLandOnTheirColumn(t *testing.T) {
	dir := copyFixture(t)
	sizes := filepath.Join(dir, "cafe", "sizes.nomi")
	writeFile(t, sizes, "import choices\n\npub choices.one_of! Size `small  medium   small`\n")
	text := checkText(t, filepath.Join(dir, "cafe", "main.nomi"))
	// The body starts at column 27, and "small  medium   " is 16 bytes.
	if !strings.Contains(text, sizes+":3:43") {
		t.Errorf("the repeated variant is not reported at its word, column 43:\n%s", text)
	}

	csv := filepath.Join(dir, "rates", "data", "rates.csv")
	writeFile(t, csv, "code,basis_points\nusd,9200\né,1.5\n")
	text = checkText(t, filepath.Join(dir, "rates", "main.nomi"))
	// "é," is three bytes.
	if !strings.Contains(text, csv+":3:4: case branch type mismatch") {
		t.Errorf("the Float cell is not reported at its column, 4:\n%s", text)
	}
}

// A macro module named like one of nomi.toml's own tables would be given
// that table as its settings, so a declaration with its macro is refused,
// saying why.
func TestMacro_AModuleNamedLikeNomisOwnTableIsRefused(t *testing.T) {
	for _, name := range analysis.NomiTables {
		t.Run(name, func(t *testing.T) {
			main := strings.Replace(genMain, "ext.gen", name+".gen", 1)
			app := macroModulesNamed(t, name, genMacro("        Source.expansion(Source.new())", ""), map[string]string{"main.nomi": main})
			text := checkText(t, filepath.Join(app, "main.nomi"))
			if text == "" {
				t.Fatalf("a macro of module %s is accepted", name)
			}
			want := "the macro `gen` cannot be used: its module is named `" + name + "`, so its settings would be nomi.toml's `[" + name + "]` table, which is Nomi's own"
			if !strings.Contains(text, want) || !strings.Contains(text, "main.nomi:6:1") {
				t.Errorf("the refusal is not at the declaration:\n%s", text)
			}
			if strings.Contains(text, "answer") {
				t.Errorf("the refusal is not reported alone:\n%s", text)
			}
		})
	}
}

// Source.mapped's hint is a default parameter: a macro in another module
// leaves it out, and the default is lowered where std/macro wrote it.
func TestMacro_SourceMappedDefaultsItsHint(t *testing.T) {
	src := `import {
    std/io
    std/macro.{Source, Span}
}

fn main() {
    s = #Span{file: "a", line: 1, col: 1, end_line: 1, end_col: 2}
    e = Source.new() |> Source.mapped("x", s) |> Source.string_literal("y", s) |> Source.expansion()
    io.print(e.source)
    io.print(Iter.count(e.spans))
}
`
	var out bytes.Buffer
	p, err := LoadSource("main.nomi", src, WithOutput(&out))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "x\"y\"\n2\n" {
		t.Errorf("got %q", got)
	}
}
