package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A Float or Decimal literal runs wherever a pattern is tested: a `case`
// arm, nested in a tuple, a variant payload, a struct field, a list head, a
// map value and under `as`, in a pattern `if` and in an `else` binding. Each
// matches once and misses once. Nested and top-level literals compare with
// `==`'s equality (rt.EqFloat, rt.EqDecimal), so `0.0` matches `-0.0` at
// any depth, a NaN matches no literal, and `1.5d` matches `1.50d`.
func TestRun_FloatAndDecimalLiteralPatternsRunWherePatternsAreTested(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Price {
  amount: Decimal
  weight: Float
}

fn top(f: Float) -> String {
  case f {
    0.0 -> "top zero"
    1.5 -> "top 1.5"
    _ -> "top miss"
  }
}

fn top_decimal(d: Decimal) -> String {
  case d {
    1.5d -> "top 1.5d"
    _ -> "top decimal miss"
  }
}

fn tuple(x: #(Float, Int)) -> String {
  case x {
    #(1.5, n) -> "tuple ${n}"
    #(0.0, n) -> "tuple zero ${n}"
    _ -> "tuple miss"
  }
}

fn payload(m: Maybe<Float>) -> String {
  case m {
    Some(-2.5) -> "payload -2.5"
    _ -> "payload miss"
  }
}

fn decimal_payload(m: Maybe<Decimal>) -> String {
  case m {
    Some(1.5d) -> "decimal payload"
    _ -> "decimal payload miss"
  }
}

fn field(p: Price) -> String {
  case p {
    {amount: 1.50d, weight: 2.0} -> "field both"
    Price{amount: 1.5d} -> "field amount"
    _ -> "field miss"
  }
}

fn list_head(xs: List<Float>) -> String {
  case xs {
    [1.5, ..rest] -> "list ${rest}"
    _ -> "list miss"
  }
}

fn decimal_list(xs: List<Decimal>) -> String {
  case xs {
    [_, 2.0d] -> "decimal list"
    _ -> "decimal list miss"
  }
}

fn map_value(m: Map<String, Decimal>) -> String {
  case m {
    %{"a" => 2.0d} -> "map a"
    _ -> "map miss"
  }
}

fn as_whole(x: #(Float, Int)) -> String {
  case x {
    #(2.5 as f, _) -> "as ${f}"
    _ -> "as miss"
  }
}

fn pattern_if(x: #(Decimal, Int)) -> String {
  if #(1.5d, n) = x {
    "if ${n}"
  } else {
    "if miss"
  }
}

fn top_if(f: Float) -> String {
  if 1.5 = f {
    "top if"
  } else {
    "top if miss"
  }
}

fn else_binding(x: #(Float, Int)) -> String {
  #(0.0, n) = x else {
    return "else miss"
  }
  "else ${n}"
}

fn top_else(d: Decimal) -> String {
  1.5d = d else {
    return "top else miss"
  }
  "top else"
}

fn main() {
  nan = Float.nan()
  io.print(top(-0.0))
  io.print(top(1.5))
  io.print(top(nan))
  io.print(top_decimal(1.500d))
  io.print(top_decimal(1.51d))
  io.print(tuple(#(1.5, 2)))
  io.print(tuple(#(-0.0, 3)))
  io.print(tuple(#(1.25, 2)))
  io.print(tuple(#(nan, 2)))
  io.print(payload(Some(-2.5)))
  io.print(payload(Some(2.5)))
  io.print(decimal_payload(Some(1.500d)))
  io.print(decimal_payload(Some(1.6d)))
  io.print(field(Price{amount: 1.5d, weight: 2.0}))
  io.print(field(Price{amount: 1.50d, weight: 3.0}))
  io.print(field(Price{amount: 2d, weight: 2.0}))
  io.print(list_head([1.5, 2.0, 3.0]))
  io.print(list_head([2.0]))
  io.print(decimal_list([1d, 2.00d]))
  io.print(decimal_list([1d, 2.01d]))
  io.print(map_value(%{"a" => 2.00d}))
  io.print(map_value(%{"a" => 2.01d}))
  io.print(as_whole(#(2.5, 1)))
  io.print(as_whole(#(2.4, 1)))
  io.print(pattern_if(#(1.50d, 7)))
  io.print(pattern_if(#(1.51d, 7)))
  io.print(top_if(1.5))
  io.print(top_if(1.6))
  io.print(else_binding(#(-0.0, 9)))
  io.print(else_binding(#(0.1, 9)))
  io.print(top_else(1.5000d))
  io.print(top_else(1.4d))
  near = fn x: #(Float, Int) -> if #(0.5, n) = x { "lambda ${n}" } else { "lambda miss" }
  io.print(near(#(0.5, 4)))
  io.print(near(#(0.25, 4)))
}
`)
	want := strings.Join([]string{
		"top zero",
		"top 1.5",
		"top miss",
		"top 1.5d",
		"top decimal miss",
		"tuple 2",
		"tuple zero 3",
		"tuple miss",
		"tuple miss",
		"payload -2.5",
		"payload miss",
		"decimal payload",
		"decimal payload miss",
		"field both",
		"field amount",
		"field miss",
		"list [2.0, 3.0]",
		"list miss",
		"decimal list",
		"decimal list miss",
		"map a",
		"map miss",
		"as 2.5",
		"as miss",
		"if 7",
		"if miss",
		"top if",
		"top if miss",
		"else 9",
		"else miss",
		"top else",
		"top else miss",
		"lambda 4",
		"lambda miss",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// In a test body, a Float or Decimal literal runs in an `assert pattern =
// value`, at the top and nested, and in a `case` arm, matched and not.
func TestRun_FloatAndDecimalLiteralPatternsInATestBody(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std/io

test "assert matched" {
  assert #(1.5, n) = #(1.5, 2)
  assert #(1.5d, m) = #(1.50d, 3)
  assert 0.0 = -0.0
  io.print("assert ${n} ${m}")
}

test "assert missed" {
  assert #(1.5, _) = #(1.25, 2)
}

test "case" {
  zero = #(-0.0, 4)
  a = case zero {
    #(0.0, n) -> "zero ${n}"
    #(_, n) -> "other ${n}"
  }
  one = #(1.0, 5)
  b = case one {
    #(0.0, n) -> "zero ${n}"
    #(_, n) -> "other ${n}"
  }
  io.print("${a}, ${b}")
  assert a == "zero 4"
  assert b == "other 5"
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the test: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	want := strings.Join([]string{
		"ok main_test.nomi :: assert matched",
		"  assert 2 3",
		"FAIL main_test.nomi :: assert missed",
		"  line 11: pattern did not match",
		"    assert #(1.5, _) = #(1.25, 2)",
		"    actual: #(1.25, 2)",
		"ok main_test.nomi :: case",
		"  zero 4, other 5",
		"test result: FAILED. 2 passed, 1 failed",
	}, "\n") + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("report:\n%s\nwant:\n%s", got, want)
	}
}
