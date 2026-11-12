package vmhost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/nodeenv"
)

// TestTourWasmReadsItsEmbeddedStdState loads the staged wasm under node, as
// the page's worker does, and requires its first stdlib build to read the
// base modules' state the wasm embeds (std/state.go) rather than build
// them: the browser keeps no cache, so without it every page pays the
// build. It then runs the same load with NOMI_STD_STATE=off, which builds
// them, and logs both loads' times (go.run, whose main makes the stdlib,
// then nomiWarm and a first hello world), so a change to the state's cost
// shows in the log.
func TestTourWasmReadsItsEmbeddedStdState(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; loads a 25MB wasm under node; -short")
	}
	if stagedFileMissing(t, "nomi.wasm.gz") || stagedFileMissing(t, "wasm_exec.js") {
		return
	}
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv(requireEnv) == "1" {
			t.Fatalf("node is not on PATH (%s=1 makes this a failure)", requireEnv)
		}
		t.Skip("node is not on PATH")
	}
	type load struct {
		Log   string  `json:"log"`
		Start float64 `json:"start"`
		Warm  float64 `json:"warm"`
		Run   float64 `json:"run"`
		Out   string  `json:"out"`
	}
	loadOnce := func(env map[string]string) load {
		t.Helper()
		dir := t.TempDir()
		driver := filepath.Join(dir, "driver.cjs")
		if err := os.WriteFile(driver, []byte(stdStateDriver), 0o644); err != nil {
			t.Fatal(err)
		}
		staged, err := filepath.Abs(stagedDir)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, node, driver, staged, string(encoded))
		cmd.Env = nodeenv.Minimal(os.Environ())
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("node driver over %s failed: %v\nstderr:\n%s", staged, err, stderr.String())
		}
		var got load
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("decode driver output: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
		if got.Out != "hello\n" {
			t.Fatalf("hello world printed %q; log:\n%s", got.Out, got.Log)
		}
		return got
	}
	read := loadOnce(map[string]string{"NOMI_DEBUG_STD_BUNDLE": "1"})
	if !strings.Contains(read.Log, "read from the embedded state") || strings.Contains(read.Log, "not its own") {
		t.Fatalf("the staged wasm did not read the std state it embeds. Its log:\n%s\nFix: %s", read.Log, rebuildInstruction)
	}
	built := loadOnce(map[string]string{"NOMI_DEBUG_STD_BUNDLE": "1", "NOMI_STD_STATE": "off"})
	if strings.Contains(built.Log, "read from the embedded state") {
		t.Fatalf("NOMI_STD_STATE=off read the embedded state. Its log:\n%s", built.Log)
	}
	for _, line := range strings.Split(read.Log, "\n") {
		if strings.Contains(line, "embedded state") {
			t.Log(line)
		}
	}
	t.Logf("start (main, the stdlib made) %.0f ms reading the state, %.0f ms building the base modules", read.Start, built.Start)
	t.Logf("nomiWarm %.0f ms and %.0f ms; first hello world %.0f ms and %.0f ms", read.Warm, built.Warm, read.Run, built.Run)
}

// stdStateDriver loads the staged wasm as nodeDriver does, with the
// environment the second argument holds, and answers what the wasm logged
// and how long its start, nomiWarm and a hello world took.
const stdStateDriver = `
const fs = require("fs");
const path = require("path");
const zlib = require("zlib");
const { webcrypto } = require("node:crypto");
if (!globalThis.crypto) globalThis.crypto = webcrypto;
const dir = process.argv[2];
let log = "";
console.log = (...args) => { log += args.join(" ") + "\n"; };
console.error = console.log;
require(path.join(dir, "wasm_exec.js"));
require(path.join(dir, "go-memfs.js"));
const go = new Go();
go.env = Object.assign({ GODEBUG: "gcstoptheworld=1" }, JSON.parse(process.argv[3]));
(async () => {
  const wasm = zlib.gunzipSync(fs.readFileSync(path.join(dir, "nomi.wasm.gz")));
  const { instance } = await WebAssembly.instantiate(wasm, go.importObject);
  let t = performance.now();
  go.run(instance); // main makes the stdlib, then parks on select{}
  const start = performance.now() - t;
  t = performance.now();
  globalThis.nomiWarm();
  const warm = performance.now() - t;
  t = performance.now();
  const r = globalThis.nomiRun('import std::io\n\nfn main()\n    io.print("hello")\nend\n');
  const run = performance.now() - t;
  const answer = JSON.stringify({ log, start, warm, run, out: r.output + r.error });
  process.stdout.write(answer, () => process.exit(0));
})().catch((err) => {
  process.stderr.write(String(err && err.stack || err) + "\n" + log, () => process.exit(1));
});
`
