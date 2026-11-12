package format

import "testing"

// A comment after an import block's `end` survives when the block combines
// with other imports: it ends the block's last entry's line, or, when that
// line has its own comment, goes on its own line before the combined `end`.
func TestFormat_CombinedImportBlockKeepsItsTrailingComment(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"import\n    A\nend // c\nimport B\n",
			"import\n    A // c\n    B\nend\n",
		},
		{
			"import\n    A\n    C\nend // c\nimport B\n",
			"import\n    A\n    B\n    C // c\nend\n",
		},
		{
			"import\n    A // a\n    C // x\nend // c\nimport B\n",
			"import\n    A // a\n    B\n    C // x\n    // c\nend\n",
		},
		{
			"import B\npub import\n    a.X\nend // c\n",
			"import B\npub import a.X // c\n",
		},
		{
			"//00000000000\nimport\nA000000000000000000000000\nend //0\nimport A\n",
			"//00000000000\nimport\n    A\n    A000000000000000000000000 //0\nend\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
