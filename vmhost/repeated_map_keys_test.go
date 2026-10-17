package vmhost_test

import (
	"testing"
)

// Two entries of a map literal whose keys are equal only at run time both
// build, and the later one wins: the map holds the key once, at the first
// entry's position, with the later entry's value (spec §19, Map, Repeated
// keys). A set literal holds such an element once, and a map pattern's two
// entries both match the one value. The compiler rejects the repetition only
// when the keys are the same constant.
func TestRun_RepeatedRuntimeMapKeysLastWins(t *testing.T) {
	out, err := runVM(t, `import std/io

fn main() {
  x = 1
  y = 1
  m = %{x => "a", 2 => "c", y => "b"}
  io.print(m)
  io.print(Map.size(m))
  s = #{x, 2, y}
  io.print(s)
  t = #(1, "k")
  u = #(1, "k")
  io.print(%{t => 1, u => 2})
  k = "a"
  j = "a"
  io.print(case %{"a" => 1} {
    %{k => 1, j => v} -> v
    _ -> 0
  })
  io.print(case %{"a" => 1} {
    %{k => 1, j => 2} -> 1
    _ -> 0
  })
}
`)
	want := "%{1 => b, 2 => c}\n2\n#{1, 2}\n%{#(1, k) => 2}\n1\n0\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}
