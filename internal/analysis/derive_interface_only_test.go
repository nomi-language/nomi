package analysis_test

import (
	"strings"
	"testing"
)

// TestDerive_NeedsOnlyTheInterfaceInScope pins that a file deriving an
// interface needs nothing in scope beyond the interface itself. The five
// prelude interfaces need no import at all (re-importing a prelude name is an
// error); ToJson and FromJson need only their own name from std/json, although
// the code they synthesize builds and matches `Json` and `Json.ShapeError`.
// Each row also covers the shapes whose synthesized bodies differ: a struct,
// an enum and a distinct type.
func TestDerive_NeedsOnlyTheInterfaceInScope(t *testing.T) {
	const types = `
struct P {
  a: Int
  b: String
}

enum Shape {
  Dot
  Circle(Int)
  Pair(Int, Int)
  Box{w: Int, h: Int}
}

type Meters Int
`
	cases := []struct {
		iface   string
		imports string
		targets []string
	}{
		{"Equatable", "", []string{"P", "Shape", "Meters"}},
		{"Hashable", "", []string{"P", "Shape", "Meters"}},
		{"Comparable", "", []string{"P", "Shape", "Meters"}},
		{"Debug", "", []string{"P", "Shape", "Meters"}},
		{"Display", "", []string{"P", "Shape", "Meters"}},
		{"ToJson", "; std/json.ToJson", []string{"P", "Shape", "Meters"}},
		// FromJson refuses an enum target by design (validateDeriveTarget).
		{"FromJson", "; std/json.FromJson", []string{"P", "Meters"}},
	}
	for _, tc := range cases {
		t.Run(tc.iface, func(t *testing.T) {
			var src strings.Builder
			src.WriteString("import { std/io" + tc.imports + " }\n")
			src.WriteString(types)
			for _, target := range tc.targets {
				src.WriteString("\nderive " + tc.iface + " for " + target + "\n")
			}
			src.WriteString("\nfn main() { io.print(\"x\") }\n")
			if errs := buildAndCollectTypeErrors(t, src.String()); len(errs) != 0 {
				t.Fatalf("derive %s with only the interface in scope is rejected:\n%v\nsource:\n%s", tc.iface, errs, src.String())
			}
		})
	}
}

// TestDerive_JsonImportUsedOnlyByDerivedCodeIsUnused pins the other half: a
// `Json` import that nothing but derived code needs is reported unused, since
// derived code does not reach `Json` through it.
func TestDerive_JsonImportUsedOnlyByDerivedCodeIsUnused(t *testing.T) {
	src := `import { std/io; std/json.{FromJson, Json, ToJson} }

struct P { a: Int }

derive ToJson for P
derive FromJson for P

fn main() { io.print("x") }
`
	errs := buildAndCollectTypeErrors(t, src)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "imported name 'Json' is unused") {
		t.Fatalf("want exactly the unused-import diagnostic for Json, got %v", errs)
	}
}
