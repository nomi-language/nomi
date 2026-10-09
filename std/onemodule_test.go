package std

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
)

// stdlibImportEntries returns every import entry declared at the top level of
// every embedded stdlib source file, paired with the file it was written in.
//
// It walks the parser's nodes rather than the source lines, because most
// entries are inside a brace block rather than standalone `import x`
// statements, and a line-oriented reading misses them:
//
//	import {
//	  iter.{Iter}
//	  maps
//	}
//
// A `^import\s+(\S+)` pattern sees only the standalone statements.
// ImportBlock is a separate AST node whose Entries are
// *ImportStmt with the shape they would have standalone, so flattening it here
// is what makes the walk complete.
//
// Attached-test (`//!`) imports are excluded by construction: they hang off a
// declaration as AttachedTest bodies, not off the file's top level. That is
// correct rather than convenient — an attached test is a separate program and
// imports `std/compiler` the way any program outside the stdlib does.
func stdlibImportEntries(t *testing.T) map[string][]*ast.ImportStmt {
	t.Helper()
	out := map[string][]*ast.ImportStmt{}
	err := fs.WalkDir(stdlibFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isEmbeddedStdlibSource(p) {
			return err
		}
		data, readErr := stdlibFS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		for _, n := range nodes {
			switch v := n.(type) {
			case *ast.ImportStmt:
				out[p] = append(out[p], v)
			case *ast.ImportBlock:
				out[p] = append(out[p], v.Entries...)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded stdlib: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no stdlib import entries found; the measurement would be vacuous")
	}
	return out
}

func importPathOf(imp *ast.ImportStmt) string {
	segs := make([]string, len(imp.ModulePath))
	for i, seg := range imp.ModulePath {
		segs[i] = ast.ImportNodeName(seg)
	}
	return strings.Join(segs, "/")
}

// TestStdlibNamesItsSiblingsBare is the surface half of "the stdlib is one Nomi
// module": inside it, a sibling is named bare.
//
// A `std/`-spelled sibling would resolve, because the compiler qualifies the
// bare form rather than rejecting the qualified one — which is exactly why this
// has to be a test and cannot be left to the loader failing. It would also be
// a second module key for one file if any consumer took the spelling at face
// value, and the tree has already paid for that once: the runtime derived a
// stdlib module key one way while std.Load derived it another, `nomi test
// std/calendar` loaded one file under two keys, and 26 attached tests failed
// with "expected Date, got Date — same name, different declarations".
func TestStdlibNamesItsSiblingsBare(t *testing.T) {
	entries := stdlibImportEntries(t)
	total := 0
	var qualified []string
	for file, imps := range entries {
		for _, imp := range imps {
			total++
			p := importPathOf(imp)
			if strings.HasPrefix(p, "std/") || p == "std" {
				qualified = append(qualified, file+": import "+p)
			}
		}
	}
	sort.Strings(qualified)
	if len(qualified) > 0 {
		t.Errorf("%d stdlib import(s) name a sibling with the `std/` prefix; the stdlib is one "+
			"module and names its siblings bare:\n  %s", len(qualified), strings.Join(qualified, "\n  "))
	}
	t.Logf("%d top-level import entries across %d stdlib files, %d std-qualified",
		total, len(entries), len(qualified))
}

// TestStdlibHasOneManifestAndNoNestedSource is the layout half of the same
// claim, and it is two assertions because the two failure modes are different
// mistakes.
//
// One manifest: a nested `std/<name>/nomi.toml` would declare a module boundary
// inside the stdlib. Nothing can cross it — nobody can take `std/calendar`
// without the rest of the stdlib, which ships with the compiler — while every
// cross-stdlib `impl` on the far side of it becomes an orphan-rule violation.
//
// No nested source: every public stdlib module is a flat `std/<name>.nomi`. A
// nested one would be a stdlib module whose physical path and public import
// path disagree, which is the shape that cost the 46-diagnostic and 26-failure
// defects recorded above.
func TestStdlibHasOneManifestAndNoNestedSource(t *testing.T) {
	var manifests, nested []string
	err := fs.WalkDir(stdlibFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dir := path.Dir(p)
		if strings.HasPrefix(dir, "_") || strings.Contains(dir, "/_") {
			return nil
		}
		switch {
		case d.Name() == "nomi.toml":
			manifests = append(manifests, p)
		case strings.HasSuffix(p, ".nomi") && dir != ".":
			nested = append(nested, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking embedded stdlib: %v", err)
	}
	sort.Strings(manifests)
	sort.Strings(nested)
	if len(manifests) != 1 || manifests[0] != "nomi.toml" {
		t.Errorf("expected exactly the stdlib's own root manifest, got %v; a nested manifest would "+
			"declare a module boundary nothing can cross and make every cross-stdlib impl an orphan",
			manifests)
	}
	if len(nested) != 0 {
		t.Errorf("nested stdlib source found: %v; every public stdlib module is a flat std/<name>.nomi", nested)
	}
	// The vacuity control. With a flat tree, "no nested .nomi" and "the walk
	// never descended" are the same observation.
	//
	// `_fixtures` is the one subdirectory and it holds nested `.nomi` on
	// purpose, so seeing its files proves the walk descends and that the `_`
	// skip above is doing work rather than never firing.
	skipped := 0
	_ = fs.WalkDir(stdlibFS, "_fixtures", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".nomi") {
			skipped++
		}
		return nil
	})
	if skipped == 0 {
		t.Fatal("the embedded tree has no nested .nomi under _fixtures, so the walk never " +
			"descended into a subdirectory and both checks above are vacuous")
	}
	t.Logf("%d manifest(s) %v, %d nested .nomi outside _fixtures, %d skipped under _fixtures",
		len(manifests), manifests, len(nested), skipped)
}
