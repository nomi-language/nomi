package analysis

import (
	"fmt"
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// patternRecord is what the checker recorded for the pattern of node type
// nodeType that starts at line and col: the type it matches and the
// variant it names.
type patternRecord struct {
	typ     string
	variant string
}

// patternRecordsAt gathers the records of every pattern in nodes, keyed by
// the pattern's node type and position.
func patternRecordsAt(fa *FileAnalysis, nodes []ast.Node) map[string][]patternRecord {
	out := map[string][]patternRecord{}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			t, typed := fa.PatternTypes[n]
			if !typed {
				return true
			}
			key := patternKey(n.NodeType(), n.LineNum())
			out[key] = append(out[key], patternRecord{typ: typeString(t), variant: fa.PatternVariants[patternHeadOf(n)]})
			return true
		})
	}
	return out
}

// patternHeadOf is the head of a pattern that may name a variant, or nil.
func patternHeadOf(n ast.Node) ast.TypeExpr {
	switch p := n.(type) {
	case *ast.EnumPattern:
		return p.Variant
	case *ast.StructPattern:
		return p.TypeName
	case *ast.ListPattern:
		return p.TypeName
	case *ast.MapPattern:
		return p.TypeName
	}
	return nil
}

func patternKey(nodeType string, line int) string {
	return fmt.Sprintf("%s@%d", nodeType, line)
}

// checkPatternSource checks src, importing modules, and answers its nodes
// with the analysis.
func checkPatternSource(t *testing.T, src string, modules map[string]string) (*FileAnalysis, []ast.Node) {
	t.Helper()
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	nodes, _ = LowerDerives(nodes)
	fa := BuildFileWithStdlib(nodes, nil, nil, "/project", reExportLoader(t, modules))
	errs := append(append([]TypeError{}, fa.TypeErrors...), BuildTypes(fa, nodes)...)
	errs = append(errs, CheckTypes(fa, nodes)...)
	for _, e := range errs {
		t.Errorf("unexpected error: %v", e)
	}
	if t.Failed() {
		t.FailNow()
	}
	return fa, nodes
}

// Every pattern, in every position and of every kind, records the type of
// the value it matches, and one that names a variant records which, by its
// declared name, qualified or `.Variant`.
func TestPatternTypes_EveryPositionAndKind(t *testing.T) {
	src := `enum Opt<T>
    Has(T)
    Empty
end

enum Shape
    Circle(Float)
    Rect{w: Int, h: Int}
    Bag(List<Int>)
end

struct Point
    x: Int
    y: Int
end

type Id Int

fn area(s: Shape) -> Int
    case s
        Shape.Circle(_) -> 0
        .Rect{w:, h:} -> w * h
        .Bag[first, ..] -> first
        .Bag(_) -> 0
    end
end

fn sum_point(Point{x:, y:}) -> Int
    x + y
end

fn raw(Id(n)) -> Int
    n
end

fn pick<T>(m: Opt<T>, fallback: T) -> T
    case m
        .Has(v) -> v
        Opt.Empty -> fallback
    end
end

fn demo(m: Opt<(Int, String)>) -> Int
    .Has((n, _)) = m else
        return 0
    end
    Id(k) = Id(3)
    (a, b) = (1, 2)
    f = fn (Point{x:, y: _}) -> x
    if .Has((q, _)) = m then
        return q
    end
    found = m is .Has((1, "x") or (2, _))
    _ = found
    n + k + a + b + f(Point{x: 1, y: 2})
end`
	fa, nodes := checkPatternSource(t, src, nil)
	got := patternRecordsAt(fa, nodes)
	for _, c := range []struct {
		key  string
		want patternRecord
	}{
		{patternKey("EnumPattern", 21), patternRecord{"Shape", "Circle"}},           // case, qualified
		{patternKey("WildcardPattern", 21), patternRecord{"Float", ""}},             // payload
		{patternKey("StructPattern", 22), patternRecord{"Shape", "Rect"}},           // case, `.Variant` struct
		{patternKey("ListPattern", 23), patternRecord{"Shape", "Bag"}},              // case, `.Variant` list
		{patternKey("IdentPattern", 23), patternRecord{"Int", ""}},                  // list element
		{patternKey("WildcardPattern", 23), patternRecord{"List<Int>", ""}},         // bare `..`
		{patternKey("StructPattern", 28), patternRecord{"Point", ""}},               // parameter, struct
		{patternKey("EnumPattern", 32), patternRecord{"Id", ""}},                    // parameter, distinct
		{patternKey("EnumPattern", 38), patternRecord{"Opt<T>", "Has"}},             // generic, `.Variant`
		{patternKey("EnumPattern", 39), patternRecord{"Opt<T>", "Empty"}},           // generic, qualified
		{patternKey("EnumPattern", 44), patternRecord{"Opt<(Int, String)>", "Has"}}, // binding with else
		{patternKey("TuplePattern", 44), patternRecord{"(Int, String)", ""}},        // nested tuple
		{patternKey("EnumPattern", 47), patternRecord{"Id", ""}},                    // destructure statement
		{patternKey("StructPattern", 49), patternRecord{"Point", ""}},               // lambda parameter
		{patternKey("WildcardPattern", 49), patternRecord{"Int", ""}},               // lambda field pattern
		{patternKey("EnumPattern", 50), patternRecord{"Opt<(Int, String)>", "Has"}}, // if pattern
		{patternKey("EnumPattern", 53), patternRecord{"Opt<(Int, String)>", "Has"}}, // is
		{patternKey("OrPattern", 53), patternRecord{"(Int, String)", ""}},           // or
	} {
		if !slices.Contains(got[c.key], c.want) {
			t.Errorf("%s: got %+v, want %+v", c.key, got[c.key], c.want)
		}
	}
	if t.Failed() {
		t.Logf("records: %v", got)
	}
}

