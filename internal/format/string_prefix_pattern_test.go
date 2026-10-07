package format

import "testing"

// A string prefix pattern, `"/users/" + id`, formats as written. It used to
// panic the formatter ("unimplemented: IdentPattern"), on the spec's own
// example.
func TestFormat_StringPrefixPattern(t *testing.T) {
	src := "fn route(path: String): String {\n" +
		"    case path {\n" +
		"        \"/users/\" + id -> id\n" +
		"        _ -> \"\"\n" +
		"    }\n" +
		"}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Fatalf("got:\n%s\nwant:\n%s", got, src)
	}
	if err := SameMeaning(src, got); err != nil {
		t.Fatal(err)
	}
}
