package main

// `nomi build --target js/wasm` and `--target wasip1/wasm`: the image goes
// into a custom section, so the output stays a valid WebAssembly module
// (vmrunner/wasm.go).

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/nodeenv"
	"github.com/nomi-language/nomi/vmrunner"
)

// wasmSection is one section of a module: its id, its name when it is a
// custom section, and where its contents lie.
type wasmSection struct {
	id         byte
	name       string
	start, end int
}

// wasmSections splits a WebAssembly module into its sections and fails on
// anything a runtime's decoder would reject at that level: a bad header, an
// unknown section id, a section running past the end, a length that is not a
// valid u32 LEB128, a custom section whose name is not UTF-8, or a known
// section out of order or repeated.
func wasmSections(data []byte) ([]wasmSection, error) {
	if !bytes.HasPrefix(data, []byte{0, 'a', 's', 'm', 1, 0, 0, 0}) {
		return nil, fmt.Errorf("no WebAssembly version 1 header")
	}
	// The order known sections must appear in (spec 5.5.2, with data count
	// before code and tag after memory).
	order := map[byte]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 13: 6, 6: 7, 7: 8, 8: 9, 9: 10, 12: 11, 10: 12, 11: 13}
	last := 0
	var out []wasmSection
	off := 8
	for off < len(data) {
		id := data[off]
		off++
		if _, ok := order[id]; !ok && id != 0 {
			return nil, fmt.Errorf("unknown section code %#x at byte %d", id, off-1)
		}
		size, n := binary.Uvarint(data[off:])
		if n <= 0 || n > 5 || size > 0xffffffff {
			return nil, fmt.Errorf("bad section length at byte %d", off)
		}
		off += n
		start, end := off, off+int(size)
		if end > len(data) {
			return nil, fmt.Errorf("section %d at byte %d overruns the module by %d bytes", id, start, end-len(data))
		}
		s := wasmSection{id: id, start: start, end: end}
		if id == 0 {
			nameLen, n := binary.Uvarint(data[start:end])
			if n <= 0 || start+n+int(nameLen) > end {
				return nil, fmt.Errorf("custom section at byte %d has a bad name", start)
			}
			name := data[start+n : start+n+int(nameLen)]
			if !utf8.Valid(name) {
				return nil, fmt.Errorf("custom section at byte %d has a name that is not UTF-8", start)
			}
			s.name = string(name)
		} else {
			if order[id] <= last {
				return nil, fmt.Errorf("section %d at byte %d is out of order", id, start)
			}
			last = order[id]
		}
		out = append(out, s)
		off = end
	}
	return out, nil
}

