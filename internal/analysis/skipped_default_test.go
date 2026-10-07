package analysis_test

import (
	"strings"
	"testing"
)

const skippedDefaultDecls = `
fn middle(a: Int, b: Int = 10, c: Int): Int {
  a + b + c
}

fn pick<T>(a: T, b: Int = 10, c: T): T {
  _ = b
  if a == c { a } else { c }
}

fn with_cb(req: Int, opt: Int = 2, cb: (Int) -> Int): Int {
  cb(req + opt)
}

fn double(n: Int): Int {
  n * 2
}
`

// Positional arguments fill parameters in order, so a call cannot leave a
// defaulted parameter empty and fill a required one after it: the spec's
// `middle(1, 2)`. Direct, generic and piped calls each say so.
func TestSkippedDefault_PositionalCannotSkipANonTrailingDefault(t *testing.T) {
	for _, tc := range []struct{ name, call string }{
		{"direct", "middle(1, 2)"},
		{"generic", "pick(1, 2)"},
		{"piped", "1 |> middle(2)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(skippedDefaultDecls + "\nfn demo(): Int {\n  " + tc.call + "\n}\n")
			want := "missing argument for parameter 'c'; positional arguments fill parameters in order " +
				"and cannot skip the defaulted 'b', so pass this one by name (c: ...)"
			for _, e := range errs {
				if e.Message == want {
					return
				}
			}
			t.Fatalf("%s: no %q among %v", tc.call, want, errs)
		})
	}
}

// Named arguments still skip a default, and a trailing callback, lambda or
// function reference, still routes past one, directly and through a pipe.
func TestSkippedDefault_NamedAndTrailingCallbackCallsStayClean(t *testing.T) {
	for _, call := range []string{
		"middle(1, 2, 3)",
		"middle(1, c: 3)",
		"1 |> middle(c: 3)",
		"pick(1, c: 2)",
		"with_cb(1, double)",
		"with_cb(1, |x| x)",
		"1 |> with_cb(double)",
	} {
		_, errs := checkSourceWithStdlib(skippedDefaultDecls + "\nfn demo(): Int {\n  " + call + "\n}\n")
		for _, e := range errs {
			if strings.Contains(e.Message, "missing argument") {
				t.Errorf("%s: %s", call, e.Message)
			}
		}
	}
}
