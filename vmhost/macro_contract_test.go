package vmhost

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// echoMacro is a macro `gen` that expands a declaration to its body,
// with each `;` a line break: the declaration writes what the macro
// answers.
var echoMacro = genMacro(`        case decl.body {
            Some(body) -> Source.new() |> Source.add(String.replace(body.text, ";", "\n") + "\n") |> Source.expansion()
            None -> Declaration.error(decl, "no body")
        }`, "")

// An expansion declares what its declaration states: the name, a type
// for a type name, `pub` exactly when the declaration is, and a function
// whose parameters end with the declaration's and whose result is the
// declared type. Each mismatch is a defect in the macro, reported at the
// declaration and alone: main.nomi's own error is not reported beside it.
func TestMacro_AnExpansionDeclaresWhatItsDeclarationStates(t *testing.T) {
	const label = "the macro `gen` (module ext) expanded "
	cases := []struct {
		name, decl, body, want string
	}{
		{"wrong result", "pub gen! say -> Int", `pub fn say() -> String {;    "x";}`,
			label + "`gen! say -> Int` to `pub fn say() -> String`: its result should be `Int`, a defect in the macro"},
		{"private when declared pub", "pub gen! say -> Int", `fn say() -> Int {;    1;}`,
			label + "`gen! say -> Int` to `fn say() -> Int`: it should be `pub`, as the declaration is, a defect in the macro"},
		{"pub when declared private", "gen! say -> Int", `pub fn say() -> Int {;    1;};;pub fn use_say() -> Int {;    say();}`,
			label + "`gen! say -> Int` to `pub fn say() -> Int`: it should not be `pub`, since the declaration is not, a defect in the macro"},
		{"missing name", "pub gen! say -> Int", `pub fn speak() -> Int {;    1;}`,
			label + "`gen! say -> Int` to code that declares no function `say`, a defect in the macro"},
		{"extra parameter", "pub gen! say(name: String) -> Int", `pub fn say(name: String, loud: Bool) -> Int {;    _ = name;    _ = loud;    1;}`,
			label + "`gen! say(name: String) -> Int` to `pub fn say(name: String, loud: Bool) -> Int`: its parameters should end with the declaration's, `name: String`, a defect in the macro"},
		{"renamed parameter", "pub gen! say(name: String) -> Int", `pub fn say(title: String) -> Int {;    _ = title;    1;}`,
			label + "`gen! say(name: String) -> Int` to `pub fn say(title: String) -> Int`: its parameters should end with the declaration's, `name: String`"},
		{"wrong parameter type", "pub gen! say(name: String) -> Int", `pub fn say(name: Int) -> Int {;    name;}`,
			label + "`gen! say(name: String) -> Int` to `pub fn say(name: Int) -> Int`: its parameter `name` should be `String`, a defect in the macro"},
		{"a differently named type", "pub gen! Size", `pub enum Sizes {;    Small;}`,
			label + "`gen! Size` to code that declares no type `Size`, a defect in the macro"},
		{"a function for a type name", "pub gen! Size", `pub fn size() -> Int {;    1;}`,
			label + "`gen! Size` to code that declares no type `Size`, a defect in the macro"},
		{"a private type", "pub gen! Size", `enum Size {;    Small;}`,
			label + "`gen! Size` to `enum Size`: it should be `pub`, as the declaration is"},
	}
	main := "import other\n\nfn main() {\n    _ = 1 + \"x\"\n}\n"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			other := "import ext.gen\n\n" + c.decl + " `" + c.body + "`\n"
			app := macroModules(t, echoMacro, map[string]string{"main.nomi": main, "other.nomi": other})
			text := checkText(t, filepath.Join(app, "main.nomi"))
			if text == "" {
				t.Fatalf("the program checks; want %q", c.want)
			}
			if !strings.Contains(text, c.want) {
				t.Errorf("the check does not say %q:\n%s", c.want, text)
			}
			col := strings.Index(c.decl, "gen!") + 1
			if at := filepath.Join(app, "other.nomi") + ":3:" + strconv.Itoa(col) + ": "; !strings.Contains(text, at) {
				t.Errorf("the error is not at the declaration's keyword, %s:\n%s", at, text)
			}
			if strings.Contains(text, "main.nomi") {
				t.Errorf("the mismatch is not reported alone:\n%s", text)
			}
		})
	}
}