// TestBuildCommand_WasmTargetsWriteValidModules builds a program for each
// WebAssembly target, splits the output into sections as a runtime's decoder
// does, and requires the image as the module's last section, a custom section
// named nomi.image. When node is on PATH it runs the module (js/wasm with the
// nomi-node.mjs the build wrote beside it, with no Go toolchain in reach;
// wasip1 with node:wasi) beside `nomi run`: stdout, stderr and exit status
// must be identical, over the same stdin and arguments.
func TestBuildCommand_WasmTargetsWriteValidModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds two runners; -short")
	}
	cacheRoot := t.TempDir()
	env := buildEnv(cacheRoot)
	dir := t.TempDir()
	programs := map[string]string{
		"mixed.nomi": `import std::io

fn main()
  counts =
    ["a", "b", "a", "c", "a"]
    |> Iter.reduce({}, fn acc, k ->
      case Map.get(acc, k)
        Some(prev) -> Map.put(acc, k, prev + 1)
        None -> Map.put(acc, k, 1)
      end
    end)
  io.print("a=${Map.get(counts, "a")} c=${Map.get(counts, "c")}")
  base = 10
  add = fn x: Int -> x + base
  io.print("closure: ${add(5)}")
  zero = case Map.get(counts, "z")
    Some(n) -> n
    None -> 0
  end
  io.print("dividing")
  io.print("${100 / NonZeroInt.try_from(zero)!}")
end
`,
		"stdin.nomi": `import std::io

fn main()
  case io.read_line()
    Ok(Some(line)) -> io.print("read: ${line}")
    _ -> io.print("no line")
  end
end
`,
		"args.nomi": `import std::io

struct App
  context: Context
end

fn boot(startup: Startup) -> Result<App, String>
  io.print(startup.args)
  Ok(App{context: Context.root()})
end

fn main()
  io.print("main ran")
end
`,
	}
	for name, src := range programs {
		mustWrite(t, filepath.Join(dir, name), src)
	}
	node, nodeErr := exec.LookPath("node")
	for _, target := range []string{"js/wasm", "wasip1/wasm"} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			for name := range programs {
				entry := filepath.Join(dir, name)
				mod := filepath.Join(t.TempDir(), strings.TrimSuffix(name, ".nomi")+".wasm")
				if b := runProcess(t, env, dir, "", nomiBin, "build", "--target", target, entry, "-o", mod); b.exit != 0 {
					t.Fatalf("nomi build --target %s %s: exit %d\n%s", target, name, b.exit, b.transcript())
				}
				data, err := os.ReadFile(mod)
				if err != nil {
					t.Fatal(err)
				}
				sections, err := wasmSections(data)
				if err != nil {
					t.Fatalf("%s for %s is not a valid module: %v", name, target, err)
				}
				// A js/wasm module carries its program as a last custom
				// section; a wasip1 one as a data segment, and no such section.
				carriers := 0
				for _, s := range sections {
					if s.name == vmrunner.WasmSectionName {
						carriers++
					}
				}
				if target == "js/wasm" {
					lastSection := sections[len(sections)-1]
					if lastSection.id != 0 || lastSection.name != vmrunner.WasmSectionName || carriers != 1 {
						t.Fatalf("the last section is id %d %q of %d %q sections, want the one custom section %q",
							lastSection.id, lastSection.name, carriers, vmrunner.WasmSectionName, vmrunner.WasmSectionName)
					}
				} else if carriers != 0 {
					t.Fatalf("a wasip1 module carries a %q section", vmrunner.WasmSectionName)
				}
				image, err := vmrunner.ReadImage(mod)
				if err != nil || len(image) == 0 {
					t.Fatalf("ReadImage: %d bytes, %v", len(image), err)
				}
				if nodeErr != nil {
					continue
				}
				args := []string{"first", "two words"}
				want := runProcess(t, env, dir, "a line\n", nomiBin, append([]string{"run", entry}, args...)...)
				var got observedRun
				if target == "js/wasm" {
					bin, nodeEnv, why := nodeWithoutGo(t, node, env)
					if bin == "" {
						t.Logf("not running the js/wasm module: %s", why)
						continue
					}
					loader := filepath.Join(filepath.Dir(mod), "nomi-node.mjs")
					got = runNode(t, nodeEnv, dir, "a line\n", bin, append([]string{loader, mod}, args...)...)
				} else {
					if major := nodeMajor(t, node); major < 20 {
						t.Logf("node %d has no node:wasi preview1; not running the wasip1 module", major)
						continue
					}
					script := filepath.Join(t.TempDir(), "wasi.mjs")
					mustWrite(t, script, wasiNodeScript)
					got = runNode(t, env, dir, "a line\n", node, append([]string{"--no-warnings", script, mod}, args...)...)
				}
				if got != want {
					t.Fatalf("%s under node differs from nomi run\n--- nomi run (exit %d) ---\n%s\n--- %s (exit %d) ---\n%s",
						name, want.exit, want.transcript(), target, got.exit, got.transcript())
				}
				// A WASI runner may pass no argv at all, not even a name, as
				// node:wasi does by default; the module runs with no arguments.
				if target == "wasip1/wasm" {
					noArgv := strings.Replace(wasiNodeScript, `args: ["/no/such/dir/app.wasm", ...process.argv.slice(3)],`, `args: [],`, 1)
					if noArgv == wasiNodeScript {
						t.Fatal("wasiNodeScript no longer sets args as this test expects")
					}
					script := filepath.Join(t.TempDir(), "wasi-no-argv.mjs")
					mustWrite(t, script, noArgv)
					want := runProcess(t, env, dir, "a line\n", nomiBin, "run", entry)
					got := runNode(t, env, dir, "a line\n", node, "--no-warnings", script, mod)
					if got != want {
						t.Fatalf("%s with an empty argv differs from nomi run\n--- nomi run (exit %d) ---\n%s\n--- wasip1 (exit %d) ---\n%s",
							name, want.exit, want.transcript(), got.exit, got.transcript())
					}
				}
				if name == "mixed.nomi" && (want.exit != 1 || !strings.Contains(want.stdout, "closure: 15")) {
					t.Fatalf("the program ran as %q, exit %d; this test compares the wrong thing", want.transcript(), want.exit)
				}
				if name == "args.nomi" && want.stdout != "[first, two words]\nmain ran\n" {
					t.Fatalf("the args program printed %q; this test compares the wrong thing", want.stdout)
				}
				if name == "stdin.nomi" && want.stdout != "read: a line\n" {
					t.Fatalf("the stdin program printed %q; this test compares the wrong thing", want.stdout)
				}
			}
		})
	}
}

