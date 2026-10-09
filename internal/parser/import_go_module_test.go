package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `go` is no keyword in an import path: a file `go.nomi` is a module like any
// other, and the flat form imports it as the block form does. `import { go }`
// parsed while `import go` was the Go-package-handle error, so `nomi fmt`,
// which writes a one-entry block flat, broke the file. Found by
// FuzzFormatKeepsMeaning.
func TestImportOfModuleNamedGo(t *testing.T) {
	for _, tc := range []struct{ flat, block string }{
		{"import go\n", "import {\n  go\n}\n"},
		{"import go.hi\n", "import {\n  go.hi\n}\n"},
		{"import go as g\n", "import {\n  go as g\n}\n"},
		{"import go.{hi, Lo}\n", "import {\n  go.{hi, Lo}\n}\n"},
		{"import a/go\n", "import {\n  a/go\n}\n"},
	} {
		flat, err := Parse(lexer.Lex(tc.flat))
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.flat, err)
			continue
		}
		block, err := Parse(lexer.Lex(tc.block))
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.block, err)
			continue
		}
		stmt, ok := flat[0].(*ast.ImportStmt)
		if !ok {
			t.Errorf("Parse(%q) = %T, want *ast.ImportStmt", tc.flat, flat[0])
			continue
		}
		entries := block[0].(*ast.ImportBlock).Entries
		if len(entries) != 1 {
			t.Errorf("Parse(%q) has %d entries, want 1", tc.block, len(entries))
			continue
		}
		if got, want := importShape(stmt), importShape(entries[0]); got != want {
			t.Errorf("%q parses as %s, %q as %s", tc.flat, got, tc.block, want)
		}
	}
}

func importShape(s *ast.ImportStmt) string {
	var parts []string
	for _, n := range s.ModulePath {
		parts = append(parts, ast.ImportNodeName(n))
	}
	out := strings.Join(parts, "/")
	for _, n := range s.Names {
		out += " ." + ast.ImportNodeName(n)
	}
	if s.ModuleAlias != nil {
		out += " as " + ast.ImportNodeName(s.ModuleAlias)
	}
	return out
}

// The old Go package handle spellings stay the error that names `gopkg`, in
// both forms.
func TestImportGoPackageHandleIsTheGopkgError(t *testing.T) {
	for _, entry := range []string{`go "net/http"`, `go http "net/http"`, `go _ "net/http"`} {
		for _, src := range []string{"import " + entry + "\n", "import {\n  " + entry + "\n}\n"} {
			_, err := Parse(lexer.Lex(src))
			if err == nil || !strings.Contains(err.Error(), "gopkg") {
				t.Errorf("Parse(%q) error = %v, want the `gopkg` error", src, err)
			}
		}
	}
}
