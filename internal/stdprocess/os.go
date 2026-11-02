//go:build !js && !wasip1

package stdprocess

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/rt"
)

// waitDelay bounds how long a wait waits, once the child has exited, for
// the copies to and from its streams to finish: a grandchild holding a
// captured stream open, or a read of the program's standard input that no
// input answers, does not hold the wait up past it.
const waitDelay = time.Second

// osProcess is a child the operating system runs.
type osProcess struct {
	cmd *exec.Cmd
	// once makes the wait happen once, by `wait` or by `kill`.
	once sync.Once
	// ctx is the context the child was started under: when it ends (the
	// task was cancelled, a deadline passed) the child is killed.
	ctx context.Context
}

// startOS starts s on the operating system.
func startOS(fr *rt.Frame, s spec) (*child, *rt.IOError) {
	ctx := context.Background()
	if fr != nil && fr.Context() != nil {
		ctx = fr.Context()
	}
	cmd := exec.CommandContext(ctx, s.program, s.args...)
	cmd.WaitDelay = waitDelay
	cmd.Dir = workingDir(fr, s.dir)
	if s.clearEnv || len(s.env) > 0 {
		var env []string
		if !s.clearEnv {
			env = os.Environ()
		}
		for _, e := range s.env {
			env = append(env, e.Key+"="+e.Val)
		}
		if env == nil {
			env = []string{}
		}
		cmd.Env = env
	}

	c := &child{program: s.program}
	var closeAfterStart []*os.File
	fail := func(e *rt.IOError) (*child, *rt.IOError) {
		for _, f := range closeAfterStart {
			_ = f.Close()
		}
		return nil, e
	}

	switch s.stdin {
	case stdinInherit:
		cmd.Stdin = readerInput{fr: fr, r: rt.IOStdin()}
	case stdinReader:
		cmd.Stdin = readerInput{fr: fr, r: s.input}
	case stdinPipe:
		r, w, err := os.Pipe()
		if err != nil {
			return fail(startError(s.program, err))
		}
		cmd.Stdin = r
		closeAfterStart = append(closeAfterStart, r)
		c.stdin = rt.NewWriter(rt.WriteSink{Name: "<" + s.program + " stdin>", W: w, Close: w.Close, Fail: streamFailure})
		c.piped[0] = true
	}
	for i, mode := range []int64{s.out, s.err} {
		var target *bytes.Buffer
		switch mode {
		case outInherit:
			w := frameOutput{fr: fr, errors: i == 1}
			if i == 0 {
				cmd.Stdout = w
			} else {
				cmd.Stderr = w
			}
		case outCapture:
			target = new(bytes.Buffer)
			c.captured[i] = target
			if i == 0 {
				cmd.Stdout = target
			} else {
				cmd.Stderr = target
			}
		case outPipe:
			r, w, err := os.Pipe()
			if err != nil {
				return fail(startError(s.program, err))
			}
			closeAfterStart = append(closeAfterStart, w)
			name := "<" + s.program + " stdout>"
			if i == 1 {
				name = "<" + s.program + " stderr>"
			}
			reader := rt.NewReader(rt.ReadSource{Name: name, R: r, Close: r.Close, Fail: streamFailure})
			if i == 0 {
				cmd.Stdout = w
				c.stdout = reader
			} else {
				cmd.Stderr = w
				c.stderr = reader
			}
			c.piped[1+i] = true
		}
	}

	if err := cmd.Start(); err != nil {
		if c.piped[0] {
			rt.WriterDiscard(c.stdin)
		}
		return fail(startError(s.program, err))
	}
	for _, f := range closeAfterStart {
		_ = f.Close()
	}
	c.proc = &osProcess{cmd: cmd, ctx: ctx}
	return c, nil
}

// wait waits for the child once and records its status. When the context
// it was started under ended, the child was killed, and the wait unwinds
// as the cancellation or the deadline does.
func (p *osProcess) wait(fr *rt.Frame, c *child) {
	p.once.Do(func() { p.finish(c) })
	if p.ctx.Err() != nil && fr != nil {
		rt.CancelIfDone(fr)
	}
}

// kill ends the child if it has not finished, then reaps it.
func (p *osProcess) kill(c *child) {
	p.once.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		p.finish(c)
	})
}

func (p *osProcess) finish(c *child) {
	err := p.cmd.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finished = true
	state := p.cmd.ProcessState
	if state == nil {
		e := startError(c.program, err)
		c.failure = e
		return
	}
	c.status = statusOf(state)
}

// statusOf is a finished process's status: `[0, code]`, or `[1, signal]`
// when a signal ended it.
func statusOf(state *os.ProcessState) []int64 {
	if ws, ok := state.Sys().(interface {
		Signaled() bool
		Signal() syscall.Signal
	}); ok && ws.Signaled() {
		return []int64{1, int64(ws.Signal())}
	}
	return []int64{0, int64(state.ExitCode())}
}

// workingDir is the directory the child runs in: dir resolved against the
// program's working directory, which `nomi test` puts at the module root
// without changing the process's own. Inside `fs_testing.run` the files are
// in memory, so a relative dir resolves against the process's directory.
func workingDir(fr *rt.Frame, dir string) string {
	if dir != "" && filepath.IsAbs(filepath.FromSlash(dir)) {
		return filepath.FromSlash(dir)
	}
	if fr == nil || fr.FilesHeld() {
		return filepath.FromSlash(dir)
	}
	cwd := stdio.CurrentDir(fr)
	if cwd.Tag != rt.TagOk {
		return filepath.FromSlash(dir)
	}
	return filepath.Join(filepath.FromSlash(cwd.Ok), filepath.FromSlash(dir))
}

// startError is the io.Error for a program that could not be started.
func startError(program string, err error) *rt.IOError {
	if errors.Is(err, exec.ErrNotFound) {
		return &rt.IOError{Tag: rt.TagIONotFound, Path: program}
	}
	e := stdio.Classify("process.start", program, err)
	return &e
}

// streamFailure is the io.Error for a failed read or write of a child's
// stream.
func streamFailure(op string, err error) rt.IOError {
	return stdio.Classify(op, "", err)
}