// TestBuildCommand_AJsWasmModuleRunsUnderNodeAlone: `nomi build --target
// js/wasm` writes wasm_exec.js, unchanged with its license header, and
// nomi-node.mjs beside the module, and `node nomi-node.mjs hello.wasm` runs
// it with no Go installation in reach: no GOROOT and no `go` on PATH. A
// js/wasm nomi-vm from `nomi build-vm` gets the same two files and runs a
// .nomic file the same way.
func TestBuildCommand_AJsWasmModuleRunsUnderNodeAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH to run the module")
	}
	env := buildEnv(t.TempDir())
	bin, nodeEnv, why := nodeWithoutGo(t, node, env)
	if bin == "" {
		t.Skip(why)
	}
	src := t.TempDir()
	entry := filepath.Join(src, "hello.nomi")
	mustWrite(t, entry, "import std::io\n\nfn main()\n  io.print(\"hello\")\nend\n")
	out := t.TempDir()
	if b := runProcess(t, env, src, "", nomiBin, "build", "--target", "js/wasm", entry, "-o", filepath.Join(out, "hello.wasm")); b.exit != 0 {
		t.Fatalf("nomi build --target js/wasm: exit %d\n%s", b.exit, b.transcript())
	}
	checkNodeLoader(t, out)
	if got := runNode(t, nodeEnv, out, "", bin, "nomi-node.mjs", "hello.wasm"); got.exit != 0 || got.stdout != "hello\n" || got.stderr != "" {
		t.Fatalf("node nomi-node.mjs hello.wasm: exit %d, want 0 and hello\n%s", got.exit, got.transcript())
	}
	if got := runNode(t, nodeEnv, out, "", bin, "nomi-node.mjs"); got.exit != 1 || !strings.Contains(got.stderr, "usage: node nomi-node.mjs <module.wasm> [args...]") {
		t.Fatalf("node nomi-node.mjs with no module: exit %d, want 1 and its usage\n%s", got.exit, got.transcript())
	}

	vmDir := t.TempDir()
	if b := runProcess(t, env, src, "", nomiBin, "build-vm", "--target", "js/wasm", "-o", filepath.Join(vmDir, "nomi-vm.wasm")); b.exit != 0 {
		t.Fatalf("nomi build-vm --target js/wasm: exit %d\n%s", b.exit, b.transcript())
	}
	checkNodeLoader(t, vmDir)
	if b := runProcess(t, env, src, "", nomiBin, "build", "--no-vm", entry, "-o", filepath.Join(vmDir, "hello.nomic")); b.exit != 0 {
		t.Fatalf("nomi build --no-vm: exit %d\n%s", b.exit, b.transcript())
	}
	if got := runNode(t, nodeEnv, vmDir, "", bin, "nomi-node.mjs", "nomi-vm.wasm", "hello.nomic"); got.exit != 0 || got.stdout != "hello\n" {
		t.Fatalf("node nomi-node.mjs nomi-vm.wasm hello.nomic: exit %d, want 0 and hello\n%s", got.exit, got.transcript())
	}
}

