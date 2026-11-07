package ffirun

// Prebuilt runners for the first-party lib/ modules: how a release `nomi`
// runs, tests and builds a program whose Go bindings are all lib/ modules'
// (the SQLite and Postgres drivers, sqlkit, markdown, image) with no Go
// toolchain.
//
// The lib/ modules' Go side is known when a release is cut, so the wrapper
// and the runner a project of theirs would build are generated from them
// once, by GenerateLibMains, into the librunners/ module, which requires
// the compiler and every lib/ module by directory. A release builds two
// binaries from it, from the same commit as `nomi`, into a lib archive per
// platform (`nomi_<version>_<goos>_<goarch>_lib.tar.gz`) beside its main
// archive:
//
//   - nomi-lib: the FFI wrapper (codegen.go) for every lib/ binding: what
//     `nomi run`, `test` and `check` run a program in, and what lowers the
//     image `nomi build` appends. Not for js/wasm.
//   - nomi-vm-lib: the runner `nomi build` appends that image to.
//
// They are a separate archive because they double what a platform
// downloads: on darwin/arm64, stripped, nomi-lib with the std bundle is
// 43.9 MB (15.8 MB gzipped) and nomi-vm-lib 21.8 MB (8.0 MB), against the
// plain nomi-vm's 13.4 MB (5.1 MB); on js/wasm nomi-vm-lib is 28.7 MB
// against 25.2 MB. A `nomi` looks for them beside itself, then in what it
// downloaded from its release before, then downloads the lib archive
// (findLibWrapper, releaseBinary), so a user who never uses a lib/ module
// with Go never fetches them. No wasip1 runner ships: modernc.org/sqlite
// does not build for wasip1. No nomi_nonet variant ships: pgx links net and
// crypto/tls whatever the tag says, so it saves 2.4 MB natively (it would
// save 9 MB of js/wasm's 28.7 MB).
//
// Prepare uses nomi-lib when every binding the program's files declare is in
// a lib/ module of libModules, in the module cache at LibVersion, the
// version this `nomi` was cut at. Anything else (a binding of the project's
// own, a lib/ module at another version or replaced with a directory) takes
// the wrapper path, which builds with Go, and when there is no Go the
// failure says which binding or version needs it (libMiss).
//
// The generated sources name each declaring file by its module's path and
// its path in the module, not by the path it had on the machine that
// generated them: the wrapper's entry-scoped keys match a file in the module
// cache by that suffix (codegen.go's nomiWithoutVersions).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// LibVersion is the version of the lib/ modules whose Go side this `nomi`'s
// release carries prebuilt: the pseudo-version of the release's commit,
// which `nomi add` of a lib/ module records. scripts/release.sh sets it with
// `-ldflags -X github.com/nomi-language/nomi/internal/ffirun.LibVersion=<version>`;
// it is empty for every other build, which builds lib/ modules' Go like any
// other project's.
var LibVersion string

// libPrefix is the path prefix of the first-party lib/ modules.
const libPrefix = compilerModulePath + "/lib/"

// libModules are the lib/ modules whose Go bindings the prebuilt runners
// link, by their directory in the repository. A lib/ module with no `go`
// binding (migrate, jobs/postgres, storage/s3) needs no entry: a project
// using it alone has no Go bindings.
var libModules = []string{
	"lib/image",
	"lib/markdown",
	"lib/password_hashers",
	"lib/sql/postgres",
	"lib/sql/sqlite",
	"lib/sql/sqlkit",
}

// LibModules are the module paths of libModules.
func LibModules() []string {
	out := make([]string, len(libModules))
	for i, dir := range libModules {
		out[i] = compilerModulePath + "/" + dir
	}
	return out
}

// LibRunnersDir is the directory of the module the prebuilt lib runners are
// built from, in the repository.
const LibRunnersDir = "librunners"

// libWrapperName is the prebuilt lib wrapper's file name.
const libWrapperName = "nomi-lib"

// libSuffix ends the lib runner's file name, nomi-vm-lib.
const libSuffix = "-lib"

// libMainPath is the main package of the prebuilt binary name.
func libMainPath(name string) string {
	return compilerModulePath + "/" + LibRunnersDir + "/" + name
}

