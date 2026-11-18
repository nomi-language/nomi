package vmhost_test

import "testing"

// A partial of a local copy of a function (`g = sub`) is the copied
// function's partial, with its defaults, whatever the name `sub` means
// where the partial is written: a later binding or a nested fn of that
// name is another thing.
func TestPartial_OfACopyIgnoresALaterBindingOfTheCopiedName(t *testing.T) {
	for _, tc := range []struct{ name, shadow, last, want string }{
		{"a value", "    sub = 7\n", "sub", "7 9 7\n"},
		{"a nested fn", "    fn sub(a: Int, b: Int = 100) -> Int\n        a + b\n    end\n", "sub(1)", "7 9 101\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runSourceOutput(t, `import
    std::io
end

fn sub(a: Int, b: Int = 1) -> Int
    a - b
end

fn main()
    g = sub
`+tc.shadow+`    h = &g(&1, 3)
    k = &g(&1)
    io.print("${h(10)} ${k(10)} ${`+tc.last+`}")
end
`)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
