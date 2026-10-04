package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestResolveStdlibImport_WriteThroughCache asserts that resolving a
// stdlib import via the b.modules fast-path now also populates
// b.cache. Pre-write-through (the four-globals era), cache was empty
// for stdlib paths when only the prelude-implicit chain pulled stdlib
// in; post-write-through, every reachable stdlib FA is in cache so
// the project impl index assembled in BuildProjectWithCache sees it.
//
// The trivial user program does no explicit `import std/X`. The
// auto-prepended prelude chain reaches std/int (and others) via
// resolveStdlibImport's b.modules fast-path — the exact site this
// task makes write-through.
func TestResolveStdlibImport_WriteThroughCache(t *testing.T) {
	src := "fn main() { x = 1 + 2 }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)

	lib := std.Load()
	_, cache, _ := analysis.BuildProjectWithCache(
		nodes, lib.Primitives, lib.Modules, lib.Files, "", nil,
	)

	var stdKeys []string
	for k := range cache {
		if strings.HasPrefix(k, "std/") {
			stdKeys = append(stdKeys, k)
		}
	}
	if len(stdKeys) == 0 {
		allKeys := make([]string, 0, len(cache))
		for k := range cache {
			allKeys = append(allKeys, k)
		}
		t.Fatalf("expected at least one std/* key in cache; got %d total keys: %v", len(cache), allKeys)
	}
}
