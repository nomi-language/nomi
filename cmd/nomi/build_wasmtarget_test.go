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
// named nomi.image. When node is on PATH it runs the module (js/wasm with Go's
// wasm_exec_node.js, wasip1 with node:wasi) beside `nomi run`: stdout, stderr
// and exit status must be identical, over the same stdin and arguments.
func TestBuildCommand_WasmTargetsWriteValidModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds two runners; -short")
	}
	cacheRoot := t.TempDir()
	env := buildEnv(cacheRoot)
	dir := t.TempDir()
	programs := map[string]string{
		"mixed.nomi": `import std/io

fn main() {
  counts =
    ["a", "b", "a", "c", "a"]
    |> Iter.reduce(fn acc = %{}, k -> {
      case Map.get(acc, k) {
        Some(prev) -> Map.put(acc, k, prev + 1)
        None -> Map.put(acc, k, 1)
      }
    })
  io.print("a=${Map.get(counts, "a")} c=${Map.get(counts, "c")}")
  base = 10
  add = fn x: Int -> x + base
  io.print("closure: ${add(5)}")
  zero = case Map.get(counts, "z") {
    Some(n) -> n
    None -> 0
  }
  io.print("dividing")
  io.print("${100 / zero}")
}
`,
		"stdin.nomi": `import std/io

fn main() {
  case io.read_line() {
    Ok(line) -> io.print("read: ${line}")
    Err(e) -> io.print("no line: ${e}")
  }
}
`,
		"args.nomi": `import std/io

struct App {
  context: Context
}

fn boot(startup: Startup) -> App {
  io.print(startup.args)
  #App{context: Context.root()}
}

fn main() {
  io.print("main ran")
}
`,
	}
	for name, src := range programs {
		mustWrite(t, filepath.Join(dir, name), src)
	}
	node, nodeErr := exec.LookPath("node")
	goroot := ""
	if nodeErr == nil {
		out, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			t.Fatalf("go env GOROOT: %v", err)
		}
		goroot = strings.TrimSpace(string(out))
	}
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
					got = runProcess(t, env, dir, "a line\n", node, append([]string{filepath.Join(goroot, "lib", "wasm", "wasm_exec_node.js"), mod}, args...)...)
				} else {
					if major := nodeMajor(t, node); major < 20 {
						t.Logf("node %d has no node:wasi preview1; not running the wasip1 module", major)
						continue
					}
					script := filepath.Join(t.TempDir(), "wasi.mjs")
					mustWrite(t, script, wasiNodeScript)
					got = runProcess(t, env, dir, "a line\n", node, append([]string{"--no-warnings", script, mod}, args...)...)
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
					got := runProcess(t, env, dir, "a line\n", node, "--no-warnings", script, mod)
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
	out, err := exec.Command(node, "--version").Output()
	if err != nil {
		t.Fatalf("node --version: %v", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		t.Fatalf("node --version printed %q", out)
	}
	return n
}
