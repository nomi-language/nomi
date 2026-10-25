package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A keyword declaration is read by the general rule
// `[pub] keyword name [(params)] [-> type] [raw body]`, each part after the
// name optional, whatever the keyword.
func TestParseKeywordDeclarations(t *testing.T) {
	cases := []struct {
		name, src            string
		keyword, declName    string
		public, params, body bool
		nparams              int
		result               string
		value                string
	}{
		{"message", "pub message cart_items(count: Int) `{$count :integer} items`\n", "message", "cart_items", true, true, true, 1, "", "{$count :integer} items"},
		{"bare", "database main\n", "database", "main", false, false, false, 0, "", ""},
		{"body only", "noun item `{{item}}`\n", "noun", "item", false, false, true, 0, "", "{{item}}"},
		{"empty params", "message hello() `Hello`\n", "message", "hello", false, true, true, 0, "", "Hello"},
		{"result", "query user_by_email(email: String) -> Maybe<User> `select 1`\n", "query", "user_by_email", false, true, true, 1, "Maybe<User>", "select 1"},
		{"qualified type name", "pub choices.one_of! Size `small medium large`\n", "one_of", "Size", true, false, true, 0, "", "small medium large"},
		{"imported type name", "one_of! Size `small`\n", "one_of", "Size", false, false, true, 0, "", "small"},
		{"unmarked qualified type name", "choices.one_of Size\n", "one_of", "Size", false, false, false, 0, "", ""},
		{"type name with params", "pub kit.record! Point(x: Int, y: Int)\n", "record", "Point", true, true, false, 2, "", ""},
		{"multi-line body", "message status(state: Status) `\n    .input {$state :string}\n    .match $state\n    * {{x}}\n    `\n", "message", "status", false, true, true, 1, "", ".input {$state :string}\n.match $state\n* {{x}}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := Parse(lexer.Lex(c.src))
			if err != nil {
				t.Fatal(err)
			}
			d, ok := nodes[0].(*ast.KeywordDecl)
			if !ok {
				t.Fatalf("got %T, want *ast.KeywordDecl", nodes[0])
			}
			if d.Keyword != c.keyword || d.Name != c.declName || d.Public != c.public || d.HasParams != c.params || len(d.Params) != c.nparams {
				t.Errorf("got %+v", d)
			}
			got := ""
			if d.ReturnTypeExpr != nil {
				got = d.ReturnTypeExpr.TypeString()
			}
			if got != c.result {
				t.Errorf("result %q, want %q", got, c.result)
			}
			if (d.Body != nil) != c.body || d.Body != nil && d.Body.Value != c.value {
				t.Errorf("body %+v, want %q", d.Body, c.value)
			}
		})
	}
}

// The keyword's and the name's positions, and where each byte of a
// multi-line body is in the source.
func TestParseKeywordDeclPositions(t *testing.T) {
	src := "/// The status.\npub message status(state: Status) `\n    .input {$state :string}\n    * {{x}}\n    `\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	d := nodes[0].(*ast.KeywordDecl)
	if d.Line != 2 || d.Col != 5 || d.NameLine != 2 || d.NameCol != 13 || d.Doc != "The status." {
		t.Fatalf("keyword %d:%d, name %d:%d, doc %q", d.Line, d.Col, d.NameLine, d.NameCol, d.Doc)
	}
	// `$state` on the body's first line is at 3:13, `{{x}}` on its second at 4:7.
	for _, c := range []struct{ offset, line, col int }{{8, 3, 13}, {26, 4, 7}} {
		if l, col := d.BodyPosition(c.offset); l != c.line || col != c.col {
			t.Errorf("offset %d at %d:%d, want %d:%d", c.offset, l, col, c.line, c.col)
		}
	}
	one, err := Parse(lexer.Lex("message hi `Hi {$name}`\n"))
	if err != nil {
		t.Fatal(err)
	}
	if l, col := one[0].(*ast.KeywordDecl).BodyPosition(3); l != 1 || col != 16 {
		t.Errorf("one-line offset 3 at %d:%d, want 1:16", l, col)
	}
}