// What the contract allows: parameters the macro puts before the
// declaration's (sqlkit's `conn`), a result that is a `Result` of the
// declared one, a type the expansion names through its own import that is
// the one the declaration names, and a declaration with no parameters or
// result (`gen! helpers`) that declares nothing of its name.
func TestMacro_TheContractAllowsLeadingParametersAndAResult(t *testing.T) {
	other := "import {\n    ext.gen\n    std/json.Json\n}\n\n" +
		"pub gen! say(name: String) -> Int `pub fn say(times: Int, name: String) -> Result<Int, String> {;    Ok(times * String.length(name));}`\n\n" +
		"pub gen! count(xs: List<Int>) -> Json `import std/json;;pub fn count(xs: List<Int>) -> json.Json {;    json.Json.Int(Iter.count(xs));}`\n\n" +
		"pub gen! helpers `pub fn twice(n: Int) -> Int {;    n * 2;}`\n"
	main := "import {\n    other\n    std/io\n}\n\nfn main() {\n    io.print(other.say(2, \"abc\"))\n    io.print(other.count([4, 5]))\n    io.print(other.twice(4))\n}\n"
	app := macroModules(t, echoMacro, map[string]string{"main.nomi": main, "other.nomi": other})
	if text := checkText(t, filepath.Join(app, "main.nomi")); text != "" {
		t.Fatalf("the program does not check:\n%s", text)
	}
	if got := runText(t, filepath.Join(app, "main.nomi")); got != "Ok(6)\n2\n8\n" {
		t.Errorf("got %q", got)
	}
}

// A macro whose declarations fail leaves the program reporting their
// errors alone, even when what a failed declaration declares names a type
// another failed declaration would have declared. sqlkit's `query!` with
// no snapshot answers a stub `fn find(s: Status) -> Int` beside its error,
// and `Status` is a type of the `database!` that declared nothing; the
// contract check reads the stub's function type, whose parameter type did
// not resolve.
func TestMacro_AStubNamingAFailedDeclarationsTypeIsReportedAlone(t *testing.T) {
	ext := genMacro(`        case decl.body {
            Some(body) -> {
                e = Source.new() |> Source.add(String.replace(body.text, ";", "\n") + "\n") |> Source.expansion()
                #{..e, diagnostics: Declaration.error(decl, "no snapshot for ${decl.name}").diagnostics}
            }
            None -> Declaration.error(decl, "no snapshot for the tables")
        }`, "")
	app := macroModules(t, ext, stubFailureFiles)
	for _, file := range []string{"main.nomi", "queries.nomi", "schema.nomi"} {
		t.Run("check "+file, func(t *testing.T) {
			text := checkText(t, filepath.Join(app, file))
			if strings.Contains(text, "internal compiler error") {
				t.Fatalf("the check crashes:\n%s", text)
			}
			want := []string{"no snapshot for the tables", "schema.nomi:3:5"}
			if file != "schema.nomi" {
				want = append(want, "no snapshot for find", "queries.nomi:6:5")
			}
			for _, w := range want {
				if !strings.Contains(text, w) {
					t.Errorf("the check does not say %q:\n%s", w, text)
				}
			}
			for _, u := range []string{"main.nomi", "Status", "defect in the macro"} {
				if strings.Contains(text, u) {
					t.Errorf("the check says %q beside the macros' errors:\n%s", u, text)
				}
			}
		})
	}
	t.Run("run", func(t *testing.T) {
		_, err := Load(filepath.Join(app, "main.nomi"))
		if err == nil {
			t.Fatal("the program loads; want the macros' errors")
		}
		if text := err.Error(); strings.Contains(text, "internal compiler error") || !strings.Contains(text, "no snapshot for find") {
			t.Errorf("the load does not report the macros' errors:\n%s", text)
		}
	})
}

// stubFailureFiles are a module whose schema.nomi declaration fails and
// declares nothing, and whose queries.nomi declaration fails with a stub
// that names a type the first would have declared.
var stubFailureFiles = map[string]string{
	"schema.nomi":  "import ext.gen\n\npub gen! tables\n",
	"queries.nomi": "import {\n    ext.gen\n    schema.Status\n}\n\npub gen! find(s: Status) -> Int `pub fn find(s: Status) -> Int {;    _ = s;    0;}`\n",
	"main.nomi":    "import {\n    queries\n    std/io\n}\n\nfn main() {\n    io.print(queries.find)\n}\n",
}
