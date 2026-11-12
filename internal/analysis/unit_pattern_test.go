package analysis_test

import (
	"testing"
)

// `Unit` is a constant pattern (spec §10 *Literal patterns*): it checks
// against a Unit value at any depth, and it covers its type, so `Ok(Unit)`
// and `Err(e)` are exhaustive over a Result<Unit, E>.
func TestUnitPatternChecksAndCoversItsType(t *testing.T) {
	_, errs := checkSourceWithStdlib(`struct Job
  done: Unit
end

fn f(r: Result<Unit, String>, u: Unit, p: (Int, Unit), j: Job, m: Maybe<Unit>) -> Int
  a = case r
    Ok(Unit) -> 1
    Err(_) -> 2
  end
  b = case u
    Unit -> 1
  end
  c = case p
    (n, Unit) -> n
  end
  d = case j
    Job{done: Unit} -> 1
  end
  e = case m
    Some(Unit) -> 1
    None -> 0
  end
  Unit = u
  a + b + c + d + e
end
`)
	expectNoStdlibErrors(t, errs)
}

func TestUnitPatternIsRefusedWhereItCannotMatch(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"over Int", `fn f(n: Int) -> Int
  case n
    Unit -> 1
    _ -> 2
  end
end
`, `pattern Unit is a Unit, but the value is an Int`},
		{"payload over String", `fn f(r: Result<String, Int>) -> Int
  case r
    Ok(Unit) -> 1
    _ -> 2
  end
end
`, `pattern Unit is a Unit, but the value is a String`},
		{"with a payload", `fn f(r: Result<Unit, Int>) -> Int
  case r
    Ok(Unit(_x)) -> 1
    _ -> 2
  end
end
`, "the pattern `Unit` takes no payload; write `Unit` or `_`"},
		{"Ok(Unit) alone is not exhaustive", `fn f(r: Result<Unit, String>) -> Int
  case r
    Ok(Unit) -> 1
  end
end
`, `non-exhaustive case on Result: missing Err`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			expectStdlibError(t, errs, tc.want)
		})
	}
}
