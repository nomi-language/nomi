package vmhost_test

import "testing"

// A partial application over a generic callee whose type parameters only a
// later use solves runs: the open slot's type comes from the call of the
// partial (`f("a")`) or a binding's annotation, not from a written
// argument. The checker typed the partial with fresh variables but recorded
// no instantiation at the callee, so the IR builder saw the callee's
// declared `T` and declined. Each written argument is evaluated on each call
// of the partial, as the equivalent lambda evaluates it.
func TestPartialApplication_TypeSolvedByALaterUse(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			name: "inherent function with a default",
			body: "    f = &Box.pad(&1, noted(7))\n    io.inspect(f(\"a\"))\n    io.inspect(f(\"b\"))\n" +
				"    g: (Int) -> #(Int, Int) = &Box.pad(&1)\n    io.inspect(g(3))\n",
			want: "made 7\n#(\"a\", 7)\nmade 7\n#(\"b\", 7)\n#(3, 2)\n",
		},
		{
			name: "same-file generic function with a default",
			body: "    k = &pad(&1, noted(4))\n    io.inspect(k(True))\n    io.inspect(k(False))\n",
			want: "made 4\n#(True, 4)\nmade 4\n#(False, 4)\n",
		},
		{
			name: "Map.get",
			body: "    g = &Map.get(&1, noted(1))\n    io.inspect(g(%{1 => \"a\"}))\n    io.inspect(g(%{2 => \"b\"}))\n",
			want: "made 1\nSome(\"a\")\nmade 1\nNone\n",
		},
		{
			name: "List.head",
			body: "    h = &List.head(&1)\n    io.inspect(h([4, 5]))\n    e: List<String> = []\n    j = &List.head(&1)\n    io.inspect(j(e))\n",
			want: "Some(4)\nNone\n",
		},
		{
			name: "Result.map",
			body: "    r = &Result.map(&1, adder(noted(1)))\n    ok: Result<Int, String> = Ok(1)\n    io.inspect(r(ok))\n    io.inspect(r(Err(\"no\")))\n",
			want: "made 1\nOk(2)\nmade 1\nErr(\"no\")\n",
		},
		{
			name: "Iter.map with the function open",
			body: "    m = &Iter.map([noted(4), 5], &1)\n    io.inspect(m(fn n -> n * 2) |> Iter.to_list())\n    io.inspect(m(fn n -> n + 1) |> Iter.to_list())\n",
			want: "made 4\n[8, 10]\nmade 4\n[5, 6]\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main := `import std/io

struct Box {
    value: Int
}

impl Box {
    fn pad<T>(x: T, n: Int = 2) -> #(T, Int) {
        #(x, n)
    }
}

fn pad<T>(x: T, n: Int = 2) -> #(T, Int) {
    #(x, n)
}

fn noted(n: Int) -> Int {
    io.print("made ${n}")
    n
}

fn adder(n: Int) -> (Int) -> Int {
    fn x -> x + n
}

fn main() {
` + tc.body + "}\n"
			got := runSiblingProgram(t, map[string]string{"main.nomi": main})
			if got != tc.want {
				t.Fatalf("output %q, want %q", got, tc.want)
			}
		})
	}
}
