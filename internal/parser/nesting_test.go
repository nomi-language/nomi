package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// nestedForms are the shapes nesting can take, each built `depth` levels
// deep. Every one is a valid program at any depth below the limit except
// the unclosed ones, which a file being typed passes through.
var nestedForms = []struct {
	name string
	src  func(depth int) string
}{
	{"unclosed braces", func(d int) string { return "fn main() {\n  x = " + strings.Repeat("{", d) + "\n  todo\n" }},
	{"braces", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("{", d) + "1" + strings.Repeat("}", d) + "\n}\n"
	}},
	{"blocks over lines", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("{\n", d) + "1\n" + strings.Repeat("}\n", d) + "}\n"
	}},
	{"parentheses", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("(", d) + "1" + strings.Repeat(")", d) + "\n}\n"
	}},
	{"unclosed parentheses", func(d int) string { return "fn main() {\n  _x = " + strings.Repeat("(", d) + "\n  todo\n" }},
	{"lists", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("[", d) + "1" + strings.Repeat("]", d) + "\n}\n"
	}},
	{"unclosed lists", func(d int) string { return "fn main() {\n  _x = " + strings.Repeat("[", d) + "\n  todo\n" }},
	{"lambdas", func(d int) string { return "fn main() {\n  _x = " + strings.Repeat("|| ", d) + "1\n}\n" }},
	{"ifs", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("if True { ", d) + "1" + strings.Repeat(" } else { 2 }", d) + "\n}\n"
	}},
	{"negations", func(d int) string { return "fn main() {\n  _x = " + strings.Repeat("-", d) + "1\n}\n" }},
	{"interpolations", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat(`"${`, d) + "1" + strings.Repeat(`}"`, d) + "\n}\n"
	}},
	{"anonymous structs", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("{a: ", d) + "1" + strings.Repeat("}", d) + "\n}\n"
	}},
	{"maps", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("{1 => ", d) + "1" + strings.Repeat("}", d) + "\n}\n"
	}},
	{"map keys", func(d int) string {
		return "fn main() {\n  _x = " + strings.Repeat("{", d) + "1" + strings.Repeat(" => 1}", d) + "\n}\n"
	}},
	{"map patterns", func(d int) string {
		return "fn main() {\n  " + strings.Repeat("{1 => ", d) + "x" + strings.Repeat("}", d) + " = todo\n}\n"
	}},
	{"tuple patterns", func(d int) string {
		return "fn main() {\n  " + strings.Repeat("(", d) + "a, b" + strings.Repeat(", c)", d) + " = todo\n}\n"
	}},
	{"types", func(d int) string {
		return "fn f(_x: " + strings.Repeat("List<", d) + "Int" + strings.Repeat(">", d) + ") {\n}\n"
	}},
}

// Every nested form past MaxNesting is a parse error naming the limit, in
// the strict parse and in the editor's resilient one.
func TestNesting_PastTheLimitIsAnError(t *testing.T) {
	want := fmt.Sprintf("nesting deeper than %d levels", MaxNesting)
	for _, form := range nestedForms {
		t.Run(form.name, func(t *testing.T) {
			src := form.src(MaxNesting + 50)
			_, errs := ParseWithRecovery(lexer.Lex(src))
			if !hasError(errs, want) {
				t.Errorf("strict parse: no %q among %v", want, errs)
			}
			_, errs, _ = ParseResilient(lexer.Lex(src))
			if !hasError(errs, want) {
				t.Errorf("resilient parse: no %q among %v", want, errs)
			}
			// The parser tries other readings of a construct that fails,
			// and past the limit each of them descended to the limit
			// again: nested map keys took four million expression parses.
			for _, resilient := range []bool{false, true} {
				p := &Parser{tokens: lexer.Lex(src), resilient: resilient}
				p.parseWithRecovery()
				if p.exprParses > 10*MaxNesting {
					t.Errorf("resilient=%v: %d expression parses", resilient, p.exprParses)
				}
			}
		})
	}
}

// Below the limit, each form parses without that error, and the parser's
// work grows linearly with the depth. Counted as expression parses started,
// speculative ones included: a `{` probes for a map entry by parsing the
// expression after it, and that probe once made each level of nested
// braces double the work, so 20 levels took a minute.
func TestNesting_WorkIsLinearInTheDepth(t *testing.T) {
	limit := fmt.Sprintf("nesting deeper than %d levels", MaxNesting)
	for _, form := range nestedForms {
		t.Run(form.name, func(t *testing.T) {
			parses := func(depth int, resilient bool) int {
				p := &Parser{tokens: lexer.Lex(form.src(depth)), resilient: resilient}
				_, errs := p.parseWithRecovery()
				if hasError(errs, limit) {
					t.Fatalf("depth %d reports the nesting limit: %v", depth, errs)
				}
				return p.exprParses
			}
			for _, resilient := range []bool{false, true} {
				// Kept shallow so that a regression fails instead of hanging.
				// Twice the depth may cost a little over twice the parses (a
				// constant per level plus the program around it); doubling
				// per level would be 64 times.
				small, large := parses(6, resilient), parses(12, resilient)
				if large > 3*small {
					t.Fatalf("resilient=%v: %d expression parses at depth 6, %d at depth 12", resilient, small, large)
				}
				if deep := parses(MaxNesting-10, resilient); deep > 100*MaxNesting {
					t.Errorf("resilient=%v: %d expression parses at depth %d", resilient, deep, MaxNesting-10)
				}
			}
		})
	}
}

func hasError(errs []ParseError, want string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return true
		}
	}
	return false
}
