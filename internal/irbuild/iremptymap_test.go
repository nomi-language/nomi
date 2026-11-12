package irbuild

import (
	"testing"
)

// A Set is a map value (and key): it hashes and compares order-insensitively.
func TestIREmptyMap_ASetValueRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  m: Map<String, Set<Int>> = {}
  io.print(Map.size(m))
  n = Map.put(m, "k", Set[1, 2])
  io.print(Map.get(n, "k") == Some(Set[2, 1]))
end
`, "0\nTrue\n")
}

func TestIREmptyMap_AnnotationsAndReturnTypes(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn empty() -> Map<String, Int>
    {}
end
fn main()
  scores: Map<String, Int> = {}
  io.print(Map.size(scores))
  empty() |> Map.size() |> io.print()
  io.print(Map.size(empty()))
  one = Map.put({}, "a", 1)
  io.inspect(Map.get(one, "a"))
end
`, "0\n0\n0\nSome(1)\n")
}

func TestIREmptyMap_EffectfulUntypedProducer(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn size(m: Map<String, Int>) -> Int
    Map.size(m)
end
fn take(m: Map<String, Int>, n: Int) -> Int
    Map.size(m) + n
end
fn mark() -> Int
    io.print("mark")
    1
end
fn main()
  empty = fn ->
      io.print("empty")
      {}
  end
  io.print(size(empty()))
  io.print(take(empty(), mark()))
  bound: Map<String, Int> = empty()
  io.print(Map.size(bound))
  supplied = fn m: Map<String, Int> = empty() -> Map.size(m)
  io.print(supplied())
end
`, "empty\n0\nempty\nmark\n1\nempty\n0\nempty\n0\n")
}

func TestIREmptyMap_EffectsAndContext(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn empty() -> Map<String, Int>
    io.print("empty")
    {}
end
fn size(m: Map<String, Int> = {}) -> Int
    Map.size(m)
end
fn choose(b: Bool) -> Map<String, Int>
  if b then {} else {"a" => 1}
end
fn main()
  m: Map<String, Int> = empty()
  io.print(Map.size(m))
  io.print(size())
  io.print(size({}))
  io.print(Map.size(choose(True)))
  io.print(Map.size(choose(False)))
  get = fn m: Map<String, Int> -> Map.get(m, "a")
  io.inspect(get({}))
end
`, "empty\n0\n0\n0\n0\n1\nNone\n")
}
