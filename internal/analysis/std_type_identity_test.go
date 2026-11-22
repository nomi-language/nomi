package analysis_test

import "testing"

// A rule about a std type reads the type's identity (isStdType), so these
// check against the stdlib, whose Result, AssertionFailure and Bool they
// name. A fixture declaring its own `Result` or `Bool` with no prelude
// declares a program's type, which no such rule takes for std's.

// An assertion in a function returning std's Result exits as its Err.
func TestAssertionsOutsideTestTypeCheckWithResultReturn(t *testing.T) {
	errs := checkWithStdlib(`import std::assertions.{AssertionFailure}

fn demo() -> Result<Unit, AssertionFailure>
    assert 1 == 1
    refute 1 == 2
    Result.Ok(begin end)
end
`)
	expectClean(t, errs)
}

// std's Bool embeds its True and False, so either is a Bool.
func TestBoolVariantsCanReturnAsBool(t *testing.T) {
	errs := checkWithStdlib(`fn implicit() -> Bool
    True
end

fn explicit() -> Bool
    return False
end
`)
	expectClean(t, errs)
}
