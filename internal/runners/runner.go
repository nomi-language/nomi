package runners

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nomi-language/nomi/internal/stdlibbindings/families"
)

// CacheKindRunner is the kind origin.json records for a runner directory.
const CacheKindRunner = "vm"

const runnerBinaryBaseName = "nomi-vm"

// reactorBinaryBaseName is the wasip1 reactor's file name, in a cache
// directory and in the wasip1 release archive.
const reactorBinaryBaseName = "nomi-vm-reactor"

// Variant is which runner a program is appended to.
type Variant struct {
	// Compiler links std/compiler's hosts, and with them the front end.
	Compiler bool
	// Reactor is the wasip1 reactor, cmd/nomi-vm-reactor: a module that
	// exports `_initialize` and the nomi_call ABI instead of `_start`, for a
	// program with an `export fn`. Only wasip1/wasm has one.
	Reactor bool
	// NoNetwork is the lean runner, built with every optional family's tag
	// (families.LeanTags): the crossings of the network and of
	// WebAssembly modules (stdlibadapters.Families) answer an error instead
	// of linking net/http, crypto/tls, net's sockets and wazero, so it is a
	// third smaller. `nomi build` picks it for a program that reaches none
	// of them. std/compiler's runner has no lean variant (Runner refuses
	// one): the front end it links is most of its size.
	NoNetwork bool
}

// Name is what a runner directory's vm.json records for the variant.
func (v Variant) Name() string {
	n := "plain"
	if v.Compiler {
		n = "compiler"
	}
	if v.Reactor {
		n = "reactor-" + n
	}
	if v.NoNetwork {
		n += "-nonet"
	}
	return n
}

// BinaryName is the runner's file name before any executable suffix, in a
// cache directory, beside `nomi` and in a release archive: nomi-vm,
// nomi-vm-reactor, then -compiler for std/compiler's variant or -nonet for
// the lean one (nomi-vm-reactor-nonet).
func (v Variant) BinaryName() string {
	name := runnerBinaryBaseName
	if v.Reactor {
		name = reactorBinaryBaseName
	}
	if v.Compiler {
		name += "-compiler"
	}
	if v.NoNetwork {
		name += "-nonet"
	}
	return name
}

// buildTags is the -tags value the variant is built with, or "". A
// prebuilt runner's build info records it, and checkRunner tells the
// variants apart by it.
func (v Variant) buildTags() string {
	var tags []string
	if v.Compiler {
		tags = append(tags, compilerTag)
	}
	if v.NoNetwork {
		tags = append(tags, families.LeanTags()...)
	}
	return strings.Join(tags, ",")
}

// compilerTag links std/compiler's hosts, and with them the front end, into
// a runner.
const compilerTag = "nomi_compiler"

// FamiliesOnTarget refuses a program that reaches an optional family
// (vmhost.Program.UsesFamilies) that no runner for target has.
func FamiliesOnTarget(reached []string, target Target) error {
	return families.OnTarget(reached, target.GOOS, target.String())
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

// HostTarget is the platform this process runs on, which a build is for
// when its command names no --target. No environment variable changes it:
// GOOS and GOARCH are set only in the environment of the `go build` that
// cross-builds a runner from source (sourceRunner).
func HostTarget() Target { return Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH} }

func (t Target) String() string { return t.GOOS + "/" + t.GOARCH }

// Record is what a runner directory's vm.json records. A runner is reused
// only when every field matches.
type Record struct {
	Compiler string `json:"compiler"`
	Target   string `json:"target"`
	Variant  string `json:"variant"`
}

