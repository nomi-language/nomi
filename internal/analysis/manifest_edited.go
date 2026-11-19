package analysis

import (
	"os"
	"path/filepath"
	"sync"
)

// A nomi.toml as an editor holds it: the package doc, "nomi.toml".

// editedManifests holds the text of each nomi.toml an editor has open, by
// its cleaned absolute path.
var editedManifests struct {
	mu    sync.RWMutex
	texts map[string]string
}

// SetEditedManifest makes text the content of the nomi.toml at path, an
// absolute path, as an editor holds it; ClearEditedManifest returns the
// file to the disk's text.
func SetEditedManifest(path, text string) {
	editedManifests.mu.Lock()
	defer editedManifests.mu.Unlock()
	if editedManifests.texts == nil {
		editedManifests.texts = map[string]string{}
	}
	editedManifests.texts[filepath.Clean(path)] = text
}

// ClearEditedManifest forgets an editor's text of the nomi.toml at path.
func ClearEditedManifest(path string) {
	editedManifests.mu.Lock()
	defer editedManifests.mu.Unlock()
	delete(editedManifests.texts, filepath.Clean(path))
}

// ReadManifest is the text of the nomi.toml in dir: an editor's
// (SetEditedManifest), else the disk's.
func ReadManifest(dir string) ([]byte, error) {
	path := filepath.Join(dir, "nomi.toml")
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
