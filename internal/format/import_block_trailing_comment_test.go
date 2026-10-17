package format

import "testing"

// A comment after an import block's `}` survives when the block combines
// with other imports: it ends the block's last entry's line, or, when that
// line has its own comment, goes on its own line before the combined `}`.
func TestFormat_CombinedImportBlockKeepsItsTrailingComment(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"import {A} // c\nimport B\n",
			"import {\n    A // c\n    B\n}\n",
		},
		{
			"import {\n    A\n    C\n} // c\nimport B\n",
			"import {\n    A\n    B\n    C // c\n}\n",
		},
		{
			"import {\n    A // a\n    C // x\n} // c\nimport B\n",
			"import {\n    A // a\n    B\n    C // x\n    // c\n}\n",
		},
		{
			"import B\npub import {a.X} // c\n",
			"import B\npub import a.X // c\n",
		},
		{
			"//00000000000\nimport{A000000000000000000000000}//0\nimport A\n",
			"//00000000000\nimport {\n    A\n    A000000000000000000000000 //0\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
