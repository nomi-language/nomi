package analysis

import (
	"strings"
	"testing"
)

// A type parameter only a `where` bound's arguments name is solved from the
// impl the bound's solved subject has, at a direct call and at a pipe stage:
// `Out` in `plus<L, R, Out>(...): Out where L: Combine<R, Out>` is Day for
// `L` Day and `R` Days, and Weeks for `R` Weeks. Before, it was read off the
// enclosing function's return type wherever the call sat, so in a Unit body
// the call was typed Unit.
func TestWhereBound_SolvesAParameterFromTheSubjectsImpl(t *testing.T) {
	const decls = `type Day Int
type Days Int
type Weeks Int

interface Combine<Rhs, Out> {
  fn combine(lhs: self, rhs: Rhs): Out
}

impl Combine<Days, Day> for Day {
  fn combine(lhs: Day, rhs: Days): Day {
    lhs
  }
}

impl Combine<Weeks, Weeks> for Day {
  fn combine(lhs: Day, rhs: Weeks): Weeks {
    rhs
  }
}

fn plus<L, R, Out>(lhs: L, rhs: R): Out where L: Combine<R, Out> {
  Combine.combine(lhs, rhs)
}

fn day(d: Day): Int {
  Day(n) = d
  n
}

fn weeks(w: Weeks): Int {
  Weeks(n) = w
  n
}
`
	rows := []struct {
		name, body, want string
	}{
		{"a direct call in a Unit body", "fn f() {\n  x = plus(Day(1), Days(2))\n  _ = day(x)\n}\n", ""},
		{"the other impl, chosen by the right operand", "fn f() {\n  x = plus(Day(1), Weeks(2))\n  _ = weeks(x)\n}\n", ""},
		{"a pipe stage", "fn f() {\n  x = Day(1) |> plus(Days(2))\n  _ = day(x)\n}\n", ""},
		{"the solved result used at another type", "fn f(): Int {\n  s: String = plus(Day(1), Days(2))\n  String.length(s)\n}\n",
			"expected String, got Day"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := strings.Join(errorTexts(t, decls+r.body), "\n")
			if r.want == "" {
				if got != "" {
					t.Fatalf("want accepted, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, r.want) {
				t.Fatalf("want an error containing %q, got:\n%s", r.want, got)
			}
		})
	}
}
