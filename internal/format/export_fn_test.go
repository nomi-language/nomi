package format

import "testing"

// `export fn` prints with its keyword, and is a fixed point.
func TestFormat_ExportFnRoundTrips(t *testing.T) {
	src := "/// Guesses one letter.\nexport fn guess(game: Game, letter: String): Game {\n    game\n}\n"
	if got := formatTwice(t, src); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
	if err := SameMeaning(src, src); err != nil {
		t.Fatal(err)
	}
}

func TestFormat_ExportFnNormalizesSpacing(t *testing.T) {
	formatsKeepingMeaning(t,
		"export   fn  answer( ):Int{ 42 }\n",
		"export fn answer(): Int {\n    42\n}\n")
}