// `[pub] macro name: Type` declares a keyword; it is no keyword
// declaration.
func TestParseMacroDecl(t *testing.T) {
	nodes, err := Parse(lexer.Lex("/// Tables from CSV.\npub macro table: CsvTable\n"))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := nodes[0].(*ast.MacroDecl)
	if !ok {
		t.Fatalf("got %T, want *ast.MacroDecl", nodes[0])
	}
	if d.Name != "table" || !d.Public || d.Impl.TypeString() != "CsvTable" || d.Doc != "Tables from CSV." {
		t.Errorf("got %+v", d)
	}
	if d.Line != 2 || d.Col != 5 || d.NameLine != 2 || d.NameCol != 11 {
		t.Errorf("keyword %d:%d, name %d:%d", d.Line, d.Col, d.NameLine, d.NameCol)
	}
	for _, c := range []struct{ src, want string }{
		{"macro table\n", "a `macro` declaration names the type whose impls expand it: write `macro table: Type`"},
		{"macro table: CsvTable `x`\n", "unexpected x in a `macro` declaration; it is `macro name: Type`"},
	} {
		_, errs := ParseWithRecovery(lexer.Lex(c.src))
		if len(errs) == 0 {
			t.Fatalf("the parser accepts %q", c.src)
		}
		if errs[0].Message != c.want {
			t.Errorf("%q: got %q, want %q", c.src, errs[0].Message, c.want)
		}
	}
}

