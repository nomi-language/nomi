package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/vmhost"
)

// infiniteIterCase is one pipeline the agreement test runs both ways. expr
// writes it with each of its sources passed through src: as written, for
// the checker, and through `hide`, which the checker does not follow, for
// the runtime.
type infiniteIterCase struct {
	name string
	// fns names the std functions the case exercises, for the coverage
	// check.
	fns  []string
	expr func(src func(string) string) string
	// infinite is whether the runtime traps on it: a case that does not
	// say what it expects proves nothing.
	infinite bool
}

func pipe(source, rest string) func(func(string) string) string {
	return func(src func(string) string) string { return src(source) + rest }
}

// infiniteIterCases holds a case for every std function the checker's
// tables name and every function `impl Iter` declares in std/iter.nomi.
var infiniteIterCases = []infiniteIterCase{
	// Sources.
	{"repeat", []string{"Iter.repeat"}, pipe("Iter.repeat(1)", " |> Iter.to_list()"), true},
	{"from", []string{"Iter.from"}, pipe("Iter.from(0)", " |> Iter.to_list()"), true},
	{"iterate", []string{"Iter.iterate"}, pipe("Iter.iterate(1, &(&1 + 1))", " |> Iter.to_list()"), true},
	{"iterate that can break", []string{"Iter.iterate"}, pipe("Iter.iterate(0, fn n -> {\n        if n > 2 {\n            break n\n        } else {\n            n + 1\n        }\n    })", " |> Iter.to_list()"), false},
	{"Range.from", []string{"Range.from"}, pipe("Range.from(1)", " |> Iter.to_list()"), true},
	{"Range.from at the bound", []string{"Range.from"}, pipe("Range.from(9_223_090_561_878_065_151)", " |> Iter.to_list()"), true},
	{"Range.from past the bound", []string{"Range.from"}, pipe("Range.from(9_223_372_036_854_775_800)", " |> Iter.to_list()"), false},
	{"Range.from a negative start", []string{"Range.from"}, pipe("Range.from(-1)", " |> Iter.to_list()"), true},
	{"Range.from the least Int", []string{"Range.from"}, pipe("Range.from(-9_223_372_036_854_775_807)", " |> Iter.count()"), true},
	{"Range.naturals", []string{"Range.naturals"}, pipe("Range.naturals()", " |> Iter.count()"), true},
	{"Range.step_by", []string{"Range.step_by"}, pipe("Range.from(1) |> Range.step_by(2)", " |> Iter.to_list()"), true},
	{"Range.step_by down", []string{"Range.step_by"}, pipe("Range.naturals() |> Range.step_by(-3)", " |> Iter.to_list()"), true},
	{"Range.step_by down from a negative start", []string{"Range.step_by"}, pipe("Range.from(-5) |> Range.step_by(-2)", " |> Iter.to_list()"), true},
	{"Range.step_by up from a negative start", []string{"Range.step_by"}, pipe("Range.from(-5) |> Range.step_by(2)", " |> Iter.to_list()"), true},
	{"Range.step_by down near the least Int", []string{"Range.step_by"}, pipe("Range.from(-9_223_372_036_854_775_000) |> Range.step_by(-1)", " |> Iter.count()"), false},
	{"Range.step_by that does not move", []string{"Range.step_by"}, pipe("Range.from(1) |> Range.step_by(0)", " |> Iter.to_list()"), false},
	// Adapters, each between a hidden infinite source and a terminal.
	{"map", []string{"Iter.map"}, pipe("Iter.from(0)", " |> Iter.map(&(&1 * 2)) |> Iter.to_list()"), true},
	{"map that can break", []string{"Iter.map"}, pipe("Iter.from(0)", " |> Iter.map(fn x -> {\n        if x > 2 {\n            break x\n        } else {\n            x\n        }\n    }) |> Iter.to_list()"), false},
	{"filter", []string{"Iter.filter"}, pipe("Iter.from(0)", " |> Iter.filter(&(&1 > 3)) |> Iter.to_list()"), true},
	{"filter that can break", []string{"Iter.filter"}, pipe("Iter.from(0)", " |> Iter.filter(fn x -> {\n        if x > 2 {\n            break False\n        } else {\n            True\n        }\n    }) |> Iter.to_list()"), false},
	{"drop", []string{"Iter.drop"}, pipe("Iter.from(0)", " |> Iter.drop(2) |> Iter.to_list()"), true},
	{"drop_while", []string{"Iter.drop_while"}, pipe("Iter.from(0)", " |> Iter.drop_while(&(&1 < 10)) |> Iter.to_list()"), true},
	{"with_index", []string{"Iter.with_index"}, pipe("Iter.from(0)", " |> Iter.with_index() |> Iter.to_list()"), true},
	{"scan", []string{"Iter.scan"}, pipe("Iter.from(0)", " |> Iter.scan(fn total = 0, x -> total + x) |> Iter.to_list()"), true},
	{"chunk_by", []string{"Iter.chunk_by"}, pipe("Iter.from(0)", " |> Iter.chunk_by(&(&1 / 4)) |> Iter.to_list()"), true},
	{"flat_map", []string{"Iter.flat_map"}, pipe("Iter.from(0)", " |> Iter.flat_map(fn x -> [x, x]) |> Iter.to_list()"), true},
	{"chunks", []string{"Iter.chunks"}, pipe("Iter.from(0)", " |> Iter.chunks(3) |> Iter.to_list()"), true},
	{"chunks of zero", []string{"Iter.chunks"}, pipe("Iter.from(0)", " |> Iter.chunks(0) |> Iter.to_list()"), false},
	{"cycle of an infinite source", []string{"Iter.cycle"}, pipe("Iter.from(0)", " |> Iter.cycle() |> Iter.to_list()"), true},
	{"cycle of a list", []string{"Iter.cycle"}, pipe("[1, 2] |> Iter.cycle()", " |> Iter.to_list()"), true},
	{"cycle of a vector", []string{"Iter.cycle"}, pipe("#[1] |> Iter.cycle()", " |> Iter.to_list()"), true},
	{"cycle of a set", []string{"Iter.cycle"}, pipe("%[1] |> Iter.cycle()", " |> Iter.to_list()"), true},
	{"cycle of a map", []string{"Iter.cycle"}, pipe("%{1 => 2} |> Iter.cycle()", " |> Iter.to_list()"), true},
	{"cycle of an empty list", []string{"Iter.cycle"}, pipe("Iter.take([1], 0) |> Iter.to_list() |> Iter.cycle()", " |> Iter.to_list()"), false},
	{"concat", []string{"Iter.concat"}, func(src func(string) string) string {
		return "Iter.concat([1], " + src("Iter.from(0)") + ") |> Iter.to_list()"
	}, true},
	{"concat of two finite", []string{"Iter.concat"}, pipe("Iter.concat([1], [2])", " |> Iter.to_list()"), false},
	{"zip of two", []string{"Iter.zip"}, func(src func(string) string) string {
		return "Iter.zip(" + src("Iter.from(0)") + ", " + src("Iter.repeat(\"a\")") + ") |> Iter.to_list()"
	}, true},
	{"zip with a finite side", []string{"Iter.zip"}, func(src func(string) string) string {
		return "Iter.zip(" + src("Iter.from(0)") + ", [\"a\"]) |> Iter.to_list()"
	}, false},
	{"take", []string{"Iter.take"}, pipe("Iter.from(0)", " |> Iter.take(3) |> Iter.to_list()"), false},
	{"take_while", []string{"Iter.take_while"}, pipe("Iter.from(0)", " |> Iter.take_while(&(&1 < 3)) |> Iter.to_list()"), false},
	// Terminals that must reach the end.
	{"to_list", []string{"Iter.to_list"}, pipe("Iter.repeat(1)", " |> Iter.to_list()"), true},
	{"to_vector", []string{"Iter.to_vector"}, pipe("Iter.from(0)", " |> Iter.to_vector()"), true},
	{"to_set", []string{"Iter.to_set"}, pipe("Iter.from(0)", " |> Iter.to_set()"), true},
	{"to_map", []string{"Iter.to_map"}, pipe("Iter.from(0) |> Iter.map(fn x -> #(x, x))", " |> Iter.to_map()"), true},
	{"count", []string{"Iter.count"}, pipe("Iter.repeat(1)", " |> Iter.count()"), true},
	{"last", []string{"Iter.last"}, pipe("Iter.from(0)", " |> Iter.last()"), true},
	{"reverse", []string{"Iter.reverse"}, pipe("Iter.from(0)", " |> Iter.reverse()"), true},
	{"flatten", []string{"Iter.flatten"}, pipe("Iter.repeat([1])", " |> Iter.flatten()"), true},
	{"sort", []string{"Iter.sort"}, pipe("Iter.from(0)", " |> Iter.sort()"), true},
	{"sort_by", []string{"Iter.sort_by"}, pipe("Iter.from(0)", " |> Iter.sort_by(fn x -> 0 - x)"), true},
	{"sort_with", []string{"Iter.sort_with"}, pipe("Iter.from(0)", " |> Iter.sort_with(&Int.compare(&1, &2))"), true},
	{"partition", []string{"Iter.partition"}, pipe("Iter.from(0)", " |> Iter.partition(fn x -> x > 1)"), true},
	{"group_by", []string{"Iter.group_by"}, pipe("Iter.from(0)", " |> Iter.group_by(fn x -> x % 2)"), true},
	{"frequencies", []string{"Iter.frequencies"}, pipe("Iter.from(0)", " |> Iter.frequencies()"), true},
	{"min", []string{"Iter.min"}, pipe("Iter.from(0)", " |> Iter.min()"), true},
	{"max", []string{"Iter.max"}, pipe("Iter.repeat(1)", " |> Iter.max()"), true},
	{"min_by", []string{"Iter.min_by"}, pipe("Iter.repeat(1)", " |> Iter.min_by(fn x -> x)"), true},
	{"max_by", []string{"Iter.max_by"}, pipe("Iter.repeat(1)", " |> Iter.max_by(fn x -> x)"), true},
	{"sum", []string{"Iter.sum"}, pipe("Iter.from(0)", " |> Iter.sum()"), true},
	{"reduce", []string{"Iter.reduce"}, pipe("Iter.repeat(1)", " |> Iter.reduce(fn total = 0, x -> total + x)"), true},
	{"reduce that can break", []string{"Iter.reduce"}, pipe("Iter.from(0)", " |> Iter.reduce(fn total = 0, x -> {\n        if x > 4 {\n            break total\n        } else {\n            total + x\n        }\n    })"), false},
	{"each", []string{"Iter.each"}, pipe("Iter.repeat(1)", " |> Iter.each(fn _x -> Unit)"), true},
	{"each that can break", []string{"Iter.each"}, pipe("Iter.from(0)", " |> Iter.each(fn x -> {\n        if x > 4 {\n            break\n        } else {\n            Unit\n        }\n    })"), false},
	{"map_reduce", []string{"Iter.map_reduce"}, pipe("Iter.from(0)", " |> Iter.map_reduce(fn s = 0, x -> #(x, s + x))"), true},
	{"String.join", []string{"String.join"}, pipe("Iter.repeat(\"a\")", " |> String.join(\", \")"), true},
	{"Maybe.values", []string{"Maybe.values"}, pipe("Iter.repeat(Maybe.Some(1))", " |> Maybe.values()"), true},
	{"Result.partition", []string{"Result.partition"}, pipe("Iter.repeat(ok)", " |> Result.partition()"), true},
	{"Task.spawn_all", []string{"Task.spawn_all"}, func(src func(string) string) string {
		return "concurrent {\n        " + src("Iter.repeat(1)") + " |> Task.spawn_all(&(&1 * 2), max_running: 2) |> Task.await_all()\n    }"
	}, true},
	// Terminals that may stop early.
	{"find", []string{"Iter.find"}, pipe("Iter.from(0)", " |> Iter.find(&(&1 > 3))"), false},
	{"first", []string{"Iter.first"}, pipe("Iter.repeat(1)", " |> Iter.first()"), false},
	{"get", []string{"Iter.get"}, pipe("Iter.from(0)", " |> Iter.get(4)"), false},
	{"any?", []string{"Iter.any?"}, pipe("Iter.from(0)", " |> Iter.any?(&(&1 == 7))"), false},
	{"all?", []string{"Iter.all?"}, pipe("Iter.from(0)", " |> Iter.all?(&(&1 < 7))"), false},
	{"contains?", []string{"Iter.contains?"}, pipe("Iter.from(0)", " |> Iter.contains?(1_000)"), false},
	{"empty?", []string{"Iter.empty?"}, pipe("Iter.from(0)", " |> Iter.empty?()"), false},
	{"not_empty?", []string{"Iter.not_empty?"}, pipe("Iter.from(0)", " |> Iter.not_empty?()"), false},
	{"each_while", []string{"Iter.each_while"}, pipe("Iter.from(0)", " |> Iter.each_while(&(&1 < 3))"), false},
	{"known_count", []string{"Iter.known_count"}, pipe("Iter.from(0)", " |> Iter.known_count()"), false},
	{"infinite?", []string{"Iter.infinite?"}, pipe("Iter.from(0)", " |> Iter.infinite?()"), false},
	{"Maybe.collect", []string{"Maybe.collect"}, pipe("Range.naturals() |> Iter.map(fn n -> if n < 3 { Maybe.Some(n) } else { Maybe.None })", " |> Maybe.collect()"), false},
	{"Result.collect", []string{"Result.collect"}, pipe("Iter.from(0) |> Iter.map(fn n -> if n < 3 { Result.Ok(n) } else { Result.Err(\"x\") })", " |> Result.collect()"), false},
}

