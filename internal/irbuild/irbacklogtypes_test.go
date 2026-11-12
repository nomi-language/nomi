package irbuild

import (
	"strings"
	"testing"
)

// Value shapes and call forms a VM-only test body retains, one case per
// shape: prelude constructors whose
// unconstrained type arguments the checker closed, a bare None with no expected
// type, Result.equal, the prelude predicates as variant tests, one prelude
// wrapper nested in another, a tuple pattern in a variant payload, a bare std
// variant constructor, literal-attach forms, a user enum's List and Map
// payloads, user enum map values, partial application, a distinct lambda
// parameter pattern and statement, a signalling callback's tail if, sets of
// tuples, lists of maps, a checked branch binding, an interface-qualified
// turbofish over a concrete type, the struct call form over a record value and
// lists of a distinct. The failing report matches the recorded one, byte for
// byte.
func TestIRTestBody_BacklogTypes(t *testing.T) {
	const src = `import
  std::json.{FromJson}
  std::json.Json.{self, String as JStr, Int as JInt}
end

type Dur Int

enum Box
  Items List<Int>
  Pairs Map<String, Int>
  Empty
end

struct Conf
  host: String
  port: Int = 8080
  retries: Int
end

fn conf_defaults() -> {retries: Int, host: String}
  {retries: 3, host: "h"}
end

enum Color
  Red
  Blue
end

fn add(x: Int, y: Int) -> Int
  x + y
end

fn first_of(m: Maybe<(Int, String)>) -> Int
  case m
    Some((n, _)) -> n
    None -> 0
  end
end

fn unwrap(m: Maybe<Result<Int, String>>) -> Int
  case m
    Some(Ok(n)) -> n
    Some(Err(_)) -> -1
    None -> 0
  end
end

test "unsolved prelude arguments and bare variants"
  assert Ok(1) == Ok(1)
  refute Ok(1) == Ok(2)
  assert None
    |> Debug.inspect()
    |> String.equal("None")
  assert Result.equal(Ok(4), Ok(4))
  refute Result.equal(Ok(4), Err("bad"))
end

test "prelude predicates"
  assert Result.is_err(Err("x"))
  refute Result.is_ok(Err("x"))
  assert Maybe.is_some(Some(1))
  refute Maybe.is_none(Some(1))
end

test "nested prelude payloads and tuple payload patterns"
  assert unwrap(Some(Ok(9))) == 9
  assert unwrap(Some(Err("e"))) == -1
  assert first_of(Some((4, "a"))) == 4
  deep = Some(Some((7, "deep")))
  got = case deep
    Some(Some((_, s))) -> s
    _ -> "miss"
  end
  assert got == "deep"
end

test "bare std variants and attached literals"
  j = Json.Arr[JStr("a"), JInt(2)]
  kind = case j
    .Arr(_) -> "arr"
    _ -> "other"
  end
  assert kind == "arr"
  b: Box = .Items[1, 2]
  p = Box.Pairs{"k" => 1}
  assert b == Box.Items[1, 2]
  refute p == Box.Empty
  colors: Map<String, Color> = {"a" => Color.Red}
  assert Map.get(colors, "a") == Some(Color.Red)
end

test "partial application and lambda patterns"
  add1 = &add(1, &1)
  assert add1(9) == 10
  doubled =
    [Dur(1), Dur(2)]
    |> Iter.map(fn Dur(n) -> n * 2)
    |> Iter.to_list()
  assert doubled == [2, 4]
  Dur(raw) = Dur(5)
  assert raw == 5
end

test "a filter callback breaks from a tail if"
  kept =
    Iter.filter([1, 2, 3, 4], fn x ->
      if x == 3 then
        break True
      else
        x > 1
      end
    end
    )
    |> Iter.to_list()
  assert kept == [2, 3]
end

test "sets of tuples, lists of maps and branch bindings"
  ts = Set[(1, 2), (3, 4), (1, 1 + 1)]
  assert Set.size(ts) == 2
  buckets: List<Map<String, Int>> = []
  assert Iter.count(buckets) == 0
  xs = case Some(3)
    Some(n) -> [n]
    None -> []
  end
  assert Iter.count(xs) == 1
end

test "a turbofish names the impl"
  assert FromJson.from_json<Int>(Json.Int(42)) == Ok(42)
end

test "the struct call form over a record value"
  c = Conf(conf_defaults())
  assert c == Conf{host: "h", port: 8080, retries: 3}
end

test "lists of a distinct compare structurally"
  assert [Dur(1), Dur(2)] == [Dur(1), Dur(2)]
  refute [Dur(1)] == [Dur(2)]
end

test "failing reports"
  assert Result.is_err(Ok(1))
end
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
  line 145: assertion failed
    assert Result.is_err(Ok(1))
    values:
      Ok(1)
        = Ok(1)
`)
	if !strings.HasPrefix(got, want.String()) || exit == 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want.String())
	}
}
