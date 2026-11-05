package format

import "testing"

// An `embed` prints its body as written, and is a fixed point.
func TestFormat_EmbedDeclRoundTrips(t *testing.T) {
	for _, src := range []string{
		"/// The site.\npub embed site `static`\n",
		"embed assets `static \"my notes.txt\" templates/*.html`\n",
		"embed assets `\n    static\n    templates/*.html\n    `\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
		if err := SameMeaning(src, src); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFormat_EmbedDeclNormalizesSpacing(t *testing.T) {
	formatsKeepingMeaning(t,
		"pub   embed  site    `static`\n",
		"pub embed site `static`\n")
}
