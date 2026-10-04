package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestStdlibHasNoImplCollisions freezes the stdlib audit: the standard
// library must never ship two impl blocks for the same
// (interface, type) pair. The collision check (wired into
// BuildProjectWithCache) walks the project's per-FA IfaceMethodImpls
// — populated for stdlib by std.Load() and folded into filesByKey via
// the stdlibFAs eager fold — so building even a trivial program
// exercises stdlib's impls on their own.
//
// This lives in package analysis_test, not the white-box
// impl_coherence_test.go: detectImplCollisions is unexported and the std
// package imports analysis, so package analysis cannot import std (import
// cycle). An integration test in analysis_test can import both, and
// driving the real BuildProjectWithCache pipeline tests the property
// through the actually-wired path rather than a hand-rolled call.
//
// A future stdlib edit that reintroduces a duplicate impl for one
// (interface, type) pair fails here instead of silently last-winning.
func TestStdlibHasNoImplCollisions(t *testing.T) {
	lib := std.Load()

	// Minimal valid program. Its own impl index is empty, so any
	// `duplicate impl` error that surfaces comes purely from stdlib's
	// per-FA IfaceMethodImpls (folded into filesByKey via stdlibFAs).
	src := "fn main() {}"
	tokens := lexer.Lex(src)
	entryNodes, _ := parser.ParseWithRecovery(tokens)

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		return nil, nil // trivial program imports nothing
	}
	entryFA, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, "", loader,
	)
	if entryFA == nil {
		t.Fatal("BuildProjectWithCache returned nil entry FA")
	}

	var collisions []string
	for _, e := range entryFA.TypeErrors {
		if strings.Contains(e.Error(), "duplicate impl") {
			collisions = append(collisions, e.Error())
		}
	}
	if len(collisions) > 0 {
		t.Fatalf("stdlib has %d impl collision(s):\n  %s",
			len(collisions), strings.Join(collisions, "\n  "))
	}
}

// TestProjectImplIndex_PopulatedAfterBuild asserts BuildProjectWithCache
// populates the project-level ImplIndex and broadcasts a non-nil
// back-pointer onto the entry FA: the index is populated and carries
// stdlib entries.
//
// Stdlib coverage is exercised via the prelude-promoted name Int: it
// impl Display, Debug, Equatable, Comparable, and Hashable in
// std/int. A trivial entry program that doesn't even import std/int
// reaches every one of those pairs via the stdlibFAs eager fold in
// BuildProjectWithCache, so the test asserts the fold is wired.
func TestProjectImplIndex_PopulatedAfterBuild(t *testing.T) {
	lib := std.Load()

	src := "fn main() {}"
	tokens := lexer.Lex(src)
	entryNodes, _ := parser.ParseWithRecovery(tokens)

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		return nil, nil
	}
	entryFA, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, "", loader,
	)
	if entryFA == nil {
		t.Fatal("BuildProjectWithCache returned nil entry FA")
	}
	if entryFA.ProjectImpls == nil {
		t.Fatal("entry FA's ProjectImpls back-pointer is nil; BuildProjectWithCache did not attach the index")
	}
	if len(entryFA.ProjectImpls.Impls) == 0 {
		t.Fatal("ProjectImpls.Impls is empty; the stdlibFAs eager fold did not run")
	}
	if len(entryFA.ProjectImpls.IfaceMethodImpls) == 0 {
		t.Fatal("ProjectImpls.IfaceMethodImpls is empty; per-FA IndexImplBlockFuncDefs did not contribute")
	}

	// Sanity-check a representative stdlib pair: Int impl Display.
	// If this drops out, the stdlibFAs fold or the per-FA contribution
	// path broke — both are stdlib-globals-retirement regressions.
	intIfaces, ok := entryFA.ProjectImpls.Impls["Int"]
	if !ok {
		t.Fatal("ProjectImpls.Impls missing Int")
	}
	if !intIfaces["Display"] {
		t.Error("ProjectImpls.Impls[Int] missing Display impl")
	}
}
