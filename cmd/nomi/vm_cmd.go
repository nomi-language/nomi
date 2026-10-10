package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/vmcmd"
	"github.com/nomi-language/nomi/vmhost"
)

// `nomi run` and `nomi test` on the VM. The routing itself is internal/vmcmd's,
// which the golden recorders share.

// runFileVM is `nomi run <file>` on the VM.
func runFileVM(path string, args ...string) {
	if err := entryFileError("run", path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if strings.HasSuffix(filepath.Base(absPath), "_test.nomi") {
		fmt.Fprintf(os.Stderr, "nomi run: %s is a test file; use `nomi test %s`\n",
			vmhost.DisplayPath(absPath), vmhost.DisplayPath(absPath))
		os.Exit(1)
	}
	if isStdlibTestPath(absPath) {
		// A stdlib file is a module, not a program: it has no `main` to run,
		// and its `//!` prompts are tests.
		fmt.Fprintf(os.Stderr, "nomi run: %s is a stdlib module, not a program; to run its tests, use `nomi test %s`\n",
			vmhost.DisplayPath(absPath), vmhost.DisplayPath(absPath))
		os.Exit(1)
	}
	// The CLI owns the process, so it installs the signal handler.
	if code := vmcmd.Run(absPath, os.Stdout, os.Stderr, os.Stdin, args, true); code != 0 {
		os.Exit(code)
	}
}

// runTestVM is `nomi test` on the VM. Every file's cases that can run, run;
// a case the VM cannot run is reported BLOCKED with its reasons, and the
// command exits non-zero when anything failed or was blocked.
func runTestVM(root string, opts vmhost.TestOptions, format vmhost.TestFormat) error {
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		return fmt.Errorf("nomi test: %w", err)
	}
	defer restoreEnv()
	files, err := discoverTestFiles(root)
	if err != nil {
		return fmt.Errorf("nomi test: %w", pathError(root, err))
	}
	rep := vmhost.NewTestReportFormat(os.Stdout, format)
	if len(files) == 0 {
		if format == vmhost.TestFormatJSON {
			rep.Summary()
			return nil
		}
		fmt.Printf("no test files found in %s\n", root)
		return nil
	}
	// A case's own output is reported under the case, in its record in json
	// mode. out takes only a write from outside every case (a task that
	// outlives its case); in json mode stdout carries only records, so that
	// goes to stderr.
	out := os.Stdout
	if format == vmhost.TestFormatJSON {
		out = os.Stderr
	}
	if opts.LineSet && len(files) == 1 {
		if err := noTestAtLine(files[0], opts.Line); err != nil {
			if format != vmhost.TestFormatJSON {
				return fmt.Errorf("nomi test: %w", err)
			}
			// A tool reading records sees the file fail, never a pass.
			rep.FailFile(files[0], err)
			rep.Summary()
			return errTestsFailed
		}
	}
	tester := &vmcmd.Tester{Out: out, ReportOut: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, Rep: rep, Format: format}
	for _, file := range files {
		fileOpts := opts
		if opts.LineSet && len(files) > 1 {
			fileOpts.LineSet = false
		}
		tester.File(file, fileOpts)
	}
	if rep.Summary() {
		return errTestsFailed
	}
	return nil
}

// noTestAtLine is the error for `--line line` selecting nothing in file, so
// a stale line (an editor's rerun after the test moved) fails instead of
// passing with no tests run. A file that does not parse answers nil: loading
// it reports the parse error.
func noTestAtLine(file string, line int) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	selected, nearest, err := frontend.SourceSelectsTestLine(string(src), line)
	if err != nil || selected {
		return nil
	}
	msg := fmt.Sprintf("no test at %s:%d", vmhost.DisplayPath(file), line)
	if nearest > 0 {
		msg += fmt.Sprintf("\nhelp: the nearest test starts on line %d", nearest)
	} else {
		msg += fmt.Sprintf("\nhelp: %s declares no tests", vmhost.DisplayPath(file))
	}
	return errors.New(msg)
}
