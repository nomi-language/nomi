package highlight_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// TestProgramOfModulesAnswersEachTab holds a block that names modules (a
// project whose go.mod requires the csvdata module under
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
		"\n// FILE: nomi.toml\n" + read("rates/nomi.toml") +
		"\n// FILE: go.mod\n" + strings.Replace(read("rates/go.mod"), "../csvdata", "./csvdata", 1) +
		"\n// FILE: csvdata/csvdata.nomi\n" + read("csvdata/csvdata.nomi") +
		"\n// FILE: csvdata/nomi.toml\n[module]\nname = \"csvdata\"\n" +
		"\n// FILE: csvdata/go.mod\n" + read("csvdata/go.mod")
	prog := highlight.AnalyzeProgram(src, t.TempDir(), std.Shared())

	got := prog.HoverIn("main.nomi", 7, colOf(t, prog, "main.nomi", 7, "rates"), nil)
	if !strings.Contains(got, "fn rates(code: String) -> Maybe<Int>") {
		t.Errorf("hover on tables.rates in main.nomi = %q; want the function the generator wrote", got)
	}
	for _, name := range []string{"main.nomi", "tables.nomi", "csvdata/csvdata.nomi"} {
		if toks := prog.SemanticTokensIn(name); len(toks) == 0 {
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
[module]
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
	if toks := prog.SemanticTokensIn("main.nomi"); len(toks) == 0 {
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

// reachSrc is a block whose one outside call nomi.toml's
// [capabilities.self] allows.
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
[module]
name = "notes"
entry_points = ["main"]

[capabilities.self]
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
	const fsLine = "\n\nReaches `std::fs`, which `nomi.toml` allows in `[capabilities.self]`."
	if got := prog.HoverIn("main.nomi", 7, colOf(t, prog, "main.nomi", 7, "read_text"), reached); !strings.HasSuffix(got, fsLine) || !strings.Contains(got, "fn read_text") {
		t.Errorf("hover on fs.read_text = %q; want its signature, then %q", got, fsLine)
	}
	if got := prog.HoverIn("main.nomi", 8, colOf(t, prog, "main.nomi", 8, "notes"), reached); strings.Contains(got, "Reaches") {
		t.Errorf("hover on the binding notes = %q; want no line about what it reaches", got)
	}

	snippet := "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n"
	reached, err = vmhost.ReachedMarked(snippet, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fa, nodes := highlight.AnalyzeNodes(snippet, std.Shared())
	inEntry := func(path string) bool { return virtualproject.NormalizeModuleName(path) == "main" }
	if got := hoverdoc.AtReaching(fa, nodes, snippet, analysis.Pos{Line: 4, Col: 9}, reached, inEntry); !strings.HasSuffix(got, "\n\nReaches `std::io`.") {
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
[module]
name = "notes"
entry_points = ["main"]

[capabilities.self]
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
	const line = "\n\nReaches `std::fs` through `read`, which `nomi.toml` allows in `[capabilities.self]`."
	if got := prog.HoverIn("main.nomi", 7, colOf(t, prog, "main.nomi", 7, "load"), reached); !strings.HasSuffix(got, line) {
		t.Errorf("hover on store.load = %q; want it to end with %q", got, line)
	}
	if got := prog.HoverIn("store.nomi", 3, colOf(t, prog, "store.nomi", 3, "load"), reached); !strings.HasSuffix(got, line) {
		t.Errorf("hover on fn load = %q; want it to end with %q", got, line)
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