// GenerateLibMains generates the main packages of the prebuilt lib
// binaries from the lib/ modules of the repository at repoRoot, by their
// path in it: nomi-lib's and nomi-vm-lib's main.go under librunners/.
func GenerateLibMains(repoRoot string) (map[string][]byte, error) {
	projectRoot := filepath.Join(repoRoot, LibRunnersDir)
	byImportPath := map[string]*DiscoveredPackage{}
	for _, dir := range libModules {
		var files []string
		if err := collectNomiSourceFilesUnder(filepath.Join(repoRoot, filepath.FromSlash(dir)), &files); err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, f := range files {
			if err := discoverNomiFile(f, byImportPath); err != nil {
				return nil, err
			}
		}
	}
	discovered := finishDiscovery(byImportPath)
	if err := validateDiscoveredGoBindings(projectRoot, discovered); err != nil {
		return nil, err
	}
	relocate := func(file string) string {
		if file == "" {
			return ""
		}
		for _, dir := range libModules {
			root := filepath.Join(repoRoot, filepath.FromSlash(dir))
			if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
				return compilerModulePath + "/" + dir + "/" + filepath.ToSlash(rel)
			}
		}
		return filepath.ToSlash(file)
	}
	out := map[string][]byte{}
	for name, kind := range map[string]mainKind{
		libWrapperName:                   {},
		runnerBinaryBaseName + libSuffix: {Runner: true},
	} {
		src, err := renderMainRelocated(projectRoot, "the first-party lib/ modules", discovered, kind, relocate)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if strings.Contains(string(src), filepath.ToSlash(repoRoot)) || strings.Contains(string(src), repoRoot) {
			return nil, fmt.Errorf("%s: the generated source names %s, the machine's own path", name, repoRoot)
		}
		out[LibRunnersDir+"/"+name+"/main.go"] = src
	}
	return out, nil
}

// libMiss is why the prebuilt lib wrapper cannot run a program: its first
// binding that is not a lib/ module's at LibVersion, or the wrapper missing
// beside this `nomi`.
type libMiss struct {
	// file and line are the binding's declaration.
	file string
	line int
	// pkg is the Go package it binds.
	pkg string
	// module and version are its lib/ module and that module's version, when
	// it is one.
	module, version string
	// local is set for a lib/ module replaced with a directory.
	local bool
	// noWrapper is why no nomi-lib beside this `nomi` could be used.
	noWrapper string
}

func (m *libMiss) String() string {
	where := displayLibPath(m.file)
	if m.line > 0 {
		where = fmt.Sprintf("%s:%d", where, m.line)
	}
	switch {
	case m.noWrapper != "":
		return "this release's prebuilt runner for the lib/ modules cannot be used: " + m.noWrapper
	case m.local:
		return fmt.Sprintf("%s binds %s in %s from a directory on disk, and this nomi carries only the lib/ "+
			"modules' Go at %s from the module cache prebuilt", where, m.pkg, m.module, LibVersion)
	case m.module != "":
		return fmt.Sprintf("%s is at %s, and this nomi carries the lib/ modules' Go prebuilt at %s; "+
			"`nomi update` moves every lib/ module to that version", m.module, m.version, LibVersion)
	}
	return fmt.Sprintf("%s binds the Go package %s, and a release carries only the first-party lib/ modules' "+
		"Go prebuilt", where, m.pkg)
}

// displayLibPath shortens a module cache path to the module's path and the
// file's path in it.
func displayLibPath(file string) string {
	if i := strings.Index(filepath.ToSlash(file), "/"+libPrefix); i >= 0 {
		return filepath.ToSlash(file)[i+1:]
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, file); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return file
}

// needsGoError is a missing Go toolchain for a project the prebuilt lib
// runners could not serve: what needed Go, then the toolchain's account.
type needsGoError struct {
	miss *libMiss
	err  error
}

func (e *needsGoError) Error() string {
	return "this project's Go bindings need Go: " + e.miss.String() + ".\n" + e.err.Error()
}

func (e *needsGoError) Unwrap() error { return e.err }

// explainNeedsGo names r's libMiss on a missing toolchain or compiler
// source, which is the failure a release user meets when a binding needs Go.
func (r *Result) explainNeedsGo(err error) error {
	if err == nil || r == nil || r.libMiss == nil {
		return err
	}
	var notFound *gotoolchain.NotFoundError
	var explained *needsGoError
	if errors.As(err, &explained) || !errors.As(err, &notFound) && !errors.Is(err, errNoCompilerSource) {
		return err
	}
	return &needsGoError{miss: r.libMiss, err: err}
}

// prebuiltLib answers the prebuilt lib wrapper for discovered, or why there
// is none. With no LibVersion both are empty: this `nomi` carries none.
func prebuiltLib(discovered []DiscoveredPackage) (string, *libMiss) {
	if LibVersion == "" {
		return "", nil
	}
	if miss := libBindingsMiss(discovered); miss != nil {
		return "", miss
	}
	bin, why := libWrapper()
	if bin == "" {
		return "", &libMiss{noWrapper: why}
	}
	return bin, nil
}

