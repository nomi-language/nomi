package analysis_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// An unannotated `once` takes the type of its value, which only checking the
// value tells. Each file's CheckTypes used to set it when it reached the
// declaration, so a read checked earlier saw a `once` with no type: a read in
// another file the front end checked first (the entry is checked before its
// siblings), a read above the declaration in its own file, and an owner-level
// `once` read through its owner (`Limits.top`). A bare read then passed
// anything, and a read inside a composite gave Unit or an unsolved T:
// `(n, help)` was `(Int, Unit)`, `Int.max_value` was "type 'Int' has no
// member 'max_value'", and iterating a `once` list of enum variants gave
// "expected Direction, got T". A read now checks the declaration on demand.

// checkOnceProject builds main.nomi and its siblings as one project and runs
// CheckTypes over every user file, in the order given ("" is the entry). It
// answers each file's errors by file name ("main.nomi" for the entry).
func checkOnceProject(t *testing.T, files map[string]string, order []string) map[string][]analysis.TypeError {
	t.Helper()
	tmp := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(files["main.nomi"]))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	lib := std.Load()
	fa, siblings, siblingNodes := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	out := map[string][]analysis.TypeError{}
	out["main.nomi"] = withoutUnreadParams(fa.TypeErrors)
	for _, key := range order {
		if key == "" {
			out["main.nomi"] = append(out["main.nomi"], withoutUnreadParams(analysis.CheckTypes(fa, entryNodes))...)
			continue
		}
		sib, ok := siblings[key]
		if !ok {
			t.Fatalf("no sibling %q; have %v", key, keysOf(siblings))
		}
		out[key+".nomi"] = withoutUnreadParams(analysis.CheckTypes(sib, siblingNodes[key]))
	}
	return out
}

func keysOf(m map[string]*analysis.FileAnalysis) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func messages(errs []analysis.TypeError) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Message
	}
	return out
}

const onceLib = `pub enum Direction {
    North
    South
}

pub struct Limits {
    low: Int
}

impl Limits {
    pub once top = 10

    pub once next = Limits.top + 1
}

pub once help = ["a", "b"]

pub once directions = [Direction.North, Direction.South]

pub once doubled = help |> Iter.map(|s| s + s) |> Iter.to_list()

pub once pairs = [(Limits.next, help)]
`

const onceMain = `import {
    lib
    lib.{help, directions, doubled, pairs, Direction, Limits}
}

struct Box {
    items: List<String>
}

fn tuple(n: Int): (Int, List<String>) {
    (n, help)
}

fn listed(): List<List<String>> {
    [help]
}

fn boxed(): Box {
    Box { items: help }
}

fn count(xs: List<String>): Int {
    Iter.count(xs)
}

fn argument(): Int {
    count(help)
}

fn arm(b: Bool): (Int, List<String>) {
    case b {
        True -> (1, help)
        False -> (0, lib.help)
    }
}

fn keyed(): Map<String, List<String>> {
    {"k" => help}
}

fn name(d: Direction): String {
    case d {
        Direction.North -> "n"
        Direction.South -> "s"
    }
}

fn names(): List<String> {
    directions |> Iter.map(name) |> Iter.to_list()
}

fn chained(): (List<String>, Int, Int, List<(Int, List<String>)>) {
    (doubled, Limits.top, Limits.next, pairs)
}

fn later(): (Int, List<String>) {
    local
}

once local = (Limits.top, help)

fn main() {
    _ = (tuple(1), listed(), boxed(), argument(), arm(True), keyed(), names(), chained(), later())
}
`

// Every composite position over another file's unannotated `once`, and over
// one declared later in the same file, type-checks whichever file is checked
// first.
func TestOnceType_InferredOnceIsTypedAtEveryRead(t *testing.T) {
	files := map[string]string{"main.nomi": onceMain, "lib.nomi": onceLib}
	for _, order := range [][]string{{"", "lib"}, {"lib", ""}} {
		got := checkOnceProject(t, files, order)
		for file, errs := range got {
			if len(errs) > 0 {
				t.Errorf("order %v: %s: unexpected errors:\n%s", order, file, strings.Join(messages(errs), "\n"))
			}
		}
	}
}

