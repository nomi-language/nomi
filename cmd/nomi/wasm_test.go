package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wasmProject is testdata/wasm with internal/stdwasm's committed modules
// under wasm/, which its files embed, copied into a directory of the test's.
func wasmProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, src := range []string{"nomi.toml", "modules_test.nomi", "trap.nomi"} {
		data, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "wasm", src))
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, src), string(data))
	}
	for _, name := range []string{"values", "imports", "upper"} {
		data, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "stdwasm", "testdata", name+".wasm"))
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, "wasm", name+".wasm"), string(data))
	}
	return dir
}

// A program compiles the committed modules and calls them: a value of each
// type, the one NaN, each trap and the dead instance after it, imports
// written in Nomi that read and write the caller's memory, a failing
// import, the checks instantiate makes and run's convention.
// The compiled code is kept in the artifact cache, one directory a module.
func TestWasm_ProgramsCallModules(t *testing.T) {
	dir, cache := wasmProject(t), t.TempDir()
	r := runNomiIn(t, dir, cache, "test", "modules_test.nomi")
	if r.exit != 0 || !strings.Contains(r.stdout, "test result: ok. 9 passed, 0 failed") {
		t.Fatalf("nomi test over the modules:\n%s", r)
	}
	kept, err := filepath.Glob(filepath.Join(cache, "wasm", "*", "wazero-*", "*"))
	if err != nil || len(kept) != 3 {
		t.Errorf("the artifact cache keeps %v (%v), want the code of the 3 modules", kept, err)
	}
}

// A trap in an import's Nomi function is a trap of the call that reached
// the import: the program stops there with the import's message.
func TestWasm_ATrapInAnImportIsTheCallsTrap(t *testing.T) {
	r := runNomiIn(t, wasmProject(t), t.TempDir(), "run", "trap.nomi")
	if r.exit != 1 || !strings.Contains(r.stderr, "unreachable: the import gave up") || strings.Contains(r.stdout, "not reached") {
		t.Fatalf("nomi run of an import that traps:\n%s", r)
	}
}

// wasmUpper is internal/stdwasm's testdata/upper.wasm as hex, which a
// program decodes without reading a file.
const wasmUpper = "0061736d01000000010c0260017f017f60027f7f017e030302000105030100010607017f014180080b071a0305616c6c6f6300000575707065720001066d656d6f727902000a59021101017f23002101230020006a240020010b4502017f017f02400340200220014f0d01200020026a200020026a2d00002203200341e1006b411a4941206c6b3a0000200241016a21020c000b0b2000ad4220862001ad840b"

// Compiling a module and running it with `run` acts on nothing, so a
// program that only does that needs no effects list entry; making an
// instance is std::wasm, named where it is made.
func TestWasm_OnlyAnInstanceIsAnEffect(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[package]\nname = \"fx\"\nentry_points = [\"main\"]\n\n[effects.self]\neffects = []\n")
	program := func(body string) string {
		return "import\n    std::hex\n    std::wasm.{self, Instance}\nend\n\n" +
			"once upper = wasm.compile(hex.decode(\"" + wasmUpper + "\")!)!\n\n" +
			"fn shout(text: String) -> String\n" +
			"    wasm.run(upper, \"upper\", String.to_bytes(text)) |> Result.map(Bytes.to_string_lossy) |> Result.with_default(\"\")\n" +
			"end\n\n" +
			"fn size() -> Int\n    instance = wasm.instantiate(upper)!\n    defer Instance.drop(instance)\n    Instance.size(instance)!\nend\n\n" +
			"fn main()\n" + body + "end\n"
	}
	cache := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), program("    _ = shout(\"hi\")\n"))
	if r := runNomiIn(t, dir, cache, "check", "."); r.exit != 0 {
		t.Errorf("a program that only runs a module, with no effects allowed:\n%s", r)
	}
	mustWrite(t, filepath.Join(dir, "main.nomi"), program("    _ = shout(\"hi\")\n    _ = size()\n"))
	r := runNomiIn(t, dir, cache, "check", ".")
	if r.exit != 1 || !strings.Contains(r.stdout+r.stderr, "this package's own code has the effect std::wasm, and [effects.self] allows it no effects") ||
		!strings.Contains(r.stdout+r.stderr, "instance = wasm.instantiate(upper)!") {
		t.Errorf("a program that makes an instance, with no effects allowed:\n%s", r)
	}
}

