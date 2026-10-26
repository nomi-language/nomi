package analysis_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A wrong argument count is reported where the count goes wrong, whatever
// the form of the call: over the call when it passes too few, and over the
// arguments past the callee's last parameter when it passes too many. A
// pipe stage counts the piped value.
func TestArityErrorsAreAtTheCall(t *testing.T) {
	const decls = `struct P {
  x: Int
}

impl P {
  fn get(p: P) -> Int {
    p.x
  }
}

enum E {
  A(Int)
  B
}

fn f(a: Int) -> Int {
  a
}

fn add(a: Int, b: Int) -> Int {
  a + b
}

fn opt(a: Int, b: Int = 2) -> Int {
  a + b
}

fn id<T>(a: T) -> T {
  a
}

`
	// Each call is on the line after `fn main() {`, at column 7.
	for _, tc := range []struct {
		name, call, message, span string
	}{
		{"std host fn through its type, too few", "Decimal.round(1d)",
			"expected 3 arguments, got 1", "+1:7-+1:24"},
		{"std host fn through its type, two short", "Decimal.divide(1d, 3d)",
			"expected 4 arguments, got 2", "+1:7-+1:29"},
		{"std host fn through its type, too many", `String.trim("a", "b")`,
			"expected 1 arguments, got 2", "+1:24-+1:27"},
		{"std Nomi-bodied fn through its type, too many", "List.head([1], 2, 3)",
			"expected 1 arguments, got 3", "+1:22-+1:26"},
		{"user inherent fn, too many", "P.get(#P{x: 1}, 2)",
			"expected 1 arguments, got 2", "+1:23-+1:24"},
		{"user inherent fn, too few", "P.get()",
			"expected 1 arguments, got 0", "+1:7-+1:14"},
		{"interface-qualified, too few", "Display.to_string()",
			"expected 1 arguments, got 0", "+1:7-+1:26"},
		{"interface-qualified generic interface, too few", "Divide.divide(1d)",
			"expected 2 arguments, got 1", "+1:7-+1:24"},
		{"type-qualified impl fn, too few", "Int.to_string()",
			"expected 1 arguments, got 0", "+1:7-+1:22"},
		{"type-qualified impl fn, too many", "Int.to_string(1, 2)",
			"expected 1 arguments, got 2", "+1:24-+1:25"},
		{"file-qualified std fn, too few", "io.print()",
			"expected 1 arguments, got 0", "+1:7-+1:17"},
		{"bare fn, too many", "f(1, 2)",
			"expected 1 arguments, got 2", "+1:12-+1:13"},
		{"bare fn, too few", "f()",
			"expected 1 arguments, got 0", "+1:7-+1:10"},
		{"generic fn, too many", "id(1, 2)",
			"expected 1 arguments, got 2", "+1:13-+1:14"},
		{"generic fn with type arguments, too few", "id<Int>()",
			"expected 1 arguments, got 0", "+1:7-+1:16"},
		{"generic fn, too many type arguments", "id<Int, Int>(1)",
			"expected 1 type argument(s), got 2", "+1:7-+1:22"},
		{"variant constructor, too many", "E.A(1, 2)",
			"expected 1 arguments, got 2", "+1:14-+1:15"},
		{"pipe stage, too many counting the piped value", "1 |> add(2, 3)",
			"expected 2 arguments, got 3 (counting the piped value)", "+1:19-+1:20"},
		{"pipe stage into a std host fn, too few", "1d |> Decimal.round()",
			"missing argument for parameter 'scale'", "+1:13-+1:28"},
		{"defaults, too few", "opt()",
			"expected 1 to 2 arguments, got 0", "+1:7-+1:12"},
		{"defaults, too many", "opt(1, 2, 3)",
			"expected 1 to 2 arguments, got 3", "+1:17-+1:18"},
		{"named argument that repeats a positional one", "add(1, a: 2)",
			"parameter 'a' already has a value", "+1:14-+1:15"},
		{"unknown named argument", "opt(1, c: 3)",
			"no parameter named 'c'", "+1:14-+1:15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "import std/io\n\n" + decls + "fn main() {\n  _ = " + tc.call + "\n}\n"
			if strings.HasPrefix(tc.call, "io.") {
				src = "import std/io\n\n" + decls + "fn main() {\n  " + strings.Repeat(" ", 4) + tc.call + "\n}\n"
			}
			callLine := strings.Count(src[:strings.Index(src, "fn main()")], "\n") + 2
			_, errs := checkSourceWithStdlib(src)
			errs = analysis.CompleteSpans(src, errs)
			var hit *analysis.TypeError
			for i := range errs {
				if errs[i].Message == tc.message {
					hit = &errs[i]
					break
				}
			}
			if hit == nil {
				var all []string
				for _, e := range errs {
					all = append(all, e.Error())
				}
				t.Fatalf("no error %q; got:\n  %s", tc.message, strings.Join(all, "\n  "))
			}
			want := strings.ReplaceAll(tc.span, "+1", strconv.Itoa(callLine))
			if got := shapeOf(*hit); got != want {
				t.Errorf("span = %s, want %s", got, want)
			}
		})
	}
}

// A call through a function value, a struct constructor and a test
// assertion are counted as a call by name is, and their errors are placed
// the same way.
func TestArityErrorsOfOtherCalleesAreAtTheCall(t *testing.T) {
	for _, tc := range []struct {
		name, src, message, span string
	}{
		{"lambda, too many",
			"fn main() {\n  g = fn a: Int -> a\n  _ = g(1, 2)\n}\n",
			"expected 1 arguments, got 2", "3:12-3:13"},
		{"lambda, too few",
			"fn main() {\n  g = fn a: Int -> a\n  _ = g()\n}\n",
			"expected 1 arguments, got 0", "3:7-3:10"},
		{"a named function with defaults held as a value, too few",
			"fn opt(a: Int, b: Int = 2) -> Int {\n  a + b\n}\n\nfn main() {\n  g = opt\n  _ = g(1)\n}\n",
			"expected 2 arguments, got 1: `g` holds the function `opt` as a value, and defaults apply only to a call of `opt` by name",
			"7:7-7:11"},
		{"a function-typed parameter, too few",
			"fn twice(h: (Int, Int) -> Int) -> Int {\n  h(1)\n}\n",
			"expected 2 arguments, got 1", "2:3-2:7"},
		{"a struct constructor, too many",
			"struct Q {\n  x: Int\n}\n\nfn main() {\n  _ = Q(#{x: 1}, 2)\n}\n",
			"Q takes 1 argument, got 2; expected Q(#{...}) with the struct's fields", "6:18-6:19"},
		{"testing.check, too many",
			"import std/testing\n\ntest \"t\" {\n  _ = testing.check(true, false)\n  assert true\n}\n",
			"testing.check expects 1 argument, got 2", "4:27-4:32"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			errs = analysis.CompleteSpans(tc.src, errs)
			var all []string
			for _, e := range errs {
				if e.Message == tc.message {
					if got := shapeOf(e); got != tc.span {
						t.Errorf("span = %s, want %s", got, tc.span)
					}
					return
				}
				all = append(all, e.Error())
			}
			t.Fatalf("no error %q; got:\n  %s", tc.message, strings.Join(all, "\n  "))
		})
	}
}
