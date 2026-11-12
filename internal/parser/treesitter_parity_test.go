package parser_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// The tree-sitter grammar in tree-sitter-nomi/ is a second parser for the
// language: Zed, Helix, Neovim and the tour highlight with it. Nothing ties it
// to this package's parser, so it can fall behind without anyone noticing.
// TestTreeSitterParsesWhatTheParserAccepts parses every input this parser
// accepts with tree-sitter and fails on each one whose tree holds an ERROR or
// MISSING node, naming the input and the first error's position. It also
// fails on each one where a statement list (the top level, a block, a `//!`
// prompt, a case's or a binding else's arms) holds a different number of
// statements for tree-sitter than for this parser, since a grammar that reads
// a statement-ending line break as whitespace joins two statements into one
// without an error (compareStatementCounts).
//
// The inputs are every tracked `.nomi` file, every ```nomi and ```nomi-run
// block of the tour, every ```nomi block of docs/spec.md, lineBreakInputs and
// patternInputs, each kept only when parser.Parse accepts it as a whole file. A fragment the
// parser rejects is not a parity question, except rejectedInputs: malformed
// sources the parser rejects, each of which tree-sitter must mark with an ERROR
// or MISSING node.
//
// Go has no tree-sitter runtime in this module, so the test runs the
// tree-sitter CLI through scripts/tree-sitter.sh, which pins the CLI version
// that generated src/parser.c. It skips when `npx` is not on PATH or the
// pinned CLI is not already in npx's cache (npm runs offline here, so the
// test never touches the network), and under -short. The CLI compiles the
// grammar into TREE_SITTER_LIBDIR, by default a directory per checkout under
// the system temp directory, so concurrent checkouts never share a library.

// treeSitterParityGaps pins the inputs the parser accepts and tree-sitter
// still cannot parse, or reads with a different statement count, each with
// its reason. A tracked file is keyed by its
// repository path; a Markdown block by "path: substring", where the substring
// occurs in exactly that block. The test fails when a pinned input starts
// parsing, so remove its entry with the fix.
var treeSitterParityGaps = map[string]string{
	"tests/14-modules-and-packaging/dotted_type_names/dotted_type_names_test.nomi": "a struct pattern whose owner has a dotted name (`Probe.Reading.Blip{at}`) reads as nested type_field_access, which the grammar resolves before it sees the `{`; letting a type_field_access name a struct literal adds conflicts that break other files",
	"internal/irbuild/testdata/dotted_values.nomi":                                 "a struct pattern whose owner has a dotted name (`Probe.Reading.Blip{at}`) reads as nested type_field_access, which the grammar resolves before it sees the `{`; letting a type_field_access name a struct literal adds conflicts that break other files",
}

const repoRoot = "../.."

// parityInput is one source text the parser accepts.
type parityInput struct {
	name string // repository path, or "path: line N" for a Markdown block
	code string
}

