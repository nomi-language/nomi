package irbuild

import "testing"

// `break` and `continue` in the callbacks of `Iter.find`, `any_match`, `all_match`,
// `drop_while`, `flat_map`, `Map.map_values` and `Map.map_keys`. The rule is
// the spec's for every iteration callback: `continue` skips this element,
// `break` stops with what has accumulated, and `break v` answers v for this
// element and then stops. What v means is the operation's own: a predicate's
// answer for find, any_match and all_match, the drop decision for drop_while, the
// element's expansion for flat_map, and the new value or key for the map
// transforms.

// find: `continue` passes over a match, `break` stops with None even when a
// later element matches, `break True` answers this element and `break False`
// stops with None.
func TestIRSignal_FindCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn odd_but_one(x: Int) -> Bool
  if x == 1 then
    continue
  end
  x % 2 == 1
end

fn upto_three(x: Int) -> Bool
  if x > 3 then
    break
  end
  x > 10
end

fn main()
  xs = [1, 2, 3, 4, 5]
  skipped = Iter.find(xs, fn x ->
    if x == 1 then continue
    x % 2 == 1
  end)
  io.print("find continue: ${skipped}")
  stopped = Iter.find(xs, fn x ->
    if x == 2 then break
    x > 4
  end)
  io.print("find break: ${stopped}")
  taken = Iter.find(xs, fn x ->
    if x == 2 then break True
    x > 4
  end)
  io.print("find break True: ${taken}")
  refused = Iter.find(xs, fn x ->
    if x == 2 then break False
    x > 4
  end)
  io.print("find break False: ${refused}")
  entry = Iter.find({"a" => 1, "b" => 2, "c" => 3}, fn (k, v) ->
    if k == "a" then continue
    v % 2 == 1
  end)
  io.print("find over a map: ${entry}")
  io.print("find named: ${Iter.find(xs, odd_but_one)}")
  io.print("find named piped: ${xs |> Iter.find(odd_but_one)}")
  io.print("find bounds an unbounded source: ${Iter.from(1) |> Iter.find(upto_three)}")
end
`, "find continue: Some(3)\nfind break: None\nfind break True: Some(2)\nfind break False: None\n"+
		"find over a map: Some((c, 3))\nfind named: Some(3)\nfind named piped: Some(3)\n"+
		"find bounds an unbounded source: None\n")
}

// any and all: what has accumulated before the answer is decided is False
// for any and True for all, so a bare `break` answers that; `break v`
// answers v. The visits show the walk stops at the break.
func TestIRSignal_AnyAllCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn passes_but_three(x: Int) -> Bool
  if x == 3 then
    continue
  end
  x != 3
end

fn main()
  xs = [1, 2, 3, 4, 5]
  io.print("any continue: ${Iter.any_match(xs, fn x ->
    if x == 3 then continue
    x == 3
  end)}")
  io.print("any break: ${Iter.any_match(xs, fn x ->
    io.print("any visits ${x}")
    if x == 2 then break
    x == 4
  end)}")
  io.print("any break True: ${Iter.any_match(xs, fn x ->
    if x == 2 then break True
    False
  end)}")
  io.print("any break False: ${Iter.any_match(xs, fn x ->
    if x == 2 then break False
    x == 4
  end)}")
  io.print("any over a map: ${Iter.any_match({"a" => 1, "b" => 2}, fn (k, v) ->
    if k == "b" then continue
    v == 2
  end)}")
  io.print("any named: ${Iter.any_match([3, 3], passes_but_three)}")
  io.print("all named: ${Iter.all_match(xs, passes_but_three)}")
  io.print("all break: ${Iter.all_match(xs, fn x ->
    io.print("all visits ${x}")
    if x == 3 then break
    x < 3
  end)}")
  io.print("all break True: ${Iter.all_match(xs, fn x ->
    if x == 2 then break True
    x < 2
  end)}")
  io.print("all break False: ${Iter.all_match(xs, fn x ->
    if x == 1 then break False
    True
  end)}")
  io.print("all over a map: ${{"a" => 1, "b" => 20} |> Iter.all_match(fn (k, v) ->
    if k == "b" then continue
    v < 10
  end)}")
end
`, "any continue: False\nany visits 1\nany visits 2\nany break: False\nany break True: True\n"+
		"any break False: False\nany over a map: False\nany named: False\nall named: True\n"+
		"all visits 1\nall visits 2\nall visits 3\nall break: True\nall break True: True\n"+
		"all break False: False\nall over a map: True\n")
}

