package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// hintsFor analyzes src and returns the hints a whole-file request with
// settings answers.
func hintsFor(t *testing.T, src string, settings inlayHintSettings) []InlayHint {
	t.Helper()
	lib := std.Load()
	nodes, errs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "/tmp", nil)
	analysis.CheckTypes(fa, nodes)
	req := hintRequest{
		settings: settings,
		content:  src,
		tokens:   func() *lexedText { return lex(src) },
	}
	return req.collect(fa, nodes)
}

// pipeHints renders the pipe-stage hints a request with settings answers
// as "line:col label", 1-based.
func pipeHints(t *testing.T, src string, settings inlayHintSettings) []string {
	t.Helper()
	var out []string
	for _, h := range hintsFor(t, src, settings) {
		if *h.Kind == InlayHintKindParameter || strings.HasPrefix(h.Label, ": ") {
			continue
		}
		out = append(out, fmt.Sprintf("%d:%d %s", h.Position.Line+1, h.Position.Character+1, h.Label))
	}
	return out
}

// wantPipeHints checks the pipe-stage hints with only that kind enabled.
func wantPipeHints(t *testing.T, src string, want ...string) {
	t.Helper()
	wantPipeHintsWith(t, src, inlayHintSettings{PipeTypes: true}, want...)
}

func wantPipeHintsWith(t *testing.T, src string, settings inlayHintSettings, want ...string) {
	t.Helper()
	got := pipeHints(t, src, settings)
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Errorf("pipe hints:\n got  %q\n want %q", got, want)
	}
}

func TestPipeHints_Binding(t *testing.T) {
	src := `struct User {
    name: String
    active?: Bool
}

fn names(users: List<User>): List<String> {
    active = users
    |> Iter.filter(.active?)    // the live ones
    |> Iter.map(.name)
    |> Iter.to_list()
    active
}
`
	wantPipeHints(t, src,
		"8:29 Iter<User>",
		"9:23 Iter<String>",
		"10:22 List<String>",
	)
}

// A pipeline a function's body ends with leaves its last stage's type to
// the declared return type. One that ends the body some other way, or a
// function without a written return type, keeps it.
func TestPipeHints_FunctionTail(t *testing.T) {
	src := `fn total(xs: List<Int>): Int {
    xs
    |> Iter.map(|x| x * 2)
    |> Iter.reduce(0, |a, b| a + b)
}

fn count(xs: List<Int>): String {
    n = xs
    |> Iter.map(|x| x * 2)
    |> Iter.count()
    Int.to_string(n)
}

fn main() {
    [1, 2]
    |> Iter.map(|x| x * 2)
    |> Iter.count()
}
`
	wantPipeHints(t, src,
		"3:27 Iter<Int>",
		"9:27 Iter<Int>",
		"10:20 Int",
		"16:27 Iter<Int>",
		"17:20 Int",
	)
}

// A call head ending on its own line gets its type; a name head does not.
func TestPipeHints_Head(t *testing.T) {
	src := `fn upto(n: Int): List<Int> {
    [n, n + 1]
}

fn evens(n: Int): List<Int> {
    upto(n)
    |> Iter.filter(|x| x % 2 == 0)
    |> Iter.to_list()
}

fn odds(ns: List<Int>): List<Int> {
    ns
    |> Iter.filter(|x| x % 2 == 1)
    |> Iter.to_list()
}
`
	wantPipeHints(t, src,
		"6:12 List<Int>",
		"7:35 Iter<Int>",
		"13:35 Iter<Int>",
	)
}

// A pipeline inside a lambda stage's block gets its own hints, and the
// outer stage's hint goes at the end of the line that closes the lambda.
func TestPipeHints_Nested(t *testing.T) {
	src := `fn lengths(groups: List<List<String>>): List<Int> {
    groups
    |> Iter.map(|g| {
        g
        |> Iter.map(String.length)
        |> Iter.count()
    })
    |> Iter.to_list()
}
`
	wantPipeHints(t, src,
		"5:35 Iter<Int>",
		"6:24 Int",
		"7:7 Iter<Int>",
	)
}