// `run` is pure but unbounded, so a rule, which the compiler runs, may not
// call it, and says why.
func TestWasm_ARuleRefusesAModuleRun(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), "import\n    std::hex\n    std::io\n    std::wasm\nend\n\n"+
		"fn shout(text: String) -> String\n"+
		"    upper = wasm.compile(hex.decode(\""+wasmUpper+"\")!)!\n"+
		"    wasm.run(upper, \"upper\", String.to_bytes(text)) |> Result.map(Bytes.to_string_lossy) |> Result.with_default(\"\")\n"+
		"end\n\n"+
		"subtype Shouted String when fn s -> shout(s) == s\n\n"+
		"fn main()\n    loud: Shouted = \"HI\"\n    io.print(loud)\nend\n")
	r := runNomiIn(t, dir, t.TempDir(), "check", "main.nomi")
	want := "the rule of Shouted calls a function whose cost grows with its argument (compile calls wasm/host.compile; run calls wasm/host.run): a rule must be bounded"
	if r.exit != 1 || !strings.Contains(r.stdout+r.stderr, want) {
		t.Errorf("a rule that runs a module:\n%s\nwant %q", r, want)
	}
}

// wasmShout is a program that runs testdata's upper.wasm, held as hex,
// through `wasm.run` and prints what it returns.
const wasmShout = "import\n    std::hex\n    std::io\n    std::wasm\nend\n\n" +
	"fn main()\n" +
	"    upper = wasm.compile(hex.decode(\"" + wasmUpper + "\")!)!\n" +
	"    io.print(Bytes.to_string_lossy(wasm.run(upper, \"upper\", String.to_bytes(\"built\"))!))\n" +
	"end\n"

// `nomi build` appends a program that reaches std::wasm to the runner that
// has WebAssembly modules, and the built program runs its module; the lean
// runner, given the program by hand, refuses it before it runs; and a
// wasip1 build, whose runners have no WebAssembly modules, is refused.
func TestWasm_BuildsPickARunnerWithModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds runners; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), wasmShout)
	if got := runProcess(t, env, dir, "", nomiBin, "build", "-o", exe("shout"), "main.nomi"); got.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", got.exit, got.transcript())
	}
	if got := runProcess(t, env, dir, "", filepath.Join(dir, exe("shout"))); got.exit != 0 || got.stdout != "BUILT\n" {
		t.Fatalf("the built program: exit %d\n%s", got.exit, got.transcript())
	}

	if got := runProcess(t, env, dir, "", nomiBin, "build", "--no-vm", "-o", "shout.nomic", "main.nomi"); got.exit != 0 {
		t.Fatalf("nomi build --no-vm: exit %d\n%s", got.exit, got.transcript())
	}
	if got := runProcess(t, env, dir, "", nomiBin, "build-vm", "--no-network", "-o", exe("lean")); got.exit != 0 {
		t.Fatalf("nomi build-vm --no-network: exit %d\n%s", got.exit, got.transcript())
	}
	got := runProcess(t, env, dir, "", filepath.Join(dir, exe("lean")), "shout.nomic")
	if want := "this program uses WebAssembly modules (wasm/host.compile), which this nomi-vm was built without"; got.exit == 0 || !strings.Contains(got.stderr, want) {
		t.Errorf("the lean runner given the program: exit %d\n%s\nwant %q", got.exit, got.transcript(), want)
	}

	got = runProcess(t, env, dir, "", nomiBin, "build", "--target", "wasip1/wasm", "-o", "shout.wasm", "main.nomi")
	if want := "nomi build: this program uses WebAssembly modules, which no runner for wasip1/wasm has; build it for another target"; got.exit != 1 || !strings.Contains(got.stderr, want) {
		t.Errorf("nomi build --target wasip1/wasm: exit %d\n%s\nwant %q", got.exit, got.transcript(), want)
	}
}
