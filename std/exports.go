package std

import (
	"io/fs"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The analysis names the import a file is missing (std::http.Method) from
// what the public std modules export, which it reads only when such a
// diagnostic asks: the std artifact's record when this process has read or
// made one (loadShared), else each module's source parsed.
func init() {
	analysis.SetStdExports(moduleExports)
}

// artifactExports is the exports of the std artifact this process builds
// the stdlib from, once it has one.
var artifactExports atomic.Pointer[map[string]analysis.StdModuleExport]

// moduleExports is what every flat std module exports, by module name.
func moduleExports() map[string]analysis.StdModuleExport {
	if exports := artifactExports.Load(); exports != nil {
		return *exports
	}
	return analysis.StdExportsFromSources(moduleSources())
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
