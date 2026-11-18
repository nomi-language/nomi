package irbuild

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// projectExempt are the kinds of analysis.Type that project answers with
// no arm of its own, each with why no recorded type the builder projects
// is one.
var projectExempt = map[string]string{
	"PartialType":   "a type the checker could only partly resolve, reported as an error, so no checked program records one",
	"AliasTemplate": "a generic alias's template, which every use instantiates to its target, so no position has it as its type",
}

// TestProjectNamesEveryTypeKind holds project, the builder's one
// conversion of a checker type to a kind (typeOf, a pattern's kind, an
// inferred position's), to the closed set of analysis.Type kinds: every
// type with a typeTag method has a case in project's switch or a reason in
// projectExempt. It is analysis's TestTypeChildrenNamesEveryKind for the
// builder.
func TestProjectNamesEveryTypeKind(t *testing.T) {
	fset := token.NewFileSet()
	parse := func(glob string) []*ast.File {
		paths, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		var files []*ast.File
		for _, p := range paths {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		return files
	}
	var kinds []string
	for _, f := range parse("../analysis/*.go") {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "typeTag" || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			if star, ok := fd.Recv.List[0].Type.(*ast.StarExpr); ok {
				kinds = append(kinds, star.X.(*ast.Ident).Name)
			}
		}
	}
	sort.Strings(kinds)
	if len(kinds) < 15 {
		t.Fatalf("found %d kinds of analysis.Type (%v): the scan for typeTag methods is broken", len(kinds), kinds)
	}
	named := map[string]bool{}
	for _, f := range parse("*.go") {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "project" || fd.Recv == nil || fd.Body == nil {
				continue
			}
			for _, st := range fd.Body.List {
				ts, ok := st.(*ast.TypeSwitchStmt)
				if !ok {
					continue
				}
				for _, c := range ts.Body.List {
					for _, e := range c.(*ast.CaseClause).List {
						if star, ok := e.(*ast.StarExpr); ok {
							if sel, ok := star.X.(*ast.SelectorExpr); ok {
								named[sel.Sel.Name] = true
							}
						}
					}
				}
			}
		}
	}
	if len(named) == 0 {
		t.Fatal("found no type switch in gen.project: the scan is broken")
	}
	for _, k := range kinds {
		_, exempt := projectExempt[k]
		switch {
		case named[k] && exempt:
			t.Errorf("project has a case for *analysis.%s and projectExempt says it needs none: drop one", k)
		case !named[k] && !exempt:
			t.Errorf("project has no case for *analysis.%s: give it one, or say in projectExempt why no recorded type is one", k)
		}
	}
	for k := range projectExempt {
		if !slices.Contains(kinds, k) {
			t.Errorf("projectExempt names %s, which is no kind of analysis.Type", k)
		}
	}
}
