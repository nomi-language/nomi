package irbuild

import "testing"

// A type argument nothing solves, nested inside a parameter type and passed
// through to the result, where the callee makes no value of it: the
// undetermined-type-argument rule admits these calls, so each must run.
// `Option.flatten(None)` checks None, an `Option<?a>`, against
// `Option<Option<T>>`, and unification binds ?a to `Option<T>` and T to
// nothing. The checker opens T to a variable it records the call at, as it
// records `Option.is_none(None)` at its unsolved T, and the builder fills it
// as any other hole.
func TestIRNestedFreeParam_PassedThroughParameterRuns(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"std flatten", `import std::io

fn main()
  io.print("${Option.flatten(Option.None)}")
  io.print("${Option.is_none(Option.flatten(Option.None))}")
  io.print("${Option.flatten(Option.flatten(Option.None))}")
  io.print("${Option.None |> Option.flatten()}")
end
`, "None\nTrue\nNone\nNone\n"},
		{"user enum nesting", `import std::io

fn flat<T>(m: Option<Option<T>>) -> Option<T>
  case m
    Some(inner) -> inner
    None -> Option.None
  end
end

fn pair<T>(m: Option<(T, Int)>) -> Option<(T, Int)>
  m
end

fn main()
  io.print("${flat(Option.None)}")
  io.print("${Option.is_none(pair(Option.None))}")
end
`, "None\nTrue\n"},
		{"list nesting", `import std::io

fn count<T>(xs: List<List<T>>) -> Int
  Iter.count(xs)
end

fn sizes<T>(m: Option<List<T>>) -> Int
  case m
    Some(xs) -> Iter.count(xs)
    None -> 0
  end
end

fn main()
  io.print("${count([])}")
  io.print("${sizes(Option.None)}")
end
`, "0\n0\n"},
		{"constructor argument", `import std::io

fn unwrap_outer<T, E>(r: Result<Result<T, E>, E>) -> Result<T, E>
  case r
    Ok(inner) -> inner
    Err(e) -> Err(e)
  end
end

fn main()
  io.print("${unwrap_outer(Err("x"))}")
end
`, "Err(x)\n"},
		{"generic caller", `import std::io

fn outer<T>(x: T) -> Bool
  _ = x
  Option.is_none(Option.flatten(Option.None))
end

fn main()
  io.print("${outer(1)}")
end
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
