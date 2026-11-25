package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// privacySibling declares one private declaration of every kind a type
// position can name, beside a public function and a public generic struct.
const privacySibling = `
private struct Hidden
  v: Int
end

impl Hidden
  private fn make() -> Hidden
    Hidden{v: 3}
  end
end

private enum Color
  Red
  Boxed(Int)
end

private interface Shape
  fn area(s: self) -> Int
end

private typealias Meters Int

private fn helper() -> Int
  1
end

pub fn ident<T>(x: T) -> T
  x
end

pub struct Span<T>
  start: T
  stop: T
end

pub enum Slot<T>
  Empty
  Full(T)
end
`

// A `private` top-level declaration is seen from its own file only (spec
// §14 *Visibility*). Each spelling below names another file's private type
// or interface and must be rejected with the same message a private
// function gets, and with no modifier, internal, each one is accepted from
// the other file of the same module.
func TestQualifiedPrivateTypeIsRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "type annotation",
			body: "fn f(x: other.Hidden) -> Int\n  x.v\nend\n",
			want: "`Hidden` is private to `other.nomi`",
		},
		{
			name: "struct literal",
			body: "fn f() -> Int\n  h = other.Hidden{v: 1}\n  h.v\nend\n",
			want: "`Hidden` is private to `other.nomi`",
		},
		{
			name: "binding annotation",
			body: "fn f() -> Int\n  h: other.Hidden = other.Hidden.make()\n  h.v\nend\n",
			want: "`Hidden` is private to `other.nomi`",
		},
		{
			name: "type argument",
			body: "fn f(xs: List<other.Hidden>) -> Int\n  Iter.count(xs)\nend\n",
			want: "`Hidden` is private to `other.nomi`",
		},
		{
			name: "explicit type argument of a call",
			body: "fn f() -> Int\n  other.ident<other.Meters>(3)\nend\n",
			want: "`Meters` is private to `other.nomi`",
		},
		{
			name: "inherent function of a private type",
			body: "fn f() -> Int\n  h = other.Hidden.make()\n  h.v\nend\n",
			want: "`Hidden` is private to `other.nomi`",
		},
		{
			name: "enum variant value",
			body: "fn f() -> Int\n  c = other.Color.Red\n  case c\n    _ -> 1\n  end\nend\n",
			want: "`Color` is private to `other.nomi`",
		},
		{
			name: "enum constructor",
			body: "fn f() -> Int\n  c = other.Color.Boxed(2)\n  case c\n    _ -> 1\n  end\nend\n",
			want: "`Color` is private to `other.nomi`",
		},
		{
			name: "enum variant pattern",
			body: "fn f(c: Int) -> Int\n  case c\n    _ -> 1\n  end\nend\n\nfn g(c: other.Color) -> Int\n  case c\n    other.Color.Red -> 1\n    other.Color.Boxed(n) -> n\n  end\nend\n",
			want: "`Color` is private to `other.nomi`",
		},
		{
			name: "interface bound",
			body: "fn f<T>(x: T) -> Int where T: other.Shape\n  T.area(x)\nend\n",
			want: "`Shape` is private to `other.nomi`",
		},
		{
			name: "impl header",
			body: "struct Sq\n  n: Int\nend\n\nimpl other.Shape for Sq\n  fn area(s: Sq) -> Int\n    s.n\n  end\nend\n",
			want: "`Shape` is private to `other.nomi`",
		},
		{
			name: "type alias",
			body: "fn f(m: other.Meters) -> Int\n  m\nend\n",
			want: "`Meters` is private to `other.nomi`",
		},
		{
			name: "private function",
			body: "fn f() -> Int\n  other.helper()\nend\n",
			want: "`helper` is private to `other.nomi`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := "import other\n\n" + tc.body
			errs := buildProjectExpectingErrors(t, entry, map[string]string{"other": privacySibling})
			if len(errs) == 0 {
				t.Fatalf("the front end now ADMITS another file's private declaration:\n%s", entry)
			}
			requireMessage(t, errs, tc.want)
			requireCode(t, errs, tc.want, analysis.PrivateDeclarationCode)
			internal := strings.ReplaceAll(privacySibling, "private ", "")
			requireNoErrors(t, buildProjectExpectingErrors(t, entry, map[string]string{"other": internal}))
		})
	}
}

