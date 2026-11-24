package runners

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// CompilerModulePath is the compiler's own module, which a runner is built
// from: at the version this `nomi` was built as, or from a local tree.
const CompilerModulePath = "github.com/nomi-language/nomi"

// nomiModuleRoot locates the source tree this binary was compiled from, which
// a development build's runner is built from.
//
// A runner is built from the compiler's Go module, not just this binary. A
// versioned `nomi` gets that source from the module proxy. A development
// build has only the tree runtime.Caller names, which -trimpath hides and a
// moved checkout loses; both report ErrNoCompilerSource rather than the stat
// that failed, which names a path the user never wrote.
func nomiModuleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locating nomi module root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if !filepath.IsAbs(file) {
		return "", ErrNoCompilerSource
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", ErrNoCompilerSource
	}
	return root, nil
}

// compilerSourceEnv names a checkout of the compiler module that a runner
// is built from in place of whatever this `nomi` would otherwise use. It is
// for developing the compiler: a versioned `nomi` with it set links the
// checkout, and a development build with no source of its own can be pointed
// at one.
const compilerSourceEnv = "NOMI_COMPILER_SOURCE"

// compilerPlan is how a runner build reaches the compiler module, decided
// from this binary's build info and the environment alone, so it costs
// nothing to ask.
//
// A versioned plan names a published version of the module, which the Go
// toolchain fetches like any dependency. A local plan replaces the module with a directory on disk: Dir when the
// environment named one, otherwise the source tree this binary was compiled
// from (nomiModuleRoot).
type compilerPlan struct {
	Version string
	Dir     string
	// Why is set on a local plan with no Dir: why this binary is not
	// versioned, in the words ErrNoCompilerSource uses when the tree it was
	// built from is gone.
	Why string
}

func (p compilerPlan) versioned() bool { return p.Version != "" }

// planCompilerSource picks the plan for a binary with build info info, given
// the value of NOMI_COMPILER_SOURCE.
//
// The binary is versioned when its main module is the compiler's and its
// version is one the module proxy can serve: valid semver with no build
// metadata (`+dirty` marks a build from a modified checkout). That covers
// `go install github.com/nomi-language/nomi/cmd/nomi@<version>`, whose version
// may be a pseudo-version, and a release built in a clean checkout at its tag.
//
// A pseudo-version stamped from a checkout (the build info carries
// vcs.revision) is not versioned. Go stamps one on every clean build of an
// untagged commit, and that commit is usually not published, so the proxy
// could not serve it; the tree it was built from can.
func planCompilerSource(info *debug.BuildInfo, override string) compilerPlan {
	if override != "" {
		return compilerPlan{Dir: override}
	}
	if info == nil {
		return compilerPlan{Why: "it records no build information"}
	}
	if info.Main.Path != CompilerModulePath {
		return compilerPlan{Why: "it is not built as the compiler's own module"}
	}
	v := info.Main.Version
	if !semver.IsValid(v) {
		return compilerPlan{Why: "it is a development build with no version"}
	}
	if semver.Build(v) != "" {
		return compilerPlan{Why: fmt.Sprintf("it was built from a modified checkout (%s)", v)}
	}
	if module.IsPseudoVersion(v) && buildSetting(info, "vcs.revision") != "" {
		return compilerPlan{Why: fmt.Sprintf("it was built from a checkout at an untagged commit (%s)", v)}
	}
	return compilerPlan{Version: v}
}

func buildSetting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// currentPlan is this process's plan. A variable so a test can stand in a
// versioned `nomi`.
var currentPlan = sync.OnceValue(func() compilerPlan {
	info, _ := debug.ReadBuildInfo()
	return planCompilerSource(info, os.Getenv(compilerSourceEnv))
})

// CompilerSource is the compiler module a runner is built from: the
// directory holding its files, and its version when it is a published one.
// A versioned source's Dir is the module cache's copy of that version.
type CompilerSource struct {
	Dir     string
	Version string
}

var (
	sourceMu     sync.Mutex
	sourceCached *CompilerSource
)

// ResolveCompilerSource answers the compiler source for this process's plan.
// A versioned plan downloads the module with `go mod download` (a no-op once
// it is in the module cache), so it needs a Go toolchain, which a runner
// build needs anyway. Only a success is remembered: `nomi lsp` lives for a
// whole editing session, and a failed download should be retried.
func ResolveCompilerSource() (CompilerSource, error) {
	sourceMu.Lock()
	defer sourceMu.Unlock()
	if sourceCached != nil {
		return *sourceCached, nil
	}
	src, err := sourceForPlan(currentPlan())
	if err != nil {
		return CompilerSource{}, err
	}
	sourceCached = &src
	return src, nil
}

