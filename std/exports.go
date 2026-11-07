package std

import (
	"io/fs"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The analysis names the import a file is missing (std::http.Method) from
// the public std modules' sources, which it reads only when such a
// diagnostic asks.
func init() {
	analysis.SetStdSources(moduleSources)
}

// moduleSources is every flat std module's source by module name; the
// test-only fixtures are no module a program imports.
func moduleSources() map[string]string {
	out := map[string]string{}
	entries, err := fs.ReadDir(stdlibFS, ".")
	if err != nil {
		return out
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".nomi")
		if !ok || e.IsDir() {
			continue
		}
		if data, err := stdlibFS.ReadFile(e.Name()); err == nil {
			out[name] = string(data)
		}
	}
	return out
}
