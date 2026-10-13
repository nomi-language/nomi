package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

func TestParseExportFunc(t *testing.T) {
	nodes, err := Parse(lexer.Lex("export fn guess(n: Int): Int {\n    n\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("got %T, want *ast.FuncDef", nodes[0])
	}
	if !fd.Export || fd.Public || fd.Name != "guess" {
		t.Errorf("Export=%v Public=%v Name=%q, want an exported, non-pub `guess`", fd.Export, fd.Public, fd.Name)
	}
	if fd.ExportLine != 1 || fd.ExportCol != 1 {
		t.Errorf("export keyword at %d:%d, want 1:1", fd.ExportLine, fd.ExportCol)
	}
	if fd.Line != 1 || fd.Col != 11 {
		t.Errorf("name at %d:%d, want 1:11", fd.Line, fd.Col)
	}
}

func TestParseExportRefusals(t *testing.T) {
	cases := []struct {
		name, src, want string
		line, col       int
	}{
		{"struct", "export struct P {\n    x: Int\n}\n", "`export` marks a function: `export fn name(...)`", 1, 1},
		{"name", "export foo\n", "`export` marks a function: `export fn name(...)`", 1, 1},
		{"brace", "export { foo }\n", "`export` marks a function: `export fn name(...)`", 1, 1},
		{"after import", "import a.{B} export\n", "`export` marks a function: `export fn name(...)`", 1, 14},
		{"export pub", "export pub fn f() {}\n", "`export` marks a function: `export fn name(...)`", 1, 1},
		{"pub export", "pub export fn f() {}\n", "an exported function takes no `pub`: write `export fn`", 1, 1},
		{"host", "export host fn f(): Int\n", "a `host fn` is supplied by the host and cannot be exported; an `export fn` has a body", 1, 1},
		{"host type", "export host type Conn\n", "`export` marks a function: `export fn name(...)`", 1, 1},
		{"go binding", "gopkg \"strings\" as s\nexport fn up(x: String): String go s.ToUpper\n", "a Go binding cannot be exported; an `export fn` has a body", 2, 1},
		{"in a body", "fn main() {\n    export fn g() {}\n}\n", "`export` applies to top-level functions only", 2, 5},
		{"in an impl", "struct P {\n    x: Int\n}\nimpl P {\n    export fn g() {}\n}\n", "a function in an `impl Type` block cannot be exported: `export fn` declares a top-level function", 5, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseWithRecovery(lexer.Lex(c.src))
			if len(errs) == 0 {
				t.Fatalf("the parser accepts %q", c.src)
			}
			e := errs[0]
			if e.Message != c.want || e.Line != c.line || e.Col != c.col {
				t.Errorf("got %d:%d %q, want %d:%d %q", e.Line, e.Col, e.Message, c.line, c.col, c.want)
			}
		})
	}
}

// A refused `export` steps past the keyword, so recovery parses what follows.
func TestParseWithRecovery_ResumesAfterARefusedExport(t *testing.T) {
	nodes, errs := ParseWithRecovery(lexer.Lex("export { foo }\nfn helper(): Int { 1 }"))
	if len(errs) == 0 {
		t.Fatal("the parser accepts `export { foo }`")
	}
	for _, n := range nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == "helper" {
			return
		}
	}
	t.Error("recovery lost `helper` after the refused `export`")
}

func TestParsePubInABlockIsRefused(t *testing.T) {
	const want = "`pub` applies to top-level declarations only"
	cases := []struct {
		name, src string
		line, col int
	}{
		{"fn body", "fn main() {\n    pub fn g() {}\n}\n", 2, 5},
		{"import in a body", "fn main() {\n    pub import std/io\n}\n", 2, 5},
		{"lambda", "fn main() {\n    f = |x| {\n        pub fn g() {}\n        x\n    }\n}\n", 3, 9},
		{"test body", "test \"t\" {\n    pub fn g() {}\n}\n", 2, 5},
		{"once initializer", "once v = {\n    pub fn g() {}\n    1\n}\n", 2, 5},
		{"case arm", "fn f(x: Int): Int {\n    case x {\n        _ -> {\n            pub fn g() {}\n            x\n        }\n    }\n}\n", 4, 13},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseWithRecovery(lexer.Lex(c.src))
			if len(errs) == 0 {
				t.Fatalf("the parser accepts %q", c.src)
			}
			e := errs[0]
			if e.Message != want || e.Line != c.line || e.Col != c.col {
				t.Errorf("got %d:%d %q, want %d:%d %q", e.Line, e.Col, e.Message, c.line, c.col, want)
			}
		})
	}
}
