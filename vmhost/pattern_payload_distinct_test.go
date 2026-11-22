package vmhost_test

import (
	"testing"
)

// A bare list or map pattern in a variant's payload over a distinct of a
// list or a map (`Some([a, ..])` over a `Maybe<Items>`, `Some({"a" => v})`
// over a `Maybe<Kvs>`) matches the distinct's inner collection, as the same
// pattern does at the top of a `case` arm, in each position a variant
// pattern takes.
func TestRun_ABareCollectionPatternInAPayloadMatchesADistinctsInner(t *testing.T) {
	out, err := runVM(t, `import std::io

type Items List<Int>
type Kvs Map<String, Int>

fn first(m: Maybe<Items>) -> Int
  case m
    Some([a, ..]) -> a
    Some([]) -> 0
    None -> -1
  end
end

fn look(m: Maybe<Kvs>) -> Int
  case m
    Some({"a" => v}) -> v
    Some(_) -> 0
    None -> -1
  end
end

fn bound(m: Maybe<Items>) -> Int
  Some([a, b]) = m else
    return -1
  end
  a + b
end

fn in_if(m: Maybe<Kvs>) -> String
  if Some({"b" => v}) = m then
    "if: ${v}"
  else
    "if: no match"
  end
end

fn main()
  io.print(first(Some(Items([4, 5]))))
  io.print(first(Some(Items([]))))
  io.print(first(None))
  io.print(look(Some(Kvs({"a" => 7}))))
  io.print(look(Some(Kvs({"z" => 7}))))
  io.print(bound(Some(Items([2, 3]))))
  io.print(bound(Some(Items([2]))))
  io.print(in_if(Some(Kvs({"b" => 1}))))
  io.print(in_if(Some(Kvs({}))))
  io.print(Some(Items([9])) is Some([9]))
  io.print(Some(Kvs({"a" => 1})) is Some({"a" => 2}))
end
`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := "4\n0\n-1\n7\n0\n5\n-1\nif: 1\nif: no match\nTrue\nFalse\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
