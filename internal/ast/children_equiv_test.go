package ast_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/projectgen"
)

// TestChildrenMatchTheReflectiveWalk holds the generated walk to the
// reflective one it was generated from: for every node of every tree the
// repository's sources parse to (every tracked .nomi file, the corpus and
// the stdlib among them, every `nomi` and `nomi-run` block of the spec, the
// style guide and the tour, and a generated project of about 10k lines),
// Children answers the same nodes in the same order as ReflectChildren,
// and the generated switch knows the node's type. Diagnostics are reported
// in walk order, so an order that differs would move golden files.
func TestChildrenMatchTheReflectiveWalk(t *testing.T) {
	sources := walkSources(t)
	nodes, types := 0, map[string]bool{}
	for _, src := range sources {
		tops, _ := parser.ParseWithRecovery(lexer.Lex(src.text))
		for _, top := range tops {
			var visit func(n ast.Node)
			visit = func(n ast.Node) {
				nodes++
				types[n.NodeType()] = true
				var gen, ref []ast.Node
				if !ast.GeneratedChildren(n, func(c ast.Node) { gen = append(gen, c) }) {
					t.Fatalf("%s: the generated walk does not know %T; run `go generate ./internal/ast`", src.name, n)
				}
				ast.ReflectChildren(n, func(c ast.Node) { ref = append(ref, c) })
				if !sameNodes(gen, ref) {
					t.Fatalf("%s: line %d: the walks of a %s differ:\ngenerated  %s\nreflective %s",
						src.name, n.LineNum(), n.NodeType(), describe(gen), describe(ref))
				}
				for _, c := range ref {
					visit(c)
				}
			}
			if top != nil && !reflect.ValueOf(top).IsNil() {
				visit(top)
			}
		}
	}
	if len(sources) < 500 || nodes < 100_000 {
		t.Fatalf("only %d sources and %d nodes were walked; the inputs went missing", len(sources), nodes)
	}
	t.Logf("%d sources, %d nodes of %d node types", len(sources), nodes, len(types))
}

func sameNodes(a, b []ast.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if reflect.ValueOf(a[i]).Kind() == reflect.Pointer {
			if a[i] != b[i] {
				return false
			}
		} else if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func describe(ns []ast.Node) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprintf("%s@%p", n.NodeType(), n)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

type walkSource struct{ name, text string }

func walkSources(t *testing.T) []walkSource {
	t.Helper()
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	root := strings.TrimSpace(string(top))
	list, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.nomi").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var out []walkSource
	for _, rel := range strings.Split(string(list), "\x00") {
		if rel == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		out = append(out, walkSource{rel, string(data)})
	}
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	chapters, err := filepath.Glob(filepath.Join(root, "tour", "src", "content", "docs", "*.md*"))
	if err != nil {
		t.Fatal(err)
	}
	docs = append(docs, chapters...)
	sort.Strings(docs)
	for _, path := range docs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, lang := range []string{"nomi", "nomi-run"} {
			for _, b := range doctest.ExtractBlocks(string(data), lang) {
				out = append(out, walkSource{fmt.Sprintf("%s:L%d", filepath.Base(path), b.Line), b.Code + "\n"})
			}
		}
	}
	proj := projectgen.Generate(projectgen.Config{Seed: 1, Lines: 10_000})
	keys := make([]string, 0, len(proj.Files))
	for k := range proj.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, walkSource{"generated:" + k, proj.Files[k]})
	}
	return out
}
