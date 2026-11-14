package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

const (
	replShoe      = "noun shoe `.input {$count :integer} .match $count one {{shoe}} * {{shoes}}`\n"
	replShoeCount = "phrase shoe_count(item: Noun, count: Int) `{$count} {$item :nomi:noun count=$count}`\n"
)

// A session declares a noun and a phrase, each in its own input, and a
// later input formats the phrase in the REPL's source locale, `en`, as a
// file outside a project does.
func TestReplVM_ANounAndAPhraseAreDeclaredAndFormatted(t *testing.T) {
	replExpect(t, replShoe+replShoeCount+
		"Phrase.format(shoe_count(shoe, 1), Locale\"en\")\n"+
		"Phrase.format(shoe_count(shoe, 3), Locale\"en\")\n",
		"\"1 shoe\"\n\"3 shoes\"\n")
}

// A redeclared phrase or noun is a new definition, as a redefined `fn` is:
// an earlier function keeps the one it was written against, and later
// inputs get the new one. A `fn` of a phrase's name replaces it for later
// inputs the same way.
func TestReplVM_ARedeclaredPhraseOrNounIsANewDefinition(t *testing.T) {
	replExpect(t, replShoe+replShoeCount+
		"fn old() -> String\n  Phrase.format(shoe_count(shoe, 1), Locale\"en\")\nend\n"+
		"phrase shoe_count(item: Noun, count: Int) `now {$count} {$item :nomi:noun count=$count}`\n"+
		"Phrase.format(shoe_count(shoe, 3), Locale\"en\")\n"+
		"old()\n"+
		"noun shoe `.input {$count :integer} .match $count one {{boot}} * {{boots}}`\n"+
		"Phrase.format(shoe_count(shoe, 3), Locale\"en\")\n"+
		"old()\n"+
		"fn shoe_count() -> Int\n  5\nend\n"+
		"shoe_count()\n"+
		"old()\n",
		"\"now 3 shoes\"\n\"1 shoe\"\n\"now 3 boots\"\n\"1 shoe\"\n5\n\"1 shoe\"\n")
}

// A phrase whose text is wrong is reported at the input's line, and the
// session keeps what it had.
func TestReplVM_ARejectedPhraseChangesNothing(t *testing.T) {
	replExpect(t, replShoe+replShoeCount+
		"phrase shoe_count(item: Noun, count: Int) `{$count} {$item :nomi:noun count=$missing}`\n"+
		"Phrase.format(shoe_count(shoe, 3), Locale\"en\")\n",
		"\"3 shoes\"\n",
		"line 1, col ")
}

// A raw string still open at the end of a line continues the input onto the
// next, as an open `{` does, and a declaration with a multi-line body is
// one input.
func TestReplVM_AnOpenBacktickContinuesTheInput(t *testing.T) {
	var prompts []bool
	lines := strings.Split(
		"noun show `\n.input {$count :integer} .match $count one {{show}} * {{shows}}\n`\n"+
			"phrase show_count(item: Noun, count: Int) `{$count} {$item :nomi:noun count=$count}`\n"+
			"Phrase.format(show_count(show, 2), Locale\"en\")\n", "\n")
	var out, errOut strings.Builder
	s := newReplSession(&out, &errOut)
	i := 0
	replLoop(func(continuation bool) (string, error) {
		prompts = append(prompts, continuation)
		if i >= len(lines) {
			return "", fmt.Errorf("end")
		}
		i++
		return lines[i-1], nil
	}, s, &out, &errOut)
	if errOut.Len() > 0 {
		t.Fatalf("stderr:\n%s", errOut.String())
	}
	if out.String() != "\"2 shows\"\n" {
		t.Errorf("stdout %q, want %q", out.String(), "\"2 shows\"\n")
	}
	// The noun's three lines (two of them continuations), the phrase, the
	// call, and the read that ends the session.
	want := []bool{false, true, true, false, false, false, false}
	if fmt.Sprint(prompts) != fmt.Sprint(want) {
		t.Errorf("continuation prompts %v, want %v", prompts, want)
	}
}

// Every declaration a file may hold at top level is a declaration in the
// REPL too: none falls through to the statements of the input's main,
// where it would be read as names ("undefined variable 'noun'").
func TestReplVM_EveryTopLevelDeclarationIsADeclaration(t *testing.T) {
	sources := []string{
		"fn f() -> Int\n    1\nend",
		"host fn now_ms() -> Int",
		"struct P\n    x: Int\nend",
		"enum Color\n    Red\n    Green\nend",
		"type Meters Int",
		"typealias Id Int",
		"interface Shape\n    fn area(value: self) -> Float\nend",
		"host type Handle",
		"impl P\n    fn get(p: P) -> Int\n        p.x\n    end\nend",
		"impl Literal for P\n    fn from_fragments(fragments: List<Fragment<String>>) -> P\n        P{x: 1}\n    end\nend",
		"derive Debug for P",
		"import std::io",
		"import\n    std::io\n    std::uuid\nend",
		"gopkg \"strings\" as strs",
		"test \"t\"\n    assert 1 == 1\nend",
		"group \"g\"\n    test \"t\"\n        assert 1 == 1\n    end\nend",
		"once x: Int = 1",
		"embed site `static`",
		replShoe,
		replShoeCount,
		"phrase hi `Hi`",
		"pub noun book `book`",
		"pub phrase hello `Hello`",
	}
	for _, src := range sources {
		nodes, err := parser.Parse(lexer.Lex(src))
		if err != nil {
			t.Errorf("%q does not parse: %v", src, err)
			continue
		}
		in, err := parseReplInput(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if len(in.stmts) > 0 {
			t.Errorf("%q (%T) is read as a statement, not a declaration", src, nodes[0])
		}
		if len(in.decls)+len(in.onces) != 1 {
			t.Errorf("%q: %d declarations and %d onces, want one", src, len(in.decls), len(in.onces))
		}
	}
	// Every node type the parser makes that a REPL statement is not would
	// be a declaration: the cases above name each, by its node type.
	seen := map[string]bool{}
	for _, src := range sources {
		nodes, _ := parser.Parse(lexer.Lex(src))
		for _, n := range nodes {
			seen[fmt.Sprintf("%T", n)] = true
		}
	}
	for _, n := range []ast.Node{&ast.FuncDef{}, &ast.ExternFunc{}, &ast.StructDef{}, &ast.EnumDef{}, &ast.TypeDef{}, &ast.TypeAlias{},
		&ast.InterfaceDef{}, &ast.ExternType{}, &ast.ImplBlock{}, &ast.ImplConformance{}, &ast.ImportStmt{}, &ast.ImportBlock{},
		&ast.ExternPackage{}, &ast.TestDecl{}, &ast.OnceBinding{}, &ast.EmbedDecl{}, &ast.KeywordDecl{}} {
		if !seen[fmt.Sprintf("%T", n)] {
			t.Errorf("no case parses to a %T", n)
		}
	}
}

// A declaration keyword the front end does not build in is a declaration
// too, and gets the front end's error for it, not one about its words.
func TestReplVM_AnUnknownDeclarationKeywordIsReportedAsOne(t *testing.T) {
	_, errOut := replRun(t, "widget w `x`\n")
	if strings.Contains(errOut, "undefined variable") || errOut == "" {
		t.Errorf("stderr:\n%s", errOut)
	}
}
