package vmhost

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// appDecls declares an application type whose fields the tests below read
// where the compiler runs code: there is no app there, since one exists
// only while the program runs. The literal tests import std::literals
// after it.
const appDecls = "import std::io\n\n" + appType

const appType = "struct App\n    limit: Int\n    label: String\nend\n\n" +
	"fn boot() -> Result<App, String>\n    Ok(App{limit: 3, label: \"lim\"})\nend\n\n"

// A rule that reads an app field, its own body or a function it calls, is
// an error at its declaration that names the field and says why, and is no
// effect; a refinement's and a validated struct's alike. A rule that reads
// none loads.
func TestRefinement_ARuleThatReadsAnAppFieldIsAnError(t *testing.T) {
	for _, tc := range []struct{ name, decls, body, want string }{
		{"a refinement's rule, through a function",
			"fn below_limit(n: Int) -> Bool\n    n < App.limit\nend\n\n" +
				"refine Bounded Int when fn n -> below_limit(n)\n\n",
			"    b: Bounded = 2\n    io.inspect(b)\n",
			"the rule of Bounded reads `App.limit`: a rule is checked with the value alone, and an app field exists only while the program runs"},
		{"a validated struct's rule, two fields",
			"struct Tagged\n    tag: String\n    n: Int\n" +
				"    when fn t -> t.tag != App.label and t.n < App.limit\nend\n\n",
			"    io.inspect(Tagged.try_from({tag: \"a\", n: 1}))\n",
			"the rule of Tagged reads `App.label`, `App.limit`: a rule is checked with the value alone, and an app field exists only while the program runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := literalDiagnostics(t, appDecls+tc.decls+"fn main()\n"+tc.body+"end\n")
			if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
				t.Fatalf("diagnostics = %v, want one reading %q", ds, tc.want)
			}
			if strings.Contains(ds[0].Message, "effect") {
				t.Errorf("the message calls the read an effect: %q", ds[0].Message)
			}
		})
	}
	pure := appDecls + "refine Small Int when fn n -> n < 10\n\nfn main()\n    s: Small = 2\n    io.inspect((s, App.limit))\nend\n"
	if _, err := LoadSource("main", pure); err != nil {
		t.Fatalf("a rule that reads no app field does not load beside code that does: %v", err)
	}
}

// A typed literal's handler that reads an app field cannot run when the
// program is checked: the literal is an error naming the field.
func TestLiteralWithoutSlots_AHandlerThatReadsAnAppFieldIsAnError(t *testing.T) {
	const decls = "import\n    std::io\n    std::literals.{Fragment, Literal}\nend\n\n" + appType +
		"struct Capped\n    n: Int\nend\n\n" +
		"impl Literal for Capped\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Capped, String>\n" +
		"        Ok(Capped{n: Iter.count(fragments) + App.limit})\n" +
		"    end\n" +
		"end\n\n"
	ds := literalDiagnostics(t, decls+"fn main()\n    io.inspect(Capped`x`)\nend\n")
	want := "typed literal Capped`x` cannot be checked at compile time: its handler reads `App.limit`: a literal is built when the program is checked, and an app field exists only while the program runs"
	if len(ds) != 1 || ds[0].Message != want || ds[0].Code != InvalidLiteralCode {
		t.Fatalf("diagnostics = %v, want one reading %q", ds, want)
	}
}

// A checked literal's check that reads an app field is one error at the
// check, naming the field.
func TestCheckedLiteral_ACheckThatReadsAnAppFieldIsAnError(t *testing.T) {
	const decls = "import\n    std::io\n    std::literals.{CheckedLiteral, Fragment, LiteralError}\nend\n\n" + appType +
		"type Short\n\n" +
		"impl CheckedLiteral<String, String> for Short\n" +
		"    fn check(statics: List<String>) -> Result<Unit, LiteralError>\n" +
		"        if Iter.count(statics) > App.limit then\n" +
		"            Err(LiteralError{message: \"too long\", fragment: 0, offset: 0})\n" +
		"        else\n" +
		"            Ok(Unit)\n" +
		"        end\n" +
		"    end\n\n" +
		"    fn build(fragments: List<Fragment<String>>) -> String\n" +
		"        \"${Iter.count(fragments)}\"\n" +
		"    end\n" +
		"end\n\n"
	src := decls + "fn main()\n    io.print(Short\"a\")\n    io.print(Short\"b\")\nend\n"
	ds := literalDiagnostics(t, src)
	want := "Short.check reads `App.limit`: a literal is built when the program is checked, and an app field exists only while the program runs"
	if len(ds) != 1 || ds[0].Message != want || ds[0].Code != InvalidLiteralCode {
		t.Fatalf("diagnostics = %v, want one reading %q", ds, want)
	}
	if ds[0].Line != 18 {
		t.Errorf("at line %d, want the check's line 18", ds[0].Line)
	}
}

// An editor's evaluation runs with no app, so a function that reads an
// app field, or replaces one with `with`, is refused with *ReadsApp naming
// the fields, and not as an effect.
func TestEvaluate_RefusesAFunctionThatReadsAnAppField(t *testing.T) {
	src := appDecls +
		"fn limit_plus_one() -> Int\n    App.limit + 1\nend\n\n" +
		"fn relabeled() -> String\n    with App.label = \"other\"\n    shown()\nend\n\n" +
		"fn shown() -> String\n    \"${App.label}\"\nend\n\n" +
		"fn main()\n    io.print(\"${limit_plus_one()} ${relabeled()}\")\nend\n"
	p, err := LoadSource("evaluate", src)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"limit_plus_one": "the function reads `App.limit`, and an app field exists only while the program runs",
		"relabeled":      "the function reads `App.label`, and an app field exists only while the program runs",
	} {
		_, err := p.Evaluate(context.Background(), name, EvalLimits{Steps: 10_000})
		var reads *ReadsApp
		if !errors.As(err, &reads) {
			t.Fatalf("%s answered %v, want *ReadsApp", name, err)
		}
		if err.Error() != want {
			t.Errorf("%s: %q, want %q", name, err.Error(), want)
		}
	}
}
