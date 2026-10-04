package ffirun

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// listDirectRequires reads go.mod under projectRoot and returns the import
// paths of every direct require (i.e. require directives without the
// `// indirect` marker). The wrapper threads these through to runtime
// diagnostics; discovery itself is driven by top-level Go blocks and inline
// bindings.
//
// Returns the empty slice (not nil) for a go.mod with no requires.
// Returns an error if go.mod is missing or unparseable; missing
// should have been ruled out by findGoModRoot but we double-check
// rather than panic.
func listDirectRequires(projectRoot string) ([]string, error) {
	f, err := parseProjectGoMod(projectRoot)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range f.Require {
		if r.Indirect {
			continue
		}
		out = append(out, r.Mod.Path)
	}
	return out, nil
}

func parseProjectGoMod(projectRoot string) (*modfile.File, error) {
	goModPath := filepath.Join(projectRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", goModPath, err)
	}
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", goModPath, err)
	}
	return f, nil
}

// writeAbsolutizedGoMod reads <projectRoot>/go.mod and writes a copy
// to dst with every relative `replace` directive rewritten to an
// absolute path (anchored to projectRoot). The wrapper's module path
// is changed to a private cache-only name and the user project is
// required/replaced back to projectRoot, so wrapper code can import
// project-local Go packages referenced by Nomi foreign bindings.
//
// Why this matters: the cache wrapper lives at
// ~/.cache/nomi/builds/<hash>/, which is nowhere near the user's
// project root. A user's `replace sqlite => ../sqlite` resolves
// relative to the GO.MOD's directory; copying it verbatim into the
// cache dir would make Go look for `<cache>/../sqlite`. Absolutizing
// at copy time keeps the cache wrapper self-contained.
//
// We use modfile.WriteFile (not bytes-level rewriting) so formatting,
// comments, and ordering are preserved across the round-trip — the
// result reads identically to the user's go.mod modulo the replaced
// path strings.
func writeAbsolutizedGoMod(projectRoot, dst string) error {
	if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); os.IsNotExist(err) {
		return writeSyntheticGoMod(dst)
	}
	f, err := parseProjectGoMod(projectRoot)
	if err != nil {
		return err
	}
	if f.Module == nil {
		return fmt.Errorf("go.mod has no module declaration")
	}
	userModule := f.Module.Mod.Path
	if err := f.AddModuleStmt("nomi-ffi-wrapper"); err != nil {
		return fmt.Errorf("setting wrapper module path: %w", err)
	}
	if err := f.AddRequire(userModule, "v0.0.0"); err != nil {
		return fmt.Errorf("requiring user module %s: %w", userModule, err)
	}
	if err := f.AddReplace(userModule, "", projectRoot, ""); err != nil {
		return fmt.Errorf("replacing user module %s => %s: %w", userModule, projectRoot, err)
	}
	for _, r := range f.Replace {
		if r.Old.Path == userModule && r.New.Path == projectRoot {
			continue
		}
		// Module-version replace target (no path): nothing to
		// absolutize. The New.Version field is empty for path
		// replaces by modfile's convention.
		if r.New.Version != "" {
			continue
		}
		target := r.New.Path
		if filepath.IsAbs(target) {
			continue
		}
		abs := filepath.Join(projectRoot, target)
		if err := f.AddReplace(r.Old.Path, r.Old.Version, abs, ""); err != nil {
			return fmt.Errorf("absolutizing replace %s => %s: %w", r.Old.Path, target, err)
		}
	}
	if err := prepareWrapperModule(f); err != nil {
		return err
	}
	out, err := f.Format()
	if err != nil {
		return fmt.Errorf("formatting absolutized go.mod: %w", err)
	}
	return os.WriteFile(dst, out, 0o644)
}

// prepareWrapperModule makes a generated main module able to build against the
// compiler's module (which the wrapper imports as nomi/vmhost, nomi/hostadapt
// and nomi/rt), and raises the wrapper's `go` directive to the compiler's when
// the project's is older. See prepareWrapperModuleFor.
func prepareWrapperModule(f *modfile.File) error {
	src, err := resolveCompilerSource()
	if err != nil {
		return err
	}
	return prepareWrapperModuleFor(f, src)
}

