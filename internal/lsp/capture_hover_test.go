package lsp

import "testing"

// A capture's `&` answers with its function type and where its parameters'
// types came from, as a lambda's `fn` does; each `&N` of its body answers
// with its type, as a parameter does, and the capture it belongs to.
func TestHover_CaptureAndItsParameters(t *testing.T) {
	runShapeHoverCases(t, shapeHoverDecls, []shapeHoverCase{
		{"a callback's `&`", "fn main()\n  big = [1, 5] |> Iter.filter(▮&(&1 > 2)) |> Iter.to_list()\n  io.inspect(big)\nend\n",
			[]string{"```nomi\n(Int) -> Bool\n```", "Fills `Iter.filter`'s `pred: (T) -> Bool`, which gives its parameter types."}, nil},
		{"an annotated binding's `&`", "fn main()\n  add: (Int, Int) -> Int = ▮&(&1 + &2)\n  io.inspect(add(1, 2))\nend\n",
			[]string{"```nomi\n(Int, Int) -> Int\n```", "Parameter types from the expected type `(Int, Int) -> Int`."}, nil},
		{"a partial capture's `&`", "fn main()\n  inc = ▮&Int.max(&1, 0)\n  io.inspect(inc(1))\nend\n",
			[]string{"```nomi\n(Int) -> Int\n```", "A partial application of `Int.max`"}, nil},
		{"a then stage's `&`", "fn main()\n  io.inspect(10 |> then ▮&Int.max(&1, 0))\nend\n",
			[]string{"The `then` stage's capture; `&1` is the piped value."}, nil},
		{"an operator's `&1`", "fn main()\n  big = [1, 5] |> Iter.filter(&(▮&1 > 2)) |> Iter.to_list()\n  io.inspect(big)\nend\n",
			[]string{"```nomi\nInt\n```", "`&1` is the first parameter of the capture `(Int) -> Bool`."}, nil},
		{"the second of two", "fn main()\n  add: (Int, Int) -> Int = &(&1 + ▮&2)\n  io.inspect(add(1, 2))\nend\n",
			[]string{"```nomi\nInt\n```", "`&2` is the second parameter of the capture `(Int, Int) -> Int`."}, nil},
		{"a reused parameter", "fn main()\n  sq: (Int) -> Int = &(&1 * ▮&1)\n  io.inspect(sq(3))\nend\n",
			[]string{"`&1` is the first parameter of the capture"}, nil},
		{"a struct literal's field", "fn main()\n  mk: (Int) -> Point = &Point{x: ▮&1, y: 0}\n  io.inspect(mk(1))\nend\n",
			[]string{"```nomi\nInt\n```", "of the capture `(Int) -> Point`."}, nil},
		{"a tuple's element", "fn main()\n  pair: (Int) -> (Int, String) = &(▮&1, \"a\")\n  io.inspect(pair(1))\nend\n",
			[]string{"```nomi\nInt\n```", "of the capture `(Int) -> (Int, String)`."}, nil},
		{"a string's slot", "fn main()\n  say: (Int) -> String = &\"id ${▮&1}\"\n  io.inspect(say(1))\nend\n",
			[]string{"```nomi\nInt\n```", "`&1` is the first parameter of the capture `(Int) -> String`."}, nil},
		{"a string capture's `&`", "fn main()\n  ids = [1] |> Iter.map(▮&\"id ${&1}\") |> Iter.to_list()\n  io.inspect(ids)\nend\n",
			[]string{"```nomi\n(Int) -> String\n```"}, nil},
		{"a partial capture's `&1` shows the parameter it leaves open", "fn main()\n  inc = &Int.max(▮&1, 0)\n  io.inspect(inc(1))\nend\n",
			[]string{"a: Int"}, []string{"first parameter of the capture"}},
	})
}
