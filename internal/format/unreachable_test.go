package format

import "testing"

// `unreachable` keeps its reason's source form, as `todo` does, and a guard
// whose else is one sits with its neighbours.
func TestFormat_Unreachable(t *testing.T) {
	src := "fn index(n: Int, ns: List<Int>) -> Int {\n" +
		"    Some(i) = ns |> Iter.find_index(&(&1 == n)) else {   unreachable    \"every number is listed\" }\n" +
		"    i\n" +
		"}\n\n" +
		"fn first(xs: List<String>) -> String {\n" +
		"    case xs {\n" +
		"        [x, .._] -> x\n" +
		"        [] ->    unreachable `raw ${reason}`\n" +
		"    }\n" +
		"}\n"
	want := "fn index(n: Int, ns: List<Int>) -> Int {\n" +
		"    Some(i) = ns |> Iter.find_index(&(&1 == n)) else {\n" +
		"        unreachable \"every number is listed\"\n" +
		"    }\n" +
		"    i\n" +
		"}\n\n" +
		"fn first(xs: List<String>) -> String {\n" +
		"    case xs {\n" +
		"        [x, .._] -> x\n" +
		"        [] -> unreachable `raw ${reason}`\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}
