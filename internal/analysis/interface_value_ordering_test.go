package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// An ordering is a use of Comparable.compare, whose two `self` arguments no
// interface value can answer (spec §12 *Interface values*), so `<`, `<=`,
// `>` and `>=` over an operand held as `Comparable`, or as a conjunction
// holding it, are the error the call `Comparable.compare(a, b)` is, from
// the one rule (checkExistentialSelfUse). Each case fails if the source
// starts being admitted.
func TestInterfaceValueOrdering_IsRejected(t *testing.T) {
	type tc struct {
		name, params, expr, subject, held, hint string
	}
	var cases []tc
	for _, op := range []string{"<", "<=", ">", ">="} {
		cases = append(cases,
			tc{"two Comparable values " + op, "a: Comparable, b: Comparable", "a " + op + " b", "a", "Comparable",
				"take a type parameter bounded by Comparable instead, so the implementor is known: `fn f<T>(a: T, b: T) -> Bool where T: Comparable`"},
			tc{"a Comparable value and an Int " + op, "a: Comparable", "a " + op + " 1", "a", "Comparable",
				"take a type parameter bounded by Comparable instead, so the implementor is known: `fn f<T>(a: T) -> Bool where T: Comparable`"},
			tc{"an Int and a Comparable value " + op, "a: Comparable", "1 " + op + " a", "a", "Comparable",
				"take a type parameter bounded by Comparable instead, so the implementor is known: `fn f<T>(a: T) -> Bool where T: Comparable`"},
			tc{"two Display and Comparable values " + op, "a: Display and Comparable, b: Display and Comparable", "a " + op + " b", "a", "Comparable and Display",
				"take a type parameter bounded by Comparable and Display instead, so the implementor is known: `fn f<T>(a: T, b: T) -> Bool where T: Comparable and Display`"},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "fn f(" + c.params + ") -> Bool\n    " + c.expr + "\nend\n"
			op := strings.Fields(c.expr)[1]
			want := "`" + op + "` cannot compare `" + c.subject + "`, a `" + c.held + "`: its two operands must be one implementing type, and `" + c.held + "` values may hold different ones"
			errs := checkInterfaceValueOrdering(t, src)
			if len(errs) == 0 {
				t.Fatalf("the checker admits %q; it must reject it with %q", src, want)
			}
			var hit *analysis.TypeError
			for i, e := range errs {
				if e.Message == want {
					hit = &errs[i]
				}
			}
			if hit == nil {
				t.Fatalf("want %q; got:\n  %s", want, joinMessages(errs))
			}
			if hit.Code != analysis.InterfaceValueSelfCode {
				t.Errorf("code = %q, want %q", hit.Code, analysis.InterfaceValueSelfCode)
			}
			if strings.Join(hit.Hints, "|") != c.hint {
				t.Errorf("hints = %q, want %q", hit.Hints, c.hint)
			}
			if len(errs) != 1 {
				t.Errorf("want the one error; got:\n  %s", joinMessages(errs))
			}
		})
	}
}

// The call form over a conjunction holding Comparable is the same rule's
// error, and the generic form is accepted.
func TestInterfaceValueOrdering_CallFormAndGenericForm(t *testing.T) {
	src := "fn f(a: Display and Comparable, b: Display and Comparable) -> Ordering\n    Comparable.compare(a, b)\nend\n"
	want := "`Comparable.compare` cannot be called on `a`, a `Comparable and Display`: its 2 `self` arguments must be one implementing type, and `Comparable and Display` values may hold different ones"
	errs := checkInterfaceValueOrdering(t, src)
	if len(errs) != 1 || errs[0].Message != want || errs[0].Code != analysis.InterfaceValueSelfCode {
		t.Errorf("want only %q [%s]; got:\n  %s", want, analysis.InterfaceValueSelfCode, joinMessages(errs))
	}
	generic := "fn less<T>(a: T, b: T) -> Bool where T: Comparable\n    a < b\nend\n\nfn equal(a: Equatable, b: Equatable) -> Bool\n    a == b\nend\n"
	if errs := checkInterfaceValueOrdering(t, generic); len(errs) != 0 {
		t.Errorf("the checker rejects the generic ordering or `==` over interface values:\n  %s", joinMessages(errs))
	}
}

// checkInterfaceValueOrdering is the errors of checking the one-file
// program src over the stdlib, less the unread-parameter warnings.
func checkInterfaceValueOrdering(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	l := loadStd()
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	fa, _, _ := analysis.BuildProjectWithCache(nodes, l.Primitives, l.Modules, l.Files, t.TempDir(), nil)
	return withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...))
}
