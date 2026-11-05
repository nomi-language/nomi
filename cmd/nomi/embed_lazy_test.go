package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A kept program stamps every file it embeds, a hidden one an `all:`
// pattern embeds included, which the walk of the embedded folder skips: an
// edit to the dotfile is read on the next run, not refused as a file that
// changed after the program was compiled.
func TestRunCommand_AnEditToAnEmbeddedDotfileIsReadOnTheNextRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"site\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import {
  std::embed.Files
  std::io
}

embed site `+"`all:conf`"+`

fn main() {
  io.print(Files.read_text(site, "conf/.env")!)
}
`)
	mustWrite(t, filepath.Join(dir, "conf", ".env"), "one\n")
	cache := newCacheDir(t)
	if got := runNomiIn(t, dir, cache, "run", "main.nomi"); got.exit != 0 || got.stdout != "one\n\n" {
		t.Fatalf("nomi run:\n%s", got)
	}
	// A file younger than the cache's racy window is not kept yet; the
	// second run, its files a moment older, stores the program, and the
	// third reads it.
	time.Sleep(2100 * time.Millisecond)
	runNomiIn(t, dir, cache, "run", "main.nomi")
	if got := runNomiIn(t, dir, cache, "run", "main.nomi"); !got.read() {
		t.Fatalf("the third run did not read the kept program: %v\n%s", got.report, got)
	}
	mustWrite(t, filepath.Join(dir, "conf", ".env"), "two\n")
	if got := runNomiIn(t, dir, cache, "run", "main.nomi"); got.exit != 0 || got.stdout != "two\n\n" {
		t.Fatalf("after an edit to the embedded dotfile, nomi run:\n%s", got)
	}
}

// lockedBuffer is a buffer a process writes while the test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// `nomi run` reads an embedded file when the program reads it, from the
// file, and refuses one that changed after the program was compiled: the
// program prints the file's size, which needs no read, waits for a line,
// and the file is rewritten meanwhile, so the read stops the program with
// an error naming the file rather than answering other contents than it
// was compiled with.
func TestRunCommand_AnEmbeddedFileChangedAfterCompilingIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"site\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import {
  std::embed.Files
  std::io
}

embed site `+"`a.txt`"+`

fn main() {
  io.print(Files.metadata(site, "a.txt")!.size)
  _ = io.read_line()
  io.print(Files.read_text(site, "a.txt")!)
}
`)
	file := filepath.Join(dir, "a.txt")
	mustWrite(t, file, "one\n")
	cmd := exec.Command(nomiBin, "run", "main.nomi")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_ARTIFACT_CACHE=off")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr lockedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(stdout.String(), "4\n") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	mustWrite(t, file, "three\n")
	stdin.Write([]byte("go\n"))
	stdin.Close()
	err = cmd.Wait()
	if err == nil || !strings.Contains(stderr.String(), "a.txt changed after the program was compiled") {
		t.Fatalf("nomi run after the embedded file changed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "three") {
		t.Fatalf("the changed contents were served:\n%s", stdout.String())
	}
}
