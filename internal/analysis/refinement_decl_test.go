package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Refinements, validated types and `type`s over refinements (spec §4 *Refinements*,
// *Validated types*): what the checker accepts and what it rejects.

const refinementDeclSource = `fn is_email(s: String) -> Bool
    String.contains(s, "@")
end

type Email String when is_email

type UserId PositiveInt

refine Digit Int when fn n -> n >= 0 and n <= 9

refine Strict NonNegativeInt and NonZeroInt

type Plain Int

`

// checkRefinementDecls checks refinementDeclSource and body as checkRefinements does.
func checkRefinementDecls(t *testing.T, body string) []analysis.TypeError {
	t.Helper()
	src := withStdlibTestImports(refinementDeclSource + body)
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	nodes, _ = analysis.LowerDerives(nodes)
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	all := append([]analysis.TypeError(nil), fa.TypeErrors...)
	all = append(all, analysis.BuildTypes(fa, nodes)...)
	all = append(all, analysis.CheckTypes(fa, nodes)...)
	return withoutUnusedBindingErrors(all)
}

func TestRefinementDecl_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a validated literal", "fn f() -> Email\n    Email(\"a@b.c\")\nend\n"},
		{"a validated try_from", "fn f(s: String) -> Option<Email>\n    Email.try_from(s)\nend\n"},
		{"a validated type destructured", "fn f(e: Email) -> String\n    Email(s) = e\n    s\nend\n"},
		{"a type over a refinement from a literal", "fn f() -> UserId\n    UserId(5)\nend\n"},
		{"a type over a refinement from its refinement", "fn f(p: PositiveInt) -> UserId\n    UserId(p)\nend\n"},
		{"a type over a refinement's try_from", "fn f(n: Int) -> Option<UserId>\n    UserId.try_from(n)\nend\n"},
		{"a refinement into the intersection its parents make", "fn f(p: PositiveInt) -> Strict\n    p\nend\n"},
		{"a ruleless refinement up to each parent", "fn f(s: Strict) -> (NonNegativeInt, NonZeroInt, Int)\n    (s, s, s)\nend\n"},
		{"a ruleless refinement's try_from", "fn f(n: Int) -> Option<Strict>\n    Strict.try_from(n)\nend\n"},
		{"a plain type keeps its constructor", "fn f(n: Int) -> Plain\n    Plain(n)\nend\n"},
		{"an intersection parameter takes a literal", "fn half(n: NonNegativeInt and NonZeroInt) -> Int\n    n / 2\nend\n\nfn f() -> Int\n    half(4)\nend\n"},
		{"an intersection parameter takes a refinement below each member", "fn half(n: NonNegativeInt and NonZeroInt) -> Int\n    n / 2\nend\n\nfn f(p: PositiveInt) -> Int\n    half(p)\nend\n"},
		{"an intersection converts to each member and the base", "fn f(n: NonNegativeInt and NonZeroInt) -> (NonNegativeInt, NonZeroInt, Int)\n    (n, n, n)\nend\n"},
		{"an intersection in a field and a list", "struct Box\n    n: NonNegativeInt and NonZeroInt\nend\n\nfn f() -> (Box, List<NonNegativeInt and NonZeroInt>)\n    (Box{n: 3}, [1, 2])\nend\n"},
		{"an intersection in either order is one type", "fn f(n: NonNegativeInt and NonZeroInt) -> NonZeroInt and NonNegativeInt\n    n\nend\n"},
		{"a refinement of an intersection is the declared intersection", "fn f(s: Strict) -> NonNegativeInt and NonZeroInt\n    s\nend\n"},
		{"an intersection with an interface its base implements", "fn f(n: NonNegativeInt and Display) -> String\n    \"${n}\"\nend\n"},
		{"a type over an intersection", "type Both NonNegativeInt and NonZeroInt\n\nfn f(n: Int) -> Option<Both>\n    Both.try_from(n)\nend\n"},
		{"siblings join at the base", "fn f(c: Bool, a: NonNegativeInt, b: NonZeroInt) -> Int\n    if c then a else b\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := checkRefinementDecls(t, tc.src); len(errs) > 0 {
				t.Fatalf("errors: %v", errs)
			}
		})
	}
}

