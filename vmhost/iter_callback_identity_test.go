package vmhost_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// std's Iter imported under another name is std's Iter: `break` in
// `Seq.map`'s and `Seq.reduce`'s lambdas stops the iteration, a stateless
// `Seq.loop` takes its callback alone, `Seq.filter` over a range gives its
// callback the range's elements, and `Seq.is_empty` narrows its source.
func TestIterCallback_IterUnderAnotherNameRuns(t *testing.T) {
	out, err := runVM(t, `import std::io
import std::iter.{Iter as Seq}

fn first_or_zero(xs: List<Int>) -> Int
    if not Seq.is_empty(xs) then NonEmptyList.first(xs) else 0
end

fn main()
    xs = [1, 2, 3, 4]
    io.print(Seq.map(xs, fn x -> if x > 2 then break else x * 10) |> Seq.to_list())
    io.print(xs |> Seq.reduce(0, fn acc, x -> if x > 2 then break acc else acc + x))
    Seq.loop(fn ->
        io.print("once")
        break
    end)
    io.print(1..=5 |> Seq.filter(fn d -> 60 % d == 0) |> Seq.count())
    io.print(first_or_zero([7]))
end
`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if want := "[10, 20]\n3\nonce\n5\n7\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// A type imported under the name `Iter`, std's Generator or a program's
// own, is not std's Iter: a lambda its `map` takes catches no `break`, its
// `map` gives the lambda no element range, and its `is_empty` narrows
// nothing. Each is reported where it is written, where reading the name
// made the first reach the builder and the other two fail at run time.
func TestIterCallback_AnotherTypeNamedIterIsNotStds(t *testing.T) {
	const bag = `pub struct Bag
    n: Int
end

impl Bag
    pub fn map(xs: List<Int>, f: (Int) -> Int) -> List<Int>
        [f(0), ..xs]
    end

    pub fn is_empty(_xs: List<Int>) -> Bool
        False
    end
end
`
	cases := []struct {
		name string
		bag  string
		main string
		want string
	}{
		{
			name: "a break in std's Generator.map",
			main: `import std::io
import std::random.{Generator as Iter, Seed}

fn main()
    gen = Iter.map(Iter.int(1..=6), fn x -> if x > 2 then break else x)
    (n, _) = Iter.step(gen, Seed.from_int(1))
    io.print(n)
end
`,
			want: "'break' can only be used inside an iter-callback",
		},
		{
			name: "a break in a program's map",
			bag:  bag,
			main: `import std::io
import bag.{Bag as Iter}

fn main()
    io.print(Iter.map([1, 2, 3], fn x -> if x > 2 then break else x))
end
`,
			want: "'break' can only be used inside an iter-callback",
		},
		{
			name: "an element range from a program's map",
			bag:  bag,
			main: `import std::io
import bag.{Bag as Iter}

fn main()
    io.print(Iter.map([1, 2, 3], fn d -> 10 % d))
end
`,
			want: "`%` divides by a NonZeroInt; `d` is an Int",
		},
		{
			name: "a narrowing from a program's is_empty",
			bag:  bag,
			main: `import std::io
import bag.{Bag as Iter}

fn first_or_zero(xs: List<Int>) -> Int
    if not Iter.is_empty(xs) then NonEmptyList.first(xs) else 0
end

fn main()
    io.print(first_or_zero([]))
end
`,
			want: "argument 1: expected NonEmptyList<T>, got List<Int>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n",
				"main.nomi": tc.main,
			}
			if tc.bag != "" {
				files["bag.nomi"] = tc.bag
			}
			writeFiles(t, dir, files)
			_, err := vmhost.Load(filepath.Join(dir, "main.nomi"))
			if err == nil {
				t.Fatal("the front end admits this program, though the call it relies on is not std's Iter")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q lacks %q", err, tc.want)
			}
		})
	}
}