// infiniteIterRuntimeNames is each case whose trap names a terminal other
// than the one the program called, and that name.
var infiniteIterRuntimeNames = map[string]string{}

// infiniteIterRuntimeSources is each case whose source the runtime cannot
// tell apart from another, and the name its trap gives. `Range.naturals()`
// is the value `Range.from(0)` builds, so the trap names `Range.from`; the
// checker sees the call and names `Range.naturals`.
var infiniteIterRuntimeSources = map[string]string{
	"Range.naturals": "Range.from",
}

// infiniteIterUntested names the std functions the agreement test does not
// run, with the reason.
var infiniteIterUntested = map[string]string{
	// It runs only during a boot, against an application's supervisor
	// field; the checker's own tests cover it.
	"Supervisor.spawn_all": "needs an application boot",
	// `Iter.loop` takes no source.
	"Iter.loop": "takes no iterator",
}

// infiniteIterProgram is the program main of which binds expr.
func infiniteIterProgram(expr string) string {
	src := strings.Replace(infiniteIterMain, "EXPR", expr, 1)
	if strings.Contains(expr, "Task.") {
		src = "import std/tasks.Task\n\n" + src
	}
	return src
}

const infiniteIterMain = `fn hide<T>(xs: Iter<T>) -> Iter<T> {
    xs
}

fn main() {
    ok: Result<Int, String> = Result.Ok(1)
    _ = ok
    _ = hide([1])
    _ = EXPR
}
`

