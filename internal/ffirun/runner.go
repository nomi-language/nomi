package ffirun

// `nomi build`'s runners: the executables a program's IR image is appended
// to.
//
// A pure-Nomi program's runner is cmd/nomi-vm, built from the compiler's
// own module and shared by every program: one per compiler identity, target
// and variant (with or without std/compiler's hosts). A wasip1 program with
// an `export fn` is appended to cmd/nomi-vm-reactor instead, the same VM
// linked with -buildmode=c-shared as a WASI reactor (Variant). An FFI project's runner
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
const cacheKindRunner = "vm"

const runnerBinaryBaseName = "nomi-vm"

// reactorBinaryBaseName is the wasip1 reactor's file name, in a cache
// directory and in the wasip1 release archive.
const reactorBinaryBaseName = "nomi-vm-reactor"

// Variant is which runner a pure-Nomi program is appended to.
type Variant struct {
	// Compiler links std/compiler's hosts, and with them the front end.
	Compiler bool
	// Reactor is the wasip1 reactor, cmd/nomi-vm-reactor: a module that
	// exports `_initialize` and the nomi_call ABI instead of `_start`, for a
	// program with an `export fn`. Only wasip1/wasm has one.
	Reactor bool
}

// name is what a runner directory's vm.json records for the variant.
func (v Variant) name() string {
	n := variantName(v.Compiler)
	if v.Reactor {
		n = "reactor-" + n
	}
	return n
}

// binaryName is the runner's file name before any executable suffix.
func (v Variant) binaryName() string {
	if v.Reactor {
		return reactorBinaryBaseName
	}
	return runnerBinaryBaseName
}

// pkg is the main package the runner is built from, relative to the
// compiler module's root.
func (v Variant) pkg() string {
	if v.Reactor {
		return "./cmd/nomi-vm-reactor"
	}
	return "./cmd/nomi-vm"
}

// mainPath is the main package a prebuilt runner of the variant must name
// in its build info.
func (v Variant) mainPath() string {
	if v.Reactor {
		return reactorMainPath
	}
	return runnerMainPath
}

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
	"link the whole front end into its nomi-vm. Releases ship only the plain nomi-vm, so this one is " +
	"built with `go build` from the compiler's own Go module, and this `nomi` is a development build " +
	"whose source tree is gone. A release `nomi` builds it with Go installed; a development build needs " +
	"its checkout, or " + compilerSourceEnv + " set to a checkout of the compiler")

// errNoSourceForProjectRunner is an FFI project built by a `nomi` with no
// compiler source.
var errNoSourceForProjectRunner = errors.New("this project has Go bindings, and `nomi build` links " +
	"them into the executable, so it cannot use the prebuilt nomi-vm. " + compilerSourceAdvice)

// ForBuild rewords an error Prepare returned in `nomi build`'s voice: the
// missing compiler source is reported as what an FFI project's runner needs
// it for, not as what `run` needs it for.
func ForBuild(err error) error {
	if errors.Is(err, errNoCompilerSource) {
		return errNoSourceForProjectRunner
	}
	return err
}