// A generic stage shows its solved type arguments.
func TestPipeHints_Generic(t *testing.T) {
	src := `fn pair<A, B>(a: A, b: B): (A, B) {
    (a, b)
}

fn tagged(xs: List<Int>): List<(Int, String)> {
    xs
    |> Iter.map(|x| pair(x, Int.to_string(x)))
    |> Iter.to_list()
}
`
	wantPipeHints(t, src,
		"7:47 Iter<(Int, String)>",
	)
}

// `|> try` shows the unwrapped value; `try` before a stage's call is one
// stage line and shows the same.
func TestPipeHints_Try(t *testing.T) {
	src := `fn parse(s: String): Result<Int, String> {
    case String.to_int(s) {
        Some(n) -> Ok(n)
        None -> Err("bad")
    }
}

fn doubled(s: String): Result<Int, String> {
    n = s
    |> parse()
    |> try
    m = s
    |> try parse()
    Ok(n + m)
}
`
	wantPipeHints(t, src,
		"10:15 Result<Int, String>",
		"11:11 Int",
		"13:19 Int",
	)
}

// A stage the checker could not type gets no hint; the stages it could
// keep theirs.
func TestPipeHints_UnknownType(t *testing.T) {
	src := `fn f(xs: List<Int>): Int {
    xs
    |> Iter.map(|x| x * 2)
    |> no_such_function()
}
`
	wantPipeHints(t, src, "3:27 Iter<Int>")
}

// A pipe on one line, or one whose only line break is inside a stage,
// gets none.
func TestPipeHints_OneLine(t *testing.T) {
	src := `fn f(xs: List<Int>): List<Int> {
    ys = xs |> Iter.map(|x| x + 1) |> Iter.to_list()
    xs |> Iter.map(|x| {
        x + 1
    }) |> Iter.to_list()
}
`
	wantPipeHints(t, src)
}

// A long type is cut, and the whole one is the tooltip.
func TestPipeHints_LongTypeIsShortened(t *testing.T) {
	src := `fn f(xs: List<Int>): List<((Int, Int), (Int, Int), (Int, Int))> {
    ys = xs
    |> Iter.map(|x| ((x, x), (x, x), (x, x)))
    |> Iter.to_list()
    ys
}
`
	hints := hintsFor(t, src, inlayHintSettings{PipeTypes: true})
	if len(hints) != 2 {
		t.Fatalf("want 2 hints, got %+v", hints)
	}
	h := hints[1]
	full := "List<((Int, Int), (Int, Int), (Int, Int))>"
	if h.Tooltip != full {
		t.Errorf("tooltip %q, want %q", h.Tooltip, full)
	}
	if n := len([]rune(h.Label)); n != maxPipeHintLen || !strings.HasSuffix(h.Label, "…") {
		t.Errorf("label %q (%d characters), want %d ending in …", h.Label, n, maxPipeHintLen)
	}
}

// Each kind of hint has its own setting.
func TestInlayHintSettings_EachKind(t *testing.T) {
	// Parameter hints: `x:` and `y:` on add's arguments, `f:` on the lambda.
	// Binding hints: n, m and the lambda's v.
	src := `fn add(x: Int, y: Int): Int { x + y }

fn main() {
    n = add(1, 2)
    m = [n]
    |> Iter.map(|v| v + 1)
    |> Iter.to_list()
}
`
	kinds := func(s inlayHintSettings) (params, bindings, pipes int) {
		for _, h := range hintsFor(t, src, s) {
			switch {
			case *h.Kind == InlayHintKindParameter:
				params++
			case strings.HasPrefix(h.Label, ": "):
				bindings++
			default:
				pipes++
			}
		}
		return
	}
	for _, tc := range []struct {
		s                       inlayHintSettings
		params, bindings, pipes int
	}{
		// m's binding hint shows List<Int>, so its last stage does not.
		{defaultInlayHintSettings, 3, 3, 1},
		{inlayHintSettings{PipeTypes: true}, 0, 0, 2},
		{inlayHintSettings{ParameterNames: true}, 3, 0, 0},
		{inlayHintSettings{BindingTypes: true}, 0, 3, 0},
		{inlayHintSettings{}, 0, 0, 0},
	} {
		p, b, pp := kinds(tc.s)
		if p != tc.params || b != tc.bindings || pp != tc.pipes {
			t.Errorf("%+v: got %d parameter, %d binding, %d pipe hints; want %d, %d, %d",
				tc.s, p, b, pp, tc.params, tc.bindings, tc.pipes)
		}
	}
}

