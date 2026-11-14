package vm_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Iter.zip pulls a right side that steps directly (a List, a Vector, an Int
// range) through its cursor and any other through a coroutine; both answer
// the same pairs, the shorter side ends the zip, and a zip read twice starts
// its cursor over.
func TestZip_EveryRightSideAnswersTheSamePairs(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std::io

fn main()
    xs = [1, 2, 3]
    v: Vector<String> = Vector["a", "b", "c", "d"]
    io.print("${Iter.zip(xs, v) |> Iter.to_list()}")
    io.print("${Iter.zip(xs, [10, 20]) |> Iter.to_list()}")
    io.print("${Iter.zip(xs, 5..100) |> Iter.to_list()}")
    io.print("${Iter.zip(xs, Iter.map(v, fn s -> "${s}!")) |> Iter.to_list()}")
    z = Iter.zip(v, xs)
    io.print("${Iter.to_list(z)} ${Iter.count(z)}")
    io.print("${Iter.zip(xs, v) |> Iter.reduce(0, fn acc, (n, _) -> acc + n)}")
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := `[(1, a), (2, b), (3, c)]
[(1, 10), (2, 20)]
[(1, 5), (2, 6), (3, 7)]
[(1, a!), (2, b!), (3, c!)]
[(a, 1), (b, 2), (c, 3)] 3
6
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}
