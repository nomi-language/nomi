package irbuild

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A callee's default lowered at a call site reads the callee's earlier
// parameter, and a recursive nested fn reads its own name in its body.
// Each read finds its value by the declaration the checker resolved it to
// (readKey), which the body holds, so the program lowers.
func TestLocalRead_ADefaultAndANestedFnReadTheirDeclarations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `fn add(a: Int, b: Int = a + 1) -> Int
  a + b
end

fn main()
  fn count(n: Int) -> Int
    if n == 0 then 0 else count(n - 1) + 1
  end
  dbg add(2)
  dbg count(3)
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	failure := func() (failure string) {
		defer func() {
			if r := recover(); r != nil {
				failure = fmt.Sprint(r)
			}
		}()
		if _, _, err := GenerateIR(p); err != nil {
			return err.Error()
		}
		return ""
	}()
	if failure != "" {
		t.Fatalf("the program does not lower: %s", failure)
	}
}

// maxSpellingResolutions is how many places of this package resolve a name
// by its spelling: calls of moduleScopeOf (the file a name binds in a
// file's module scope) outside builderSpellings. What a written name names
// is the checker's reference at it (identRef), and a local is read by its
// key (readKey): none.
const maxSpellingResolutions = 0

// builderSpellings are the functions whose spelling resolution resolves a
// name the builder writes, never one the program does, with why.
var builderSpellings = map[string]string{
	"scopeOfRef": "a qualifier with no reference, which only an identifier the builder writes is (madeIdent)",
}

// TestSpellingResolutionsOnlyGoDown counts the places that resolve a name
// by its spelling and fails when the count rises, or falls without its
// maximum following it.
func TestSpellingResolutionsOnlyGoDown(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	resolvers := map[string]bool{"moduleScopeOf": true}
	fset := token.NewFileSet()
	var sites []string
	builderSites := map[string]int{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		fn := ""
		ast.Inspect(file, func(n ast.Node) bool {
			if fd, isFunc := n.(*ast.FuncDecl); isFunc {
				fn = fd.Name.Name
			}
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			var callee string
			switch f := call.Fun.(type) {
			case *ast.Ident:
				callee = f.Name
			case *ast.SelectorExpr:
				callee = f.Sel.Name
			}
			if _, builder := builderSpellings[fn]; resolvers[callee] && builder {
				builderSites[fn]++
			} else if resolvers[callee] {
				sites = append(sites, fmt.Sprintf("%s: %s", fset.Position(call.Pos()), callee))
			}
			return true
		})
	}
	for fn, why := range builderSpellings {
		if builderSites[fn] != 1 {
			t.Errorf("%s resolves %d names by their spelling and builderSpellings lists one (%s)", fn, builderSites[fn], why)
		}
	}
	switch {
	case len(sites) > maxSpellingResolutions:
		t.Errorf("%d places resolve a name by its spelling, and at most %d may: read the checker's reference at the name (identRef):\n  %s",
			len(sites), maxSpellingResolutions, strings.Join(sites, "\n  "))
	case len(sites) < maxSpellingResolutions:
		t.Errorf("%d places resolve a name by its spelling: lower maxSpellingResolutions to %d", len(sites), len(sites))
	}
}
