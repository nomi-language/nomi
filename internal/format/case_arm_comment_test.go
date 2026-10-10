package format

import "testing"

// A case arm's trailing comment does not count toward the arm's width, as
// no trailing comment does. The arms were measured without it, but the
// pipe in the body broke to make room for it, stacking its stages at the
// arm's indent; the next format found the arm too wide and broke every arm.
func TestFormat_CaseArmTrailingCommentDoesNotBreakItsBody(t *testing.T) {
	src := "fn total(t: Tree): Int {\n" +
		"    case t {\n" +
		"        Tree.Leaf(n) -> n\n" +
		"        Tree.Branch(groves) -> groves |> Iter.map(grove_total) |> Iter.reduce(|acc = 0, x| acc + x) // t10\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, src)
}
