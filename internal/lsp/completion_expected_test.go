package lsp

import (
	"slices"
	"strings"
	"testing"
)

const colorDecls = `enum Color {
    Red
    Green
}

enum Size {
    Small
    Large
}

struct Brush {
    color: Color
    width: Int
}

fn paint(c: Color, times: Int = 1): Int {
    times
}

fn mix(n: Int, c: Color): Int {
    n
}
`

func TestCompletion_DotVariantUsesTheExpectedEnum(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"function tail", "fn f(): Color {\n    ." + cursorMark + "\n}\n"},
		{"return", "fn f(b: Bool): Color {\n    if b {\n        return ." + cursorMark + "\n    }\n    .Red\n}\n"},
		{"call argument", "fn f(): Int {\n    paint(." + cursorMark + ")\n}\n"},
		{"call argument, typed and recorded", "fn f(): Int {\n    paint(.Re" + cursorMark + ")\n}\n"},
		{"named argument", "fn f(): Int {\n    paint(c: ." + cursorMark + ")\n}\n"},
		{"pipe stage argument", "fn f(): Int {\n    3 |> mix(." + cursorMark + ")\n}\n"},
		{"annotated binding", "fn f(): Int {\n    x: Color = ." + cursorMark + "\n    1\n}\n"},
		{"struct literal field", "fn f(): Brush {\n    Brush{color: ." + cursorMark + ", width: 1}\n}\n"},
		{"equality", "fn f(c: Color): Bool {\n    c == ." + cursorMark + "\n}\n"},
		{"case arm pattern", "fn f(c: Color): Int {\n    case c {\n        ." + cursorMark + "\n    }\n}\n"},
		{"case arm body", "fn f(s: Size): Color {\n    case s {\n        .Small -> ." + cursorMark + "\n        .Large -> .Green\n    }\n}\n"},
		{"if branch", "fn f(b: Bool): Color {\n    if b {\n        ." + cursorMark + "\n    } else {\n        .Green\n    }\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, colorDecls+tt.src)
			labelsInclude(t, items, "Red", "Green")
			labelsExclude(t, items, "Small", "Large", "Some", "None", "Ok", "True")
		})
	}
}

func TestCompletion_DotVariantWithoutExpectedTypeOffersAll(t *testing.T) {
	items := complete(t, colorDecls+"fn f(): Int {\n    x = ."+cursorMark+"\n    1\n}\n")
	labelsInclude(t, items, "Red", "Green", "Small", "Large", "Some", "None")
}

func TestCompletion_ClockVariants(t *testing.T) {
	items := complete(t, "tests \"g\" {\n    clock ."+cursorMark+"\n}\n")
	got := itemLabels(items)
	slices.Sort(got)
	if strings.Join(got, ",") != "System,Virtual" {
		t.Fatalf("clock variants = %v, want System and Virtual", got)
	}
}

func TestCompletion_RanksExpectedTypeFirst(t *testing.T) {
	src := colorDecls + `fn label(): String {
    "x"
}

fn count(): Int {
    1
}

fn f(): Int {
    word = "a"
    other = 2
    n: Int = ` + cursorMark + `
    n
}
`
	items := complete(t, src)
	labelsBefore(t, items, "other", "word")
	labelsBefore(t, items, "count", "label")
	labelsBefore(t, items, "count", "word")
	// Fit outranks locality: a file-level function returning Int beats a
	// local String.
	labelsBefore(t, items, "mix", "word")
}

func TestCompletion_RanksLocalsBeforeFileNamesBeforeImports(t *testing.T) {
	src := "fn total_file(): Int {\n    1\n}\n\nfn f(): Int {\n    total_local = 2\n    tota" + cursorMark + "\n}\n"
	items := complete(t, src)
	labelsBefore(t, items, "total_local", "total_file")
}

func TestCompletion_FunctionValueIsNotCalled(t *testing.T) {
	src := "fn double(n: Int): Int {\n    n * 2\n}\n\nfn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(dou" + cursorMark + ") |> Iter.to_list()\n}\n"
	it := mustItem(t, completeWith(t, true, src), "double")
	if te := textEditOf(t, it); te.NewText != "double" {
		t.Fatalf("a function passed as a value was inserted as %q", te.NewText)
	}
}
