package format

import "testing"

// A case arm's trailing comment does not count toward the arm's width, as
// no trailing comment does. The arms were measured without it, but the
// pipe in the body broke to make room for it, stacking its stages at the
// arm's indent; the next format found the arm too wide and broke every arm.
func TestFormat_CaseArmTrailingCommentDoesNotBreakItsBody(t *testing.T) {
	src := "fn total(t: Tree) -> Int\n" +
		"    case t\n" +
		"        Tree.Leaf(n)    -> n\n" +
		"        Tree.Branch(gs) -> gs |> Iter.map(grove_total) |> Iter.reduce(0, fn acc, x -> acc + x) // t10\n" +
		"    end\n" +
		"end\n"
	formatsKeepingMeaning(t, src, src)
}

// A jump below its arm's `->` with a comment after it is the arm's body,
// the comment on the jump (parser.singleBody). The formatter wrote the
// jump on the pattern's line and dropped the comment.
func TestFormat_CaseArmJumpKeepsItsComment(t *testing.T) {
	src := "fn main()\n" +
		"    dv = Iter.find([1, 2, 3], fn dx ->\n" +
		"        case dx\n" +
		"            -1 ->\n" +
		"                continue // t53\n" +
		"\n" +
		"            _ -> dx == 1\n" +
		"        end\n" +
		"    end)\n" +
		"end\n"
	want := "fn main()\n" +
		"    dv = Iter.find([1, 2, 3], fn dx ->\n" +
		"        case dx\n" +
		"            -1 ->\n" +
		"                continue // t53\n" +
		"\n" +
		"            _ ->\n" +
		"                dx == 1\n" +
		"        end\n" +
		"    end)\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)
}

// An arm's one expression on the `->`'s line and the same expression on
// the line below are the same arm. When the arms break, the formatter moves
// a body below its `->`, and a trailing comment goes with it, where the
// parser keeps the body as a block of one statement to hold the comment.
func TestFormat_BrokenArmWithACommentKeepsMeaning(t *testing.T) {
	src := "fn main()\n" +
		"    g = case 3\n" +
		"        1 -> \"Hi\"\n" +
		"        n -> \"Hello \" + \"name_rather_long_rather_long_rather_long_rather_long name_rather_long_rather_long_rather_long\" // t11\n" +
		"    end\n" +
		"end\n"
	want := "fn main()\n" +
		"    g = case 3\n" +
		"        1 ->\n" +
		"            \"Hi\"\n" +
		"\n" +
		"        n ->\n" +
		"            \"Hello \" + \"name_rather_long_rather_long_rather_long_rather_long name_rather_long_rather_long_rather_long\" // t11\n" +
		"    end\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)
}

// An `assert` arm body on the `->`'s line is an expression and below it a
// statement of a block; when the arms break the formatter writes it below.
func TestFormat_BrokenAssertArmKeepsMeaning(t *testing.T) {
	src := "test \"t\"\n" +
		"    case 1\n" +
		"        1 -> assert True\n" +
		"        _ -> assert \"name_rather_long_rather_long_rather_long_rather_long\" == \"name_rather_long_rather_long\"\n" +
		"    end\n" +
		"end\n"
	want := "test \"t\"\n" +
		"    case 1\n" +
		"        1 ->\n" +
		"            assert True\n" +
		"\n" +
		"        _ ->\n" +
		"            assert \"name_rather_long_rather_long_rather_long_rather_long\"\n" +
		"                == \"name_rather_long_rather_long\"\n" +
		"    end\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)
}

// A comment between a case's last arm and its `end`, when that arm's body
// is below its `->`, is written at the body's indent: the parser reads the
// lines up to `end` as the body's, so at the arms' indent the next format
// moved it.
func TestFormat_CaseEndCommentAfterABrokenArmIsStable(t *testing.T) {
	src := "fn drop_all<T>(xs: List<T>) -> Int\n" +
		"    case xs\n" +
		"        [] -> 0\n" +
		"        [_, ..rest] -> Iter.map([rest], fn r -> 1 + drop_all(r)) |> Iter.reduce(0, fn a_rather_long, b -> a_rather_long + b + 1000000000)\n" +
		"  // s7\n" +
		"    end\n" +
		"end\n"
	want := "fn drop_all<T>(xs: List<T>) -> Int\n" +
		"    case xs\n" +
		"        [] ->\n" +
		"            0\n" +
		"\n" +
		"        [_, ..rest] ->\n" +
		"            Iter.map([rest], fn r -> 1 + drop_all(r))\n" +
		"            |> Iter.reduce(0, fn a_rather_long, b -> a_rather_long + b + 1000000000)\n" +
		"            // s7\n" +
		"    end\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)
	formatsTo(t, want, want)
}
