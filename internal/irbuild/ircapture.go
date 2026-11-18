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
		panic("irbuild: runCaptured: runCaptured lowers std/io's private run_captured/run_replayed, called only from io.capture/io.replay bodies with three positional args")
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok || args.kinds[0] != kindString {
		return no()
	}
	run, finish := args.kinds[1], args.kinds[2]
	if run.tag != tagFunc || run.comp == nil || len(funcParams(run)) != 0 {
		panic("irbuild: runCaptured: the run argument in the std bodies is always a zero-parameter function value")
	}
	if finish.tag != tagFunc || finish.comp == nil || len(funcParams(finish)) != 1+len(rest) ||
		funcParams(finish)[0] != funcResult(run) {
		panic("irbuild: runCaptured: the finish argument in the std bodies always has the matching shape")
	}
	for i, k := range rest {
		if funcParams(finish)[1+i] != k {
			panic("irbuild: runCaptured: finish's trailing parameter kinds are fixed by the std declarations")
		}
	}
	return bl.concHostEmit(t, key, funcResult(finish), args.temps...)
}

// runOnFiles lowers std/fs/host's private `run_on(files, body, finish)`,
// the one call `std::fs::host.run_in`'s body makes, as a crossing the VM answers
// (internal/vm/sandbox.go): body runs on a frame whose file system is files
// (a sandbox, or the directory `fs.within` confines it to), and finish,
// which takes body's value, gives the result. std/process/host's
// `run_on(fake, body, finish)` has the same shape and lowers the same way,
// with the fake `std::process::testing.run` installs in place of the files.
func (bl *irScalarBuilder) runOnFiles(t *ast.Call, key string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if namedArgNode(t.Args) != nil || len(t.Args) != 3 {
		panic("irbuild: runOnFiles: runOnFiles lowers std/fs/host's private run_on, called only from std::fs::host.run_in's body with three positional args")
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok {
		return no()
	}
	body, finish := args.kinds[1], args.kinds[2]
	if body.tag != tagFunc || body.comp == nil || len(funcParams(body)) != 0 {
		panic("irbuild: runOnFiles: the body argument in std::fs::host.run_in is always a zero-parameter function value")
	}
	if finish.tag != tagFunc || finish.comp == nil || len(funcParams(finish)) != 1 ||
		funcParams(finish)[0] != funcResult(body) {
		panic("irbuild: runOnFiles: std::fs::host.run_in's finish is the identity lambda over the body's result")
	}
	return bl.concHostEmit(t, key, funcResult(finish), args.temps...)
}

// answerOn lowers std/http/host's private `answer_on(request, found,
// missing)`, the one call `std::http::host.answered` makes, as a crossing the VM
// answers (internal/vm/network.go): found takes what the installed fake
// answered, and missing, called when none is installed, gives the same
// result.
func (bl *irScalarBuilder) answerOn(t *ast.Call, key string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if namedArgNode(t.Args) != nil || len(t.Args) != 3 {
		panic("irbuild: answerOn: answerOn lowers std/http/host's private answer_on, called only from std::http::host.answered with a request and two lambdas whose kinds lower")
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok {
		return no()
	}
	found, missing := args.kinds[1], args.kinds[2]
	if found.tag != tagFunc || found.comp == nil || len(funcParams(found)) != 1 {
		panic("irbuild: answerOn: answerOn lowers std/http/host's private answer_on, called only from std::http::host.answered with a request and two lambdas whose kinds lower")
	}
	if missing.tag != tagFunc || missing.comp == nil || len(funcParams(missing)) != 0 ||
		funcResult(missing) != funcResult(found) {
		panic("irbuild: answerOn: answerOn lowers std/http/host's private answer_on, called only from std::http::host.answered with a request and two lambdas whose kinds lower")
	}
	return bl.concHostEmit(t, key, funcResult(found), args.temps...)
}

// recordedOn lowers std/http/host's private `recorded_on(found)`: found
// takes the requests the installed fake answered.
func (bl *irScalarBuilder) recordedOn(t *ast.Call, key string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if namedArgNode(t.Args) != nil || len(t.Args) != 1 {
		panic("irbuild: recordedOn: recordedOn lowers std/http/host's private recorded_on, called only from std::http::host.answered_requests with one identity lambda")
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok {
		return no()
	}
	found := args.kinds[0]
	if found.tag != tagFunc || found.comp == nil || len(funcParams(found)) != 1 {
		panic("irbuild: recordedOn: recordedOn lowers std/http/host's private recorded_on, called only from std::http::host.answered_requests with one identity lambda")
	}
	return bl.concHostEmit(t, key, funcResult(found), args.temps...)
}

// requestOn lowers std/http/host's private `request_on(incoming, timeout,
// body, finish)`, the one call `std::http::host.run_request` makes, as a crossing
// the VM answers (internal/vm/network.go): body takes the request's context
// and runs on a frame under it, and finish takes its value on the caller's
// frame.
func (bl *irScalarBuilder) requestOn(t *ast.Call, key string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if namedArgNode(t.Args) != nil || len(t.Args) != 4 {
		panic("irbuild: requestOn: requestOn lowers std/http/host's private request_on, called only from std::http::host.run_request with an Incoming, an Int and two lambdas whose kinds lower")
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok {
		return no()
	}
	body, finish := args.kinds[2], args.kinds[3]
	if body.tag != tagFunc || body.comp == nil || len(funcParams(body)) != 1 {
		panic("irbuild: requestOn: requestOn lowers std/http/host's private request_on, called only from std::http::host.run_request with an Incoming, an Int and two lambdas whose kinds lower")
	}
	if finish.tag != tagFunc || finish.comp == nil || len(funcParams(finish)) != 1 ||
		funcParams(finish)[0] != funcResult(body) {
		panic("irbuild: requestOn: requestOn lowers std/http/host's private request_on, called only from std::http::host.run_request with an Incoming, an Int and two lambdas whose kinds lower")
	}
	return bl.concHostEmit(t, key, funcResult(finish), args.temps...)
}
