package stdprocess

import (
	"bytes"
	"errors"
	"io"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// How std/process/host's `start` connects the child's standard input.
const (
	stdinNull    = 0 // the null device
	stdinInherit = 1 // the program's own standard input
	stdinReader  = 2 // the Reader `start` is given
	stdinPipe    = 3 // a Writer the body writes (Child.stdin)
)

// How it connects the child's standard output and error.
const (
	outNull    = 0 // the null device
	outInherit = 1 // the program's own output or error
	outCapture = 2 // kept in memory, answered by `captured`
	outPipe    = 3 // a Reader the body reads (Child.stdout, Child.stderr)
)

// spec is what `start` was asked to run.
type spec struct {
	program  string
	args     []string
	env      []rt.MapEntry[string, string]
	clearEnv bool
	dir      string
	stdin    int64
	input    rt.Reader
	out, err int64
	// newGroup starts the child as the leader of a process group of its
	// own (group_unix.go, group_windows.go).
	newGroup bool
}

// child is a started process, real or the fake's. A real child's state
// lives on its process; a fake one is finished when it starts.
type child struct {
	program string
	// id is the operating system's process id, or the fake's position of
	// the call, from 1.
	id int64
	// group is set when the child leads a process group of its own.
	group bool

	stdin          rt.Writer
	stdout, stderr rt.Reader
	piped          [3]bool

	// captured holds stdout and stderr in capture mode, once the child
	// finished.
	captured [2]*bytes.Buffer

	// proc is the operating system's process, or nil for the fake's.
	proc *osProcess

	mu       sync.Mutex
	finished bool
	status   []int64
	failure  *rt.IOError
}

// Start is std/process/host's `start`: the child running program with args,
// its environment extended by env (or made of env alone when clearEnv is
// set), in dir (the current directory when empty), with its streams in
// the modes given. A fake the frame carries answers it instead of the
// operating system. A program that cannot be started is an io.Error.
func Start(fr *rt.Frame, program string, args *rt.List[string], env rt.Map[string, string], clearEnv bool, dir string,
	stdin int64, input rt.Reader, out, errs int64, newGroup bool) rt.Result[rt.ChildProcess, rt.IOError] {
	if stdin < stdinNull || stdin > stdinPipe || out < outNull || out > outPipe || errs < outNull || errs > outPipe {
		rt.Trap("process: a stream mode is out of range")
	}
	s := spec{program: program, env: rt.MapEntries(env), clearEnv: clearEnv, dir: dir,
		stdin: stdin, input: input, out: out, err: errs, newGroup: newGroup}
	for l := args; l != nil; l = l.Tail {
		s.args = append(s.args, l.Head)
	}
	if program == "" {
		return rt.Err[rt.ChildProcess](rt.IOError{Tag: rt.TagIOInvalidPath, Path: program, Reason: "the program name is empty"})
	}
	var c *child
	var e *rt.IOError
	if f, ok := rt.ProcessesOf(fr).(*Fake); ok {
		c, e = f.start(fr, s)
	} else {
		c, e = startOS(fr, s)
	}
	if e != nil {
		return rt.Err[rt.ChildProcess](*e)
	}
	return rt.Ok[rt.ChildProcess, rt.IOError](rt.NewChildProcess(c))
}

func childOf(c rt.ChildProcess) *child {
	ch, ok := c.Child().(*child)
	if !ok {
		rt.Trap("process: the value is not a started process")
	}
	return ch
}

// Stdin is std/process/host's `child_stdin`: the Writer to the child's
// standard input, which `start` piped.
func Stdin(c rt.ChildProcess) rt.Writer {
	ch := childOf(c)
	if !ch.piped[0] {
		rt.Trap("process: the child's standard input is not piped")
	}
	return ch.stdin
}

// Stdout is std/process/host's `child_stdout`: the Reader over the child's
// standard output, which `start` piped.
func Stdout(c rt.ChildProcess) rt.Reader {
	ch := childOf(c)
	if !ch.piped[1] {
		rt.Trap("process: the child's standard output is not piped")
	}
	return ch.stdout
}

// Stderr is std/process/host's `child_stderr`, as Stdout is.
func Stderr(c rt.ChildProcess) rt.Reader {
	ch := childOf(c)
	if !ch.piped[2] {
		rt.Trap("process: the child's standard error is not piped")
	}
	return ch.stderr
}

// Wait is std/process/host's `wait`: closes the child's piped standard
// input, so it sees the end of its input, and waits for it to finish. The
// answer is `[0, code]` when it exited and `[1, signal]` when a signal
// ended it. A second wait answers the first's status. A wait whose task is
// cancelled, or whose deadline passes, kills the child and unwinds.
func Wait(fr *rt.Frame, c rt.ChildProcess) rt.Result[*rt.List[int64], rt.IOError] {
	ch := childOf(c)
	if ch.piped[0] {
		_ = rt.WriterClose(fr, ch.stdin)
	}
	if ch.proc != nil {
		ch.proc.wait(fr, ch)
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.failure != nil {
		return rt.Err[*rt.List[int64]](*ch.failure)
	}
	return rt.Ok[*rt.List[int64], rt.IOError](rt.Cons(ch.status[0], rt.Cons(ch.status[1], nil)))
}

// Captured is std/process/host's `captured`: what the finished child wrote
// to its standard output (stream 1) or error (stream 2) in capture mode.
func Captured(c rt.ChildProcess, stream int64) rt.Bytes {
	ch := childOf(c)
	if stream != 1 && stream != 2 {
		rt.Trap("process: stream must be 1 or 2")
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if b := ch.captured[stream-1]; b != nil {
		return rt.Bytes(b.String())
	}
	return ""
}

// Kill is std/process/host's `kill`: ends the child if it is still running
// and waits for it, so no process outlives the call that started it. A
// finished child is left alone.
func Kill(c rt.ChildProcess) rt.Unit {
	ch := childOf(c)
	if ch.proc != nil {
		ch.proc.kill(ch)
	}
	return rt.Unit{}
}

// ID is std/process/host's `id`: this process's id. A js/wasm or wasip1
// module, which has none, answers 1.
func ID() int64 { return processID() }

// ChildID is std/process/host's `child_id`: the child's process id, which
// stays its own until it is waited for. A fake child's is the position of
// its call, from 1.
func ChildID(c rt.ChildProcess) int64 { return childOf(c).id }

// Signal is std/process/host's `signal`: sends the signal POSIX names
// `name` (`SIGTERM`) to the child, or to every process in the group it
// leads when group is set. A child that has ended, the fake's included,
// which ends as it starts, answers `Other` with code ESRCH, no such
// process; a group signal to a child not started with `new_group` answers
// Unsupported, since its group is the program's own.
func Signal(c rt.ChildProcess, name string, group bool) rt.Result[rt.Unit, rt.IOError] {
	ch := childOf(c)
	op := "process.signal"
	if group {
		op = "process.signal_group"
	}
	if group && !ch.group {
		return rt.Err[rt.Unit](rt.IOError{Tag: rt.TagIOUnsupported, Operation: op, Target: "a child not started with new_group"})
	}
	if ch.proc == nil {
		return rt.Err[rt.Unit](ended(op, ch.program))
	}
	if e := ch.proc.signal(ch, op, name, group); e != nil {
		return rt.Err[rt.Unit](*e)
	}
	return rt.Ok[rt.Unit, rt.IOError](rt.Unit{})
}

// ended is the io.Error for a signal to a child that has ended: ESRCH,
// no such process.
func ended(op, program string) rt.IOError {
	return rt.IOError{Tag: rt.TagIOOther, Operation: op, Path: program, Code: "ESRCH"}
}

// frameOutput is the program's standard output (or error) as an
// io.Writer, so a child inheriting it writes where `io.print` does,
// through an `io.capture` in force.
type frameOutput struct {
	fr     *rt.Frame
	errors bool
}

func (w frameOutput) Write(p []byte) (int, error) {
	target := rt.IOStdout()
	if w.errors {
		target = rt.IOStderr()
	}
	if r := rt.WriterWrite(w.fr, target, rt.Bytes(p)); r.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: r.Err}
	}
	return len(p), nil
}

// readerInput is a Reader as an io.Reader, for a child whose standard
// input it is. It runs on the goroutine that copies the input, so a
// cancellation the read raises ends the copy rather than the program.
type readerInput struct {
	fr *rt.Frame
	r  rt.Reader
}

func (in readerInput) Read(p []byte) (n int, err error) {
	defer func() {
		if recover() != nil {
			n, err = 0, errors.New("process: the input's read was cancelled")
		}
	}()
	chunk := rt.ReaderRead(in.fr, in.r, rt.SmallInt(int64(len(p))))
	if chunk.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: chunk.Err}
	}
	if chunk.Ok.Tag != rt.TagSome {
		return 0, io.EOF
	}
	return copy(p, chunk.Ok.Some), nil
}
