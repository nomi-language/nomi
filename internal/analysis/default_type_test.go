package analysis_test

import (
	"strings"
	"testing"
)

// A field with a default may leave out its type, which is its default's
// (default_type.go).

const fieldDefaultsLib = `pub struct Settings {
    retries = 3
    name = "app"
    origin = Point{x: 0, y: 0}
    limit = default_limit()
    tags: List<String> = []
}

pub struct Point {
    x: Int
    y: Int
}

pub enum Shape {
    Circle{radius = 1.0}
}

fn default_limit() -> Int {
    100
}
`

// Another file reads each inferred field type, whichever file is checked
// first.
func TestDefaultType_FieldTypeIsReadFromAnotherFile(t *testing.T) {
	src := `import lib.{Settings, Shape}

fn retries(s: Settings) -> Int {
    s.retries + s.limit + s.origin.x
}

fn label(s: Settings) -> String {
    s.name + "!"
}

fn radius(s: Shape) -> Float {
    case s {
        .Circle{radius} -> radius
    }
}

fn main() {
    _ = (retries(Settings{}), label(Settings{retries: 1}), radius(Shape.Circle{}))
}
`
	files := map[string]string{"main.nomi": src, "lib.nomi": fieldDefaultsLib}
	for _, order := range [][]string{{"", "lib"}, {"lib", ""}} {
		got := checkOnceProject(t, files, order)
		for file, errs := range got {
			if len(errs) > 0 {
				t.Errorf("order %v: %s: unexpected errors:\n%s", order, file, strings.Join(messages(errs), "\n"))
			}
		}
	}
}

