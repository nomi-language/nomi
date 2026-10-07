package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// noGoPath is a PATH value for an installed machine without Go: a fresh
// directory linking every command in /usr/bin and /bin except go and gofmt.
// A bare PATH=/usr/bin:/bin is not enough, since some machines (GitHub's
// Ubuntu runners among them) install a go there.
func noGoPath(t *testing.T) string {
	t.Helper()
	return pathWithout(t, []string{"/usr/bin", "/bin"}, "go", "gofmt")
}

// pathWithout links every entry of dirs into one fresh directory, earlier
// dirs winning, except the names in omit, and returns that directory.
func pathWithout(t *testing.T, dirs []string, omit ...string) string {
	t.Helper()
	bin := t.TempDir()
	skip := map[string]bool{}
	for _, name := range omit {
		skip[name] = true
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if skip[name] {
				continue
			}
			link := filepath.Join(bin, name)
			if _, err := os.Lstat(link); err == nil {
				continue
			}
			if err := os.Symlink(filepath.Join(dir, name), link); err != nil {
				t.Fatal(err)
			}
		}
	}
	return bin
}

// A go in one of the directories does not reach the PATH, and the other
// commands there do.
func TestPathWithout_DropsGoAndKeepsTheRest(t *testing.T) {
	fake := t.TempDir()
	for _, name := range []string{"go", "gofmt", "tool"} {
		if err := os.WriteFile(filepath.Join(fake, name), []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + pathWithout(t, []string{fake, "/usr/bin", "/bin"}, "go", "gofmt")}
	for _, name := range []string{"go", "gofmt"} {
		probe := exec.Command("/usr/bin/env", name)
		probe.Env = env
		if out, err := probe.CombinedOutput(); err == nil {
			t.Fatalf("%s still resolves: %s", name, out)
		}
	}
	probe := exec.Command("/usr/bin/env", "tool")
	probe.Env = env
	if out, err := probe.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "tool" {
		t.Fatalf("tool did not run: %v %s", err, out)
	}
}
