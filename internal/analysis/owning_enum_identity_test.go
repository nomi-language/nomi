package analysis_test

import (
	"strings"
	"testing"
)

// A variant imported on its own (`import a.Json.{Obj}`) belongs to the enum
// that declares that variant, never to another enum that happens to share
// the enum's name. Here b.nomi also declares a `Json`, imported under its
// own name or an alias. The owning enum used to be looked up by name, so a
// bare `%Obj{...}` pattern took b's `Json` as the owner and reported that it
// has no map payload.
func TestOwningEnumOfAnImportedVariantIsTheEnumThatDeclaresIt(t *testing.T) {
	const a = `pub enum Json {
    Obj Map<String, Int>
    Num Int
}
`
	for _, tc := range []struct{ name, b, imports string }{
		{"b's Json lacks the variant, imported by name", "pub enum Json {\n    Num Int\n}\n", "import b.Json"},
		{"b's Json lacks the variant, imported under an alias", "pub enum Json {\n    Num Int\n}\n", "import b.{Json as BJson}"},
		{"b's Json has the variant with another payload, imported under an alias", "pub enum Json {\n    Obj Map<String, String>\n}\n", "import b.{Json as BJson}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "import a\nimport a.Json.{Obj}\n" + tc.imports + `

fn size(j: a.Json) -> Int {
    case j {
        %Obj{"k" => v} -> v
        _ -> 0
    }
}

fn main() {
    _ = size(%Obj{"k" => 2})
}
`
			_, errs := checkProjectWithFiles(t, src, map[string]string{"a.nomi": a, "b.nomi": tc.b})
			var msgs []string
			for _, e := range errs {
				if strings.Contains(e.Message, "is never read") || strings.Contains(e.Message, "is unused") {
					continue
				}
				msgs = append(msgs, e.Error())
			}
			if len(msgs) > 0 {
				t.Fatalf("expected no errors, got:\n  %s\nsource:\n%s", strings.Join(msgs, "\n  "), src)
			}
		})
	}
}
