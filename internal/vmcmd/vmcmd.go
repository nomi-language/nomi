// Package vmcmd is `nomi run <file>` and `nomi test <file>` on the VM, with
// their streams supplied by the caller.
//
// The CLI (vm_cmd.go) calls it with the process's own streams, and the golden
// recorders (internal/expectation's population tests, internal/irbuild's irbuild
// population) call it with buffers, so a golden record is what the command
// prints by construction rather than by a second copy of its routing.
package vmcmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"
)

// Run is `nomi run <absPath> [args]` on the VM once the command's own guards
// (a `_test.nomi` file, a stdlib module) have passed. It answers the exit
// status the command exits with.
//
// handleSignals installs the process-wide signal handler, which only a CLI
// that owns the process may do. The run ends when ctx does
// (vmhost.Program.Run).
func Run(ctx context.Context, absPath string, stdout, stderr io.Writer, stdin io.Reader, args []string, handleSignals bool) int {
	opts := []vmhost.Option{vmhost.WithErrorOutput(stderr), vmhost.WithInput(stdin), vmhost.WithWarnings(stderr)}
	// A program kept in the artifact cache runs without its files being
	// checked or lowered again.
	cache := vmhost.OpenCache(absPath)
	if p, ok := cache.Program(opts...); ok {
		return runLoaded(ctx, p, absPath, stdout, stderr, args, handleSignals)
	}
	p, err := vmhost.Load(absPath, append(opts, vmhost.WithCache(cache))...)
	if err != nil {
		vmhost.WriteFailure(stderr, err)
		return 1
	}
	return runLoaded(ctx, p, absPath, stdout, stderr, args, handleSignals)
}

// runLoaded runs a loaded program's main as `nomi run` does.
func runLoaded(ctx context.Context, p *vmhost.Program, absPath string, stdout, stderr io.Writer, args []string, handleSignals bool) int {
	if err := p.RequireMain("run"); err != nil {
		fmt.Fprintf(stderr, "nomi run: %v\n", err)
		return 1
	}
	if err := p.Run(ctx, stdout, args, handleSignals); err != nil {
		if blocked, ok := vmhost.IsBlocked(err); ok {
			blocked.Write(stderr, vmhost.DisplayPath(absPath))
			return 1
		}
		vmhost.WriteFailure(stderr, err)
		return vmhost.ExitCode(err)
	}
	return 0
}

// Tester runs test files the way `nomi test` does, into one report.
type Tester struct {
	// Out is where output written outside every case goes (a task that
	// outlives its case): stdout in text mode, stderr in json mode, where
	// stdout carries only records. A case's own output is reported under it.
	Out io.Writer
	// Stderr is where a program's error output and warnings go.
	Stderr io.Writer
	Rep    *vmhost.TestReport
	Format vmhost.TestFormat
	// Stop, when set, ends the cases running when it ends (vmhost.WithStop).
	Stop context.Context
	// Timeout is how long each case may run before it is stopped and fails
	// (vmhost.Program.SetTestTimeout), or zero for no limit.
	Timeout time.Duration
	// Runs says which cases run when a stored pass holds their code
	// (vmhost.Program.UseStoredPasses): `--all` and `--changed`. A program
	// the artifact cache does not hold, a stdlib module's among them, runs
	// every case.
	Runs vmhost.RunPolicy
	// Reaching, when set, runs only the cases whose code reaches the
	// function there (vmhost.Program.SelectReaching): `--reaching`. A file
	// is then always loaded.
	Reaching *vmhost.TestLocation
}

// File runs one test file's cases into t.Rep, and answers how many cases the
// run accounted for. A file that fails to load accounts for none.
func (t *Tester) File(file string, opts vmhost.TestOptions) int {
	label := func(name string) string { return vmhost.TestName(file, name) }
	if _, _, std, err := frontend.StdlibFile(file); err != nil {
		t.Rep.FailFile(file, err)
		return 0
	} else if std {
		// A stdlib module's cases are built in the module's own scope
		// against the cached stdlib lowering.
		p, err := vmhost.LoadStdlib(file)
		if err != nil {
			t.Rep.FailFile(file, err)
			return 0
		}
		return t.test(p, file, opts, label)
	}
	// A relative path a test's std/fs call takes is under its package root
	// (vmhost.TestDir), wherever `nomi test` runs.
	loadOpts := append(t.loadOptions(), vmhost.WithFileSystem(vmhost.DiskFilesAt(vmhost.TestDir(file))))
	// A file kept in the artifact cache is tested without its files being
	// checked or lowered again (Run's reason).
	cache := vmhost.OpenCache(file)
	// A file whose every selected case a stored pass of the kept program
	// answers is not loaded at all.
	if n, ok := t.counted(func() bool {
		return t.Reaching == nil && cache.TestUnchanged(t.Rep, file, opts, label, t.Runs, t.Timeout, loadOpts...)
	}); ok {
		return n
	}
	if p, ok := cache.Program(loadOpts...); ok {
		p.UseStoredPasses(cache, t.Runs)
		return t.test(p, file, opts, label)
	}
	p, err := vmhost.Load(file, append(loadOpts, vmhost.WithCache(cache))...)
	if err != nil {
		t.Rep.FailFile(file, err)
		return 0
	}
	p.UseStoredPasses(cache, t.Runs)
	return t.test(p, file, opts, label)
}

func (t *Tester) loadOptions() []vmhost.Option {
	var opts []vmhost.Option
	if t.Stderr != nil {
		opts = append(opts, vmhost.WithErrorOutput(t.Stderr), vmhost.WithWarnings(t.Stderr))
	}
	if t.Stop != nil {
		opts = append(opts, vmhost.WithStop(t.Stop))
	}
	return opts
}

func (t *Tester) test(p *vmhost.Program, file string, opts vmhost.TestOptions, label func(string) string) int {
	n, _ := t.counted(func() bool {
		p.SetTestTimeout(t.Timeout, vmhost.TestTimeoutHint)
		if t.Reaching != nil {
			p.SelectReaching(*t.Reaching)
		}
		p.Test(t.Out, t.Rep, file, opts, label)
		return true
	})
	return n
}

// counted runs report, which reports a file's cases into t.Rep when it
// answers true, and answers how many cases it reported.
func (t *Tester) counted(report func() bool) (int, bool) {
	passed, failed := t.Rep.Counts()
	blocked := t.Rep.BlockedCount()
	if !report() {
		return 0, false
	}
	passed2, failed2 := t.Rep.Counts()
	return passed2 - passed + failed2 - failed + t.Rep.BlockedCount() - blocked, true
}
