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

// maxLocalReadSites is how many distinct source sites of each known way a
// read's binding, found by its name, and the declaration the checker
// resolved the read to differ (verifyLocalRead, knownLocalReadClasses) the
// programs TestIRGolden_RecordsMatchTheVM runs reach. Each count only goes
// down. Every class's cause is in knownLocalReadClasses; a binding keyed by
// its declaration, as the checker keys it, removes all four. A refinement
// rule's literal is read so only in std's own lowerings, which the census
// does not count, so the count is the same with the artifact cache empty,
// warm or off.
var maxLocalReadSites = map[localReadClass]int{
	localReadCallSiteDefault:       6,
	localReadRefinementLiteral:     0,
	localReadDestructuredParameter: 1,
	localReadNestedSelf:            1,
}

// localReadRatchet holds the local read census to maxLocalReadSites:
// every class knownLocalReadClasses lists.
func localReadRatchet() censusRatchet {
	r := censusRatchet{what: "local reads", held: map[string]bool{}, max: map[string]int{},
		fix: "read the binding the checker resolved"}
	for class := range knownLocalReadClasses {
		r.held[string(class)] = true
	}
	for class, n := range maxLocalReadSites {
		r.max[string(class)] = n
	}
	return r
}

// A read whose binding and reference differ in a way no listed class
// covers fails the lowering, naming the read's position and both answers.
// With the class listed, the same program lowers.
func TestVerifyLocalRead_AnUnlistedDifferenceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `fn add(a: Int, b: Int = a + 1) -> Int
  a + b
end

fn main()
  dbg add(2)
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	lower := func() (failure string) {
		defer func() {
			if r := recover(); r != nil {
				failure = fmt.Sprint(r)
			}
		}()
		if _, _, err := GenerateIR(p); err != nil {
			return err.Error()
		}
		return ""
	}
	if failure := lower(); failure != "" {
		t.Fatalf("the program does not lower with every class listed: %s", failure)
	}
	cause := knownLocalReadClasses[localReadCallSiteDefault]
	delete(knownLocalReadClasses, localReadCallSiteDefault)
	defer func() { knownLocalReadClasses[localReadCallSiteDefault] = cause }()
	want := "main.nomi:1:25: the builder reads `a` from no binding of a body, and the checker resolves it to the binding declared at 1:8"
	if failure := lower(); !strings.Contains(failure, want) {
		t.Fatalf("lowering with the class unlisted failed with %q, want it to name %q", failure, want)
	}
}

// maxSpellingResolutions is how many places of this package resolve a name
// by its spelling: calls of moduleScopeOf (the file a name binds in a
// file's module scope), of moduleScope (the same, a block's imports
// first) and of boundBySpelling (the builder's own bindings of a name).
// What a written name names is the checker's reference at it (identRef);
// the three value-name sites left are the fallbacks for an identifier the
// builder writes (scopeOfRef, namesLocal) and the bare callee
// ownModuleCall respells, and the rest resolve a type's qualifier written
// as a string (`json.Json` in a type, a dotted name). The count only goes
// down.
const maxSpellingResolutions = 16

// TestSpellingResolutionsOnlyGoDown counts the places that resolve a name
// by its spelling and fails when the count rises, or falls without its
// maximum following it.
func TestSpellingResolutionsOnlyGoDown(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	resolvers := map[string]bool{"moduleScopeOf": true, "moduleScope": true, "boundBySpelling": true}
	fset := token.NewFileSet()
	var sites []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
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
			if resolvers[callee] {
				sites = append(sites, fmt.Sprintf("%s: %s", fset.Position(call.Pos()), callee))
			}
			return true
		})
	}
	switch {
	case len(sites) > maxSpellingResolutions:
		t.Errorf("%d places resolve a name by its spelling, and at most %d may: read the checker's reference at the name (identRef):\n  %s",
			len(sites), maxSpellingResolutions, strings.Join(sites, "\n  "))
	case len(sites) < maxSpellingResolutions:
		t.Errorf("%d places resolve a name by its spelling: lower maxSpellingResolutions to %d", len(sites), len(sites))
	}
}
