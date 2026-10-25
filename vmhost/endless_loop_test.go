package vmhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An `Iter.loop` with no `break` runs until something stops it. Under the run
// harness's step budget (runBounded), a `main` or a test case that spins
// forever is classed as too long, with the exact output it printed, and is
// not a crash. A `main`'s output comes before the verdict; a test case's
// output is reported under its case, after the verdict line.
func TestEndlessLoopUsesTheStepBudget(t *testing.T) {
	for _, c := range []struct{ name, src, transcript, output string }{
		{"main", `import std/io

fn main() {
    io.print("start")
    _ = Iter.loop(fn n = 0 -> {
        if n < 3 { io.print(n) }
        n + 1
    })
}
`, "start\n0\n1\n2\n\n--- too long: ", ""},
		{"a test case", `import std/io

test "spins" {
    assert 1 == 1
    io.print("case")
    Iter.loop(fn -> {
        _ = 1
    })
}
`, "\n--- spins: too long: ", "\ncase\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := runInput(t.TempDir(), c.src)
			if o.class != runTooLong || !strings.HasPrefix(o.transcript, c.transcript) ||
				!strings.HasSuffix(o.transcript, c.output) {
				t.Fatalf("class %s, skip %q, crash %q, transcript %q; want too long, starting %q and ending %q",
					o.class, o.skip, o.crash, o.transcript, c.transcript, c.output)
			}
		})
	}
}

// The tour's worker loop (concurrency.md, "A task's own context") has no
// `break`: it runs until its task is cancelled. It is a plain block, since a
// doctest cannot wait for that, so this checks it as `nomi check` does,
// beside the declarations it names.
func TestTourWorkerLoopChecks(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "tour", "src", "content", "docs", "concurrency.md"))
	if err != nil {
		t.Fatal(err)
	}
	var block string
	for _, part := range strings.Split(string(doc), "```nomi\n")[1:] {
		code, _, _ := strings.Cut(part, "```")
		if strings.Contains(code, "fn outbox_worker()") {
			block = code
		}
	}
	if block == "" {
		t.Fatal("concurrency.md has no plain block declaring `fn outbox_worker()`")
	}
	src := `import {
    std/channels.{Channel, Receiver}
    std/duration.Duration
    std/io
    std/timer
}

struct Config {
    context: Context
    outbox: Channel<String>
}

fn boot() -> Result<Config, String> {
    Ok(#Config{context: Context.root(), outbox: Channel.buffered<String>(8)})
}

fn drain_pending(outbox: Channel<String>) {
    case Receiver.receive(outbox.receiver) {
        Some(message) -> io.print(message)
        None -> Unit
    }
}

` + block + `
fn main() {
    worker: () -> Unit = outbox_worker
    _ = worker
}
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Check(path); err != nil {
		t.Fatalf("nomi check rejects the tour's worker loop:\n%s\n%v", src, err)
	}
}