// A parameter destructuring a distinct type another module declares
// records the type: the IR builder reads it for the parameter's kind when
// it lowers the signature. The declarations' typing records it
// (paramPatternType), so a std module whose bodies are checked later has it
// too (std's TestShared_LazyModulesAreEagerModules).
func TestPatternTypes_ParameterOfAnotherModulesDistinct(t *testing.T) {
	modules := map[string]string{"jobs": "pub type JobId Int\n\npub fn make(n: Int) -> JobId\n    JobId(n)\nend\n"}
	src := `import jobs.{JobId}

fn id_of(JobId(n)) -> Int
    n
end`
	fa, nodes := checkPatternSource(t, src, modules)
	got := patternRecordsAt(fa, nodes)
	if r := got[patternKey("EnumPattern", 3)]; !slices.Contains(r, patternRecord{"JobId", ""}) {
		t.Errorf("the parameter's pattern records %+v, want the type JobId", r)
	}
}

// A variant a pattern names through an import alias is recorded by its
// declared name, in each pattern shape that names one.
func TestPatternTypes_AliasedVariantIsItsDeclaredName(t *testing.T) {
	modules := map[string]string{"shapes": `pub enum Bag
    Items(List<Int>)
    Named(Map<String, Int>)
    One(Int)
end
`}
	src := `import shapes.{Bag}
import shapes.Bag.{Items as I, Named as N, One as O}

fn first(b: Bag) -> Int
    case b
        I[x, ..] -> x
        N{"k" => v} -> v
        O(n) -> n
        _ -> 0
    end
end`
	fa, nodes := checkPatternSource(t, src, modules)
	got := patternRecordsAt(fa, nodes)
	for _, c := range []struct {
		key     string
		variant string
	}{
		{patternKey("ListPattern", 6), "Items"},
		{patternKey("MapPattern", 7), "Named"},
		{patternKey("EnumPattern", 8), "One"},
	} {
		if !slices.Contains(got[c.key], patternRecord{"Bag", c.variant}) {
			t.Errorf("%s: got %+v, want the variant %s", c.key, got[c.key], c.variant)
		}
	}
}
