package format

import "testing"

// An `if` condition is parsed with struct literals off, so a type name
// before its `{` loses its parentheses.
func TestFormat_IfConditionTypeNameLosesParens(t *testing.T) {
	formatsKeepingMeaning(t,
		"fn main()\n    a = if (Flag) then 1 else 2\nend\n",
		"fn main()\n    a = if Flag then 1 else 2\nend\n")
}
