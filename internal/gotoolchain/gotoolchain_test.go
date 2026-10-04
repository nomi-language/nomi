package gotoolchain

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestFind_TheWrapperRefusesWithItsOwnAccount covers the arm internal/ffirun
// cannot reach: an EMPTY PATH. `exec.LookPath` treats an empty PATH as "the
// current directory", so it is a distinct outcome from a directory that exists
// and holds no `go`, and rendering it as "(looked in )" would be the kind of
// message this package exists to replace.
func TestFind_TheWrapperRefusesWithItsOwnAccount(t *testing.T) {
	t.Setenv("PATH", "")
	if p, err := exec.LookPath("go"); err == nil {
		t.Skipf("PATH=\"\" still resolves go at %s, so there is a `go` in the working "+
			"directory and this test cannot observe the empty-PATH arm", p)
	}
	wrapper, wrapperErr := FindForFFIWrapper()
	if wrapperErr == nil {
		t.Fatalf("FindForFFIWrapper returned %q with an empty PATH", wrapper)
	}
	var notFound *NotFoundError
	if !errors.As(wrapperErr, &notFound) {
		t.Fatalf("the failure is not a *NotFoundError: %v", wrapperErr)
	}
	msg := wrapperErr.Error()
	for _, s := range []string{
		"no Go toolchain",
		"this project has Go FFI",
		"(PATH is empty)",
		"https://go.dev/dl/",
		"A project with no Go FFI runs with no toolchain at all.",
	} {
		if !strings.Contains(msg, s) {
			t.Errorf("missing %q. Got:\n%s", s, msg)
		}
	}
	// No version, because the wrapper links the compiler's module and its
	// floor is that module's directive.
	for _, s := range []string{"asks for Go", "looked in"} {
		if strings.Contains(msg, s) {
			t.Errorf("contains %q, which it must not. Got:\n%s", s, msg)
		}
	}
	if !errors.Is(wrapperErr, exec.ErrNotFound) {
		t.Errorf("the failure does not unwrap to exec.ErrNotFound, so a caller cannot "+
			"tell it apart from an unreadable PATH entry: %v", wrapperErr)
	}
}
