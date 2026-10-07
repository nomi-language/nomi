package irbuild

import "testing"

func TestIRMapList_TourIndex(t *testing.T) {
	verifyLambdaProgram(t, `typealias Index Map<String, List<Int>>

fn record(index: Index, bucket: String, n: Int): Index {
  existing: List<Int> = case Map.get(index, bucket) {
    Some(xs) -> xs
    None -> []
  }
  Map.put(index, bucket, [n, ..existing])
}

fn main() {
  start: Index = Map.empty()
  result =
    start
    |> record("evens", 2)
    |> record("evens", 4)
    |> record("odds", 1)

  dbg result
}
`, "dbg line 19: result = {\"evens\" => [4, 2], \"odds\" => [1]}\n")
}

func TestIRMapList_DebugAndPersistence(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  counts = {"a\"q" => 1, "b" => 2}
  dbg counts
  lists = {"x" => [1, 2], "y" => [3]}
  more = Map.put(lists, "x", [9])
  dbg lists
  dbg more
  io.print(Map.size(more))
  empty: Map<String, Int> = Map.empty()
  dbg empty
  io.print("done")
}
`, "dbg line 4: counts = {\"a\\\"q\" => 1, \"b\" => 2}\n"+
		"dbg line 7: lists = {\"x\" => [1, 2], \"y\" => [3]}\n"+
		"dbg line 8: more = {\"x\" => [9], \"y\" => [3]}\n2\n"+
		"dbg line 11: empty = {=>}\ndone\n")
}
