package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestInternalAccess_RootInternal_RootImporterAllowed — case 1 from
// the plan: importer at module root, importee at `internal/foo`. The
// root directory IS the parent of `internal/`, so the import is
// allowed.
func TestInternalAccess_RootInternal_RootImporterAllowed(t *testing.T) {
	err := checkInternalAccess(
		"main",            // importer module-relative path (module root file)
		"internal/id_gen", // importee module-relative path
		true,              // same module
	)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestInternalAccess_NestedInternal_SiblingAllowed — case 2 from the
// plan: importer at `tools/seed`, importee at `tools/internal/seed_data`.
// `tools/` is the parent of the nested `internal/`, so any file
// rooted under `tools/` may import.
func TestInternalAccess_NestedInternal_SiblingAllowed(t *testing.T) {
	err := checkInternalAccess(
		"tools/seed",
		"tools/internal/seed_data",
		true,
	)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestInternalAccess_NestedInternal_NonSiblingRejected — case 3 from
// the plan: importer at module root, importee at
// `tools/internal/seed_data`. The root is not rooted under `tools/`,
// so the import is rejected even though both files are in the same
// module.
func TestInternalAccess_NestedInternal_NonSiblingRejected(t *testing.T) {
	err := checkInternalAccess(
		"main",
		"tools/internal/seed_data",
		true,
	)
	if err == nil {
		t.Fatal("expected access error for main importing tools/internal/seed_data")
	}
	if !strings.Contains(err.Error(), "tools/internal/seed_data") {
		t.Errorf("error should mention the importee path, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "tools") {
		t.Errorf("error should mention the parent subtree %q, got %q", "tools", err.Error())
	}
}

// TestInternalAccess_CrossModuleRejected — case 4 from the plan: a
// cross-module import that targets any `internal/*` path is rejected
// regardless of importer path. The same-module sibling permission
// does not extend across module boundaries.
func TestInternalAccess_CrossModuleRejected(t *testing.T) {
	err := checkInternalAccess(
		"main",
		"internal/id_gen",
		false, // cross module
	)
	if err == nil {
		t.Fatal("expected access error for cross-module import of internal/id_gen")
	}
	if !strings.Contains(err.Error(), "internal/id_gen") {
		t.Errorf("error should mention the importee path, got %q", err.Error())
	}
}

// TestInternalAccess_CrossModuleNestedRejected — cross-module variant
// of case 3: any `internal/` segment anywhere in the importee path is
// a rejection across module boundaries, even when the importer's
// position would have permitted it intra-module.
func TestInternalAccess_CrossModuleNestedRejected(t *testing.T) {
	err := checkInternalAccess(
		"tools/seed",
		"tools/internal/seed_data",
		false,
	)
	if err == nil {
		t.Fatal("expected access error for cross-module import of tools/internal/seed_data")
	}
}

// TestInternalAccess_NoInternalSegmentAllowed — edge case: an
// importee path with no `internal/` segment must pass regardless of
// importer position or same-module flag. The check is a no-op on
// imports that don't touch the internal/ rule.
func TestInternalAccess_NoInternalSegmentAllowed(t *testing.T) {
	cases := []struct {
		name       string
		importer   string
		importee   string
		sameModule bool
	}{
		{"intra-module sibling", "main", "helper", true},
		{"intra-module nested", "tools/seed", "tools/util", true},
		{"cross-module", "main", "stringkit/pad", false},
		{"empty importer (entry file)", "", "helper", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkInternalAccess(tc.importer, tc.importee, tc.sameModule); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestInternalAccess_EmptyImporterRootInternal — the entry file's
// module-relative path can be empty (BuildProjectWithCache doesn't
// thread the entry path through yet — see Task 4's wiring note).
// An empty importer is at the module root by definition, so it may
// import a root-level `internal/...`.
func TestInternalAccess_EmptyImporterRootInternal(t *testing.T) {
	err := checkInternalAccess("", "internal/id_gen", true)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestInternalAccess_EmptyImporterNestedInternal — even though an
// empty importer is at the module root, a nested internal/ under a
// subdirectory is still off-limits to the root, mirroring the
// "main" → "tools/internal/seed_data" rejection in case 3.
func TestInternalAccess_EmptyImporterNestedInternal(t *testing.T) {
	err := checkInternalAccess("", "tools/internal/seed_data", true)
	if err == nil {
		t.Fatal("expected access error: empty importer is at module root, not under tools/")
	}
}

// TestInternalAccess_ImporterInsideInternal — a file already inside
// `tools/internal/` may freely import sibling internal files; the
// parent-subtree rule says any file rooted under `tools/` can reach
// `tools/internal/`, and `tools/internal/X` is itself rooted under
// `tools/`.
func TestInternalAccess_ImporterInsideInternal(t *testing.T) {
	err := checkInternalAccess(
		"tools/internal/seed_data",
		"tools/internal/id_gen",
		true,
	)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// parseSrc is a small helper for the BuildProject wiring tests: lex +
// parse with recovery, returning the node slice the analyzer expects.
func parseSrc(t *testing.T, src string) []ast.Node {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	return nodes
}

// TestInternalAccessWiring_NonSiblingRejected drives BuildProject end-
// to-end with an in-memory loader to confirm the analyzer wiring fires
// the diagnostic at the offending import's Line/Col (not 1,1). The
// sibling file `helper.nomi` (module-relative "helper") tries to
// import `tools/internal/seed_data` — the importer is not rooted under
// `tools/`, so the import is rejected.
func TestInternalAccessWiring_NonSiblingRejected(t *testing.T) {
	const projectRoot = "/project"
	sources := map[string]string{
		"helper":                   "import tools/internal/seed_data\npub fn run(): Int { seed_data.data() }\n",
		"tools/internal/seed_data": "pub fn data(): Int { 0 }\n",
	}
	loader := func(_ string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		src, ok := sources[key]
		if !ok {
			return nil, nil
		}
		return parseSrc(t, src), nil
	}
	entryNodes := parseSrc(t, "import helper: helper\nfn main() {}\n")
	_, cache, _ := BuildProjectWithCache(entryNodes, nil, nil, nil, projectRoot, loader)
	helperFA, ok := cache["helper"]
	if !ok {
		t.Fatalf("helper FA missing from cache; got keys %v", cacheKeys(cache))
	}
	var found *TypeError
	for i := range helperFA.TypeErrors {
		if strings.Contains(helperFA.TypeErrors[i].Message, "internal/") ||
			strings.Contains(helperFA.TypeErrors[i].Message, "internal") {
			found = &helperFA.TypeErrors[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected an internal/ access error on helper.nomi, got %v", helperFA.TypeErrors)
	}
	// The import is on line 1; the next test
	// (TestInternalAccessWiring_ErrorPositionIsImport) uses a non-1
	// position to prove the diagnostic really comes from the ImportStmt
	// and isn't a 1,1 placeholder.
	if found.Line != 1 {
		t.Errorf("expected error at line 1 (import line), got %d", found.Line)
	}
}

// TestInternalAccessWiring_ErrorPositionIsImport — same as above but
// with the import on line 3 to prove the diagnostic position is the
// ImportStmt's Line/Col, not a 1,1 placeholder.
func TestInternalAccessWiring_ErrorPositionIsImport(t *testing.T) {
	const projectRoot = "/project"
	sources := map[string]string{
		"helper":                   "\n\nimport tools/internal/seed_data\npub fn run(): Int { seed_data.data() }\n",
		"tools/internal/seed_data": "pub fn data(): Int { 0 }\n",
	}
	loader := func(_ string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		src, ok := sources[key]
		if !ok {
			return nil, nil
		}
		return parseSrc(t, src), nil
	}
	entryNodes := parseSrc(t, "import helper: helper\nfn main() {}\n")
	_, cache, _ := BuildProjectWithCache(entryNodes, nil, nil, nil, projectRoot, loader)
	helperFA := cache["helper"]
	if helperFA == nil {
		t.Fatalf("helper FA missing")
	}
	var found *TypeError
	for i := range helperFA.TypeErrors {
		if strings.Contains(helperFA.TypeErrors[i].Message, "internal") {
			found = &helperFA.TypeErrors[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected internal/ access error, got %v", helperFA.TypeErrors)
	}
	if found.Line != 3 {
		t.Errorf("expected error anchored at import on line 3, got line %d", found.Line)
	}
	if found.Col == 0 {
		t.Errorf("expected non-zero column on import diagnostic, got %d", found.Col)
	}
}

// TestInternalAccessWiring_SiblingAllowed — the sibling case from the
// pure-function tests, exercised through BuildProject. A file at
// `tools/seed` may import `tools/internal/seed_data` because it sits
// under `tools/`.
func TestInternalAccessWiring_SiblingAllowed(t *testing.T) {
	const projectRoot = "/project"
	sources := map[string]string{
		"tools/seed":               "import tools/internal/seed_data\npub fn run(): Int { seed_data.data() }\n",
		"tools/internal/seed_data": "pub fn data(): Int { 0 }\n",
	}
	loader := func(_ string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		src, ok := sources[key]
		if !ok {
			return nil, nil
		}
		return parseSrc(t, src), nil
	}
	entryNodes := parseSrc(t, "import tools/seed: seed\nfn main() {}\n")
	_, cache, _ := BuildProjectWithCache(entryNodes, nil, nil, nil, projectRoot, loader)
	seedFA := cache["tools/seed"]
	if seedFA == nil {
		t.Fatalf("tools/seed FA missing")
	}
	for _, e := range seedFA.TypeErrors {
		if strings.Contains(e.Message, "internal") {
			t.Errorf("unexpected internal/ access error on tools/seed: %v", e)
		}
	}
}

// TestInternalAccessWiring_RootInternalFromRootEntry — the entry file
// (FilePath empty) sits at the module root by convention. A
// `import internal/x` from the entry must be allowed.
func TestInternalAccessWiring_RootInternalFromRootEntry(t *testing.T) {
	const projectRoot = "/project"
	sources := map[string]string{
		"internal/x": "pub fn x(): Int { 0 }\n",
	}
	loader := func(_ string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		src, ok := sources[key]
		if !ok {
			return nil, nil
		}
		return parseSrc(t, src), nil
	}
	entryNodes := parseSrc(t, "import internal/x\nfn main() {}\n")
	entryFA, _, _ := BuildProjectWithCache(entryNodes, nil, nil, nil, projectRoot, loader)
	for _, e := range entryFA.TypeErrors {
		if strings.Contains(e.Message, "internal") {
			t.Errorf("unexpected internal/ access error on entry: %v", e)
		}
	}
}

// cacheKeys returns sorted keys for diagnostic output in test
// failures.
func cacheKeys(c map[string]*FileAnalysis) []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return out
}
