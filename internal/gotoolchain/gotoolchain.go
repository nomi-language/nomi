// Package gotoolchain answers one question: is there a Go toolchain for the
// command in front of the user to compile with, and what should the failure say
// when there is not.
//
// # WHY THIS PACKAGE EXISTS
//
// `nomi run`, `nomi test` and `nomi check` on a project with Go FFI build a
// wrapper with `go build` (internal/ffirun), and a user who installed a
// prebuilt `nomi` may have no Go at all. `exec.Command` records ErrNotFound on
// the command and `CombinedOutput` returns no output, so the raw failure read
// `ffirun: building wrapper: exec: "go": executable file not found in $PATH`.
// This package turns that into a message that says what needed Go and where it
// looked.
//
// # WHY THERE IS NO DOWNLOADER HERE
//
// Go's own toolchain switching (GOTOOLCHAIN) downloads and verifies the Go
// version a module asks for, against sum.golang.org, once any `go` launcher is
// present. What it cannot do is bootstrap, because it is a feature of the `go`
// command: no `go`, no GOTOOLCHAIN. That residue is this package, and it is a
// message rather than a download.
package gotoolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
		Why:  "`nomi build` builds the runner a program is appended to with `go build`",
		Then: "`nomi run` needs no toolchain",
	})
}

// FindForProjectRunner resolves the `go` launcher for `nomi build` on a
// project with Go FFI. Its runner links the project's own Go bindings through
// adapters generated for them, so no prebuilt runner can stand in for it.
func FindForProjectRunner() (string, error) {
	return find(&NotFoundError{
		Why: "this project has Go FFI, so `nomi build` compiles a runner that links " +
			"your Go bindings with `go build`, and a prebuilt runner cannot carry bindings it has never seen",
		Then: "A project with no Go FFI builds with no toolchain from a release, which ships a prebuilt runner",
	})
}

// find fills in the lookup result on a prepared error, or returns the launcher.
//
// The returned path is handed to exec.Command deliberately. exec.Command
// resolves a bare name through PATH itself, so passing "go" would work — and
// would put the lookup failure back inside CombinedOutput's empty output, which
// is the defect this package exists to fix.
func find(notFound *NotFoundError) (string, error) {
	path, err := exec.LookPath("go")
	if err == nil {
		return path, nil
	}
	notFound.Dirs = searchPath()
	notFound.Err = err
	return "", notFound
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
}

func (e *NotFoundError) Unwrap() error { return e.Err }

// Error names the missing prerequisite, the exact places it was looked for, and
// what needs no toolchain at all.
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
	b.WriteString("Install Go from https://go.dev/dl/ and re-run.")
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