// A read used to get no type at all, which a bare return let through. It is
// the value's type now, so a wrong use is rejected.
func TestOnceType_WrongUseOfInferredOnceIsRejected(t *testing.T) {
	src := `import lib.help

fn wrong(): Int {
    help
}

fn main() {
    _ = wrong()
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src, "lib.nomi": onceLib}, []string{"", "lib"})
	errs := got["main.nomi"]
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "expected Int, got List<String>") {
		t.Fatalf("want one return mismatch naming List<String>, got:\n%s", strings.Join(messages(errs), "\n"))
	}
}

// A file-qualified read (`lib.doubled`) is the first read of a `once` built
// from another `once`, as stack-machine's `collatz.program` is.
func TestOnceType_FileQualifiedReadIsTyped(t *testing.T) {
	src := `import lib

fn first(): (Int, List<String>) {
    (1, lib.doubled)
}

fn main() {
    _ = first()
}
`
	for _, order := range [][]string{{"", "lib"}, {"lib", ""}} {
		got := checkOnceProject(t, map[string]string{"main.nomi": src, "lib.nomi": onceLib}, order)
		for file, errs := range got {
			if len(errs) > 0 {
				t.Errorf("order %v: %s: unexpected errors:\n%s", order, file, strings.Join(messages(errs), "\n"))
			}
		}
	}
}

// An owner-level `once` read in an attached test above it, and through its
// owner from another file, as std/int's `Int.max_value` is.
func TestOnceType_OwnerLevelOnceIsTypedAboveItsDeclaration(t *testing.T) {
	lib := `pub struct Limits {
    low: Int
}

impl Limits {
    //! assert Limits.top == 10
    //
    pub once top = 10

    pub once bottom = 0 - Limits.top
}
`
	src := `import lib.Limits

fn span(): Int {
    Limits.top - Limits.bottom
}

fn main() {
    _ = span()
}
`
	for _, order := range [][]string{{"", "lib"}, {"lib", ""}} {
		got := checkOnceProject(t, map[string]string{"main.nomi": src, "lib.nomi": lib}, order)
		for file, errs := range got {
			if len(errs) > 0 {
				t.Errorf("order %v: %s: unexpected errors:\n%s", order, file, strings.Join(messages(errs), "\n"))
			}
		}
	}
}

// Unannotated onces whose values read each other have no type to infer. Each
// is rejected at its declaration with the cycle, instead of the read getting
// an unsolved T.
func TestOnceType_CycleIsRejectedAtEachDeclaration(t *testing.T) {
	a := `import b.second

pub once first = [second]

once selfish = [selfish]
`
	b := `import a.first

pub once second = [first]
`
	src := `import a.first

fn main() {
    _ = first
}
`
	files := map[string]string{"main.nomi": src, "a.nomi": a, "b.nomi": b}
	want := map[string][]string{
		"a.nomi": {
			"once 'first' has no type annotation and its value depends on its own type (first → second → first)",
			"once 'selfish' has no type annotation and its value depends on its own type (selfish → selfish)",
		},
		"b.nomi": {
			"once 'second' has no type annotation and its value depends on its own type (second → first → second)",
		},
	}
	for _, order := range [][]string{{"", "a", "b"}, {"b", "a", ""}} {
		got := checkOnceProject(t, files, order)
		for file, prefixes := range want {
			errs := got[file]
			if len(errs) != len(prefixes) {
				t.Errorf("order %v: %s: want %d errors, got:\n%s", order, file, len(prefixes), strings.Join(messages(errs), "\n"))
				continue
			}
			for i, p := range prefixes {
				if !strings.HasPrefix(errs[i].Message, p) {
					t.Errorf("order %v: %s: error %d = %q, want prefix %q", order, file, i, errs[i].Message, p)
				}
			}
		}
		if errs := got["main.nomi"]; len(errs) > 0 {
			t.Errorf("order %v: main.nomi: unexpected errors:\n%s", order, strings.Join(messages(errs), "\n"))
		}
	}
}