// NeedsCompilerSource reports whether Prepare failed because the program has
// Go bindings and this `nomi` has no compiler source to build them with.
func NeedsCompilerSource(err error) bool {
	return errors.Is(err, errNoCompilerSource)
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

// runnerRecord is what a runner directory's vm.json records. A runner is
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

// Runner answers the path of a pure-Nomi runner of variant v for target: a
// prebuilt runner installed
// beside this `nomi`, one built from the compiler's source and cached, or one
// downloaded from this `nomi`'s release, in that order (prebuilt.go). A
// download is announced on progress.
//
// A versioned `nomi` that is not a release archive (`go install ...@version`)
// has no release to download a runner from, so it builds one from the module
// version it fetches through Go, as it does for std/compiler's variant, which
// no release ships.
func Runner(target Target, v Variant, progress io.Writer) (string, error) {
	if v.Reactor && (target.GOOS != "wasip1" || target.GOARCH != "wasm") {
		return "", fmt.Errorf("ffirun: only wasip1/wasm has a reactor nomi-vm, not %s", target)
	}
	compiler := v.Compiler
	nomiRoot := localCompilerRoot()
	versionedBuild := currentPlan().versioned() && (compiler || ReleaseVersion == "")
	if !compiler {
		bin, err := siblingRunner(target, v, nomiRoot != "" || versionedBuild)
		if err != nil || bin != "" {
			return bin, err
		}
	}
	if nomiRoot != "" {
		return sourceRunner(nomiRoot, target, v)
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
		return sourceRunner(src.Dir, target, v)
	}
	if compiler {
		return "", errNoSourceForCompilerRunner
	}
	return releaseRunner(target, v, progress)
}

// sourceRunner builds the runner from the compiler's source at nomiRoot and
// caches it per compiler identity, target and variant. nomiRoot may be a
// version's copy in the module cache, which is read-only: -mod=readonly
// overrides a GOFLAGS=-mod=mod that would try to write its go.mod. The
// reactor is linked with -buildmode=c-shared, which makes a wasip1 module
// export `_initialize` and its //go:wasmexport functions instead of
// `_start`.
func sourceRunner(nomiRoot string, target Target, v Variant) (string, error) {
	id, err := compilerIdentity()
	if err != nil {
		return "", err
	}
	variant := v.name()
	dir, err := cacheDirForKey("nomi-vm\x00"+nomiRoot+"\x00"+variant+"\x00"+target.String(),
		nomiRoot, cacheKindRunner)
	if err != nil {
		return "", err
	}
	want := runnerRecord{Compiler: id, Target: target.String(), Variant: variant}
	bin := filepath.Join(dir, v.binaryName())
	if runnerCached(dir, bin, want) && (!target.needsWasmExec() || fileExists(filepath.Join(dir, WasmExecName))) {
		return bin, nil
	}
	args := []string{"build", "-trimpath", "-mod=readonly"}
	if v.Compiler {
		args = append(args, "-tags", "nomi_compiler")
	}
	if v.Reactor {
		args = append(args, "-buildmode=c-shared")
	}
	env := append(os.Environ(), "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "CGO_ENABLED=0", "GOWORK=off")
	if err := goBuildInto(gotoolchain.FindForBuild, dir, bin, nomiRoot, env, append(args, "-o"), v.pkg(), nil); err != nil {
		return "", err
	}
	if target.needsWasmExec() {
		if err := copyGorootWasmExec(gotoolchain.FindForBuild, dir, nomiRoot, env); err != nil {
			return "", err
		}
	}
	return bin, writeRunnerRecord(dir, want)
}

// WasmExecName is Go's JS support file for a js/wasm module. A js/wasm
// runner is cached with the copy from the Go toolchain that linked it beside
// it, because the file must match that Go version: sourceRunner copies it
// out of that toolchain's GOROOT, and a release ships it in the js/wasm
// archive (prebuilt.go). `nomi build --web` writes it next to the module.
const WasmExecName = "wasm_exec.js"

// needsWasmExec reports whether target's runner needs Go's JS support file.
func (t Target) needsWasmExec() bool { return t.GOOS == "js" && t.GOARCH == "wasm" }

// WasmExec answers the wasm_exec.js cached beside the js/wasm runner at
// runner.
func WasmExec(runner string) (string, error) {
	path := filepath.Join(filepath.Dir(runner), WasmExecName)
	if !fileExists(path) {
		return "", fmt.Errorf("ffirun: the js/wasm nomi-vm at %s has no %s beside it, and it must come from the Go "+
			"toolchain that built it. Delete that nomi-vm so `nomi build` fetches or builds it again",
			runner, WasmExecName)
	}
	return path, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// copyGorootWasmExec copies wasm_exec.js out of the GOROOT of the toolchain
// `go` resolves to in workDir under env, which is the one that just built
// the runner (a go.mod toolchain line selects it there too).
func copyGorootWasmExec(findGo func() (string, error), dir, workDir string, env []string) error {
	goBin, err := findGo()
	if err != nil {
		return err
	}
	cmd := exec.Command(goBin, "env", "GOROOT")
	cmd.Dir = workDir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ffirun: go env GOROOT: %w", err)
	}
	goroot := strings.TrimSpace(string(out))
	var data []byte
	// Go 1.24 moved it from misc/wasm to lib/wasm.
	for _, rel := range []string{"lib/wasm", "misc/wasm"} {
		if data, err = os.ReadFile(filepath.Join(goroot, rel, WasmExecName)); err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("ffirun: the Go toolchain at %s has no %s in lib/wasm: %w", goroot, WasmExecName, err)
	}
	return writeFileAtomic(filepath.Join(dir, WasmExecName), data, 0o644)
}

// writeFileAtomic writes data to path through a temporary file beside it.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
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
	dir, err := cacheDirForKey(r.projectRoot+"\x00vm\x00"+variant+"\x00"+target.String(),
		r.projectRoot, cacheKindRunner)
	if err != nil {
		return "", err
	}
	rec := runnerRecord{Compiler: want.Compiler, Target: target.String(), Variant: variant, Project: want}
	bin := filepath.Join(dir, runnerBinaryBaseName)
	if runnerCached(dir, bin, rec) && (!target.needsWasmExec() || fileExists(filepath.Join(dir, WasmExecName))) {
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
		// A bound package that does not build for target is reported at
		// its `gopkg`, not as the compiler's output (target.go).
		if problem := targetBuildProblem(r, dir, env, target, err); problem != nil {
			return "", problem
		}
		return "", err
	}
	// A js/wasm runner is loaded by the wasm_exec.js of the toolchain that
	// built it, as a pure-Nomi one is (sourceRunner).
	if target.needsWasmExec() {
		if err := copyGorootWasmExec(gotoolchain.FindForProjectRunner, dir, dir, env); err != nil {
			return "", err
		}
	}
	return bin, writeRunnerRecord(dir, rec)
}

// ImageBuild is the image BuildImageVM builds: one for a build for Target,
// refused as Command (`nomi build --web`) when the program reaches a
// capability class the target lacks and Allowed does not name
// (vmhost.Program.CheckTarget). Web builds the image a browser folder
// carries (vmhost.Program.BuildWebImage), with its exported functions, its
// TypeScript declarations and its locale files.
type ImageBuild struct {
	Command string
	Target  Target
	Web     bool
	Allowed []string
}

// BuiltImage is the image BuildImageVM built and what the wrapper reported
// about it.
type BuiltImage struct {
	Image []byte
	// UsesCompiler is set when the program reaches std/compiler's hosts.
	UsesCompiler bool
	// HasExports is set when the program declares an `export fn`.
	HasExports bool
	// UsesFiles is set, for a web build, when the program reaches the
	// `files` class (vmhost.WebFilesNote).
	UsesFiles bool
	// Declarations is a web build's nomi.d.ts (vmhost.Program.WebDeclarations).
	Declarations string
	// LocaleFiles are a web build's locale files by their path in the
	// folder, `locales/<tag>.nomil`.
	LocaleFiles map[string][]byte
}

// BuildImageVM lowers the FFI project's entry in its wrapper (the in-process
// compiler cannot bind the project's Go) and answers the image spec asks
// for. When the program cannot be built, ok is false and stderr is what the
// wrapper reported: a front-end error, `nomi run`'s BLOCKED lines, or the
// target's refusal.
func BuildImageVM(r *Result, entryAbsPath string, spec ImageBuild) (built BuiltImage, stderr string, ok bool, err error) {
	dir, err := os.MkdirTemp("", "nomi-image-")
	if err != nil {
		return built, "", false, err
	}
	defer os.RemoveAll(dir)
	kind := "image"
	if spec.Web {
		kind = "web"
	}
	args := append([]string{dir, kind, spec.Command, spec.Target.GOOS, spec.Target.GOARCH}, spec.Allowed...)
	cmd, err := commandMode(r, "vmimage", entryAbsPath, args...)
	if err != nil {
		return built, "", false, err
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if _, exited := err.(*exec.ExitError); exited {
			return built, cleanWrapperCommandOutput(r, errOut.String()) + "\n", false, nil
		}
		return built, "", false, fmt.Errorf("ffirun: launching wrapper: %w", err)
	}
	built.UsesCompiler = strings.Contains(out.String(), "__NOMI_IMAGE_USES_COMPILER")
	built.HasExports = strings.Contains(out.String(), "__NOMI_IMAGE_HAS_EXPORTS")
	built.UsesFiles = strings.Contains(out.String(), "__NOMI_IMAGE_USES_FILES")
	if built.Image, err = os.ReadFile(filepath.Join(dir, "image")); err != nil {
		return built, "", false, fmt.Errorf("ffirun: reading the wrapper's image: %w", err)
	}
	if !spec.Web {
		return built, "", true, nil
	}
	decls, err := os.ReadFile(filepath.Join(dir, "nomi.d.ts"))
	if err != nil {
		return built, "", false, fmt.Errorf("ffirun: reading the wrapper's nomi.d.ts: %w", err)
	}
	built.Declarations = string(decls)
	locales, _ := os.ReadDir(filepath.Join(dir, "locales"))
	for _, e := range locales {
		data, err := os.ReadFile(filepath.Join(dir, "locales", e.Name()))
		if err != nil {
			return built, "", false, fmt.Errorf("ffirun: reading the wrapper's locale file: %w", err)
		}
		if built.LocaleFiles == nil {
			built.LocaleFiles = map[string][]byte{}
		}
		built.LocaleFiles["locales/"+e.Name()] = data
	}
	return built, "", true, nil
}

func runnerCached(dir, bin string, want runnerRecord) bool {
	data, err := os.ReadFile(filepath.Join(dir, "vm.json"))
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
	tmp := filepath.Join(dir, "vm.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "vm.json"))
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
	_ = os.Remove(filepath.Join(dir, "vm.json"))
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
			return &goBuildFailure{err: wrapperCommandError(clean, "building nomi-vm", err, out), output: string(out)}
		}
		return &goBuildFailure{err: fmt.Errorf("ffirun: building nomi-vm: %w\n%s", err, strings.TrimRight(string(out), "\n")), output: string(out)}
	}
	if err := os.Rename(tmpPath, bin); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ffirun: caching nomi-vm: %w", err)
	}
	return nil
}