// loaderPrograms are the js/wasm programs
// TestBuildCommand_TheNodeLoaderPassesAnyEnvironmentAndItsStatus runs: one
// that prints an environment variable, its argument count and its last
// argument and exits 3, one whose main returns another Err, and one whose
// main returns nothing.
var loaderPrograms = map[string]string{
	"probe.nomi": `import
    std::io
    std::process.Exit
end

struct App
    probe: String
    args:  List<String>
end

fn boot(startup: Startup) -> Result<App, String>
    probe = Map.get(startup.env, "NOMI_PROBE") |> Option.with_default("missing")
    Ok(App{probe:, args: startup.args})
end

fn main() -> Result<Unit, Exit>
    io.print(App.probe)
    io.print(List.length(App.args))
    io.print(Iter.last(App.args))
    Err(Exit{code: 3})
end
`,
	"fail.nomi": `import std::io

fn main() -> Result<Unit, String>
    io.print("before")
    Err("stopped on purpose")
end
`,
	"ok.nomi": `import std::io

fn main()
    io.print("fine")
end
`,
}

// TestBuildCommand_TheNodeLoaderPassesAnyEnvironmentAndItsStatus:
// nomi-node.mjs hands a js/wasm module its arguments and environment
// through globalThis.nomiArgs and nomiEnv (rt/jsprocess), not through the
// 8 KiB wasm_exec.js writes go.argv and go.env into. So a module started
// with an environment of 64 KiB, one variable of 30,000 bytes and 500 small
// ones, and 2,000 arguments reads them all. The status main ends with is
// the process's: an Exit's code, 1 for another Err, and 0 for a main that
// returns nothing.
func TestBuildCommand_TheNodeLoaderPassesAnyEnvironmentAndItsStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH to run the module")
	}
	env := buildEnv(t.TempDir())
	bin, nodeEnv, why := nodeWithoutGo(t, node, env)
	if bin == "" {
		t.Skip(why)
	}
	src, out := t.TempDir(), t.TempDir()
	for name, text := range loaderPrograms {
		entry := filepath.Join(src, name)
		mustWrite(t, entry, text)
		module := filepath.Join(out, strings.TrimSuffix(name, ".nomi")+".wasm")
		if b := runProcess(t, env, src, "", nomiBin, "build", "--target", "js/wasm", entry, "-o", module); b.exit != 0 {
			t.Fatalf("nomi build --target js/wasm %s: exit %d\n%s", name, b.exit, b.transcript())
		}
	}

	// A Windows variable holds at most 32,767 characters, and a command
	// line as many, so the large variable and the arguments stay under it.
	probe := strings.Repeat("x", 30_000)
	large := append(append([]string(nil), nodeEnv...), "NOMI_PROBE="+probe)
	for i := range 500 {
		large = append(large, fmt.Sprintf("NOMI_LOADER_FILLER_%03d=%s", i, strings.Repeat("y", 70)))
	}
	if size := len(strings.Join(large, "\x00")); size < 64<<10 {
		t.Fatalf("the environment is %d bytes, want at least 64 KiB", size)
	}
	args := []string{"nomi-node.mjs", "probe.wasm"}
	for i := range 2_000 {
		args = append(args, fmt.Sprintf("arg%d", i))
	}
	// runNode would cut the environment down to what Node needs; this run
	// keeps all of it on purpose, with runNode's bound.
	got := runFor(t, nodeLimit, large, out, "", bin, args...)
	if want := probe + "\n2000\nSome(arg1999)\n"; got.exit != 3 || got.stdout != want || got.stderr != "" {
		t.Fatalf("node nomi-node.mjs probe.wasm with a 64 KiB environment: exit %d, want 3 and the variable, 2000 and Some(arg1999)\n%.600s", got.exit, got.transcript())
	}

	if got := runNode(t, nodeEnv, out, "", bin, "nomi-node.mjs", "fail.wasm"); got.exit != 1 || got.stdout != "before\n" || !strings.Contains(got.stderr, "stopped on purpose") {
		t.Fatalf("node nomi-node.mjs fail.wasm: exit %d, want 1 and the Err\n%s", got.exit, got.transcript())
	}
	if got := runNode(t, nodeEnv, out, "", bin, "nomi-node.mjs", "ok.wasm"); got.exit != 0 || got.stdout != "fine\n" || got.stderr != "" {
		t.Fatalf("node nomi-node.mjs ok.wasm: exit %d, want 0 and fine\n%s", got.exit, got.transcript())
	}
}

