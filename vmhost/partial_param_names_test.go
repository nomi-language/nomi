package vmhost_test

import "testing"

// A partial application's open parameters cannot shadow a name its callee
// expression starts from. `option.Option.map`'s first parameter is named
// `option`, and the partial's lambda took the callee's parameter names, so
// inside it `option` was the parameter and `option.Option` a field read on it.
// The partial is still called by the callee's names.
func TestPartialApplication_ParameterNamedLikeTheQualifier(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import
    std::io
    std::option
    std::lists
end

fn noted(n: Int) -> Int
    io.print("made ${n}")
    n
end

fn tag(_label: String, n: Int) -> Int
    n
end

fn main()
    m = &option.Option.map(&1, fn n: Int -> n + noted(1))
    io.inspect(m(Some(4)))
    io.inspect(m(option: Some(1)))
    w = &option.Option.with_default(&1, noted(0))
    io.inspect(w(None))
    io.inspect(w(Some(5)))
    h = &lists.List.first(&1)
    io.inspect(h([7]))
    t = &tag(&1, noted(3))
    io.inspect(t(_label: "z"))
end
`})
	want := "made 1\nSome(5)\nmade 1\nSome(2)\nmade 0\n0\nmade 0\n5\nSome(7)\nmade 3\n3\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