// A wrong use of an inferred field is the type error an annotated field
// gives.
func TestDefaultType_WrongUseOfInferredFieldIsRejected(t *testing.T) {
	src := `import lib.Settings

fn wrong(s: Settings) -> String {
    s.retries
}

fn main() {
    _ = wrong(Settings{name: 1})
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src, "lib.nomi": fieldDefaultsLib}, []string{"", "lib"})
	msgs := strings.Join(messages(got["main.nomi"]), "\n")
	for _, want := range []string{"expected String, got Int", "field 'name'"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("want an error containing %q, got:\n%s", want, msgs)
		}
	}
}

// A default whose type only an expected type tells is an error at the
// field, with help that writes the annotation.
func TestDefaultType_UndeterminedFieldIsRejected(t *testing.T) {
	src := `enum Restart {
    Temporary
    Permanent
}

struct Bad {
    guessed = %[]
    items = []
    later = None
    restart = .Temporary
    loop = Bad{}.loop
}

fn main() {
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
	errs := got["main.nomi"]
	want := []struct{ msg, hint string }{
		{"can't tell this field's type from `%[]`", "write `guessed: Set<_> = %[]`, with the type for each `_`"},
		{"can't tell this field's type from `[]`", "write `items: List<_> = []`, with the type for each `_`"},
		{"can't tell this field's type from `None`", "write `later: Maybe<_> = None`, with the type for each `_`"},
		{"can't tell this field's type from `.Temporary`", "write `restart: T = .Temporary`, with its type for `T`"},
		{"can't tell this field's type from `Bad{}.loop`: it depends on a type that comes from this default", "write its type: `loop: T = Bad{}.loop`"},
	}
	if len(errs) != len(want) {
		t.Fatalf("want %d errors, got:\n%s", len(want), strings.Join(messages(errs), "\n"))
	}
	for i, w := range want {
		if errs[i].Message != w.msg || len(errs[i].Hints) != 1 || errs[i].Hints[0] != w.hint {
			t.Errorf("error %d = %q %q, want %q %q", i, errs[i].Message, errs[i].Hints, w.msg, w.hint)
		}
	}
}

// A public struct's field whose inferred type is private is the error a
// written private type is.
func TestDefaultType_PublicFieldExposingPrivateTypeIsRejected(t *testing.T) {
	src := `struct Secret {
    v: Int
}

pub struct Leaky {
    s = Secret{v: 1}
}

fn main() {
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
	msgs := messages(got["main.nomi"])
	if len(msgs) != 1 || msgs[0] != "public struct field exposes private type Secret" {
		t.Fatalf("want the private-type error, got:\n%s", strings.Join(msgs, "\n"))
	}
}

// A derived from_json names each field's type, so a field without one is
// refused at the field.
func TestDefaultType_DeriveFromJsonNeedsWrittenType(t *testing.T) {
	src := `import std::json.FromJson

struct Cfg {
    port = 8080
}

derive FromJson for Cfg

fn main() {
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
	errs := got["main.nomi"]
	if len(errs) != 1 || errs[0].Message != "`derive FromJson for Cfg` needs field 'port' to have a written type" ||
		len(errs[0].Hints) != 1 || errs[0].Hints[0] != "write its type: `port: T = 8080`" {
		t.Fatalf("want the FromJson field error, got:\n%s", strings.Join(messages(errs), "\n"))
	}
}

// A struct declared in a block takes its field types when the block is
// checked.
func TestDefaultType_BlockLocalStructField(t *testing.T) {
	src := `fn main() {
    struct Local {
        n = 5
    }
    l = Local{}
    _ = l.n + 1
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
	if errs := got["main.nomi"]; len(errs) > 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(messages(errs), "\n"))
	}
}

const paramDefaultsLib = `pub enum Mode {
    Replace
    Append
}

pub fn search(query: String, limit = 10, offset = 0) -> String {
    "${query}${limit}${offset}"
}

pub fn write(path: String, mode = Mode.Replace) -> Mode {
    mode
}

pub struct Page {
    size: Int
}

impl Page {
    pub fn window(page: Page, offset = page.size * 2) -> Int {
        offset
    }
}
`

// A parameter with a default may leave out its type, which another file's
// calls read, whichever file is checked first.
func TestDefaultType_ParamTypeIsReadFromAnotherFile(t *testing.T) {
	src := `import lib.{Mode, Page, search, write}

fn main() {
    a: String = search("q", limit: 5)
    b: Mode = write("p", mode: .Append)
    c: Int = Page.window(Page{size: 1}, offset: 3)
    _ = (a, b, c)
}
`
	files := map[string]string{"main.nomi": src, "lib.nomi": paramDefaultsLib}
	for _, order := range [][]string{{"", "lib"}, {"lib", ""}} {
		got := checkOnceProject(t, files, order)
		for file, errs := range got {
			if len(errs) > 0 {
				t.Errorf("order %v: %s: unexpected errors:\n%s", order, file, strings.Join(messages(errs), "\n"))
			}
		}
	}
	bad := "import lib.search\n\nfn main() {\n    _ = search(\"q\", limit: \"many\")\n}\n"
	got := checkOnceProject(t, map[string]string{"main.nomi": bad, "lib.nomi": paramDefaultsLib}, []string{"", "lib"})
	if msgs := strings.Join(messages(got["main.nomi"]), "\n"); !strings.Contains(msgs, "expected Int, got String") {
		t.Errorf("want the argument mismatch against the inferred Int, got:\n%s", msgs)
	}
}

// A parameter's default whose type only an expected type tells is refused
// with the field rule's error, and a public function may not take a private
// type from a default.
func TestDefaultType_UndeterminedParamIsRefused(t *testing.T) {
	src := `struct Secret {
    v: Int
}

fn f(since = None, items = []) -> Int {
    1
}

pub fn leak(s = Secret{v: 1}) -> Int {
    s.v
}

fn main() {
    _ = (f(), leak())
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
	errs := got["main.nomi"]
	want := []struct{ msg, hint string }{
		{"can't tell this parameter's type from `None`", "write `since: Maybe<_> = None`, with the type for each `_`"},
		{"can't tell this parameter's type from `[]`", "write `items: List<_> = []`, with the type for each `_`"},
	}
	for _, w := range want {
		found := false
		for _, e := range errs {
			if e.Message == w.msg && len(e.Hints) == 1 && e.Hints[0] == w.hint {
				found = true
			}
		}
		if !found {
			t.Errorf("want %q with %q, got:\n%s", w.msg, w.hint, strings.Join(messages(errs), "\n"))
		}
	}
	if msgs := strings.Join(messages(errs), "\n"); !strings.Contains(msgs, "function parameter exposes private type Secret") {
		t.Errorf("want the private-type error, got:\n%s", msgs)
	}
}

// A lambda's parameter takes its default's type, and where the lambda's
// position expects a function type the two must agree.
func TestDefaultType_LambdaParamDefault(t *testing.T) {
	src := `fn apply(f: (Int) -> Int) -> Int {
    f(1)
}

fn main() {
    add = fn a: Int, b = 1 -> a + b
    _ = add(1)
    _ = apply(fn x = "a" -> 1)
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
	msgs := messages(got["main.nomi"])
	if len(msgs) != 1 || msgs[0] != "default value for parameter 'x' is String, expected Int" {
		t.Fatalf("want the one default mismatch, got:\n%s", strings.Join(msgs, "\n"))
	}
}