// libBindingsMiss is the first binding in discovered that is not a lib/
// module's at LibVersion from the module cache, or nil.
func libBindingsMiss(discovered []DiscoveredPackage) *libMiss {
	known := map[string]bool{}
	for _, m := range LibModules() {
		known[m] = true
	}
	modules := map[string]libModuleDir{}
	check := func(pkg, file string, line int) *libMiss {
		dir := filepath.Dir(file)
		m, ok := modules[dir]
		if !ok {
			m = libModuleOf(dir)
			modules[dir] = m
		}
		miss := &libMiss{file: file, line: line, pkg: pkg, module: m.path, version: m.version}
		switch {
		case !known[m.path]:
			miss.module, miss.version = "", ""
			return miss
		case m.version == "":
			miss.local = true
			return miss
		case m.version != LibVersion:
			return miss
		}
		return nil
	}
	for _, pkg := range discovered {
		for _, t := range pkg.Types {
			if miss := check(pkg.ImportPath, t.SourceFile, t.SourceLine); miss != nil {
				return miss
			}
		}
		for _, e := range pkg.Exports {
			if miss := check(pkg.ImportPath, e.SourceFile, e.SourceLine); miss != nil {
				return miss
			}
		}
	}
	return nil
}

// libModuleDir is the Go module a file is in: its path, and its version when
// its directory is the module cache's copy of one (`<path>@<version>`).
type libModuleDir struct{ path, version string }

// libModuleOf is the module of the nearest go.mod at or above dir.
func libModuleOf(dir string) libModuleDir {
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			m := libModuleDir{path: modfile.ModulePath(data)}
			if _, v, ok := strings.Cut(filepath.Base(dir), "@"); ok {
				m.version = v
			}
			return m
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return libModuleDir{}
		}
		dir = parent
	}
}

// libWrapperSpec is nomi-lib as a release ships it, in each native
// platform's lib archive.
var libWrapperSpec = binSpec{name: libWrapperName, mainPath: libMainPath(libWrapperName), lib: true}

var (
	libDownloadMu      sync.Mutex
	libDownloadAllowed bool
	libDownloadOffline bool
	libDownloadLog     io.Writer
)

// AllowLibDownload lets this process download a release's lib archive when
// nomi-lib is not installed beside it, naming each download on log (nil is
// silent). Only the CLI calls it; offline refuses the download and says so.
func AllowLibDownload(log io.Writer, offline bool) {
	libDownloadMu.Lock()
	defer libDownloadMu.Unlock()
	libDownloadAllowed, libDownloadOffline, libDownloadLog = true, offline, log
}

var (
	libWrapperOnce sync.Once
	libWrapperBin  string
	libWrapperWhy  string
)

// libWrapper answers this platform's nomi-lib from this `nomi`'s release, or
// why there is none.
func libWrapper() (string, string) {
	libWrapperOnce.Do(func() {
		libWrapperBin, libWrapperWhy = findLibWrapper()
	})
	return libWrapperBin, libWrapperWhy
}

// findLibWrapper looks for nomi-lib beside this `nomi`, then in the cache of
// what was downloaded from its release, then downloads the platform's lib
// archive (releaseBinary).
func findLibWrapper() (string, string) {
	host := Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	bin, err := siblingBinary(host, libWrapperSpec, false)
	if err != nil {
		return "", err.Error()
	}
	if bin != "" {
		return bin, ""
	}
	if ReleaseVersion == "" {
		return "", "this nomi is not a release build, so there is no release to download " + libWrapperName + " from"
	}
	libDownloadMu.Lock()
	allowed, offline, log := libDownloadAllowed, libDownloadOffline, libDownloadLog
	libDownloadMu.Unlock()
	if cached := cachedReleaseBinary(host, libWrapperSpec); cached != "" {
		return cached, ""
	}
	switch {
	case !allowed:
		return "", libWrapperName + " is neither beside this nomi nor downloaded"
	case offline:
		return "", fmt.Sprintf("%s is neither beside this nomi nor downloaded, and --offline forbids downloading %s",
			libWrapperName, libArchiveName(ReleaseVersion, host))
	}
	bin, err = releaseBinary(host, libWrapperSpec, log)
	if err != nil {
		return "", strings.TrimPrefix(err.Error(), "ffirun: ")
	}
	return bin, ""
}

// libRunnerTarget reports whether a release ships nomi-vm-lib for target:
// every native platform it ships, and js/wasm.
func libRunnerTarget(target Target) bool {
	switch target.String() {
	case "darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64", "windows/amd64", "js/wasm":
		return true
	}
	return false
}

// libRunner answers nomi-vm-lib for target: beside this `nomi`, else
// downloaded from its release's lib archive for target (releaseBinary).
func libRunner(target Target) (string, error) {
	s := Variant{Lib: true}.spec()
	bin, err := siblingBinary(target, s, false)
	if err != nil || bin != "" {
		return bin, err
	}
	libDownloadMu.Lock()
	log := libDownloadLog
	libDownloadMu.Unlock()
	return releaseBinary(target, s, log)
}

// forTarget is r for a build for target, whose libMiss says that no lib
// runner ships for target when r's program is otherwise served by one.
func (r *Result) forTarget(target Target) *Result {
	if r == nil || r.prebuilt == "" || libRunnerTarget(target) {
		return r
	}
	c := *r
	c.libMiss = &libMiss{noWrapper: fmt.Sprintf("a release ships no lib runner for %s, since a lib/ module's Go does not build for it", target)}
	return &c
}
