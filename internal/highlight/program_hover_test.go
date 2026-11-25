package highlight_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/semtokens"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// TestProgramOfModulesAnswersEachTab holds a block that names modules (a
// project whose nomi.toml names the csvdata module under
// csvdata/, as testdata/tools lays it out)
// to an analysis of its files as written under a directory: a call in
// main.nomi into the table the generator wrote hovers with its function,
// and the other module's own file, analyzed as its module's, has tokens.
func TestProgramOfModulesAnswersEachTab(t *testing.T) {
	read := func(path string) string {
		data, err := os.ReadFile("../../testdata/tools/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	src := "// FILE: main.nomi\n" + read("rates/main.nomi") +
		"\n// FILE: tables.nomi\n" + read("rates/tables.nomi") +
		"\n// FILE: data/rates.csv\n" + read("rates/data/rates.csv") +
		"\n// FILE: nomi.toml\n" + strings.Replace(read("rates/nomi.toml"), "../csvdata", "./csvdata", 1) +
		"\n// FILE: csvdata/csvdata.nomi\n" + read("csvdata/csvdata.nomi") +
		"\n// FILE: csvdata/nomi.toml\n[package]\nname = \"csvdata\"\n"
	prog := highlight.AnalyzeProgram(src, t.TempDir(), std.Shared())

	got := prog.HoverIn("main.nomi", 7, colOf(t, prog, "main.nomi", 7, "rates"), nil)
	if !strings.Contains(got, "fn rates(code: String) -> Option<Int>") {
		t.Errorf("hover on tables.rates in main.nomi = %q; want the function the generator wrote", got)
	}
	for _, name := range []string{"main.nomi", "tables.nomi", "csvdata/csvdata.nomi"} {
		if toks := prog.SemanticTokensIn(name, nil); len(toks) == 0 {
			t.Errorf("%s has no semantic tokens", name)
		}
	}
}

// programHoverSrc is the tour's first translating-text example: a call in
// main.nomi into a phrase declared in cart.nomi, whose Polish text is a
// catalog and whose locales the manifest lists.
const programHoverSrc = `// FILE: main.nomi
import
    std::io
    cart
end

fn main()
    [1, 2]
    |> Iter.each(fn n ->
        badge = cart.cart_count(n)
        io.print(Phrase.format(badge, Locale"en"))
    end)
end

// FILE: cart.nomi
/// The cart badge, such as "3 items in your cart".
pub phrase cart_count(count: Int) ` + "`" + `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    ` + "`" + `

pub fn total(n: Int) -> Int
    n * 2
end

// FILE: locales/pl/cart.toml
[cart_count]
source = '''
.input {$count :integer}
.match $count
one {{{$count} item in your cart}}
* {{{$count} items in your cart}}
'''
text = '''
.input {$count :integer}
.match $count
one {{{$count} produkt w koszyku}}
* {{{$count} produktu w koszyku}}
'''

// FILE: nomi.toml
[package]
name = "shop"
entry_points = ["main"]

[i18n]
locales = ["en", "pl"]
`

// colOf is the 1-based column of the first occurrence of sub on line (1-based)
// of the file name's text in src.
func colOf(t *testing.T, prog *highlight.Program, name string, line int, sub string) int {
	t.Helper()
	f, ok := prog.File(name)
	if !ok {
		t.Fatalf("no analysis of %s", name)
	}
	lines := strings.Split(f.Source, "\n")
	i := strings.Index(lines[line-1], sub)
	if i < 0 {
		t.Fatalf("%s line %d (%q) has no %q", name, line, lines[line-1], sub)
	}
	return i + 1
}

// TestProgramHoverResolvesAcrossFiles holds the tour's multi-file hover to
// the whole program: a call in main.nomi into cart.nomi shows the
// phrase's signature and doc from cart.nomi, and a declaration inside
// cart.nomi answers too.
func TestProgramHoverResolvesAcrossFiles(t *testing.T) {
	prog := highlight.AnalyzeProgram(programHoverSrc, "", std.Shared())

	got := prog.HoverIn("main.nomi", 9, colOf(t, prog, "main.nomi", 9, "cart_count"), nil)
	if !strings.Contains(got, "cart_count(count: Int)") || !strings.Contains(got, "The cart badge") {
		t.Errorf("hover on cart.cart_count in main.nomi = %q; want the phrase's signature and doc", got)
	}

	got = prog.HoverIn("cart.nomi", 9, colOf(t, prog, "cart.nomi", 9, "total"), nil)
	if !strings.Contains(got, "fn total(n: Int) -> Int") {
		t.Errorf("hover on fn total in cart.nomi = %q; want its signature", got)
	}

	if got := prog.HoverIn("nomi.toml", 1, 2, nil); got != "" {
		t.Errorf("hover in nomi.toml = %q; want none", got)
	}
	if toks := prog.SemanticTokensIn("main.nomi", nil); len(toks) == 0 {
		t.Error("main.nomi has no semantic tokens")
	}
}

// TestProgramHoverSurvivesAnError holds hover to the program's analyses when
// the program has a type error: the cross-file call still resolves.
func TestProgramHoverSurvivesAnError(t *testing.T) {
	src := strings.Replace(programHoverSrc, "fn main()\n", "fn main()\n    x: Int = \"no\"\n", 1)
	prog := highlight.AnalyzeProgram(src, "", std.Shared())
	got := prog.HoverIn("main.nomi", 10, colOf(t, prog, "main.nomi", 10, "cart_count"), nil)
	if !strings.Contains(got, "cart_count(count: Int)") {
		t.Errorf("hover on cart.cart_count with a type error elsewhere = %q; want the phrase's signature", got)
	}
}

// reachSrc is a block whose one effect call nomi.toml's
// [effects.self] allows.
const reachSrc = `// FILE: main.nomi
import
    std::fs
    std::io
end

fn main()
    notes = fs.read_text("notes.txt") |> Result.with_default("") |> String.trim()
    io.print(notes)
end

// FILE: notes.txt
Buy milk.

// FILE: nomi.toml
[package]
name = "notes"
entry_points = ["main"]

[effects.self]
std = ["std::io", "std::fs"]
`

// The tour's editors end a hover on a call with what it reaches, as the
// language server does (hoverdoc.AppendReach): from the calls a lowering
// of the block's program finds (vmhost.ReachedMarked), in a tab of a
// multi-file block (nomiHoverIn) and in a single-file block (nomiHover).
// A hover on anything else has no such line.
func TestProgramHoverEndsWithWhatACallReaches(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	reached, err := vmhost.ReachedMarked(reachSrc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prog := highlight.AnalyzeProgram(reachSrc, t.TempDir(), std.Shared())
	const fsLine = "\n\nHas effects: `std::fs`, which `nomi.toml` allows in `[effects.self]`."
	if got := prog.HoverIn("main.nomi", 7, colOf(t, prog, "main.nomi", 7, "read_text"), reached); !strings.HasSuffix(got, fsLine) || !strings.Contains(got, "fn read_text") {
		t.Errorf("hover on fs.read_text = %q; want its signature, then %q", got, fsLine)
	}
	if got := prog.HoverIn("main.nomi", 8, colOf(t, prog, "main.nomi", 8, "notes"), reached); strings.Contains(got, "Has effects") {
		t.Errorf("hover on the binding notes = %q; want no line about what it reaches", got)
	}

	snippet := "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n"
	reached, err = vmhost.ReachedMarked(snippet, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fa, nodes := highlight.AnalyzeNodes(snippet, std.Shared())
	inEntry := func(path string) bool { return virtualproject.NormalizeModuleName(path) == "main" }
	if got := hoverdoc.AtReaching(fa, nodes, snippet, analysis.Pos{Line: 4, Col: 9}, reached, inEntry); !strings.HasSuffix(got, "\n\nHas effects: `std::io`.") {
		t.Errorf("hover on io.print in a single-file block = %q; want it to end with what it reaches", got)
	}
}

// functionReachSrc is a block whose entry calls a function of another tab
// that reaches std::fs through a function of its own.
const functionReachSrc = `// FILE: main.nomi
import
    std::io
    store
end

fn main()
    io.print(store.load("notes.txt"))
end

// FILE: store.nomi
import std::fs

pub fn load(path: String) -> String
    read(path) |> String.trim()
end

fn read(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

// FILE: notes.txt
Buy milk.

// FILE: nomi.toml
[package]
name = "notes"
entry_points = ["main"]

[effects.self]
std = ["std::io", "std::fs"]
`

// The tour's editors end a hover on a function of the program, and on a
// call of one, with what it can reach, as the language server does.
func TestProgramHoverEndsWithWhatAFunctionReaches(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	reached, err := vmhost.ReachedMarked(functionReachSrc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prog := highlight.AnalyzeProgram(functionReachSrc, t.TempDir(), std.Shared())
	const line = "\n\nHas effects: `std::fs` through `read`, which `nomi.toml` allows in `[effects.self]`."
	if got := prog.HoverIn("main.nomi", 7, colOf(t, prog, "main.nomi", 7, "load"), reached); !strings.HasSuffix(got, line) {
		t.Errorf("hover on store.load = %q; want it to end with %q", got, line)
	}
	if got := prog.HoverIn("store.nomi", 3, colOf(t, prog, "store.nomi", 3, "load"), reached); !strings.HasSuffix(got, line) {
		t.Errorf("hover on fn load = %q; want it to end with %q", got, line)
	}
}

// appFieldSrc is a block whose entry calls a function of another tab that
// reads an app field through a function of its own, and reaches std::fs.
const appFieldSrc = `// FILE: main.nomi
import
    std::io
    app.App
    store
end

pub fn boot() -> Result<App, String>
    Ok(App{dir: "notes", limit: 3})
end

fn main()
    io.print(store.load("notes.txt"))
end

// FILE: app.nomi
pub struct App
    dir: String
    limit: Int
end

// FILE: store.nomi
import
    std::fs
    app.App
end

pub fn load(path: String) -> String
    read(path) |> String.trim()
end

fn read(path: String) -> String
    fs.read_text("${App.dir}/${path}") |> Result.with_default("")
end

// FILE: notes/notes.txt
Buy milk.
`

// The tour's editors end a hover on a function that reads an app field,
// and on a call of one, with the fields it uses, after what it reaches, as
// the language server does, and mark no token for it: the call that
// reaches std::fs is marked for that, and nothing else is.
func TestProgramHoverSaysWhichAppFieldsAFunctionUses(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	reached, err := vmhost.ReachedMarked(appFieldSrc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prog := highlight.AnalyzeProgram(appFieldSrc, t.TempDir(), std.Shared())
	const lines = "\n\nHas effects: `std::fs` through `read`.\n\nUses `App.dir`."
	if got := prog.HoverIn("main.nomi", 12, colOf(t, prog, "main.nomi", 12, "load"), reached); !strings.HasSuffix(got, lines) {
		t.Errorf("hover on store.load = %q; want it to end with %q", got, lines)
	}
	if got := prog.HoverIn("store.nomi", 6, colOf(t, prog, "store.nomi", 6, "load"), reached); !strings.HasSuffix(got, lines) {
		t.Errorf("hover on fn load = %q; want it to end with %q", got, lines)
	}
	if got := prog.HoverIn("main.nomi", 7, colOf(t, prog, "main.nomi", 7, "boot"), reached); strings.Contains(got, "Uses") {
		t.Errorf("hover on fn boot = %q; want no line of app fields", got)
	}
	f, _ := prog.File("store.nomi")
	got := effectTokens(f.Source, prog.SemanticTokensIn("store.nomi", reached))
	if want := map[[2]int]string{{6, 8}: "load", {7, 5}: "read", {10, 4}: "read", {11, 8}: "read_text"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("store.nomi marks %v as having effects; want %v", got, want)
	}
}

// What a call reaches is worked out once per source and costs what loading
// the block costs; this logs it for a block the size of the tour's.
func TestProgramHoverReachCost(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	if _, err := vmhost.ReachedMarked(reachSrc, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const runs = 10
	start := time.Now()
	for range runs {
		if _, err := vmhost.ReachedMarked(reachSrc, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("vmhost.ReachedMarked of a tour block: %v a run", time.Since(start)/runs)
}

// effectTokens is each token of toks, by its 1-based line and column,
// that is marked as having effects, with the text it covers.
func effectTokens(src string, toks []semtokens.Token) map[[2]int]string {
	lines := strings.Split(src, "\n")
	out := map[[2]int]string{}
	for _, tok := range toks {
		if tok.Effect {
			out[[2]int{tok.Line, tok.Col}] = lines[tok.Line-1][tok.Col-1 : tok.Col-1+tok.Length]
		}
	}
	return out
}

// The tour's editors mark the tokens that reach outside the machine where
// a hover says so, as the language server does (hoverdoc.MarkReaching):
// in each tab of a multi-file block, the callee of an effect call, of a
// call of a function that reaches one and such a function's name (`main`
// among them), and in a single-file block the same. A qualifier, a pure
// call and a function that reaches nothing are not marked, and with
// nothing reached nothing is.
func TestProgramTokensMarkWhatHasEffects(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	reached, err := vmhost.ReachedMarked(functionReachSrc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prog := highlight.AnalyzeProgram(functionReachSrc, t.TempDir(), std.Shared())
	for name, want := range map[string]map[[2]int]string{
		"main.nomi":  {{6, 4}: "main", {7, 8}: "print", {7, 20}: "load"},
		"store.nomi": {{3, 8}: "load", {4, 5}: "read", {7, 4}: "read", {8, 8}: "read_text"},
	} {
		f, _ := prog.File(name)
		got := effectTokens(f.Source, prog.SemanticTokensIn(name, reached))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s marks %v as having effects; want %v", name, got, want)
		}
		if got := effectTokens(f.Source, prog.SemanticTokensIn(name, nil)); len(got) > 0 {
			t.Errorf("%s marks %v with nothing reached", name, got)
		}
	}

	snippet := "import std::io\n\nfn twice(n: Int) -> Int\n    n * 2\nend\n\nfn main()\n    io.print(\"${twice(2)}\")\nend\n"
	reached, err = vmhost.ReachedMarked(snippet, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := effectTokens(snippet, highlight.SemanticTokens(snippet, std.Shared(), reached, highlight.InFile("main.nomi")))
	if want := map[[2]int]string{{7, 4}: "main", {8, 8}: "print"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("a single-file block marks %v as having effects; want %v", got, want)
	}
}

// dbgReachSrc is a block under effects lists that holds a `dbg` in main,
// in a function nothing calls, in a generic function nothing instantiates
// and in a test.
const dbgReachSrc = `// FILE: main.nomi
import std::io

fn draft(n: Int) -> Int
    dbg n
end

fn first<T>(xs: List<T>) -> Option<T>
    xs |> dbg |> List.first()
end

fn main()
    io.print("${dbg 1 + 1}")
end

test "two"
    assert dbg 2 == 2
end

// FILE: nomi.toml
[package]
name = "notes"
entry_points = ["main"]

[effects.self]
std = ["std::io"]
`

// The tour's editors leave the keyword of each `dbg`, the call that
// prints, plain, as the language server does, and mark the function that
// holds it; its hover keeps the keyword's text and ends with what it
// reaches, which no list names and every list allows.
func TestProgramTokensAndHoverOfADbg(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	reached, err := vmhost.ReachedMarked(dbgReachSrc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prog := highlight.AnalyzeProgram(dbgReachSrc, t.TempDir(), std.Shared())
	f, _ := prog.File("main.nomi")
	got := effectTokens(f.Source, prog.SemanticTokensIn("main.nomi", reached))
	want := map[[2]int]string{{3, 4}: "draft", {7, 4}: "first", {11, 4}: "main", {12, 8}: "print"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("main.nomi marks %v as having effects; want %v", got, want)
	}
	for _, line := range []int{4, 8, 12, 16} {
		hover := prog.HoverIn("main.nomi", line, colOf(t, prog, "main.nomi", line, "dbg")+1, reached)
		if !strings.Contains(hover, "Debug rendering") || !strings.HasSuffix(hover, "\n\nHas effects: `dbg`.") {
			t.Errorf("hover on the dbg on line %d = %q; want the keyword's text, then what it reaches", line, hover)
		}
	}
}
