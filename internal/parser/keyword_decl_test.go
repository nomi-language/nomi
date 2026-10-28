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

func TestParseKeywordDeclRefusals(t *testing.T) {
	cases := []struct {
		name, src, want string
		line, col       int
	}{
		{"quoted body", "message hi \"Hi\"\n", "the body of a `message` declaration is a raw string: write it between backticks", 1, 12},
		{"trailing", "message hi `Hi` 3\n", "unexpected 3 in a `message` declaration; it is `message name(params) -> Type `body``, each part after the name optional", 1, 17},
		{"tagged message", "message hi Mf2`Hi`\n", "a `message` body is MessageFormat 2 text and takes no tag: remove `Mf2`", 1, 12},
		{"tagged noun", "noun item Mf2`{{item}}`\n", "a `noun` body is MessageFormat 2 text and takes no tag: remove `Mf2`", 1, 11},
		{"misspelled type keyword", "pub strct Point {\n    x: Int\n}\n", "`strct` declares nothing: a type is declared with `struct`, `enum` or `type`", 1, 5},
		{"in a body", "fn main() {\n    message hi `Hi`\n}\n", "", 0, 0},
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

// A name before a type name is no keyword
// declaration, so the top-level forms that start that way keep their
// meaning (`derive Debug for Point`), except after `message` and `noun`,
// whose names are never type names: that is their error.
func TestParseKeywordDeclNamesNoType(t *testing.T) {
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

// A body may be a multi-line raw string, whose lines start at BodyCol, and
// has no tag.
func TestParseMultiLineKeywordDeclBody(t *testing.T) {
	multi := "message orders `\n    SELECT 1\n    FROM t\n    `\n"
	nodes, err := Parse(lexer.Lex(multi))
	if err != nil {
		t.Fatal(err)
	}
	d := nodes[0].(*ast.KeywordDecl)
	if d.BodyTag != "" || d.TagCol() != 0 || !d.Body.Triple || d.Body.Value != "SELECT 1\nFROM t" || d.BodyCol != 5 {
		t.Errorf("got tag %q, body %+v, body col %d", d.BodyTag, d.Body, d.BodyCol)
	}
	if l, col := d.BodyPosition(9); l != 3 || col != 5 {
		t.Errorf("offset 9 at %d:%d, want 3:5", l, col)
	}
}
