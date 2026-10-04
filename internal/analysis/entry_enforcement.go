package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckEntryPlacement enforces the manifest-vs-source invariant for
// executable entry callbacks:
//
//   - every file whose module-relative path is in
//     manifest.EntryPoints must contain an entry callback;
//   - every file NOT listed must contain zero entry callbacks.
//
// Returns one TypeError per violation. With a nil manifest (no
// nomi.toml), the check is skipped — single-file `nomi run` mode and
// ad-hoc test fixtures keep their existing behavior.
//
// `entryNodes` is the entry file's top-level node slice;
// `entryModRel` is the entry file's module-relative path (e.g.
// "main" for <root>/main.nomi, "cmd/foo/main" for
// <root>/cmd/foo/main.nomi). Pass "" when the caller doesn't
// know the entry's module-relative path — the entry-side check is
// skipped (sibling files in `files` are still enforced).
//
// `files` is the discovered sibling map (keyed by "/"-joined module
// path), the same shape as Project.Files. The entry file is NOT in
// this map; it's passed separately.
//
// Diagnostic positions: the non-entry-with-callback case is anchored at
// the offending callback's Line/Col so the LSP can highlight the bad
// declaration. The entry-declared-but-missing-main case has no Nomi-
// source node to point at (the contract violation is the manifest's
// claim, not any specific line of code), so Line/Col are zero and the
// message names the file.
func CheckEntryPlacement(
	manifest *Manifest,
	entryNodes []ast.Node,
	entryModRel string,
	files map[string][]ast.Node,
) []TypeError {
	if manifest == nil {
		return nil
	}
	declared := make(map[string]bool, len(manifest.EntryPoints))
	for _, e := range manifest.EntryPoints {
		declared[e] = true
	}

	var errs []TypeError

	check := func(modulePath string, nodes []ast.Node) {
		entryNode := findEntryFuncDef(nodes)
		hasEntry := entryNode != nil
		switch {
		case declared[modulePath] && !hasEntry:
			// Line/Col 1,1 is a placeholder: the discrepancy originates
			// in nomi.toml, not at any .nomi node. Matches the
			// manifest-load TypeError convention in buildProjectWithCache;
			// the message itself names the file.
			errs = append(errs, TypeError{
				Line: 1, Col: 1,
				Message: fmt.Sprintf(
					"%s is declared in nomi.toml entry_points but has no entry callback — add `fn main() { ... }`, or remove %q from entry_points",
					modulePath, modulePath),
			})
		case !declared[modulePath] && hasEntry:
			errs = append(errs, TypeError{
				Line: entryNode.Line, Col: entryNode.Col,
				Message: fmt.Sprintf(
					"entry callback in %s but file is not declared in nomi.toml entry_points — add %q to entry_points or move the entry callback to an entry file",
					modulePath, modulePath),
			})
		}
	}

	// Entry file: skip when caller didn't supply a module-relative
	// path. Most existing test callers and single-file `nomi run` mode
	// fall through to sibling-only enforcement.
	if entryModRel != "" {
		check(entryModRel, entryNodes)
	}
	for modulePath, nodes := range files {
		check(modulePath, nodes)
	}
	return errs
}

// findEntryFuncDef returns the entry callback in the given top-level node
// slice, or nil when none exists.
func findEntryFuncDef(nodes []ast.Node) *ast.FuncDef {
	for _, n := range nodes {
		if fn, ok := n.(*ast.FuncDef); ok && fn.Name == "main" {
			return fn
		}
	}
	return nil
}
