package irbuild

import (
	"testing"
)

// A Set is a map value (and key): it hashes and compares order-insensitively.
func TestIREmptyMap_ASetValueRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  m: Map<String, Set<Int>> = Map.empty<String, Set<Int>>()
  io.print(Map.size(m))
  n = Map.put(m, "k", #{1, 2})
  io.print(Map.get(n, "k") == Some(#{2, 1}))
}
`, "0\nTrue\n")
}

func TestIREmptyMap_AnnotationsAndTypeArguments(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): Map<String, Int> { Map.empty() }
fn main() {
  scores: Map<String, Int> = Map.empty()
  io.print(Map.size(scores))
  Map.empty<String, Int>() |> Map.size() |> io.print()
  io.print(Map.size(empty()))
  one = Map.put(Map.empty(), "a", 1)
  io.inspect(Map.get(one, "a"))
}
`, "0\n0\n0\nSome(1)\n")
}

func TestIREmptyMap_EffectfulUntypedProducer(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn size(m: Map<String, Int>): Int { Map.size(m) }
fn take(m: Map<String, Int>, n: Int): Int { Map.size(m) + n }
fn mark(): Int { io.print("mark") 1 }
fn main() {
  empty = || { io.print("empty") Map.empty() }
  io.print(size(empty()))
  io.print(take(empty(), mark()))
  bound: Map<String, Int> = empty()
  io.print(Map.size(bound))
  supplied = |m: Map<String, Int> = empty()| Map.size(m)
  io.print(supplied())
}
`, "empty\n0\nempty\nmark\n1\nempty\n0\nempty\n0\n")
}

func TestIREmptyMap_EffectsAndContext(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): Map<String, Int> { io.print("empty") Map.empty() }
fn size(m: Map<String, Int> = Map.empty()): Int { Map.size(m) }
fn choose(b: Bool): Map<String, Int> {
  if b { Map.empty() } else { {"a" => 1} }
}
fn main() {
  m: Map<String, Int> = empty()
  io.print(Map.size(m))
  io.print(size())
  io.print(size(Map.empty()))
  io.print(Map.size(choose(True)))
  io.print(Map.size(choose(False)))
  get = |m: Map<String, Int>| Map.get(m, "a")
  io.inspect(get(Map.empty()))
}
`, "empty\n0\n0\n0\n0\n1\nNone\n")
}
