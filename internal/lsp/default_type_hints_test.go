package lsp

import (
	"strings"
	"testing"
)

// A field whose type comes from its default shows that type as an inlay
// hint after its name, and a hover on it names the type.
func TestInlayHintFieldTypeFromDefault(t *testing.T) {
	src := `struct Game {
    word: String
    misses = 0
    ratio = 1.5
}

enum Shape {
    Circle{radius = 1.0}
}

fn main() {
}`
	hints := filterByKind(getInlayHints(t, src), InlayHintKindType)
	var labels []string
	for _, h := range hints {
		labels = append(labels, h.Label)
	}
	want := []string{": Int", ": Float", ": Float"}
	if len(labels) != len(want) {
		t.Fatalf("type hints = %q, want %q", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("type hints = %q, want %q", labels, want)
		}
	}
	// `misses` is on line 3 (0-based 2), the hint after the name.
	if hints[0].Position.Line != 2 || hints[0].Position.Character != 10 {
		t.Errorf("misses hint at %+v, want line 2 character 10", hints[0].Position)
	}
}

func TestHoverFieldTypeFromDefault(t *testing.T) {
	got := hoverAtMark(t, "struct Game {\n    word: String\n    ▮misses = 0\n}\n")
	want := "```nomi\nmisses: Int\n```"
	if got != want {
		t.Errorf("hover = %q, want %q", got, want)
	}
}

// A parameter shows its annotation as written, and the struct it names
// prints with its trailing defaults left out; the declaration shows its
// defaults.
func TestHoverDefaultTypeArguments(t *testing.T) {
	src := "struct Tagged<T, Tag = String> {\n    value: T\n    tag: Tag\n}\n\nfn label(t: Tagged<Int, String>) -> String {\n    ▮t.tag\n}\n"
	want := "```nomi\nt: Tagged<Int, String>\n\nstruct Tagged<Int> {\n    value: Int\n    tag: String\n}\n```"
	if got := hoverAtMark(t, src); got != want {
		t.Errorf("hover = %q, want %q", got, want)
	}
	decl := "struct ▮Tagged<T, Tag = String> {\n    value: T\n    tag: Tag\n}\n"
	if got := hoverAtMark(t, decl); !strings.Contains(got, "struct Tagged<T, Tag = String>") {
		t.Errorf("declaration hover = %q, want it to show the default", got)
	}
}
