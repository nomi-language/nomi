package main

// `nomi run --watch` and `nomi test --watch` (watch_cmd.go): each run's
// output, the status line between runs, a build error then a fix, and a run
// stopped mid-loop or mid-read when a file changes.

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

// TestWatcher_SeesAChangeMadeBeforeItRuns holds a watcher to the stamps it
// read when made: a change made after newWatcher returns and before run
// starts polling, as a first build or run's would be, is a change. The
// session's first edit was lost when the stamps were read on the polling
// goroutine, which a loaded machine could start after the edit.
func TestWatcher_SeesAChangeMadeBeforeItRuns(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.nomi")
	mustWrite(t, file, "one")
	changed := make(chan string, 1)
	w := newWatcher(watchSet{
		files:   func() []string { return []string{file} },
		changed: func(names string) { changed <- names },
	})
	mustWrite(t, file, "two, longer")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case names := <-changed:
		if names != vmhost.DisplayPath(file) {
			t.Fatalf("changed %q, want %q", names, vmhost.DisplayPath(file))
		}
	case <-time.After(time.Minute):
		t.Fatal("the watcher never saw the change made before it ran")
	}
}

func TestParseRunWatchArgs(t *testing.T) {
	for _, c := range []struct {
		args  []string
		watch bool
		entry string
		rest  []string
		err   string
	}{
		{args: []string{"--watch", "a.nomi"}, watch: true, entry: "a.nomi", rest: []string{}},
		{args: []string{"--watch", "a.nomi", "x", "--web"}, watch: true, entry: "a.nomi", rest: []string{"x", "--web"}},
		{args: []string{"--web", "a.nomi"}},
		{args: []string{"--watch", "--web", "a.nomi"}, err: "--watch and --web cannot be combined"},
		{args: []string{"--port=0", "--watch", "a.nomi"}, err: "--watch and --web cannot be combined"},
		{args: []string{"--watch", "--verbose", "a.nomi"}, err: `nomi run: unknown flag "--verbose"`},
		{args: []string{"--watch"}, err: "usage: nomi run"},
	} {
		watch, entry, rest, err := parseRunWatchArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || watch != c.watch || entry != c.entry || (c.watch && strings.Join(rest, " ") != strings.Join(c.rest, " ")) {
			t.Errorf("%q: %v %q %q %v; want %v %q %q", c.args, watch, entry, rest, err, c.watch, c.entry, c.rest)
		}
	}
	if _, err := parseRunWebArgs([]string{"--web", "a.nomi", "--watch"}); err == nil || !strings.Contains(err.Error(), "--watch and --web cannot be combined") {
		t.Errorf("--web a.nomi --watch: error %v", err)
	}
	if parsed, err := parseTestArgs([]string{"--watch", "tests", "--format", "json"}); err != nil || !parsed.watch || parsed.root != "tests" {
		t.Errorf("test --watch: %+v, %v", parsed, err)
	}
}