// The checker reports an infinite iterator exactly where the runtime traps
// on one at once, with the runtime's words. Each case runs twice: as
// written, where the checker must reject it iff the case is infinite, and
// with its sources passed through a function, which the checker does not
// follow, where the runtime must trap iff the case is infinite. Every `impl
// Iter` function std/iter.nomi declares, and every name in the checker's
// tables, must have a case, so neither can grow alone.
func TestInfiniteIterCheckAgreesWithTheRuntime(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	covered := map[string]bool{}
	for _, c := range infiniteIterCases {
		for _, fn := range c.fns {
			covered[fn] = true
		}
	}
	terminals, adapters, sources := analysis.InfiniteIterTables()
	for _, fn := range slices.Concat(terminals, adapters, sources, stdIterFunctions(t)) {
		if !covered[fn] && infiniteIterUntested[fn] == "" {
			t.Errorf("no agreement case runs %s", fn)
		}
	}
	trapLine := regexp.MustCompile("`[^`]+` of an infinite iterator \\(`[^`]+`\\) never finishes; limit it with `Iter.take` or `Iter.take_while`")
	for _, c := range infiniteIterCases {
		t.Run(c.name, func(t *testing.T) {
			direct := infiniteIterProgram(c.expr(func(s string) string { return s }))
			_, err := vmhost.LoadSource("main.nomi", direct)
			var checked string
			if err != nil {
				checked = trapLine.FindString(err.Error())
				if checked == "" {
					t.Fatalf("the front end rejects this for another reason: %v\n%s", err, direct)
				}
			}
			hidden := infiniteIterProgram(c.expr(func(s string) string { return "hide(" + s + ")" }))
			p, err := vmhost.LoadSource("main.nomi", hidden)
			if err != nil {
				t.Fatalf("the front end rejects the hidden form: %v\n%s", err, hidden)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out bytes.Buffer
			runErr := p.Run(ctx, &out, nil, false)
			var trapped string
			if runErr != nil {
				trapped = trapLine.FindString(runErr.Error() + out.String())
				if trapped == "" {
					t.Fatalf("the hidden form fails for another reason: %v\n%s\n%s", runErr, out.String(), hidden)
				}
			} else if trapLine.MatchString(out.String()) {
				trapped = trapLine.FindString(out.String())
			}
			if (trapped != "") != c.infinite {
				t.Fatalf("the runtime traps: %q; the case says infinite = %v\n%s", trapped, c.infinite, hidden)
			}
			if runtimeName, ok := infiniteIterRuntimeNames[c.name]; ok {
				if !strings.HasPrefix(trapped, "`"+runtimeName+"` ") {
					t.Fatalf("the runtime now traps with %q, not naming `%s`; remove the case from infiniteIterRuntimeNames", trapped, runtimeName)
				}
				trapped = strings.Replace(trapped, "`"+runtimeName+"`", "`"+c.fns[0]+"`", 1)
			}
			if runtimeSource, ok := infiniteIterRuntimeSources[c.name]; ok {
				if !strings.Contains(trapped, "(`"+runtimeSource+"`)") {
					t.Fatalf("the runtime now traps with %q, not naming `%s`; remove the case from infiniteIterRuntimeSources", trapped, runtimeSource)
				}
				trapped = strings.Replace(trapped, "(`"+runtimeSource+"`)", "(`"+c.name+"`)", 1)
			}
			if checked != trapped {
				t.Fatalf("the checker reports %q where the runtime traps with %q\n%s", checked, trapped, direct)
			}
		})
	}
}

// stdIterFunctions is "Iter.<name>" for every function std/iter.nomi's
// `impl Iter` blocks declare.
func stdIterFunctions(t *testing.T) []string {
	t.Helper()
	text, err := os.ReadFile("../std/iter.nomi")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^    (?:pub (?:host )?|open )fn ([a-z_?]+)`)
	out := []string{"Iter.each_while"}
	for _, m := range decl.FindAllStringSubmatch(string(text), -1) {
		out = append(out, "Iter."+m[1])
	}
	if len(out) < 40 {
		t.Fatalf("found %d Iter functions in std/iter.nomi; the pattern no longer reads it", len(out))
	}
	return out
}
