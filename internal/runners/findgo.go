package runners

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// goOverrideEnv names the `go` a runner is built with, ahead of PATH.
const goOverrideEnv = "NOMI_GO"

// findGo answers the `go` a runner is built from source with: the one
// NOMI_GO names (the command, or a Go root holding bin/go), else the one on
// PATH. Only a `nomi` that is not a release builds a runner (Runner).
//
// The answer is a path for exec.Command, never the bare name "go": with a
// bare name exec.Command looks it up itself, and a missing `go` would be
// reported inside the build's empty output instead of by goNotFoundError.
func findGo() (string, error) {
	if override := os.Getenv(goOverrideEnv); override != "" {
		return overrideGo(override)
	}
	path, err := exec.LookPath("go")
	if err != nil {
		return "", &goNotFoundError{dirs: searchPath(), err: err}
	}
	return path, nil
}

// overrideGo is the `go` NOMI_GO names: the command itself, or a Go root
// holding bin/go.
func overrideGo(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s=%s: %w", goOverrideEnv, path, err)
	}
	if info.IsDir() {
		bin := filepath.Join(path, "bin", exeName("go", runtime.GOOS))
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("%s=%s names a directory with no bin/%s", goOverrideEnv, path, exeName("go", runtime.GOOS))
		}
		return bin, nil
	}
	return path, nil
}

// goNotFoundError is a `nomi` built from source with no `go` to build a
// runner with. It names the places it looked, and that running a program
// needs none: a user who meets it has lost nothing else.
type goNotFoundError struct {
	// dirs is the PATH entries searched, in order.
	dirs []string
	// err is exec.LookPath's error.
	err error
}

func (e *goNotFoundError) Unwrap() error { return e.err }

func (e *goNotFoundError) Error() string {
	var b strings.Builder
	b.WriteString("no Go toolchain: `nomi build` builds nomi-vm, which a program is appended to, with `go build`, " +
		"and no `go` was found on PATH")
	if len(e.dirs) > 0 {
		fmt.Fprintf(&b, " (looked in %s)", strings.Join(e.dirs, ", "))
	} else {
		b.WriteString(" (PATH is empty)")
	}
	b.WriteString(". Install Go from https://go.dev/dl/, or set " + goOverrideEnv + " to a `go` command, and re-run. " +
		"`nomi run` needs no toolchain.")
	return b.String()
}

// searchPath is the PATH entries findGo looked in, in order, so the message
// can name them. PATH is often not what its owner expects: a launchd
// session, a stripped CI environment, an editor's inherited environment. An
// empty entry means the current directory to exec.LookPath, and it is
// spelled that way here.
func searchPath() []string {
	raw := os.Getenv("PATH")
	if raw == "" {
		return nil
	}
	parts := filepath.SplitList(raw)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			p = "."
		}
		out = append(out, p)
	}
	return out
}
