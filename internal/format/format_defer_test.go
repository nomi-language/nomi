package format

import "testing"

func TestFormat_Defer(t *testing.T) {
	src := "fn main() { defer sqlite.close(db) }\n"
	want := `fn main() {
    defer sqlite.close(db)
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
