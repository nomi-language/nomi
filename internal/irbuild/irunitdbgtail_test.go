package irbuild

import "testing"

// A Unit body ending in a `dbg` observation runs the observation, prints its
// line, discards the value and answers Unit: in a top-level function (with
// and without a written `: Unit`, and with a deferred call that runs after
// the observation), an impl function, a nested function, and as a pipe whose
// last stage is `|> dbg`. A non-Unit body ending in dbg still answers the
// observed value.
func TestIRUnitDbgTail_RunsAndPrints(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Box {
  n: Int
}

impl Box {
  fn show(b: Box) {
    dbg b.n
  }
}

fn twice(n: Int): Int {
  dbg n * 2
}

fn report(): Unit {
  defer io.print("deferred")
  dbg twice(4)
}

fn main() {
  fn inner() {
    "nested" |> dbg
  }
  inner()
  Box.show(Box{n: 7})
  report()
  io.print("total ${twice(1)}")
  [1, 2]
  |> Iter.map(|n| n * 10)
  |> Iter.to_list()
  |> dbg
}
`, "dbg line 24: \"nested\" = \"nested\"\n"+
		"dbg line 9: b.n = 7\n"+
		"dbg line 14: n * 2 = 8\n"+
		"dbg line 19: twice(4) = 8\n"+
		"deferred\n"+
		"dbg line 14: n * 2 = 2\n"+
		"total 2\n"+
		"dbg line 33:\n  [1, 2]\n  |> Iter.map(|n| n * 10)\n  |> Iter.to_list()\n  = [10, 20]\n")
}
