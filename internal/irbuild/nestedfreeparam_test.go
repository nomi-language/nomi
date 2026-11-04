package irbuild

import "testing"

// A type argument nothing solves, nested inside a parameter type and passed
// through to the result, where the callee makes no value of it: the
// undetermined-type-argument rule admits these calls, so each must run.
// `Maybe.flatten(None)` checks None, a `Maybe<?a>`, against
// `Maybe<Maybe<T>>`, and unification binds ?a to `Maybe<T>` and T to
// nothing. The checker opens T to a variable it records the call at, as it
// records `Maybe.is_none(None)` at its unsolved T, and the builder fills it
// as any other hole.
func TestIRNestedFreeParam_PassedThroughParameterRuns(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"std flatten", `import std::io

fn main() {
  io.print("${Maybe.flatten(Maybe.None)}")
  io.print("${Maybe.is_none(Maybe.flatten(Maybe.None))}")
  io.print("${Maybe.flatten(Maybe.flatten(Maybe.None))}")
  io.print("${Maybe.None |> Maybe.flatten()}")
}
`, "None\nTrue\nNone\nNone\n"},
		{"user enum nesting", `import std::io

fn flat<T>(m: Maybe<Maybe<T>>) -> Maybe<T> {
  case m {
    Some(inner) -> inner
    None -> Maybe.None
  }
}

fn pair<T>(m: Maybe<#(T, Int)>) -> Maybe<#(T, Int)> {
  m
}

fn main() {
  io.print("${flat(Maybe.None)}")
  io.print("${Maybe.is_none(pair(Maybe.None))}")
}
`, "None\nTrue\n"},
		{"list nesting", `import std::io

fn count<T>(xs: List<List<T>>) -> Int {
  Iter.count(xs)
}

fn sizes<T>(m: Maybe<List<T>>) -> Int {
  case m {
    Some(xs) -> Iter.count(xs)
    None -> 0
  }
}

fn main() {
  io.print("${count([])}")
  io.print("${sizes(Maybe.None)}")
}
`, "0\n0\n"},
		{"constructor argument", `import std::io

fn unwrap_outer<T, E>(r: Result<Result<T, E>, E>) -> Result<T, E> {
  case r {
    Ok(inner) -> inner
    Err(e) -> Err(e)
  }
}

fn main() {
  io.print("${unwrap_outer(Err("x"))}")
}
`, "Err(x)\n"},
		{"generic caller", `import std::io

fn outer<T>(x: T) -> Bool {
  _ = x
  Maybe.is_none(Maybe.flatten(Maybe.None))
}

fn main() {
  io.print("${outer(1)}")
}
`, "True\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := AnalyzeSource("main.nomi", tc.src); err != nil {
				t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
			}
			verifyLambdaProgram(t, tc.src, tc.want)
		})
	}
}
