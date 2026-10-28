// Package toolside holds what `nomi tool sqlkit generate` needs from Go and
// std does not offer: the module's `[sqlkit]` configuration, read from
// nomi.toml.
package toolside

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the `[sqlkit]` table of root/nomi.toml as JSON text, "null"
// when there is none.
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
