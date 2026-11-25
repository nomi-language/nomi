package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/runners"
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
// nomi-vm that does not embed it (vmrunner/imagefile.go). `nomi build-vm`
// (build_vm.go) writes that nomi-vm.
//
// For wasip1/wasm the program picks the module's shape, with no flag: one
// with no `export fn` is a WASI command module (nomi-vm, `_start` runs
// main), and one with at least one `export fn` is a WASI reactor
// (nomi-vm-reactor: `_initialize`, nomi_alloc, nomi_call, nomi_free and
// nomi_main), whose image carries the exported functions as `--web`'s does,
// with nomi-wasi.js and nomi-wasi.d.ts written beside it. A js/wasm module
// gets wasm_exec.js and nomi-node.mjs beside it, which run it under Node
// (writeNodeLoader).
//
// A program the front end rejects, or one the VM would block, is refused with
// the text `nomi run` prints, and nothing is written.

const buildUsage = "usage: nomi build <file|dir> [-o <out>] [--target <os>/<arch> | --web [--static <dir>] | --no-vm] [--allow-unsupported <class>]"

// errBuildRefused is a build that already reported why on stderr.
var errBuildRefused = errors.New("build refused")

type buildArgs struct {
	entry  string
	out    string
	target runners.Target
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
	// allowUnsupported are the effect classes (`files`) the build may
	// reach although its target lacks them (vmhost.CheckTarget).
	allowUnsupported []string
}

// effectClasses are the classes --allow-unsupported names.
var effectClasses = []string{vmhost.ClassFiles, vmhost.ClassProcesses, vmhost.ClassNetwork, vmhost.ClassConnect, vmhost.ClassListen}

func parseBuildArgs(args []string) (buildArgs, error) {
	var b buildArgs
	var target runners.Target
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
			if target, err = parseTarget("nomi build", v); err != nil {
				return b, err
			}
		case arg == "--web":
			b.web = true
		case arg == "--allow-unsupported" || strings.HasPrefix(arg, "--allow-unsupported="):
			v, inline := strings.CutPrefix(arg, "--allow-unsupported=")
			if !inline {
				var err error
				if v, err = value(); err != nil {
					return b, err
				}
			}
			if !slices.Contains(effectClasses, v) {
				return b, fmt.Errorf("nomi build: --allow-unsupported takes an effect class (%s); got %q",
					strings.Join(effectClasses, ", "), v)
			}
			b.allowUnsupported = append(b.allowUnsupported, v)
		case arg == "--no-vm":
			b.noVM = true
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
	if b.entry == "" && atWorkspaceRoot() {
		// The root names its members (projectEntry).
		b.entry = "."
	}
	if b.entry == "" {
		return b, errors.New(buildUsage)
	}
	if b.noVM {
		switch {
		case b.web || b.static != "":
			return b, fmt.Errorf("nomi build: --no-vm and --web are different outputs; pick one\n%s", buildUsage)
		case target != (runners.Target{}):
			return b, fmt.Errorf("nomi build --no-vm: a compiled program runs on every target's nomi-vm, so it takes no --target\n%s", buildUsage)
		}
		if b.out == "" {
			b.out = programName(b.entry) + ".nomic"
		}
		return b, nil
	}
	if b.static != "" && !b.web {
		return b, fmt.Errorf("nomi build: --static needs --web\n%s", buildUsage)
	}
	if b.web {
		if target != (runners.Target{}) && target != jsWasm {
			return b, fmt.Errorf("nomi build: --web builds for js/wasm, so it cannot take --target %s", target)
		}
		b.target = jsWasm
		if b.out == "" {
			b.out = programName(b.entry) + "-web"
		}
		return b, nil
	}
	b.target = targetOrHost(target)
	if b.out == "" {
		// go build's default: the program's name, in the working
		// directory.
		b.out = defaultExecutableName(programName(b.entry), b.target)
	}
	return b, nil
}

// outFileError is why out, the file a build writes, cannot be written
// there: it is a directory, such as the project directory `nomi build
// hello` names when run beside it.
func outFileError(out string) error {
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		return fmt.Errorf("nomi build: %s is a directory, so the program cannot be written there; name the file to write with -o", out)
	}
	return nil
}

// programName is the name what `nomi build` writes for entry has by
// default: a file's name without .nomi, or a project directory's own name,
// as go build names a package's executable.
func programName(entry string) string {
	if info, err := os.Stat(entry); err == nil && info.IsDir() {
		if abs, err := filepath.Abs(entry); err == nil {
			return filepath.Base(abs)
		}
	}
	return strings.TrimSuffix(filepath.Base(entry), ".nomi")
}

