package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

// `nomi build <entry.nomi> -o <out>`: a single executable, the way `deno
// compile` and `bun build --compile` make one. It lowers the program as `nomi
// run` does, encodes its linked IR (ir.EncodeImage), and writes a cached
// runner binary with the image and a trailer appended (nomi/vmrunner); a
// WebAssembly runner gets the image as a custom section instead. The
// binary runs at `nomi run` speed: it skips the front end and the lowering at
// startup and runs the same VM over the same IR. It is not native code.
//
// `--no-vm` writes the encoded IR alone as a compiled program (.nomic), for a
// nomi-vm that does not embed it, and `--vm` writes that bare nomi-vm
// (vmrunner/imagefile.go).
//
// For wasip1/wasm the program picks the module's shape, with no flag: one
// with no `export fn` is a WASI command module (nomi-vm, `_start` runs
// main), and one with at least one `export fn` is a WASI reactor
// (nomi-vm-reactor: `_initialize`, nomi_alloc, nomi_call, nomi_free and
// nomi_main), whose image carries the exported functions as `--web`'s does.
//
// A program the front end rejects, or one the VM would block, is refused with
// the text `nomi run` prints, and nothing is written.

const buildUsage = "usage: nomi build <file.nomi> [-o <out>] [--target <goos>/<goarch> | --web [--static <dir>] | --no-vm]\n" +
	"       nomi build --vm [--compiler] [-o <out>] [--target <goos>/<goarch>]"

// errBuildRefused is a build that already reported why on stderr.
var errBuildRefused = errors.New("build refused")

type buildArgs struct {
	entry  string
	out    string
	target ffirun.Target
	// web writes a folder that runs the program in a browser (build_web.go).
	web bool
	// static is --static: the folder whose files --web copies in, in place
	// of the program's web/ folder.
	static string
	// noStatic leaves the static files out, for `nomi run --web`, which
	// serves them from where they are.
	noStatic bool
	// noVM writes the program alone as a .nomic file, for a nomi-vm that
	// does not embed it (vmrunner/imagefile.go).
	noVM bool
	// vm writes a copy of nomi-vm itself, with no program; compiler picks
	// the one that links std/compiler's hosts.
	vm       bool
	compiler bool
}

func parseBuildArgs(args []string) (buildArgs, error) {
	var b buildArgs
	var goos, goarch string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("nomi build: %s needs a value\n%s", arg, buildUsage)
			}
			i++
			return args[i], nil
		}
		switch {
		case arg == "-o":
			v, err := value()
			if err != nil {
				return b, err
			}
			b.out = v
		case strings.HasPrefix(arg, "-o="):
			b.out = strings.TrimPrefix(arg, "-o=")
		case arg == "--target":
			v, err := value()
			if err != nil {
				return b, err
			}
			var ok bool
			if goos, goarch, ok = strings.Cut(v, "/"); !ok || goos == "" || goarch == "" {
				return b, fmt.Errorf("nomi build: --target takes <goos>/<goarch>, such as linux/amd64; got %q", v)
			}
		case arg == "--web":
			b.web = true
		case arg == "--no-vm":
			b.noVM = true
		case arg == "--vm":
			b.vm = true
		case arg == "--compiler":
			b.compiler = true
		case arg == "--static" || strings.HasPrefix(arg, "--static="):
			v, inline := strings.CutPrefix(arg, "--static=")
			if !inline {
				var err error
				if v, err = value(); err != nil {
					return b, err
				}
			}
			if v == "" {
				return b, fmt.Errorf("nomi build: --static takes a folder\n%s", buildUsage)
			}
			b.static = v
		case isFlag(arg):
			return b, unknownFlag("nomi build", arg, buildUsage)
		case b.entry == "":
			b.entry = arg
		default:
			return b, fmt.Errorf("nomi build: unexpected argument %q\n%s", arg, buildUsage)
		}
	}
	if b.compiler && !b.vm {
		return b, fmt.Errorf("nomi build: --compiler needs --vm\n%s", buildUsage)
	}
	if b.vm {
		switch {
		case b.entry != "":
			return b, fmt.Errorf("nomi build --vm: nomi-vm carries no program, so it takes no file (%s)\n%s", b.entry, buildUsage)
		case b.web || b.noVM || b.static != "":
			return b, fmt.Errorf("nomi build: --vm takes only -o, --target and --compiler\n%s", buildUsage)
		}
		b.target = ffirun.ResolveTarget(goos, goarch)
		if b.out == "" {
			b.out = defaultExecutableName("nomi-vm", b.target)
		}
		return b, nil
	}
	if b.entry == "" {
		return b, errors.New(buildUsage)
	}
	if b.noVM {
		switch {
		case b.web || b.static != "":
			return b, fmt.Errorf("nomi build: --no-vm and --web are different outputs; pick one\n%s", buildUsage)
		case goos != "":
			return b, fmt.Errorf("nomi build --no-vm: a compiled program runs on every target's nomi-vm, so it takes no --target\n%s", buildUsage)
		}
		if b.out == "" {
			b.out = strings.TrimSuffix(filepath.Base(b.entry), ".nomi") + ".nomic"
		}
		return b, nil
	}
	if b.static != "" && !b.web {
		return b, fmt.Errorf("nomi build: --static needs --web\n%s", buildUsage)
	}
	if b.web {
		if goos != "" && (goos != "js" || goarch != "wasm") {
			return b, fmt.Errorf("nomi build: --web builds for js/wasm, so it cannot take --target %s/%s", goos, goarch)
		}
		b.target = ffirun.Target{GOOS: "js", GOARCH: "wasm"}
		if b.out == "" {
			b.out = strings.TrimSuffix(filepath.Base(b.entry), ".nomi") + "-web"
		}
		return b, nil
	}
	b.target = ffirun.ResolveTarget(goos, goarch)
	if b.out == "" {
		// go build's default: the entry's base name, in the working
		// directory.
		b.out = defaultExecutableName(strings.TrimSuffix(filepath.Base(b.entry), ".nomi"), b.target)
	}
	return b, nil
}

