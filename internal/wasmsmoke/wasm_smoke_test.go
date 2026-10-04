// Package wasmsmoke checks that the front end and VM still work when built
// for the browser.
//
// The tour playground does not run the code in this repository — it runs
// `nomi.wasm`, a gitignored build artifact. Nothing connected the two, so
// `make test-tour` could pass while the playground was months stale: it
// once served a stdlib with no `Restart.Permanent` and the pre-boot_scope
// lexical check, and the only thing that surfaced it was a person
// clicking Run.
//
// This builds the wasm fresh and runs a program through it under Node,
// which is the same path the browser takes. Skipped when Node is absent
// so it never blocks a plain `go test ./...`.
//
// WHAT IT DOES NOT CHECK: the wasm it runs is the one it just BUILT, in a
// temp directory it deletes. It never reads
// `tour/public/nomi/nomi.wasm`. So it answers "the wasm TARGET still
// works", and a stale staged bundle passes it. The staged bundle is the
// subject of
// runtime.TestTourWasmBundleIsTheCurrentSources and
// runtime.TestTourWasmAnswersTheTourBlocks; keep the two questions
// separate rather than assuming either covers the other.
package wasmsmoke

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// harness drives nomiRun the way the playground's worker does. Node needs
// a crypto polyfill that browsers provide natively; wasm_exec.js aborts
// at load without it.
const harness = `
import fs from 'node:fs';
import { webcrypto } from 'node:crypto';
if (!globalThis.crypto) globalThis.crypto = webcrypto;
await import('./wasm_exec.js');
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(
  fs.readFileSync(new URL('./nomi.wasm', import.meta.url)), go.importObject);
go.run(instance);
const res = globalThis.nomiRun(fs.readFileSync(process.argv[2], 'utf8'));
if (res.error) { console.error('ERROR: ' + res.error); process.exit(1); }
process.stdout.write(res.output || '');
process.exit(0);
`

// program exercises the pieces most likely to break under GOOS=js:
// goroutines, timers, cancellation, and a supervisor's shutdown drain.
// Go's wasm target drives timers off the JS event loop, so a blocking
// sleep inside a synchronous nomiRun call is exactly the shape that
// could deadlock.
const program = `
import {
  std/supervisors.Supervisor
  std/tasks.Task
  std/duration.Duration
  std/timer
}

struct Config {
  context: Context
  work: Supervisor
}

fn boot(): Config {
  Config{context: Context.root(),
      work: Supervisor.new(shutdown_timeout: Duration.seconds(1), max_running: 2),
    }
}

fn slow(): Int {
  timer.sleep(Duration.seconds(30))
  1
}

fn quick(n: Int): Int {
  timer.sleep(Duration.milliseconds(10))
  n * 2
}

fn main(): Int {
  total = concurrent {
    a = Task.spawn(|| quick(10))
    b = Task.spawn(|| quick(20))
    doomed = Task.spawn(|| slow())
    Task.cancel(doomed)
    _ = Task.outcome(doomed)
    Task.await(a) + Task.await(b)
  }
  _ = Supervisor.spawn(Config.work, || timer.sleep(Duration.milliseconds(10)))
  _ = Supervisor.flush(Config.work)
  dbg total
}
`

func TestWasmBuildRunsAConcurrentProgram(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping wasm smoke test")
	}
	dir := t.TempDir()

	// The tour's build flags (scripts/build-tour-wasm.sh), so this smokes
	// the binary the tour ships.
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", filepath.Join(dir, "nomi.wasm"), "./cmd/nomi-wasm")
	build.Dir = "../.."
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building nomi.wasm: %v\n%s", err, out)
	}

	glue := filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js")
	if _, err := os.Stat(glue); err != nil {
		glue = filepath.Join(runtime.GOROOT(), "misc", "wasm", "wasm_exec.js")
	}
	data, err := os.ReadFile(glue)
	if err != nil {
		t.Skipf("no wasm_exec.js in GOROOT (%v); skipping", err)
	}
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("wasm_exec.js", string(data))
	runner := write("run.mjs", harness)
	src := write("main.nomi", program)

	cmd := exec.Command(node, runner, src)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the program under wasm failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "total = 60") {
		t.Errorf("wasm run produced %q, want it to contain %q", got, "total = 60")
	}
}
