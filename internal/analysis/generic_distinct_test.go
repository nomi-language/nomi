package analysis_test

import (
	"strings"
	"testing"
)

// A generic distinct's constructor solves its type argument from the value
// it wraps, and every use checks that argument: a construction, an
// annotation's arity, and the type a destructuring binds.
func TestGenericDistinctChecksItsTypeArgument(t *testing.T) {
	accepted := `type Tagged<T> (String, T)

fn value<T>(Tagged((_, v)): Tagged<T>) -> T
  v
end

fn f() -> Int
  value(Tagged(("n", 1))) + 1
end
`
	if _, errs := checkSourceWithStdlib(accepted); len(errs) != 0 {
		t.Fatalf("the front end rejects a generic distinct: %v", errs)
	}
	for name, c := range map[string]struct{ src, want string }{
		"construction": {`type Tagged<T> (String, T)

fn f() -> Tagged<String>
  Tagged(("s", 1))
end
`, "expected Tagged<String>, got Tagged<Int>"},
		"arity": {`type Tagged<T> (String, T)

fn f(t: Tagged<Int, Int>) -> Int
  1
end
`, "`Tagged` expects 1 type argument, got 2"},
		"destructuring": {`type Tagged<T> (String, T)

fn f() -> String
  Tagged((_, v)) = Tagged(("s", 1))
  v
end
`, "expected String, got Int"},
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(c.src)
			for _, e := range errs {
				if strings.Contains(e.Message, c.want) {
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", c.want, errs)
		})
	}
}
