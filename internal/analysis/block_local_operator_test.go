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
// enum Token derives Equatable, std/calendar's DateTime and std/phrase's
// Noun write theirs by hand, and std/log's Level derives it. The record
// names no impl, so nothing can run another type's.
func TestOperatorUses_ABlockLocalTypeHasNoStdTypesImpl(t *testing.T) {
	const body = `fn main()
    type Token String
    struct DateTime
        a: Int
        b: Int
    end
    struct Noun
        a: Int
    end
    enum Level
        Debug
        Info
    end
    t = Token("a")
    d = DateTime{a: 1, b: 2}
    n = Noun{a: 1}
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
		{"the std modules imported", "import std::xml\nimport std::calendar\nimport std::phrase\nimport std::log\n",
			"_ = (xml.xml_namespace(), log.Level.Info)\nend\n\nfn uses(n: phrase.Noun, d: calendar.DateTime) -> (String, String)\n    (Debug.inspect(n), Debug.inspect(d))"},
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