func TestParseKeywordDeclRefusals(t *testing.T) {
	cases := []struct {
		name, src, want string
		line, col       int
	}{
		{"quoted body", "message hi \"Hi\"\n", "the body of a `message` declaration is a raw string: write it between backticks", 1, 12},
		{"trailing", "message hi `Hi` 3\n", "unexpected 3 in a `message` declaration; it is `message name(params) -> Type `body``, each part after the name optional", 1, 17},
		{"qualified trailing", "sqlkit.query! q `x` 3\n", "unexpected 3 in a `sqlkit.query!` declaration; it is `sqlkit.query! name(params) -> Type `body``, each part after the name optional", 1, 21},
		{"quoted tagged body", "sqlkit.query! q Sql\"SELECT 1\"\n", "the body of a `sqlkit.query!` declaration is a raw string: write it between backticks", 1, 17},
		{"tagged message", "message hi Mf2`Hi`\n", "a `message` body is MessageFormat 2 text and takes no tag: remove `Mf2`", 1, 12},
		{"marked message", "message! hi `Hi`\n", "`message` is built in; write it without `!`", 1, 8},
		{"marked noun", "pub noun! item `{{item}}`\n", "`noun` is built in; write it without `!`", 1, 9},
		{"spaced mark", "sqlkit.query ! q `x`\n", "a macro's keyword is marked with a `!` written against it: `query!`", 1, 14},
		{"tagged noun", "noun item Mf2`{{item}}`\n", "a `noun` body is MessageFormat 2 text and takes no tag: remove `Mf2`", 1, 11},
		{"misspelled type keyword", "pub strct Point {\n    x: Int\n}\n", "`strct` declares nothing: a type is declared with `struct`, `enum` or `type`, and a macro's declaration marks its keyword with `!`: `strct! Point`", 1, 5},
		{"unmarked imported keyword before a type name", "pub one_of Size `small`\n", "`one_of` declares nothing: a type is declared with `struct`, `enum` or `type`, and a macro's declaration marks its keyword with `!`: `one_of! Size`", 1, 5},
		{"in a body", "fn main() {\n    message hi `Hi`\n}\n", "", 0, 0},
		{"marked in a body", "fn main() {\n    one_of! Size\n}\n", "", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, errs := ParseWithRecovery(lexer.Lex(c.src))
			if c.want == "" {
				// In a body two names in a row are no declaration.
				if len(errs) == 0 {
					if _, ok := nodes[0].(*ast.KeywordDecl); ok {
						t.Fatalf("a keyword declaration was read inside a body")
					}
				}
				return
			}
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

// An unmarked, unqualified name before a type name is no keyword
// declaration, so the top-level forms that start that way keep their
// meaning (`derive Debug for Point`), except after `message` and `noun`,
// whose names are never type names: that is their error.
func TestParseKeywordDeclTypeNameNeedsAMark(t *testing.T) {
	nodes, err := Parse(lexer.Lex("derive Debug for Point\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodes[0].(*ast.KeywordDecl); ok {
		t.Fatalf("`derive Debug for Point` was read as a keyword declaration")
	}
	for _, kw := range []string{"message", "noun"} {
		_, errs := ParseWithRecovery(lexer.Lex(kw + " Hi `Hi`\n"))
		if len(errs) == 0 {
			t.Fatalf("the parser accepts a `%s` named by a type name", kw)
		}
		if want := "the name of a `" + kw + "` is snake_case, not the type name `Hi`"; errs[0].Message != want || errs[0].Col != len(kw)+2 {
			t.Errorf("got %d %q, want %q at the name", errs[0].Col, errs[0].Message, want)
		}
	}
}

// A macro's keyword is qualified by the file it is imported from, and
// a body may carry a typed literal's tag: `sqlkit.query! q(id: Int) Sql`...“.
func TestParseQualifiedTaggedKeywordDecl(t *testing.T) {
	src := "pub sqlkit.query! orders(user_id: Int) -> Int Sql`SELECT 1`\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := nodes[0].(*ast.KeywordDecl)
	if !ok {
		t.Fatalf("got %T, want *ast.KeywordDecl", nodes[0])
	}
	if d.Module != "sqlkit" || d.Keyword != "query" || !d.Marked || d.Name != "orders" || !d.Public || d.BodyTag != "Sql" || d.Body == nil || d.Body.Value != "SELECT 1" {
		t.Fatalf("got %+v", d)
	}
	if d.ModuleLine != 1 || d.ModuleCol != 5 || d.Line != 1 || d.Col != 12 || d.KeywordEndCol() != 18 || d.NameCol != 19 || d.TagCol() != 47 || d.Body.Col != 50 {
		t.Errorf("module %d:%d, keyword %d:%d to %d, name col %d, tag col %d, body col %d", d.ModuleLine, d.ModuleCol, d.Line, d.Col, d.KeywordEndCol(), d.NameCol, d.TagCol(), d.Body.Col)
	}
	if l, col := d.BodyPosition(0); l != 1 || col != 51 {
		t.Errorf("body starts at %d:%d, want 1:51", l, col)
	}
	if got := d.QualifiedKeyword(); got != "sqlkit.query!" {
		t.Errorf("qualified keyword %q", got)
	}
	nodes, err = Parse(lexer.Lex("query! orders\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d := nodes[0].(*ast.KeywordDecl); d.Module != "" || d.Keyword != "query" || !d.Marked || d.Name != "orders" {
		t.Errorf("got %+v", d)
	}
	multi := "sqlkit.query! orders Sql`\n    SELECT 1\n    FROM t\n    `\n"
	nodes, err = Parse(lexer.Lex(multi))
	if err != nil {
		t.Fatal(err)
	}
	d = nodes[0].(*ast.KeywordDecl)
	if d.BodyTag != "Sql" || !d.Body.Triple || d.Body.Value != "SELECT 1\nFROM t" || d.BodyCol != 5 {
		t.Errorf("got tag %q, body %+v, body col %d", d.BodyTag, d.Body, d.BodyCol)
	}
	if l, col := d.BodyPosition(9); l != 3 || col != 5 {
		t.Errorf("offset 9 at %d:%d, want 3:5", l, col)
	}
	// A bare keyword has no qualifier, and an untagged body no tag.
	nodes, err = Parse(lexer.Lex("message hi `Hi`\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d := nodes[0].(*ast.KeywordDecl); d.Module != "" || d.BodyTag != "" || d.TagCol() != 0 {
		t.Errorf("got %+v", d)
	}
}
