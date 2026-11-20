package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// destructureSrc writes a tuple and a struct destructuring statement in
// each place a statement stands: a function body, a test body, an `if`
// arm and a block, over a generic struct and an anonymous one, with `_`
// discards and a name bound again.
const destructureSrc = `struct Box<T>
    item: T
    label: String
end

fn body(b: Box<Int>) -> Int
    (n, _) = (b.item, "x")
    {item: m, label:} = b
    (n, k) = (n + m, label)
    if k == label then n else m
end

fn arm(flag: Bool) -> Int
    if flag then
        (a, _, c) = (1, 2, 3)
        {x:, y: z} = {x: a, y: c}
        x + z
    else
        0
    end
end

fn block() -> Int
    total = begin
        {item:} = Box{item: 4, label: "four"}
        (p, q) = (item, item)
        p + q
    end
    total
end

test "a test body"
    (s, _) = ("a", 1)
    {label: name} = Box{item: 1, label: s}
    assert name == "a"
end
`

// destructurePatterns is the pattern of every destructuring statement in
// nodes: the tuple or struct pattern a PatternBinding with no `else` binds.
func destructurePatterns(nodes []ast.Node) []ast.Node {
	var out []ast.Node
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			if pb, ok := n.(*ast.PatternBinding); ok && pb.Else == nil {
				switch pb.Pattern.(type) {
				case *ast.TuplePattern, *ast.StructPattern:
					out = append(out, pb.Pattern)
				}
			}
			return true
		})
	}
	return out
}

// TestDestructurePattern_RecordsTheStatementAndItsParts holds each
// destructuring statement, in every place one stands, to the records a
// pattern gets: the pattern's type under the pattern, each name's and each
// discard's under its own node, and each name's symbol typed.
func TestDestructurePattern_RecordsTheStatementAndItsParts(t *testing.T) {
	fa, nodes := checkNodes(t, destructureSrc)
	stmts := destructurePatterns(nodes)
	if len(stmts) != 9 {
		t.Fatalf("want 9 destructuring statements in the source, found %d", len(stmts))
	}
	want := map[string]string{
		"n": "Int", "m": "Int", "label": "String", "k": "String",
		"a": "Int", "c": "Int", "x": "Int", "z": "Int",
		"item": "Int", "p": "Int", "q": "Int",
		"s": "String", "name": "String",
	}
	for _, s := range stmts {
		ty, ok := fa.PatternTypes[s]
		if !ok || ty == nil {
			t.Errorf("%T at %d has no PatternTypes record", s, s.LineNum())
		}
		for _, site := range patternSites(s) {
			sym := fa.Definitions[site.pos]
			if sym == nil || sym.Type == nil {
				t.Errorf("%s at %v has no typed symbol", site.name, site.pos)
				continue
			}
			if got := sym.Type.String(); got != want[site.name] {
				t.Errorf("%s at %v is %s, want %s", site.name, site.pos, got, want[site.name])
			}
		}
		if tp, isTuple := s.(*ast.TuplePattern); isTuple {
			tt, _ := ty.(*TupleType)
			for i, part := range tp.Patterns {
				got, ok := fa.PatternTypes[part]
				if !ok || tt == nil || !TypesEqual(got, tt.Elems[i]) {
					t.Errorf("part %d of the tuple at line %d records %v, want %v", i, tp.Line, got, tt)
				}
			}
		}
	}
	if vs := besidesStdlessOperators(VerifyElaboration(fa, nodes)); len(vs) > 0 {
		t.Fatalf("want nothing reported, got %v", vs)
	}
}

