package ffirun

// Bindings to Go standard library packages.
//
// `gopkg "strings" as strings` names no module, so it needs no go.mod: the
// package's source is the Go toolchain's own, under GOROOT/src. Validation and
// the adapter generator read it there as they read a local package's source
// (goSourceDir), with the toolchain's build constraints for this platform, and
// the wrapper imports it by its path with nothing in go.mod.
//
// The toolchain is the `go` the wrapper is built with: `go env GOROOT
// GOVERSION`, asked once per process. Its version is part of the wrapper's
// cache key when a project binds a standard library package (hashRecord's
// GoStd), since that toolchain's source is what the adapters were generated
// from and what the wrapper links.

import (
	"bufio"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// goToolchainInfo is the Go toolchain whose standard library a binding reads.
type goToolchainInfo struct {
	root    string // GOROOT; "" when no toolchain was found
	version string // GOVERSION, `go1.27.0`
}

// stdToolchain asks the `go` on PATH, from a directory with no go.mod, so the
// answer is the toolchain GOTOOLCHAIN selects for a module that asks for
// nothing newer. With no `go` it falls back to the GOROOT this binary
// was built with, when that tree still exists.
var stdToolchain = sync.OnceValue(func() goToolchainInfo {
	if goBin, err := gotoolchain.FindForFFIWrapper(); err == nil {
		cmd := exec.Command(goBin, "env", "GOROOT", "GOVERSION")
		cmd.Dir = os.TempDir()
		if out, err := cmd.Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) == 2 && isDir(filepath.Join(lines[0], "src")) {
				return goToolchainInfo{root: lines[0], version: strings.TrimSpace(lines[1])}
			}
		}
	}
	root := build.Default.GOROOT
	if root == "" || !isDir(filepath.Join(root, "src")) {
		return goToolchainInfo{}
	}
	return goToolchainInfo{root: root, version: goRootVersion(root)}
})

// goRootVersion is the first line of GOROOT/VERSION, `go1.27.0`.
func goRootVersion(root string) string {
	f, err := os.Open(filepath.Join(root, "VERSION"))
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if s.Scan() {
		return strings.TrimSpace(s.Text())
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// IsStdPackage reports whether importPath is a package of the Go standard
// library: its first element has no dot, and the toolchain's GOROOT/src holds
// it. A `cmd/...` package is the go command's own and not importable, so it
// is not one.
func IsStdPackage(importPath string) bool {
	_, ok := StdPackageDir(importPath)
	return ok
}

// StdPackageDir is the directory of the standard library package importPath
// in the toolchain's GOROOT, and whether it is one (IsStdPackage).
func StdPackageDir(importPath string) (string, bool) {
	first, _, _ := strings.Cut(importPath, "/")
	if first == "" || strings.Contains(first, ".") || first == "cmd" || first == "vendor" {
		return "", false
	}
	root := stdToolchain().root
	if root == "" {
		return "", false
	}
	dir := filepath.Join(root, "src", filepath.FromSlash(importPath))
	matches, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, m := range matches {
		if !strings.HasSuffix(m, "_test.go") {
			return dir, true
		}
	}
	return "", false
}

// stdInternal reports whether a standard library import path is internal to
// it (`internal/abi`, `crypto/internal/fips140`), which no other module may
// import.
func stdInternal(importPath string) bool {
	for _, elem := range strings.Split(importPath, "/") {
		if elem == "internal" {
			return true
		}
	}
	return false
}

// stdGoFiles is the files of the standard library package in dir that the
// toolchain compiles for this platform: build constraints decide, as they do
// for `go build`. A standard library directory holds a file per platform for
// many declarations, and reading every one would let the last file read
// decide a signature.
func stdGoFiles(dir string) ([]string, error) {
	ctx := build.Default
	ctx.GOROOT = stdToolchain().root
	bp, err := ctx.ImportDir(dir, 0)
	if err != nil {
		return nil, err
	}
	return append(append([]string(nil), bp.GoFiles...), bp.CgoFiles...), nil
}

// discoversStd reports whether any discovered package is a standard library
// package, so the toolchain's version keys the wrapper.
func discoversStd(discovered []DiscoveredPackage) bool {
	for _, d := range discovered {
		if d.ImportPath != "" && IsStdPackage(d.ImportPath) {
			return true
		}
	}
	return false
}
