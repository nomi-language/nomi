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

// moduleExports is what every std module exports, by module name.
func moduleExports() map[string]analysis.StdModuleExport {
	if exports := artifactExports.Load(); exports != nil {
		return *exports
	}
	return analysis.StdExportsFromSources(moduleSources())
}

// moduleSources is every std module's source by module name ("fs",
// "fs/testing"); the test-only fixtures are no module a program imports.
func moduleSources() map[string]string {
	out := map[string]string{}
	_ = fs.WalkDir(stdlibFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), "_") {
				return fs.SkipDir
			}
			return nil
		}
		if name, ok := strings.CutSuffix(p, ".nomi"); ok {
			if data, err := stdlibFS.ReadFile(p); err == nil {
				out[name] = string(data)
			}
		}
		return nil
	})
	return out
}
