package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// genericSelfDecls is a generic interface whose functions name `self` in
// every position, with one implementor of it and one that implements it
// at two type arguments.
const genericSelfDecls = `interface Scale<N>
    fn scale(value: self, by: N) -> self
    fn grown(value: self, by: N) -> Maybe<self>
    fn paired(value: self, by: N) -> (self, N)
    fn same(value: self, other: self) -> Bool
    fn many(value: self) -> List<self>
    fn maker(value: self) -> (N) -> self
    fn amount(value: self) -> N

    fn twice(value: self, by: N) -> self
        scale(scale(value, by), by)
    end
end

struct Money
    cents: Int
end

impl Scale<Int> for Money
    fn scale(value: Money, by: Int) -> Money
        Money{cents: value.cents * by}
    end

    fn grown(value: Money, by: Int) -> Maybe<Money>
        Some(Money{cents: value.cents + by})
    end

    fn paired(value: Money, by: Int) -> (Money, Int)
        (value, by)
    end

    fn same(value: Money, other: Money) -> Bool
        value.cents == other.cents
    end

    fn many(value: Money) -> List<Money>
        [value, value]
    end

    fn maker(value: Money) -> (Int) -> Money
        fn n -> Money{cents: value.cents + n}
    end

    fn amount(value: Money) -> Int
        value.cents
    end
end

struct Tag
    text: String
end

impl Scale<Int> for Tag
    fn scale(value: Tag, by: Int) -> Tag
        Tag{text: "${value.text}${by}"}
    end

    fn grown(value: Tag, _by: Int) -> Maybe<Tag>
        Some(value)
    end

    fn paired(value: Tag, by: Int) -> (Tag, Int)
        (value, by)
    end

    fn same(value: Tag, other: Tag) -> Bool
        value.text == other.text
    end

    fn many(value: Tag) -> List<Tag>
        [value]
    end

    fn maker(value: Tag) -> (Int) -> Tag
        fn n -> Tag{text: "${value.text}${n}"}
    end

    fn amount(value: Tag) -> Int
        String.length(value.text)
    end
end

impl Scale<String> for Tag
    fn scale(value: Tag, by: String) -> Tag
        Tag{text: value.text + by}
    end

    fn grown(value: Tag, _by: String) -> Maybe<Tag>
        Some(value)
    end

    fn paired(value: Tag, by: String) -> (Tag, String)
        (value, by)
    end

    fn same(value: Tag, other: Tag) -> Bool
        value.text == other.text
    end

    fn many(value: Tag) -> List<Tag>
        [value]
    end

    fn maker(value: Tag) -> (String) -> Tag
        fn s -> Tag{text: value.text + s}
    end

    fn amount(value: Tag) -> String
        value.text
    end
end
`

// genericSelfCalls are functions each holding one call of a Scale
// function, its result typed by the function's result.
const genericSelfCalls = `
fn qualified(m: Money) -> Money
    Scale.scale(m, 2)
end

fn type_qualified(m: Money) -> Money
    Money.scale(m, 2)
end

fn piped(m: Money) -> Money
    m |> Scale.scale(2)
end

fn in_maybe(m: Money) -> Maybe<Money>
    Scale.grown(m, 1)
end

fn in_tuple(m: Money) -> (Money, Int)
    Scale.paired(m, 1)
end

fn second_self(m: Money) -> Bool
    Scale.same(m, m)
end

fn in_list(m: Money) -> List<Money>
    Scale.many(m)
end

fn in_function(m: Money) -> (Int) -> Money
    Scale.maker(m)
end

fn from_the_impl(m: Money) -> Int
    Scale.amount(m)
end

fn inherited(m: Money) -> Money
    Scale.twice(m, 2)
end

fn inherited_type_qualified(m: Money) -> Money
    Money.twice(m, 2)
end

fn chosen_by_argument(t: Tag) -> Tag
    Scale.scale(t, "!")
end

fn chosen_by_result(t: Tag) -> String
    Scale.amount(t)
end

fn through_bound<T>(x: T) -> T where T: Scale<Int>
    Scale.scale(x, 2)
end

fn through_parameter<T>(x: T) -> T where T: Scale<Int>
    T.scale(x, 2)
end

fn amounts(ms: List<Money>) -> List<Int>
    Iter.map(ms, Scale.amount) |> Iter.to_list()
end

fn main()
    m = Money{cents: 5}
    _ = qualified(m)
    _ = through_bound(m)
end
`