// The settings arrive as initializationOptions and as
// workspace/didChangeConfiguration's settings, bare or under `nomi`, and a
// field left out keeps its value.
func TestInlayHintSettings_FromClient(t *testing.T) {
	s := NewServer()
	if got := s.settings.inlayHints(); got != defaultInlayHintSettings {
		t.Fatalf("defaults: %+v", got)
	}
	if _, err := s.initialize(&glsp.Context{}, &protocol.InitializeParams{
		InitializationOptions: map[string]any{
			"inlayHints": map[string]any{"parameterNames": false},
		},
	}); err != nil {
		t.Fatal(err)
	}
	want := inlayHintSettings{ParameterNames: false, BindingTypes: true, PipeTypes: true}
	if got := s.settings.inlayHints(); got != want {
		t.Errorf("after initializationOptions: %+v, want %+v", got, want)
	}
	if err := s.workspaceDidChangeConfiguration(nil, &protocol.DidChangeConfigurationParams{
		Settings: map[string]any{
			"nomi": map[string]any{"inlayHints": map[string]any{"pipeTypes": false, "bindingTypes": false}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	want = inlayHintSettings{}
	if got := s.settings.inlayHints(); got != want {
		t.Errorf("after didChangeConfiguration: %+v, want %+v", got, want)
	}
	s.settings.apply("not an object")
	if got := s.settings.inlayHints(); got != want {
		t.Errorf("a malformed value changed the settings: %+v", got)
	}
}

// A hint request over a few lines of a large file reads the analysis it
// already has: it analyzes nothing, locates only the pipelines in the
// range, and lexes the text once however often it is asked.
func TestPipeHints_RangedRequestOnLargeFile(t *testing.T) {
	var src strings.Builder
	for i := range 400 {
		fmt.Fprintf(&src, "fn f%d(xs: List<Int>): List<Int> {\n    xs\n    |> Iter.map(|x| x + %d)\n    |> Iter.to_list()\n}\n\n", i, i)
	}
	dir := writeProject(t, map[string]string{"main.nomi": src.String()})
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.IndexWorkspace(context.Background())
	uri := "file://" + filepath.Join(dir, "main.nomi")
	s.docs.Open(uri, src.String())
	before := s.docs.Snapshot(uri)
	if before == nil || before.Analysis == nil {
		t.Fatal("no analysis")
	}

	// Lines 601..612 (0-based 600..611) hold f100 and f101.
	params := &InlayHintParams{}
	params.TextDocument.URI = uri
	params.Range.Start.Line = 600
	params.Range.End.Line = 611
	var hints []InlayHint
	for range 3 {
		var err error
		hints, err = s.textDocumentInlayHint(nil, params)
		if err != nil {
			t.Fatal(err)
		}
	}
	var pipes []string
	for _, h := range hints {
		if !strings.HasPrefix(h.Label, ": ") && *h.Kind == InlayHintKindType {
			pipes = append(pipes, fmt.Sprintf("%d %s", h.Position.Line, h.Label))
		}
	}
	want := []string{"602 Iter<Int>", "608 Iter<Int>"}
	if !reflect.DeepEqual(pipes, want) {
		t.Errorf("pipe hints %q, want %q", pipes, want)
	}
	after := s.docs.Snapshot(uri)
	if after.Analysis != before.Analysis || after.AnalyzedVersion != before.AnalyzedVersion {
		t.Error("the hint request analyzed the file again")
	}
	if s.pipeTokens.lexes != 1 {
		t.Errorf("lexed %d times, want once", s.pipeTokens.lexes)
	}

	// The collector locates the two pipelines in range, not all 400.
	c := hintRequest{
		settings:  defaultInlayHintSettings,
		content:   after.Content,
		tokens:    func() *lexedText { return s.pipeTokens.get(uri, after.Content) },
		startLine: 601,
		endLine:   612,
	}.run(after.Analysis, after.Nodes)
	if c.pipelinesMeasured != 2 {
		t.Errorf("located %d pipelines, want 2", c.pipelinesMeasured)
	}
}

// Pipelines in impl functions, `once` bindings and tests get hints too.
func TestPipeHints_ImplOnceAndTest(t *testing.T) {
	src := `struct Bag {
    items: List<Int>
}

impl Bag {
    fn doubled(b: Bag): List<Int> {
        b.items
        |> Iter.map(|x| x * 2)
        |> Iter.to_list()
    }
}

once evens = [1, 2, 3]
|> Iter.filter(|x| x % 2 == 0)
|> Iter.to_list()

test "sum" {
    total = [1, 2]
    |> Iter.map(|x| x + 1)
    |> Iter.count()
    assert total == 2
}
`
	wantPipeHints(t, src,
		"8:31 Iter<Int>",
		"14:31 Iter<Int>",
		"15:18 List<Int>",
		"19:27 Iter<Int>",
		"20:20 Int",
	)
}

// A generic stage shows its instantiated type (`Iter<Int>` after a
// List<Int> head). Inside a generic function a stage's type may name the
// function's own parameter, and is shown as written.
func TestPipeHints_TypeParameters(t *testing.T) {
	src := `fn evens(n: Int): List<Int> {
    [0, n]
    |> Iter.filter(|x| x % 2 == 0)
    |> Iter.to_list()
}

fn firsts<T>(xs: List<T>): List<T> {
    xs
    |> Iter.take(1)
    |> Iter.to_list()
}
`
	wantPipeHints(t, src,
		"3:35 Iter<Int>",
		"9:20 Iter<T>",
	)
}

// A stage line shows its type only when it differs from the line above's.
// The first stage compares with the head's type even when the head's hint
// is not shown (a name or a literal head).
func TestPipeHints_OnlyNewTypes(t *testing.T) {
	src := `fn double(n: Int): Int { n * 2 }

fn add(n: Int, m: Int): Int { n + m }

fn keep(xs: List<Int>): List<Int> { xs }

fn upto(n: Int): List<Int> { [n, n + 1] }

fn main() {
    a = 5
    |> double()
    |> add(1)
    b = [1, 2]
    |> keep()
    |> Iter.map(|x| x + 1)
    |> Iter.filter(|x| x > 2)
    |> Iter.to_list()
    c = upto(1)
    |> keep()
    d = [1]
    |> Iter.map(|x| x + 1)
    |> dbg
    |> Iter.to_list()
    io.print("${a} ${b} ${c} ${d}")
}
`
	wantPipeHints(t, src,
		"15:27 Iter<Int>",
		"17:22 List<Int>",
		"18:16 List<Int>",
		"21:27 Iter<Int>",
		"23:22 List<Int>",
	)
}

// The motivating case: an annotated binding over stages that keep the
// head's type shows nothing.
func TestPipeHints_SameTypeThroughout(t *testing.T) {
	src := `fn double(n: Int): Int { n * 2 }

fn add(n: Int, m: Int): Int { n + m }

fn main() {
    result: Int = 5
    |> double()
    |> add(1)
    io.print("${result}")
}
`
	wantPipeHints(t, src)
	wantPipeHintsWith(t, src, defaultInlayHintSettings)
}

// The last stage's type is left off when the binding the pipeline
// initializes already shows it: a written annotation, or the binding-type
// hint when that setting is on. With it off, the stage's hint is the only
// place the type shows, and it stays.
func TestPipeHints_BindingShowsLastType(t *testing.T) {
	src := `struct User {
    name: String
    active?: Bool
}

once evens: List<Int> = [1, 2, 3]
|> Iter.filter(|x| x % 2 == 0)
|> Iter.to_list()

fn names(users: List<User>): List<String> {
    written: List<String> = users
    |> Iter.filter(.active?)
    |> Iter.map(.name)
    |> Iter.to_list()
    inferred = users
    |> Iter.filter(.active?)
    |> Iter.map(.name)
    |> Iter.to_list()
    List.concat(written, inferred)
}
`
	wantPipeHints(t, src,
		"7:31 Iter<Int>",
		"12:29 Iter<User>",
		"13:23 Iter<String>",
		"16:29 Iter<User>",
		"17:23 Iter<String>",
		"18:22 List<String>",
	)
	wantPipeHintsWith(t, src, inlayHintSettings{PipeTypes: true, BindingTypes: true},
		"7:31 Iter<Int>",
		"12:29 Iter<User>",
		"13:23 Iter<String>",
		"16:29 Iter<User>",
		"17:23 Iter<String>",
	)
}