// drop_while: the value is the drop decision. `continue` drops the element
// and keeps dropping, `break` and `break True` drop it and end the sequence,
// and `break False` emits it and ends. flat_map: the value is the element's
// expansion, so `break v` emits v's elements and ends.
func TestIRSignal_DropWhileAndFlatMapCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn drop_small(x: Int) -> Bool
  if x == 5 then
    continue
  end
  x < 3
end

fn pair_until_three(x: Int) -> List<Int>
  if x == 3 then
    break [x]
  end
  [x, x * 10]
end

fn main()
  continued = [5, 1, 2, 6, 1] |> Iter.drop_while(fn x ->
    if x == 5 then continue
    x < 3
  end) |> Iter.to_list()
  io.print("drop_while continue: ${continued}")
  broken = [1, 2, 3, 4] |> Iter.drop_while(fn x ->
    if x == 2 then break
    x < 3
  end) |> Iter.to_list()
  io.print("drop_while break: ${broken}")
  kept = [1, 2, 3, 4] |> Iter.drop_while(fn x ->
    if x == 2 then break False
    x < 3
  end) |> Iter.to_list()
  io.print("drop_while break False: ${kept}")
  dropped = [1, 2, 3, 4] |> Iter.drop_while(fn x ->
    if x == 2 then break True
    x < 3
  end) |> Iter.to_list()
  io.print("drop_while break True: ${dropped}")
  io.print("drop_while named: ${Iter.drop_while([5, 1, 2, 6, 1], drop_small) |> Iter.to_list()}")
  io.print("drop_while over a map: ${Iter.drop_while({"a" => 1, "b" => 2, "c" => 3}, fn (k, v) ->
    if k == "b" then break False
    v < 5
  end) |> Iter.to_list()}")
  io.print("drop_while bounds an unbounded source: ${Iter.from(1) |> Iter.drop_while(fn x ->
    if x == 3 then break False
    True
  end) |> Iter.to_list()}")
  io.print("flat_map continue: ${[1, 2, 3] |> Iter.flat_map(fn x ->
    if x == 2 then continue
    [x, x * 10]
  end) |> Iter.to_list()}")
  io.print("flat_map break: ${[1, 2, 3] |> Iter.flat_map(fn x ->
    if x == 2 then break
    [x, x * 10]
  end) |> Iter.to_list()}")
  io.print("flat_map named: ${Iter.flat_map([1, 2, 3, 4], pair_until_three) |> Iter.to_list()}")
  io.print("flat_map lazy inner: ${[1, 2, 3] |> Iter.flat_map(fn x ->
    if x == 2 then break Iter.take(Iter.repeat(x), 3)
    Iter.take(Iter.repeat(x), 1)
  end) |> Iter.to_list()}")
  io.print("flat_map over a map: ${{"a" => 1, "b" => 2} |> Iter.flat_map(fn (k, v) ->
    if k == "a" then continue
    [k, "${v}"]
  end) |> Iter.to_list()}")
  io.print("flat_map bounds an unbounded source: ${Iter.from(1) |> Iter.flat_map(fn x ->
    if x > 2 then break
    [x, x]
  end) |> Iter.to_list()}")
