package format

import "testing"

// Codepoint literals keep their spelling, escapes included, in expression and
// pattern position, and the output is a fixed point.
func TestFormat_CodepointLiteralsKeepTheirSpelling(t *testing.T) {
	src := "fn f(cp: Codepoint): Int {\n" +
		"  r = 'a'..='z'\n" +
		"  q = ['\\'', '\\\\', '\\n', '\\t', '\\u{7F}', '\\u{d}', '\"', ' ']\n" +
		"  case cp {\n" +
		"    'a' -> 1\n" +
		"    '\\'' -> 2\n" +
		"    _ -> 3\n" +
		"  }\n" +
		"}\n"
	want := "fn f(cp: Codepoint): Int {\n" +
		"    r = 'a'..='z'\n" +
		"    q = ['\\'', '\\\\', '\\n', '\\t', '\\u{7F}', '\\u{d}', '\"', ' ']\n" +
		"\n" +
		"    case cp {\n" +
		"        'a' -> 1\n" +
		"        '\\'' -> 2\n" +
		"        _ -> 3\n" +
		"    }\n" +
		"}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Format:\n got:\n%s\nwant:\n%s", got, want)
	}
	again, err := Format(got)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("Format is not idempotent:\n first:\n%s\nsecond:\n%s", got, again)
	}
}
