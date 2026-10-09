package format

import "testing"

// An import block holding only comments keeps them inside its braces. It
// used to come out as `import {}`. Found by FuzzFormatKeepsMeaning.
func TestFormat_EmptyImportBlockKeepsComments(t *testing.T) {
	formatsKeepingMeaning(t, "import{//00\n}\n", "import {\n    //00\n}\n")
	formatsKeepingMeaning(t, "import {\n}\n", "import {}\n")
}
