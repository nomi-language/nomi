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
// parser rejects is not a parity question.
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
var treeSitterParityGaps = map[string]string{}

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
				if parses(b.Code) {
					inputs = append(inputs, parityInput{name: fmt.Sprintf("%s: line %d", rel, b.Line), code: b.Code + "\n"})
				}
			}
		}
	}
	for _, list := range []struct {
		name   string
		inputs []parityInput
	}{{"lineBreakInputs", lineBreakInputs}, {"patternInputs", patternInputs}} {
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
// `|>`, `)` or `]`, and the parser steps over the break only before `else`
// and, in a declaration's signature, before `{` and `where`.
var lineBreakInputs = []parityInput{
	{"minus", "fn main() {\n    x = a\n    -1\n}\n"},
	{"paren after a name", "fn main() {\n    x = f\n    (2)\n}\n"},
	{"paren after a call", "fn main() {\n    x = f(1)\n    (2)\n}\n"},
	{"tuple after a destructure", "fn main() {\n    (a, b) = f()\n    (a, b)\n}\n"},
	{"bracket after a type name", "fn main() {\n    x = Foo\n    [1, 2]\n}\n"},
	{"bracket after a variant", "fn main() {\n    x = .None\n    [1]\n}\n"},
	{"bracket after a name", "fn main() {\n    x = xs\n    [1, 2]\n}\n"},
	{"dot field", "fn main() {\n    x = a\n    .b\n}\n"},
	{"dot variant", "fn main() {\n    x = a\n    .Some\n}\n"},
	{"bang", "fn main() {\n    x = a\n    !c\n}\n"},
	{"string after a type name", "fn main() {\n    x = Foo\n    \"s\"\n}\n"},
	{"brace after a type name", "fn main() {\n    x = Foo\n    {a: 1}\n}\n"},
	{"number", "fn main() {\n    x = a\n    1\n}\n"},
	{"name", "fn main() {\n    x = a\n    b\n}\n"},
	{"minus after a float", "fn main() {\n    x = 1.5\n    -1\n}\n"},
	{"minus after a string", "fn main() {\n    x = \"a\"\n    -1\n}\n"},
	{"minus after an if", "fn main() {\n    x = if c { a } else { b }\n    -1\n}\n"},
	{"paren after an if", "fn main() {\n    x = if c { a } else { b }\n    (1)\n}\n"},
	{"minus after a pipe", "fn main() {\n    x = a |> f\n    -1\n}\n"},
	{"minus after a typed binding", "fn main() {\n    x: Int = a\n    -1\n}\n"},
	{"minus after a trailing comment", "fn main() {\n    x = a // c\n    -1\n}\n"},
	{"minus after a comment line", "fn main() {\n    x = a\n    // c\n    -1\n}\n"},
	{"minus in a lambda block", "fn main() {\n    f = |x| {\n        x\n        -1\n    }\n}\n"},
	{"minus after a lambda", "fn main() {\n    f = |x| x\n    -1\n}\n"},
	{"minus in a case arm block", "fn main() {\n    case n {\n        1 -> {\n            a\n            -1\n        }\n        _ -> 0\n    }\n}\n"},
	{"tuple pattern arm", "fn main() {\n    case n {\n        1 -> f\n        (x, y) -> b\n    }\n}\n"},
	{"negative guard arm", "fn main() {\n    case {\n        a -> b\n        -1 == c -> d\n    }\n}\n"},
	{"list pattern else arm", "fn main() {\n    Ok(v) = r else {\n        Err(e) -> Foo\n        [a] -> g\n    }\n}\n"},
	{"minus in a test", "test \"t\" {\n    x = a\n    -1\n    assert x\n}\n"},
	{"minus in a setup", "tests \"g\" {\n    setup {\n        x = a\n        -1\n    }\n\n    test \"t\", v {\n        assert v\n    }\n}\n"},
	{"minus in a concurrent block", "fn main() {\n    concurrent {\n        x = a\n        -1\n    }\n}\n"},
	{"minus after a once", "once x = a\n-1\n"},
	{"minus at the top level", "x = a\n-1\n"},
	{"minus on a prompt line", "//! x = a\n//! -1\n//! assert x\nfn f() {\n}\n"},
	{"minus after assert", "fn main() {\n    assert a\n    -1\n}\n"},
	{"minus after with", "fn main() {\n    with App.x = a\n    -1\n}\n"},
	{"paren after defer", "fn main() {\n    defer f()\n    (1)\n}\n"},
	{"paren after return", "fn main() {\n    return a\n    (1)\n}\n"},
	{"minus after break", "fn main() {\n    break a\n    -1\n}\n"},
	{"minus after dbg", "fn main() {\n    x = dbg a\n    -1\n}\n"},
	{"minus after continue", "fn main() {\n    continue\n    -1\n}\n"},
	{"variant pattern binding", "fn main() {\n    Day(n) = d\n    n\n}\n"},
	// The line continues.
	{"pipe", "fn main() {\n    x = a\n    |> f\n}\n"},
	{"pipe after a comment line", "fn main() {\n    x = a\n    // c\n    |> f\n}\n"},
	{"else on the next line", "fn main() {\n    x = if c {\n        a\n    }\n    else {\n        b\n    }\n}\n"},
	{"binding else on the next line", "fn main() {\n    Some(v) = m\n    else { return 0 }\n    v\n}\n"},
	{"body on the next line", "fn f(): Int\n{\n    1\n}\n"},
	{"where on the next line", "fn f<T>(x: T): T\nwhere T: Display {\n    x\n}\n"},
	{"impl body on the next line", "impl Foo {\n    fn f(): Int\n    {\n        1\n    }\n}\n"},
}

// patternInputs put a pattern where the parser reads one: the left side of a
// binding (with `as`, with `else`, nested), a case, guard or else arm, a
// lambda or function parameter, an `if` condition and a pattern assertion.
var patternInputs = []parityInput{
	{"headless struct as", "fn main() {\n    {x: _c, y} as p = q\n    0\n}\n"},
	{"headless struct punned first as", "fn main() {\n    {y, x: _c} as p = q\n    0\n}\n"},
	{"headless struct as with else", "fn main() {\n    {x: _c, y} as p = q else { return 0 }\n    0\n}\n"},
	{"headless struct with a nested as", "fn main() {\n    {x: (a, b) as inner, y} as p = q\n    0\n}\n"},
	{"punned headless struct as", "fn main() {\n    {x, y} as p = q\n    0\n}\n"},
	{"labeled headless struct as", "fn main() {\n    {x: a, y: b} as p = q\n    0\n}\n"},
	{"headed struct with a nested as", "fn main() {\n    P{x: (a, b) as inner, y} as p = q\n    0\n}\n"},
	{"tuple as", "fn main() {\n    (a, b) as t = pair\n    0\n}\n"},
	{"tuple with a nested as", "fn main() {\n    ((a, b) as inner, c) as t = pair\n    0\n}\n"},
	{"list as", "fn main() {\n    [x, ..rest] as all = xs\n    0\n}\n"},
	{"list as with else", "fn main() {\n    [x, ..rest] as all = xs else { return 0 }\n    0\n}\n"},
	{"list with a nested as", "fn main() {\n    [(a, b) as first, ..rest] as all = xs else { return 0 }\n    0\n}\n"},
	{"variant as", "fn main() {\n    Some(n) as m = opt else { return 0 }\n    0\n}\n"},
	{"variant with a nested as", "fn main() {\n    Some((a, b) as inner) as m = opt else { return 0 }\n    0\n}\n"},
	{"variant with a nested as and no outer as", "fn main() {\n    Some((a, b) as inner) = opt else { return 0 }\n    0\n}\n"},
	{"map as", "fn main() {\n    {\"a\" => v} as all = m else { return 0 }\n    0\n}\n"},
	{"map with a nested as", "fn main() {\n    {\"a\" => (a, b) as v} as all = m else { return 0 }\n    0\n}\n"},
	{"nested tuple destructure", "fn main() {\n    ((a, b), c) = pair\n    0\n}\n"},
	{"tuple destructure with a nested as", "fn main() {\n    ((a, b) as inner, c) = pair\n    0\n}\n"},
	{"struct destructure with a nested tuple", "fn main() {\n    {x: (a, b), y} = q\n    0\n}\n"},
	{"struct destructure with a variant and else", "fn main() {\n    {x: Some(a), y} = q else { return 0 }\n    0\n}\n"},
	{"map destructure with a nested tuple", "fn main() {\n    {\"a\" => (a, b)} = m else { return 0 }\n    0\n}\n"},
	{"case arm headless struct as", "fn main() {\n    case q {\n        {x: _c, y} as p -> y\n        {x: (a, b) as xs, y} as p -> y\n    }\n}\n"},
	{"case arm tuple and list as", "fn main() {\n    case q {\n        ([x, ..rest] as all, n) as t -> x\n        _ -> 0\n    }\n}\n"},
	{"guard arm nested as", "fn main() {\n    v |> case {\n            Some((a, b) as p) -> a\n            _ -> 0\n        }\n}\n"},
	{"else arm headless struct as", "fn main() {\n    Ok(v) = r else {\n        Err({x: _c, y} as p) -> return y\n    }\n}\n"},
	{"if condition headless struct as", "fn main() {\n    if {x: _c, y} as p = q { y } else { 0 }\n}\n"},
	{"lambda headless struct as", "fn main() {\n    xs |> Iter.map(|{x: _c, y} as p| y)\n}\n"},
	{"lambda nested as", "fn main() {\n    xs |> Iter.map(|((a, b) as t, c)| a)\n    xs |> Iter.map(|{x: (a, b) as t, y}| y)\n}\n"},
	{"parameter headless struct as", "fn f({x: _c, y} as p: P): Int {\n    y\n}\n"},
	{"pattern assertion headless struct as", "test \"t\" {\n    assert {x: _c, y} as p = q\n    assert Some((a, b) as p) = v\n}\n"},
	{"self parameter pattern", "interface S {\n    fn f(self(n) as t, by: Int): self where self: Distinct<Int> {\n        t\n    }\n}\n"},
	{"self binding and case", "interface S {\n    fn f(t: self): self where self: Distinct<Int> {\n        self(m) = t\n        case t {\n            self(0) -> self(m)\n            self((a, b)) -> t\n            _ -> m |> self()\n        }\n    }\n}\n"},
	{"self as a value", "interface S {\n    fn f(_t: self, ns: List<Int>): Int where self: Distinct<Int> {\n        make = self\n        ns |> Iter.map(self) |> Iter.count()\n    }\n}\n"},
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
// break: the top level, a block, the arms of a `case` and the arms of a
// binding's `else`. It is keyed by the 1-based line and byte column of its
// opening token (`{`, or `case`); the top level is {0, 0}.
type listPos struct{ line, col int }

// compareStatementCounts compares the number of statements in each of the
// parser's statement lists with tree-sitter's reading of the same list. A
// line break the grammar reads as whitespace where the parser ends a
// statement (`x = a` then a line `-1`) joins two statements into one with no
// ERROR, which only a count catches. It returns the first difference and the
// number of lists compared. A list only one side has (a brace the parser
// reads as a map literal and tree-sitter as a block) is not compared.
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
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].line != diffs[j].line {
			return diffs[i].line < diffs[j].line
		}
		return diffs[i].col < diffs[j].col
	})
	d := diffs[0]
	where := fmt.Sprintf("the list at %d:%d", d.line, d.col)
	if d == (listPos{}) {
		where = "the top level"
	}
	return fmt.Sprintf("%s holds %d statements for the parser and %d for tree-sitter", where, goLists[d], tsLists[d]), n
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
				if v.EndLine != 0 { // a synthesized block has no braces
					lists[listPos{v.Line, v.Col}] = len(v.Stmts)
				}
			case *ast.Case:
				lists[listPos{v.Line, v.Col}] = len(v.Branches)
			case *ast.BindingElse:
				if v.Arms != nil {
					lists[listPos{v.LBraceLine, v.LBraceCol}] = len(v.Arms)
				}
			case *ast.AttachedTest:
				if v.Body != nil {
					lists[listPos{v.Line, v.Col}] = len(v.Body.Stmts)
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
// tree-sitter's tree for code: the items of the top level, a body and a
// concurrent block, and the arms of a case and of a binding's else. Comments
// and `//!` line prompts are extras, not statements.
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
	lineStarts := []int{0}
	for i := 0; i < len(code); i++ {
		if code[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	// braceAfter is the position of the first `{` at or after row:col, the
	// opening brace of a concurrent block, which has no body node.
	braceAfter := func(row, col int) listPos {
		if row >= len(lineStarts) {
			return listPos{-1, -1}
		}
		off := lineStarts[row] + col
		i := strings.IndexByte(code[off:], '{')
		if i < 0 {
			return listPos{-1, -1}
		}
		off += i
		r := sort.Search(len(lineStarts), func(k int) bool { return lineStarts[k] > off }) - 1
		return listPos{r + 1, off - lineStarts[r] + 1}
	}
	lists := map[listPos]int{}
	keyOf := map[int]listPos{}
	for i, n := range nodes {
		switch n.kind {
		case "source_file":
			keyOf[i] = listPos{}
		case "body", "case_expression", "else_arms", "attached_test_prompt":
			keyOf[i] = listPos{n.row + 1, n.col + 1}
		case "concurrent_block":
			keyOf[i] = braceAfter(n.row, n.col)
		default:
			continue
		}
		lists[keyOf[i]] = 0
	}
	for _, n := range nodes {
		key, ok := keyOf[n.parent]
		if n.parent < 0 || !ok {
			continue
		}
		switch nodes[n.parent].kind {
		case "case_expression":
			if n.kind == "case_branch" || n.kind == "guard_branch" {
				lists[key]++
			}
		case "else_arms":
			if n.kind == "else_arm" {
				lists[key]++
			}
		default:
			switch n.kind {
			case "line_comment", "doc_comment", "attached_comment_test_line_prompt",
				"attached_comment_test_prompt_marker", "shebang":
			default:
				lists[key]++
			}
		}
	}
	return lists
}
