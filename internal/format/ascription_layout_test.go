package format

import "testing"

// `value as Type` has one space on each side of `as`, and keeps the
// parentheses an operand of a tighter operator needs.
func TestFormatAscription(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"fn main() {\n    x = []   as   List<Int>\n}\n",
			"fn main() {\n    x = [] as List<Int>\n}\n",
		},
		{
			"fn main() {\n    x = xs |> Iter.to_list() as List<Int>\n}\n",
			"fn main() {\n    x = xs |> Iter.to_list() as List<Int>\n}\n",
		},
		{
			"fn main() {\n    x = (y as Int) + 1\n}\n",
			"fn main() {\n    x = (y as Int) + 1\n}\n",
		},
		{
			"once admin = #{tags: [] as List<String>, user: `GET /u/{id}` as Route<#{id: Uuid}>}\n",
			"once admin = #{tags: [] as List<String>, user: `GET /u/{id}` as Route<#{id: Uuid}>}\n",
		},
	}
	for _, c := range cases {
		got, err := Format(c.in)
		if err != nil {
			t.Fatalf("Format(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
