package ffirun

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/mod/modfile"
)

// writeSyntheticGoMod stamps a wrapper module for a project that has no go.mod
// of its own. It requires the compiler's module, which holds the runtime
// library too, and takes its `go` directive from the compiler's, for the
// reasons prepareWrapperModule spells out.
func writeSyntheticGoMod(dst string) error {
	f, err := modfile.Parse(dst, []byte("module nomi-build-wrapper\n"), nil)
	if err != nil {
		return fmt.Errorf("ffirun: starting synthetic go.mod: %w", err)
	}
	if err := prepareWrapperModule(f); err != nil {
		return err
	}
	out, err := f.Format()
	if err != nil {
		return fmt.Errorf("ffirun: formatting synthetic go.mod: %w", err)
	}
	return os.WriteFile(dst, out, 0o644)
}

// compilerModulePath is the compiler's own module. A wrapper imports
// nomi/vmhost and nomi/hostadapt from it, and prepareWrapperModule requires it
// at the version this `nomi` was built as, or replaces it with a local tree
// (compilersource.go).
const compilerModulePath = "github.com/nomi-language/nomi"

// nomiModuleRoot locates the source tree this binary was compiled from, which
// a development build's wrapper replaces the compiler module with.
//
// The wrapper needs the compiler's Go module, not just this binary:
// renderWrapper emits `import nomivmhost "github.com/nomi-language/nomi/vmhost"`,
// and vmhost runs the front end and the IR builder before the VM, so `go
// build` compiles nomi/analysis, internal/irbuild and the rest from source. A
// versioned `nomi` gets that source from the module proxy. A development build
// has only the tree runtime.Caller names, which -trimpath hides and a moved
// checkout loses; both report errNoCompilerSource rather than the stat that
// failed, which at 390b9c46 read
//
//	nomi run: ffirun: locating nomi module root: stat nomi/go.mod: no such file or directory
func nomiModuleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("ffirun: locating nomi module root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if !filepath.IsAbs(file) {
		return "", errNoCompilerSource
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", errNoCompilerSource
	}
	return root, nil
}

func findNomiManifestRoot(startDir string) (string, bool) {
	dir := startDir
	for {
		if info, err := os.Stat(filepath.Join(dir, "nomi.toml")); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
