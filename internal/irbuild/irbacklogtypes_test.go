package irbuild

import (
	"strings"
	"testing"
)

// Value shapes and call forms a VM-only test body retains, one case per
// shape: prelude constructors whose
// unconstrained type arguments read as Unit, a bare None with no expected
// type, Result.equal?, the prelude predicates as variant tests, one prelude
// wrapper nested in another, a tuple pattern in a variant payload, a bare std
// variant constructor, literal-attach forms, a user enum's List and Map
// payloads, user enum map values, partial application, a distinct lambda
// parameter pattern and statement, a signalling callback's tail if, sets of
// tuples, lists of maps, a checked branch binding, an interface-qualified
// turbofish over a concrete type, the struct call form over a record value and
// lists of a distinct. The failing report matches the recorded one, byte for
// byte.
func TestIRTestBody_BacklogTypes(t *testing.T) {
	const src = `import {
  std/json.{FromJson}
  std/json.Json.{self, String as JStr, Int as JInt}
}

type Dur Int

enum Box {
  Items List<Int>
  Pairs Map<String, Int>
  Empty
}

struct Conf {
  host: String
  port: Int = 8080
  retries: Int
}

fn conf_defaults(): {retries: Int, host: String} {
  {retries: 3, host: "h"}
}

enum Color {
  Red
  Blue
}

fn add(x: Int, y: Int): Int {
  x + y
}

fn first_of(m: Maybe<(Int, String)>): Int {
  case m {
    Some((n, _)) -> n
    None -> 0
  }
}

fn unwrap(m: Maybe<Result<Int, String>>): Int {
  case m {
    Some(Ok(n)) -> n
    Some(Err(_)) -> -1
    None -> 0
  }
}

test "unsolved prelude arguments and bare variants" {
  assert Ok(1) == Ok(1)
  refute Ok(1) == Ok(2)
  assert None
    |> Debug.inspect()
    |> String.equal?("None")
  assert Result.equal?(Ok(4), Ok(4))
  refute Result.equal?(Ok(4), Err("bad"))
}

test "prelude predicates" {
  assert Result.err?(Err("x"))
  refute Result.ok?(Err("x"))
  assert Maybe.some?(Some(1))
  refute Maybe.none?(Some(1))
}

test "nested prelude payloads and tuple payload patterns" {
  assert unwrap(Some(Ok(9))) == 9
  assert unwrap(Some(Err("e"))) == -1
  assert first_of(Some((4, "a"))) == 4
  deep = Some(Some((7, "deep")))
  got = case deep {
    Some(Some((_, s))) -> s
    _ -> "miss"
  }
  assert got == "deep"
}

test "bare std variants and attached literals" {
  j = Json.Arr[JStr("a"), JInt(2)]
  kind = case j {
    .Arr(_) -> "arr"
    _ -> "other"
  }
  assert kind == "arr"
  b: Box = .Items[1, 2]
  p = Box.Pairs{"k" => 1}
  assert b == Box.Items[1, 2]
  refute p == Box.Empty
  colors: Map<String, Color> = {"a" => Color.Red}
  assert Map.get(colors, "a") == Some(Color.Red)
}

test "partial application and lambda patterns" {
  add1 = add(1, _)
  assert add1(9) == 10
  doubled =
    [Dur(1), Dur(2)]
    |> Iter.map(|Dur(n)| n * 2)
    |> Iter.to_list()
  assert doubled == [2, 4]
  Dur(raw) = Dur(5)
  assert raw == 5
}

test "a filter callback breaks from a tail if" {
  kept =
    Iter.filter([1, 2, 3, 4], |x|
      if x == 3 {
        break True
      } else {
        x > 1
      }
    )
    |> Iter.to_list()
  assert kept == [2, 3]
}

test "sets of tuples, lists of maps and branch bindings" {
  ts = #{(1, 2), (3, 4), (1, 2)}
  assert Set.size(ts) == 2
  buckets: List<Map<String, Int>> = []
  assert Iter.count(buckets) == 0
  xs = case Some(3) {
    Some(n) -> [n]
    None -> []
  }
  assert Iter.count(xs) == 1
}

test "a turbofish names the impl" {
  assert FromJson.from_json<Int>(Json.Int(42)) == Ok(42)
}

test "the struct call form over a record value" {
  c = Conf(conf_defaults())
  assert c == Conf{host: "h", port: 8080, retries: 3}
}

test "lists of a distinct compare structurally" {
  assert [Dur(1), Dur(2)] == [Dur(1), Dur(2)]
  refute [Dur(1)] == [Dur(2)]
}

test "failing reports" {
  assert Result.err?(Ok(1))
}
`
	got, exit := irShapesRun(t, src, []string{
		"unsolved prelude arguments and bare variants",
		"prelude predicates",
		"nested prelude payloads and tuple payload patterns",
		"bare std variants and attached literals",
		"partial application and lambda patterns",
		"a filter callback breaks from a tail if",
		"sets of tuples, lists of maps and branch bindings",
		"a turbofish names the impl",
		"the struct call form over a record value",
		"lists of a distinct compare structurally",
		"failing reports",
	})
	var want strings.Builder
	for _, name := range []string{
		"unsolved prelude arguments and bare variants",
		"prelude predicates",
		"nested prelude payloads and tuple payload patterns",
		"bare std variants and attached literals",
		"partial application and lambda patterns",
		"a filter callback breaks from a tail if",
		"sets of tuples, lists of maps and branch bindings",
		"a turbofish names the impl",
		"the struct call form over a record value",
		"lists of a distinct compare structurally",
	} {
		want.WriteString("ok <path> :: " + name + "\n")
	}
	want.WriteString(`FAIL <path> :: failing reports
  line 144: assertion failed
    assert Result.err?(Ok(1))
    values:
      Ok(1)
        = Ok(1)
`)
	if !strings.HasPrefix(got, want.String()) || exit == 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want.String())
	}
}
