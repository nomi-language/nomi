package analysis_test

import "testing"

// A pattern whose head an import aliased (`import shapes.Holder.{Of as
// O}`, then `O{value:}`) covers the variant the head names, by the
// checker's record of it, and no other: a `case` of it alone is not
// exhaustive, and with the other variants it is.
func TestCoverage_AnAliasedHeadCoversItsVariant(t *testing.T) {
	alone := "import shapes\nimport shapes.Holder.{Of as O}\n\n" +
		"fn show(h: shapes.Holder<Int>) -> Int\n    case h\n        O{value:} -> value\n    end\nend\n"
	_, errs := checkProjectWithFiles(t, alone, map[string]string{"shapes.nomi": drillGenericShapes})
	expectErrorContaining(t, errs, "non-exhaustive case on Holder: missing Wrap, Empty")

	all := "import shapes\nimport shapes.Holder.{Of as O, Wrap as W, Empty}\n\n" +
		"fn show(h: shapes.Holder<Int>) -> Int\n    case h\n        O{value:} -> value\n        W(v) -> v\n        Empty -> 0\n    end\nend\n"
	_, errs = checkProjectWithFiles(t, all, map[string]string{"shapes.nomi": drillGenericShapes})
	expectNoStdlibErrors(t, errs)
}

// A list pattern's rest that is itself a list pattern names the elements it
// matches (`[..[]]` is `[]`, `[a, ..[b]]` is `[a, b]`), so it covers those
// lengths and no others; a rest another pattern constrains covers none.
func TestCoverage_AListRestPatternCoversItsElements(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn only_empty(xs: List<Int>) -> String
    case xs
        [..[]] -> "empty"
    end
end`)
	expectStdlibError(t, errs, "non-exhaustive case")

	_, errs = checkSourceWithStdlib(`
fn bind(xs: List<Int>) -> Int
    [a, ..[b]] = xs
    a + b
end`)
	expectStdlibError(t, errs, "this pattern can fail to match a value of type List<Int>")

	_, errs = checkSourceWithStdlib(`
fn lengths(xs: List<Int>) -> String
    case xs
        [..[]] -> "empty"
        [_, ..[_]] -> "two"
        [_, ..[_, _, ..]] -> "three or more"
        [_] -> "one"
    end
end`)
	expectNoStdlibErrors(t, errs)
}
