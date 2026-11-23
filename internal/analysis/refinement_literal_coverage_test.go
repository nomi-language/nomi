package analysis_test

import "testing"

// A literal over a refinement's value tests it as it would the base's value
// (spec §4 *Refinements*): it covers one value, never the type, whether the
// refinement is declared or a narrowing gave it (`x > 0` makes x a
// PositiveInt).

const refinementLiteralDecl = `
refine Port Int when fn port -> port >= 1 and port <= 65535
`

func TestRefinementLiteral_ACaseOfLiteralsIsNotExhaustive(t *testing.T) {
	_, errs := checkSourceWithStdlib(refinementLiteralDecl + `
fn name(p: Port) -> String
    case p
        80 -> "http"
        443 -> "https"
    end
end`)
	expectStdlibError(t, errs, "non-exhaustive case: add a `_` arm")
}

func TestRefinementLiteral_ANarrowedValuesLiteralIsNotExhaustive(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn guarded(x: Int) -> Int
    if x > 0 then
        case x
            7 -> 70
        end
    else
        0
    end
end`)
	expectStdlibError(t, errs, "non-exhaustive case: add a `_` arm")
}

func TestRefinementLiteral_ABindingOfALiteralCanFail(t *testing.T) {
	_, errs := checkSourceWithStdlib(refinementLiteralDecl + `
fn eighty(p: Port) -> Int
    80 = p
    80
end`)
	expectStdlibError(t, errs, "this pattern can fail to match a value of type Port")
}

func TestRefinementLiteral_AnIfOfALiteralTests(t *testing.T) {
	_, errs := checkSourceWithStdlib(refinementLiteralDecl + `
fn is_http(p: Port) -> Bool
    if 80 = p then
        True
    else
        False
    end
end

fn named(p: Port) -> String
    case p
        80 -> "http"
        Port(n) -> "port ${n}"
    end
end`)
	expectNoStdlibErrors(t, errs)
}