func TestRefinementDecl_Rejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a validated type converts to nothing", "fn f(e: Email) -> String\n    e\nend\n",
			"return type mismatch: expected String, got Email"},
		{"nothing converts into a validated type", "fn f(s: String) -> Email\n    s\nend\n",
			"return type mismatch: expected Email, got String\nhelp: Email is a new type, which nothing converts to; build one with `Email(...)` from a literal, or check a value with `Email.try_from(...)`"},
		{"a validated type's call form on a computed value", "fn f(s: String) -> Email\n    Email(s)\nend\n",
			"Email(...) takes a literal: Email is a validated type, and a String computed when the program runs may not satisfy its rule `is_email`\nhelp: check the value with `Email.try_from(s)`, an `Option<Email>`"},
		{"a type over a refinement from a computed Int", "fn f(n: Int) -> UserId\n    UserId(n)\nend\n",
			"UserId(...) takes a literal or a PositiveInt: UserId wraps the refinement PositiveInt, and an Int computed when the program runs may not be one\nhelp: check the value with `UserId.try_from(n)`, an `Option<UserId>`"},
		{"a type over a refinement converts to nothing", "fn f(u: UserId) -> PositiveInt\n    u\nend\n",
			"return type mismatch: expected PositiveInt, got UserId"},
		{"a plain type has no try_from", "fn f(n: Int) -> Option<Plain>\n    Plain.try_from(n)\nend\n",
			"type 'Plain' has no member 'try_from'"},
		{"a validated type as a function value", "fn f(xs: List<String>) -> List<Email>\n    Iter.map(xs, Email) |> Iter.to_list()\nend\n",
			"`Email` is a validated type and has no constructor function"},
		{"a member does not convert into an intersection", "fn half(n: NonNegativeInt and NonZeroInt) -> Int\n    n / 2\nend\n\nfn f(n: NonNegativeInt) -> Int\n    half(n)\nend\n",
			"argument 1: expected NonNegativeInt and NonZeroInt, got NonNegativeInt"},
		{"an intersection of refinements of different bases", "fn f(n: NonNegativeInt and NonEmptyString) -> Int\n    0\nend\n",
			"`and` intersects refinements of one type, and NonEmptyString is a String where NonNegativeInt is an Int"},
		{"an intersection with an interface its base lacks", "interface Named\n    fn name(x: self) -> String\nend\n\nfn f(n: NonNegativeInt and Named) -> Int\n    n\nend\n",
			"no value is both NonNegativeInt and Named: NonNegativeInt is an Int, and Int does not implement Named"},
		{"an intersection with a plain type", "fn f(n: NonNegativeInt and Int) -> Int\n    n\nend\n",
			"`and` joins interfaces or refinements, and Int is neither"},
		{"a join of siblings is not their intersection", "fn f(c: Bool, a: NonNegativeInt, b: NonZeroInt) -> NonNegativeInt\n    if c then a else b\nend\n",
			"return type mismatch: expected NonNegativeInt, got Int"},
		{"a parent does not convert into a ruleless refinement", "fn f(n: NonNegativeInt) -> Strict\n    n\nend\n",
			"return type mismatch: expected Strict, got NonNegativeInt"},
		{"a refinement with no rule anywhere", "refine Celsius Float\n",
			"a refinement with no rule has the same values as Float: `Celsius` adds no `when`, and nothing it narrows has one\nhelp: name it with `typealias Celsius Float`, declare a new type with `type Celsius Float`, or give the refinement a rule with `when`"},
		{"a refinement of interfaces with no rule", "interface Priced\n    fn price(p: self) -> Int\nend\n\ninterface Costed\n    fn cost(p: self) -> Int\nend\n\nrefine Item Priced and Costed\n",
			"a refinement with no rule has the same values as Priced and Costed: `Item` adds no `when`, and nothing it narrows has one\nhelp: name it with `typealias Item Priced and Costed`, declare a new type with `type Item Priced and Costed`, or give the refinement a rule with `when`"},
		{"a refinement of one parent with no rule of its own", "refine Small Digit\n",
			"a refinement of one parent with no rule of its own has the same values as Digit: `Small` adds no `when`\nhelp: name it with `typealias Small Digit`, declare a new type with `type Small Digit`, or give the refinement a rule with `when`"},
		{"a bounded generic refinement with no rule", "refine Sortable<T> NonEmptyList<T> where T: Comparable\n",
			"a refinement of one parent with no rule of its own has the same values as NonEmptyList<T>: `Sortable` adds no `when`\nhelp: name it with `typealias Sortable<T> NonEmptyList<T> where T: Comparable`"},
		{"a bounded generic refinement of a plain type", "refine Sortable<T> List<T> where T: Comparable\n",
			"a refinement with no rule has the same values as List<T>: `Sortable` adds no `when`, and nothing it narrows has one\nhelp: name it with `typealias Sortable<T> List<T> where T: Comparable`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkRefinementDecls(t, tc.src)
			var msgs []string
			for _, e := range errs {
				if strings.HasPrefix(diagText(e), tc.want) {
					return
				}
				msgs = append(msgs, diagText(e))
			}
			if len(errs) == 0 {
				t.Fatalf("the checker accepts this; want %q", tc.want)
			}
			t.Fatalf("errors = %q, want one reading %q", msgs, tc.want)
		})
	}
}
