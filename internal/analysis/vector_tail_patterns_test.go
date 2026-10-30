package analysis_test

import "testing"

// A vector pattern with elements after its `..` tests them against the
// vector's last elements, so it can fail: it needs an else as a binding,
// takes one, and does not cover a case on its own.
func TestVectorPatternsWithTailsCanFail(t *testing.T) {
	wantNoShapePatternError(t, `    #[1, .., 3] = v else {
        return 0
    }
    1`)
	wantNoShapePatternError(t, `    #[first, ..mid, last] = v else {
        return 0
    }
    first + Vector.length(mid) + last`)
	wantShapePatternError(t, `    #[1, .., 3] = v
    1`, "this pattern can fail to match a value of type Vector<Int>")
	wantShapePatternError(t, `    #[first, .., last] = v
    first + last`, "this pattern can fail to match a value of type Vector<Int>")
	wantShapePatternError(t, `    case v {
        #[1, .., 3] -> 1
    }`, "non-exhaustive case on Vector<Int>: missing #[]")
	wantShapePatternError(t, `    case v {
        #[1, .., 3] -> 1
        #[] -> 0
        #[_] -> 0
    }`, "non-exhaustive case on Vector<Int>: missing #[_, .., _]")
	wantShapePatternError(t, `    case v {
        #[] -> 0
        #[_] -> 0
        #[_, _, _] -> 0
        #[_, .., 3] -> 1
    }`, "non-exhaustive case on Vector<Int>: missing #[_, _]")
}

// Patterns with tails still cover what they match: every length from their
// element count on, with the tails at the end.
func TestVectorPatternsWithTailsCover(t *testing.T) {
	wantNoShapePatternError(t, `    case v {
        #[] -> 0
        #[_] -> 1
        #[_, .., _] -> 2
    }`)
	wantNoShapePatternError(t, `    case v {
        #[] -> 0
        #[.., last] -> last
    }`)
	wantNoShapePatternError(t, `    case v {
        #[1, .., 3] -> 0
        #[..] -> 1
    }`)
	wantNoShapePatternError(t, `    case v {
        #[a, _, _] -> a
        #[x, .., _] -> x
        #[] -> 0
        #[_] -> 1
    }`)
	wantNoShapePatternError(t, `    #[..all, last] = v else {
        return 0
    }
    Vector.length(all) + last`)
}