// Runner answers the path of a runner of variant v for target: the one this
// `nomi` carries, unpacked into the cache; one installed beside this `nomi`;
// one built from the compiler's source and cached; or one from the release
// this `nomi` was cut as, cached or downloaded, in that order (prebuilt.go).
// A download is announced on progress.
//
// A release `nomi` (ReleaseVersion set) builds no runner and never runs
// `go`: it carries its own platform's two runners, and its release ships
// every other prebuilt. Any other `nomi` builds one from the compiler's
// source: its checkout, or for a versioned build (`go install ...@version`)
// that version of the module, which Go fetches.
func Runner(target Target, v Variant, progress io.Writer) (string, error) {
	if v.Reactor && (target.GOOS != "wasip1" || target.GOARCH != "wasm") {
		return "", fmt.Errorf("only wasip1/wasm has a reactor nomi-vm, not %s", target)
	}
	if v.Compiler && v.NoNetwork {
		return "", fmt.Errorf("std/compiler's runner has no lean variant: a program that imports std/compiler "+
			"is appended to %s", Variant{Compiler: true, Reactor: v.Reactor}.BinaryName())
	}
	if embeddedRunner(target, v) != nil {
		return releaseRunner(target, v, progress)
	}
	nomiRoot, versioned := "", false
	if ReleaseVersion == "" {
		nomiRoot = LocalCompilerRoot()
		versioned = currentPlan().versioned()
	}
	bin, err := siblingRunner(target, v, nomiRoot != "" || versioned)
	if err != nil || bin != "" {
		return bin, err
	}
	switch {
	case nomiRoot != "":
		return sourceRunner(nomiRoot, target, v)
	case versioned:
		// Asked first so a missing toolchain is reported in `nomi build`'s
		// voice rather than the download's.
		if _, err := findGo(); err != nil {
			return "", err
		}
		src, err := ResolveCompilerSource()
		if err != nil {
			return "", err
		}
		return sourceRunner(src.Dir, target, v)
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
	id, err := CompilerIdentity()
	if err != nil {
		return "", err
	}
	variant := v.Name()
	dir, err := CacheDirForKey("nomi-vm\x00"+nomiRoot+"\x00"+variant+"\x00"+target.String(),
		nomiRoot, CacheKindRunner)
	if err != nil {
		return "", err
	}
	want := Record{Compiler: id, Target: target.String(), Variant: variant}
	bin := filepath.Join(dir, v.BinaryName())
	if Cached(dir, bin, target, want) {
		return bin, nil
	}
	args := []string{"build", "-trimpath", "-mod=readonly"}
	if tags := v.buildTags(); tags != "" {
		args = append(args, "-tags", tags)
	}
	if v.Reactor {
		args = append(args, "-buildmode=c-shared")
	}
	env := append(os.Environ(), "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "CGO_ENABLED=0", "GOWORK=off")
	if err := GoBuildInto(dir, bin, nomiRoot, env, append(args, "-o"), v.pkg(), nil); err != nil {
		return "", err
	}
	if target.NeedsWasmExec() {
		if err := CopyGorootWasmExec(dir, nomiRoot, env); err != nil {
			return "", err
		}
	}
	return bin, WriteRecord(dir, want)
}

// WasmExecName is Go's JS support file for a js/wasm module. A js/wasm
// runner is cached with the copy from the Go toolchain that linked it beside
// it, because the file must match that Go version: sourceRunner copies it
// out of that toolchain's GOROOT, and a release ships it in the js/wasm
// archive (prebuilt.go). `nomi build --web` writes it next to the module.
const WasmExecName = "wasm_exec.js"

// NeedsWasmExec reports whether target's runner needs Go's JS support file.
func (t Target) NeedsWasmExec() bool { return t.GOOS == "js" && t.GOARCH == "wasm" }

// WasmExec answers the wasm_exec.js cached beside the js/wasm runner at
// runner.
func WasmExec(runner string) (string, error) {
	path := filepath.Join(filepath.Dir(runner), WasmExecName)
	if !fileExists(path) {
		return "", fmt.Errorf("the js/wasm nomi-vm at %s has no %s beside it. "+
			"Delete that nomi-vm so `nomi build` fetches or builds it again",
			runner, WasmExecName)
	}
	return path, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// CopyGorootWasmExec copies wasm_exec.js into dir out of the GOROOT of the
// toolchain `go` resolves to in workDir under env, which is the one that
// just built the runner (a go.mod toolchain line selects it there too).
func CopyGorootWasmExec(dir, workDir string, env []string) error {
	goBin, err := findGo()
	if err != nil {
		return err
	}
	cmd := exec.Command(goBin, "env", "GOROOT")
	cmd.Dir = workDir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go env GOROOT: %w", err)
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
		return fmt.Errorf("the Go toolchain at %s has no %s in lib/wasm: %w", goroot, WasmExecName, err)
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

// Cached reports whether the runner directory dir holds bin, built for
// target, with a vm.json that matches want in every field, and for js/wasm
// the wasm_exec.js beside it. want is a Record, or a record that embeds one
// and adds fields of its own.
func Cached[R comparable](dir, bin string, target Target, want R) bool {
	data, err := os.ReadFile(filepath.Join(dir, "vm.json"))
	if err != nil {
		return false
	}
	var got R
	if json.Unmarshal(data, &got) != nil || got != want {
		return false
	}
	if _, err := os.Stat(bin); err != nil {
		return false
	}
	return !target.NeedsWasmExec() || fileExists(filepath.Join(dir, WasmExecName))
}

// WriteRecord writes rec as dir's vm.json, through a temporary file.
func WriteRecord[R any](dir string, rec R) error {
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

// GoBuildFailure is a failed `go build` of a runner and what it printed.
type GoBuildFailure struct {
	Err    error
	Output string
}

func (e *GoBuildFailure) Error() string { return e.Err.Error() }
func (e *GoBuildFailure) Unwrap() error { return e.Err }

// GoBuildInto runs `go <args> <tmp> <pkg>` in workDir and moves the result
// to bin, so a failed or concurrent build never leaves a half-written runner
// where a warm lookup would take it. A failure is a *GoBuildFailure whose
// error is failed's wording of the command's error and output, or, with
// failed nil, the runner build's own.
func GoBuildInto(dir, bin, workDir string, env, args []string, pkg string,
	failed func(err error, out []byte) error) error {
	goBin, err := findGo()
	if err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, "vm.json"))
	tmp, err := os.CreateTemp(dir, runnerBinaryBaseName+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating runner temp file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	cmd := exec.Command(goBin, append(append(args, tmpPath), pkg)...)
	cmd.Dir = workDir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		if failed != nil {
			return &GoBuildFailure{Err: failed(err, out), Output: string(out)}
		}
		return &GoBuildFailure{Err: fmt.Errorf("building nomi-vm: %w\n%s", err, strings.TrimRight(string(out), "\n")), Output: string(out)}
	}
	if err := os.Rename(tmpPath, bin); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("caching nomi-vm: %w", err)
	}
	return nil
}
