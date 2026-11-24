package frontend_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// interfacePositionDecls declares Foo, which implements nothing, and
// Measure<P>, whose written Display, ToHtml and ToSql each ask only
// `P: Unit2`. Feet implements Unit2; Bare implements nothing. shown and
// gshown take a Display value, the second in a generic function.
const interfacePositionDecls = `import std::html.{Html, ToHtml}
import std::sql.{Sql, ToSql, Value}

interface Unit2
    fn symbol(unit: self) -> String
end

struct Feet
    n: Int
end

impl Unit2 for Feet
    fn symbol(_unit: Feet) -> String
        "ft"
    end
end

struct Bare
    n: Int
end

struct Foo
    n: Int
end

struct Measure<P>
    amount: Int
    unit: P
end

impl Display for Measure<P> where P: Unit2
    fn to_string(m: Measure<P>) -> String
        "${m.amount} ${Unit2.symbol(m.unit)}"
    end
end

impl ToHtml for Measure<P> where P: Unit2
    fn to_html(m: Measure<P>) -> Html
        Html"<b>${m.amount}</b>"
    end
end

impl ToSql for Measure<P> where P: Unit2
    fn to_sql(m: Measure<P>) -> Value
        Value.Int(m.amount)
    end
end

fn shown(x: Display) -> String
    Display.to_string(x)
end

fn gshown<T>(x: Display, y: T) -> String
    _ = y
    Display.to_string(x)
end

fn statement() -> String
    "${Sql"SELECT 1"}"
end

fn main()
    m = Measure{amount: 1, unit: Bare{n: 0}}
    fine = Measure{amount: 1, unit: Feet{n: 0}}
    f = Foo{n: 1}
    h = Html"<b>x</b>"
`