// A call of a generic interface's function is for the type its `self`
// is solved to: `self` there is that type in every position it appears,
// and the interface's own parameters are the arguments of that type's
// impl (spec §12 *Generic interfaces*). The expression's type, the
// record's `For` and its `self` type argument all name the implementor,
// however the call is spelled.
func TestGenericInterfaceSelf_IsTheImplementor(t *testing.T) {
	fa, nodes := checkWithStd(t, genericSelfDecls+genericSelfCalls)
	want := map[string]struct{ ty, self string }{
		"qualified":                {"Money", "Money"},
		"type_qualified":           {"Money", ""},
		"piped":                    {"Money", "Money"},
		"in_maybe":                 {"Maybe<Money>", "Money"},
		"in_tuple":                 {"(Money, Int)", "Money"},
		"second_self":              {"Bool", "Money"},
		"in_list":                  {"List<Money>", "Money"},
		"in_function":              {"(Int) -> Money", "Money"},
		"from_the_impl":            {"Int", "Money"},
		"inherited":                {"Money", "Money"},
		"inherited_type_qualified": {"Money", "Money"},
		"chosen_by_argument":       {"Tag", ""},
		"chosen_by_result":         {"String", ""},
		"through_bound":            {"T", "T"},
		"through_parameter":        {"T", "T"},
	}
	for name, w := range want {
		fn := genericSelfFunc(nodes, name)
		if fn == nil {
			t.Fatalf("no fn %s", name)
		}
		last := tailExpr(fn.Body)
		if got := typeText(fa.ExprTypes[last]); got != w.ty {
			t.Errorf("%s: the call is typed %s, want %s", name, got, w.ty)
		}
		call := genericSelfScaleCall(fn)
		if call == nil {
			t.Fatalf("%s: no call of a Scale function", name)
		}
		rc := fa.Calls[call]
		if rc == nil {
			t.Errorf("%s: the call has no record", name)
			continue
		}
		if w.self == "" {
			// A call the type names, or whose impl the checker selected
			// among several, names the impl's function: no `self` is
			// left to solve.
			continue
		}
		if got := typeText(rc.Callee.For); got != w.self {
			t.Errorf("%s: the call is recorded for %s, want %s", name, got, w.self)
		}
		if name == "through_parameter" {
			// `T.scale` names the bound's function, typed at T
			// (checkTypeParamQualifiedCall): its For is T and it solves
			// no `self`.
			continue
		}
		if rc.Callee.Ref == nil {
			t.Errorf("%s: the call has no reference", name)
			continue
		}
		if got := typeText(rc.Callee.Ref.CallTypeArgs["self"]); got != w.self {
			t.Errorf("%s: the call's self type argument is %s, want %s", name, got, w.self)
		}
	}
	if vs := analysis.VerifyElaboration(fa, nodes); len(vs) > 0 {
		t.Fatalf("elaboration violations: %v", vs)
	}
}

// std's generic interfaces type a call through them by the implementor
// too, and a call of Iter's functions types as it did: a pipeline keeps
// its element type, and a lambda's parameter is the element.
func TestGenericInterfaceSelf_StdInterfaces(t *testing.T) {
	src := `import
    std::parse.Parse
end

struct Meters
    n: Int
end

impl Steppable<Int> for Meters
    fn step_by(value: Meters, by: Int) -> Maybe<Meters>
        Some(Meters{n: value.n + by})
    end
end

fn stepped(m: Meters) -> Maybe<Meters>
    Steppable.step_by(m, 4)
end

fn stepped_piped(m: Meters) -> Maybe<Meters>
    m |> Steppable.step_by(4)
end

fn parsed_by_argument() -> Result<Int, String>
    Parse.parse<Int>("42")
end

fn parsed_by_result() -> Result<Int, String>
    Parse.parse("42")
end

fn contained(xs: List<Int>) -> Bool
    Contains.contains(xs, 2)
end

fn counted(xs: List<Int>) -> Maybe<Int>
    Iter.known_count(xs)
end

fn pipeline(xs: List<Int>) -> List<Int>
    xs |> Iter.map(fn x -> x + 1) |> Iter.to_list()
end

fn walked(xs: List<Int>) -> Bool
    Iter.each_while(xs, fn x -> x < 2)
end
`
	fa, nodes := checkWithStd(t, src)
	for name, want := range map[string]string{
		"stepped":            "Maybe<Meters>",
		"stepped_piped":      "Maybe<Meters>",
		"parsed_by_argument": "Result<Int, String>",
		"parsed_by_result":   "Result<Int, String>",
		"contained":          "Bool",
		"counted":            "Maybe<Int>",
		"pipeline":           "List<Int>",
		"walked":             "Bool",
	} {
		fn := genericSelfFunc(nodes, name)
		if got := typeText(analysis.ResolveTypeVar(fa.ExprTypes[tailExpr(fn.Body)])); got != want {
			t.Errorf("%s: the call is typed %s, want %s", name, got, want)
		}
	}
	walked := genericSelfFunc(nodes, "walked")
	ast.Inspect(walked, func(n ast.Node) bool {
		if l, ok := n.(*ast.Lambda); ok {
			if got := typeText(analysis.ResolveTypeVar(fa.ExprTypes[l])); got != "(Int) -> Bool" {
				t.Errorf("walked: the lambda is typed %s, want (Int) -> Bool", got)
			}
		}
		return true
	})
	if vs := analysis.VerifyElaboration(fa, nodes); len(vs) > 0 {
		t.Fatalf("elaboration violations: %v", vs)
	}
}

