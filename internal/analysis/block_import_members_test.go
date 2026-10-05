package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// blockAndFileImport answers the same body under the import written at the
// top of `fn main`'s block and at file level. The body starts on line 3 in
// both, so a diagnostic in it has the same position either way.
func blockAndFileImport(imp, body string) (block, file string) {
	block = "fn main() {\n  import " + imp + "\n" + body + "\n  Unit\n}\n"
	file = "import " + imp + "\nfn main() {\n" + body + "\n  Unit\n}\n"
	return block, file
}

func memberError(errs []analysis.TypeError, want string) (analysis.TypeError, bool) {
	for _, e := range errs {
		if e.Message == want {
			return e, true
		}
	}
	return analysis.TypeError{}, false
}

func errorTexts(errs []analysis.TypeError) string {
	var got []string
	for _, e := range errs {
		got = append(got, diagText(e))
	}
	return strings.Join(got, "\n  ")
}

// A member a block-imported type does not have is rejected with the message
// and position the same use gets under a file-level import: a variant
// constructor, a nullary variant, an owner function, a nested type, a
// variant of a type named through a whole-file import, an aliased import and
// an interface's function. Before, the checker did not know the type inside
// the block and let the use through to the IR builder, which reported "this
// call to `Json.Number` is not supported yet".
func TestBlockImport_UnknownMemberRejectedAsAtFileLevel(t *testing.T) {
	cases := []struct {
		name, imp, body, want string
	}{
		{"variant call", "std/json.Json", "  x = Json.Number(1.5)\n  dbg x", "type 'Json' has no member 'Number'"},
		{"nullary variant", "std/json.Json", "  x = Json.Nope\n  dbg x", "type 'Json' has no member 'Nope'"},
		{"owner function", "std/json.Json", "  x = Json.nope(\"1\")\n  dbg x", "type 'Json' has no member 'nope'"},
		{"nested type", "std/json.Json", "  x = Json.Nope.Snake\n  dbg x", "type 'Json' has no member 'Nope'"},
		{"file-qualified type", "std/json", "  x = json.Json.Number(1.0)\n  dbg x", "type 'json.Json' has no member 'Number'"},
		{"aliased type", "std/json.Json as J", "  x = J.Number(1.0)\n  dbg x", "type 'J' has no member 'Number'"},
		{"interface", "std/json.ToJson", "  dbg ToJson.nope(1)", "type 'ToJson' has no member 'nope'"},
		{"owner function of Duration", "std/duration.Duration", "  dbg Duration.nope(1)", "type 'Duration' has no member 'nope'"},
		{"record variant field", "std/json.Json", "  x = Json.ShapeError{path: [], nope: 1}\n  dbg x", "Json.ShapeError has no field 'nope'"},
		{"variant pattern", "std/json.Json", "  x = Json.Null\n  y = case x {\n    Json.Number(n) -> n\n    _ -> 2.0\n  }\n  dbg y", "variant Number not found in enum Json"},
		{"dot variant pattern", "std/json.Json", "  x = Json.Null\n  y = case x {\n    .Nope -> 1\n    _ -> 2\n  }\n  dbg y", "variant Nope not found in enum Json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blockSrc, fileSrc := blockAndFileImport(tc.imp, tc.body)
			_, fileErrs := checkSourceWithStdlib(fileSrc)
			at, ok := memberError(fileErrs, tc.want)
			if !ok {
				t.Fatalf("the file-level import no longer reports %q, so there is nothing to match:\n%s\ngot:\n  %s", tc.want, fileSrc, errorTexts(fileErrs))
			}
			_, blockErrs := checkSourceWithStdlib(blockSrc)
			if len(blockErrs) == 0 {
				t.Fatalf("the front end admits the block-imported use:\n%s", blockSrc)
			}
			got, ok := memberError(blockErrs, tc.want)
			if !ok {
				t.Fatalf("want %q, got:\n  %s", tc.want, errorTexts(blockErrs))
			}
			if got.Line != at.Line || got.Col != at.Col {
				t.Errorf("block import reports at %d:%d, file import at %d:%d", got.Line, got.Col, at.Line, at.Col)
			}
		})
	}
}

// Valid uses of a block-imported type check clean: its variants, owner
// functions, a type annotation naming it, a file-qualified type and an
// interface function.
func TestBlockImport_ValidMembersAccepted(t *testing.T) {
	cases := []struct {
		name, imp, body string
	}{
		{"variant", "std/json.Json", "  x = Json.Arr([Json.Int(1), Json.Null])\n  dbg x"},
		{"annotation", "std/json.Json", "  x: Json = Json.Float(1.5)\n  dbg x"},
		{"generic annotation", "std/json.Json", "  x: List<Json> = [Json.Null]\n  dbg x"},
		{"owner function", "std/json.Json", "  dbg Json.decode(\"[1]\")"},
		{"pattern", "std/json.Json", "  x = Json.Null\n  y = case x {\n    Json.Float(n) -> n\n    .Null -> 0.0\n    _ -> 2.0\n  }\n  dbg y"},
		{"file-qualified type", "std/json", "  x: json.Json = json.Json.Null\n  dbg x"},
		{"aliased type", "std/json.Json as J", "  x: J = J.Null\n  dbg x"},
		{"interface", "std/json.ToJson", "  dbg ToJson.to_json(1)"},
		{"owner function of Duration", "std/duration.Duration", "  d: Duration = Duration.seconds(1)\n  dbg d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blockSrc, _ := blockAndFileImport(tc.imp, tc.body)
			if _, errs := checkSourceWithStdlib(blockSrc); len(errs) != 0 {
				t.Fatalf("rejected:\n%s\ngot:\n  %s", blockSrc, errorTexts(errs))
			}
		})
	}
}

// A block import binds only inside its block: another function of the file
// does not see the type.
func TestBlockImport_TypeStaysInsideItsBlock(t *testing.T) {
	src := `fn f(): Unit {
  import std/json.Json
  dbg Json.Null
  Unit
}

fn main() {
  x: Json = Json.Null
  dbg x
  f()
}
`
	_, errs := checkSourceWithStdlib(src)
	if _, ok := memberError(errs, `unknown type "Json"`); !ok {
		t.Fatalf("want the other function's annotation rejected, got:\n  %s", errorTexts(errs))
	}
}
