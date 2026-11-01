package vmhost

import "testing"

// A load error of a block's project names each file by its path from the
// block's root, written with `/` as the block marks it, on Windows too.
func TestFromRoot_NamesAFileAsTheBlockMarksIt(t *testing.T) {
	for _, c := range []struct {
		name            string
		text, root, rel string
		sep             byte
		want            string
	}{
		{"unix absolute", "/tmp/b/rates/tables.nomi:17:18: mismatch", "/tmp/b", "", '/',
			"rates/tables.nomi:17:18: mismatch"},
		{"windows absolute", `C:\T\b\rates\tables.nomi:17:18: mismatch`, `C:\T\b`, "", '\\',
			"rates/tables.nomi:17:18: mismatch"},
		{"windows relative", `see "b\rates\tables.nomi" (b\main.nomi:1:1)`, `C:\T\b`, "b", '\\',
			`see "rates/tables.nomi" (main.nomi:1:1)`},
		{"elsewhere", `C:\std\fs.nomi:1:1: x`, `C:\T\b`, "", '\\', `C:\std\fs.nomi:1:1: x`},
	} {
		if got := fromRootSep(c.text, c.root, c.rel, c.sep); got != c.want {
			t.Errorf("%s: fromRootSep = %q, want %q", c.name, got, c.want)
		}
	}
}
