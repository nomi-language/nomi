package vmhost

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// markedSpans is the source text of each place in file that the editor's
// lowering of the program marks (Reached.Marked; an editor draws bold the
// token that ends there), as `line:text`, in position order, each once.
func markedSpans(t *testing.T, p *Program, dir, file string) []string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(file))
	var spans []span
	seen := map[span]bool{}
	for _, r := range p.reachedCalls(p.editorWalks(p.walks(p.checkedRoots()))) {
		s := span{r.Path, r.Line, r.Col, r.EndLine, r.EndCol}
		if !r.Marked || !samePathForTest(r.Path, path) || seen[s] {
			continue
		}
		seen[s] = true
		spans = append(spans, s)
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].line < spans[j].line || spans[i].line == spans[j].line && spans[i].col < spans[j].col
	})
	out := []string{}
	for _, s := range spans {
		out = append(out, fmt.Sprintf("%d:%s", s.line, p.spanText(s)))
	}
	return out
}

// marksCase is one program of the marks tests: the places marked in its
// main.nomi, and the last line of a hover at the first occurrence of each
// needle ("" for none).
type marksCase struct {
	src    string
	marked []string
	hovers map[string]string
}

func (tc marksCase) run(t *testing.T) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"main.nomi": tc.src,
		"nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	if got := markedSpans(t, p, dir, "main.nomi"); !slices.Equal(got, tc.marked) {
		t.Errorf("the marked places are %q, want %q", got, tc.marked)
	}
	for _, needle := range slices.Sorted(maps.Keys(tc.hovers)) {
		got := hoverReach(t, p, dir, "main.nomi", needle, 0)
		last := ""
		if len(got) > 0 {
			last = got[len(got)-1]
		}
		if last != tc.hovers[needle] {
			t.Errorf("a hover on %q ends with %q, want %q", needle, last, tc.hovers[needle])
		}
	}
}

const (
	heldLine = "Has effects on what it is passed."
	ioLine   = "Has effects: `std::io`."
)

// A call is marked when the compiler cannot show it is pure, and a
// function when its body holds such a call, so the mark climbs every
// caller up to main; a chain of pure helpers stays plain.
func TestEffectMarks_Chains(t *testing.T) {
	marksCase{
		src: `import std::io

fn double(n: Int) -> Int
    n * 2
end

fn quad(n: Int) -> Int
    double(double(n))
end

fn level3()
    io.print("deep")
end

fn level2()
    level3()
end

fn level1()
    level2()
end

fn main()
    _ = quad(List.length([1, 2]))
    level1()
end
`,
		marked: []string{"11:level3", "12:io.print", "15:level2", "16:level3", "19:level1", "20:level2", "23:main", "25:level1"},
		hovers: map[string]string{
			"quad(List": "", "double(n)": "", "length": "",
			"level1()": "Has effects: `std::io` through `level2`, `level3`.",
			"level3()": ioLine,
		},
	}.run(t)
}

// marksStores has one implementation of UserStore that is pure and, with
// console, one that acts.
func marksStores(console bool) string {
	src := `import std::io

interface UserStore
    fn find(store: self, id: Int) -> String
end

struct Memory
    rows: Map<Int, String>
end

impl UserStore for Memory
    fn find(store: Memory, id: Int) -> String
        Map.get(store.rows, id) |> Option.with_default("")
    end
end

fn lookup(store: UserStore, id: Int) -> String
    UserStore.find(store, id)
end

fn main()
    _ = lookup(Memory{rows: {1 => "a"}}, 1)
`
	if console {
		src += `    _ = lookup(Console{prefix: "id "}, 2)
end

struct Console
    prefix: String
end

impl UserStore for Console
    fn find(store: Console, id: Int) -> String
        io.print("${store.prefix}${id}")
        ""
    end
end
`
	} else {
		src = strings.Replace(src, "import std::io\n\n", "", 1) + "end\n"
	}
	return src
}

// A call through an interface value counts every implementation the
// program has: with one that acts it may act, and is marked, and with only
// pure ones it is plain.
func TestEffectMarks_ThroughAnInterface(t *testing.T) {
	t.Run("one implementation acts", func(t *testing.T) {
		marksCase{
			src:    marksStores(true),
			marked: []string{"17:lookup", "18:UserStore.find", "21:main", "22:lookup", "23:lookup", "31:find", "32:io.print"},
			hovers: map[string]string{
				"UserStore.find(store": "May have effects: `UserStore.find` is not pure in every implementation.",
				"lookup(store":         "Has effects: `std::io` through `UserStore.find for Console`.",
				"find(store: Memory":   "",
			},
		}.run(t)
	})
	t.Run("every implementation is pure", func(t *testing.T) {
		marksCase{
			src:    marksStores(false),
			marked: []string{},
			hovers: map[string]string{"UserStore.find(store": "", "lookup(Memory": ""},
		}.run(t)
	})
}

