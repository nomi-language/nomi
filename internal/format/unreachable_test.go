package format

import "testing"

// `unreachable` keeps its reason's source form, as `todo` does, and a guard
// whose else is one sits with its neighbours.
func TestFormat_Unreachable(t *testing.T) {
	src := "fn index(n: Int, ns: List<Int>) -> Int\n" +
		"    Some(i) = ns |> Iter.find_index(&(&1 == n)) else unreachable    \"every number is listed\"\n" +
		"    i\n" +
		"end\n" +
		"\n" +
		"fn first(xs: List<String>) -> String\n" +
		"    case xs\n" +
		"        [x, ..] -> x\n" +
		"        [] ->    unreachable `raw ${reason}`\n" +
		"    end\n" +
		"end\n"
	want := "fn index(n: Int, ns: List<Int>) -> Int\n" +
		"    Some(i) = ns |> Iter.find_index(&(&1 == n)) else unreachable \"every number is listed\"\n" +
		"    i\n" +
		"end\n" +
		"\n" +
		"fn first(xs: List<String>) -> String\n" +
		"    case xs\n" +
		"        [x, ..] -> x\n" +
		"        []      -> unreachable `raw ${reason}`\n" +
		"    end\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)
}