// prepareWrapperModuleFor names the compiler module src in f. A versioned
// source is required at its version and nothing replaces it, so the Go
// toolchain fetches it like any other dependency. A local source is required
// at v0.0.0 and replaced with its directory.
//
// The project's go.mod does not name the module. It holds the project's own
// module line and its own dependencies, and nothing else. When a project does
// require or replace it anyway, these lines win: AddRequire rewrites every
// existing require of the path, every existing replace of it is dropped, and a
// local source adds its own, so the wrapper always links the compiler that is
// running.
//
// Toolchain selection reads the MAIN module's directive only (it does not
// chain-switch on a dependency's), so a wrapper stamped `go 1.26.3` against a
// compiler module that says `go 1.27.0` fails with "requires go >= 1.27.0
// (running go 1.26.3)" no matter what GOTOOLCHAIN says. The wrapper links that
// module, so its floor is that module's floor.
func prepareWrapperModuleFor(f *modfile.File, src compilerSource) error {
	version := "v0.0.0"
	if src.versioned() {
		version = src.Version
	}
	if err := f.AddRequire(compilerModulePath, version); err != nil {
		return fmt.Errorf("requiring %s: %w", compilerModulePath, err)
	}
	for _, r := range append([]*modfile.Replace(nil), f.Replace...) {
		if r.Old.Path == compilerModulePath {
			if err := f.DropReplace(r.Old.Path, r.Old.Version); err != nil {
				return fmt.Errorf("dropping replace of %s: %w", compilerModulePath, err)
			}
		}
	}
	if !src.versioned() {
		if err := f.AddReplace(compilerModulePath, "", src.Dir, ""); err != nil {
			return fmt.Errorf("replacing %s => %s: %w", compilerModulePath, src.Dir, err)
		}
	}
	compilerGo, err := goDirectiveOf(src.Dir)
	if err != nil {
		return err
	}
	if f.Go == nil || semver.Compare("v"+f.Go.Version, "v"+compilerGo) < 0 {
		if err := f.AddGoStmt(compilerGo); err != nil {
			return fmt.Errorf("raising go directive to %s: %w", compilerGo, err)
		}
	}
	f.Cleanup()
	return nil
}

// stageGoSum writes the wrapper's go.sum: every line of the project's go.sum
// (when it has one) plus the compiler module's own go.sum, and, for a
// versioned compiler, the module's checksums themselves. The wrapper's build
// graph includes the compiler module's dependencies, and their checksums are
// already written down beside its go.mod, so the project's go.sum need not
// carry them and `go build` never has to look them up. Lines are deduplicated
// and sorted, which is the order go writes.
func stageGoSum(projectRoot, dst string) error {
	src, err := resolveCompilerSource()
	if err != nil {
		return err
	}
	return stageGoSumFor(projectRoot, dst, src)
}

func stageGoSumFor(projectRoot, dst string, src compilerSource) error {
	seen := map[string]bool{}
	var lines []string
	add := func(line string) {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			return
		}
		seen[line] = true
		lines = append(lines, line)
	}
	for _, path := range []string{
		filepath.Join(projectRoot, "go.sum"),
		filepath.Join(src.Dir, "go.sum"),
	} {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			add(line)
		}
	}
	if src.versioned() {
		if src.Sum != "" {
			add(compilerModulePath + " " + src.Version + " " + src.Sum)
		}
		if src.GoModSum != "" {
			add(compilerModulePath + " " + src.Version + "/go.mod " + src.GoModSum)
		}
	}
	sort.Strings(lines)
	var out bytes.Buffer
	for _, line := range lines {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return os.WriteFile(dst, out.Bytes(), 0o644)
}

// compilerGoDirective reads the `go` line from the compiler module's own
// go.mod, so a wrapper never asks for an older toolchain than the module it
// links and never drifts when that line is bumped.
func compilerGoDirective() (string, error) {
	src, err := resolveCompilerSource()
	if err != nil {
		return "", err
	}
	return goDirectiveOf(src.Dir)
}

func goDirectiveOf(root string) (string, error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("ffirun: reading %s: %w", path, err)
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return "", fmt.Errorf("ffirun: parsing %s: %w", path, err)
	}
	if f.Go == nil {
		return "", fmt.Errorf("ffirun: %s has no go directive", path)
	}
	return f.Go.Version, nil
}
