package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// An `embed` is the keyword-declaration rule with a body of patterns, which
// the parser splits and places.
func TestParseEmbedDecl(t *testing.T) {
	src := "/// The site.\npub embed site `static \"my file.txt\"  *.html`\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := nodes[0].(*ast.EmbedDecl)
	if !ok {
		t.Fatalf("got %T, want *ast.EmbedDecl", nodes[0])
	}
	if d.Name != "site" || !d.Public || d.Doc != "The site." || d.Line != 2 || d.Col != 5 || d.NameCol != 11 {
		t.Fatalf("got %+v", d)
	}
	want := []ast.EmbedPattern{
		{Text: "static", Line: 2, Col: 17, Width: 6},
		{Text: "my file.txt", Line: 2, Col: 24, Width: 13},
		{Text: "*.html", Line: 2, Col: 39, Width: 6},
	}
	if len(d.Patterns) != len(want) {
		t.Fatalf("patterns %+v, want %+v", d.Patterns, want)
	}
	lines := strings.Split(src, "\n")
	for i, p := range d.Patterns {
		if p != want[i] {
			t.Errorf("pattern %d: %+v, want %+v", i, p, want[i])
		}
		if got := lines[p.Line-1][p.Col-1 : p.Col-1+p.Width]; strings.Trim(got, `"`) != p.Text {
			t.Errorf("pattern %d is placed on %q", i, got)
		}
	}
}

// A multi-line body places each pattern on its own line.
func TestParseEmbedDeclMultiLine(t *testing.T) {
	src := "embed assets `\n    static\n    templates/*.html\n    `\n"
	nodes, err := Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	d := nodes[0].(*ast.EmbedDecl)
	want := []ast.EmbedPattern{
		{Text: "static", Line: 2, Col: 5, Width: 6},
		{Text: "templates/*.html", Line: 3, Col: 5, Width: 16},
	}
	if len(d.Patterns) != 2 || d.Patterns[0] != want[0] || d.Patterns[1] != want[1] {
		t.Fatalf("patterns %+v, want %+v", d.Patterns, want)
	}
}

// Each malformed `embed` is refused with its own message. Each case is a
// rejection: it fails if the source starts being admitted.
func TestParseEmbedDeclErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"embed site\n", "line 1, col 11: an `embed` names the files it holds"},
		{"embed site(x: Int) `a`\n", "line 1, col 11: an `embed` takes no parameters"},
		{"embed site -> Files `a`\n", "line 1, col 12: an `embed` is an `embed.Files`, so it is written with no `->` type"},
		{"embed site \"static\"\n", "line 1, col 12: the patterns of an `embed` are a raw string"},
		{"embed site Html`static`\n", "line 1, col 12: an `embed` body is a list of file patterns and takes no tag: remove `Html`"},
		{"embed site ``\n", "line 1, col 12: an `embed` names at least one file pattern"},
		{"embed site `  `\n", "line 1, col 12: an `embed` names at least one file pattern"},
		{"embed site `\"a b`\n", "line 1, col 13: this `embed` pattern's `\"` is never closed"},
		{"embed site `\"a\"b`\n", "line 1, col 13: a quoted `embed` pattern ends at its closing `\"`"},
		{"embed site `\"\"`\n", "line 1, col 13: an `embed` pattern is empty"},
		{"embed Site `a`\n", "line 1, col 7: the name of a `embed` is snake_case, not the type name `Site`"},
		{"embed site `a` 1\n", "line 1, col 16: unexpected 1 in an `embed` declaration"},
	}
	for _, c := range cases {
		_, err := Parse(lexer.Lex(c.src))
		if err == nil {
			t.Errorf("%q parsed; want %q", c.src, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.src, err, c.want)
		}
	}
}
