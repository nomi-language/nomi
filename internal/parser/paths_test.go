package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// An import path joins its segments with `::`, and each segment keeps its
// own position.
func TestImportPath_DoubleColonSegments(t *testing.T) {
	nodes := parse(t, "import views::admin::users.{self, UserDetail}\n")
	imp, ok := nodes[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("got %T, want *ast.ImportStmt", nodes[0])
	}
	want := []struct {
		name string
		col  int
	}{{"views", 8}, {"admin", 15}, {"users", 22}}
	if len(imp.ModulePath) != len(want) || imp.FileSegments != len(want) {
		t.Fatalf("path %v (file segments %d), want 3 segments", imp.ModulePath, imp.FileSegments)
	}
	for i, w := range want {
		id, ok := imp.ModulePath[i].(*ast.Ident)
		if !ok || id.Name != w.name || id.Line != 1 || id.Col != w.col {
			t.Errorf("segment %d is %#v, want %s at 1:%d", i, imp.ModulePath[i], w.name, w.col)
		}
	}
	if len(imp.Names) != 1 || ast.ImportNodeName(imp.Names[0]) != "UserDetail" || !imp.IncludeParent {
		t.Errorf("selection %v (self %v), want {self, UserDetail}", imp.Names, imp.IncludeParent)
	}
}

// A `/` in an import path is an error that spells the path with `::`. The
// parser must keep rejecting it: there is no second spelling.
func TestImportPath_SlashIsAnError(t *testing.T) {
	for _, c := range []struct {
		src, msg  string
		line, col int
	}{
		{"import std/io\n", "import paths use `::`: write `std::io`", 1, 11},
		{"import std/http.{Request}\n", "import paths use `::`: write `std::http`", 1, 11},
		{"import views/admin/users\n", "import paths use `::`: write `views::admin::users`", 1, 13},
		{"import\n    std/io\nend\n", "import paths use `::`: write `std::io`", 2, 8},
		{"import std::http/request\n", "import paths use `::`: write `std::http::request`", 1, 17},
	} {
		_, err := Parse(lexer.Lex(c.src))
		if err == nil {
			t.Errorf("%q parses; a `/` in an import path must be an error", c.src)
			continue
		}
		pe, ok := err.(ParseError)
		if !ok || pe.Message != c.msg || pe.Line != c.line || pe.Col != c.col {
			t.Errorf("%q: got %v, want %d:%d %q", c.src, err, c.line, c.col, c.msg)
		}
	}
}

// `::` joins segments only when it touches both of them.
func TestImportPath_SpacedDoubleColonIsAnError(t *testing.T) {
	for _, src := range []string{"import std :: io\n", "fn f() -> Int\n    db :: posts.count()\nend\n"} {
		_, err := Parse(lexer.Lex(src))
		if err == nil {
			t.Errorf("%q parses", src)
		}
	}
	_, err := Parse(lexer.Lex("import std :: io\n"))
	if err == nil || !strings.Contains(err.Error(), "`::` joins the lowercase segments of a file's path, with no space around it") {
		t.Errorf("got %v, want the `::` spacing error", err)
	}
}

// A full path is a FilePath, the object of a member access, in a call.
func TestFullPath_Expression(t *testing.T) {
	nodes := parse(t, "fn f() -> Int\n    db::posts.count(1)\nend\n")
	fn := nodes[0].(*ast.FuncDef)
	stmt := fn.Body.Stmts[0]
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	call, ok := stmt.(*ast.Call)
	if !ok {
		t.Fatalf("got %T, want *ast.Call", stmt)
	}
	fa, ok := call.Func.(*ast.FieldAccess)
	if !ok || fa.Field.Name != "count" {
		t.Fatalf("callee %#v, want a field access of count", call.Func)
	}
	fp, ok := fa.Object.(*ast.FilePath)
	if !ok {
		t.Fatalf("object %T, want *ast.FilePath", fa.Object)
	}
	if fp.Spelling() != "db::posts" || fp.Line != 2 || fp.Col != 5 || len(fp.Segments) != 2 || fp.Segments[1].Col != 9 {
		t.Errorf("path %+v, want db::posts at 2:5 with posts at 2:9", fp)
	}
}

// A full path qualifies a type, a struct literal and a pattern, as a
// QualifiedType whose Path holds the segments.
func TestFullPath_TypesLiteralsPatterns(t *testing.T) {
	src := "fn f(p: views::admin::users.User) -> Int\n" +
		"    q = db::schema.Post{id: 1}\n" +
		"    case p\n" +
		"        db::schema.Kind.Active -> 1\n" +
		"        _ -> 2\n" +
		"    end\n" +
		"end\n"
	nodes := parse(t, src)
	var quals []*ast.QualifiedType
	ast.Inspect(nodes[0], func(n ast.Node) bool {
		if q, ok := n.(*ast.QualifiedType); ok && q.Path != nil {
			quals = append(quals, q)
		}
		return true
	})
	want := []string{"views::admin::users", "db::schema", "db::schema.Kind"}
	if len(quals) != len(want) {
		t.Fatalf("got %d full-path qualified types, want %d", len(quals), len(want))
	}
	for i, q := range quals {
		if q.Module != want[i] {
			t.Errorf("qualifier %d is %q, want %q", i, q.Module, want[i])
		}
	}
	if p := quals[0].Path; len(p) != 3 || p[2].Name != "users" || p[2].Col != 23 {
		t.Errorf("path %+v, want users at column 23", p)
	}
}

// A full path alone is not a value.
func TestFullPath_AloneIsAnError(t *testing.T) {
	_, err := Parse(lexer.Lex("fn f() -> Int\n    x = db::posts\n    1\nend\n"))
	if err == nil || !strings.Contains(err.Error(), "`db::posts` is a file, not a value") {
		t.Errorf("got %v, want the file-is-not-a-value error", err)
	}
}
