//go:build !js

package stdwasm

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/internal/artifactcache"
)

// A module compiled in one process is read back from the artifact cache in
// the next: its directory holds wazero's entry, a second compile leaves the
// entry as it was rather than writing it again, and with the cache off
// nothing is written.
func TestCompile_TheArtifactCacheKeepsTheCode(t *testing.T) {
	root := t.TempDir()
	t.Setenv(artifactcache.RootEnv, root)
	data := fixture(t, "upper")
	sum := sha256.Sum256(data)
	if _, fail := compile(data, sum); fail != "" {
		t.Fatal(fail)
	}
	moduleDir := func(sum [sha256.Size]byte) string { return filepath.Join(root, cacheDir, fmt.Sprintf("%x", sum)) }
	entries := func() map[string]int64 {
		out := map[string]int64{}
		_ = filepath.Walk(moduleDir(sum), func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				out[path] = info.ModTime().UnixNano()
			}
			return nil
		})
		return out
	}
	first := entries()
	if len(first) != 1 {
		t.Fatalf("after one compile the module's directory holds %v, want one entry", first)
	}
	if _, fail := compile(data, sum); fail != "" {
		t.Fatal(fail)
	}
	if second := entries(); !reflect.DeepEqual(first, second) {
		t.Errorf("a second compile rewrote the cache: %v, then %v", first, second)
	}

	t.Setenv(artifactcache.RootEnv, "off")
	values := sha256.Sum256(fixture(t, "values"))
	if _, fail := compile(fixture(t, "values"), values); fail != "" {
		t.Fatal(fail)
	}
	if _, err := os.Stat(moduleDir(values)); !os.IsNotExist(err) {
		t.Errorf("with the cache off, compiling wrote %s (%v)", moduleDir(values), err)
	}
}
