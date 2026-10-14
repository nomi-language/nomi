package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ffirun"
)

// `nomi build-vm`: a copy of nomi-vm for a target with no program, which runs
// the .nomic file it is handed (vmrunner/imagefile.go). It is the runner
// `nomi build` would append a program to, found, built or downloaded as that
// does (ffirun.Runner). `--reactor` writes the wasip1 reactor with nothing
// embedded instead, which takes a .nomic through nomi_load.

const buildVMUsage = "usage: nomi build-vm [--target <goos>/<goarch>] [--compiler] [-o <out>]\n" +
	"       nomi build-vm --reactor [--compiler] [-o <out>]"

// wasiLoaderJS and wasiLoaderDTS are nomi-wasi.js and its types, which
// `--reactor` writes beside the reactor: the JavaScript that
// implements the WASI imports the reactor needs and loads, calls and runs
// .nomic programs in it.
var (
	//go:embed wasi/nomi-wasi.js
	wasiLoaderJS []byte
	//go:embed wasi/nomi-wasi.d.ts
	wasiLoaderDTS []byte
)

type buildVMArgs struct {
	out    string
	target ffirun.Target
	// compiler picks the VM that links std/compiler's hosts.
	compiler bool
	// reactor writes the wasip1 reactor, which loads a .nomic through
	// nomi_load, in place of the command runner.
	reactor bool
}

func parseBuildVMArgs(args []string) (buildVMArgs, error) {
	var v buildVMArgs
	var goos, goarch string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("nomi build-vm: %s needs a value\n%s", arg, buildVMUsage)
			}
			i++
			return args[i], nil
		}
		switch {
		case arg == "-o":
			out, err := value()
			if err != nil {
				return v, err
			}
			v.out = out
		case strings.HasPrefix(arg, "-o="):
			v.out = strings.TrimPrefix(arg, "-o=")
		case arg == "--target":
			t, err := value()
			if err != nil {
				return v, err
			}
			var ok bool
			if goos, goarch, ok = strings.Cut(t, "/"); !ok || goos == "" || goarch == "" {
				return v, fmt.Errorf("nomi build-vm: --target takes <goos>/<goarch>, such as linux/amd64; got %q", t)
			}
		case arg == "--compiler":
			v.compiler = true
		case arg == "--reactor":
			v.reactor = true
		case isFlag(arg):
			return v, unknownFlag("nomi build-vm", arg, buildVMUsage)
		default:
			if slices.Contains(args, "--reactor") {
				return v, fmt.Errorf("nomi build-vm: the reactor carries no program, so it takes no file (%s). "+
					"A program with an `export fn` already builds a reactor: nomi build %s --target wasip1/wasm\n%s",
					arg, arg, buildVMUsage)
			}
			return v, fmt.Errorf("nomi build-vm: the VM carries no program, so it takes no file (%s). "+
				"nomi build %s writes the program with the VM\n%s", arg, arg, buildVMUsage)
		}
	}
	if v.reactor {
		// Only wasip1 has a reactor, so it is the target without --target.
		if goos != "" && (goos != wasip1.GOOS || goarch != wasip1.GOARCH) {
			return v, fmt.Errorf("nomi build-vm --reactor: the reactor is a WASI module, so it builds for "+
				"wasip1/wasm, not %s/%s\n%s", goos, goarch, buildVMUsage)
		}
		v.target = wasip1
		if v.out == "" {
			v.out = "nomi-vm-reactor.wasm"
		}
		return v, nil
	}
	v.target = ffirun.ResolveTarget(goos, goarch)
	if v.out == "" {
		v.out = defaultExecutableName("nomi-vm", v.target)
	}
	return v, nil
}

// runBuildVM is `nomi build-vm`.
func runBuildVM(args []string) error {
	v, err := parseBuildVMArgs(args)
	if err != nil {
		return err
	}
	return writeRunner(v, os.Stderr)
}

// writeRunner writes the VM v names. A js/wasm runner gets Go's wasm_exec.js
// beside it, from the toolchain that linked it. For wasip1 it is the command
// runner, and with --reactor nomi-vm-reactor with nothing embedded, with
// nomi-wasi.js and nomi-wasi.d.ts beside it: the loader that runs it in a
// browser or Node with no WASI library. The reactor's three files are one
// output, so its folder is made as `nomi build --web`'s is.
func writeRunner(v buildVMArgs, stderr io.Writer) error {
	cmd := "nomi build-vm"
	if v.reactor {
		cmd = "nomi build-vm --reactor"
	}
	runner, err := ffirun.Runner(v.target, ffirun.Variant{Compiler: v.compiler, Reactor: v.reactor}, stderr)
	if err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	if v.reactor {
		if err := os.MkdirAll(filepath.Dir(v.out), 0o755); err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
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
	if err := copyTo(v.out, runner, 0o755); err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	if v.reactor {
		if err := writeWasiLoader(filepath.Dir(v.out)); err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
	}
	if v.target.GOOS == "js" && v.target.GOARCH == "wasm" {
		support, err := ffirun.WasmExec(runner)
		if err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
		if err := copyTo(filepath.Join(filepath.Dir(v.out), ffirun.WasmExecName), support, 0o644); err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
	}
	return nil
}

// writeWasiLoader writes nomi-wasi.js and nomi-wasi.d.ts into dir, beside a
// reactor: the one `--reactor` writes, or one `nomi build` embedded a
// program in.
func writeWasiLoader(dir string) error {
	for name, data := range map[string][]byte{"nomi-wasi.js": wasiLoaderJS, "nomi-wasi.d.ts": wasiLoaderDTS} {
		if err := writeAtomic(filepath.Join(dir, name), 0o644, func(w io.Writer) error {
			_, err := w.Write(data)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
