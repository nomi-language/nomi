package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A pattern `if` lowers every pattern a `case` arm does: a map pattern with a
// constant key, a computed key (evaluated once) and a nested value pattern, a
// map pattern that rebinds a name already in scope (its later key reads the
// name's value before the pattern), a list pattern with a rest, a tuple and a
// struct pattern with a literal part, an Int and a String literal, an `as`
// around a list and a map pattern, and a list and a tuple nested in a
// variant. Each runs once matched and once not, as a value with `else` and as
// a statement without one.
func TestRun_PatternIfLowersEveryCasePatternKind(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Point {
  x: Int
  y: Int
}

fn note(s: String) -> String {
  io.print("key ${s}")
  s
}

fn map_constant(m: Map<String, String>) -> String {
  if %{"mode" => b} = m {
    "mode ${b}"
  } else {
    "no mode"
  }
}

fn map_computed(m: Map<String, Maybe<Int>>) -> String {
  if %{note("n") => Some(n)} = m {
    "n ${n}"
  } else {
    "no n"
  }
}

fn map_rebind(m: Map<String, String>) -> String {
  a = "host"
  if %{"mode" => a, a => b} = m {
    "rebind ${a} ${b}"
  } else {
    "rebind miss ${a}"
  }
}

fn map_statement(m: Map<String, String>) {
  if %{"mode" => b} = m {
    io.print("statement ${b}")
  }
  io.print("after")
}

fn list_rest(xs: List<Int>) -> String {
  if [first, ..rest] = xs {
    "first ${first} rest ${Iter.count(rest)}"
  } else {
    "empty"
  }
}

fn tuple_part(p: #(Int, Int)) -> String {
  if #(0, y) = p {
    "y ${y}"
  } else {
    "x not 0"
  }
}

fn int_lit(n: Int) -> String {
  if 1 = n {
    "one"
  } else {
    "not one"
  }
}

fn string_lit(s: String) -> String {
  if "go" = s {
    "go!"
  } else {
    "stay"
  }
}

fn struct_field(p: Point) -> String {
  if #Point{x: 0, y} = p {
    "on axis at ${y}"
  } else {
    "off axis"
  }
}

fn as_list(xs: List<Int>) -> String {
  if [a, _] as whole = xs {
    "pair from ${a} of ${Iter.count(whole)}"
  } else {
    "not a pair"
  }
}

fn as_map(m: Map<String, String>) -> String {
  if %{"mode" => md} as whole = m {
    "${md} in ${Map.size(whole)}"
  } else {
    "no mode in ${Map.size(m)}"
  }
}

fn nested(v: Maybe<List<#(Int, String)>>) -> String {
  if Some([#(0, s), ..]) = v {
    "nested ${s}"
  } else {
    "nested miss"
  }
}

fn main() {
  io.print(map_constant(%{"mode" => "debug", "host" => "h1"}))
  io.print(map_constant(%{"host" => "h1"}))
  io.print(map_computed(%{"n" => Some(3)}))
  io.print(map_computed(Map.put(%{"n" => Some(1)}, "n", None)))
  io.print(map_computed(%{"m" => Some(1)}))
  io.print(map_rebind(%{"mode" => "debug", "host" => "h1"}))
  io.print(map_rebind(%{"host" => "h1"}))
  map_statement(%{"mode" => "debug"})
  map_statement(%{"host" => "h1"})
  io.print(list_rest([1, 2, 3]))
  io.print(list_rest([]))
  io.print(tuple_part(#(0, 5)))
  io.print(tuple_part(#(1, 5)))
  io.print(int_lit(1))
  io.print(int_lit(2))
  io.print(string_lit("go"))
  io.print(string_lit("no"))
  io.print(struct_field(#Point{x: 0, y: 4}))
  io.print(struct_field(#Point{x: 1, y: 4}))
  io.print(as_list([7, 8]))
  io.print(as_list([7]))
  io.print(as_map(%{"mode" => "m", "a" => "b"}))
  io.print(as_map(%{"a" => "b"}))
  io.print(nested(Some([#(0, "zero"), #(1, "one")])))
  io.print(nested(Some([#(1, "one")])))
  io.print(nested(None))
}
`)
	want := strings.Join([]string{
		"mode debug", "no mode",
		"key n", "n 3", "key n", "no n", "key n", "no n",
		"rebind debug h1", "rebind miss host",
		"statement debug", "after", "after",
		"first 1 rest 2", "empty",
		"y 5", "x not 0",
		"one", "not one",
		"go!", "stay",
		"on axis at 4", "off axis",
		"pair from 7 of 2", "not a pair",
		"m in 2", "no mode in 1",
		"nested zero", "nested miss", "nested miss",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// In a test body, a pattern `if` over a map, a list and a tuple runs as a
// statement and as a value, matched and not, and in a lambda.
func TestRun_PatternIfOverMapListAndTupleInATestBody(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std/io

test "pattern if kinds" {
  m = %{"mode" => "debug"}
  if %{"mode" => b} = m {
    io.print("mode ${b}")
  }
  if %{"host" => h} = m {
    io.print("host ${h}")
  }
  r = if #(0, y) = #(0, 5) { y } else { -1 }
  f = fn xs: List<Int> -> if [x, ..] = xs { x } else { 0 }
  io.print("${r} ${f([4, 5])} ${f([])}")
  assert r == 5
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the test: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if !strings.Contains(out, ":: pattern if kinds\n  mode debug\n  5 4 0\n") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want the two lines and one passing case:\n%s", out)
	}
}
