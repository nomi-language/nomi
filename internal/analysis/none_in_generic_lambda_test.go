package analysis_test

import "testing"

// `None` in the first branch of an `if` that a lambda passed to a generic
// returns is a `Maybe` of a fresh inference variable, which the other branch
// solves. It was the nominal `Maybe<T>`, and the `if` was rejected as a
// branch mismatch against `Maybe<Float>`.
func TestNoneInGenericLambdaBranch_Checks(t *testing.T) {
	expectClean(t, checkWithStdlib(`
fn apply<T, U>(x: T, f: (T) -> U): U {
  f(x)
}

fn main() {
  a = apply(1, |x| if x > 5 { None } else { Some(2.5) })
  b = Maybe.map(Some(1), |x| if x > 5 { None } else { Some(2.5) })
  c = [1] |> Iter.map(|x| if x > 5 { None } else { Some(2.5) }) |> Iter.to_list()
  _ = a
  _ = b
  _ = c
}
`))
}
