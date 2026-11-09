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

func TestFormat_DeferOnError(t *testing.T) {
	src := "fn main() { defer  on   error sqlite.close(db) }\n"
	want := `fn main() {
    defer on error sqlite.close(db)
}
`
	got, _ := Format(src)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again, _ := Format(got); again != got {
		t.Errorf("a second format changed it:\n%s", again)
	}
}
