package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A `#!` first line is not part of the program: the file parses as if the
// line were blank, and declarations after it keep their source lines.
func TestParse_ShebangFirstLineIsIgnored(t *testing.T) {
	nodes, err := Parse(lexer.Lex("#!/usr/bin/env nomi\nimport std/io\n\nfn main() {\n    io.print(1)\n}\n"))
	if err != nil {
		t.Fatalf("Parse rejected a file with a shebang: %v", err)
	}
	if len(nodes) != 2 || nodes[0].LineNum() != 2 || nodes[1].LineNum() != 4 {
		t.Fatalf("got %d nodes at lines %v, want the import at 2 and main at 4", len(nodes), lineNums(nodes))
	}
}

// `#!` that does not start the file is an error that says where it may go.
func TestParse_ShebangAfterFirstByteIsRejected(t *testing.T) {
	const want = "a `#!` line is allowed only as the first line of a file"
	cases := map[string]struct {
		src       string
		line, col int
	}{
		"second line":    {"import std/io\n#!/usr/bin/env nomi\n", 2, 1},
		"after a blank":  {"\n#!/usr/bin/env nomi\n", 2, 1},
		"leading space":  {" #!/usr/bin/env nomi\n", 1, 2},
		"inside a block": {"fn main() {\n    #!/usr/bin/env nomi\n}\n", 2, 5},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("Parse accepted a misplaced #! line:\n%s", tc.src)
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("Parse error = %q, want it to say %q", err, want)
			}
			if pe, ok := err.(ParseError); !ok || pe.Line != tc.line || pe.Col != tc.col {
				t.Fatalf("Parse error = %#v, want it at %d:%d", err, tc.line, tc.col)
			}
		})
	}
}

func lineNums(nodes []ast.Node) []int {
	out := make([]int, len(nodes))
	for i, n := range nodes {
		out[i] = n.LineNum()
	}
	return out
}
