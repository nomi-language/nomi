package analysis_test

import (
	"testing"
)

// `tap f` runs a function by name: a plain one, a qualified one, a generic
// one solved from the piped value, one whose parameters past the first
// have defaults, and a local binding that holds a function.
func TestPipeTapStage_FunctionByName(t *testing.T) {
	decls := "import std::io\n\nfn log_it(n: Int)\n    io.print(n)\nend\n\nfn labeled(n: Int, label: String = \"n\")\n    io.print(\"${label} ${n}\")\nend\n\nfn show<T>(v: T) where T: Display\n    io.print(v)\nend\n\nfn total(xs: Iter<Int>)\n    io.print(Iter.sum(xs))\nend\n\n"
	for name, body := range map[string]string{
		"plain":             "fn f() -> Int\n    1 |> tap log_it\nend\n",
		"qualified":         "fn f() -> Int\n    1 |> tap io.print\nend\n",
		"generic":           "fn f() -> String\n    \"a\" |> tap show\nend\n",
		"defaults":          "fn f() -> Int\n    1 |> tap labeled\nend\n",
		"local binding":     "fn f() -> Int\n    g = fn n: Int -> io.print(n)\n    1 |> tap g\nend\n",
		"a list to an Iter": "fn f() -> List<Int>\n    [1, 2] |> tap total\nend\n",
		"mid pipeline":      "fn f() -> Int\n    \"ab\" |> tap io.print |> String.length() |> tap log_it\nend\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// A `tap` function by name takes the piped value and returns Unit; each
// other function, and a name that holds no function, is an error at the
// name.
func TestPipeTapStage_FunctionByNameRejections(t *testing.T) {
	decls := "import std::io\n\nfn double(n: Int) -> Int\n    n * 2\nend\n\nfn pair(a: Int, b: Int)\n    io.print(a + b)\nend\n\nfn says(s: String)\n    io.print(s)\nend\n\nfn same<T>(v: T) -> T\n    v\nend\n\n"
	// decls is 18 lines; each body's tap is on line 20, its name at col 14.
	for _, tc := range []struct{ name, body, want string }{
		{
			"returns a value",
			"fn f() -> Int\n    1 |> tap double\nend\n",
			"`tap` passes its input on unchanged, so its function must return Unit; `double` returns Int\nhelp: use `|> double()` to replace the value with the function's result",
		},
		{
			"generic returning its input",
			"fn f() -> Int\n    1 |> tap same\nend\n",
			"`tap` passes its input on unchanged, so its function must return Unit; `same` returns Int\nhelp: use `|> same()` to replace the value with the function's result",
		},
		{
			"two parameters",
			"fn f() -> Int\n    1 |> tap pair\nend\n",
			"a `tap` function takes one parameter, the piped value; `pair` takes 2",
		},
		{
			"another parameter type",
			"fn f() -> Int\n    1 |> tap says\nend\n",
			"`says` takes a String, but the pipe passes an Int\nhelp: Int implements Display; add `|> Display.to_string()` before this stage",
		},
		{
			"no function",
			"fn f(count: Int) -> Int\n    1 |> tap count\nend\n",
			"`tap` takes a function, but `count` is an Int\nhelp: write `tap fn v -> ...` to run an expression on the piped value",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + tc.body)
			got := errorsAt(errs, 20, 14)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("errors at 20:14 = %q, want exactly [%q]; all: %v", got, tc.want, errs)
			}
		})
	}
}
