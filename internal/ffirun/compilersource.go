package ffirun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// compilerSourceEnv names a checkout of the compiler module that a wrapper
// builds against in place of whatever this `nomi` would otherwise use. It is
// for developing the compiler: a versioned `nomi` with it set links the
// checkout, and a development build with no source of its own can be pointed
// at one.
const compilerSourceEnv = "NOMI_COMPILER_SOURCE"

// compilerPlan is how a wrapper reaches the compiler module, decided from this
// binary's build info and the environment alone, so it costs nothing to ask.
//
// A versioned plan names a published version of the module: the wrapper's
// go.mod requires it and the Go toolchain fetches it like any dependency. A
// local plan replaces the module with a directory on disk: Dir when the
// environment named one, otherwise the source tree this binary was compiled
// from (nomiModuleRoot).
type compilerPlan struct {
	Version string
	Dir     string
	// Why is set on a local plan with no Dir: why this binary is not
	// versioned, in the words errNoCompilerSource uses when the tree it was
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
	if info.Main.Path != compilerModulePath {
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

// CompilerModuleVersion is the version of the compiler module that a `nomi`
// with build info info builds Go bindings against, fetched through the Go
// toolchain, or "" when it links a local source tree instead.
func CompilerModuleVersion(info *debug.BuildInfo) string {
	return planCompilerSource(info, "").Version
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

// compilerSource is the compiler module as a wrapper build sees it: a
// directory holding its files, which the generator reads (std/, go.mod,
// go.sum), and how the wrapper's go.mod names it. A versioned source's Dir is
// the module cache's copy of that version, and Sum and GoModSum are its
// go.sum hashes.
type compilerSource struct {
	Dir      string
	Version  string
	Sum      string
	GoModSum string
}

func (s compilerSource) versioned() bool { return s.Version != "" }

var (
	sourceMu     sync.Mutex
	sourceCached *compilerSource
)

// resolveCompilerSource answers the compiler source for this process's plan.
// A versioned plan downloads the module with `go mod download` (a no-op once
// it is in the module cache), so it needs a Go toolchain, which a wrapper
// build needs anyway. Only a success is remembered: `nomi-lsp` lives for a
// whole editing session, and a failed download should be retried.
func resolveCompilerSource() (compilerSource, error) {
	sourceMu.Lock()
	defer sourceMu.Unlock()
	if sourceCached != nil {
		return *sourceCached, nil
	}
	src, err := sourceForPlan(currentPlan())
	if err != nil {
		return compilerSource{}, err
	}
	sourceCached = &src
	return src, nil
}

func sourceForPlan(p compilerPlan) (compilerSource, error) {
	switch {
	case p.versioned():
		return downloadCompilerModule(p.Version)
	case p.Dir != "":
		return checkoutSource(p.Dir)
	}
	root, err := nomiModuleRoot()
	if err != nil {
		if errors.Is(err, errNoCompilerSource) && p.Why != "" {
			return compilerSource{}, &noCompilerSourceError{why: p.Why}
		}
		return compilerSource{}, err
	}
	return compilerSource{Dir: root}, nil
}

// checkoutSource is the directory NOMI_COMPILER_SOURCE names, once it is
// known to hold the compiler module.
func checkoutSource(dir string) (compilerSource, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return compilerSource{}, fmt.Errorf("ffirun: %s=%s: %w", compilerSourceEnv, dir, err)
	}
	path := filepath.Join(abs, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return compilerSource{}, fmt.Errorf("ffirun: %s=%s does not name a checkout of the compiler: %w",
			compilerSourceEnv, dir, err)
	}
	if got := modfile.ModulePath(data); got != compilerModulePath {
		return compilerSource{}, fmt.Errorf("ffirun: %s=%s does not name a checkout of the compiler: "+
			"its go.mod declares module %q, not %q", compilerSourceEnv, dir, got, compilerModulePath)
	}
	return compilerSource{Dir: abs}, nil
}

// downloadCompilerModule puts version of the compiler module in the module
// cache through the user's own GOPROXY, GOFLAGS and checksum database, and
// answers where it landed with its hashes.
func downloadCompilerModule(version string) (compilerSource, error) {
	goBin, err := gotoolchain.FindForFFIWrapper()
	if err != nil {
		return compilerSource{}, fmt.Errorf("ffirun: %w", err)
	}
	ref := compilerModulePath + "@" + version
	cmd := exec.Command(goBin, "mod", "download", "-json", ref)
	// Outside any module, so a go.mod or go.work around the user's working
	// directory has no say in fetching the compiler.
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var got struct {
		Dir, Sum, GoModSum, Error string
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil && runErr == nil {
		return compilerSource{}, fmt.Errorf("ffirun: reading `go mod download -json %s`: %w", ref, err)
	}
	if runErr != nil || got.Error != "" || got.Dir == "" {
		detail := strings.TrimSpace(got.Error)
		if detail == "" {
			detail = strings.TrimSpace(stderr.String())
		}
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		return compilerSource{}, fmt.Errorf("ffirun: this project has Go bindings, which build against "+
			"the compiler module %s, and the Go toolchain could not download it: %s", ref, detail)
	}
	return compilerSource{Dir: got.Dir, Version: version, Sum: got.Sum, GoModSum: got.GoModSum}, nil
}

// localCompilerRoot is the compiler's directory when this process links a
// local one, without downloading anything: "" for a versioned plan or a
// missing tree.
func localCompilerRoot() string {
	p := currentPlan()
	if p.versioned() {
		return ""
	}
	src, err := resolveCompilerSource()
	if err != nil {
		return ""
	}
	return src.Dir
}

// errNoCompilerSource is what `nomi run`, `nomi test` and `nomi check` report
// for a project with Go FFI when this `nomi` is a development build and the
// tree it was built from is not on the machine. One value, so every probe
// says the same thing about one situation; noCompilerSourceError says it with
// the reason and matches it under errors.Is. It is written for a user: it says
// what to do and names nothing inside the compiler.
var errNoCompilerSource = errors.New("this project has Go bindings. " + compilerSourceAdvice)

type noCompilerSourceError struct{ why string }

func (e *noCompilerSourceError) Error() string {
	return "this project has Go bindings, and this `nomi` cannot find the compiler module to build them " +
		"against: " + e.why + ", and the source tree it was built from is gone. " + compilerSourceAdvice
}

func (e *noCompilerSourceError) Is(target error) bool { return target == errNoCompilerSource }

// compilerSourceAdvice is the part of every "no compiler source" refusal that
// says what to do.
const compilerSourceAdvice = "A release `nomi`, or one installed with " +
	"`go install github.com/nomi-language/nomi/cmd/nomi@<version>`, fetches the compiler module " +
	"through the Go toolchain and needs only Go installed. A development build links the checkout " +
	"it was built from: keep that checkout in place, or set " + compilerSourceEnv + " to a checkout " +
	"of the compiler. A project with no Go bindings needs neither."