// A call that is passed a function value whose body acts may run it, and
// is marked; the function named as a value is no call, and a call passed
// only pure functions is plain.
func TestEffectMarks_FunctionValues(t *testing.T) {
	marksCase{
		src: `import std::io

fn main()
    xs = ["a", "b"]
    Iter.each(xs, io.print)
    ys = Iter.map([1, 2], fn x -> x * 2) |> Iter.to_list()
    _ = List.length(ys)
end
`,
		marked: []string{"3:main", "5:Iter.each"},
		hovers: map[string]string{
			"Iter.each": heldLine,
			"print)":    ioLine,
			"Iter.map":  "", "to_list": "",
		},
	}.run(t)
}

// A handle in an app field, in a `once` or passed in a test acts where it
// is used: the calls that use it are marked, and the reads of it are not.
// The call in a once's initializer is marked as any call is; the once's
// name is not. A function nothing calls (draft) is marked by the same
// rule, from the walk of what the program's roots leave.
func TestEffectMarks_Handles(t *testing.T) {
	marksCase{
		src: `import
    std::fs
    std::io.{self, Error, Writer}
end

struct App
    out: Writer
end

fn boot() -> Result<App, String>
    Ok(App{out: io.stderr})
end

once log: Writer = fs.create("log.txt") |> Result.with_default(io.stderr)

fn report(to: Writer, count: Int) -> Result<Unit, Error>
    to |> Writer.print("${count} items")
end

fn shout(text: String)
    _ = Writer.print(App.out, text)
end

fn note(text: String)
    _ = Writer.print(log, text)
end

fn draft(to: Writer)
    _ = Writer.print(to, "draft")
end

fn main()
    shout("hi")
    note("hi")
end

test "report writes the count"
    (_, written) = Writer.collect(fn w -> report(w, 3))
    assert Bytes.to_string(written) == Ok("3 items\n")
end
`,
		marked: []string{"14:fs.create", "16:report", "17:Writer.print", "20:shout", "21:Writer.print", "24:note", "25:Writer.print",
			"28:draft", "29:Writer.print", "32:main", "33:shout", "34:note", "38:Writer.collect", "38:report"},
		hovers: map[string]string{
			"draft(to":           heldLine,
			"report(w":           heldLine,
			"Writer.collect":     ioLine,
			"App.out":            "",
			"out, text":          "",
			"log, text":          "Has effects: `std::fs`, `std::io`.",
			"log: Writer":        "Has effects: `std::fs`, `std::io`.",
			"fs.create":          "Has effects: `std::fs`.",
			"shout(\"hi\")":      "Uses `App.out`.",
			"Writer.print(App.":  heldLine,
			"Writer.print(log, ": heldLine,
		},
	}.run(t)
}

// A `dbg` is the call that prints: its hover says so, in a function main
// reaches, in its pipe form, in a function nothing calls, in a generic
// function nothing instantiates and in a test. Its keyword is not marked,
// since it shows the effect on its line, and the function that holds it,
// and each call of that function, are marked as a call's holder is.
// An `assert`, a `refute`, a `todo` and an `unreachable` stop the program
// and act on nothing, so they stay plain, and a `defer` is not the call it
// defers: the deferred call's callee is marked, as any call's is.
func TestEffectMarks_ADbg(t *testing.T) {
	marksCase{
		src: `import std::io

fn double(n: Int) -> Int
    n * 2 |> dbg
end

fn draft(n: Int) -> Int
    dbg n
end

fn first<T>(xs: List<T>) -> Option<T>
    dbg xs
    List.first(xs)
end

fn later(n: Int) -> Int
    if n > 0 then
        n
    else
        todo
    end
end

fn main()
    x = dbg double(2)
    _ = later(x)
end

fn sign(n: Int) -> Int
    if n >= 0 then
        1
    else
        unreachable "a count is never below zero"
    end
end

fn noisy()
    defer io.print("bye")
    io.print("hi")
end

test "double doubles"
    assert dbg double(2) == 4
    refute sign(1) == 0
end
`,
		marked: []string{"3:double", "7:draft", "11:first", "24:main", "25:double",
			"37:noisy", "38:io.print", "39:io.print", "43:double"},
		hovers: map[string]string{
			"dbg\n":         "Has effects: `dbg`.",
			"dbg n":         "Has effects: `dbg`.",
			"dbg xs":        "Has effects: `dbg`.",
			"dbg double(2)": "Has effects: `dbg`.",
			"todo":          "",
			"unreachable":   "",
			"assert":        "",
			"refute":        "",
			"defer":         "",
		},
	}.run(t)
}
