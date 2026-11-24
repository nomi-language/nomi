package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/runners"
)

// `nomi build-vm`: a copy of nomi-vm for a target with no program, which runs
// the .nomic file it is handed (vmrunner/imagefile.go). It is the runner
// `nomi build` would append a program to, found, built or downloaded as that
// does (runners.Runner). `--reactor` writes the wasip1 reactor with nothing
// embedded instead, which takes a .nomic through nomi_load. `--no-network`
// picks the lean variant (runners.Variant.NoNetwork), a third smaller,
// built without the network and WebAssembly modules, whose crossings of
// those answer an error.

const buildVMUsage = "usage: nomi build-vm [--target <os>/<arch>] [--compiler | --no-network] [-o <out>]\n" +
	"       nomi build-vm --reactor [--compiler | --no-network] [-o <out>]"

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
	target runners.Target
	// compiler picks the VM that links std/compiler's hosts.
	compiler bool
	// reactor writes the wasip1 reactor, which loads a .nomic through
	// nomi_load, in place of the command runner.
	reactor bool
	// noNetwork picks the runner built without the network.
	noNetwork bool
}

func parseBuildVMArgs(args []string) (buildVMArgs, error) {
	var v buildVMArgs
	var target runners.Target
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
			if target, err = parseTarget("nomi build-vm", t); err != nil {
				return v, err
			}
		case arg == "--compiler":
			v.compiler = true
		case arg == "--reactor":
			v.reactor = true
		case arg == "--no-network":
			v.noNetwork = true
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
		if target != (runners.Target{}) && target != wasip1 {
			return v, fmt.Errorf("nomi build-vm --reactor: the reactor is a WASI module, so it builds for "+
				"wasip1/wasm, not %s\n%s", target, buildVMUsage)
		}
		v.target = wasip1
		if v.out == "" {
			v.out = "nomi-vm-reactor.wasm"
		}
		return v, nil
	}
	v.target = targetOrHost(target)
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

// writeRunner writes the VM v names. A js/wasm runner gets what runs it
// under Node beside it (writeNodeLoader). For wasip1 it is the command
// runner, and with --reactor nomi-vm-reactor with nothing embedded, with
// nomi-wasi.js and nomi-wasi.d.ts beside it: the loader that runs it in a
// browser or Node with no WASI library. The reactor's three files are one
// output, so its folder is made as `nomi build --web`'s is.
func writeRunner(v buildVMArgs, stderr io.Writer) error {
	cmd := "nomi build-vm"
	if v.reactor {
		cmd = "nomi build-vm --reactor"
	}
	runner, err := runners.Runner(v.target, runners.Variant{Compiler: v.compiler, Reactor: v.reactor, NoNetwork: v.noNetwork}, stderr)
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
	if v.target == jsWasm {
		if err := writeNodeLoader(filepath.Dir(v.out), runner); err != nil {
			return fmt.Errorf("%s: %w", cmd, err)
		}
	}
	return nil
}

// nodeLoaderJS is nomi-node.mjs, which runs a js/wasm module under Node:
// `node nomi-node.mjs app.wasm [args...]`.
//
//go:embed node/nomi-node.mjs
var nodeLoaderJS []byte

// writeNodeLoader writes what runs a js/wasm module under Node into dir,
// beside the module: wasm_exec.js, the module's JavaScript support file,
// copied unchanged from beside the js/wasm runner at runner
// (runners.WasmExec), and nomi-node.mjs, which loads it and runs the module.
func writeNodeLoader(dir, runner string) error {
	support, err := runners.WasmExec(runner)
	if err != nil {
		return err
	}
	wasmExec, err := os.ReadFile(support)
	if err != nil {
		return err
	}
	return writeBeside(dir, map[string][]byte{runners.WasmExecName: wasmExec, "nomi-node.mjs": nodeLoaderJS})
}

// writeWasiLoader writes nomi-wasi.js and nomi-wasi.d.ts into dir, beside a
// reactor: the one `--reactor` writes, or one `nomi build` embedded a
// program in.
func writeWasiLoader(dir string) error {
	return writeBeside(dir, map[string][]byte{"nomi-wasi.js": wasiLoaderJS, "nomi-wasi.d.ts": wasiLoaderDTS})
}

// writeBeside writes each of files into dir under its name, a loader beside
// the module it runs.
func writeBeside(dir string, files map[string][]byte) error {
	for name, data := range files {
		if err := writeAtomic(filepath.Join(dir, name), 0o644, func(w io.Writer) error {
			_, err := w.Write(data)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