func sourceForPlan(p compilerPlan) (CompilerSource, error) {
	switch {
	case p.versioned():
		return downloadCompilerModule(p.Version)
	case p.Dir != "":
		return checkoutSource(p.Dir)
	}
	root, err := nomiModuleRoot()
	if err != nil {
		if errors.Is(err, ErrNoCompilerSource) && p.Why != "" {
			return CompilerSource{}, &noCompilerSourceError{why: p.Why}
		}
		return CompilerSource{}, err
	}
	return CompilerSource{Dir: root}, nil
}

// checkoutSource is the directory NOMI_COMPILER_SOURCE names, once it is
// known to hold the compiler module.
func checkoutSource(dir string) (CompilerSource, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return CompilerSource{}, fmt.Errorf("%s=%s: %w", compilerSourceEnv, dir, err)
	}
	path := filepath.Join(abs, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return CompilerSource{}, fmt.Errorf("%s=%s does not name a checkout of the compiler: %w",
			compilerSourceEnv, dir, err)
	}
	if got := modfile.ModulePath(data); got != CompilerModulePath {
		return CompilerSource{}, fmt.Errorf("%s=%s does not name a checkout of the compiler: "+
			"its go.mod declares module %q, not %q", compilerSourceEnv, dir, got, CompilerModulePath)
	}
	return CompilerSource{Dir: abs}, nil
}

// downloadCompilerModule puts version of the compiler module in the module
// cache through the user's own GOPROXY, GOFLAGS and checksum database, and
// answers where it landed with its hashes.
func downloadCompilerModule(version string) (CompilerSource, error) {
	goBin, err := findGo()
	if err != nil {
		return CompilerSource{}, fmt.Errorf("%w", err)
	}
	ref := CompilerModulePath + "@" + version
	cmd := exec.Command(goBin, "mod", "download", "-json", ref)
	// Outside any module, so a go.mod or go.work around the user's working
	// directory has no say in fetching the compiler.
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var got struct {
		Dir, Error string
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil && runErr == nil {
		return CompilerSource{}, fmt.Errorf("reading `go mod download -json %s`: %w", ref, err)
	}
	if runErr != nil || got.Error != "" || got.Dir == "" {
		detail := strings.TrimSpace(got.Error)
		if detail == "" {
			detail = strings.TrimSpace(stderr.String())
		}
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		return CompilerSource{}, fmt.Errorf("a nomi-vm is built from the compiler module %s, "+
			"and the Go toolchain could not download it: %s", ref, detail)
	}
	return CompilerSource{Dir: got.Dir, Version: version}, nil
}

// LocalCompilerRoot is the compiler's directory when this process links a
// local one, without downloading anything: "" for a versioned plan or a
// missing tree.
func LocalCompilerRoot() string {
	p := currentPlan()
	if p.versioned() {
		return ""
	}
	src, err := ResolveCompilerSource()
	if err != nil {
		return ""
	}
	return src.Dir
}

// ErrNoCompilerSource is what a runner build reports when this `nomi` is a
// development build and the tree it was built from is not on the machine.
// One value, so every probe says the same thing about one situation;
// noCompilerSourceError says it with the reason and matches it under
// errors.Is. It is written for a user: it says what to do and names nothing
// inside the compiler.
var ErrNoCompilerSource = errors.New("this `nomi` cannot find the compiler module a nomi-vm is built from. " + CompilerSourceAdvice)

type noCompilerSourceError struct{ why string }

func (e *noCompilerSourceError) Error() string {
	return "this `nomi` cannot find the compiler module a nomi-vm is built from: " + e.why +
		", and the source tree it was built from is gone. " + CompilerSourceAdvice
}

func (e *noCompilerSourceError) Is(target error) bool { return target == ErrNoCompilerSource }

// CompilerSourceAdvice is the part of every "no compiler source" refusal that
// says what to do.
const CompilerSourceAdvice = "A release `nomi` needs no compiler module, since its release ships every " +
	"nomi-vm. One installed with `go install github.com/nomi-language/nomi/cmd/nomi@<version>` fetches " +
	"the compiler module through Go. A development build links the checkout it was built from: keep " +
	"that checkout in place, or set " + compilerSourceEnv + " to a checkout of the compiler. " +
	"`nomi run`, `test` and `check` need neither."
