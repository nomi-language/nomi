package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// runCaptured lowers std/io's private `run_captured(input, run, finish)`,
// the one call `io.capture`'s body makes, and `run_replayed(script, run,
// finish)`, the one `io.replay`'s makes, as crossings the VM answers
// (internal/vm/capture.go). The result is what finish returns, the
// instance's `Captured<T>` or the `Replayed`, which finish builds in Nomi so
// the VM never needs that struct's layout. finish takes run's value and then
// rest: (output, transcript) for a capture, (output, transcript, expected,
// unread) for a replay.
func (bl *irScalarBuilder) runCaptured(t *ast.Call, key string, rest ...kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if namedArgNode(t.Args) != nil || len(t.Args) != 3 {
		return no()
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok || args.kinds[0] != kindString {
		return no()
	}
	run, finish := args.kinds[1], args.kinds[2]
	if run.tag != tagFunc || run.comp == nil || len(funcParams(run)) != 0 {
		return no()
	}
	if finish.tag != tagFunc || finish.comp == nil || len(funcParams(finish)) != 1+len(rest) ||
		funcParams(finish)[0] != funcResult(run) {
		return no()
	}
	for i, k := range rest {
		if funcParams(finish)[1+i] != k {
			return no()
		}
	}
	return bl.concHostEmit(t, key, funcResult(finish), args.temps...)
}