func TestTreeSitterParsesWhatTheParserAccepts(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the tree-sitter CLI")
	}
	runCLI := treeSitterCLI(t)

	inputs := parityInputs(t)
	dir := t.TempDir()
	var paths []string
	byPath := map[string]parityInput{}
	for i, in := range inputs {
		p := filepath.Join(dir, fmt.Sprintf("%04d.nomi", i))
		if err := os.WriteFile(p, []byte(in.code), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		byPath[p] = in
	}
	// A sentinel the CLI must report. Without it, a CLI that parsed nothing
	// (a failed grammar build, say) could look like a clean run.
	sentinel := filepath.Join(dir, "sentinel.nomi")
	if err := os.WriteFile(sentinel, []byte("fn (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, sentinel)
	rejectedByPath := map[string]parityInput{}
	for i, in := range rejectedInputs {
		if parses(in.code) {
			t.Errorf("rejectedInputs %q: the parser accepts it", in.name)
			continue
		}
		p := filepath.Join(dir, fmt.Sprintf("rejected-%02d.nomi", i))
		if err := os.WriteFile(p, []byte(in.code), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		rejectedByPath[p] = in
	}
	list := filepath.Join(dir, "paths.txt")
	if err := os.WriteFile(list, []byte(strings.Join(paths, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _ := runCLI("parse", "--paths", list)
	trees, errLines := splitParseOutput(out)
	if len(trees) != len(paths) {
		t.Fatalf("the tree-sitter CLI printed %d trees for %d inputs, so its output cannot be trusted:\n%s", len(trees), len(paths), firstLines(out, 40))
	}
	failures := map[string]string{} // input name -> first error or count mismatch
	sawSentinel := false
	for _, line := range errLines {
		m := parseFailureRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[1] == sentinel {
			sawSentinel = true
			continue
		}
		if _, ok := rejectedByPath[m[1]]; ok {
			delete(rejectedByPath, m[1])
			continue
		}
		in, ok := byPath[m[1]]
		if !ok {
			continue
		}
		row, _ := strconv.Atoi(m[3])
		col, _ := strconv.Atoi(m[4])
		failures[in.name] = fmt.Sprintf("%s at %d:%d", m[2], row+1, col+1)
	}
	if !sawSentinel {
		t.Fatalf("the tree-sitter CLI did not report the malformed sentinel input, so its output cannot be trusted:\n%s", firstLines(out, 40))
	}
	for _, in := range rejectedByPath {
		t.Errorf("rejectedInputs %q: the parser rejects it and tree-sitter parses it with no ERROR or MISSING node:\n%s", in.name, in.code)
	}
	compared := 0
	for i, p := range paths[:len(inputs)] {
		in := byPath[p]
		if _, failed := failures[in.name]; failed {
			continue
		}
		diff, n := compareStatementCounts(in.code, trees[i])
		compared += n
		if diff != "" {
			failures[in.name] = diff
		}
	}
	if compared == 0 {
		t.Fatalf("no statement list was compared, so the tree-sitter output was not read:\n%s", firstLines(out, 40))
	}

	matched := map[string]bool{}
	var unexpected []string
	for _, in := range inputs {
		key, pinned := pinnedGap(in)
		if pinned {
			matched[key] = true
		}
		first, failed := failures[in.name]
		switch {
		case failed && !pinned:
			unexpected = append(unexpected, fmt.Sprintf("  %s: %s", in.name, first))
		case !failed && pinned:
			t.Errorf("%s now parses with tree-sitter as the parser does; remove its entry from treeSitterParityGaps", key)
		}
	}
	for key := range treeSitterParityGaps {
		if !matched[key] {
			t.Errorf("treeSitterParityGaps entry %q names no input the parser accepts", key)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Errorf("tree-sitter cannot parse %d of the %d inputs the parser accepts, or reads a different number of statements in one (first ERROR or MISSING node, or first statement list whose count differs, line:column):\n%s\n\n"+
			"Fix tree-sitter-nomi/grammar.js (AGENTS.md, \"Grammar and editor extensions\"), or pin the input in treeSitterParityGaps with its reason.",
			len(unexpected), len(inputs), strings.Join(unexpected, "\n"))
	}
	t.Logf("tree-sitter parsed %d inputs the parser accepts, with the same statement count in %d statement lists", len(inputs), compared)
}

// parseFailureRe reads one `tree-sitter parse --quiet` line:
// `<path> \t <n> ms \t (ERROR [row, col] - [row, col])`.
var parseFailureRe = regexp.MustCompile(`^(\S+)\s+\d+ ms\s+\((ERROR|MISSING[^\[]*?)\s*\[(\d+), (\d+)\]`)

// pinnedGap reports the treeSitterParityGaps key that names in, if any.
func pinnedGap(in parityInput) (string, bool) {
	if _, ok := treeSitterParityGaps[in.name]; ok {
		return in.name, true
	}
	path, _, _ := strings.Cut(in.name, ": line ")
	for key := range treeSitterParityGaps {
		doc, sub, ok := strings.Cut(key, ": ")
		if ok && doc == path && strings.Contains(in.code, sub) {
			return key, true
		}
	}
	return "", false
}

// blockFileMarker is a tour block's `// FILE: <name>` marker.
var blockFileMarker = regexp.MustCompile(`^\s*//\s*FILE:\s*([A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+)\s*$`)

// withoutDataFiles is a multi-file block with its `// FILE:` sections that
// are not Nomi (a data file the program reads, `notes.txt`) left out, marker
// and text, since the editor highlights those as plain text.
func withoutDataFiles(code string) string {
	var kept []string
	keep := true
	for _, line := range strings.Split(code, "\n") {
		if m := blockFileMarker.FindStringSubmatch(line); m != nil {
			keep = strings.HasSuffix(m[1], ".nomi") || strings.HasSuffix(m[1], ".toml")
		}
		if keep {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// parityInputs collects the tracked .nomi files and the Markdown blocks the
// parser accepts.
func parityInputs(t *testing.T) []parityInput {
	t.Helper()
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "*.nomi").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var inputs []parityInput
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		if parses(string(data)) {
			inputs = append(inputs, parityInput{name: rel, code: string(data)})
		}
	}
	tour, err := filepath.Glob(filepath.Join(repoRoot, "tour/src/content/docs/*.md"))
	if err != nil || len(tour) == 0 {
		t.Fatalf("no tour chapters found: %v", err)
	}
	docs := map[string][]string{"docs/spec.md": {"nomi"}}
	for _, p := range tour {
		rel, _ := filepath.Rel(repoRoot, p)
		docs[filepath.ToSlash(rel)] = []string{"nomi", "nomi-run"}
	}
	for rel, langs := range docs {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, lang := range langs {
			for _, b := range doctest.ExtractBlocks(string(data), lang) {
				code := withoutDataFiles(b.Code)
				if parses(code) {
					inputs = append(inputs, parityInput{name: fmt.Sprintf("%s: line %d", rel, b.Line), code: code + "\n"})
				}
			}
		}
	}
	for _, list := range []struct {
		name   string
		inputs []parityInput
	}{{"lineBreakInputs", lineBreakInputs}, {"patternInputs", patternInputs}, {"typeBodyInputs", typeBodyInputs}} {
		for _, in := range list.inputs {
			if !parses(in.code) {
				t.Errorf("%s %q: the parser rejects it, so it is no parity question", list.name, in.name)
				continue
			}
			inputs = append(inputs, parityInput{name: list.name + ": " + in.name, code: in.code})
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].name < inputs[j].name })
	return inputs
}

// lineBreakInputs end a statement at a line break before a line that could
// continue it if the break were whitespace. The lexer ends a statement at a
// line break after a token that can end one, unless the next line starts with
// `|>`, `)`, `]`, `where` or a binary operator, and a line that starts with
// `else` or `end` closes the body above it.
var lineBreakInputs = []parityInput{
	{"minus", "fn main()\n    x = a\n    -1\nend\n"},
	{"or pattern broken after its or", "fn main()\n    case n\n        1 or\n            2 -> 0\n        _ -> 1\n    end\nend\n"},
	{"paren after a name", "fn main()\n    x = f\n    (2)\nend\n"},
	{"paren after a call", "fn main()\n    x = f(1)\n    (2)\nend\n"},
	{"tuple after a destructure", "fn main()\n    (a, b) = f()\n    (a, b)\nend\n"},
	{"tuple destructure after an unwrap", "fn main()\n    x = f()!\n    (a, b) = x\nend\n"},
	{"lambda tuple parameter and annotations", "fn main()\n    f = fn (a, b) -> a\n    g = fn p: (Int, Int) -> p.first\n    h = fn p: (Int, Int) -> p.second\n    k = fn q: (Int) -> Int -> q(1)\n    m = fn r: ((Int, Int)) -> Int, x -> r((x, x))\nend\n"},
	{"bracket after a type name", "fn main()\n    x = Foo\n    [1, 2]\nend\n"},
	{"bracket after a variant", "fn main()\n    x = .None\n    [1]\nend\n"},
	{"bracket after a name", "fn main()\n    x = xs\n    [1, 2]\nend\n"},
	{"dot field", "fn main()\n    x = a\n    .b\nend\n"},
	{"dot variant", "fn main()\n    x = a\n    .Some\nend\n"},
	{"not", "fn main()\n    x = a\n    not c\nend\n"},
	{"string after a type name", "fn main()\n    x = Foo\n    \"s\"\nend\n"},
	{"brace after a type name", "fn main()\n    x = Foo\n    {a: 1}\nend\n"},
	{"named struct after a name", "fn main()\n    x = a\n    Point{x: 1}\nend\n"},
	{"vector after a name", "fn main()\n    x = xs\n    Vector[1, 2]\nend\n"},
	{"map after a name", "fn main()\n    x = a\n    {1 => 2}\nend\n"},
	{"set after a name", "fn main()\n    x = a\n    Set[1]\nend\n"},
	{"block after a name", "fn main()\n    x = a\n    begin\n        b\n    end\nend\n"},
	{"block after a type name", "fn main()\n    x = Foo\n    begin\n        b\n    end\nend\n"},
	{"number", "fn main()\n    x = a\n    1\nend\n"},
	{"name", "fn main()\n    x = a\n    b\nend\n"},
	{"minus after a float", "fn main()\n    x = 1.5\n    -1\nend\n"},
	{"minus after a string", "fn main()\n    x = \"a\"\n    -1\nend\n"},
	{"minus after an if", "fn main()\n    x = if c then a else b\n    -1\nend\n"},
	{"paren after an if", "fn main()\n    x = if c then a else b\n    (1)\nend\n"},
	{"minus after a pipe", "fn main()\n    x = a |> f\n    -1\nend\n"},
	{"minus after a typed binding", "fn main()\n    x: Int = a\n    -1\nend\n"},
	{"minus after a trailing comment", "fn main()\n    x = a // c\n    -1\nend\n"},
	{"minus after a comment line", "fn main()\n    x = a\n    // c\n    -1\nend\n"},
	{"minus in a lambda block", "fn main()\n    f = fn x ->\n        x\n        -1\n    end\nend\n"},
	{"minus after a lambda", "fn main()\n    f = fn x -> x\n    -1\nend\n"},
	{"minus in a case arm block", "fn main()\n    case n\n        1 ->\n            a\n            -1\n        _ -> 0\n    end\nend\n"},
	{"tuple pattern arm", "fn main()\n    case n\n        1 -> f\n        (x, y) -> b\n    end\nend\n"},
	{"negative guard arm", "fn main()\n    case\n        a -> b\n        -1 == c -> d\n    end\nend\n"},
	{"list pattern else arm", "fn main()\n    Ok(v) = r else\n        Err(e) -> Foo\n        [a] -> g\n    end\nend\n"},
	{"minus in a test", "test \"t\"\n    x = a\n    -1\n    assert x\nend\n"},
	{"minus in a setup", "group \"g\"\n    setup begin\n        x = a\n        -1\n    end\n\n    test \"t\", v\n        assert v\n    end\nend\n"},
	{"minus in a concurrent block", "fn main()\n    concurrent\n        x = a\n        -1\n    end\nend\n"},
	{"minus after a once", "once x = a\n-1\n"},
	{"minus at the top level", "x = a\n-1\n"},
	{"minus on a prompt line", "//! x = a\n//! -1\n//! assert x\nfn f() end\n"},
	{"minus after assert", "fn main()\n    assert a\n    -1\nend\n"},
	{"minus after with", "fn main()\n    with App.x = a\n    -1\nend\n"},
	{"paren after defer", "fn main()\n    defer f()\n    (1)\nend\n"},
	{"paren after return", "fn main()\n    return a\n    (1)\nend\n"},
	{"minus after break", "fn main()\n    break a\n    -1\nend\n"},
	{"minus after dbg", "fn main()\n    x = dbg a\n    -1\nend\n"},
	{"minus after continue", "fn main()\n    continue\n    -1\nend\n"},
	{"variant pattern binding", "fn main()\n    Day(n) = d\n    n\nend\n"},
	{"semicolons", "fn main()\n    x = 1; y = 2;\n    x + y\nend\n"},
	{"semicolons at the top level", "x = 1; y = 2\n"},
	// The line continues.
	{"pipe", "fn main()\n    x = a\n    |> f\nend\n"},
	{"pipe after a comment line", "fn main()\n    x = a\n    // c\n    |> f\nend\n"},
	{"else starting its line", "fn main()\n    x = if c then\n        a\n    else\n        b\n    end\nend\n"},
	{"binding else on the next line", "fn main()\n    Some(v) = m\n    else\n        return 0\n    end\n    v\nend\n"},
	{"body on the next line", "fn f() -> Int\n    1\nend\n"},
	{"empty bodies on the header's line", "fn f() end\nstruct E end\nimpl M for E end\n"},
	{"inline if branches that jump", "fn main()\n    if c then break else continue\n    if c then return 1\n    -1\nend\n"},
	{"block if chain sharing one end", "fn main()\n    if a then\n        1\n    else if b then\n        2\n    else\n        3\n    end\n    -1\nend\n"},
	{"arm bodies below their arrows", "fn main()\n    case n\n        1 ->\n            a\n            b\n\n        x when x > 2 ->\n            c\n        _ -> d\n    end\nend\n"},
	{"a lambda body below its arrow and an empty one", "fn main()\n    f = fn x ->\n        x\n    end\n    g = fn -> end\n    -1\nend\n"},
	{"a concurrent block in a group's test", "group \"g\"\n    test \"t\"\n        concurrent\n            x = a\n        end\n        assert x\n    end\nend\n"},
	{"an import block", "import\n    std::io\n    std::iter.Iter\nend\n"},
	{"where on the next line", "fn f<T>(x: T) -> T\nwhere T: Display\n    x\nend\n"},
	{"impl body on the next line", "impl Foo\n    fn f() -> Int\n        1\n    end\nend\n"},
}

// typeBodyInputs separate the items of a struct, enum or interface body by a
// line break, a semicolon, or a line break after a trailing comment.
var typeBodyInputs = []parityInput{
	{"struct fields on lines", "struct P\n    x: Int\n    y: Int = 1\nend\n"},
	{"struct fields after a comment", "struct P\n    x: Int // c\n    y: Int\nend\n"},
	{"struct fields and semicolons", "struct P\n    ; x: Int; y: Int;; z: Int;\nend\n"},
	{"struct field after a semicolon line", "struct P\n    x: Int\n    ;y: Int\nend\n"},
	{"enum variants and semicolons", "enum E\n    A; B Int; C {x: Int}\n    D\nend\n"},
	{"interface functions and semicolons", "interface I\n    fn f(v: self) -> Int; fn g(v: self) -> Int\n    fn h(v: self) -> Int\n        1\n    end\nend\n"},
}

// rejectedInputs are malformed sources the parser rejects, each of which
// tree-sitter must also mark with an ERROR or MISSING node, so an editor shows
// the error `nomi check` reports. A type body holds one item per line.
var rejectedInputs = []parityInput{
	// A `|> subtests` stage ends a pipeline that stands as a statement.
	{"a subtests stage in a binding", "test \"t\"\n    x = [1] |> subtests n\n        assert n == 1\n    end\nend\n"},
	{"a subtests stage in an argument", "test \"t\"\n    f([1] |> subtests n\n        assert n == 1\n    end)\nend\n"},
	{"a stage after a subtests stage", "test \"t\"\n    [1]\n    |> subtests n\n        assert n == 1\n    end\n    |> dbg\nend\n"},
	// A keyword declaration's name is never a type name.
	{"a misspelled type keyword before a type name", "pub strct Point {\n    x: Int\n}\n"},
	{"a keyword before a type name", "pub one_of Size `small`\n"},
	{"a phrase named by a type name", "phrase Hi `Hi`\n"},
	{"two struct fields on one line", "struct P\n    x: Int    y: Int\nend\n"},
	{"a struct field after a default on its line", "struct P\n    x: Int = 1 y: Int\nend\n"},
	{"two enum variants on one line", "enum E\n    A Int B\nend\n"},
	{"an enum variant after a struct payload on its line", "enum E\n    A {x: Int} B\nend\n"},
	{"two interface functions on one line", "interface I\n    fn f(v: self) -> Int    fn g(v: self) -> Int\nend\n"},
	{"an interface function after a default on its line", "interface I\n    fn f(v: self) -> Int end fn g(v: self) -> Int\nend\n"},
	// A collection literal or pattern has no symbol: the `#` and `%` that
	// wrote vectors, sets and maps are errors in both parsers.
	{"a vector written with `#`", "fn main()\n    _v = #[1, 2]\nend\n"},
	{"a set written with `%`", "fn main()\n    _s = %[1, 2]\nend\n"},
	{"a map written with `%`", "fn main()\n    _m = %{\"a\" => 1}\nend\n"},
	{"a named literal written with `#`", "fn main()\n    _p = #Path[1]\nend\n"},
	{"a named map written with `%`", "fn main()\n    _k = %Kvs{\"a\" => 1}\nend\n"},
	{"a vector pattern written with `#`", "fn main()\n    x = case v\n        #[a, ..] -> a\n        _ -> 0\n    end\nend\n"},
	{"a set pattern written with `%`", "fn main()\n    x = case s\n        %[\"a\", ..] -> 1\n        _ -> 0\n    end\nend\n"},
	{"a struct field after a map entry", "fn main()\n    _m = {\"a\" => 1, b: 2}\nend\n"},
	// A capture's body is a call, a parenthesized expression or a literal.
	{"a capture of a name", "fn main()\n    _f = &x\nend\n"},
	{"a set written {...}", "fn main()\n    _s = {1, 2}\nend\n"},

	{"a struct with no type name written .{...}", "fn main()\n    _p = .{x: 1}\nend\n"},
	{"an anonymous struct type written .{...}", "fn f(p: .{x: Int}) -> Int\n    p.x\nend\n"},
	// A field is punned as `name:`; its name alone is no field.
	{"a struct literal field written as its name alone", "fn main()\n    x = 1\n    _p = P{x}\nend\n"},
	{"a struct pattern field written as its name alone", "fn main()\n    x = case p\n        P{x} -> x\n        _ -> 0\n    end\nend\n"},
	{"an anonymous struct type with no field", "fn f(p: {}) -> Int\n    1\nend\n"},

	{"a percent with a space before its bracket", "fn main()\n    _s = % [1, 2]\nend\n"},
	{"a hash with no shape after its name", "fn main()\n    _p = #Point\nend\n"},
	// A struct pattern's or literal's fields are separated by commas.
	{"a renamed struct pattern field with no comma before the next", "fn main()\n    x = case r\n        {err: a b} -> 0\n        _ -> 1\n    end\nend\n"},
	{"punned struct pattern fields with no comma", "fn main()\n    x = case p\n        Point{x y} -> 0\n        _ -> 1\n    end\nend\n"},
	{"struct pattern fields on two lines with no comma", "fn main()\n    x = case p\n        Point{\n            x\n            y\n        } -> 0\n        _ -> 1\n    end\nend\n"},
	{"struct destructure fields with no comma", "fn main()\n    {a b} = p\nend\n"},
	{"struct literal fields with no comma", "fn main()\n    _p = Point{x: 1 y: 2}\nend\n"},
	{"anonymous struct literal fields with no comma", "fn main()\n    _p = {x: 1 y: 2}\nend\n"},
	{"punned struct literal fields with no comma", "fn main()\n    _p = Point{x y}\nend\n"},
	// So are an update's spread and fields, and a map's entries.
	{"a struct update with no comma after its spread", "fn main()\n    _q = {..p x: 10}\nend\n"},
	{"struct update fields with no comma", "fn main()\n    _q = {..p, x: 10 y: 20}\nend\n"},
	{"a stacked struct update with no commas", "fn main()\n    _q = {\n        ..p\n        x: 7\n        y: 8\n    }\nend\n"},
	{"a bare struct update with no comma after its spread", "fn main()\n    _q = {..p, home: {.. city: \"NYC\"}}\nend\n"},
	{"bare struct update fields with no comma", "fn main()\n    _q = {..p, home: {.., city: \"NYC\" zip: \"1\"}}\nend\n"},
	{"a Struct.update patch's fields with no comma", "fn main()\n    _q = Struct.update(u, {home: {.., city: \"NYC\" zip: \"1\"}})\nend\n"},
	{"a map update with no comma after its spread", "fn main()\n    _q = {..m \"b\" => 2}\nend\n"},
	{"map update entries with no comma", "fn main()\n    _q = {..m, \"a\" => 1 \"b\" => 2}\nend\n"},
	{"a stacked map update with no commas", "fn main()\n    _q = {\n        ..m\n        \"a\" => 1\n    }\nend\n"},
	{"map literal entries with no comma", "fn main()\n    _q = {\"a\" => 1 \"b\" => 2}\nend\n"},
	{"map pattern entries with no comma", "fn main()\n    x = case m\n        {\"a\" => a \"b\" => b} -> 0\n        _ -> 1\n    end\nend\n"},
	// No list leaves an empty slot between two commas.
	{"a doubled comma in a struct literal", "fn main()\n    _p = Point{x: 1,, y: 2}\nend\n"},
	{"a doubled comma in an anonymous struct literal", "fn main()\n    _p = {x: 1,, y: 2}\nend\n"},
	{"a doubled comma after an update's spread", "fn main()\n    _q = {..p,, x: 1}\nend\n"},
	{"a doubled comma between update fields", "fn main()\n    _q = {..p, x: 1,, y: 2}\nend\n"},
	{"a doubled comma after a bare spread", "fn main()\n    _q = {..p, home: {..,, city: \"NYC\"}}\nend\n"},
	{"a doubled trailing comma in a struct literal", "fn main()\n    _p = {x: 1,,}\nend\n"},
	{"a doubled comma across lines in a struct literal", "fn main()\n    _p = {\n        x: 1,\n        ,\n        y: 2,\n    }\nend\n"},
	{"a doubled comma in a map literal", "fn main()\n    _m = {\"a\" => 1,, \"b\" => 2}\nend\n"},
	{"a doubled comma after a map update's spread", "fn main()\n    _m = {..m,, \"b\" => 2}\nend\n"},
	{"a doubled comma in a set", "fn main()\n    _s = Set[1,, 2]\nend\n"},
	{"a doubled comma in a list", "fn main()\n    _l = [1,, 2]\nend\n"},
	{"a doubled trailing comma in a list", "fn main()\n    _l = [1,,]\nend\n"},
	{"a doubled comma in a vector", "fn main()\n    _v = Vector[1,, 2]\nend\n"},
	{"a doubled comma in a tuple", "fn main()\n    _t = (1,, 2)\nend\n"},
	{"a doubled comma in call arguments", "fn main()\n    _x = f(1,, 2)\nend\n"},
	{"a doubled comma in named call arguments", "fn main()\n    _x = f(a: 1,, b: 2)\nend\n"},
	{"a doubled comma in parameters", "fn f(a: Int,, b: Int) -> Int\n    a + b\nend\n"},
	{"a doubled comma in lambda parameters", "fn main()\n    _f = fn a,, b -> a\nend\n"},
	{"a doubled comma in a struct pattern", "fn main()\n    x = case p\n        Point{x,, y} -> 0\n        _ -> 1\n    end\nend\n"},
	{"a doubled comma in a struct destructure", "fn main()\n    {a,, b} = p\nend\n"},
	{"a doubled comma in a tuple pattern", "fn main()\n    x = case p\n        (a,, b) -> 0\n        _ -> 1\n    end\nend\n"},
	{"a doubled comma in a list pattern", "fn main()\n    x = case p\n        [a,, b] -> 0\n        _ -> 1\n    end\nend\n"},
	{"a doubled comma in a map pattern", "fn main()\n    x = case m\n        {\"a\" => a,, \"b\" => b} -> 0\n        _ -> 1\n    end\nend\n"},
	{"a doubled comma in a variant pattern", "fn main()\n    x = case p\n        Some(a,, b) -> 0\n        _ -> 1\n    end\nend\n"},
	{"a doubled comma in type arguments", "fn main()\n    _m: Map<String,, Int> = {}\nend\n"},
	{"a doubled comma in type parameters", "fn f<A,, B>(a: A, _b: B) -> A {\n    a\n}\n"},
	{"a doubled comma in a tuple type", "fn f(p: (Int,, Int)) -> Int\n    0\nend\n"},
	{"a doubled comma in a function type", "fn f(g: (Int,, Int) -> Int) -> Int\n    0\nend\n"},
	{"a doubled comma in a variant payload", "enum E\n    A(Int,, Int)\nend\n"},
	{"a doubled comma in an import list", "import std::json.{Json,, ToJson}\n"},
}

// patternInputs put a pattern where the parser reads one: the left side of a
// binding (with `as`, with `else`, nested), a case, guard or else arm, a
// lambda or function parameter, an `if` condition and a pattern assertion.
var patternInputs = []parityInput{
	{"headless struct as", "fn main()\n    {x: _c, y:} as p = q\n    0\nend\n"},
	{"headless struct punned first as", "fn main()\n    {y:, x: _c} as p = q\n    0\nend\n"},
	{"headless struct as with else", "fn main()\n    {x: _c, y:} as p = q else\n        return 0\n    end\n    0\nend\n"},
	{"headless struct with a nested as", "fn main()\n    {x: (a, b) as inner, y:} as p = q\n    0\nend\n"},
	{"punned headless struct as", "fn main()\n    {x:, y:} as p = q\n    0\nend\n"},
	{"labeled headless struct as", "fn main()\n    {x: a, y: b} as p = q\n    0\nend\n"},
	{"headed struct with a nested as", "fn main()\n    P{x: (a, b) as inner, y:} as p = q\n    0\nend\n"},
	{"tuple as", "fn main()\n    (a, b) as t = pair\n    0\nend\n"},
	{"tuple with a nested as", "fn main()\n    ((a, b) as inner, c) as t = pair\n    0\nend\n"},
	{"list as", "fn main()\n    [x, ..rest] as all = xs\n    0\nend\n"},
	{"list as with else", "fn main()\n    [x, ..rest] as all = xs else return 0\n    0\nend\n"},
	{"list with a nested as", "fn main()\n    [(a, b) as first, ..rest] as all = xs else\n        return 0\n    end\n    0\nend\n"},
	{"variant as", "fn main()\n    Some(n) as m = opt else return 0\n    0\nend\n"},
	{"variant with a nested as", "fn main()\n    Some((a, b) as inner) as m = opt else\n        return 0\n    end\n    0\nend\n"},
	{"variant with a nested as and no outer as", "fn main()\n    Some((a, b) as inner) = opt else\n        return 0\n    end\n    0\nend\n"},
	{"map as", "fn main()\n    {\"a\" => v} as all = m else\n        return 0\n    end\n    0\nend\n"},
	{"map with a nested as", "fn main()\n    {\"a\" => (a, b) as v} as all = m else\n        return 0\n    end\n    0\nend\n"},
	{"nested tuple destructure", "fn main()\n    ((a, b), c) = pair\n    0\nend\n"},
	{"tuple destructure with a nested as", "fn main()\n    ((a, b) as inner, c) = pair\n    0\nend\n"},
	{"struct destructure with a nested tuple", "fn main()\n    {x: (a, b), y:} = q\n    0\nend\n"},
	{"struct destructure with a variant and else", "fn main()\n    {x: Some(a), y:} = q else\n        return 0\n    end\n    0\nend\n"},
	{"map destructure with a nested tuple", "fn main()\n    {\"a\" => (a, b)} = m else\n        return 0\n    end\n    0\nend\n"},
	{"case arm headless struct as", "fn main()\n    case q\n        {x: _c, y:} as p -> y\n        {x: (a, b) as xs, y:} as p -> y\n    end\nend\n"},
	{"case arm tuple and list as", "fn main()\n    case q\n        ([x, ..rest] as all, n) as t -> x\n        _ -> 0\n    end\nend\n"},
	{"guard arm nested as", "fn main()\n    v |> case\n            Some((a, b) as p) -> a\n            _ -> 0\n        end\nend\n"},
	{"else arm headless struct as", "fn main()\n    Ok(v) = r else\n        Err({x: _c, y:} as p) -> return y\n    end\nend\n"},
	{"if condition headless struct as", "fn main()\n    if {x: _c, y:} as p = q then y else 0\nend\n"},
	{"lambda headless struct as", "fn main()\n    xs |> Iter.map(fn {x: _c, y:} as p -> y)\nend\n"},
	{"lambda nested as", "fn main()\n    xs |> Iter.map(fn ((a, b) as t, c) -> a)\n    xs |> Iter.map(fn {x: (a, b) as t, y:} -> y)\nend\n"},
	{"parameter headless struct as", "fn f({x: _c, y:} as p: P) -> Int\n    y\nend\n"},
	{"pattern assertion headless struct as", "test \"t\"\n    assert {x: _c, y:} as p = q\n    assert Some((a, b) as p) = v\nend\n"},
	{"self parameter pattern", "interface S\n    fn f(self(n) as t, by: Int) -> self where self: Distinct<Int>\n        t\n    end\nend\n"},
	{"self binding and case", "interface S\n    fn f(t: self) -> self where self: Distinct<Int>\n        self(m) = t\n        case t\n            self(0) -> self(m)\n            self((a, b)) -> t\n            _ -> m |> self()\n        end\n    end\nend\n"},
	{"if condition map pattern", "fn main()\n    if {\"host\" => h} = m then\n        h\n    else\n        0\n    end\nend\n"},
	{"if condition map pattern binding host", "fn main()\n    if {\"host\" => host} = m then\n        host\n    end\n    0\nend\n"},
	{"if condition punned struct pattern", "fn main()\n    if P{x:, y:} = p then\n        x\n    else\n        0\n    end\nend\n"},
	{"if condition headless struct pattern with a literal", "fn main()\n    if {x: 0, y:} = p then y else 0\nend\n"},
	{"if condition list pattern", "fn main()\n    if [first, ..rest] = xs then first else 0\nend\n"},
	{"if condition or pattern of maps", "fn main()\n    if {\"a\" => v} or {\"b\" => v} = m then v else 0\nend\n"},
	{"if condition or pattern of lists", "fn main()\n    if [a] or [a, _] = xs then a else 0\nend\n"},
	{"map binding with else", "fn main()\n    {\"k\" => v} = m else\n        return 0\n    end\n    v\nend\n"},
	{"map binding with else arms", "fn main()\n    {\"k\" => v} = m else\n        {\"j\" => w} -> return w\n        _ -> return 0\n    end\n    v\nend\n"},
	{"headless struct binding with a literal and else", "fn main()\n    {x: 1, y:} = p else\n        return 0\n    end\n    y\nend\n"},
	{"headless struct binding with a variant and else", "fn main()\n    {done: True, n:} = p else\n        return 0\n    end\n    n\nend\n"},
	{"list binding with else", "fn main()\n    [a, ..rest] = xs else\n        return 0\n    end\n    a\nend\n"},
	{"or pattern of maps binding with else", "fn main()\n    {\"a\" => v} or {\"b\" => v} = m else\n        return 0\n    end\n    v\nend\n"},
	{"host as a name", "struct S\n    host: String\nend\n\nhost fn now() -> Int\n\nfn f(host: String) -> S\n    host = String.trim(host)\n    host\n    S{host:}\nend\n"},
	{"named struct binding", "fn main()\n    Point{x:, y:} = p\n    x\nend\n"},
	{"named tuple binding", "fn main()\n    Pair(a, b) = p\n    a\nend\n"},
	{"named tuple binding with else", "fn main()\n    Pair(0, b) = p else\n        return 0\n    end\n    b\nend\n"},
	{"vector binding with else", "fn main()\n    [first, ..rest] = v else\n        return 0\n    end\n    first\nend\n"},
	{"vector binding from the end with else", "fn main()\n    [.., last] = v else\n        return 0\n    end\n    last\nend\n"},
	{"set binding with else", "fn main()\n    [\"admin\", ..rest] = s else\n        return 0\n    end\n    rest\nend\n"},
	{"named map binding with else", "fn main()\n    Kvs{\"k\" => v} = m else\n        return 0\n    end\n    v\nend\n"},
	{"case arm vector patterns", "fn main()\n    x = case v\n        [] -> 0\n        [a, b] -> a\n        [first, ..rest] -> first\n        [.., last] -> last\n        [a, ..mid, z] -> a\n        [a, b, ..] -> a\n        [..] -> 0\n        Path[a, ..] -> a\n        .Items[x] -> x\n    end\nend\n"},
	{"case arm vector or and as", "fn main()\n    x = case v\n        [a] or [a, _] -> a\n        [(a, b), ..] as whole -> a\n        _ -> 0\n    end\nend\n"},
	{"case arm set patterns", "fn main()\n    x = case s\n        [\"admin\", ..rest] -> 1\n        [\"guest\", ..] -> 2\n        [1, 2] -> 3\n        Tags[\"a\", ..] -> 4\n        _ -> 0\n    end\nend\n"},
	{"case arm named patterns", "fn main()\n    x = case s\n        .Circle{radius:} -> radius\n        Shape.Square{side:} -> side\n        geo.Point{x:} -> x\n        Kvs{\"k\" => v} -> v\n        .Obj{\"k\" => v} -> v\n        Pair(a, b) -> a\n        _ -> 0\n    end\nend\n"},
	{"case on a struct literal", "fn main()\n    x = case (Point{x: 1})\n        Point{x:} -> x\n    end\nend\n"},
	{"else arm named patterns", "fn main()\n    Ok(v) = r else\n        Err(.Invalid{reason:}) -> return 0\n        Err(e) -> return 1\n    end\n    v\nend\n"},
	{"else arm vector and set patterns", "fn main()\n    Ok(v) = r else\n        [a, ..] -> return a\n        [1, ..] -> return 1\n        _ -> return 0\n    end\n    v\nend\n"},
	{"if condition vector pattern", "fn main()\n    if [a, ..] = v then a else 0\nend\n"},
	{"if condition set pattern", "fn main()\n    if [1, ..] = s then 1 else 0\nend\n"},
	{"if condition named struct pattern", "fn main()\n    if Point{x:, y:} = p then x else 0\nend\n"},
	{"if condition struct literal", "fn main()\n    x = if p == (Point{x: 1}) then 1 else 2\nend\n"},
	{"if condition vector literal", "fn main()\n    x = if Vector[.., z] == v then z else 0\nend\n"},
	{"if condition bracket literal with a bare spread", "fn main()\n    x = if [.., z] == v then z else 0\nend\n"},
	{"empty map literal and pattern", "fn main()\n    m = {}\n    k = Kvs{}\n    x = case m\n        {} -> 0\n    end\nend\n"},
	{"named std collections", "fn main()\n    _v = Vector[1, ..v]\n    _s = Set[]\n    _m = Map{\"a\" => 1}\n    _l = List[1]\nend\n"},
	{"parameter patterns of each shape", "fn f(Point{x:, y:}, [a, ..], {\"k\" => v}, Pair(m, n)) -> Int\n    x\nend\n"},
	{"lambda vector and map patterns", "fn main()\n    f = fn [x, ..] -> x\n    g = fn {\"k\" => v} -> v\nend\n"},
	{"pattern assertion named patterns", "test \"t\"\n    assert Point{x:, y:} = Point{x: 3, y: 4}\n    assert [a, ..] = Vector[1, 2]\nend\n"},
	{"self as a value", "interface S\n    fn f(_t: self, ns: List<Int>) -> Int where self: Distinct<Int>\n        make = self\n        ns |> Iter.map(self) |> Iter.count()\n    end\nend\n"},
	// A variant named through the file that declares its type.
	{"case arm file-qualified variants", "fn main()\n    x = case r\n        Err(sql.Error.Closed) -> 1\n        Err(sql.Error.Constraint(k)) -> 2\n        Err(shapes.Bag.Items[a, ..rest]) -> 3\n        Err(sql.Error.Closed or sql.Error.Finished) -> 4\n        Err(sql.Error.Closed as e) -> 5\n        sql.Error.Closed -> 6\n        sql.Error.Constraint(k) when k > 0 -> 7\n        sql.Error.A or sql.Error.B -> 8\n        sql.Error.C(n) as e -> 9\n        shapes.Bag.Items[a] -> 10\n        geo.Deep.Enum.V -> 11\n        [sql.Error.A, ..rest] -> 12\n        {\"k\" => sql.Error.A} -> 13\n        (sql.Error.A) -> 14\n        _ -> 0\n    end\nend\n"},
	{"if condition file-qualified variants", "fn main()\n    if Err(sql.Error.Closed) = r then\n        1\n    end\n    if sql.Error.Constraint(n) = r then n else 0\nend\n"},
	{"else arm file-qualified variants", "fn main()\n    Ok(v) = r else\n        sql.Error.Closed -> return 1\n        sql.Error.A or sql.Error.B -> return 2\n        Err(sql.Error.C(_)) as e -> return 3\n        shapes.Bag.Items[a] -> return a\n        _ -> return 0\n    end\n    v\nend\n"},
	{"binding and assertion file-qualified variants", "test \"t\"\n    Err(sql.Error.Constraint(k)) = r else\n        return\n    end\n    shapes.Wrap.W(m) = w\n    assert Err(sql.Error.Closed) = r\nend\n"},
	{"struct pattern field file-qualified variants", "fn main()\n    x = case s\n        {err: sql.Error.Closed} -> 1\n        Slot{shape: shapes.Shape.Square(n), bag:} -> n\n        _ -> 0\n    end\n    if {err: sql.Error.Closed} = s then 1 else 0\nend\n"},
	{"if condition type-qualified variants", "fn main()\n    if Shape.Circle(r) = s then\n        return r\n    end\n    if Probe.Reading.Steady(r) = s then\n        return r\n    end\n    if Shape.Circle(r) as c = s then\n        return r\n    end\n    if Shape.Circle(r) or Shape.Square(r) = s then\n        return r\n    end\n    if Shape.Circle((a, _b)) = s then a else 0\nend\n"},
	{"parameter and lambda file-qualified variants", "fn f(shapes.Wrap.W(n)) -> Int\n    g = fn shapes.Wrap.W(m) -> m\n    n\nend\n"},
}

// treeSitterCLI returns a function that runs scripts/tree-sitter.sh with its
// arguments and returns the combined output and error, or skips the test
// when the pinned CLI is not available without the network.
func treeSitterCLI(t *testing.T) func(args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx is not on PATH; the tree-sitter CLI runs through it")
	}
	script, err := filepath.Abs(filepath.Join(repoRoot, "scripts/tree-sitter.sh"))
	if err != nil {
		t.Fatal(err)
	}
	libdir := os.Getenv("TREE_SITTER_LIBDIR")
	if libdir == "" {
		grammar, err := filepath.Abs(filepath.Join(repoRoot, "tree-sitter-nomi"))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(grammar))
		libdir = filepath.Join(os.TempDir(), "nomi-tree-sitter-lib", hex.EncodeToString(sum[:8]))
	}
	if err := os.MkdirAll(libdir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command(script, args...)
		cmd.Env = append(os.Environ(), "TREE_SITTER_LIBDIR="+libdir, "npm_config_offline=true")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("--version"); err != nil {
		t.Skipf("the pinned tree-sitter CLI is not in npx's cache (run scripts/tree-sitter.sh --version once online): %v\n%s", err, out)
	}
	return run
}

// splitParseOutput splits the output of `tree-sitter parse` over several
// files into one S-expression per file, in the order the files were given,
// and the per-file error lines it prints after a tree with an error.
func splitParseOutput(out string) (trees []string, errLines []string) {
	var cur []string
	flush := func() {
		if cur != nil {
			trees = append(trees, strings.Join(cur, "\n"))
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "(source_file"):
			flush()
			cur = []string{line}
		case strings.HasPrefix(line, " ") && cur != nil:
			cur = append(cur, line)
		default:
			flush()
			if strings.HasPrefix(line, "/") {
				errLines = append(errLines, line)
			}
		}
	}
	flush()
	return trees, errLines
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// A statement list is a place where the parser ends a statement at a line
// break: the top level, a body, an `if`'s branch, an arm's body below its
// `->`, the arms of a `case` and the arms of a binding's `else`. It is keyed
// by the 1-based line its first statement or arm starts on, which no other
// list's first item shares; the top level is line 0. An empty list is not
// compared.
type listPos struct{ line int }

// compareStatementCounts compares the number of statements in each of the
// parser's statement lists with tree-sitter's reading of the same list. A
// line break the grammar reads as whitespace where the parser ends a
// statement (`x = a` then a line `-1`) joins two statements into one with no
// ERROR, which only a count catches. It returns the first difference and the
// number of lists compared. A list only one side has is not compared.
func compareStatementCounts(code, tree string) (string, int) {
	goLists := parserStatementLists(code)
	tsLists := treeSitterStatementLists(code, tree)
	var diffs []listPos
	n := 0
	for pos, want := range goLists {
		got, ok := tsLists[pos]
		if !ok {
			continue
		}
		n++
		if got != want {
			diffs = append(diffs, pos)
		}
	}
	if len(diffs) == 0 {
		return "", n
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].line < diffs[j].line })
	d := diffs[0]
	where := fmt.Sprintf("the list whose first item starts on line %d", d.line)
	if d == (listPos{}) {
		where = "the top level"
	}
	return fmt.Sprintf("%s holds %d statements for the parser and %d for tree-sitter", where, goLists[d], tsLists[d]), n
}

// startLine is the line a statement or an arm starts on.
func startLine(n ast.Node) int {
	if s, ok := n.(ast.HasSpan); ok && s.GetSpan().StartLine > 0 {
		return s.GetSpan().StartLine
	}
	return n.LineNum()
}

// parserStatementLists counts the statements of each statement list
// parser.Parse reads in code.
func parserStatementLists(code string) map[listPos]int {
	nodes, err := parser.Parse(lexer.Lex(code))
	if err != nil {
		return nil
	}
	lists := map[listPos]int{{}: len(nodes)}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Block:
				// An inline branch's or a one-line lambda's block has no
				// end, and holds no list.
				if v.EndLine != 0 && len(v.Stmts) > 0 {
					lists[listPos{startLine(v.Stmts[0])}] = len(v.Stmts)
				}
			case *ast.Case:
				if len(v.Branches) > 0 {
					lists[listPos{v.Branches[0].Line}] = len(v.Branches)
				}
			case *ast.BindingElse:
				if len(v.Arms) > 0 {
					lists[listPos{v.Arms[0].Line}] = len(v.Arms)
				}
			case *ast.AttachedTest:
				if v.Body != nil && len(v.Body.Stmts) > 0 {
					lists[listPos{startLine(v.Body.Stmts[0])}] = len(v.Body.Stmts)
				}
			}
			return true
		})
	}
	return lists
}