// defaultExecutableName is name with the extension target's executables
// carry.
func defaultExecutableName(name string, target ffirun.Target) string {
	switch {
	case target.GOOS == "windows":
		return name + ".exe"
	case target.GOARCH == "wasm":
		return name + ".wasm"
	}
	return name
}

// runBuild is `nomi build`.
func runBuild(args []string) error {
	b, err := parseBuildArgs(args)
	if err != nil {
		return err
	}
	return build(b, os.Stderr)
}

func build(b buildArgs, stderr io.Writer) error {
	if b.vm {
		return writeRunner(b, stderr)
	}
	absPath, err := programEntry("build", b.entry)
	if err != nil {
		return err
	}
	var static webStatic
	if b.web && !b.noStatic {
		dir, err := webStaticDir("build", absPath, b.static)
		if err != nil {
			return err
		}
		if static, err = readStatic(dir); err != nil {
			return fmt.Errorf("nomi build --web: %w", err)
		}
		if len(static.files) > 0 {
			if err := checkWebOut(b.out, dir); err != nil {
				return err
			}
		}
	}
	res, err := ffirun.Prepare(absPath)
	if b.web && hasGoBindings(res, err) {
		return errWebGoBindings
	}
	if b.noVM && hasGoBindings(res, err) {
		return errNoVMGoBindings
	}
	if err != nil {
		return fmt.Errorf("nomi build: %w", ffirun.ForBuild(err))
	}
	var image []byte
	var runner, decls string
	if res.FastPath {
		p, err := vmhost.Load(absPath)
		if err != nil {
			vmhost.WriteFailure(stderr, err)
			return errBuildRefused
		}
		reactor := b.target == wasip1 && p.HasExports()
		buildImage := p.BuildImage
		if b.web || reactor {
			buildImage = p.BuildExportsImage
		}
		img, usesCompiler, err := buildImage()
		if err != nil {
			if blocked, ok := vmhost.IsBlocked(err); ok {
				blocked.Write(stderr, vmhost.DisplayPath(absPath))
				return errBuildRefused
			}
			return fmt.Errorf("nomi build: %w", err)
		}
		image = img
		if b.web {
			decls = p.WebDeclarations()
		}
		if b.noVM {
			return writeImageFile(b.out, image, usesCompiler)
		}
		if runner, err = ffirun.Runner(b.target, ffirun.Variant{Compiler: usesCompiler, Reactor: reactor}, stderr); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	} else {
		if err := ffirun.CheckProjectBuild(); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
		dir, err := os.MkdirTemp("", "nomi-build-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		imagePath := filepath.Join(dir, "image")
		built, report, ok, err := ffirun.BuildImageVM(res, absPath, imagePath)
		if err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
		if !ok {
			fmt.Fprint(stderr, report)
			return errBuildRefused
		}
		if b.target == wasip1 && built.HasExports {
			return errReactorGoBindings
		}
		if image, err = os.ReadFile(imagePath); err != nil {
			return err
		}
		if runner, err = ffirun.ProjectRunner(res, b.target, built.UsesCompiler); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	}
	if b.web {
		return writeWebFolder(b, runner, image, decls, static, stderr)
	}
	if err := writeExecutable(b.out, runner, image, b.target); err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	return nil
}

// programEntry is the absolute path of entry, the program `nomi <cmd>`
// builds, or why it cannot be one: it must be a program file, and neither a
// test file nor a stdlib module.
func programEntry(cmd, entry string) (string, error) {
	if err := entryFileError(cmd, entry); err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(entry)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(filepath.Base(absPath), "_test.nomi") {
		return "", fmt.Errorf("nomi %s: %s is a test file; use `nomi test %s`",
			cmd, vmhost.DisplayPath(absPath), vmhost.DisplayPath(absPath))
	}
	if isStdlibTestPath(absPath) {
		return "", fmt.Errorf("nomi %s: %s is a stdlib module, not a program", cmd, vmhost.DisplayPath(absPath))
	}
	return absPath, nil
}

// hasGoBindings reports whether ffirun.Prepare's answer is a project with Go
// bindings, which a browser build cannot link.
func hasGoBindings(res *ffirun.Result, err error) bool {
	return err == nil && !res.FastPath || ffirun.NeedsCompilerSource(err)
}

// writeExecutable writes runner's bytes, then image, then the trailer to
// out (writeAtomic). A WebAssembly runner keeps its sections whole
// (vmrunner/wasm.go): a wasip1 runner gets the image as a data segment
// written into its own memory, so it runs with no file access, and a js/wasm
// runner gets it as a custom section its loader hands it.
func writeExecutable(out, runner string, image []byte, target ffirun.Target) error {
	if target == wasip1 {
		module, err := os.ReadFile(runner)
		if err != nil {
			return err
		}
		module, err = vmrunner.EmbedWasmImage(module, image)
		if err != nil {
			return err
		}
		return writeAtomic(out, 0o755, func(w io.Writer) error {
			_, err := w.Write(module)
			return err
		})
	}
	src, err := os.Open(runner)
	if err != nil {
		return err
	}
	defer src.Close()
	head := make([]byte, 8)
	n, _ := src.ReadAt(head, 0)
	wasm := vmrunner.IsWasm(head[:n])
	return writeAtomic(out, 0o755, func(w io.Writer) error {
		size, err := io.Copy(w, src)
		if err != nil {
			return err
		}
		if wasm {
			_, err = w.Write(vmrunner.WasmImageSection(image))
			return err
		}
		if _, err := w.Write(image); err != nil {
			return err
		}
		_, err = w.Write(vmrunner.Trailer(size, int64(len(image))))
		return err
	})
}

// writeAtomic writes out through a temporary file beside it, so a failed
// build leaves nothing and an existing out is replaced whole.
func writeAtomic(out string, perm os.FileMode, write func(io.Writer) error) error {
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(absOut), "."+filepath.Base(absOut)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if err := write(tmp); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, absOut); err != nil {
		return err
	}
	ok = true
	return nil
}

