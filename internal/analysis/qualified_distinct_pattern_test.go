package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const qualifiedDistinctKinds = `pub type Items List<Int>

pub type Other List<Int>

pub type Kvs Map<String, Int>

pub type Path Vector<Int>

pub type Pt (Int, Int)

pub type Qt (Int, Int)
`

// A pattern names a distinct type through its module as it names it bare:
// the list, map, vector and constructor prefixes each resolve.
func TestQualifiedDistinctPrefix_Accepted(t *testing.T) {
	src := `import kinds
import kinds as k2

fn f(xs: kinds.Items, m: kinds.Kvs, p: kinds.Path, pt: kinds.Pt) -> Int
    a = case xs
        kinds.Items[x, y] -> x + y
        k2.Items[x, ..] -> x
        _ -> 0
    end
    b = case m
        kinds.Kvs{"a" => v} -> v
        _ -> 0
    end
    c = case p
        kinds.Path[x, .., z] -> x + z
        _ -> 0
    end
    d = case pt
        kinds.Pt((x, y)) -> x + y
    end
    a + b + c + d
end
`
	errs := checkTypedLiteralProject(t, src, map[string]string{"kinds": qualifiedDistinctKinds})
	var real []analysis.TypeError
	for _, e := range errs {
		// The test loader leaves the prelude's re-exports unresolved, at
		// synthesized lines; only errors in the source count.
		if !analysis.IsSynthesizedLine(e.Line) {
			real = append(real, e)
		}
	}
	if len(real) != 0 {
		t.Fatalf("the checker rejects module-qualified distinct patterns:\n%s", joinErrs(real))
	}
}

// A qualified prefix that names another distinct type is still an error.
func TestQualifiedDistinctPrefix_OtherTypeRejected(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"list", "case xs\n        kinds.Other[x, y] -> x + y\n        _ -> 0\n    end",
			"pattern names kinds.Other, but the value is an Items"},
		{"map", "case m\n        kinds.Items{\"a\" => v} -> v\n        _ -> 0\n    end",
			"pattern names kinds.Items, but the value is a Kvs"},
		{"constructor", "case pt\n        kinds.Qt((x, y)) -> x + y\n    end",
			"pattern names kinds.Qt, but the value is a Pt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import kinds\n\nfn f(xs: kinds.Items, m: kinds.Kvs, pt: kinds.Pt) -> Int\n    " + tc.body + "\nend\n"
			errs := checkTypedLiteralProject(t, src, map[string]string{"kinds": qualifiedDistinctKinds})
			expectTypedLiteralError(t, errs, tc.want)
		})
	}
}

func joinErrs(errs []analysis.TypeError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Two modules that each declare an `Items`: a type's identity is its
// declaring file and its name, so a pattern that names one module's type
// does not match the other's, whatever their names.
const qualifiedDistinctOther = `pub type Items List<Int>

pub type Kvs Map<String, Int>

pub type Path Vector<Int>

pub type Tags Set<Int>

pub type Pt (Int, Int)
`

func TestQualifiedDistinctPrefix_SameNameOtherModuleRejected(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"list", "case xs\n        other.Items[x, ..] -> x\n        _ -> 0\n    end",
			"pattern names other.Items, but the value is a kinds.Items"},
		{"map", "case m\n        other.Kvs{\"a\" => v} -> v\n        _ -> 0\n    end",
			"pattern names other.Kvs, but the value is a kinds.Kvs"},
		{"vector", "case p\n        other.Path[x, ..] -> x\n        _ -> 0\n    end",
			"pattern names other.Path, but the value is a kinds.Path"},
		{"set", "case s\n        other.Tags[5] -> 1\n        _ -> 0\n    end",
			"pattern names other.Tags, but the value is a kinds.Tags"},
		{"constructor", "case pt\n        other.Pt((x, y)) -> x + y\n    end",
			"pattern names other.Pt, but the value is a kinds.Pt"},
		{"destructure", "other.Pt(q) = pt\n    q.0",
			"pattern names other.Pt, but the value is a kinds.Pt"},
		{"not a module", "case xs\n        nope.Items[x, ..] -> x\n        _ -> 0\n    end",
			`unknown type "nope.Items"`},
		{"not a type", "case xs\n        kinds.items[x, ..] -> x\n        _ -> 0\n    end",
			`unknown type "kinds.items"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import kinds\nimport other\n\nfn f(xs: kinds.Items, m: kinds.Kvs, p: kinds.Path, s: kinds.Tags, pt: kinds.Pt) -> Int\n    " + tc.body + "\nend\n"
			errs := checkTypedLiteralProject(t, src, map[string]string{
				"kinds": qualifiedDistinctKinds + "\npub type Tags Set<Int>\n\npub fn items() -> Items\n    Items([1])\nend\n",
				"other": qualifiedDistinctOther,
			})
			expectTypedLiteralError(t, errs, tc.want)
		})
	}
}

// The right module of the two, through its name, an alias or an imported
// name, is accepted.
func TestQualifiedDistinctPrefix_SameNameRightModuleAccepted(t *testing.T) {
	src := `import
    kinds.{self, Items}
    kinds as k2
    other
end

fn f(xs: kinds.Items, pt: kinds.Pt, ys: other.Items) -> Int
    a = case xs
        kinds.Items[x, ..] -> x
        k2.Items[x, ..] -> x
        Items[x, ..] -> x
        _ -> 0
    end
    b = case ys
        other.Items[x, ..] -> x
        _ -> 0
    end
    kinds.Pt((c, d)) = pt
    a + b + c + d
end
`
	errs := checkTypedLiteralProject(t, src, map[string]string{"kinds": qualifiedDistinctKinds, "other": qualifiedDistinctOther})
	var real []analysis.TypeError
	for _, e := range errs {
		if !analysis.IsSynthesizedLine(e.Line) {
			real = append(real, e)
		}
	}
	if len(real) != 0 {
		t.Fatalf("the checker rejects a pattern naming the scrutinee's own type:\n%s", joinErrs(real))
	}
}

// A literal's prefix resolves through its module as a pattern's does: the
// other module's `Path` builds the other module's type.
func TestQualifiedDistinctLiteral_SameNameOtherModuleRejected(t *testing.T) {
	src := "import kinds\nimport other\n\nfn f() -> kinds.Path\n    other.Path[1, 2]\nend\n"
	errs := checkTypedLiteralProject(t, src, map[string]string{"kinds": qualifiedDistinctKinds, "other": qualifiedDistinctOther})
	expectTypedLiteralError(t, errs, "other.Path")
}
