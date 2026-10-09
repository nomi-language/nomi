package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// qualifiedValueLeaf declares the owners the programs below name through the
// `leaf` file qualifier.
const qualifiedValueLeaf = `pub type UserId Int

pub struct Box {
    value: Int
}

impl Box {
    pub fn twice(b: Box): Int { b.value * 2 }
    pub fn add(b: Box, n: Int): Int { b.value + n }
}

impl Display for Box {
    fn to_string(b: Box): String { "box ${b.value}" }
}

pub enum Shape {
    Line(Int)
    embeds UserId
    Dot
}
`

// An owner's function or positional constructor named through a file
// qualifier is a function value, as the same name is without the qualifier:
// an inherent function, an interface impl's function, a positional variant,
// an embedded wrapping distinct, a std owner's function and variant, and a
// partial application of one.
func TestQualifiedOwnerFuncValue_Runs(t *testing.T) {
	for _, tc := range []struct {
		name, imp, body, want string
	}{
		{
			name: "inherent function",
			imp:  "leaf",
			body: "    f = leaf.Box.twice\n    io.print(Int.to_string(f(leaf.Box{value: 3})))\n",
			want: "6\n",
		},
		{
			name: "inherent function passed to Iter.map",
			imp:  "leaf",
			body: "    [leaf.Box{value: 1}, leaf.Box{value: 4}]\n        |> Iter.map(leaf.Box.twice)\n        |> Iter.map(Int.to_string)\n        |> String.join(\",\")\n        |> io.print()\n",
			want: "2,8\n",
		},
		{
			name: "positional variant",
			imp:  "leaf",
			body: "    g = leaf.Shape.Line\n    io.print(\"${g(4) == leaf.Shape.Line(4)}\")\n",
			want: "True\n",
		},
		{
			name: "positional variant passed to Iter.map",
			imp:  "leaf",
			body: "    shapes = [1, 2] |> Iter.map(leaf.Shape.Line) |> Iter.to_list()\n    io.print(\"${shapes == [leaf.Shape.Line(1), leaf.Shape.Line(2)]}\")\n",
			want: "True\n",
		},
		{
			name: "interface impl function",
			imp:  "leaf",
			body: "    s = leaf.Box.to_string\n    io.print(s(leaf.Box{value: 5}))\n",
			want: "box 5\n",
		},
		{
			name: "embedded wrapping distinct",
			imp:  "leaf",
			body: "    e = leaf.Shape.UserId\n    io.print(\"${e(8) == leaf.Shape.UserId(8)}\")\n",
			want: "True\n",
		},
		{
			name: "partial application",
			imp:  "leaf",
			body: "    p = leaf.Box.add(_, 1)\n    io.print(Int.to_string(p(leaf.Box{value: 9})))\n",
			want: "10\n",
		},
		{
			name: "std owner function",
			imp:  "std/json",
			body: "    e = json.Json.encode\n    io.print(e(json.Json.Int(4)))\n",
			want: "4\n",
		},
		{
			name: "std positional variant",
			imp:  "std/json",
			body: "    i = json.Json.Int\n    io.print(\"${i(3) == json.Json.Int(3)}\")\n",
			want: "True\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			main := "import {\n    std/io\n    " + tc.imp + "\n}\n\nfn main() {\n" + tc.body + "}\n"
			for name, src := range map[string]string{
				"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
				"leaf.nomi": qualifiedValueLeaf,
				"main.nomi": main,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "main.nomi")
			if err := vmhost.Check(path); err != nil {
				t.Fatalf("check: %v", err)
			}
			prog, err := vmhost.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var out bytes.Buffer
			if err := prog.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("run: %v", err)
			}
			if out.String() != tc.want {
				t.Fatalf("output %q, want %q", out.String(), tc.want)
			}
		})
	}
}
