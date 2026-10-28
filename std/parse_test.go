package std

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestEmbeddedStdlibParses holds every embedded .nomi file to the strict
// parser. Load parses with recovery, so a syntax error in a std file would
// otherwise drop the declaration recovery skipped and surface only as what
// the checker then says about it ("undefined variable"). It also checks
// that Load recorded no syntax errors (StdLib.SyntaxErrors), which the front
// end and the language server report ahead of anything else.
func TestEmbeddedStdlibParses(t *testing.T) {
	err := fs.WalkDir(stdlibFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isEmbeddedFile(path) {
			return err
		}
		data, err := stdlibFS.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := parser.Parse(lexer.Lex(string(data))); err != nil {
			var pe parser.ParseError
			if errors.As(err, &pe) {
				t.Errorf("std/%s:%d:%d: %s", path, pe.Line, pe.Col, pe.Message)
			} else {
				t.Errorf("std/%s: %v", path, err)
			}
			return nil
		}
		if _, errs := parser.ParseWithRecovery(lexer.Lex(string(data))); len(errs) > 0 {
			t.Errorf("std/%s:%d:%d: the recovering parser reports %q, which the strict parser accepts",
				path, errs[0].Line, errs[0].Col, errs[0].Message)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Shared().SyntaxError(); err != nil {
		t.Errorf("Load recorded syntax errors: %v", err)
	}
}

// TestLoadRecordsAStdSyntaxError checks the loader Load uses hands an
// embedded module's syntax errors to Load, keyed by module name, and that
// SyntaxError names each at its std file, line and column.
func TestLoadRecordsAStdSyntaxError(t *testing.T) {
	got := map[string][]parser.ParseError{}
	loader := makeLoader(nil, func(name string, errs []parser.ParseError) {
		if len(errs) > 0 {
			got[name] = errs
		}
	})
	if _, err := loader("", []string{"std", "io"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("std/io parses, but the loader reported %v", got)
	}

	_, errs := parser.ParseWithRecovery(lexer.Lex("fn f() -> Bool {\n  True\n    ) False\n}\n"))
	if len(errs) == 0 {
		t.Fatal("the parser accepts a line that starts with `)`; pick another syntax error")
	}
	lib := &StdLib{SyntaxErrors: map[string][]parser.ParseError{"io": errs}}
	err := lib.SyntaxError()
	if err == nil {
		t.Fatal("SyntaxError answered nil for a module with a syntax error")
	}
	want := "std/io.nomi:3:5: "
	if !containsLine(err.Error(), want) {
		t.Errorf("SyntaxError = %q, want a line starting %q", err, want)
	}
}

func containsLine(text, prefix string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
