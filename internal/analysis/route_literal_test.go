package analysis_test

import "testing"

// A Route literal with no expected type has a String field per wildcard,
// and one whose parameters cannot be read as text is an error at it.
func TestTargetLiteral_RouteParameters(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"    r = Route`GET /users/{id}/{rest...}`\n    n: Int = r\n    n\n", "type mismatch: expected Int, got Route<#{id: String, rest: String}>"},
		{"    r = Route`GET /`\n    n: Int = r\n    n\n", "type mismatch: expected Int, got Route"},
		{"    r: Route<#{id: Bool}> = \"GET /flags/{id}\"\n    _ = r\n    1\n", "a route's parameters are read from its path and written into links as text: each field's type implements `Parse` and `Display`, and `Bool`, the type of `id`, does not implement `Parse`"},
	} {
		src := "import std::http.Route\n\nfn f() -> Int {\n" + c.body + "}\n"
		found := false
		var got []string
		for _, e := range checkTypedLiteralProject(t, src, nil) {
			got = append(got, e.Message)
			if e.Message == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q, got %v", c.body, c.want, got)
		}
	}
}