// sexpNodeRe reads one node line of a `tree-sitter parse` S-expression: its
// indentation, its kind and its start.
var sexpNodeRe = regexp.MustCompile(`^( *)(?:[a-z_]+: )?\(([A-Za-z_]+) \[(\d+), (\d+)\]`)

// treeSitterStatementLists counts the statements of each statement list in
// tree-sitter's tree for code: the items of the top level, a body, an `if`
// branch's block and an arm's block, and the arms of a case and of a
// binding's else. Comments and `//!` line prompts are extras, not
// statements.
func treeSitterStatementLists(code, tree string) map[listPos]int {
	type node struct {
		kind     string
		row, col int
		parent   int
	}
	var nodes []node
	var stack []int // the open node at each depth
	for _, line := range strings.Split(tree, "\n") {
		m := sexpNodeRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		depth := len(m[1]) / 2
		if depth > len(stack) {
			return nil // not the CLI's layout; compare nothing
		}
		row, _ := strconv.Atoi(m[3])
		col, _ := strconv.Atoi(m[4])
		parent := -1
		if depth > 0 {
			parent = stack[depth-1]
		}
		stack = append(stack[:depth], len(nodes))
		nodes = append(nodes, node{kind: m[2], row: row, col: col, parent: parent})
	}
	isList := func(kind string) bool {
		switch kind {
		case "source_file", "body", "block", "arm_block", "case_expression", "else_arms", "attached_test_prompt":
			return true
		}
		return false
	}
	counts := map[int]int{}
	first := map[int]int{} // the row of a list's first item
	for _, n := range nodes {
		if n.parent < 0 || !isList(nodes[n.parent].kind) {
			continue
		}
		switch nodes[n.parent].kind {
		case "case_expression":
			if n.kind != "case_branch" && n.kind != "guard_branch" {
				continue
			}
		case "else_arms":
			if n.kind != "else_arm" {
				continue
			}
		default:
			switch n.kind {
			case "line_comment", "doc_comment", "attached_comment_test_line_prompt",
				"attached_comment_test_prompt_marker", "shebang":
				continue
			}
		}
		if counts[n.parent] == 0 {
			first[n.parent] = n.row
		}
		counts[n.parent]++
	}
	lists := map[listPos]int{}
	for i, c := range counts {
		key := listPos{first[i] + 1}
		if nodes[i].kind == "source_file" {
			key = listPos{}
		}
		lists[key] = c
	}
	return lists
}
