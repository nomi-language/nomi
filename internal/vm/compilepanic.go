package vm

import (
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/nomi-language/nomi/internal/ir"
)

// A panic while compiling a function to bytecode is a bug in Nomi, never in
// the program. compile recovers it and answers a body that raises a
// *CompilePanic when it is entered, so the panic leaves the machine as an
// error on the path every other run error takes, and on no goroutine that
// could crash the process (a task body, an iteration callback).
//
// It is not a Nomi fault: no fault edge catches it (Machine.fault), a task
// that reaches it does not settle as Failed for the program to observe
// (taskLimit), ProgramFailure answers it as neither a fault nor a machine
// limit, and a test case reports it as the case's failure. vmhost answers it
// as an *InternalError; a built executable prints it.

// CompilePanic is a panic raised while compiling Func to bytecode.
type CompilePanic struct {
	Func  string
	Panic any
	Stack []byte
}

// Error is the internal-error text, which vmhost.InternalError shares.
func (e *CompilePanic) Error() string { return InternalErrorText(e.What()) }

// What is the panic and where it happened, without the report request.
func (e *CompilePanic) What() string {
	return fmt.Sprintf("vm: compiling %s to bytecode: %v", e.Func, e.Panic)
}

// Failure makes the panic a test case's result (rt.TestFailure).
func (e *CompilePanic) Failure() error { return e }

// InternalErrorText is the text of a compiler bug: what panicked, and a
// request to report it.
func InternalErrorText(what any) string {
	return fmt.Sprintf("internal compiler error: %v\nThis is a bug in Nomi, not in your program; "+
		"please report it at https://github.com/nomi-language/nomi/issues", what)
}

// AsCompilePanic reports the compile panic err carries, if it carries one.
func AsCompilePanic(err error) (*CompilePanic, bool) {
	var cp *CompilePanic
	if errors.As(err, &cp) {
		return cp, true
	}
	return nil, false
}

// CompileHook, when set, runs at the start of every function's compile. It
// exists so a test can plant a compile panic; nothing else sets it.
var CompileHook func(f *ir.Func)

// recoverCompile turns a panic out of compiling f into a body that raises it.
func recoverCompile(f *ir.Func, c **code) {
	r := recover()
	if r == nil {
		return
	}
	*c = panicCode(f, &CompilePanic{Func: f.Name(), Panic: r, Stack: debug.Stack()})
}

// panicCode is a body for f that raises err on entry. Its parameters keep the
// registers compile would give them, parameters first in each bank, because a
// caller writes its operands straight into the callee's window.
func panicCode(f *ir.Func, err error) *code {
	c := &code{fn: f}
	n := f.NumTemps() + 1
	c.locs = make([]loc, n)
	c.types = make([]*ir.ValType, n)
	assigned := make([]bool, n)
	counts := [4]int{}
	for _, p := range f.Params() {
		if p.Temp == ir.NoTemp || int(p.Temp) >= n {
			continue
		}
		if !assigned[p.Temp] {
			ty := f.TempType(p.Temp)
			b := classOf(ty)
			reg := 0
			if b != bankNone {
				reg = counts[b]
				counts[b]++
			}
			c.locs[p.Temp], c.types[p.Temp], assigned[p.Temp] = mkLoc(b, reg), ty, true
		}
		c.params = append(c.params, c.locs[p.Temp])
	}
	c.nW, c.nS, c.nR = counts[bankW], counts[bankS], counts[bankR]
	c.fails = []error{err}
	c.sites = []site{{pc: 0, pos: f.Pos(), handler: -1}}
	c.code = []uint32{uint32(opFail)}
	return c
}
