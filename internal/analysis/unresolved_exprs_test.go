package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

func checkNodes(t *testing.T, src string) (*FileAnalysis, []ast.Node) {
	t.Helper()
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("the source does not parse: %v", perrs)
	}
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	errs := append(BuildTypes(fa, nodes), CheckTypes(fa, nodes)...)
	errs = append(errs, fa.TypeErrors...)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	return fa, nodes
}

const unresolvedSrc = `struct Point
    x: Int
    y: Int
end

struct Foo
    a: Int
end

interface Counter
    fn count(c: self, n: Int) -> Int
end

impl Counter for Point
    fn count(p: Point, n: Int) -> Int
        if n <= 0 then p.x else count(p, n - 1)
    end
end

fn twice(f: (Int) -> Int, n: Int) -> Int
    f(f(n))
end

fn main_like(p: Point) -> Int
    q = {..p, y: 2}
    Point{x:, y:} = q
    foo = Foo({a: x})
    shifted = twice(fn n -> n + y, foo.a)
    case shifted
        0 -> 1
        n -> n * 2
    end
end
`

// TestUnresolvedExprs_AWellTypedFileReportsNothing walks a file holding
// the positions that are not values (field names, patterns, a same-owner
// call's callee, a struct call form's record) and finds every value typed.
func TestUnresolvedExprs_AWellTypedFileReportsNothing(t *testing.T) {
	fa, nodes := checkNodes(t, unresolvedSrc)
	if got := UnresolvedExprs(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
}

// TestUnresolvedExprs_ReportsAMissingAndAnUnsolvedType holds the walk to
// what it is for: an expression with no recorded type is reported once, at
// its root, and one whose type is an unsolved variable is reported.
func TestUnresolvedExprs_ReportsAMissingAndAnUnsolvedType(t *testing.T) {
	fa, nodes := checkNodes(t, unresolvedSrc)
	var missing, unsolved ast.Node
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Call:
				if id, ok := n.Func.(*ast.Ident); ok && id.Name == "twice" && missing == nil {
					missing = n
				}
			case *ast.Ident:
				if n.Name == "shifted" && unsolved == nil {
					if _, ok := fa.ExprTypes[n]; ok {
						unsolved = n
					}
				}
			}
			return true
		})
	}
	if missing == nil || unsolved == nil {
		t.Fatalf("the source has no call to twice (%v) or no typed read of shifted (%v)", missing, unsolved)
	}
	delete(fa.ExprTypes, missing)
	fa.ExprTypes[unsolved] = &TypeVar{ID: 99}
	got := UnresolvedExprs(fa, nodes)
	var lines []string
	for _, u := range got {
		lines = append(lines, u.String())
	}
	want := []string{
		"28:15: Call in Binding has no type recorded",
		"29:10: Ident shifted in Case has an unsolved type variable in ?99",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// TestUnresolvedExprs_APipedStructCallFormIsTyped: `{a: 1} |> Foo()` is the
// struct call form with the piped value as its record, and the pipe has the
// struct's type. The checker once returned no type for it.
func TestUnresolvedExprs_APipedStructCallFormIsTyped(t *testing.T) {
	fa, nodes := checkNodes(t, `struct Foo
    a: Int
end

fn f() -> Int
    foo = {a: 1} |> Foo()
    foo.a
end
`)
	if got := UnresolvedExprs(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
}

// TestUnresolvedExprs_APipeIntoALocalFunctionValueIsTyped: a local bound to
// a function value whose type a generic call solves late (`Opt.Empty` leaves
// `pick`'s T open until the lambda fixes it, as `Err("no")` leaves
// `Result.with_default`'s) holds an inference variable, and a pipe stage
// calling it has the function's result type. The checker once read only a
// bare function type there and recorded nothing.
func TestUnresolvedExprs_APipeIntoALocalFunctionValueIsTyped(t *testing.T) {
	fa, nodes := checkNodes(t, `enum Opt<T>
    Has T
    Empty
end

fn pick<T>(o: Opt<T>, fallback: T) -> T
    case o
        .Has(v) -> v
        .Empty -> fallback
    end
end

fn f() -> Int
    bump = pick(Opt.Empty, fn x: Int -> x + 1)
    1 |> bump()
end
`)
	if got := UnresolvedExprs(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
}
