package format

import "testing"

// An import block holding only comments keeps them above its `end`. It
// used to come out empty. Found by FuzzFormatKeepsMeaning.
func TestFormat_EmptyImportBlockKeepsComments(t *testing.T) {
	formatsKeepingMeaning(t, "import //00\nend\n", "import\n    //00\nend\n")
	formatsKeepingMeaning(t, "import end\n", "import end\n")
}
