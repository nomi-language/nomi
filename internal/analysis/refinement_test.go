package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Refinement types (spec §4 *Refinement types*): what the checker accepts
// and what it rejects, with its messages.

const refinementDecls = `type NonNegative Float when fn x -> x >= 0.0
type Positive NonNegative when fn x -> x > 0.0
type Port Int when fn port -> port >= 1 and port <= 65535
type Even Int when fn n -> n % 2 == 0
type Odd Int when fn n -> n % 2 == 1

fn is_name(s: String) -> Bool
    String.length(s) > 1
end

type Name String when is_name

struct Span
    low:  Int
    high: Int
end when fn span -> span.low <= span.high

type Pair List<Int> when fn xs -> List.length(xs) == 2
type Weights Map<String, Float> when fn m -> Map.size(m) > 0

fn half(x: Float) -> Float
    x / 2.0
end

fn serve(port: Port) -> Int
    port
end

`

// checkRefinements checks refinementDecls and body as checkSourceWithStdlib
// does, with what the front end writes for a file's declarations: the
// refinements' `try_from` and `unchecked` among them.
func checkRefinements(t *testing.T, body string) []analysis.TypeError {
	t.Helper()
	src := withStdlibTestImports(refinementDecls + body)
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	nodes, _ = analysis.LowerDerives(nodes)
	nodes, _ = analysis.SynthesizeDerives(nodes)
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	all := append([]analysis.TypeError(nil), fa.TypeErrors...)
	all = append(all, analysis.BuildTypes(fa, nodes)...)
	all = append(all, analysis.CheckTypes(fa, nodes)...)
	return withoutUnusedBindingErrors(all)
}

func TestRefinement_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a literal where the refinement is expected", "fn main()\n    p: Port = 8080\n    _ = p\nend\n"},
		{"the call form on a literal", "fn main()\n    _ = Port(8080)\nend\n"},
		{"a negated literal", "fn main()\n    n: NonNegative = -0.0\n    _ = n\nend\n"},
		{"a literal argument", "fn main()\n    _ = serve(80)\nend\n"},
		{"a literal field of a list", "fn main()\n    xs: List<Port> = [80, 443]\n    _ = xs\nend\n"},
		{"a literal inside a generic call", "fn main()\n    m: Maybe<Port> = Some(22)\n    _ = m\nend\n"},
		{"a literal return", "fn default_port() -> Port\n    443\nend\n"},
		{"a named rule", "fn main()\n    n: Name = \"ada\"\n    _ = n\nend\n"},
		{"up a chain to its base", "fn f(p: Positive) -> Float\n    half(p)\nend\n"},
		{"up a chain to an ancestor", "fn f(p: Positive) -> NonNegative\n    p\nend\n"},
		{"inside a container", "fn f(ps: List<Positive>) -> List<Float>\n    ps\nend\n"},
		{"into an interface as the base", "fn f(p: Port) -> String\n    d: Display = p\n    \"${d}\"\nend\n"},
		{"arithmetic is the base's", "fn f(p: Positive) -> Float\n    p + 1.0\nend\n"},
		{"a comparison with a literal", "fn f(p: Port) -> Bool\n    p == 80 or p < 1024\nend\n"},
		{"a literal pattern", "fn f(p: Port) -> String\n    case p\n        80 -> \"http\"\n        _ -> \"other\"\n    end\nend\n"},
		{"try_from", "fn f(n: Int) -> Maybe<Port>\n    Port.try_from(n)\nend\n"},
		{"try_from up a chain", "fn f(x: Float) -> Maybe<Positive>\n    Positive.try_from(x)\nend\n"},
		{"unchecked in the declaring file", "fn f(n: Int) -> Port\n    Port.unchecked(n)\nend\n"},
		{"destructuring reads the parent", "fn f(p: Positive) -> NonNegative\n    Positive(x) = p\n    x\nend\n"},
		{"an inherent function", "impl Port\n    fn is_privileged(port: Port) -> Bool\n        port < 1024\n    end\nend\n"},
		{"a struct literal of literals", "fn main()\n    _ = Span{low: 1, high: 2}\nend\n"},
		{"a struct try_from", "fn f(a: Int, b: Int) -> Maybe<Span>\n    Span.try_from({low: a, high: b})\nend\n"},
		{"a join at the written type", "fn f(c: Bool, p: Port) -> Int\n    if c then p else 5\nend\n"},
		{"a refinement as a type argument", "fn f(xs: Set<Port>) -> Int\n    Set.size(xs)\nend\n"},
		{"a list literal of literals", "fn main()\n    p: Pair = [1, 2]\n    _ = p\nend\n"},
		{"a map literal of literals", "fn main()\n    w: Weights = {\"a\" => 1.0}\n    _ = w\nend\n"},
		{"collection refinement literals in a list", "fn main()\n    ps: List<Pair> = [[1, 2], [3, 4]]\n    _ = ps\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := checkRefinements(t, tc.body); len(errs) != 0 {
				var msgs []string
				for _, e := range errs {
					msgs = append(msgs, diagText(e))
				}
				t.Fatalf("the checker rejects this: %q", msgs)
			}
		})
	}
}

