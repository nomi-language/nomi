package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Subtypes, validated types and `type`s over subtypes (spec §4 *Subtypes*,
// *Validated types*): what the checker accepts and what it rejects.

const subtypeDecls = `fn is_email(s: String) -> Bool
    String.contains(s, "@")
end

type Email String when is_email

type UserId PositiveInt

subtype Digit Int when fn n -> n >= 0 and n <= 9

subtype Strict NonNegativeInt and NonZeroInt

type Plain Int

`

// checkSubtypes checks subtypeDecls and body as checkRefinements does.
func checkSubtypes(t *testing.T, body string) []analysis.TypeError {
	t.Helper()
	src := withStdlibTestImports(subtypeDecls + body)
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

func TestSubtype_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a validated literal", "fn f() -> Email\n    Email(\"a@b.c\")\nend\n"},
		{"a validated try_from", "fn f(s: String) -> Maybe<Email>\n    Email.try_from(s)\nend\n"},
		{"a validated type destructured", "fn f(e: Email) -> String\n    Email(s) = e\n    s\nend\n"},
		{"a type over a subtype from a literal", "fn f() -> UserId\n    UserId(5)\nend\n"},
		{"a type over a subtype from its subtype", "fn f(p: PositiveInt) -> UserId\n    UserId(p)\nend\n"},
		{"a type over a subtype's try_from", "fn f(n: Int) -> Maybe<UserId>\n    UserId.try_from(n)\nend\n"},
		{"a subtype into the intersection its parents make", "fn f(p: PositiveInt) -> Strict\n    p\nend\n"},
		{"a ruleless subtype up to each parent", "fn f(s: Strict) -> (NonNegativeInt, NonZeroInt, Int)\n    (s, s, s)\nend\n"},
		{"a ruleless subtype's try_from", "fn f(n: Int) -> Maybe<Strict>\n    Strict.try_from(n)\nend\n"},
		{"a plain type keeps its constructor", "fn f(n: Int) -> Plain\n    Plain(n)\nend\n"},
		{"an intersection parameter takes a literal", "fn half(n: NonNegativeInt and NonZeroInt) -> Int\n    n / 2\nend\n\nfn f() -> Int\n    half(4)\nend\n"},
		{"an intersection parameter takes a subtype below each member", "fn half(n: NonNegativeInt and NonZeroInt) -> Int\n    n / 2\nend\n\nfn f(p: PositiveInt) -> Int\n    half(p)\nend\n"},
		{"an intersection converts to each member and the base", "fn f(n: NonNegativeInt and NonZeroInt) -> (NonNegativeInt, NonZeroInt, Int)\n    (n, n, n)\nend\n"},
		{"an intersection in a field and a list", "struct Box\n    n: NonNegativeInt and NonZeroInt\nend\n\nfn f() -> (Box, List<NonNegativeInt and NonZeroInt>)\n    (Box{n: 3}, [1, 2])\nend\n"},
		{"an intersection in either order is one type", "fn f(n: NonNegativeInt and NonZeroInt) -> NonZeroInt and NonNegativeInt\n    n\nend\n"},
		{"a subtype of an intersection is the declared intersection", "fn f(s: Strict) -> NonNegativeInt and NonZeroInt\n    s\nend\n"},
		{"an intersection with an interface its base implements", "fn f(n: NonNegativeInt and Display) -> String\n    \"${n}\"\nend\n"},
		{"a type over an intersection", "type Both NonNegativeInt and NonZeroInt\n\nfn f(n: Int) -> Maybe<Both>\n    Both.try_from(n)\nend\n"},
		{"siblings join at the base", "fn f(c: Bool, a: NonNegativeInt, b: NonZeroInt) -> Int\n    if c then a else b\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := checkSubtypes(t, tc.src); len(errs) > 0 {
				t.Fatalf("errors: %v", errs)
			}
		})
	}
}