// checkNodeLoader requires dir to hold the nomi-node.mjs this nomi carries
// and a wasm_exec.js that keeps its license header.
func checkNodeLoader(t *testing.T, dir string) {
	t.Helper()
	loader, err := os.ReadFile(filepath.Join(dir, "nomi-node.mjs"))
	if err != nil || !bytes.Equal(loader, nodeLoaderJS) {
		t.Fatalf("%s holds nomi-node.mjs %d bytes (%v), want the loader nomi carries", dir, len(loader), err)
	}
	support, err := os.ReadFile(filepath.Join(dir, "wasm_exec.js"))
	if err != nil || !bytes.HasPrefix(support, []byte("// Copyright 2018 The Go Authors. All rights reserved.\n// Use of this source code is governed by a BSD-style\n")) {
		t.Fatalf("%s holds a wasm_exec.js without its license header (%v):\n%.200s", dir, err, support)
	}
}

// nodeWithoutGo answers the Node binary itself, which a version manager's
// shim on PATH would find through PATH, and an environment for it with no
// GOROOT and a PATH of one empty directory, so nothing from a Go
// installation is in reach. With a node older than 22 it answers "" and why.
func nodeWithoutGo(t *testing.T, node string, env []string) (bin string, nodeEnv []string, why string) {
	t.Helper()
	if major := nodeMajor(t, node); major < 22 {
		return "", nil, fmt.Sprintf("node %d is older than 22, which nomi-node.mjs is written for", major)
	}
	path := runNode(t, env, "", "", node, "-p", "process.execPath")
	if path.exit != 0 {
		t.Fatalf("node -p process.execPath: exit %d\n%s", path.exit, path.transcript())
	}
	for _, kv := range nodeenv.Minimal(env) {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.EqualFold(name, "PATH") && !strings.EqualFold(name, "GOROOT") {
			nodeEnv = append(nodeEnv, kv)
		}
	}
	return strings.TrimSpace(path.stdout), append(nodeEnv, "PATH="+t.TempDir()), ""
}

// wasiNodeScript runs a wasip1 module under Node's node:wasi as a host with
// no file system does: no preopens, no environment, and an argv[0] naming
// no file. The module reads its program from its own memory.
const wasiNodeScript = `import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { WASI } from "node:wasi";

const path = resolve(process.argv[2]);
const wasi = new WASI({
  version: "preview1",
  args: ["/no/such/dir/app.wasm", ...process.argv.slice(3)],
  env: {},
  preopens: {},
  returnOnExit: true,
});
const module = await WebAssembly.compile(await readFile(path));
const instance = await WebAssembly.instantiate(module, wasi.getImportObject());
process.exitCode = wasi.start(instance);
`

// wasiNode answers the node on PATH when it runs node:wasi preview1 (node
// 20 or later), or "" when there is none.
func wasiNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil || nodeMajor(t, node) < 20 {
		return ""
	}
	return node
}

func nodeMajor(t *testing.T, node string) int {
	t.Helper()
	version := runNode(t, os.Environ(), "", "", node, "--version")
	if version.exit != 0 {
		t.Fatalf("node --version: exit %d\n%s", version.exit, version.transcript())
	}
	v := strings.TrimPrefix(strings.TrimSpace(version.stdout), "v")
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		t.Fatalf("node --version printed %q", version.stdout)
	}
	return n
}
