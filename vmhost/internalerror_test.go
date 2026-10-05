package vmhost

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/irbuild"
)

// A panic while loading (an ir.Lint violation in the IR builder, say) is a
// compiler bug. It reaches the caller as an InternalError instead of
// crashing the process, and the next load still takes the lowering lock.
func TestLower_PanicIsAnInternalError(t *testing.T) {
	_, err := lower(newConfig(nil), func() (*irbuild.Program, error) {
		panic("irbuild: scalar arithmetic body: ir: Lint: 1 violation")
	})
	var ice *InternalError
	if !errors.As(err, &ice) {
		t.Fatalf("err = %v (%T), want an *InternalError", err, err)
	}
	msg := err.Error()
	for _, want := range []string{
		"internal compiler error: irbuild: scalar arithmetic body: ir: Lint: 1 violation",
		"please report it at https://github.com/nomi-language/nomi/issues",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
	if len(ice.Stack) == 0 {
		t.Error("the InternalError carries no stack")
	}
	if irbuild.IRDeclineObserved != nil || irbuild.IRDeclineAt != nil {
		t.Error("the decline hooks are left installed after a panic")
	}
	if _, err := LoadSource("main.nomi", "fn main() {}\n"); err != nil {
		t.Fatalf("a load after the panic: %v", err)
	}
}
