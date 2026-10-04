package analysis_test

import "testing"

// An interface method that declares self only in its result takes self from
// the expected type at the call, not from the enclosing function's return
// type, which names something else here. The return type used to win and
// solved self as Int, which reported `expected List<Int>, got Int`.
func TestReturnOnlySelf_SolvedFromTheExpectedType(t *testing.T) {
	src := `import std/json.{FromJson, Json}

fn count(parsed: Json): Result<Int, Json.ShapeError> {
  ids: List<Int> = try FromJson.from_json(parsed)
  Ok(Iter.count(ids))
}`
	expectClean(t, checkWithStdlib(src))
}
