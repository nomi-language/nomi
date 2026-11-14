package format

import "testing"

// An anonymous struct is written with its brace alone, and a field or a
// named argument whose value is its own name is punned as `name:`.
func TestFormat_BraceStructsAndPuns(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"fn main()\n    p = {x: x, y: 2}\nend\n", "fn main()\n    p = {x:, y: 2}\nend\n"},
		{"fn main()\n    p = P{x: x, y:}\nend\n", "fn main()\n    p = P{x:, y:}\nend\n"},
		{"fn main()\n    q = {..p, x: x}\nend\n", "fn main()\n    q = {..p, x:}\nend\n"},
		{"fn main()\n    r = f(body, status: status)\nend\n", "fn main()\n    r = f(body, status:)\nend\n"},
		{"fn main()\n    {x: x, y: b} = p\nend\n", "fn main()\n    {x:, y: b} = p\nend\n"},
		{"fn f(p: {a: Int}) -> {b: Int}\n    {b: p.a}\nend\n", "fn f(p: {a: Int}) -> {b: Int}\n    {b: p.a}\nend\n"},
		{"fn main()\n    x = case p\n        P{x: x} -> x\n        _ -> 0\n    end\nend\n", "fn main()\n    x = case p\n        P{x:} -> x\n        _     -> 0\n    end\nend\n"},
	} {
		got := formatTwice(t, tc.src)
		if got != tc.want {
			t.Errorf("Format(%q) =\n%s\nwant\n%s", tc.src, got, tc.want)
		}
	}
}
