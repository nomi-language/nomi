package vmhost

import (
	"strings"
	"testing"
)

// A Route literal is held to two rules stricter than Go's ServeMux: its
// method is a standard one in capitals, and a brace stands only in a
// whole-segment wildcard. Each is a compile error at the literal, with the
// reason Route.parse gives.
func TestRouteLiteral_RefusesWhatGoAccepts(t *testing.T) {
	const methods = "a route's method is GET, HEAD, POST, PUT, PATCH, DELETE, CONNECT, OPTIONS or TRACE, and `Route.with_method` takes any other"
	const closing = "a `}` outside a wildcard; a wildcard is a whole segment, `{name}`"
	const opening = "a `{` outside a wildcard; a wildcard is a whole segment, `{name}`"
	for _, c := range []struct{ pattern, reason string }{
		{"GETX /complete-profile/token}", "unknown method `GETX`; did you mean `GET`?"},
		{"get /orders", "unknown method `get`; did you mean `GET`?"},
		{"Post /orders", "unknown method `Post`; did you mean `POST`?"},
		{"PUTT /orders", "unknown method `PUTT`; did you mean `PUT`?"},
		{"DELET /orders/{id}", "unknown method `DELET`; did you mean `DELETE`?"},
		{"PROPFIND /files", "unknown method `PROPFIND`; " + methods},
		{"GET /complete-profile/token}", closing},
		{"/a}/b", closing},
		{"GET /files/v{n}", opening},
		{"GET /files/{a}{b}", opening},
		{"example.com{/a", opening},
	} {
		src := "import std::http.Route\n\nfn main()\n    _ = Route`" + c.pattern + "`\nend\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.pattern, len(ds), ds)
		}
		want := "typed literal Route`" + c.pattern + "` is invalid: pattern " + c.pattern + ": " + c.reason
		if ds[0].Message != want {
			t.Errorf("%s:\n got %q\nwant %q", c.pattern, ds[0].Message, want)
		}
		if ds[0].Line != 4 || ds[0].Col != 9 {
			t.Errorf("%s: at %d:%d, want 4:9", c.pattern, ds[0].Line, ds[0].Col)
		}
	}
}

// What the rules leave alone still loads: every standard method, no
// method, the three wildcard forms, a host, and a percent-encoded brace.
func TestRouteLiteral_AdmitsStandardPatterns(t *testing.T) {
	var lines []string
	for _, p := range []string{
		"GET /", "HEAD /a", "POST /a", "PUT /a", "PATCH /a", "DELETE /a",
		"CONNECT /a", "OPTIONS /a", "TRACE /a", "/any", "GET\t/tab",
		"GET /users/{id}", "/files/{rest...}", "/{$}", "GET example.com/a/{x}",
		"/a/%7Bliteral%7D",
	} {
		lines = append(lines, "    _ = Route`"+p+"`")
	}
	src := "import std::http.Route\n\nfn main()\n" + strings.Join(lines, "\n") + "\nend\n"
	if _, err := LoadSource("main", src); err != nil {
		t.Fatalf("the front end rejects standard Route literals: %v", err)
	}
}
