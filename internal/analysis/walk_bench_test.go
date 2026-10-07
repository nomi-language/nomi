package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// stdTrees parses every top-level std/*.nomi file, the largest body of Nomi
// in the repository, for the walk benchmarks.
func stdTrees(b *testing.B) [][]ast.Node {
	b.Helper()
	paths, err := filepath.Glob("../../std/*.nomi")
	if err != nil || len(paths) == 0 {
		b.Fatalf("no std files: %v", err)
	}
	var trees [][]ast.Node
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			b.Fatal(err)
		}
		nodes, err := parser.Parse(lexer.Lex(string(src)))
		if err != nil {
			b.Fatalf("%s: %v", p, err)
		}
		trees = append(trees, nodes)
	}
	return trees
}

// BenchmarkWalkNodes visits every node of the stdlib's syntax trees.
func BenchmarkWalkNodes(b *testing.B) {
	trees := stdTrees(b)
	b.ReportAllocs()
	b.ResetTimer()
	count := 0
	for range b.N {
		count = 0
		for _, nodes := range trees {
			for _, n := range nodes {
				WalkNodes(n, func(ast.Node) { count++ })
			}
		}
	}
	b.ReportMetric(float64(count), "nodes")
}

// BenchmarkCheckUselessReturns runs the useless-return sweep over the
// stdlib's syntax trees.
func BenchmarkCheckUselessReturns(b *testing.B) {
	trees := stdTrees(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, nodes := range trees {
			CheckUselessReturns(nodes)
		}
	}
}
