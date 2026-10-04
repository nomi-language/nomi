package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// fillActions opens src at uri and asks for quick fixes over every
// diagnostic the client would hold, returning the fill fixes.
func fillActions(t *testing.T, s *Server, uri, src string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(uri, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	// The client echoes diagnostics back without their code (glsp drops it).
	for i := range diags {
		diags[i].Code = nil
	}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Context: protocol.CodeActionContext{
			Diagnostics: diags,
			Only:        []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
		},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	actions, _ := res.([]protocol.CodeAction)
	var out []protocol.CodeAction
	for _, a := range actions {
		if strings.HasPrefix(a.Title, "Implement missing") || strings.HasPrefix(a.Title, "Add missing") {
			out = append(out, a)
		}
	}
	return out
}

// checkFill applies the one fill fix src is offered and returns the result,
// after asserting the fix is preferred, names its diagnostics, and leaves a
// document that is `nomi fmt` output whose only errors are allowed ones.
// The fills write `todo`, so a fix that leaves any error behind fails here
// unless the test names it.
func checkFill(t *testing.T, s *Server, uri, src, title string, allowed ...string) string {
	t.Helper()
	actions := fillActions(t, s, uri, src)
	if len(actions) != 1 {
		titles := make([]string, len(actions))
		for i, a := range actions {
			titles[i] = a.Title
		}
		t.Fatalf("want one fill fix, got %q", titles)
	}
	a := actions[0]
	if a.Title != title {
		t.Errorf("title = %q, want %q", a.Title, title)
	}
	if a.IsPreferred == nil || !*a.IsPreferred {
		t.Error("fix is not preferred")
	}
	if len(a.Diagnostics) == 0 {
		t.Error("fix names no diagnostic")
	}
	edited := applyWorkspaceEdit(t, src, a, uri)
	if out, err := format.Format(edited); err != nil || out != edited {
		t.Errorf("result is not nomi fmt output (err %v):\n%s\nformatted:\n%s", err, edited, out)
	}
	s.docs.Open(uri, edited)
	snap := s.docs.Snapshot(uri)
	if len(snap.Errors) > 0 {
		t.Fatalf("result does not parse: %v\n%s", snap.Errors, edited)
	}
	for _, e := range snap.Analysis.TypeErrors {
		ok := false
		for _, m := range allowed {
			ok = ok || e.Message == m
		}
		if !ok {
			t.Errorf("result has error %d:%d %s\n%s", e.Line, e.Col, e.Message, edited)
		}
	}
	return edited
}

const fillURI = "file:///fill/main.nomi"

const fillShapeDecls = "interface Shape {\n    fn area(shape: self): Float\n    fn name(shape: self): String\n\n    fn label(shape: self): String {\n        Shape.name(shape)\n    }\n}\n\nstruct Square {\n    side: Float\n}\n\n"

func TestFillFunctions_OneMissing(t *testing.T) {
	src := fillShapeDecls + "impl Shape for Square {\n    fn area(square: Square): Float {\n        square.side * square.side\n    }\n}\n"
	got := checkFill(t, NewServer(), fillURI, src, "Implement missing function 'name'")
	want := fillShapeDecls + "impl Shape for Square {\n    fn area(square: Square): Float {\n        square.side * square.side\n    }\n\n    fn name(square: Square): String {\n        todo\n    }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFillFunctions_AllMissing(t *testing.T) {
	want := fillShapeDecls + "impl Shape for Square {\n    fn area(square: Square): Float {\n        todo\n    }\n\n    fn name(square: Square): String {\n        todo\n    }\n}\n"
	for name, block := range map[string]string{
		"open block":   "impl Shape for Square {\n}\n",
		"empty braces": "impl Shape for Square {}\n",
		// How `nomi fmt` writes an empty block.
		"bodyless": "impl Shape for Square\n",
	} {
		t.Run(name, func(t *testing.T) {
			got := checkFill(t, NewServer(), fillURI, fillShapeDecls+block, "Implement missing functions")
			if got != want {
				t.Fatalf("got\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestFillFunctions_GenericImpl(t *testing.T) {
	decls := "struct Box<T> {\n    value: T\n}\n\n"
	got := checkFill(t, NewServer(), fillURI, decls+"impl Iter for Box<T> {\n}\n", "Implement missing function 'each_while'")
	want := decls + "impl Iter for Box<T> {\n    fn each_while(box: Box<T>, yield: (T) -> Bool): Bool {\n        todo\n    }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}

	store := "interface Store<K, V> {\n    fn get(store: self, key: K): Maybe<V>\n    fn put(store: self, key: K, value: V): self\n}\n\nstruct Cache<T> {\n    items: List<T>\n}\n\n"
	// The interface's type arguments are pinned by the function the block
	// already defines.
	get := "    fn get(cache: Cache<T>, key: Int): Maybe<T> {\n        if key > 0 {\n            List.head(cache.items)\n        } else {\n            None\n        }\n    }\n"
	got = checkFill(t, NewServer(), fillURI, store+"impl Store for Cache<T> {\n"+get+"}\n", "Implement missing function 'put'")
	if want := store + "impl Store for Cache<T> {\n" + get + "\n    fn put(cache: Cache<T>, key: Int, value: T): Cache<T> {\n        todo\n    }\n}\n"; got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A header that spells the interface's type arguments, one of them the
// block's own type parameter, reports the missing functions and fills them
// with those arguments.
func TestFillFunctions_HeaderTypeArguments(t *testing.T) {
	store := "interface Store<K, V> {\n    fn get(store: self, key: K): Maybe<V>\n    fn put(store: self, key: K, value: V): self\n}\n\nstruct Cache<T> {\n    items: List<T>\n}\n\n"
	got := checkFill(t, NewServer(), fillURI, store+"impl Store<String, T> for Cache<T> {}\n", "Implement missing functions",
		"parameter 'cache' is never read",
		"parameter 'key' is never read",
		"parameter 'value' is never read")
	want := store + "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: String): Maybe<T> {\n        todo\n    }\n\n    fn put(cache: Cache<T>, key: String, value: T): Cache<T> {\n        todo\n    }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFillFunctions_ImportedInterface(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"engine.nomi": "pub interface Powered {\n    fn label(e: self): String\n\n    open fn describe(e: self, _loud: Bool = False): String {\n        Powered.label(e)\n    }\n}\n",
	})
	uri := "file://" + filepath.Join(dir, "main.nomi")
	decls := "struct Motor {\n    name: String\n}\n\n"
	got := checkFill(t, NewServer(), uri, "import engine.Powered\n\n"+decls+"impl Powered for Motor {\n}\n", "Implement missing function 'label'")
	if !strings.Contains(got, "impl Powered for Motor {\n    fn label(motor: Motor): String {\n        todo\n    }\n}\n") {
		t.Fatalf("got\n%s", got)
	}
	got = checkFill(t, NewServer(), uri, "import engine\n\n"+decls+"impl engine.Powered for Motor {\n}\n", "Implement missing function 'label'")
	if !strings.Contains(got, "fn label(motor: Motor): String {\n        todo\n    }") {
		t.Fatalf("got\n%s", got)
	}
}

func TestFillFunctions_NotOfferedWhenComplete(t *testing.T) {
	src := fillShapeDecls + "impl Shape for Square {\n    fn area(square: Square): Float {\n        square.side * square.side\n    }\n\n    fn name(_square: Square): String {\n        \"square\"\n    }\n}\n"
	if got := fillActions(t, NewServer(), fillURI, src); len(got) != 0 {
		t.Fatalf("offered %d fixes for a complete impl", len(got))
	}
}

const fillPointDecls = "struct Point {\n    x: Int\n    y: Int\n    z: Int\n    label: String = \"\"\n}\n\n"

func TestFillFields_OneLine(t *testing.T) {
	body := func(lit string) string {
		return fillPointDecls + "fn main() {\n    p = " + lit + "\n    _sum = p.x + p.y + p.z\n}\n"
	}
	got := checkFill(t, NewServer(), fillURI, body("Point{x: 1, y: 2}"), "Add missing field 'z'")
	if want := body("Point{x: 1, y: 2, z: todo}"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	got = checkFill(t, NewServer(), fillURI, body("Point{y: 2}"), "Add missing fields")
	if want := body("Point{y: 2, x: todo, z: todo}"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	got = checkFill(t, NewServer(), fillURI, body("Point{}"), "Add missing fields")
	if want := body("Point{x: todo, y: todo, z: todo}"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFillFields_MultiLine(t *testing.T) {
	body := func(lit string) string {
		return fillPointDecls + "fn main() {\n    p = " + lit + "\n\n    _sum = p.x + p.y + p.z\n}\n"
	}
	got := checkFill(t, NewServer(), fillURI, body("Point{\n        x: 1,\n        label: \"a\",\n    }"), "Add missing fields")
	if want := body("Point{\n        x: 1,\n        label: \"a\",\n        y: todo,\n        z: todo,\n    }"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFillFields_WideLiteralBreaksAsFmtDoes(t *testing.T) {
	decls := "struct Request {\n    method_name: String\n    destination_address: String\n    request_body_text: String\n}\n\n"
	src := decls + "fn main() {\n    r = Request{method_name: \"GET\", destination_address: \"https://example.com/a/b/c\"}\n    _m = r.method_name\n}\n"
	got := checkFill(t, NewServer(), fillURI, src, "Add missing field 'request_body_text'")
	if !strings.Contains(got, "        request_body_text: todo,\n    }") {
		t.Fatalf("the literal did not break:\n%s", got)
	}
}

func TestFillFields_VariantLiteral(t *testing.T) {
	decls := "enum Shape {\n    Rect {width: Int, height: Int}\n    Dot\n}\n\n"
	src := decls + "fn make(): Shape {\n    .Rect{width: 1}\n}\n"
	got := checkFill(t, NewServer(), fillURI, src, "Add missing field 'height'")
	if want := decls + "fn make(): Shape {\n    .Rect{width: 1, height: todo}\n}\n"; got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFillFields_NotOfferedWhenComplete(t *testing.T) {
	src := fillPointDecls + "fn main() {\n    p = Point{x: 1, y: 2, z: 3}\n    _sum = p.x + p.y + p.z\n}\n"
	if got := fillActions(t, NewServer(), fillURI, src); len(got) != 0 {
		t.Fatalf("offered %d fixes for a complete literal", len(got))
	}
}

const fillColorDecls = "enum Color {\n    Red\n    Green\n    Rgb Int\n    Named {name: String, code: Int}\n}\n\n"

func TestFillArms_Missing(t *testing.T) {
	body := func(arms string) string {
		return fillColorDecls + "fn f(c: Color): Int {\n    case c {\n" + arms + "    }\n}\n"
	}
	got := checkFill(t, NewServer(), fillURI, body("        .Red -> 1\n        .Green -> 2\n        .Rgb(n) -> n\n"), "Add missing arm .Named{name,")
	if want := body("        .Red -> 1\n        .Green -> 2\n        .Rgb(n) -> n\n        .Named{name, code} -> todo\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	got = checkFill(t, NewServer(), fillURI, body("        .Green -> 2\n"), "Add missing arms")
	if want := body("        .Green -> 2\n        .Red -> todo\n        .Rgb(value) -> todo\n        .Named{name, code} -> todo\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFillArms_Nested(t *testing.T) {
	src := fillColorDecls + "fn f(a: Color, b: Maybe<Color>): Int {\n    case a {\n        .Red ->\n            case b {\n                Some(.Red) -> 1\n                None -> 0\n            }\n\n        _ ->\n            2\n    }\n}\n"
	got := checkFill(t, NewServer(), fillURI, src, "Add missing arm .Some(value)")
	want := fillColorDecls + "fn f(a: Color, b: Maybe<Color>): Int {\n    case a {\n        .Red ->\n            case b {\n                Some(.Red) -> 1\n                None -> 0\n                .Some(value) -> todo\n            }\n\n        _ ->\n            2\n    }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFillArms_NotOfferedWhenExhaustive(t *testing.T) {
	for name, arms := range map[string]string{
		"every variant": "        .Red -> 1\n        .Green -> 2\n        .Rgb(n) -> n\n        .Named{code, ..} -> code\n",
		"catch-all":     "        .Red -> 1\n        _ -> 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			src := fillColorDecls + "fn f(c: Color): Int {\n    case c {\n" + arms + "    }\n}\n"
			if got := fillActions(t, NewServer(), fillURI, src); len(got) != 0 {
				t.Fatalf("offered %d fixes for an exhaustive case", len(got))
			}
		})
	}
}

// In a document that is not `nomi fmt` output the fix writes its own
// layout and touches nothing else.
func TestFill_UnformattedDocumentKeepsItsLayout(t *testing.T) {
	// `Rgb(Int)` is not how `nomi fmt` writes a payload.
	decls := "enum Color {\n    Red\n    Rgb(Int)\n}\n\n" + fillPointDecls
	cases := []struct{ name, src, want string }{
		{
			"impl",
			decls + fillShapeDecls + "impl Shape for Square {\n    fn area(square: Square): Float {\n        square.side\n    }\n}\n",
			decls + fillShapeDecls + "impl Shape for Square {\n    fn area(square: Square): Float {\n        square.side\n    }\n\n    fn name(square: Square): String {\n        todo\n    }\n}\n",
		},
		{
			"multi-line literal without a trailing comma",
			decls + "fn main() {\n    p = Point{\n        x: 1\n    }\n    _s = p.x\n}\n",
			decls + "fn main() {\n    p = Point{\n        x: 1,\n        y: todo,\n        z: todo,\n    }\n    _s = p.x\n}\n",
		},
		{
			"case",
			decls + "fn f(c: Color): Int {\n    case c {\n        .Red -> 1\n    }\n}\n",
			decls + "fn f(c: Color): Int {\n    case c {\n        .Red -> 1\n        .Rgb(value) -> todo\n    }\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actions := fillActions(t, NewServer(), fillURI, c.src)
			if len(actions) != 1 {
				t.Fatalf("want one fill fix, got %d", len(actions))
			}
			if got := applyWorkspaceEdit(t, c.src, actions[0], fillURI); got != c.want {
				t.Fatalf("got\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}