// wasip1 is the target whose module is a WASI reactor when the program has
// an `export fn`.
var wasip1 = ffirun.Target{GOOS: "wasip1", GOARCH: "wasm"}

// errReactorGoBindings is a wasip1 build of an FFI project with an `export
// fn`: its module would be a reactor, and the reactor is only the shared
// nomi-vm-reactor, which links no project's Go bindings.
var errReactorGoBindings = errors.New("nomi build: this project has Go bindings and an `export fn`, so its " +
	"wasip1 module would be a WASI reactor, and a reactor cannot link a project's Go bindings yet. " +
	"Move the exported functions into a program without Go bindings, or remove `export` to build a command module")

// errNoVMGoBindings is `nomi build --no-vm` for an FFI project: its Go
// bindings need a runner built with them, which the shared nomi-vm is not.
var errNoVMGoBindings = errors.New("nomi build --no-vm: this project has Go bindings, which only a VM " +
	"built with them can answer, so it has no portable compiled program. Use `nomi build` for an executable that links them")

// writeImageFile writes the .nomic file `nomi build --no-vm` makes: image
// behind a header naming its format and this nomi's version
// (vmrunner/imagefile.go).
func writeImageFile(out string, image []byte, usesCompiler bool) error {
	var flags uint32
	if usesCompiler {
		flags |= vmrunner.FlagUsesCompiler
	}
	data := vmrunner.EncodeImageFile(image, flags, imageWriterVersion())
	if err := writeAtomic(out, 0o644, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}); err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	return nil
}

// writeRunner is `nomi build --vm`: a copy of nomi-vm for b.target with no
// program, which runs the .nomic file it is handed. It is the runner
// `nomi build` would append a program to, found, built or downloaded as that
// does (ffirun.Runner). A js/wasm runner gets Go's wasm_exec.js beside it,
// from the toolchain that linked it. For wasip1 it is the command runner:
// the reactor reads its program only from its own memory, so a bare copy of
// it could run nothing.
func writeRunner(b buildArgs, stderr io.Writer) error {
	runner, err := ffirun.Runner(b.target, ffirun.Variant{Compiler: b.compiler}, stderr)
	if err != nil {
		return fmt.Errorf("nomi build --vm: %w", err)
	}
	copyTo := func(out, src string, perm os.FileMode) error {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		return writeAtomic(out, perm, func(w io.Writer) error {
			_, err := io.Copy(w, f)
			return err
		})
	}
	if err := copyTo(b.out, runner, 0o755); err != nil {
		return fmt.Errorf("nomi build --vm: %w", err)
	}
	if b.target.GOOS == "js" && b.target.GOARCH == "wasm" {
		support, err := ffirun.WasmExec(runner)
		if err != nil {
			return fmt.Errorf("nomi build --vm: %w", err)
		}
		if err := copyTo(filepath.Join(filepath.Dir(b.out), ffirun.WasmExecName), support, 0o644); err != nil {
			return fmt.Errorf("nomi build --vm: %w", err)
		}
	}
	return nil
}
