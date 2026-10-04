package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		// A stdlib file is analyzed by the stdlib-aware single-file front end,
		// which the IR lowering does not run.
		(&vmhost.Blocked{Reasons: []string{
			"[stdlib] a stdlib module is not a program the VM lowers",
		}}).Write(os.Stderr, vmhost.DisplayPath(absPath))
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
	// In json mode stdout carries only records, so what a case prints goes to
	// stderr.
	out := os.Stdout
	if format == vmhost.TestFormatJSON {
		out = os.Stderr
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
