package analysis_test

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/std"
)

// tryBoundaryDiagnostics is the closed set of message prefixes
// checker.checkTryBoundary emits. Kept as data so the blast-radius census and
// the standing guard below cannot drift apart, and so a fourth arm added to the
// check without a row here fails to be counted rather than silently vanishing
// from the census.
var tryBoundaryDiagnostics = []string{
	"try error type mismatch:",
	"`try` on a ",
	"`try` cannot propagate a ",
}

func isTryBoundaryDiagnostic(msg string) bool {
	for _, p := range tryBoundaryDiagnostics {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

// TestTryBoundary_TreeIsClean is BOTH the blast-radius census for the `try`
// boundary rule and the standing guard that the tree stays converted.
//
// It runs the production analysis pipeline — analysis.DocumentManager, which is
// what the LSP opens a file with and which folds BuildTypes, CheckTypes, the
// concurrency passes and FinalizeCoherence into one TypeErrors slice — over
// every `.nomi` file in `std/` and `tests/`, plus every
// fenced Nomi block in the tour. Only checkTryBoundary's own diagnostics are
// counted, so unrelated diagnostics a file may carry for its own reasons (a
// `tests/` negative case, a doc block that is deliberately ill-typed)
// do not pollute the count.
//
// Run with `-v` for the by-directory table; the failure message carries the
// per-site detail.
func TestTryBoundary_TreeIsClean(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	lib := std.Load()
	newManager := func(workspace string) *analysis.DocumentManager {
		dm := analysis.NewDocumentManager()
		dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
		dm.SetWorkspaceRoot(workspace)
		return dm
	}

	type hit struct {
		bucket string
		where  string
		msg    string
	}
	var hits []hit
	counts := map[string]int{}
	scanned := map[string]int{}

	record := func(bucket, where string, errs []analysis.TypeError) {
		scanned[bucket]++
		for _, e := range errs {
			if !isTryBoundaryDiagnostic(e.Message) {
				continue
			}
			counts[bucket]++
			hits = append(hits, hit{bucket: bucket, where: where, msg: e.Message})
		}
	}

	// --- file trees ------------------------------------------------------
	for _, bucket := range []string{"std", "tests"} {
		dir := filepath.Join(root, bucket)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".nomi") {
				return err
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			// Each file is opened as its own focused document, with the
			// workspace rooted at the file's own project so sibling imports
			// resolve the way they do in production.
			dm := newManager(projectRootFor(root, path))
			doc := dm.Open("file://"+path, string(content))
			if doc == nil || doc.Analysis == nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			record(bucket, rel, doc.Analysis.TypeErrors)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", bucket, err)
		}
	}

	// --- tour ------------------------------------------------------------
	// `nomi-run` blocks are executed by runtime's TestTourDoctests, so a
	// laundering site in one is load-bearing. Plain `nomi` blocks are
	// highlight-only and nothing compiles them; they are counted under a
	// separate bucket so the table does not imply an enforcement that
	// does not exist.
	tourRoot := filepath.Join(root, "tour", "src", "content", "docs")
	tmp := t.TempDir()
	blockN := 0
	err = filepath.WalkDir(tourRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".mdx") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		for _, fence := range []struct{ lang, bucket string }{
			{"nomi-run", "tour (nomi-run, executed)"},
			{"nomi", "tour (nomi, highlight-only)"},
		} {
			for _, b := range doctest.ExtractBlocks(string(data), fence.lang) {
				if b.HasInfo("ignore") {
					continue
				}
				blockN++
				dir := filepath.Join(tmp, "block", strconv.Itoa(blockN))
				entry, writeErr := writeTourBlock(dir, b.Code)
				if writeErr != nil {
					t.Fatalf("stage tour block %s:L%d: %v", rel, b.Line, writeErr)
				}
				content, readErr := os.ReadFile(entry)
				if readErr != nil {
					t.Fatal(readErr)
				}
				dm := newManager(dir)
				doc := dm.Open("file://"+entry, string(content))
				if doc == nil || doc.Analysis == nil {
					continue
				}
				record(fence.bucket, rel+":L"+strconv.Itoa(b.Line), doc.Analysis.TypeErrors)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk tour: %v", err)
	}

	// --- report ----------------------------------------------------------
	buckets := make([]string, 0, len(scanned))
	for b := range scanned {
		buckets = append(buckets, b)
	}
	sort.Strings(buckets)
	total := 0
	for _, b := range buckets {
		total += counts[b]
		t.Logf("%-34s units=%-5d try-boundary sites=%d", b, scanned[b], counts[b])
	}
	t.Logf("%-34s %30d", "TOTAL", total)

	if total == 0 {
		return
	}
	var detail strings.Builder
	for _, h := range hits {
		detail.WriteString("\n  [" + h.bucket + "] " + h.where + ": " + h.msg)
	}
	t.Fatalf("%d `try` boundary violation(s) in the tree:%s", total, detail.String())
}

// projectRootFor returns the directory the given file's project is rooted at:
// the nearest ancestor carrying a nomi.toml, falling back to the file's own
// directory. Opening a file with the repo root as the workspace makes the
// DocumentManager treat unrelated siblings as part of one project, which
// manufactures diagnostics that have nothing to do with the file.
func projectRootFor(repoRoot, path string) string {
	dir := filepath.Dir(path)
	for cur := dir; strings.HasPrefix(cur, repoRoot) && cur != repoRoot; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "nomi.toml")); err == nil {
			return cur
		}
	}
	return dir
}

// writeTourBlock stages one fenced tour block on disk and returns the entry
// file's path. `// FILE: <name>` markers split the block into sibling files
// exactly as the tour playground and vmhost.SplitMultiFile do; a block with no
// markers is one file. Reimplemented rather than imported so this census does
// not make the analysis package's tests depend on vmhost.
func writeTourBlock(dir, code string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	const marker = "// FILE:"
	files := map[string][]string{}
	order := []string{}
	current := "main.nomi"
	add := func(name string) {
		if _, seen := files[name]; !seen {
			files[name] = nil
			order = append(order, name)
		}
	}
	add(current)
	for _, line := range strings.Split(code, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, marker) {
			current = strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
			add(current)
			continue
		}
		files[current] = append(files[current], line)
	}
	entry := ""
	for _, name := range order {
		body := strings.Join(files[name], "\n")
		if strings.TrimSpace(body) == "" {
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", err
		}
		if entry == "" && strings.HasSuffix(name, ".nomi") {
			entry = path
		}
	}
	if entry == "" {
		entry = filepath.Join(dir, "main.nomi")
		if err := os.WriteFile(entry, []byte(code), 0o644); err != nil {
			return "", err
		}
	}
	return entry, nil
}
