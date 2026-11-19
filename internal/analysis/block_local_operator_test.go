package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A type declared inside a body has no impl: an impl is a module-level
// declaration and cannot name it. Its `==` and `!=` compare by its shape,
// as `Equatable.equal` on it does, however a std module names a type of
// its own, and whether or not the program imports that module: std/xml's
// enum Token derives Equatable, std/calendar's DateTime and
// OffsetDateTime write theirs by hand, and std/log's Level derives it.
// The record names no impl, so nothing can run another type's.
func TestOperatorUses_ABlockLocalTypeHasNoStdTypesImpl(t *testing.T) {
	const body = `fn main()
    type Token String
    struct DateTime
        a: Int
        b: Int
    end
    struct OffsetDateTime
        a: Int
    end
    enum Level
        Debug
        Info
    end
    t = Token("a")
    d = DateTime{a: 1, b: 2}
    n = OffsetDateTime{a: 1}
    l = Level.Debug
    _ = (t == Token("b"), t != Token("b"), Equatable.equal(t, t))
    _ = (d == d, d != d, Equatable.equal(d, d))
    _ = (n == n, n != n, Equatable.equal(n, n))
    _ = (l == Level.Info, l != Level.Info, Equatable.equal(l, l))
    <USES>
end
`
	for _, c := range []struct {
		name    string
		imports string
		uses    string
	}{
		{"no import", "", "_ = 1"},
		{"the std modules imported", "import std::xml\nimport std::calendar\nimport std::log\n",
			"_ = (xml.xml_namespace(), log.Level.Info)\nend\n\nfn uses(n: calendar.OffsetDateTime, d: calendar.DateTime) -> (String, String)\n    (Debug.inspect(n), Debug.inspect(d))"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := c.imports + strings.Replace(body, "<USES>", c.uses, 1)
			lines := strings.Count(c.imports, "\n")
			fa, errs := checkEntry(t, src)
			if len(errs) > 0 {
				t.Fatalf("the checker rejects this source: %v", errs)
			}
			ops := 0
			for n, use := range fa.Operators {
				b, ok := n.(*ast.Binary)
				if !ok || b.Op != "==" && b.Op != "!=" {
					continue
				}
				ops++
				impl := ""
				if use.Impl != nil {
					file, _ := fa.Program().ImplMethodFile(use.Impl.Method)
					impl = " (" + use.Impl.Interface + " for " + use.Impl.Receiver.String() + " in " + file + ")"
				}
				if use.Form != analysis.OperatorStructural || use.Impl != nil {
					t.Errorf("line %d `%s`: Form %d%s, want structural and no impl", b.Line-lines, b.Op, use.Form, impl)
				}
			}
			if ops != 8 {
				t.Errorf("%d `==` and `!=` records, want 8", ops)
			}
			calls := 0
			for n, rc := range fa.Calls {
				call, ok := n.(*ast.Call)
				if !ok || rc.Callee.Method != "equal" {
					continue
				}
				calls++
				if rc.Callee.Form != analysis.OperatorStructural || rc.Callee.Impl != nil {
					t.Errorf("line %d Equatable.equal: Form %d, Impl %v, want structural and no block", call.Line-lines, rc.Callee.Form, rc.Callee.Impl != nil)
				}
			}
			if calls != 4 {
				t.Errorf("%d Equatable.equal records, want 4", calls)
			}
		})
	}
}

// A type declared inside a body has no impl of Comparable, Display or an
// operator interface however a std type of its name has one:
// std/duration's Duration (Add, Subtract, Multiply, Divide, Remainder
// among them), std/calendar's Date and DateTime and std/uuid's Uuid.
// Ordering it, comparing it, a Comparable bound, an interpolation and
// every binary operator are each the missing-impl error a body type named
// Pt gets, never std's impl and never "no matching Add impl", for a
// struct, an enum and a distinct type alike. Its help says to declare the
// type at module level, since a body holds no impl or derive. The file's
// own module-level Duration, whose impl Comparable is written, lends none
// to the body's.
func TestMissingImpls_ABlockLocalTypeHasNoStdTypesImpl(t *testing.T) {
	decls := []struct{ kind, decl, a, b string }{
		{"struct", "struct N\n        n: Int\n    end", "N{n: 1}", "N{n: 2}"},
		{"enum", "enum N\n        A\n        B\n    end", "N.A", "N.B"},
		{"distinct", "type N Int", "N(1)", "N(2)"},
	}
	ops := []struct{ op, iface string }{
		{"a < b", "Comparable"},
		{"Comparable.compare(a, b)", "Comparable"},
		{"Iter.max([a, b])", "Comparable"},
		{"Iter.sort([a, b])", "Comparable"},
		{`"${a}"`, "Display"},
		{"a >= b", "Comparable"},
		{"a + b", "Add"},
		{"a - b", "Subtract"},
		{"a * b", "Multiply"},
		{"a / b", "Divide"},
		{"a % b", "Remainder"},
		{"a &&& b", "BitwiseAnd"},
		{"a ||| b", "BitwiseOr"},
		{"a ^^^ b", "BitwiseXor"},
		{"a <<< b", "ShiftLeft"},
		{"a >>> b", "ShiftRight"},
	}
	for _, d := range decls {
		for _, name := range []string{"Duration", "Date", "DateTime", "Uuid", "Pt"} {
			for _, o := range ops {
				t.Run(d.kind+" "+name+" "+o.op, func(t *testing.T) {
					src := "fn main()\n    " + strings.ReplaceAll(d.decl, "N", name) +
						"\n    a = " + strings.ReplaceAll(d.a, "N", name) +
						"\n    b = " + strings.ReplaceAll(d.b, "N", name) +
						"\n    _ = (a, b)\n    _ = " + o.op + "\nend\n"
					fa, errs := checkEntry(t, src)
					errs = append(errs, analysis.FinalizeCoherence(fa)...)
					if len(errs) == 0 {
						t.Fatalf("the checker accepts this source:\n%s", src)
					}
					// A bound a type of no impl's name misses says so first
					// (`Pt does not implement Comparable`).
					missing := "no impl of `" + o.iface + "` for `"
					unmet := name + " does not implement " + o.iface
					help := "is declared inside a body, where it can have no impl or derive; declare it at module level to give it an impl of `" + o.iface + "`"
					for _, e := range errs {
						if strings.Contains(e.Message, missing) && strings.Contains(e.Message, name+"`") || strings.HasPrefix(e.Message, unmet) {
							if len(e.Hints) != 1 || !strings.Contains(e.Hints[0], help) {
								t.Errorf("%q: help %q, want one containing %q", e.Message, e.Hints, help)
							}
							return
						}
					}
					t.Errorf("errors %v, want one containing %q for %s", errs, missing, name)
				})
			}
		}
	}
	t.Run("the file's own Duration", func(t *testing.T) {
		const src = `struct Duration
    n: Int
end

impl Comparable for Duration
    fn compare(a: Duration, b: Duration) -> Ordering
        Comparable.compare(a.n, b.n)
    end
end

fn local() -> Bool
    struct Duration
        n: Int
    end
    Duration{n: 1} < Duration{n: 2}
end

fn main()
    _ = (local(), Duration{n: 1} < Duration{n: 2})
end
`
		fa, errs := checkEntry(t, src)
		errs = append(errs, analysis.FinalizeCoherence(fa)...)
		if len(errs) != 1 || !strings.Contains(errs[0].Message, "no impl of `Comparable` for `") || errs[0].Line != 15 {
			t.Fatalf("errors %v, want one missing Comparable at line 15, the body's `<`", errs)
		}
	})
}
