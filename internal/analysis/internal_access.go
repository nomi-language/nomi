package analysis

import (
	"fmt"
	"strings"
)

// checkInternalAccess verifies an import is permitted under the
// `internal/` subtree rule. Both paths are module-relative (the
// file's path within its module's root, slash-separated, no .nomi
// extension). `sameModule` is true when both files are in the same
// module.
//
// Rule: an importee under any `internal/` segment is reachable only
// from files whose path is rooted at the parent directory of that
// `internal/`. Across modules, any `internal/` segment in the
// importee is a rejection regardless of importer path or `pub` —
// the orphan-rule-style hygiene applies to module boundaries
// unconditionally.
//
// Examples (intra-module):
//   - importer "main",       importee "internal/id_gen"        → allowed
//     (root is the parent of `internal/`)
//   - importer "tools/seed", importee "tools/internal/seed_data" → allowed
//     (importer is rooted under `tools/`)
//   - importer "main",       importee "tools/internal/seed_data" → REJECTED
//     (importer is not rooted under `tools/`)
//
// Cross-module: any `internal/` in importee is rejected.
//
// An importee path with no `internal/` segment is always allowed —
// the function is a no-op when the rule doesn't apply.
func checkInternalAccess(importerModRel, importeeModRel string, sameModule bool) error {
	segs := strings.Split(importeeModRel, "/")
	for i, seg := range segs {
		if seg != "internal" {
			continue
		}
		if !sameModule {
			return fmt.Errorf("cannot import %q: internal/ paths are unreachable from other modules", importeeModRel)
		}
		// Allowed iff importer's module-relative path is rooted at the
		// directory containing this `internal/`. That directory's
		// module-relative path is `segs[:i]` joined ("" for a
		// module-root-level internal/, in which case any same-module
		// file qualifies).
		parent := strings.Join(segs[:i], "/")
		if parent == "" {
			return nil
		}
		if importerModRel == parent || strings.HasPrefix(importerModRel, parent+"/") {
			return nil
		}
		return fmt.Errorf("cannot import %q from %q: outside the parent subtree of %s/internal/",
			importeeModRel, importerModRel, parent)
	}
	return nil
}