func TestSubtype_Rejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a validated type converts to nothing", "fn f(e: Email) -> String\n    e\nend\n",
			"return type mismatch: expected String, got Email"},
		{"nothing converts into a validated type", "fn f(s: String) -> Email\n    s\nend\n",
			"return type mismatch: expected Email, got String\nhelp: Email is a new type, which nothing converts to; build one with `Email(...)` from a literal, or check a value with `Email.try_from(...)`"},
		{"a validated type's call form on a computed value", "fn f(s: String) -> Email\n    Email(s)\nend\n",
			"Email(...) takes a literal: Email is a validated type, and a String computed when the program runs may not satisfy its rule `is_email`\nhelp: check the value with `Email.try_from(s)`, a `Maybe<Email>`"},
		{"a type over a subtype from a computed Int", "fn f(n: Int) -> UserId\n    UserId(n)\nend\n",
			"UserId(...) takes a literal or a PositiveInt: UserId wraps the subtype PositiveInt, and an Int computed when the program runs may not be one\nhelp: check the value with `UserId.try_from(n)`, a `Maybe<UserId>`"},
		{"a type over a subtype converts to nothing", "fn f(u: UserId) -> PositiveInt\n    u\nend\n",
			"return type mismatch: expected PositiveInt, got UserId"},
		{"a plain type has no try_from", "fn f(n: Int) -> Maybe<Plain>\n    Plain.try_from(n)\nend\n",
			"type 'Plain' has no member 'try_from'"},
		{"a validated type as a function value", "fn f(xs: List<String>) -> List<Email>\n    Iter.map(xs, Email) |> Iter.to_list()\nend\n",
			"`Email` is a validated type and has no constructor function"},
		{"a member does not convert into an intersection", "fn half(n: NonNegativeInt and NonZeroInt) -> Int\n    n / 2\nend\n\nfn f(n: NonNegativeInt) -> Int\n    half(n)\nend\n",
			"argument 1: expected NonNegativeInt and NonZeroInt, got NonNegativeInt"},
		{"an intersection of subtypes of different bases", "fn f(n: NonNegativeInt and NonEmptyString) -> Int\n    0\nend\n",
			"`and` intersects subtypes of one type, and NonEmptyString is a String where NonNegativeInt is an Int"},
		{"an intersection with an interface its base lacks", "interface Named\n    fn name(x: self) -> String\nend\n\nfn f(n: NonNegativeInt and Named) -> Int\n    n\nend\n",
			"no value is both NonNegativeInt and Named: NonNegativeInt is an Int, and Int does not implement Named"},
		{"an intersection with a plain type", "fn f(n: NonNegativeInt and Int) -> Int\n    n\nend\n",
			"`and` joins interfaces or subtypes, and Int is neither"},
		{"a join of siblings is not their intersection", "fn f(c: Bool, a: NonNegativeInt, b: NonZeroInt) -> NonNegativeInt\n    if c then a else b\nend\n",
			"return type mismatch: expected NonNegativeInt, got Int"},
		{"a parent does not convert into a ruleless subtype", "fn f(n: NonNegativeInt) -> Strict\n    n\nend\n",
			"return type mismatch: expected Strict, got NonNegativeInt"},
		{"a subtype with no rule anywhere", "subtype Celsius Float\n",
			"a subtype with no rule has the same values as Float: `Celsius` adds no `when`, and nothing it narrows has one\nhelp: name it with `typealias Celsius Float`, declare a new type with `type Celsius Float`, or give the subtype a rule with `when`"},
		{"a subtype of interfaces with no rule", "interface Priced\n    fn price(p: self) -> Int\nend\n\ninterface Costed\n    fn cost(p: self) -> Int\nend\n\nsubtype Item Priced and Costed\n",
			"a subtype with no rule has the same values as Priced and Costed: `Item` adds no `when`, and nothing it narrows has one\nhelp: name it with `typealias Item Priced and Costed`, declare a new type with `type Item Priced and Costed`, or give the subtype a rule with `when`"},
		{"a subtype of one parent with no rule of its own", "subtype Small Digit\n",
			"a subtype of one parent with no rule of its own has the same values as Digit: `Small` adds no `when`\nhelp: name it with `typealias Small Digit`, declare a new type with `type Small Digit`, or give the subtype a rule with `when`"},
		{"a bounded generic subtype with no rule", "subtype Sortable<T> NonEmptyList<T> where T: Comparable\n",
			"a subtype of one parent with no rule of its own has the same values as NonEmptyList<T>: `Sortable` adds no `when`\nhelp: name it with `typealias Sortable<T> NonEmptyList<T> where T: Comparable`"},
		{"a bounded generic subtype of a plain type", "subtype Sortable<T> List<T> where T: Comparable\n",
			"a subtype with no rule has the same values as List<T>: `Sortable` adds no `when`, and nothing it narrows has one\nhelp: name it with `typealias Sortable<T> List<T> where T: Comparable`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkSubtypes(t, tc.src)
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
