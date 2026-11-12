package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `nomi fmt -check` lists each file that would change and exits 1 when
// there is one, so a CI step can fail on its status; `-l` lists them and
// exits 0. A formatted file passes -check silently.
func TestFmtCheck_ExitsOneOnAnUnformattedFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.nomi")
	good := filepath.Join(dir, "good.nomi")
	if err := os.WriteFile(bad, []byte("fn  f( )->Int\n    1\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte("fn f() -> Int\n    1\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(nomiBin, append([]string{"fmt"}, args...)...)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return strings.TrimSpace(out.String()), 0
		case errors.As(err, &exit):
			return strings.TrimSpace(out.String()), exit.ExitCode()
		}
		t.Fatalf("nomi fmt %v: %v", args, err)
		return "", 0
	}
	for _, c := range []struct {
		args       []string
		out        string
		exitStatus int
	}{
		{[]string{"-check", bad, good}, bad, 1},
		{[]string{"--check", bad}, bad, 1},
		{[]string{"-check", good}, "", 0},
		{[]string{"-l", bad, good}, bad, 0},
	} {
		out, code := run(c.args...)
		if out != c.out || code != c.exitStatus {
			t.Errorf("nomi fmt %v: printed %q and exited %d, want %q and %d", c.args, out, code, c.out, c.exitStatus)
		}
	}
	if _, code := run("-check", "-l", good); code != 1 {
		t.Errorf("nomi fmt -check -l: exited %d, want 1 for conflicting flags", code)
	}
}