// Inside the interface's own default, a bare call of its function on the
// default's `self` is for that `self`, which each instance binds.
func TestGenericInterfaceSelf_InADefaultIsTheDefaultsSelf(t *testing.T) {
	fa, nodes := checkWithStd(t, genericSelfDecls+genericSelfCalls)
	var body *ast.Block
	for _, n := range nodes {
		if id, ok := n.(*ast.InterfaceDef); ok && id.Name == "Scale" {
			for _, m := range id.Methods {
				if b, ok := m.Body.(*ast.Block); ok && m.Name == "twice" {
					body = b
				}
			}
		}
	}
	if body == nil {
		t.Fatal("no default twice")
	}
	last := tailExpr(body)
	ty, ok := fa.ExprTypes[last].(*analysis.InterfaceType)
	if !ok || !ty.DefaultSelf || ty.Name != "Scale" {
		t.Errorf("the default's call is typed %v, want the default's own self", fa.ExprTypes[last])
	}
}

// A call that returns `self` on an interface value names no implementor:
// it is the error the spec gives for a non-generic interface (§12
// *Interface values*), not an interface value of the result.
func TestGenericInterfaceSelf_OnAnInterfaceValueReturnsNoSelf(t *testing.T) {
	src := genericSelfDecls + `
fn main()
    s: Scale<Int> = Money{cents: 5}
    dbg Scale.amount(s)
    dbg Scale.scale(s, 2)
end
`
	errs := identityDiagTexts(identityDiags(t, src, nil))
	want := "`Scale.scale` cannot be called on `s`, a `Scale`: its result is `self`, the implementor's own type, and a `Scale` value does not say which implementor it holds"
	if len(errs) != 1 || !strings.HasPrefix(errs[0], want) {
		t.Fatalf("got %q, want one error %q", errs, want)
	}
	if help := "`where T: Scale<Int>`"; !strings.Contains(errs[0], help) {
		t.Errorf("got %q, want the help to name the bound %s", errs[0], help)
	}
}

// The interface named through its module types the call as the bare name
// does.
func TestGenericInterfaceSelf_ThroughItsModule(t *testing.T) {
	files := map[string]string{"scale_a.nomi": `pub interface Scale<N>
    fn scale(value: self, by: N) -> self
end
`}
	src := `import
    scale_a
end

struct Money
    cents: Int
end

impl scale_a.Scale<Int> for Money
    fn scale(value: Money, by: Int) -> Money
        Money{cents: value.cents * by}
    end
end

fn main()
    m = Money{cents: 5}
    dbg scale_a.Scale.scale(m, 2).cents
end
`
	if errs := identityDiagTexts(identityDiags(t, src, files)); len(errs) > 0 {
		t.Fatalf("the checker rejects the call: %q", errs)
	}
}

// genericSelfFunc is the module-level fn name in nodes.
func genericSelfFunc(nodes []ast.Node, name string) *ast.FuncDef {
	for _, n := range nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == name {
			return fd
		}
	}
	return nil
}

// genericSelfScaleCall is the first call in fn of a function Scale declares.
func genericSelfScaleCall(fn *ast.FuncDef) *ast.Call {
	var found *ast.Call
	names := map[string]bool{"scale": true, "grown": true, "paired": true, "same": true, "many": true, "maker": true, "amount": true, "twice": true}
	ast.Inspect(fn, func(n ast.Node) bool {
		if c, ok := n.(*ast.Call); ok && found == nil {
			if fa, ok := c.Func.(*ast.FieldAccess); ok && fa.Field != nil && names[fa.Field.Name] {
				found = c
			}
		}
		return found == nil
	})
	return found
}

// tailExpr is the expression a body ends with.
func tailExpr(b *ast.Block) ast.Node {
	last := b.Stmts[len(b.Stmts)-1]
	if es, ok := last.(*ast.ExprStmt); ok {
		return es.Expr
	}
	return last
}

func typeText(t analysis.Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}