func TestRefinement_Rejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a computed base value", "fn f(n: Int) -> Port\n    n\nend\n",
			"return type mismatch: expected Port, got Int\nhelp: Port is a refinement of Int; check the value with `Port.try_from(...)`"},
		{"a computed argument", "fn f(n: Int) -> Int\n    serve(n)\nend\n",
			"argument 1: expected Port, got Int\nhelp: Port is a refinement of Int; check the value with `Port.try_from(...)`"},
		{"the call form on a computed value", "fn f(n: Int) -> Port\n    Port(n)\nend\n",
			"Port(...) takes a literal: Port is a refinement, and an Int computed when the program runs may not satisfy its rule\nhelp: check the value with `Port.try_from(...)`"},
		{"down a chain", "fn f(n: NonNegative) -> Positive\n    n\nend\n",
			"return type mismatch: expected Positive, got NonNegative\nhelp: Positive is a refinement of NonNegative; check the value with `Positive.try_from(...)`"},
		{"across siblings", "fn f(e: Even) -> Odd\n    e\nend\n",
			"return type mismatch: expected Odd, got Even"},
		{"a literal of another type", "fn main()\n    p: Port = \"80\"\n    _ = p\nend\n",
			"type mismatch: expected Port, got String"},
		{"the constructor as a function", "fn main()\n    _ = Iter.map([1], Port)\nend\n",
			"`Port` is a refinement and has no constructor function"},
		{"an opaque refinement", "pub opaque type Secret Int when fn n -> n > 0\n",
			"a refinement cannot be opaque"},
		{"a generic refinement", "type Many<T> List<T> when fn xs -> True\n",
			"a refinement takes no type parameters"},
		{"a rule that is no predicate", "type Wrong Int when fn n -> n + 1\n",
			"type mismatch: expected (Int) -> Bool, got (Int) -> Int"},
		{"an interface impl", "impl Display for Port\n    fn to_string(_p: Port) -> String\n        \"port\"\n    end\nend\n",
			"a refinement implements no interface of its own: `Port` is an Int at run time and has Int's impls"},
		{"a refinement in an impl header", "struct Box\n    n: Int\nend\n\nimpl Divide<Port, Box> for Box\n    fn divide(b: Box, _rhs: Port) -> Box\n        b\n    end\nend\n",
			"an impl's interface cannot take the refinement `Port` as an argument"},
		{"a generated name taken", "impl Port\n    fn try_from(n: Int) -> Maybe<Port>\n        None\n    end\nend\n",
			"`Port.try_from` is generated for the refinement `Port`; name this function otherwise"},
		{"an enum embedding one", "enum Endpoint\n    embeds Port\n    Local\nend\n",
			"an enum cannot embed the refinement `Port`"},
		{"a struct literal with a computed field", "fn f(n: Int) -> Span\n    Span{low: n, high: 4}\nend\n",
			"Span is a refinement: a literal of it is checked when the program is compiled, so each field is a literal, and `low` is not"},
		{"a struct patch", "fn f(s: Span) -> Span\n    {..s, low: 0}\nend\n",
			"a patch of the refinement Span could break its rule `fn span -> span.low <= span.high`"},
		{"the struct call form on a record value", "fn f(r: {low: Int, high: Int}) -> Span\n    Span(r)\nend\n",
			"Span(...) takes a literal: Span is a refinement, and a record computed when the program runs may not satisfy its rule"},
		{"a refinement in a body", "fn main()\n    type Local Int when fn n -> n > 0\n    _ = 1\nend\n",
			"a refinement is declared at the top of a file: move `Local` out of the body"},
		{"a list literal with a computed element", "fn f(n: Int) -> Pair\n    [1, n]\nend\n",
			"Pair is a refinement: a literal of it is checked when the program is compiled, so each of its elements is a literal, and `n` is not\nhelp: build a Pair from values known only when the program runs with `Pair.try_from(...)`"},
		{"a map literal with a computed value", "fn f(x: Float) -> Weights\n    {\"a\" => x}\nend\n",
			"Weights is a refinement: a literal of it is checked when the program is compiled, so each of its elements is a literal, and `x` is not"},
		{"a list literal with a spread", "fn f(xs: List<Int>) -> Pair\n    [..xs, 1]\nend\n",
			"Pair is a refinement: a literal of it is checked when the program is compiled, so each of its elements is a literal, and `xs` is not"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkRefinements(t, tc.src)
			for _, e := range errs {
				if strings.HasPrefix(diagText(e), tc.want) {
					return
				}
			}
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, diagText(e))
			}
			if len(errs) == 0 {
				t.Fatalf("the checker accepts this; want %q", tc.want)
			}
			t.Fatalf("errors = %q, want one reading %q", msgs, tc.want)
		})
	}
}

// `unchecked` is the declaring file's: another file that imports the
// refinement may check values with `try_from` and use the type, and may not
// build one without its rule.
func TestRefinement_UncheckedIsTheDeclaringFiles(t *testing.T) {
	_, errs := opaqueProject(t, map[string]string{
		"units.nomi": `pub type Meters Float when fn m -> m >= 0.0

pub fn zero() -> Meters
  Meters.unchecked(0.0)
end
`,
		"main.nomi": `import units.{Meters, zero}

fn main()
  ok = Meters.try_from(2.0)
  m: Meters = 3.0
  bad = Meters.unchecked(-1.0)
  _ = (ok, m, bad, zero())
end
`,
	})
	opaqueExpectErrorContains(t, errs, "`Meters.unchecked` builds a Meters without checking its rule, and only the file that declares Meters may call it")
	if len(errs) != 1 {
		t.Errorf("errors = %v, want only the unchecked one", errs)
	}
}