end
`, "drop_while continue: [6, 1]\ndrop_while break: []\ndrop_while break False: [2]\n"+
		"drop_while break True: []\ndrop_while named: [6, 1]\ndrop_while over a map: [(b, 2)]\n"+
		"drop_while bounds an unbounded source: [3]\nflat_map continue: [1, 10, 3, 30]\n"+
		"flat_map break: [1, 10]\nflat_map named: [1, 10, 2, 20, 3]\nflat_map lazy inner: [1, 2, 2, 2]\n"+
		"flat_map over a map: [b, 2]\nflat_map bounds an unbounded source: [1, 1, 2, 2]\n")
}

// Map.map_values and Map.map_keys: `continue` leaves the entry out, `break`
// stops with the entries so far, and `break v` keeps this entry under v and
// stops.
func TestIRSignal_MapTransformCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn tens_but_two(v: Int) -> Int
  if v == 2 then
    continue
  end
  v * 10
end

fn shout_until_b(k: String) -> String
  if k == "b" then
    break "B"
  end
  k + "!"
end

fn main()
  m = {"a" => 1, "b" => 2, "c" => 3}
  io.print("map_values continue: ${Map.map_values(m, fn v ->
    if v == 2 then continue
    v * 10
  end)}")
  io.print("map_values break: ${Map.map_values(m, fn v ->
    if v == 2 then break
    v * 10
  end)}")
  io.print("map_values break value: ${Map.map_values(m, fn v ->
    if v == 2 then break 0
    v * 10
  end)}")
  io.print("map_values named: ${Map.map_values(m, tens_but_two)}")
  io.print("map_values named piped: ${m |> Map.map_values(tens_but_two)}")
  io.print("map_keys continue: ${Map.map_keys(m, fn k ->
    if k == "a" then continue
    k + "!"
  end)}")
  io.print("map_keys break: ${Map.map_keys(m, fn k ->
    if k == "b" then break
    k + "!"
  end)}")
  io.print("map_keys named: ${Map.map_keys(m, shout_until_b)}")
end
`, "map_values continue: {a => 10, c => 30}\nmap_values break: {a => 10}\n"+
		"map_values break value: {a => 10, b => 0}\nmap_values named: {a => 10, c => 30}\n"+
		"map_values named piped: {a => 10, c => 30}\nmap_keys continue: {b! => 2, c! => 3}\n"+
		"map_keys break: {a! => 1}\nmap_keys named: {a! => 1, B => 2}\n")
}

// In a fused pipeline a stage's `break` stops the source, so the consuming
// terminal sees only what the stage emitted before it: without that, the
// filter below would let 6 through to find.
func TestIRSignal_BreakReachesTheConsumingTerminal(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn main()
  stopped = [1, 2, 3, 4, 5, 6] |> Iter.filter(fn x ->
    if x == 4 then break
    x % 2 == 0
  end) |> Iter.find(fn x -> x > 3)
  io.print("filter break reaches find: ${stopped}")
  io.print("find break in a chain: ${[1, 2, 3, 4, 5] |> Iter.map(fn x -> x * 10) |> Iter.find(fn x ->
    if x == 30 then break
    x > 40
  end)}")
  io.print("flat_map break reaches any: ${[1, 2, 3] |> Iter.flat_map(fn x ->
    if x == 2 then break [x]
    [x]
  end) |> Iter.any_match(fn x -> x == 3)}")
  io.print("drop_while break reaches any: ${[1, 2, 3, 4] |> Iter.drop_while(fn x ->
    if x == 1 then break False
    True
  end) |> Iter.any_match(fn x -> x == 4)}")
  io.print("map continue then all: ${[1, 2, 3] |> Iter.map(fn x ->
    if x == 2 then continue
    x
  end) |> Iter.all_match(fn x ->
    if x == 3 then break
    x == 1
  end)}")
end
`, "filter break reaches find: None\nfind break in a chain: None\nflat_map break reaches any: False\n"+
		"drop_while break reaches any: False\nmap continue then all: True\n")
}