// requireCode fails unless the error whose message is want carries code.
func requireCode(t *testing.T, errs []analysis.TypeError, want, code string) {
	t.Helper()
	for _, e := range errs {
		if e.Message == want && e.Code != code {
			t.Fatalf("%q has code %q, want %q", want, e.Code, code)
		}
	}
}

// requireNoErrors fails on any error but an unused binding.
func requireNoErrors(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	var got []string
	for _, e := range errs {
		if e.Code != analysis.UnusedBindingCode {
			got = append(got, e.Error())
		}
	}
	if len(got) > 0 {
		t.Fatalf("want no errors, got:\n  %s", strings.Join(got, "\n  "))
	}
}

// A selective import of a private declaration is rejected with the same
// message whatever the declaration is: a function, a struct, an enum or an
// interface, braced or not.
func TestSelectiveImportOfPrivateDeclarationIsRejected(t *testing.T) {
	cases := []struct {
		imp  string
		want string
	}{
		{"import other.helper", "`helper` is private to `other.nomi`"},
		{"import other.{helper}", "`helper` is private to `other.nomi`"},
		{"import other.Hidden", "`Hidden` is private to `other.nomi`"},
		{"import other.{Hidden}", "`Hidden` is private to `other.nomi`"},
		{"import other.{Color}", "`Color` is private to `other.nomi`"},
		{"import other.{Shape}", "`Shape` is private to `other.nomi`"},
	}
	for _, tc := range cases {
		t.Run(tc.imp, func(t *testing.T) {
			entry := tc.imp + "\n\nfn main() end\n"
			errs := buildProjectExpectingErrors(t, entry, map[string]string{"other": privacySibling})
			if len(errs) == 0 {
				t.Fatalf("the front end now ADMITS importing a private declaration:\n%s", entry)
			}
			requireMessage(t, errs, tc.want)
		})
	}
}

// A file names its own private types freely, including as the type argument
// of another file's public generic function, struct or enum: the instance
// lives in the other file, but the only file that names `Point` is its own.
// Public declarations stay reachable across files in every spelling.
func TestPrivateTypeOwnFileAndPublicTypesAcrossFilesAreAccepted(t *testing.T) {
	sibling := strings.NewReplacer(
		"\nprivate struct Hidden", "\npub struct Hidden",
		"\n  private fn make", "\n  pub fn make",
		"\nprivate enum Color", "\npub enum Color",
		"\nprivate interface Shape", "\npub interface Shape",
		"\nprivate typealias Meters", "\npub typealias Meters",
	).Replace(privacySibling)
	entry := `import other
import other.{Hidden, Shape}

struct Point
  x: Int
end

struct Sq
  n: Int
end

impl other.Shape for Sq
  fn area(s: Sq) -> Int
    s.n
  end
end

fn area_of<T>(x: T) -> Int where T: Shape
  T.area(x)
end

fn color(c: other.Color) -> Int
  case c
    other.Color.Red -> 1
    other.Color.Boxed(n) -> n
  end
end

fn demo() -> Int
  p = other.ident(Point{x: 1})
  span = other.Span{start: Point{x: 1}, stop: Point{x: 2}}
  slot: other.Slot<Point> = other.Slot.Full(Point{x: 3})
  points: List<Point> = [p, span.start, span.stop]
  h: other.Hidden = other.Hidden{v: 1}
  bare = Hidden{v: 2}
  made = other.Hidden.make()
  m: other.Meters = 4
  n = case slot
    other.Slot.Full(q) -> q.x
    other.Slot.Empty -> 0
  end
  Iter.count(points) + h.v + bare.v + made.v + m + n + color(other.Color.Boxed(5)) + area_of(Sq{n: 6})
end
`
	errs := buildProjectExpectingErrors(t, entry, map[string]string{"other": sibling})
	var got []string
	for _, e := range errs {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		got = append(got, e.Error())
	}
	if len(got) > 0 {
		t.Fatalf("want no errors, got:\n  %s", strings.Join(got, "\n  "))
	}
}

func requireMessage(t *testing.T, errs []analysis.TypeError, want string) {
	t.Helper()
	var got []string
	for _, e := range errs {
		if e.Message == want {
			return
		}
		got = append(got, e.Error())
	}
	t.Fatalf("want error %q, got:\n  %s", want, strings.Join(got, "\n  "))
}