// interfacePositionCases is each position that takes a value of an
// interface type, under each cause of a miss: the value's type has no impl
// of the interface, a part of it has none (through an impl that asks the
// interface of its parts), or its impl's own where clause is unmet. Each
// refused entry is the message and hints of the one error the value draws.
// A position that takes a value says what it takes of the whole value for
// the first two, with the same help in a generic function as in a plain
// one; an unmet where clause names its bound, what the program wrote that
// required it (the parameter and the function, the template's or the
// literal's slot, the interpolation), and the impl whose where clause
// asked it.
var interfacePositionCases = []struct{ position, cause, expr, refused string }{
	{"plain call", "no impl", "shown(f)",
		"argument 1: expected Display, got Foo | add `derive Display` to `Foo` or write an `impl Display for Foo` block with `fn to_string(value: Foo) -> String`"},
	{"plain call", "part with no impl", "shown(Some(f))",
		"argument 1: expected Display, got Option<Foo> | `Option<Foo>` is Display when `Foo` is, and `Foo` is not: add `derive Display` to `Foo` or write an `impl Display for Foo` block with `fn to_string(value: Foo) -> String`"},
	{"plain call", "part with no impl", "shown([h])",
		"argument 1: expected Display, got List<Html> | `List<Html>` is Display when `Html` is, and `Html` is not: only std can implement `Display` for `Html`; wrap it in a type of your own that implements `Display`"},
	{"plain call", "part with no impl", "shown([[f]])",
		"argument 1: expected Display, got List<List<Foo>> | `List<List<Foo>>` is Display when `List<Foo>` is, and `List<Foo>` is not: `List<Foo>` is Display when `Foo` is, and `Foo` is not: add `derive Display` to `Foo` or write an `impl Display for Foo` block with `fn to_string(value: Foo) -> String`"},
	{"plain call", "unmet where clause", "shown(m)",
		"no impl of `Unit2` for `Bare` (required by parameter `x` of `shown`) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"plain call", "unmet where clause", "shown([m])",
		"no impl of `Unit2` for `Bare` (required by parameter `x` of `shown`) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},

	{"generic call", "no impl", "gshown(f, 1)",
		"argument 1: Foo does not implement Display | add `derive Display` to `Foo` or write an `impl Display for Foo` block with `fn to_string(value: Foo) -> String`"},
	{"generic call", "part with no impl", "gshown(Some(f), 1)",
		"argument 1: Option<Foo> does not implement Display | `Option<Foo>` is Display when `Foo` is, and `Foo` is not: add `derive Display` to `Foo` or write an `impl Display for Foo` block with `fn to_string(value: Foo) -> String`"},
	{"generic call", "unmet where clause", "gshown([m], 1)",
		"no impl of `Unit2` for `Bare` (required by parameter `x` of `gshown`) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},

	{"Html content slot", "no impl", `Html"<p>${f}</p>"`,
		"this slot takes a `ToHtml` or a `Display`, and a `Foo` is neither | an element's content takes a value that renders itself (`ToHtml`: a `String`, a number, an `Html`, a `List<Html>`, an `Option` of one), or any other `Display` value, escaped as text"},
	{"Html content slot", "part with no impl", `Html"<p>${Some(True)}</p>"`,
		"element content does not show an `Option<Bool>` | an `Option` or a `Result` would show as `Some(...)` or `Ok(...)`: unwrap it; an `Option` of a `ToHtml` value shows its value, and `None` nothing"},
	{"Html content slot", "part with no impl", `Html"<p>${Some(f)}</p>"`,
		"element content does not show an `Option<Foo>` | an `Option` or a `Result` would show as `Some(...)` or `Ok(...)`: unwrap it; an `Option` of a `ToHtml` value shows its value, and `None` nothing"},
	{"Html content slot", "unmet where clause", `Html"<p>${m}</p>"`,
		"no impl of `Unit2` for `Bare` (required by this `Html` slot) | `Measure<P>`'s `ToHtml` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},

	{"Html attribute slot", "no impl", `Html"<p class='${h}'></p>"`,
		"this slot takes a `Display`, and an `Html` is not one | an attribute's value is text: pass a `String` or another `Display` value, or an `Option` of one, which writes nothing for `None`"},
	{"Html attribute slot", "part with no impl", `Html"<p class='${Some(h)}'></p>"`,
		"this slot takes a `Display`, and an `Option<Html>` is not one | an attribute's value is text: pass a `String` or another `Display` value, or an `Option` of one, which writes nothing for `None`"},
	{"Html attribute slot", "part with no impl", `Html"<p class='${[h]}'></p>"`,
		"this slot takes a `Display`, and a `List<Html>` is not one | an attribute's value is text: pass a `String` or another `Display` value, or an `Option` of one, which writes nothing for `None`"},
	{"Html attribute slot", "unmet where clause", `Html"<p class='${m}'></p>"`,
		"no impl of `Unit2` for `Bare` (required by this `Html` slot) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},

	{"interpolation", "no impl", `"${f}"`,
		"no impl of `Display` for `Foo` (required to interpolate it) | add `derive Display` to `Foo` or write an `impl Display for Foo` block"},
	{"interpolation", "part with no impl", `"${[f]}"`,
		"no impl of `Display` for `Foo` (required to interpolate it) | add `derive Display` to `Foo` or write an `impl Display for Foo` block"},
	{"interpolation", "unmet where clause", `"${m}"`,
		"no impl of `Unit2` for `Bare` (required to interpolate it) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"interpolation", "unmet where clause", `"${[m]}"`,
		"no impl of `Unit2` for `Bare` (required to interpolate it) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},

	{"Sql slot", "no impl", `Sql"SELECT ${f}"`,
		"slot expression of type 'Foo' does not implement 'ToSql' (required by tag 'Sql')"},
	{"Sql slot", "part with no impl", `Sql"SELECT ${Some(f)}"`,
		"slot expression of type 'Option<Foo>' does not implement 'ToSql' (required by tag 'Sql')"},
	{"Sql slot", "unmet where clause", `Sql"SELECT ${m}"`,
		"no impl of `Unit2` for `Bare` (required by this `Sql` slot) | `Measure<P>`'s `ToSql` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Sql slot", "unmet where clause", `Sql"SELECT ${[m]}"`,
		"no impl of `Unit2` for `Bare` (required by this `Sql` slot) | `Measure<P>`'s `ToSql` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
}

// A log field's value is an argument of std/log's `any`, called as the
// program writes it: an unmet where clause names that parameter, and the
// impl whose where clause asked it.
func TestInterfacePosition_ALogFieldNamesTheParameterItFills(t *testing.T) {
	src := `import std::log

interface Unit2
    fn symbol(unit: self) -> String
end

struct Bare
    n: Int
end

struct Measure<P>
    amount: Int
    unit: P
end

impl Display for Measure<P> where P: Unit2
    fn to_string(m: Measure<P>) -> String
        "${m.amount} ${Unit2.symbol(m.unit)}"
    end
end

fn main()
    m = Measure{amount: 1, unit: Bare{n: 0}}
    _ = log.any("size", m)
end
`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("the program is admitted; want the unmet where clause")
	}
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("not diagnostics: %v", err)
	}
	var got []string
	for _, d := range ds {
		if !d.Warning && !d.Hint {
			got = append(got, strings.Join(append([]string{d.Message}, d.Hints...), " | "))
		}
	}
	want := []string{"no impl of `Unit2` for `Bare` (required by parameter `value` of `log.any`) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// interfacePositionErrors checks expr as the last statement of
// interfacePositionDecls' main and answers the errors on its line, each as
// its message and hints joined by " | ".
func interfacePositionErrors(t *testing.T, expr string) []string {
	t.Helper()
	src := interfacePositionDecls + "    _ = (m, fine, f, h)\n    _ = " + expr + "\nend\n"
	line := strings.Count(src, "\n") - 1
	err := checkSource(t, src)
	if err == nil {
		return nil
	}
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("not diagnostics: %v", err)
	}
	var out []string
	for _, d := range ds {
		if d.Warning || d.Hint {
			continue
		}
		if d.Line != line {
			t.Errorf("an error off the case's line %d: %d:%d %s", line, d.Line, d.Col, d.Message)
			continue
		}
		out = append(out, strings.Join(append([]string{d.Message}, d.Hints...), " | "))
	}
	return out
}

// A value that misses the interface its position takes is one error at the
// value. Where its type, or a part its impl asks the interface of, has no
// impl, the error is the position's own, naming the whole value and what
// the position takes. Where its impl's own where clause is unmet, the error
// names that bound, and the same value at a type that meets it is admitted.
func TestInterfacePosition_OneErrorPerCause(t *testing.T) {
	for _, tc := range interfacePositionCases {
		t.Run(tc.position+"/"+tc.cause+"/"+tc.expr, func(t *testing.T) {
			errs := interfacePositionErrors(t, tc.expr)
			if len(errs) == 0 {
				t.Fatalf("the program is admitted; want %q", tc.refused)
			}
			if !slices.Equal(errs, []string{tc.refused}) {
				t.Fatalf("got %q\nwant [%q]", errs, tc.refused)
			}
			if tc.cause != "unmet where clause" {
				return
			}
			met := strings.ReplaceAll(tc.expr, "${m}", "${fine}")
			met = strings.ReplaceAll(strings.ReplaceAll(met, "(m)", "(fine)"), "[m]", "[fine]")
			if met == tc.expr {
				t.Fatalf("no Measure<Feet> form of %s", tc.expr)
			}
			if errs := interfacePositionErrors(t, met); len(errs) != 0 {
				t.Errorf("%s: want no error, got %q", met, errs)
			}
		})
	}
}
