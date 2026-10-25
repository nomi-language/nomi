// Package gotoolchain answers one question: which Go toolchain the command in
// front of the user compiles with, and what the failure says when there is
// none.
//
// `nomi run`, `nomi test` and `nomi check` on a project with Go FFI build a
// wrapper with `go build` (internal/ffirun), and a user who installed a
// prebuilt `nomi` may have no Go at all. The toolchain is, in order:
//
//  1. NOMI_GO, the path of a `go` command (or of a GOROOT holding bin/go);
//  2. the `go` on PATH, so a Go developer downloads nothing;
//  3. the managed toolchain (managed.go): the Go release this `nomi` was built
//     with, downloaded from go.dev once, verified against a SHA-256 compiled
//     into this binary, and kept in the cache. Only a command the user ran
//     downloads it (AllowDownload); the language server uses it only once it
//     is there.
//
// When none is found the failure says what needed Go, where it looked, and
// why the managed toolchain was not used.
package gotoolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// goOverrideEnv names the `go` to use, ahead of PATH and the managed
// toolchain.
const goOverrideEnv = "NOMI_GO"

// FindForFFIWrapper resolves the `go` launcher for the Go-FFI path, where
// `run`, `test` and `check` build a wrapper that links the VM with adapters
// for the user's Go bindings.
//
// NO VERSION, deliberately. The wrapper links the COMPILER's Go module, so its
// floor is that module's own `go` directive rather than anything the caller
// staged, and naming a version read off the wrong go.mod would be worse than
// naming none. See internal/ffirun/gomod.go's writeAbsolutizedGoMod.
func FindForFFIWrapper() (string, error) {
	return find(&NotFoundError{
		Why: "this project has Go FFI, so `run`, `test` and `check` build a wrapper " +
			"with `go build`",
		Then: "A project with no Go FFI runs with no toolchain at all",
	})
}

// FindForBuild resolves the `go` launcher for `nomi build`, which builds the
// runner a program's IR is appended to with `go build` (and caches it).
func FindForBuild() (string, error) {
	return find(&NotFoundError{
		Why:  "`nomi build` builds nomi-vm, which a program is appended to, with `go build`",
		Then: "`nomi run` needs no toolchain",
	})
}

// FindForProjectRunner resolves the `go` launcher for `nomi build` on a
// project with Go FFI. Its runner links the project's own Go bindings through
// adapters generated for them, so no prebuilt runner can stand in for it.
func FindForProjectRunner() (string, error) {
	return find(&NotFoundError{
		Why: "this project has Go FFI, so `nomi build` compiles a nomi-vm that links " +
			"your Go bindings with `go build`, and a prebuilt nomi-vm cannot carry bindings it has never seen",
		Then: "A project with no Go FFI builds with no toolchain from a release, which ships a prebuilt nomi-vm",
	})
}

// FindForModules resolves the `go` launcher for a module GOPROXY sends to its
// version control repository (`direct`), which only the go command reads.
func FindForModules() (string, error) {
	return find(&NotFoundError{
		Why: "GOPROXY (or GOPRIVATE) sends this module to its version control repository, " +
			"which nomi reads through `go mod download`",
		Then: "A module the module proxy serves needs no toolchain",
	})
}

// find fills in the lookup result on a prepared error, or returns the launcher.
//
// The returned path is handed to exec.Command deliberately. exec.Command
// resolves a bare name through PATH itself, so passing "go" would work — and
// would put the lookup failure back inside CombinedOutput's empty output, which
// is the defect this package exists to fix.
func find(notFound *NotFoundError) (string, error) {
	if override := os.Getenv(goOverrideEnv); override != "" {
		return overrideGo(override)
	}
	path, err := exec.LookPath("go")
	if err == nil {
		return path, nil
	}
	managed, managedErr := Managed()
	if managedErr == nil {
		return managed, nil
	}
	notFound.Dirs = searchPath()
	notFound.Err = err
	notFound.Managed = managedErr.Error()
	return "", notFound
}

// overrideGo is the `go` NOMI_GO names: the command itself, or a GOROOT
// holding bin/go.
func overrideGo(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s=%s: %w", goOverrideEnv, path, err)
	}
	if info.IsDir() {
		bin := filepath.Join(path, "bin", exeName("go"))
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("%s=%s names a directory with no bin/%s", goOverrideEnv, path, exeName("go"))
		}
		return bin, nil
	}
	return path, nil
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// NotFoundError is the absence of a Go toolchain, as a type so a caller can
// tell it apart from a compile failure in the wrapper. The two are reported
// very differently: one is a missing prerequisite on the machine and the other
// is a defect.
type NotFoundError struct {
	// Why is what needs the toolchain, in the voice of the command the user
	// ran.
	Why string
	// Then is what works without a toolchain, phrased for this caller.
	Then string
	// Dirs is the PATH entries that were searched, in order.
	Dirs []string
	// Err is exec.LookPath's error.
	Err error
	// Managed is why the managed toolchain was not used.
	Managed string
}

func (e *NotFoundError) Unwrap() error { return e.Err }

// Error names the missing prerequisite, the exact places it was looked for,
// why nomi did not use its own copy of Go, and what needs no toolchain at all.
//
// That last clause is load-bearing rather than reassurance. A toolchain-free
// `nomi run` is what makes shipping prebuilt binaries worth doing
// (TestInstalledBinaryRunsWithoutASourceTree asserts it). A user who hits this
// message has not lost that, and a message that did not say so would read like
// the install was broken.
func (e *NotFoundError) Error() string {
	var b strings.Builder
	b.WriteString("no Go toolchain: ")
	b.WriteString(e.Why)
	b.WriteString(", and no `go` was found on PATH")
	if len(e.Dirs) > 0 {
		fmt.Fprintf(&b, " (looked in %s)", strings.Join(e.Dirs, ", "))
	} else {
		b.WriteString(" (PATH is empty)")
	}
	b.WriteString(". ")
	if e.Managed != "" {
		b.WriteString("nomi's own copy of Go is not available: ")
		b.WriteString(e.Managed)
		b.WriteString(". ")
	}
	b.WriteString("Install Go from https://go.dev/dl/, or set " + goOverrideEnv + " to a `go` command, and re-run.")
	if e.Then != "" {
		b.WriteString(" ")
		b.WriteString(e.Then)
		b.WriteString(".")
	}
	return b.String()
}

// searchPath is the PATH entries find looked in, in order, so the message can
// name them.
//
// Reported rather than summarized as "PATH" because the failure this replaces
// was routinely hit with a PATH that looked fine to its owner — a launchd
// session, a stripped CI environment, an editor's inherited environment. An
// empty element means the current directory to exec.LookPath, and it is spelled
// that way here instead of rendering as a gap between two commas.
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
