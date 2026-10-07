package lsp

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A panic in the server is a bug in Nomi, never in the text being edited.
// The server must survive it: an editor whose language server died loses
// diagnostics, hover and completion until it is restarted. Every place work
// starts recovers a panic there with recoverPanic, deferred directly:
//
//   - each request and notification handler (rpc.go: rpcHandler.call and
//     rpcHandler.notification). A request is answered with an
//     InternalError; a notification has no answer.
//   - each background analysis of an edited document (docsched.go), which
//     publishes internalErrorDiag at the top of the file instead of its
//     diagnostics.
//   - each lowering run (lowering.go), whose diagnostics become
//     internalErrorDiag; the debounced start of an edited document's run
//     and the publish after a run, which log the panic only.
//   - each background evaluation of typed literals (literal_eval.go), whose
//     literals are recorded as skipped.
//   - each closed file's diagnostics pass, the workspace scan, and the
//     propagation of a change to the files that import it (workspace.go,
//     server.go), which log the panic only.
//
// Every panic is written with its stack to the server's log (stderr). No
// work is retried after a panic: the next edit, save or request runs it
// again, once.

// faultHook, when set, runs at the start of the work each boundary
// protects, with the name that boundary logs. It exists so a test can plant
// a panic; nothing else sets it.
var faultHook atomic.Pointer[func(where string)]

// fault runs faultHook.
func fault(where string) {
	if h := faultHook.Load(); h != nil {
		(*h)(where)
	}
}

// panicHook, when set, receives every recovered panic with the boundary's
// name and the stack. It exists so a test can fail on a panic the server
// survived (typing_test.go); nothing else sets it.
var panicHook atomic.Pointer[func(where string, value any, stack []byte)]

// serverPanic is a panic recovered at a boundary.
type serverPanic struct {
	value any
}

// Error is the internal-error text, worded as the compiler's
// (vm.InternalErrorText).
func (p *serverPanic) Error() string {
	return fmt.Sprintf("internal language server error: %v\nThis is a bug in Nomi, not in your program; "+
		"please report it at https://github.com/nomi-language/nomi/issues", p.value)
}

// diagnostic is the panic as one diagnostic at the top of the file.
func (p *serverPanic) diagnostic() protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	source := "nomi"
	return protocol.Diagnostic{
		Severity: &severity,
		Source:   &source,
		Code:     &protocol.IntegerOrString{Value: "internal-language-server-error"},
		Message:  p.Error(),
	}
}

// panicLog is where recovered panics are written. stdout carries the
// protocol, so the log is stderr, which editors show as the server's log.
// panicLogMu guards it, so a test can replace it.
var (
	panicLogMu sync.Mutex
	panicLog   io.Writer = os.Stderr
)

// recoverPanic recovers a panic in the function that defers it, logs it
// with its stack, and passes it to handle, which may be nil. It must be
// deferred directly (defer recoverPanic(...)): recover answers only
// there.
func recoverPanic(where string, handle func(*serverPanic)) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	panicLogMu.Lock()
	fmt.Fprintf(panicLog, "%s: recovered panic in %s: %v\n%s\n", serverName, where, r, stack)
	panicLogMu.Unlock()
	if h := panicHook.Load(); h != nil {
		(*h)(where, r, stack)
	}
	if handle != nil {
		handle(&serverPanic{value: r})
	}
}
