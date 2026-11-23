package stdhttp

import (
	"mime"
	"slices"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// Response.content's type for a name comes from Go's own table whatever the
// machine's tables say. The test gives the process the type a Windows
// runner's registry gives `.js`, as mime.TypeByExtension then answers, and
// the response's type is still Go's; a name with no type in the table is
// typed by its data.
func TestAnswerContent_TheTypeIsTheSameOnEveryMachine(t *testing.T) {
	builtin := mime.TypeByExtension(".js")
	if err := mime.AddExtensionType(".js", "application/javascript"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mime.AddExtensionType(".js", builtin) })
	if got := mime.TypeByExtension(".js"); got != "application/javascript" {
		t.Fatalf("the machine's type for .js is %q; the test did not set it", got)
	}
	for _, c := range []struct{ name, data, want string }{
		{"app.js", "go()", "text/javascript; charset=utf-8"},
		{"APP.JS", "go()", "text/javascript; charset=utf-8"},
		{"page.html", "<p>", "text/html; charset=utf-8"},
		{"notes", "plain words", "text/plain; charset=utf-8"},
		{"blob.unknownext", "\x00\x01\x02", "application/octet-stream"},
	} {
		got := strs(AnswerContent("GET", "/f", nil, c.name, rt.SmallInt(-1), nil, rt.Bytes(c.data)))
		at := slices.Index(got, "Content-Type")
		if at < 0 || at+1 >= len(got) || got[at+1] != c.want {
			t.Errorf("%s: answered %q, want Content-Type %q", c.name, got, c.want)
		}
	}
	set := listOf([]string{"Content-Type", "application/x-mine"})
	got := strs(AnswerContent("GET", "/f", nil, "app.js", rt.SmallInt(-1), set, rt.Bytes("go()")))
	if at := slices.Index(got, "Content-Type"); at < 0 || got[at+1] != "application/x-mine" {
		t.Errorf("a type the headers set: answered %q, want it kept", got)
	}
}
