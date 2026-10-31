package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An interface method that declares self only in its result takes self from
// the expected type at the call, not from the enclosing function's return
// type, which names something else here. The return type used to win and
// solved self as Int, which reported `expected List<Int>, got Int`.
func TestReturnOnlySelf_SolvedFromTheExpectedType(t *testing.T) {
	src := `import std/json.{FromJson, Json}

fn count(parsed: Json) -> Result<Int, Json.ShapeError> {
  ids: List<Int> = FromJson.from_json(parsed)?
  Ok(Iter.count(ids))
}`
	expectClean(t, checkWithStdlib(src))
}

// A return-only self the expected type seeds before the arguments are
// checked (an argument such as `Json.Null` takes the expected type, so the
// call is seeded from it) is still recorded as the call's return-only self,
// which is how the IR builder knows which impl the call names. Recorded as
// an argument-solved self instead, it carried no ReturnSelf, and the builder
// ran Int's impl where `Maybe<Int>`'s was asked for: "a value of kind Int
// where Maybe<Int> is expected".
func TestReturnOnlySelf_SeededFromTheExpectedTypeIsRecorded(t *testing.T) {
	bodies := map[string]string{
		"under ?":         "  _ = json\n  m: Maybe<Int> = FromJson.from_json(Json.Null)?\n  Ok(m)",
		"annotated":       "  _ = json\n  r: Result<Maybe<Int>, Json.ShapeError> = FromJson.from_json(Json.Null)\n  r",
		"argument solved": "  m: Maybe<Int> = FromJson.from_json(json)?\n  Ok(m)",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			src := "import std/json.{FromJson, Json}\n\nfn read(json: Json) -> Result<Maybe<Int>, Json.ShapeError> {\n" + body + "\n}\n"
			fa, errs := checkSourceWithStdlib(src)
			expectClean(t, errs)
			pos, found := analysis.Pos{}, false
			for i, line := range strings.Split(src, "\n") {
				if col := strings.Index(line, "from_json("); col >= 0 {
					pos, found = analysis.Pos{Line: i + 1, Col: col + 1}, true
					break
				}
			}
			if !found {
				t.Fatal("no from_json call in the source")
			}
			ref := fa.References[pos]
			if ref == nil {
				t.Fatalf("no reference at the call's from_json (%d:%d)", pos.Line, pos.Col)
			}
			if ref.ReturnSelf == nil || ref.ReturnSelf.String() != "Maybe<Int>" {
				t.Fatalf("the call's return-only self is %v, want Maybe<Int>", ref.ReturnSelf)
			}
		})
	}
}
