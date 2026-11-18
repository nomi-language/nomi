package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// typeExprRecordAt is the type TypeExprTypes records for the type
// expression spelled spelling that starts on line, and whether one is.
func typeExprRecordAt(fa *analysis.FileAnalysis, line int, spelling string) (analysis.Type, bool) {
	for te, t := range fa.TypeExprTypes {
		if te.LineNum() == line && te.TypeString() == spelling {
			return t, true
		}
	}
	return nil, false
}

// Every caller class of the type resolver records the type each type
// expression resolves to, nested ones included, and one inside a generic
// declaration records the type with the declaration's own parameters.
func TestTypeExprTypes_RecordEachPosition(t *testing.T) {
	src := `struct Box<T>
    item: T
    rest: List<T>
end

enum Shape
    Circle(Float)
    Named{label: Maybe<String>}
end

typealias Pairs (Int, Int)

subtype Small Int when fn n -> n < 10

subtype Many<T> List<T> when fn xs -> not Iter.is_empty(xs)

interface Sized
    fn size(x: self) -> Int
end

impl Sized for Box<T>
    fn size(b: Box<T>) -> Int
        List.length(b.rest) + 1
    end
end

fn largest<T>(xs: List<T>) -> Maybe<T> where T: Comparable
    List.first(xs)
end

fn count(m: Many<Int>) -> Int
    List.length(m)
end

fn demo() -> (Int, Int, Small, Maybe<String>)
    b: Box<Int> = Box{item: 1, rest: [2]}
    p: Pairs = (1, 2)
    s = 3 as Small
    found = largest<String>(["a"])
    (Sized.size(b), p.0, s, found)
end`
	fa, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
	// The lines the stdlib imports put above the source.
	shift := strings.Count(withStdlibTestImports(src), "\n") - strings.Count(src, "\n")
	for _, c := range []struct {
		line           int
		spelling, want string
	}{
		{2, "T", "T"},                         // struct field
		{3, "List<T>", "List<T>"},             // struct field, generic
		{3, "T", "T"},                         // nested argument
		{7, "Float", "Float"},                 // enum payload
		{8, "Maybe<String>", "Maybe<String>"}, // enum record field
		{11, "(Int, Int)", "(Int, Int)"},      // alias target
		{13, "Int", "Int"},                    // subtype parent
		{15, "List<T>", "List<T>"},            // generic subtype parent
		{21, "Sized", "Sized"},                // impl header interface
		{21, "Box<T>", "Box<T>"},              // impl header receiver
		{22, "Box<T>", "Box<T>"},              // impl fn parameter
		{27, "List<T>", "List<T>"},            // fn parameter
		{27, "Maybe<T>", "Maybe<T>"},          // fn return
		{27, "Comparable", "Comparable"},      // where bound
		{31, "Many<Int>", "Many<Int>"},        // generic refinement
		{36, "Box<Int>", "Box<Int>"},          // binding annotation
		{36, "Int", "Int"},                    // its argument
		{37, "Pairs", "(Int, Int)"},           // an alias names its target
		{38, "Small", "Small"},                // ascription
		{39, "String", "String"},              // type argument at a call
	} {
		got, ok := typeExprRecordAt(fa, c.line+shift, c.spelling)
		if !ok || got == nil {
			t.Errorf("line %d: `%s` has no recorded type (recorded: %v)", c.line, c.spelling, ok)
			continue
		}
		if got.String() != c.want {
			t.Errorf("line %d: `%s` records %s, want %s", c.line, c.spelling, got, c.want)
		}
	}
	// Inside the generic declaration, the parameter is the declaration's
	// own, not an instance's.
	got, _ := typeExprRecordAt(fa, 3+shift, "List<T>")
	if lt, ok := got.(*analysis.ListType); !ok || !isTypeParam(lt.Elem) {
		t.Errorf("Box's `List<T>` records %#v, want a List of the parameter T", got)
	}
}

func isTypeParam(t analysis.Type) bool {
	_, ok := t.(*analysis.TypeParam_)
	return ok
}

// A type expression that does not resolve is recorded with no type, so the
// elaboration gate can tell it from one the checker never reached.
func TestTypeExprTypes_FailureRecordsNoType(t *testing.T) {
	src := `fn demo() -> Int
    x: Missing = 1
    x
end`
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("the check accepts an annotation naming no type")
	}
	got, ok := typeExprRecordAt(fa, 2, "Missing")
	if !ok || got != nil {
		t.Fatalf("`Missing` records %v (recorded: %v), want a record with no type", got, ok)
	}
}

// The gate holds every type expression of a checked file to a record.
func TestTypeExprTypes_GateFindsNoneUnrecorded(t *testing.T) {
	src := `struct Pair<A, B>
    left: A
    right: B
end

fn swap<A, B>(p: Pair<A, B>) -> Pair<B, A>
    Pair{left: p.right, right: p.left}
end

fn demo() -> String
    p: Pair<Int, String> = Pair{left: 1, right: "a"}
    case Some(swap(p))
        Some(Pair{left: l, right: _}) -> l
        None -> ""
    end
end`
	fa, nodes, errs := checkSourceNodesWithStdlib(src)
	expectNoErrorsT(t, errs)
	for _, v := range analysis.VerifyElaboration(fa, nodes) {
		if v.Category == analysis.ElabTypeExprUnrecorded {
			t.Errorf("%s", v)
		}
	}
	var heads int
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if te, ok := n.(ast.TypeExpr); ok {
				if _, recorded := fa.TypeExprTypes[te]; recorded {
					heads++
				}
			}
			return true
		})
	}
	if heads == 0 {
		t.Fatal("no type expression has a record")
	}
}
