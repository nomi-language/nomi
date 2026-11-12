package lsp

import (
	"slices"
	"strings"
	"testing"
)

const colorDecls = `enum Color
    Red
    Green
end

enum Size
    Small
    Large
end

struct Brush
    color: Color
    width: Int
end

fn paint(c: Color, times: Int = 1) -> Int
    times
end

fn mix(n: Int, c: Color) -> Int
    n
end
`

func TestCompletion_DotVariantUsesTheExpectedEnum(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"function tail", "fn f() -> Color\n    ." + cursorMark + "\nend\n"},
		{"return", "fn f(b: Bool) -> Color\n    if b then\n        return ." + cursorMark + "\n    end\n    .Red\nend\n"},
		{"call argument", "fn f() -> Int\n    paint(." + cursorMark + ")\nend\n"},
		{"call argument, typed and recorded", "fn f() -> Int\n    paint(.Re" + cursorMark + ")\nend\n"},
		{"named argument", "fn f() -> Int\n    paint(c: ." + cursorMark + ")\nend\n"},
		{"pipe stage argument", "fn f() -> Int\n    3 |> mix(." + cursorMark + ")\nend\n"},
		{"annotated binding", "fn f() -> Int\n    x: Color = ." + cursorMark + "\n    1\nend\n"},
		{"struct literal field", "fn f() -> Brush\n    Brush{color: ." + cursorMark + ", width: 1}\nend\n"},
		{"equality", "fn f(c: Color) -> Bool\n    c == ." + cursorMark + "\nend\n"},
		{"case arm pattern", "fn f(c: Color) -> Int\n    case c\n        ." + cursorMark + "\n    end\nend\n"},
		{"case arm body", "fn f(s: Size) -> Color\n    case s\n        .Small -> ." + cursorMark + "\n        .Large -> .Green\n    end\nend\n"},
		{"if branch", "fn f(b: Bool) -> Color\n    if b then\n        ." + cursorMark + "\n    else\n        .Green\n    end\nend\n"},
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
	items := complete(t, colorDecls+"fn f() -> Int\n    x = ."+cursorMark+"\n    1\nend\n")
	labelsInclude(t, items, "Red", "Green", "Small", "Large", "Some", "None")
}

func TestCompletion_ClockVariants(t *testing.T) {
	items := complete(t, "group \"g\"\n    clock ."+cursorMark+"\nend\n")
	got := itemLabels(items)
	slices.Sort(got)
	if strings.Join(got, ",") != "System,Virtual" {
		t.Fatalf("clock variants = %v, want System and Virtual", got)
	}
}

func TestCompletion_RanksExpectedTypeFirst(t *testing.T) {
	src := colorDecls + `fn label() -> String
    "x"
end

fn count() -> Int
    1
end

fn f() -> Int
    word = "a"
    other = 2
    n: Int = ` + cursorMark + `
    n
end
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
	src := "fn total_file() -> Int\n    1\nend\n\nfn f() -> Int\n    total_local = 2\n    tota" + cursorMark + "\nend\n"
	items := complete(t, src)
	labelsBefore(t, items, "total_local", "total_file")
}

func TestCompletion_FunctionValueIsNotCalled(t *testing.T) {
	src := "fn double(n: Int) -> Int\n    n * 2\nend\n\nfn f(xs: List<Int>) -> List<Int>\n    xs |> Iter.map(dou" + cursorMark + ") |> Iter.to_list()\nend\n"
	it := mustItem(t, completeWith(t, true, src), "double")
	if te := textEditOf(t, it); te.NewText != "double" {
		t.Fatalf("a function passed as a value was inserted as %q", te.NewText)
	}
}
