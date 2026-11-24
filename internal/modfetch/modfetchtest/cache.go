package modfetchtest

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
)

// Cache makes a module cache for t, removed when t ends although the
// cache keeps its repositories read-only, and points NOMI_MODULE_CACHE at
// it.
func Cache(t testing.TB) string {
	dir := CacheDir(t)
	t.Setenv(modcache.CacheEnv, dir)
	return dir
}

// CacheDir is Cache without setting NOMI_MODULE_CACHE, for a test that
// hands it to a `nomi` it starts.
func CacheDir(t testing.TB) string {
	dir := filepath.Join(t.TempDir(), "modules")
	t.Cleanup(func() { _ = modcache.RemoveTree(dir) })
	return dir
}

// CacheRepo puts repo at version into cache as a fetch of it leaves it,
// with files by their slash paths from the repository's root, and answers
// the commit it records for the tag.
func CacheRepo(t testing.TB, cache, repo, version string, files map[string]string) string {
	t.Helper()
	dir := modcache.RepoDir(cache, repo, version)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".tmp-")
	if err != nil {
		t.Fatal(err)
	}
	var list []modcache.RepoFile
	for _, name := range sorted(files) {
		p := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(files[name]), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(files[name]))
		list = append(list, modcache.RepoFile{Path: name, Sum: hex.EncodeToString(sum[:])})
	}
	commit := fmt.Sprintf("%x", sha1.Sum([]byte(repo+" "+version)))
	if err := modcache.Install(cache, repo, version, commit, tmp, list); err != nil {
		t.Fatal(err)
	}
	return commit
}

// Hash is the hash nomi.lock holds for the module in subdir ("" for the
// root) of a repository with files, worked out here from the rule rather
// than by the code under test: a line `<sha256>  <path>` per file under
// subdir, sorted by path, the path from subdir, hashed with SHA-256.
func Hash(files map[string]string, subdir string) string {
	prefix := ""
	if subdir != "" {
		prefix = subdir + "/"
	}
	var rels []string
	sums := map[string]string{}
	for name, text := range files {
		if rel, ok := strings.CutPrefix(name, prefix); ok {
			sum := sha256.Sum256([]byte(text))
			rels = append(rels, rel)
			sums[rel] = hex.EncodeToString(sum[:])
		}
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		fmt.Fprintf(h, "%s  %s\n", sums[rel], rel)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