// watchOutput is a run's stdout and stderr together, safe to write from the
// watch's goroutines while a test reads it.
type watchOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *watchOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *watchOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// await waits until the output after from contains want, and answers the
// offset just past it.
func (o *watchOutput) await(t *testing.T, ctx context.Context, from int, want string) int {
	t.Helper()
	for {
		s := o.String()
		if i := strings.Index(s[from:], want); i >= 0 {
			return from + i + len(want)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no %q after offset %d in:\n%s", want, from, s)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// writeSource writes src to path with a modification time later than the
// last, so a poll sees the change even within the file system's time
// resolution.
func writeSource(t *testing.T, path, src string) {
	t.Helper()
	old, statErr := os.Stat(path)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if statErr == nil {
		next := old.ModTime().Add(time.Second)
		if err := os.Chtimes(path, next, next); err != nil {
			t.Fatal(err)
		}
	}
}

// startWatch runs body on a context the test cancels at its end, and waits
// for body to return then.
func startWatch(t *testing.T, body func(ctx context.Context) error) context.Context {
	t.Helper()
	deadline, cancelDeadline := context.WithTimeout(context.Background(), time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- body(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("watch: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Errorf("the watch did not return after its context ended")
		}
		cancelDeadline()
	})
	return deadline
}

func TestRunWatch_RunsAgainOnEveryChange(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	dir := t.TempDir()
	app := filepath.Join(dir, "app.nomi")
	lib := filepath.Join(dir, "lib.nomi")
	writeSource(t, app, "import std/io\n\nfn main() {\n  io.print(\"one\")\n}\n")
	var out watchOutput
	stdin, typed := io.Pipe()
	defer typed.Close()
	ctx := startWatch(t, func(ctx context.Context) error {
		return runWatch(ctx, app, nil, &out, &out, stdin)
	})
	at := out.await(t, ctx, 0, "one\n")

	writeSource(t, app, "import std/io\n\nfn main() {\n  io.print(\"two!\")\n}\n")
	at = out.await(t, ctx, at, "app.nomi changed; running again\ntwo!\n")

	// A build error is printed as `nomi run` prints it, and the next change
	// runs again.
	writeSource(t, app, "import std/io\n\nfn main() {\n  io.print(\"three\"\n}\n")
	at = out.await(t, ctx, at, "app.nomi changed; running again\n")
	at = out.await(t, ctx, at, "error")
	writeSource(t, app, "import std/io\n\nfn main() {\n  io.print(\"three\")\n}\n")
	at = out.await(t, ctx, at, "app.nomi changed; running again\nthree\n")

	// A file the program comes to import is watched from the run that
	// imports it.
	writeSource(t, lib, "pub fn greet() -> String {\n  \"hello\"\n}\n")
	writeSource(t, app, "import lib\nimport std/io\n\nfn main() {\n  io.print(lib.greet())\n}\n")
	at = out.await(t, ctx, at, "running again\nhello\n")
	writeSource(t, lib, "pub fn greet() -> String {\n  \"hello again\"\n}\n")
	at = out.await(t, ctx, at, "lib.nomi changed; running again\nhello again\n")

	// A run still computing is stopped.
	writeSource(t, app, "import std/io\n\nfn spin(n: Int) -> Int {\n  spin(n + 1)\n}\n\nfn main() {\n  io.print(\"spinning\")\n  io.print(spin(0))\n}\n")
	at = out.await(t, ctx, at, "running again\nspinning\n")
	writeSource(t, app, "import std/io\n\nfn main() {\n  io.print(\"stopped the spin\")\n}\n")
	at = out.await(t, ctx, at, "app.nomi changed; running again\nstopped the spin\n")

	// A run waiting for input is stopped, and the line typed next is the
	// next run's.
	writeSource(t, app, "import std/io\n\nfn main() {\n  io.print(\"waiting\")\n  case io.read_line() {\n    Ok(line) -> io.print(\"got ${line}\")\n    Err(e) -> io.print(e)\n  }\n}\n")
	at = out.await(t, ctx, at, "running again\nwaiting\n")
	writeSource(t, app, "import std/io\n\nfn main() {\n  io.print(\"reading again\")\n  case io.read_line() {\n    Ok(line) -> io.print(\"got ${line}\")\n    Err(e) -> io.print(e)\n  }\n}\n")
	at = out.await(t, ctx, at, "app.nomi changed; running again\nreading again\n")
	if _, err := io.WriteString(typed, "typed\n"); err != nil {
		t.Fatal(err)
	}
	at = out.await(t, ctx, at, "got typed\n")
	if rest := out.String()[at:]; rest != "" {
		t.Errorf("unexpected output after the last run: %q", rest)
	}
}

func TestRunWatch_RefusesWhatItCannotWatch(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "app.nomic")
	if err := os.WriteFile(image, []byte(vmrunner.ImageFileMagic+"rest"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runWatch(context.Background(), image, nil, io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "is a compiled program, with no source to watch") {
		t.Errorf("a .nomic file: %v", err)
	}
	err = runWatch(context.Background(), filepath.Join(dir, "missing.nomi"), nil, io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("a missing file: %v", err)
	}
}

func TestTestWatch_RunsTheTestsAgainOnEveryChange(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.nomi")
	writeSource(t, lib, "pub fn double(n: Int) -> Int {\n  n + 1\n}\n")
	writeSource(t, filepath.Join(dir, "lib_test.nomi"), "import lib\n\ntest \"double\" {\n  assert lib.double(2) == 4\n}\n")
	var out watchOutput
	ctx := startWatch(t, func(ctx context.Context) error {
		return testWatch(ctx, testArgs{root: dir, format: vmhost.TestFormatText}, &out, &out, nil)
	})
	at := out.await(t, ctx, 0, "1 failed")
	// lib.nomi has no tests; it is watched because the tests import it.
	writeSource(t, lib, "pub fn double(n: Int) -> Int {\n  n * 2\n}\n")
	at = out.await(t, ctx, at, "lib.nomi changed; running again\n")
	at = out.await(t, ctx, at, "1 passed")
	// A new test file under the path is watched too.
	writeSource(t, filepath.Join(dir, "more_test.nomi"), "test \"more\" {\n  assert 1 == 2\n}\n")
	at = out.await(t, ctx, at, "more_test.nomi changed; running again\n")
	out.await(t, ctx, at, "1 failed")
}

func TestRunWatch_CtrlCExitsWithTheSignalsStatus(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app.nomi")
	writeSource(t, app, "import std/io\n\nfn spin(n: Int) -> Int {\n  spin(n + 1)\n}\n\nfn main() {\n  io.print(\"spinning\")\n  io.print(spin(0))\n}\n")
	cmd := exec.Command(nomiBin, "run", "--watch", app)
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir())
	var out watchOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out.await(t, ctx, 0, "spinning\n")
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 130 {
			t.Errorf("exit %v, want status 130; output:\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("nomi run --watch did not exit on SIGINT; output:\n%s", out.String())
	}
}
