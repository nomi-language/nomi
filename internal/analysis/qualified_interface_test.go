package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An impl header, a bound and a parameter type each name an interface
// through a module qualifier (`calendar.DateParts`, `other.Shape`) in a file
// that declares its own interface of the same name. The qualified spelling
// is the module's interface and the bare one is the file's own. The impl
// side resolved the header's interface by its bare name in the impl's file,
// so `impl calendar.DateParts for Q` was filed as an impl of the local
// `DateParts`, and `Q` met neither a `where T: calendar.DateParts` bound nor
// a `calendar.DateParts` parameter.

// qualifiedStdEntry declares its own DateParts beside std/calendar's and
// implements each for Q.
const qualifiedStdEntry = `import
    std::calendar std::io
end

interface DateParts
    fn stamp(value: self) -> String
end

struct Q
    n: Int
end

impl calendar.DateParts for Q
    fn year(value: Q) -> Int
        value.n
    end

    fn month(_: Q) -> Int
        1
    end

    fn day(_: Q) -> Int
        2
    end
end

impl DateParts for Q
    fn stamp(_: Q) -> String
        "local"
    end
end

fn total<T>(x: T, y: T) -> Int where T: calendar.DateParts
    calendar.DateParts.year(x) + calendar.DateParts.month(y)
end

fn as_value(x: calendar.DateParts) -> Int
    calendar.DateParts.day(x)
end

fn local<T>(x: T, y: T) -> String where T: DateParts
    DateParts.stamp(x) + DateParts.stamp(y)
end

fn main()
    q = Q{n: 7}
    io.print(total(q, q))
    io.print(as_value(q))
    io.print(local(q, q))
end
`

func TestInterfaceIdentity_AQualifiedStdInterfaceBesideALocalOneOfItsName(t *testing.T) {
	if errs := buildSiblingProject(t, qualifiedStdEntry, nil); len(errs) != 0 {
		t.Fatalf("`impl calendar.DateParts for Q` must meet a `calendar.DateParts` bound and parameter beside a local `interface DateParts`, got:\n  %s", joinMessages(errs))
	}
}

const qualifiedModuleOther = `pub interface Shape
    fn area(value: self) -> Int
end
`

const qualifiedModuleEntry = `import
    other std::io
end

interface Shape
    fn label(value: self) -> String
end

struct Sq
    side: Int
end

impl other.Shape for Sq
    fn area(value: Sq) -> Int
        value.side * value.side
    end
end

impl Shape for Sq
    fn label(_: Sq) -> String
        "square"
    end
end

fn double<T>(x: T, y: T) -> Int where T: other.Shape
    other.Shape.area(x) + other.Shape.area(y)
end

fn area_of(x: other.Shape) -> Int
    other.Shape.area(x)
end

fn labels<T>(x: T, y: T) -> String where T: Shape
    Shape.label(x) + Shape.label(y)
end

fn main()
    s = Sq{side: 3}
    io.print(double(s, s))
    io.print(area_of(s))
    io.print(labels(s, s))
end
`

func TestInterfaceIdentity_AQualifiedModuleInterfaceBesideALocalOneOfItsName(t *testing.T) {
	errs := buildSiblingProject(t, qualifiedModuleEntry, map[string]string{"other": qualifiedModuleOther})
	if len(errs) != 0 {
		t.Fatalf("`impl other.Shape for Sq` must meet an `other.Shape` bound and parameter beside a local `interface Shape`, got:\n  %s", joinMessages(errs))
	}
}

// The bare name stays the file's own interface: an impl of only the
// qualified one meets no bound the bare name writes, and the reverse.
func TestInterfaceIdentity_TheBareNameIsStillTheLocalInterface(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			name: "qualified impl, bare bound",
			src: `import std::calendar

interface DateParts
    fn stamp(value: self) -> String
end

struct Q
    n: Int
end

impl calendar.DateParts for Q
    fn year(value: Q) -> Int
        value.n
    end

    fn month(_: Q) -> Int
        1
    end

    fn day(_: Q) -> Int
        2
    end
end

fn local<T>(x: T, y: T) -> String where T: DateParts
    DateParts.stamp(x) + DateParts.stamp(y)
end

fn main()
    _s = local(Q{n: 1}, Q{n: 2})
end
`,
			want: "Q does not implement DateParts",
		},
		{
			name: "bare impl, qualified parameter",
			src: `import std::calendar

interface DateParts
    fn stamp(value: self) -> String
end

struct Q
    n: Int
end

impl DateParts for Q
    fn stamp(_: Q) -> String
        "local"
    end
end

fn as_value(x: calendar.DateParts) -> Int
    calendar.DateParts.day(x)
end

fn main()
    _d = as_value(Q{n: 1})
end
`,
			want: "expected DateParts, got Q",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := buildSiblingProject(t, tc.src, nil)
			if len(errs) != 1 || !strings.Contains(errs[0].Message, tc.want) {
				t.Fatalf("want exactly one diagnostic containing %q, got %d:\n  %s", tc.want, len(errs), joinMessages(errs))
			}
		})
	}
}

// Hover and go to definition read References: the qualified spelling in a
// bound, a parameter type and an impl header names std's declaration, and
// the bare one the file's own.
func TestInterfaceIdentity_QualifiedReferencesNameTheModuleDeclaration(t *testing.T) {
	fa := buildEntry(t, qualifiedStdEntry)
	lines := strings.Split(qualifiedStdEntry, "\n")
	origins := map[string]int{}
	for i, line := range lines {
		for _, spelling := range []string{"calendar.DateParts for", "T: calendar.DateParts", "x: calendar.DateParts", "T: DateParts", "impl DateParts"} {
			at := strings.Index(line, spelling)
			if at < 0 {
				continue
			}
			col := at + strings.Index(spelling, "DateParts") + 1
			sym := fa.References[analysis.Pos{Line: i + 1, Col: col}]
			for sym != nil && sym.Resolved != nil {
				sym = sym.Resolved
			}
			if sym == nil {
				t.Fatalf("line %d %q: no reference at the interface name", i+1, spelling)
			}
			it, ok := sym.Type.(*analysis.InterfaceType)
			if !ok {
				t.Fatalf("line %d %q: the reference is %T, not an interface", i+1, spelling, sym.Type)
			}
			qualified := strings.Contains(spelling, "calendar.")
			if qualified != (it.Origin == "std/calendar") {
				t.Errorf("line %d %q names the interface declared at %q", i+1, spelling, it.Origin)
			}
			origins[spelling]++
		}
	}
	if len(origins) != 5 {
		t.Fatalf("found %d of the 5 spellings: %v", len(origins), origins)
	}
}

// buildEntry analyzes src as a project's entry and fails on any diagnostic.
func buildEntry(t *testing.T, src string) *analysis.FileAnalysis {
	t.Helper()
	fa, errs := buildSiblingProjectFA(t, src, nil)
	if len(errs) != 0 {
		t.Fatalf("unexpected diagnostics:\n  %s", joinMessages(errs))
	}
	return fa
}
