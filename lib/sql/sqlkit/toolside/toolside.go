// Package toolside holds what `nomi tool sqlkit prepare` needs from Go and
// std does not offer: the module's `[sqlkit]` configuration, read from
// nomi.toml as the compiler reads it for the macros, and the files
// under a directory.
package toolside

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the `[sqlkit]` table of root/nomi.toml as JSON text, "null"
// when there is none: what the `query` and `database` macros receive
// as their configuration.
func Config(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "nomi.toml"))
	if err != nil {
		return "", err
	}
	var manifest map[string]any
	if err := toml.Unmarshal(data, &manifest); err != nil {
		return "", errors.New("nomi.toml: " + err.Error())
	}
	table, ok := manifest["sqlkit"]
	if !ok {
		return "null", nil
	}
	out, err := json.Marshal(table)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Files are the paths, from root and with forward slashes, of the files
// under root/dir whose names end in suffix, in order. Directories whose
// names start with a dot are skipped, and so is a directory holding a
// nomi.toml of its own other than root, which is another module.
func Files(root, dir, suffix string) ([]string, error) {
	base := filepath.Join(root, filepath.FromSlash(dir))
	out := []string{}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == base {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			if path != base && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			if path != filepath.Clean(root) {
				if _, err := os.Stat(filepath.Join(path, "nomi.toml")); err == nil {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), suffix) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
