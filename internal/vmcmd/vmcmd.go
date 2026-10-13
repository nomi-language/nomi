// Package vmcmd is `nomi run <file>` and `nomi test <file>` on the VM, with
// their streams supplied by the caller.
//
// The CLI (vm_cmd.go) calls it with the process's own streams, and the golden
// recorders (internal/expectation's population tests, internal/irbuild's irbuild
// population) call it with buffers, so a golden record is what the command
// prints by construction rather than by a second copy of its routing: an FFI
// project through its generated wrapper, anything else in process through
// vmhost.
package vmcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/vmhost"
)

// Run is `nomi run <absPath> [args]` on the VM once the command's own guards
// (a `_test.nomi` file, a stdlib module) have passed. It answers the exit
// status the command exits with.
//
// handleSignals installs the process-wide signal handler, which only a CLI
// that owns the process may do. The run ends when ctx does: in process
// through vmhost.Program.Run, and an FFI project's wrapper is interrupted,
// then killed if it has not exited a second later.
func Run(ctx context.Context, absPath string, stdout, stderr io.Writer, stdin io.Reader, args []string, handleSignals bool) int {
	res, err := ffirun.Prepare(absPath)
	if err != nil {
		fmt.Fprintf(stderr, "nomi run: %v\n", err)
		return 1
	}
	if !res.FastPath && ctx.Done() != nil {
		return runWrapper(ctx, res, absPath, stdout, stderr, stdin, args)
	}
	if !res.FastPath {
		code, err := ffirun.RunCapturedVM(res, absPath, stdout, stderr, stdin, args...)
		if err != nil {
			fmt.Fprintf(stderr, "nomi run: %v\n", err)
			return 1
		}
		return code
	}
	p, err := vmhost.Load(absPath, vmhost.WithErrorOutput(stderr), vmhost.WithInput(stdin))
	if err != nil {
		vmhost.WriteFailure(stderr, err)
		return 1
	}
	if err := p.RequireMain("run"); err != nil {
		fmt.Fprintf(stderr, "nomi run: %v\n", err)
		return 1
	}
	if err := p.Run(ctx, stdout, args, handleSignals); err != nil {
		if blocked, ok := vmhost.IsBlocked(err); ok {
			blocked.Write(stderr, vmhost.DisplayPath(absPath))
		} else {
			vmhost.WriteFailure(stderr, err)
		}
		return 1
	}
	return 0
}

// runWrapper runs an FFI project's wrapper until it exits or ctx ends.
func runWrapper(ctx context.Context, res *ffirun.Result, absPath string, stdout, stderr io.Writer, stdin io.Reader, args []string) int {
	cmd, err := ffirun.CommandVM(res, absPath, args...)
	if err != nil {
		fmt.Fprintf(stderr, "nomi run: %v\n", err)
		return 1
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "nomi run: launching wrapper: %v\n", err)
		return 1
	}
	exited := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case <-exited:
			case <-time.After(time.Second):
				_ = cmd.Process.Kill()
			}
		case <-exited:
		}
	}()
	err = cmd.Wait()
	close(exited)
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		fmt.Fprintf(stderr, "nomi run: %v\n", err)
		return 1
	}
	return 0
}

// Tester runs test files the way `nomi test` does, into one report.
type Tester struct {
	// Out is where output written outside every case goes (a task that
	// outlives its case): stdout in text mode, stderr in json mode, where
	// stdout carries only records. A case's own output is reported under it.
	Out io.Writer
	// ReportOut is the stream Rep writes to. An FFI wrapper's report lines
	// are forwarded there; nil means Out, which is the same stream in text
	// mode.
	ReportOut io.Writer
	// Stderr and Stdin are an FFI wrapper's.
	Stderr io.Writer
	Stdin  io.Reader
	Rep    *vmhost.TestReport
	Format vmhost.TestFormat
	// Stop, when set, ends the cases running in process when it ends
	// (vmhost.WithStop). An FFI wrapper's cases run to the end.
	Stop context.Context

	ffi map[string]*ffirun.Result
}

// File runs one test file's cases into t.Rep, and answers how many cases the
// run accounted for: the cases it reported, or the counts an FFI wrapper
// reported. A file that fails to load accounts for none.
func (t *Tester) File(file string, opts vmhost.TestOptions) int {
	label := func(name string) string { return vmhost.TestName(file, name) }
	if IsStdlibPath(file) {
		// A stdlib module's cases are built in the module's own scope
		// against the cached stdlib lowering.
		p, err := vmhost.LoadStdlib(file)
		if err != nil {
			t.Rep.FailFile(file, err)
			return 0
		}
		return t.test(p, file, opts, label)
	}
	goRoot, hasGoRoot := ffirun.GoModRoot(file)
	fileDir, _ := filepath.Abs(filepath.Dir(file))
	cacheKey := fileDir
	if hasGoRoot {
		cacheKey = goRoot + "\x00" + fileDir
	}
	if t.ffi == nil {
		t.ffi = map[string]*ffirun.Result{}
	}
	res, cached := t.ffi[cacheKey]
	if !cached {
		var err error
		res, err = ffirun.Prepare(file)
		if err != nil {
			t.Rep.FailFile(file, err)
			return 0
		}
		t.ffi[cacheKey] = res
	}
	if !res.FastPath {
		// The wrapper's stdout is its report, so it goes where Rep writes;
		// a case's own output is in the report, under the case.
		reportOut := t.ReportOut
		if reportOut == nil {
			reportOut = t.Out
		}
		passed, failed, blocked, err := ffirun.RunTestVMWith(res, file, opts.Line, opts.LineSet,
			t.Format.String(), reportOut, t.Stderr, t.Stdin)
		t.Rep.Add(passed, failed)
		t.Rep.AddBlocked(blocked)
		if err != nil {
			t.Rep.FailFile(file, err)
		}
		return passed + failed + blocked
	}
	p, err := vmhost.Load(file, t.loadOptions()...)
	if err != nil {
		t.Rep.FailFile(file, err)
		return 0
	}
	return t.test(p, file, opts, label)
}

func (t *Tester) loadOptions() []vmhost.Option {
	var opts []vmhost.Option
	if t.Stderr != nil {
		opts = append(opts, vmhost.WithErrorOutput(t.Stderr))
	}
	if t.Stop != nil {
		opts = append(opts, vmhost.WithStop(t.Stop))
	}
	return opts
}

func (t *Tester) test(p *vmhost.Program, file string, opts vmhost.TestOptions, label func(string) string) int {
	passed, failed := t.Rep.Counts()
	blocked := t.Rep.BlockedCount()
	p.Test(t.Out, t.Rep, file, opts, label)
	passed2, failed2 := t.Rep.Counts()
	return passed2 - passed + failed2 - failed + t.Rep.BlockedCount() - blocked
}

// IsStdlibPath reports whether path is inside the stdlib's source directory.
func IsStdlibPath(path string) bool {
	stdPath, err := analysis.StdlibPath()
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absStd, err := filepath.Abs(stdPath)
	if err != nil {
		return false
	}
	if realPath, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = realPath
	}
	if realStd, err := filepath.EvalSymlinks(absStd); err == nil {
		absStd = realStd
	}
	rel, err := filepath.Rel(absStd, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
