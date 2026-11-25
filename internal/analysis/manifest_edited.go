package analysis

import (
	"os"
	"path/filepath"
	"sync"
)

// A nomi.toml, or a nomi-workspace.toml, as an editor holds it: the
// package doc, "nomi.toml".

// editedManifests holds the text of each nomi.toml and nomi-workspace.toml
// an editor has open, by its cleaned absolute path.
var editedManifests struct {
	mu    sync.RWMutex
	texts map[string]string
}

// SetEditedManifest makes text the content of the nomi.toml or
// nomi-workspace.toml at path, an absolute path, as an editor holds it;
// ClearEditedManifest returns the file to the disk's text.
func SetEditedManifest(path, text string) {
	editedManifests.mu.Lock()
	defer editedManifests.mu.Unlock()
	if editedManifests.texts == nil {
		editedManifests.texts = map[string]string{}
	}
	editedManifests.texts[filepath.Clean(path)] = text
}

// ClearEditedManifest forgets an editor's text of the file at path.
func ClearEditedManifest(path string) {
	editedManifests.mu.Lock()
	defer editedManifests.mu.Unlock()
	delete(editedManifests.texts, filepath.Clean(path))
}

// ReadManifest is the text of the nomi.toml in dir (ReadManifestFile).
func ReadManifest(dir string) ([]byte, error) {
	return ReadManifestFile(filepath.Join(dir, "nomi.toml"))
}

// ReadManifestFile is the text of the nomi.toml or nomi-workspace.toml at
// path: an editor's (SetEditedManifest), else the disk's. A workspace's
// lookup reads its file through it (pkgcache.ManifestReader.Read), so a
// member is checked against the text an editor holds.
func ReadManifestFile(path string) ([]byte, error) {
	editedManifests.mu.RLock()
	edited := len(editedManifests.texts) > 0
	editedManifests.mu.RUnlock()
	if edited {
		abs, err := filepath.Abs(path)
		if err == nil {
			editedManifests.mu.RLock()
			text, ok := editedManifests.texts[abs]
			editedManifests.mu.RUnlock()
			if ok {
				return []byte(text), nil
			}
		}
	}
	return os.ReadFile(path)
}
