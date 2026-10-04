package ffirun

// `nomi build`'s runners: the executables a program's IR image is appended
// to.
//
// A pure-Nomi program's runner is cmd/nomi-runner, built from the compiler's
// own module and shared by every program: one per compiler identity, target
// and variant (with or without std/compiler's hosts). An FFI project's runner
// is generated like its wrapper (renderMain with mainKind.Runner) so it links
// the project's Go bindings through the same generated adapters, and it is
// cached per project, target and variant.
//
// Both are keyed on the compiler identity (identity.go), because a runner is
// a binary linking the compiler's VM and rt: a warm cache must not serve one
// built from a different tree.
//
// A pure-Nomi program's runner can also come prebuilt: installed beside
// `nomi` by a release archive, or downloaded from that release for another
// target. prebuilt.go holds the lookup order and the commit check.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// cacheKindRunner is the kind origin.json records for a runner directory.
const cacheKindRunner = "runner"

const runnerBinaryBaseName = "nomi-runner"

// Target is the platform a runner is built for.
type Target struct {
	GOOS, GOARCH string
}

// ResolveTarget fills an empty half of a target from GOOS/GOARCH in the
// environment and then from this process's own platform, which is what
// `go build` would build for.
func ResolveTarget(goos, goarch string) Target {
	if goos == "" {
		goos = os.Getenv("GOOS")
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = os.Getenv("GOARCH")
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return Target{GOOS: goos, GOARCH: goarch}
}

func (t Target) String() string { return t.GOOS + "/" + t.GOARCH }

// errNoSourceForCompilerRunner is a program importing std/compiler built by a
// `nomi` with no compiler source: its runner links the front end, and
// releases ship only the plain runner (see prebuilt.go for the sizes).
var errNoSourceForCompilerRunner = fmt.Errorf("ffirun: this program imports std/compiler, whose hosts " +
	"link the whole front end into its runner. Releases ship only the plain runner, so this runner is " +
	"built with `go build` from the compiler's own Go module, and this `nomi` is a development build " +
	"whose source tree is gone. A release `nomi` builds it with Go installed; a development build needs " +
	"its checkout, or " + compilerSourceEnv + " set to a checkout of the compiler")

// errNoSourceForProjectRunner is an FFI project built by a `nomi` with no
// compiler source.
var errNoSourceForProjectRunner = errors.New("this project has Go bindings, and `nomi build` links " +
	"them into the executable, so it cannot use the prebuilt runner. " + compilerSourceAdvice)

// ForBuild rewords an error Prepare returned in `nomi build`'s voice: the
// missing compiler source is reported as what an FFI project's runner needs
// it for, not as what `run` needs it for.
func ForBuild(err error) error {
	if errors.Is(err, errNoCompilerSource) {
		return errNoSourceForProjectRunner
	}
	return err
}

// CheckProjectBuild reports, before anything is lowered, whether this `nomi`
// can build an FFI project's runner: it needs a Go toolchain and the
// compiler's module (a local tree, or a version Go can download), and the
// error says why.
func CheckProjectBuild() error {
	if !currentPlan().versioned() {
		if _, err := resolveCompilerSource(); err != nil {
			return ForBuild(err)
		}
	}
	if _, err := gotoolchain.FindForProjectRunner(); err != nil {
		return err
	}
	if _, err := resolveCompilerSource(); err != nil {
		return ForBuild(err)
	}
	return nil
}

// runnerRecord is what a runner directory's runner.json records. A runner is
// reused only when every field matches.
type runnerRecord struct {
	Compiler string     `json:"compiler"`
	Target   string     `json:"target"`
	Variant  string     `json:"variant"`
	Project  hashRecord `json:"project"`
}

func variantName(compiler bool) string {
	if compiler {
		return "compiler"
	}
	return "plain"
}

// Runner answers the path of a pure-Nomi runner for target, with
// std/compiler's hosts when compiler is set: a prebuilt runner installed
// beside this `nomi`, one built from the compiler's source and cached, or one
// downloaded from this `nomi`'s release, in that order (prebuilt.go). A
// download is announced on progress.
//
// A versioned `nomi` that is not a release archive (`go install ...@version`)
// has no release to download a runner from, so it builds one from the module
// version it fetches through Go, as it does for std/compiler's variant, which
// no release ships.
func Runner(target Target, compiler bool, progress io.Writer) (string, error) {
	nomiRoot := localCompilerRoot()
	versionedBuild := currentPlan().versioned() && (compiler || ReleaseVersion == "")
	if !compiler {
		bin, err := siblingRunner(target, nomiRoot != "" || versionedBuild)
		if err != nil || bin != "" {
			return bin, err
		}
	}
	if nomiRoot != "" {
		return sourceRunner(nomiRoot, target, compiler)
	}
	if versionedBuild {
		// Asked first so a missing toolchain is reported in `nomi build`'s
		// voice rather than the download's.
		if _, err := gotoolchain.FindForBuild(); err != nil {
			return "", err
		}
		src, err := resolveCompilerSource()
		if err != nil {
			return "", err
		}
		return sourceRunner(src.Dir, target, compiler)
	}
	if compiler {
		return "", errNoSourceForCompilerRunner
	}
	return releaseRunner(target, progress)
}

// sourceRunner builds the runner from the compiler's source at nomiRoot and
// caches it per compiler identity, target and variant. nomiRoot may be a
// version's copy in the module cache, which is read-only: -mod=readonly
// overrides a GOFLAGS=-mod=mod that would try to write its go.mod.
func sourceRunner(nomiRoot string, target Target, compiler bool) (string, error) {
	id, err := compilerIdentity()
	if err != nil {
		return "", err
	}
	variant := variantName(compiler)
	dir, err := cacheDirForKey("nomi-runner\x00"+nomiRoot+"\x00"+variant+"\x00"+target.String(),
		nomiRoot, cacheKindRunner)
	if err != nil {
		return "", err
	}
	want := runnerRecord{Compiler: id, Target: target.String(), Variant: variant}
	bin := filepath.Join(dir, runnerBinaryBaseName)
	if runnerCached(dir, bin, want) {
		return bin, nil
	}
	args := []string{"build", "-trimpath", "-mod=readonly"}
	if compiler {
		args = append(args, "-tags", "nomi_compiler")
	}
	env := append(os.Environ(), "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "CGO_ENABLED=0", "GOWORK=off")
	if err := goBuildInto(gotoolchain.FindForBuild, dir, bin, nomiRoot, env, append(args, "-o"), "./cmd/nomi-runner", nil); err != nil {
		return "", err
	}
	return bin, writeRunnerRecord(dir, want)
}

// ProjectRunner answers the path of the cached runner for r's FFI project:
// the project's Go bindings and their generated adapters linked beside the
// VM, built for target.
func ProjectRunner(r *Result, target Target, compiler bool) (string, error) {
	if r == nil || r.FastPath {
		return "", fmt.Errorf("ffirun: ProjectRunner called on a FastPath result")
	}
	want, err := computeHashes(r.projectRoot, r.discovered, r.allRequires)
	if err != nil {
		return "", err
	}
	variant := variantName(compiler)
	dir, err := cacheDirForKey(r.projectRoot+"\x00runner\x00"+variant+"\x00"+target.String(),
		r.projectRoot, cacheKindRunner)
	if err != nil {
		return "", err
	}
	rec := runnerRecord{Compiler: want.Compiler, Target: target.String(), Variant: variant, Project: want}
	bin := filepath.Join(dir, runnerBinaryBaseName)
	if runnerCached(dir, bin, rec) {
		return bin, nil
	}
	src, err := renderMain(r.projectRoot, r.discovered, mainKind{Runner: true, Compiler: compiler})
	if err != nil {
		return "", err
	}
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, src, 0o644); err != nil {
		return "", fmt.Errorf("ffirun: writing runner main.go: %w", err)
	}
	if err := writeAbsolutizedGoMod(r.projectRoot, filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("ffirun: staging runner go.mod: %w", err)
	}
	if err := stageGoSum(r.projectRoot, filepath.Join(dir, "go.sum")); err != nil {
		return "", fmt.Errorf("ffirun: staging go.sum: %w", err)
	}
	env := append(os.Environ(), "GOOS="+target.GOOS, "GOARCH="+target.GOARCH)
	clean := &Result{WrapperPath: mainPath, WrapperDir: dir}
	if err := goBuildInto(gotoolchain.FindForProjectRunner, dir, bin, dir, env, []string{"build", "-mod=mod", "-trimpath", "-o"}, mainPath, clean); err != nil {
		return "", err
	}
	return bin, writeRunnerRecord(dir, rec)
}

// BuildImageVM lowers the FFI project's entry in its wrapper (the in-process
// compiler cannot bind the project's Go) and writes its IR image to
// imagePath. When the program cannot be built, ok is false and stderr is what
// the wrapper reported: a front-end error, or `nomi run`'s BLOCKED lines.
func BuildImageVM(r *Result, entryAbsPath, imagePath string) (usesCompiler bool, stderr string, ok bool, err error) {
	cmd, err := commandMode(r, "vmimage", entryAbsPath, imagePath)
	if err != nil {
		return false, "", false, err
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if _, exited := err.(*exec.ExitError); exited {
			return false, cleanWrapperCommandOutput(r, errOut.String()) + "\n", false, nil
		}
		return false, "", false, fmt.Errorf("ffirun: launching wrapper: %w", err)
	}
	return strings.Contains(out.String(), "__NOMI_IMAGE_USES_COMPILER"), "", true, nil
}

func runnerCached(dir, bin string, want runnerRecord) bool {
	data, err := os.ReadFile(filepath.Join(dir, "runner.json"))
	if err != nil {
		return false
	}
	var got runnerRecord
	if json.Unmarshal(data, &got) != nil || got != want {
		return false
	}
	_, err = os.Stat(bin)
	return err == nil
}

func writeRunnerRecord(dir string, rec runnerRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "runner.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "runner.json"))
}

// goBuildInto runs `go <args> <tmp> <pkg>` in workDir and moves the result
// to bin, so a failed or concurrent build never leaves a half-written runner
// where a warm lookup would take it. clean, when set, rewrites the generated
// module's paths out of a compile error.
func goBuildInto(findGo func() (string, error), dir, bin, workDir string, env, args []string, pkg string, clean *Result) error {
	goBin, err := findGo()
	if err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, "runner.json"))
	tmp, err := os.CreateTemp(dir, runnerBinaryBaseName+".*.tmp")
	if err != nil {
		return fmt.Errorf("ffirun: creating runner temp file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	cmd := exec.Command(goBin, append(append(args, tmpPath), pkg)...)
	cmd.Dir = workDir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		if clean != nil {
			return wrapperCommandError(clean, "building the runner", err, out)
		}
		return fmt.Errorf("ffirun: building the runner: %w\n%s", err, strings.TrimRight(string(out), "\n"))
	}
	if err := os.Rename(tmpPath, bin); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ffirun: caching the runner: %w", err)
	}
	return nil
}
