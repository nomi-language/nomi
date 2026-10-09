package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

const strayPromptMessage = "a `//!` attached test must start its own line"

// A `//!` after code on its line is an error, wherever it is: on its own
// line the same text is an attached-test prompt, so the formatter could not
// move it to a line of its own. Found by FuzzFormatKeepsMeaning
// (`A{a//!000000\n}`).
func TestStrayPrompt_AfterCodeIsAnError(t *testing.T) {
	cases := map[string]struct {
		src       string
		line, col int
	}{
		"after a punned field":     {"A{a//!000000\n}\n", 1, 4},
		"after a field and comma":  {"fn f(): A {\n  A{a: 1, //! note\n    b: 2}\n}\n", 2, 11},
		"after a statement":        {"fn f(): Int {\n  x = 1 //! note\n  x\n}\n", 2, 9},
		"after a block's brace":    {"fn f(): Int { //! note\n  1\n}\n", 1, 15},
		"after a declaration":      {"fn f(): Int {\n  1\n} //! assert f() == 1\n", 3, 3},
		"with nothing after it":    {"fn f(): Int {\n  1 //!\n}\n", 2, 5},
		"inside a prompt's line":   {"//! assert f() == 1 //! more\nfn f(): Int {\n  1\n}\n", 1, 21},
		"after a list's last item": {"fn f(): List<Int> {\n  [\n    1, //! note\n  ]\n}\n", 3, 8},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(c.src))
			if err == nil {
				t.Fatalf("Parse accepted a `//!` after code:\n%s", c.src)
			}
			pe, ok := err.(ParseError)
			if !ok || !strings.Contains(pe.Message, strayPromptMessage) || pe.Line != c.line || pe.Col != c.col {
				t.Fatalf("Parse error = %#v, want %q at %d:%d", err, strayPromptMessage, c.line, c.col)
			}
			_, _, err = ParseFile(lexer.Lex(c.src))
			if err == nil || !strings.Contains(err.Error(), strayPromptMessage) {
				t.Fatalf("ParseFile error = %v, want %q", err, strayPromptMessage)
			}
		})
	}
}

// The resilient parse the LSP uses reports it and keeps the declaration.
func TestStrayPrompt_ResilientParseReportsIt(t *testing.T) {
	nodes, errs, _ := ParseResilient(lexer.Lex("fn f(): Int {\n  1 //! note\n}\n"))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, strayPromptMessage) {
		t.Fatalf("ParseResilient errors = %v, want one stray-prompt error", errs)
	}
	if len(nodes) != 1 {
		t.Fatalf("ParseResilient kept %d nodes, want the function", len(nodes))
	}
}

// A prompt that starts its line, indented or not, is still an attached test,
// and a comment that only contains `//!` after other text is a comment.
func TestStrayPrompt_PromptsAndOtherCommentsStillParse(t *testing.T) {
	for _, src := range []string{
		"//! assert f() == 1\nfn f(): Int {\n  1\n}\n",
		"impl A {\n  //! assert A.f() == 1\n  fn f(): Int {\n    1\n  }\n}\nstruct A {\n  x: Int\n}\n",
		"fn f(): Int {\n  1 ////! note\n}\n",
		"fn f(): Int {\n  1 // see //! note\n}\n",
		"fn f(): Int {\n  1 //# note\n}\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse rejected:\n%s\nerror: %v", src, err)
		}
	}
}