// TestDestructurePattern_TheGateHoldsTheRecords removes the record of each
// destructuring statement's pattern, and of a tuple's name, and holds
// VerifyElaboration to reporting it.
func TestDestructurePattern_TheGateHoldsTheRecords(t *testing.T) {
	fa, nodes := checkNodes(t, destructureSrc)
	for _, s := range destructurePatterns(nodes) {
		ty := fa.PatternTypes[s]
		delete(fa.PatternTypes, s)
		if !hasElabCategory(VerifyElaboration(fa, nodes), s, ElabPatternUnrecorded) {
			t.Errorf("a %T at line %d with no record is not reported", s, s.LineNum())
		}
		fa.PatternTypes[s] = ty
		tp, isTuple := s.(*ast.TuplePattern)
		if !isTuple {
			continue
		}
		name, isName := tp.Patterns[0].(*ast.IdentPattern)
		if !isName {
			continue
		}
		ty = fa.PatternTypes[name]
		delete(fa.PatternTypes, name)
		if !hasElabCategory(VerifyElaboration(fa, nodes), name, ElabPatternUnrecorded) {
			t.Errorf("the name %s at line %d with no record is not reported", name.Name, name.Line)
		}
		fa.PatternTypes[name] = ty
	}
}

// TestDestructurePattern_StatementsGiveThePatternsMessages holds each
// rejected destructuring statement to the message the same pattern gets in
// a `case` arm.
func TestDestructurePattern_StatementsGiveThePatternsMessages(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"a tuple over an Int", "fn f()\n    (a, b) = 5\nend\n", "tuple pattern requires a tuple type, got Int"},
		{"a tuple of the wrong arity", "fn f()\n    (a, b) = (1, 2, 3)\nend\n", "tuple pattern has 2 elements but expected 3"},
		{"a struct over an Int", "fn f()\n    {w:} = 5\nend\n", "struct pattern requires a struct type, got Int"},
		{"a struct over an enum", "enum Shape\n    Rect {w: Int}\nend\nfn f(s: Shape)\n    {w:} = s\nend\n", "struct pattern requires a struct type, got Shape"},
		{"an unknown field", "struct P\n    x: Int\nend\nfn f(p: P)\n    {y:} = p\nend\n", "field y not found in type P"},
		{"a name not in snake_case", "struct P\n    x: Int\nend\nfn f(p: P)\n    {x: bigX} = p\nend\n", "binding name \"bigX\""},
		{"a tuple binding a name twice", "fn f() -> Int\n    (a, a) = (1, 2)\n    a\nend\n", "`a` is bound twice in one pattern"},
		{"a struct binding a name twice", "struct P\n    x: Int\n    y: Int\nend\nfn f(p: P) -> Int\n    {x: a, y: a} = p\n    a\nend\n", "`a` is bound twice in one pattern"},
		{"a distinct over an Int", "type Meters Int\nfn f() -> Int\n    Meters(n) = 5\n    n\nend\n", "pattern names Meters, but the value is an Int"},
		{"a distinct over an Int in a case arm", "type Meters Int\nfn f() -> Int\n    case 5\n        Meters(n) -> n\n        _ -> 0\n    end\nend\n", "pattern names Meters, but the value is an Int"},
		{"a distinct over another distinct", "type Meters Int\ntype Feet Int\nfn f(d: Feet) -> Int\n    Meters(n) = d\n    n\nend\n", "pattern names Meters, but the value is a Feet"},
		{"a distinct's name not in snake_case", "type Meters Int\nfn f(m: Meters) -> Int\n    Meters(bigN) = m\n    bigN\nend\n", "binding name \"bigN\" must be snake_case"},
		{"a variant's name not in snake_case in a case arm", "enum Opt\n    Has(Int)\n    Empty\nend\nfn f(m: Opt) -> Int\n    case m\n        Opt.Has(bigN) -> bigN\n        Opt.Empty -> 0\n    end\nend\n", "binding name \"bigN\" must be snake_case"},
		{"a variant the enum lacks", "enum Opt\n    Has(Int)\n    Empty\nend\nfn f(m: Opt) -> Int\n    Opt.Many(n) = m\n    n\nend\n", "variant Many not found in enum Opt"},
		{"a variant of an enum of two", "enum Opt\n    Has(Int)\n    Empty\nend\nfn f(m: Opt) -> Int\n    Opt.Has(n) = m\n    n\nend\n", "this pattern can fail to match a value of type Opt"},
		{"a variant that carries no data", "enum E\n    A\nend\nfn f(e: E)\n    E.A(n) = e\nend\n", "variant A carries no data; use the bare pattern A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(tc.src)
			for _, e := range errs {
				if strings.Contains(e.Message, tc.want) {
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", tc.want, errs)
		})
	}
}
