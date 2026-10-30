package analysis_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/virtualproject"
)

// A file's analysis is a declaration part, kept across an edit that changes
// only bodies, and a body part, thrown away and rebuilt. These tests prove
// the line between them is clean: a checked program whose files' body parts
// are rebuilt one after another, each against the kept declarations and the
// other files as they are, dumps exactly as a full check of the same texts
// (analysis.DumpProgramForTest). A fact the declarations lose, a body fact
// left behind, or a body fact written into the declarations shows in the
// dump.

// rebuildEachFile checks p, then rebuilds the body part of each of its files
// in turn, the entry last and then once more, and after each rebuild compares
// the program's dump with a full check's. With fresh set, each file's bodies
// are first replaced by those of a new parse of its text (graftBodies), as
// an edit to a body would replace them. It answers the first difference as
// readable text, or "".
func rebuildEachFile(tb testing.TB, p incrementalProject, fresh bool) string {
	tb.Helper()
	want := analysis.DumpProgramForTest(fullCheck(p, p.texts))
	prog := fullCheck(p, p.texts)
	keys := prog.Keys()
	order := append(append(keys[1:len(keys):len(keys)], keys[0]), keys[0])
	for i, key := range order {
		if fresh {
			nodes, _, _ := parser.ParseResilient(lexer.Lex(prog.Texts[key]))
			graftBodies(prog.Nodes[key], nodes)
		}
		if !analysis.RebuildBodiesForTest(prog, key) {
			tb.Fatalf("%s: the program kept no declarations for %s", p.name, key)
		}
		if got := analysis.DumpProgramForTest(prog); got != want {
			return fmt.Sprintf("%s: after rebuilding the bodies of %s (rebuild %d of %d) the program differs from a full check:\n%s",
				p.name, key, i+1, len(order), dumpDiff(want, got))
		}
	}
	return ""
}

// graftBodies gives the declarations in nodes the bodies of the same
// declarations, by kind and position, in fresh, a new parse of the same
// text: each function's body and attached tests, a test's body, clock, boot,
// setup and context pattern, and the same for the functions an impl block or
// a type's namespace holds. Declarations synthesized from others have no
// counterpart in fresh and keep theirs.
func graftBodies(nodes, fresh []ast.Node) {
	type at struct {
		kind      string
		line, col int
	}
	key := func(n ast.Node) (at, bool) {
		switch d := n.(type) {
		case *ast.FuncDef:
			return at{"fn", d.Line, d.Col}, true
		case *ast.TestDecl:
			return at{"test", d.Line, d.Col}, true
		case *ast.ImplBlock:
			return at{"impl", d.Line, d.Col}, true
		case *ast.StructDef:
			return at{"struct", d.Line, d.Col}, true
		case *ast.EnumDef:
			return at{"enum", d.Line, d.Col}, true
		case *ast.TypeDef:
			return at{"type", d.Line, d.Col}, true
		}
		return at{}, false
	}
	byKey := map[at]ast.Node{}
	for _, n := range fresh {
		if k, ok := key(n); ok {
			byKey[k] = n
		}
	}
	tests := func(old, new []ast.AttachedTest) {
		if len(old) == len(new) {
			for i := range old {
				old[i].Body = new[i].Body
			}
		}
	}
	for _, n := range nodes {
		k, ok := key(n)
		if !ok {
			continue
		}
		f, ok := byKey[k]
		if !ok {
			continue
		}
		switch d := n.(type) {
		case *ast.FuncDef:
			nf := f.(*ast.FuncDef)
			d.Body = nf.Body
			tests(d.AttachedTests, nf.AttachedTests)
		case *ast.TestDecl:
			nt := f.(*ast.TestDecl)
			d.Body, d.Clock, d.Boot, d.Setup, d.ContextPattern = nt.Body, nt.Clock, nt.Boot, nt.Setup, nt.ContextPattern
		case *ast.ImplBlock:
			graftBodies(d.Items, f.(*ast.ImplBlock).Items)
		case *ast.StructDef:
			graftBodies(d.Items, f.(*ast.StructDef).Items)
		case *ast.EnumDef:
			graftBodies(d.Items, f.(*ast.EnumDef).Items)
		case *ast.TypeDef:
			graftBodies(d.Items, f.(*ast.TypeDef).Items)
		}
	}
}

// TestRebuildBodiesMatchesFull rebuilds the bodies of every file of every
// multi-file corpus project, every single-file corpus program and
// orderProject.
func TestRebuildBodiesMatchesFull(t *testing.T) {
	projects := []incrementalProject{orderIncremental()}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	projects = append(projects, corpus...)
	singles, err := filepath.Glob(filepath.Join("..", "..", "tests", "*", "*.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(singles) < 50 {
		t.Fatalf("found %d single-file corpus programs, want at least 50", len(singles))
	}
	for _, path := range singles {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(path), "../../tests/")
		key := strings.TrimSuffix(filepath.Base(path), ".nomi")
		projects = append(projects, incrementalProject{name: name, entry: key, texts: map[string]string{key: string(src)}})
	}
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			for _, fresh := range []bool{false, true} {
				if diff := rebuildEachFile(t, p, fresh); diff != "" {
					t.Fatalf("fresh bodies %v: %s", fresh, diff)
				}
			}
		})
	}
}

// markedProject is the project a doc block's `// FILE:` markers describe,
// or the block alone as main.
func markedProject(tb testing.TB, name, code string) incrementalProject {
	tb.Helper()
	vp, err := virtualproject.FromMarkedSource(code)
	if err != nil {
		tb.Fatalf("%s: %v", name, err)
	}
	entry := strings.TrimSuffix(vp.EntryName, ".nomi")
	p := incrementalProject{name: name, entry: entry, texts: map[string]string{entry: vp.EntrySource}}
	for file, src := range vp.VirtualFiles {
		if strings.HasSuffix(file, ".nomi") {
			p.texts[strings.TrimSuffix(file, ".nomi")] = src
		}
	}
	return p
}

// TestRebuildBodiesMatchesFull_TourAndSpec rebuilds the bodies of every file
// of every runnable tour block and every complete program in the spec.
func TestRebuildBodiesMatchesFull_TourAndSpec(t *testing.T) {
	var projects []incrementalProject
	root := filepath.Join("..", "..", "tour", "src", "content", "docs")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (!strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".mdx")) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			if b.Runs() {
				projects = append(projects, markedProject(t, fmt.Sprintf("tour/%s:L%d", filepath.Base(path), b.Line), b.Code))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tour := len(projects)
	if tour < 100 {
		t.Fatalf("found %d runnable tour blocks, want at least 100", tour)
	}
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	specMain := regexp.MustCompile(`(?m)^fn main\b`)
	for _, b := range doctest.ExtractBlocks(string(spec), "nomi") {
		if specMain.MatchString(b.Code) && !strings.Contains(b.Code, "...") {
			projects = append(projects, markedProject(t, fmt.Sprintf("spec:L%d", b.Line), b.Code))
		}
	}
	if len(projects)-tour < 10 {
		t.Fatalf("found %d complete spec programs, want at least 10", len(projects)-tour)
	}
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			for _, fresh := range []bool{false, true} {
				if diff := rebuildEachFile(t, p, fresh); diff != "" {
					t.Fatalf("fresh bodies %v: %s", fresh, diff)
				}
			}
		})
	}
}