// defaultExecutableName is name with the extension target's executables
// carry.
func defaultExecutableName(name string, target runners.Target) string {
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
	absPath, err := programEntry("build", b.entry)
	if err != nil {
		return err
	}
	if !b.web {
		if err := outFileError(b.out); err != nil {
			return err
		}
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
	// A build of the same files for the same target is kept in the artifact
	// cache, as a run's lowering is: a hit needs no check, no lowering and
	// no image, only the runner.
	cache := vmhost.OpenCacheFor(absPath, buildKind(b))
	kept, hit := decodeKeptBuild(cache.Kept())
	if !hit {
		if kept, err = buildInProcess(b, absPath, stderr, cache); err != nil {
			return err
		}
	} else {
		io.WriteString(stderr, kept.Notes)
	}
	image, sources, reactor, decls, localeFiles := kept.Image, kept.Sources, kept.Reactor, kept.Decls, kept.LocaleFiles
	if b.noVM {
		return writeImageFile(b.out, image, sources, kept.UsesCompiler)
	}
	// A program that reaches no optional family's crossing is appended to
	// the lean runner, a third smaller (runners.Variant), unless it imports
	// std/compiler, whose runner has no lean variant; one that reaches a
	// family its target's runners never have has no runner.
	if err := runners.FamiliesOnTarget(kept.Families, b.target); err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	variant := runners.Variant{Compiler: kept.UsesCompiler, Reactor: reactor,
		NoNetwork: !kept.UsesCompiler && len(kept.Families) == 0}
	runner, err := runners.Runner(b.target, variant, stderr)
	if err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	if b.web {
		return writeWebFolder(b, runner, image, sources, decls, localeFiles, static, stderr)
	}
	if err := writeExecutable(b.out, runner, image, sources, b.target); err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	if reactor {
		if err := writeWasiLoader(filepath.Dir(b.out)); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	}
	if b.target == jsWasm {
		if err := writeNodeLoader(filepath.Dir(b.out), runner); err != nil {
			return fmt.Errorf("nomi build: %w", err)
		}
	}
	return nil
}

// parseTarget reads --target's value, <os>/<arch>, for the command cmd.
func parseTarget(cmd, v string) (runners.Target, error) {
	goos, goarch, ok := strings.Cut(v, "/")
	if !ok || goos == "" || goarch == "" {
		return runners.Target{}, fmt.Errorf("%s: --target takes <os>/<arch>, such as linux/amd64; got %q", cmd, v)
	}
	return runners.Target{GOOS: goos, GOARCH: goarch}, nil
}

// targetOrHost is the target --target named, or with none named the
// platform nomi runs on.
func targetOrHost(target runners.Target) runners.Target {
	if target == (runners.Target{}) {
		return runners.HostTarget()
	}
	return target
}

// buildCommand is the build as a report names it: `nomi build --web`, or
// `nomi build --target <os>/<arch>`.
func buildCommand(b buildArgs) string {
	if b.web {
		return "nomi build --web"
	}
	return "nomi build --target " + b.target.String()
}

// writeExecutable writes runner's bytes, then image (its code, then its
// files region streamed from sources), then the trailer to out
// (writeAtomic). A WebAssembly runner keeps its sections whole
// (vmrunner/wasm.go): a wasip1 runner gets the image as a data segment
// written into its own memory, so it runs with no file access, and a
// js/wasm runner gets it as a custom section its loader hands it.
func writeExecutable(out, runner string, image []byte, sources []vmhost.EmbedSource, target runners.Target) error {
	n, err := imageLen(image)
	if err != nil {
		return err
	}
	if target == wasip1 {
		module, err := os.ReadFile(runner)
		if err != nil {
			return err
		}
		// The image goes into the module's own memory, bounded by its
		// image area, so it is put together whole.
		var whole bytes.Buffer
		if err := writeImage(&whole, image, sources); err != nil {
			return err
		}
		module, err = vmrunner.EmbedWasmImage(module, whole.Bytes())
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
	k, _ := src.ReadAt(head, 0)
	wasm := vmrunner.IsWasm(head[:k])
	return writeAtomic(out, 0o755, func(w io.Writer) error {
		size, err := io.Copy(w, src)
		if err != nil {
			return err
		}
		if wasm {
			if _, err := w.Write(vmrunner.WasmImageSectionHead(n)); err != nil {
				return err
			}
			return writeImage(w, image, sources)
		}
		if err := writeImage(w, image, sources); err != nil {
			return err
		}
		_, err = w.Write(vmrunner.Trailer(size, n))
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
var wasip1 = runners.Target{GOOS: "wasip1", GOARCH: "wasm"}

// jsWasm is the target a browser build is for.
var jsWasm = runners.Target{GOOS: "js", GOARCH: "wasm"}

// writeImageFile writes the .nomic file `nomi build --no-vm` makes: image,
// its code and then its files region streamed from sources, behind a
// header naming its format and this nomi's version (vmrunner/imagefile.go).
func writeImageFile(out string, image []byte, sources []vmhost.EmbedSource, usesCompiler bool) error {
	var flags uint32
	if usesCompiler {
		flags |= vmrunner.FlagUsesCompiler
	}
	n, err := imageLen(image)
	if err == nil {
		err = writeAtomic(out, 0o644, func(w io.Writer) error {
			if _, err := w.Write(vmrunner.ImageFileHead(n, flags, imageWriterVersion())); err != nil {
				return err
			}
			return writeImage(w, image, sources)
		})
	}
	if err != nil {
		return fmt.Errorf("nomi build: %w", err)
	}
	return nil
}

// imageLen is how long image is once written: its code and its files
// region.
func imageLen(image []byte) (int64, error) {
	region, err := vmhost.FilesRegionSize(image)
	return int64(len(image)) + region, err
}

// writeImage writes image's code, then its files region, each file read
// from its source a buffer at a time (vmhost.WriteFilesRegion).
func writeImage(w io.Writer, image []byte, sources []vmhost.EmbedSource) error {
	if _, err := w.Write(image); err != nil {
		return err
	}
	return vmhost.WriteFilesRegion(w, image, sources)
}
