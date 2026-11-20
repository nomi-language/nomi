package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// bodyEndingDeclarations is one declaration of each kind a body may hold,
// indented to stand as a body's last statement.
var bodyEndingDeclarations = map[string]string{
	"distinct type": "type B Int",
	"struct":        "struct P\n    x: Int\nend",
	"enum":          "enum Q\n    A\n    B\nend",
	"typealias":     "typealias N Int",
	"interface":     "interface J\n    fn j(v: self) -> Int\nend",
	"fn":            "fn inner() -> Int\n    1\nend",
}

// indentBy indents every line of s by n levels of four spaces.
func indentBy(s string, n int) string {
	pad := strings.Repeat("    ", n)
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}

// A body whose last statement is a declaration answers Unit, as one ending
// in a binding or a `defer` does, and runs. The builder lowered such a
// declaration as the body's value and stopped with an internal compiler
// error ("lowerNode reached with *ast.TypeDef, which is no expression").
func TestRun_ABodyEndingInADeclarationAnswersUnit(t *testing.T) {
	contexts := map[string]func(decl string) string{
		"function body": func(decl string) string {
			return "import std::io\n\nfn f()\n    io.print(\"ran\")\n" + indentBy(decl, 1) + "\nend\n\nfn main()\n    f()\nend\n"
		},
		"lambda body": func(decl string) string {
			return "import std::io\n\nfn main()\n    g = fn ->\n        io.print(\"ran\")\n" + indentBy(decl, 2) + "\n    end\n    g()\nend\n"
		},
		"if arm": func(decl string) string {
			return "import std::io\n\nfn pick(c: Bool)\n    if c then\n        io.print(\"ran\")\n" + indentBy(decl, 2) +
				"\n    else\n        io.print(\"other\")\n    end\nend\n\nfn main()\n    pick(True)\nend\n"
		},
		"case arm": func(decl string) string {
			return "import std::io\n\nfn pick(n: Int)\n    case n\n        0 ->\n            io.print(\"ran\")\n" + indentBy(decl, 3) +
				"\n        _ -> io.print(\"other\")\n    end\nend\n\nfn main()\n    pick(0)\nend\n"
		},
	}
	for kind, decl := range bodyEndingDeclarations {
		for where, program := range contexts {
			t.Run(kind+" ending a "+where, func(t *testing.T) {
				if out := runOutput(t, program(decl)); out != "ran\n" {
					t.Errorf("output %q, want %q", out, "ran\n")
				}
			})
		}
		t.Run(kind+" ending a test body", func(t *testing.T) {
			src := "test \"ends in a declaration\"\n    assert 1 + 1 == 2\n" + indentBy(decl, 1) + "\nend\n"
			const name = "decl_test.nomi"
			p, err := vmhost.LoadSource(name, src)
			if err != nil {
				t.Fatalf("the front end rejects the test: %v", err)
			}
			var buf bytes.Buffer
			rep := vmhost.NewTestReport(&buf)
			p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
			rep.Summary()
			if out := buf.String(); !strings.Contains(out, "1 passed, 0 failed") {
				t.Errorf("the test did not pass:\n%s", out)
			}
		})
	}
}

// The checker types such a body as Unit, so a function declared to return
// another type and ending in a declaration is the return type mismatch a
// body ending in a binding is.
func TestRun_ABodyEndingInADeclarationIsNoOtherType(t *testing.T) {
	for kind, decl := range bodyEndingDeclarations {
		t.Run(kind, func(t *testing.T) {
			src := "fn f() -> Int\n" + indentBy(decl, 1) + "\nend\n\nfn main()\n    _ = f()\nend\n"
			_, err := vmhost.Load(writeProgram(t, src))
			if err == nil {
				t.Fatal("a body declared to return Int and ending in a declaration is accepted")
			}
			if want := "return type mismatch: expected Int, got Unit"; !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not say %q", err, want)
			}
		})
	}
}
