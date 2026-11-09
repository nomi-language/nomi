package analysis_test

import "testing"

// A Route literal with no expected type has a String field per wildcard,
// and one whose parameters cannot be read as text is an error at it.
func TestTargetLiteral_RouteParameters(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"    r = Route`GET /users/{id}/{rest...}`\n    n: Int = r\n    n\n", "type mismatch: expected Int, got Route<.{id: String, rest: String}>"},
		{"    r = Route`GET /`\n    n: Int = r\n    n\n", "type mismatch: expected Int, got Route"},
		{"    r: Route<.{id: Bool}> = \"GET /flags/{id}\"\n    _ = r\n    1\n", "a route's parameters are read from its path and written into links as text: each field's type implements `Parse` and `Display`, and `Bool`, the type of `id`, does not implement `Parse`"},
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

// A route literal in Route.to_with beside a handler that states its
// parameters is checked after the handler and typed by it, tagged or
// untagged; a lambda that states none takes a String per wildcard from the
// literal, and a field type that cannot be read from text is an error at
// the literal.
func TestTargetLiteral_RouteArgumentTypedByTheHandler(t *testing.T) {
	const head = "import std::http.{Body, Entry, Request, Response, Route}\n\n" +
		"fn update(_: Request, item: .{id: Int}) -> Response {\n    Response{body: Body.Text(\"${item.id + 1}\")}\n}\n\n" +
		"fn flag(_: Request, _: .{id: Bool}) -> Response {\n    Response{body: Body.Text(\"flag\")}\n}\n\n" +
		"fn f() -> List<Entry> {\n    "
	// What the project gives with no route at all (the test loader's
	// prelude) is not the route's.
	baseline := map[string]bool{}
	for _, e := range checkTypedLiteralProject(t, head+"[]\n}\n", nil) {
		baseline[e.Message] = true
	}
	for _, c := range []struct {
		entry, want string
		col         int
	}{
		{`[Route.to_with("PATCH /todos/{id}", update)]`, "", 0},
		{"[Route.to_with(Route`PATCH /todos/{id}`, update)]", "", 0},
		{`["PATCH /todos/{id}" |> Route.to_with(update)]`, "", 0},
		{"[Route`PATCH /todos/{id}` |> Route.to_with(update)]", "", 0},
		{`[Route.to_with("GET /n/{id}", fn _: Request, p: .{id: Int} -> Response{body: Body.Text("${p.id + 1}")})]`, "", 0},
		{`[Route.to_with("GET /n/{id}", fn _, p -> Response{body: Body.Text(p.id + "!")})]`, "", 0},
		{`[Route.to_with("GET /n/{id}", fn _, p -> Response{body: Body.Text("${p.id + 1}")})]`, "binary + type mismatch: String vs Int", 0},
		{`[Route.to_with("GET /flags/{id}", flag)]`, "a route's parameters are read from its path and written into links as text: each field's type implements `Parse` and `Display`, and `Bool`, the type of `id`, does not implement `Parse`", 20},
	} {
		var got []string
		found := false
		for _, e := range checkTypedLiteralProject(t, head+c.entry+"\n}\n", nil) {
			if baseline[e.Message] {
				continue
			}
			got = append(got, e.Message)
			if e.Message == c.want {
				found = true
				if c.col != 0 && (e.Line != 12 || e.Col != c.col) {
					t.Errorf("%s: at %d:%d, want 12:%d", c.entry, e.Line, e.Col, c.col)
				}
			}
		}
		if c.want == "" && len(got) > 0 {
			t.Errorf("%s: the checker refuses it: %v", c.entry, got)
		}
		if c.want != "" && !found {
			t.Errorf("%s: want %q, got %v", c.entry, c.want, got)
		}
	}
}
