package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// infiniteIterErrors is the infinite-iterator errors the checker reports
// for src, as `line:col message`.
func infiniteIterErrors(t *testing.T, src string) []string {
	t.Helper()
	_, errs := checkSourceWithStdlib(src)
	var out []string
	for _, e := range errs {
		if e.Code == analysis.InfiniteIterCode {
			out = append(out, e.Message)
			continue
		}
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		t.Fatalf("the checker rejects this for another reason: %v\n%s", e, src)
	}
	return out
}

func infiniteMessage(terminal, source string) string {
	return "`" + terminal + "` of an infinite iterator (`" + source + "`) never finishes; limit it with `Iter.take` or `Iter.take_while`"
}

// A terminal that must reach the end of a source the checker can see never
// ends is an error at the terminal, worded as the runtime's trap.
func TestInfiniteIter_Rejected(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		terminal, source string
	}{
		// The maintainer's example.
		{"repeat piped", "coin = 5\n    _ = coin\n    |> Iter.repeat()\n    |> Iter.to_list()", "Iter.to_list", "Iter.repeat"},
		// Each source.
		{"repeat", "_ = Iter.repeat(1) |> Iter.to_list()", "Iter.to_list", "Iter.repeat"},
		{"from", "_ = Iter.from(0) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"iterate", "_ = Iter.iterate(1, &(&1 * 2)) |> Iter.to_list()", "Iter.to_list", "Iter.iterate"},
		{"iterate lambda", "_ = Iter.iterate(1, fn n -> n + 1) |> Iter.count()", "Iter.count", "Iter.iterate"},
		{"Range.from", "_ = Range.from(1) |> Iter.to_list()", "Iter.to_list", "Range.from"},
		{"Range.from a negative start", "_ = Range.from(-1) |> Iter.to_list()", "Iter.to_list", "Range.from"},
		{"Range.from a large negative start", "_ = Range.from(-9_223_372_036_854_775_807) |> Iter.count()", "Iter.count", "Range.from"},
		{"Range.naturals", "_ = Range.naturals() |> Iter.count()", "Iter.count", "Range.naturals"},
		{"Range.step_by", "_ = Range.from(1) |> Range.step_by(2) |> Iter.to_list()", "Iter.to_list", "Range.step_by"},
		{"Range.step_by down", "_ = Range.naturals() |> Range.step_by(-3) |> Iter.to_list()", "Iter.to_list", "Range.step_by"},
		{"Range.step_by down from a negative start", "_ = Range.from(-5) |> Range.step_by(-2) |> Iter.to_list()", "Iter.to_list", "Range.step_by"},
		{"Range.step_by up from a negative start", "_ = Range.from(-5) |> Range.step_by(2) |> Iter.to_list()", "Iter.to_list", "Range.step_by"},
		{"cycle of a list literal", "_ = [1, 2] |> Iter.cycle() |> Iter.to_list()", "Iter.to_list", "Iter.cycle"},
		{"cycle of a vector literal", "_ = #[1] |> Iter.cycle() |> Iter.to_list()", "Iter.to_list", "Iter.cycle"},
		{"cycle of a set literal", "_ = %[1] |> Iter.cycle() |> Iter.to_list()", "Iter.to_list", "Iter.cycle"},
		{"cycle of a map literal", "_ = %{1 => 2} |> Iter.cycle() |> Iter.to_list()", "Iter.to_list", "Iter.cycle"},
		{"cycle of an infinite source", "_ = Iter.from(0) |> Iter.cycle() |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		// Each keeping adapter.
		{"map", "_ = Iter.from(0) |> Iter.map(&(&1 * 2)) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"filter", "_ = Iter.from(0) |> Iter.filter(fn x -> x > 3) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"drop", "_ = Iter.from(0) |> Iter.drop(2) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"drop_while", "_ = Iter.from(0) |> Iter.drop_while(&(&1 < 10)) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"with_index", "_ = Iter.from(0) |> Iter.with_index() |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"scan", "_ = Iter.from(0) |> Iter.scan(fn total = 0, x -> total + x) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"chunk_by", "_ = Iter.from(0) |> Iter.chunk_by(&(&1 / 4)) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"flat_map", "_ = Iter.from(0) |> Iter.flat_map(fn x -> [x, x]) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"chunks", "_ = Iter.from(0) |> Iter.chunks(3) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"concat right", "_ = Iter.concat([1, 2], Iter.repeat(3)) |> Iter.to_list()", "Iter.to_list", "Iter.repeat"},
		{"concat left", "_ = Iter.from(0) |> Iter.concat([1]) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"zip of two", "_ = Iter.zip(Iter.from(0), Iter.repeat(\"a\")) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		{"a long chain", "_ = Iter.from(0)\n    |> Iter.map(&(&1 * 2))\n    |> Iter.filter(&(&1 > 3))\n    |> Iter.with_index()\n    |> Iter.map(fn #(i, _) -> i)\n    |> Iter.chunks(2)\n    |> Iter.flat_map(fn chunk -> chunk)\n    |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		// Each terminal.
		{"to_vector", "_ = Iter.from(0) |> Iter.to_vector()", "Iter.to_vector", "Iter.from"},
		{"to_set", "_ = Iter.from(0) |> Iter.to_set()", "Iter.to_set", "Iter.from"},
		{"to_map", "_ = Iter.from(0) |> Iter.map(fn x -> #(x, x)) |> Iter.to_map()", "Iter.to_map", "Iter.from"},
		{"count", "_ = Iter.repeat(1) |> Iter.count()", "Iter.count", "Iter.repeat"},
		{"last", "_ = Iter.from(0) |> Iter.last()", "Iter.last", "Iter.from"},
		{"reverse", "_ = Iter.from(0) |> Iter.reverse()", "Iter.reverse", "Iter.from"},
		{"flatten", "_ = Iter.repeat([1]) |> Iter.flatten()", "Iter.flatten", "Iter.repeat"},
		{"sort", "_ = Iter.from(0) |> Iter.sort()", "Iter.sort", "Iter.from"},
		{"sort_by", "_ = Iter.from(0) |> Iter.sort_by(fn x -> 0 - x)", "Iter.sort_by", "Iter.from"},
		{"sort_with", "_ = Iter.from(0) |> Iter.sort_with(&Int.compare(&1, &2))", "Iter.sort_with", "Iter.from"},
		{"partition", "_ = Iter.from(0) |> Iter.partition(fn x -> x > 1)", "Iter.partition", "Iter.from"},
		{"group_by", "_ = Iter.from(0) |> Iter.group_by(fn x -> x % 2)", "Iter.group_by", "Iter.from"},
		{"frequencies", "_ = Iter.from(0) |> Iter.frequencies()", "Iter.frequencies", "Iter.from"},
		{"min", "_ = Iter.from(0) |> Iter.min()", "Iter.min", "Iter.from"},
		{"max", "_ = Iter.repeat(1) |> Iter.max()", "Iter.max", "Iter.repeat"},
		{"min_by", "_ = Iter.repeat(1) |> Iter.min_by(fn x -> x)", "Iter.min_by", "Iter.repeat"},
		{"max_by", "_ = Iter.repeat(1) |> Iter.max_by(fn x -> x)", "Iter.max_by", "Iter.repeat"},
		{"sum", "_ = Iter.from(0) |> Iter.sum()", "Iter.sum", "Iter.from"},
		{"reduce", "_ = Iter.repeat(1) |> Iter.reduce(fn total = 0, x -> total + x)", "Iter.reduce", "Iter.repeat"},
		{"each", "Iter.repeat(1) |> Iter.each(fn _x -> Unit)", "Iter.each", "Iter.repeat"},
		{"map_reduce", "_ = Iter.from(0) |> Iter.map_reduce(fn s = 0, x -> #(x, s + x))", "Iter.map_reduce", "Iter.from"},
		{"String.join", "_ = Iter.repeat(\"a\") |> String.join(\", \")", "String.join", "Iter.repeat"},
		{"Maybe.values", "_ = Iter.repeat(Maybe.Some(1)) |> Maybe.values()", "Maybe.values", "Iter.repeat"},
		{"Result.partition", "ok: Result<Int, String> = Result.Ok(1)\n    _ = Iter.repeat(ok) |> Result.partition()", "Result.partition", "Iter.repeat"},
		// Nested calls and named arguments.
		{"nested", "_ = Iter.to_list(Iter.map(Iter.from(0), &(&1 + 1)))", "Iter.to_list", "Iter.from"},
		{"nested zip", "_ = Iter.count(Iter.zip(Iter.from(0), Range.naturals()))", "Iter.count", "Iter.from"},
		{"named separator", "_ = String.join(Iter.repeat(\"a\"), separator: \",\")", "String.join", "Iter.repeat"},
		{"parenthesized", "_ = (Iter.from(0) |> Iter.map(&(&1 + 1))) |> Iter.to_list()", "Iter.to_list", "Iter.from"},
		// Local bindings.
		{"one binding", "xs = Iter.repeat(5)\n    _ = xs |> Iter.to_list()", "Iter.to_list", "Iter.repeat"},
		{"two bindings", "xs = Iter.from(0)\n    ys = xs |> Iter.map(&(&1 * 2))\n    _ = ys |> Iter.sum()", "Iter.sum", "Iter.from"},
		{"a binding inside a lambda's body", "_ = [1]\n    |> Iter.map(fn n -> {\n        xs = Iter.repeat(n)\n        xs |> Iter.to_list()\n    })\n    |> Iter.to_list()", "Iter.to_list", "Iter.repeat"},
		{"a shadowed binding", "xs = [1, 2]\n    xs = Iter.cycle(xs)\n    xs = Iter.cycle([1])\n    _ = Iter.to_list(xs)", "Iter.to_list", "Iter.cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main() {\n    " + tc.body + "\n}\n"
			got := infiniteIterErrors(t, src)
			want := infiniteMessage(tc.terminal, tc.source)
			if len(got) != 1 || got[0] != want {
				t.Fatalf("errors = %q, want exactly [%q]\n%s", got, want, src)
			}
		})
	}
}

// The error is at the terminal, with the source as related information.
func TestInfiniteIter_Positions(t *testing.T) {
	src := "fn main() {\n    xs = Iter.repeat(5)\n    _ = xs\n    |> Iter.map(&(&1 + 1))\n    |> Iter.to_list()\n}\n"
	_, errs := checkSourceWithStdlib(src)
	var found *analysis.TypeError
	for i := range errs {
		if errs[i].Code == analysis.InfiniteIterCode {
			found = &errs[i]
		}
	}
	if found == nil {
		t.Fatalf("no infinite-iterator error: %v", errs)
	}
	if found.Line != 5 || found.Col != 8 || found.EndCol != 20 {
		t.Errorf("error at %d:%d-%d, want 5:8-20", found.Line, found.Col, found.EndCol)
	}
	if len(found.Related) != 1 || found.Related[0].Line != 2 || found.Related[0].Col != 10 || found.Related[0].Message != "`Iter.repeat` never ends" {
		t.Errorf("related = %+v, want `Iter.repeat` never ends at 2:10", found.Related)
	}
}

// A pipeline that may end, or one the checker cannot see, is accepted; the
// runtime still traps where it can tell.
func TestInfiniteIter_Accepted(t *testing.T) {
	for name, src := range map[string]string{
		"take":                          "fn main() {\n    _ = Iter.repeat(1) |> Iter.take(3) |> Iter.to_list()\n}\n",
		"take_while":                    "fn main() {\n    _ = Iter.from(0) |> Iter.take_while(&(&1 < 3)) |> Iter.sum()\n}\n",
		"take then map":                 "fn main() {\n    _ = Iter.from(0) |> Iter.take(3) |> Iter.map(&(&1 * 2)) |> Iter.to_list()\n}\n",
		"map that can break":            "fn main() {\n    _ = Iter.from(0)\n    |> Iter.map(fn x -> {\n        if x > 2 {\n            break x\n        } else {\n            x\n        }\n    })\n    |> Iter.to_list()\n}\n",
		"filter that continues":         "fn main() {\n    _ = Iter.from(0)\n    |> Iter.filter(fn x -> {\n        if x > 2 {\n            continue\n        } else {\n            True\n        }\n    })\n    |> Iter.to_list()\n}\n",
		"iterate that can break":        "fn main() {\n    _ = Iter.iterate(0, fn n -> {\n        if n > 2 {\n            break n\n        } else {\n            n + 1\n        }\n    })\n    |> Iter.to_list()\n}\n",
		"reduce that can break":         "fn main() {\n    _ = Iter.reduce(Iter.from(0), fn total = 0, x -> {\n        if x > 4 {\n            break total\n        } else {\n            total + x\n        }\n    })\n}\n",
		"each that can break":           "fn main() {\n    Iter.from(0)\n    |> Iter.each(fn x -> {\n        if x > 4 {\n            break\n        } else {\n            Unit\n        }\n    })\n}\n",
		"a sensitive callback":          "fn stop(x: Int) -> Int {\n    if x > 2 {\n        break x\n    } else {\n        x\n    }\n}\n\nfn main() {\n    _ = Iter.from(0) |> Iter.map(stop) |> Iter.to_list()\n}\n",
		"find":                          "fn main() {\n    _ = Iter.find(Iter.from(0), &(&1 > 3))\n}\n",
		"first":                         "fn main() {\n    _ = Iter.repeat(1) |> Iter.first()\n}\n",
		"any?":                          "fn main() {\n    _ = Iter.from(0) |> Iter.any?(&(&1 == 7))\n}\n",
		"all?":                          "fn main() {\n    _ = Iter.from(0) |> Iter.all?(&(&1 < 7))\n}\n",
		"contains?":                     "fn main() {\n    _ = Iter.from(0) |> Iter.contains?(1_000)\n}\n",
		"get":                           "fn main() {\n    _ = Iter.from(0) |> Iter.get(4)\n}\n",
		"empty?":                        "fn main() {\n    _ = Iter.from(0) |> Iter.empty?()\n}\n",
		"each_while":                    "fn main() {\n    _ = Iter.each_while(Iter.from(0), &(&1 < 3))\n}\n",
		"infinite?":                     "fn main() {\n    _ = Iter.from(0) |> Iter.infinite?()\n}\n",
		"Maybe.collect":                 "fn main() {\n    _ = Range.naturals() |> Iter.map(fn n -> if n < 3 { Maybe.Some(n) } else { Maybe.None }) |> Maybe.collect()\n}\n",
		"a parameter":                   "fn drain(xs: Iter<Int>) -> List<Int> {\n    xs |> Iter.to_list()\n}\n\nfn main() {\n    _ = drain(Iter.repeat(1))\n}\n",
		"a function result":             "fn ones() -> Iter<Int> {\n    Iter.repeat(1)\n}\n\nfn main() {\n    _ = ones() |> Iter.to_list()\n}\n",
		"a once":                        "once ones = Iter.repeat(1)\n\nfn main() {\n    _ = ones |> Iter.to_list()\n}\n",
		"a field":                       "struct Holder {\n    xs: Iter<Int>\n}\n\nfn main() {\n    h = #Holder{xs: Iter.repeat(1)}\n    _ = h.xs |> Iter.to_list()\n}\n",
		"a Range near max_value":        "fn main() {\n    _ = Range.from(Int.max_value - 1) |> Iter.to_list()\n}\n",
		"a Range literal near max":      "fn main() {\n    _ = Range.from(9_223_372_000_000_000_000) |> Iter.to_list()\n}\n",
		"a Range from a variable":       "fn main() {\n    n = 3\n    _ = Range.from(n) |> Iter.take(2) |> Iter.to_list()\n}\n",
		"a Range from a negative":       "fn main() {\n    _ = Range.from(-1) |> Iter.take(2) |> Iter.to_list()\n}\n",
		"step_by down near min_value":   "fn main() {\n    _ = Range.from(-9_223_372_036_854_775_000) |> Range.step_by(-1) |> Iter.to_list()\n}\n",
		"step_by that does not move":    "fn main() {\n    _ = Range.from(1) |> Range.step_by(0) |> Iter.to_list()\n}\n",
		"step_by of a bounded range":    "fn main() {\n    _ = 1..10 |> Range.step_by(2) |> Iter.to_list()\n}\n",
		"chunks of zero":                "fn main() {\n    _ = Iter.from(0) |> Iter.chunks(0) |> Iter.to_list()\n}\n",
		"chunks of a variable":          "fn main() {\n    n = 2\n    _ = Iter.from(0) |> Iter.chunks(n) |> Iter.take(1) |> Iter.to_list()\n}\n",
		"cycle of an empty list":        "fn main() {\n    xs: List<Int> = []\n    _ = [] |> Iter.cycle() |> Iter.to_list()\n    _ = xs\n}\n",
		"cycle of a filter":             "fn main() {\n    _ = [1, 2] |> Iter.filter(&(&1 > 1)) |> Iter.cycle() |> Iter.take(2) |> Iter.to_list()\n}\n",
		"cycle of a string":             "fn main() {\n    _ = \"ab\" |> Iter.cycle() |> Iter.take(2) |> Iter.to_list()\n}\n",
		"zip with a finite side":        "fn main() {\n    _ = Iter.zip(Iter.from(0), [\"a\", \"b\"]) |> Iter.to_list()\n}\n",
		"concat of two finite":          "fn main() {\n    _ = Iter.concat([1], [2]) |> Iter.to_list()\n}\n",
		"flat_map of an infinite inner": "fn main() {\n    _ = [1, 2] |> Iter.flat_map(fn x -> Iter.repeat(x)) |> Iter.take(3) |> Iter.to_list()\n}\n",
		"a binding later rebound":       "fn main() {\n    xs = Iter.repeat(1)\n    xs = xs |> Iter.take(2)\n    _ = xs |> Iter.to_list()\n}\n",
		"a user Iter owner":             "struct Box {\n    n: Int\n}\n\nimpl Box {\n    fn repeat(n: Int) -> List<Int> {\n        [n]\n    }\n}\n\nfn main() {\n    _ = Box.repeat(1) |> Iter.to_list()\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := infiniteIterErrors(t, src); len(got) > 0 {
				t.Fatalf("the checker rejects this: %q\n%s", got, src)
			}
		})
	}
}

// The terminals outside std/iter that take their source after another
// argument, or in a `concurrent` block, and a callee reached through an
// import alias or a selective import.
func TestInfiniteIter_OtherSpellings(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"Supervisor.spawn_all",
			"import std/supervisors.Supervisor\n\nfn start(sup: Supervisor) {\n    _ = Supervisor.spawn_all(sup, Iter.repeat(1), fn _x -> Unit)\n}\n",
			infiniteMessage("Supervisor.spawn_all", "Iter.repeat"),
		},
		{
			"Supervisor.spawn_all piped",
			"import std/supervisors.Supervisor\n\nfn start(sup: Supervisor) {\n    _ = sup |> Supervisor.spawn_all(Iter.from(0), fn _x -> Unit)\n}\n",
			infiniteMessage("Supervisor.spawn_all", "Iter.from"),
		},
		{
			"Task.spawn_all",
			"import std/tasks.Task\n\nfn main() {\n    _ = concurrent {\n        Iter.repeat(1) |> Task.spawn_all(&(&1 * 2), max_running: 2) |> Task.await_all()\n    }\n}\n",
			infiniteMessage("Task.spawn_all", "Iter.repeat"),
		},
		{
			"selective import",
			"import std/iter.Iter.{repeat, to_list}\n\nfn main() {\n    _ = repeat(1) |> to_list()\n}\n",
			infiniteMessage("Iter.to_list", "Iter.repeat"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := infiniteIterErrors(t, tc.src)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("errors = %q, want exactly [%q]\n%s", got, tc.want, tc.src)
			}
		})
	}
}

// The error reaches `nomi check`'s diagnostics from a test body too.
func TestInfiniteIter_InATest(t *testing.T) {
	got := infiniteIterErrors(t, "test \"t\" {\n    assert Iter.repeat(1) |> Iter.to_list() == []\n}\n")
	if len(got) != 1 || !strings.Contains(got[0], "`Iter.to_list` of an infinite iterator") {
		t.Fatalf("errors = %q", got)
	}
}
