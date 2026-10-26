package format

import "testing"

// A `once` whose value is a pipe lays out as a local binding's does: a
// stacked pipe stays stacked, one level in under `once name =`, and an
// inline pipe that does not fit breaks into that form.
func TestFormat_OncePipeLaysOutAsABindingDoes(t *testing.T) {
	stacked := "    [1, 2, 3]\n    |> Iter.map(fn n -> n * 2)\n    |> Iter.to_list()\n"
	long := "[1, 2, 3, 4, 5, 6, 7, 8, 9, 10] |> Iter.map(fn n -> n * 2222222) |> Iter.filter(fn n -> n > 3) |> Iter.to_list()"
	longBroken := "    [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]\n    |> Iter.map(fn n -> n * 2222222)\n    |> Iter.filter(fn n -> n > 3)\n    |> Iter.to_list()\n"
	cases := []struct {
		name, in, want string
	}{
		{
			name: "stacked",
			in:   "once stacked =\n" + stacked,
			want: "once stacked =\n" + stacked,
		},
		{
			name: "stacked, written on the once line",
			in:   "once stacked = [1, 2, 3]\n|> Iter.map(fn n -> n * 2)\n|> Iter.to_list()\n",
			want: "once stacked =\n" + stacked,
		},
		{
			name: "inline over the width",
			in:   "once long = " + long + "\n",
			want: "once long =\n" + longBroken,
		},
		{
			name: "with a type annotation",
			in:   "once typed: List<Int> =\n" + stacked,
			want: "once typed: List<Int> =\n" + stacked,
		},
		{
			name: "pub",
			in:   "pub once shared = " + long + "\n",
			want: "pub once shared =\n" + longBroken,
		},
		{
			name: "inline that fits",
			in:   "once short = [1, 2, 3] |> Iter.to_list()\n",
			want: "once short = [1, 2, 3] |> Iter.to_list()\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Format(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("Format()\n got: %q\nwant: %q", got, c.want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("not idempotent\nfirst: %q\nagain: %q", got, again)
			}
		})
	}
}
