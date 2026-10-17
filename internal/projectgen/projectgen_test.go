package projectgen_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/vmhost"
)

// TestWriteGeneratedProject writes a generated project to a directory, to
// open in an editor or run with `nomi`. It runs only when
// NOMI_GENPROJECT_DIR names the directory; NOMI_GENPROJECT_LINES sets the
// size (default 10000) and NOMI_GENPROJECT_SEED the seed (default 1):
//
//	NOMI_GENPROJECT_DIR=/tmp/gen NOMI_GENPROJECT_LINES=50000 go test ./internal/projectgen -run TestWriteGeneratedProject -count=1 -v
func TestWriteGeneratedProject(t *testing.T) {
	dir := os.Getenv("NOMI_GENPROJECT_DIR")
	if dir == "" {
		t.Skip("set NOMI_GENPROJECT_DIR to write a generated project there")
	}
	cfg := projectgen.Config{Seed: envInt(t, "NOMI_GENPROJECT_SEED", 1), Lines: int(envInt(t, "NOMI_GENPROJECT_LINES", 10000))}
	p := projectgen.Generate(cfg)
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d files (%d program lines, %d test lines) to %s; the entry is main.nomi",
		len(p.Files), p.Lines(), p.TestLines(), dir)
}

func envInt(t *testing.T, name string, def int64) int64 {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return n
}

// TestGeneratedProjectsAreClean holds generated projects of about 1k and
// 10k lines to what the generator promises: `nomi check` reports nothing
// on the entry, `nomi run` runs it, and every test file's cases pass.
func TestGeneratedProjectsAreClean(t *testing.T) {
	for _, lines := range []int{1000, 10000} {
		t.Run(strconv.Itoa(lines), func(t *testing.T) {
			p := projectgen.Generate(projectgen.Config{Seed: 1, Lines: lines})
			if got := p.Lines(); got < lines*9/10 || got > lines*13/10 {
				t.Errorf("asked for about %d lines, got %d", lines, got)
			}
			dir := t.TempDir()
			if err := p.Write(dir); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(dir, "main.nomi")
			if err := vmhost.Check(entry); err != nil {
				t.Fatalf("nomi check reports:\n%s", err)
			}
			prog, err := vmhost.Load(entry)
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := prog.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("nomi run fails: %v\n%s", err, out.String())
			}
			if !strings.HasPrefix(out.String(), "checksum ") {
				t.Errorf("nomi run printed %q", out.String())
			}
			cases := 0
			for _, key := range p.Tests {
				path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
				if err := vmhost.Check(path); err != nil {
					t.Fatalf("nomi check %s reports:\n%s", key, err)
				}
				tp, err := vmhost.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range tp.Cases(io.Discard, vmhost.TestOptions{}) {
					cases++
					if c.Err != nil || len(c.Blocked) > 0 {
						t.Errorf("%s: case %q: %v %v\n%s", key, c.Name, c.Err, c.Blocked, c.Output)
					}
				}
			}
			if cases == 0 {
				t.Error("no test case ran")
			}
			t.Logf("%d files, %d program lines, %d test cases", len(p.Files), p.Lines(), cases)
		})
	}
}

// TestGenerateIsDeterministic holds a project to its seed and size.
func TestGenerateIsDeterministic(t *testing.T) {
	a := projectgen.Generate(projectgen.Config{Seed: 7, Lines: 3000})
	b := projectgen.Generate(projectgen.Config{Seed: 7, Lines: 3000})
	if len(a.Files) != len(b.Files) {
		t.Fatalf("%d files, then %d", len(a.Files), len(b.Files))
	}
	for key, text := range a.Files {
		if b.Files[key] != text {
			t.Fatalf("%s differs between two generations", key)
		}
	}
}
