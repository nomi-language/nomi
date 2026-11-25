package analysis_test

import (
	"bytes"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestOnlyCheckStandaloneFileBuildsAFileOnItsOwn: no shipped code builds a
// single file but analysis.CheckStandaloneFile, which completes the file's
// declarations and types and checks the slice it built. A caller of the
// builder itself must complete the declarations and then check that same
// slice, and two of the three that once called it had not (the functions
// the front end writes had symbols and no types).
//
// Every non-test Go file of the module is read. The single-file builder,
// buildFileWithStdlibAtPath, may be called from CheckStandaloneFile and
// from its two exported forms, BuildFileWithStdlib and
// BuildFileWithStdlibAtPath, which tests call to look at one pass over a
// file they build; no non-test file may call those.
func TestOnlyCheckStandaloneFileBuildsAFileOnItsOwn(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate the test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	// The functions that may call each builder, by the builder's name.
	allowed := map[string][]string{
		"buildFileWithStdlibAtPath": {"CheckStandaloneFile", "BuildFileWithStdlib", "BuildFileWithStdlibAtPath"},
		"BuildFileWithStdlib":       nil,
		"BuildFileWithStdlibAtPath": nil,
	}
	calls := map[string]int{}
	var bad []string
	files := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		if !bytes.Contains(src, []byte("uildFileWithStdlib")) {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*goast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}
			goast.Inspect(fn.Body, func(n goast.Node) bool {
				call, isCall := n.(*goast.CallExpr)
				if !isCall {
					return true
				}
				var name string
				switch f := call.Fun.(type) {
				case *goast.Ident:
					name = f.Name
				case *goast.SelectorExpr:
					name = f.Sel.Name
				}
				callers, builder := allowed[name]
				if !builder {
					return true
				}
				calls[name]++
				for _, c := range callers {
					if c == fn.Name.Name && file.Name.Name == "analysis" {
						return true
					}
				}
				bad = append(bad, filepath.ToSlash(rel)+": "+fn.Name.Name+" calls "+name)
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("%s: a file checked on its own is built by analysis.CheckStandaloneFile", b)
	}
	// The walk found the builder's callers, so a clean result is not a
	// walk that read nothing: the two exported forms and
	// CheckStandaloneFile each call it once.
	if files < 100 || calls["buildFileWithStdlibAtPath"] != 3 {
		t.Errorf("read %d Go files and found %d calls of buildFileWithStdlibAtPath, want its 3 callers among the module's files", files, calls["buildFileWithStdlibAtPath"])
	}
}
