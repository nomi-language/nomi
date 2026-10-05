package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/parser"
)

// errFormatFailed is a `nomi fmt` run that already reported each file it
// could not format on stderr.
var errFormatFailed = errors.New("format failed")

// reportFormatError prints why file could not be formatted: a syntax error as
// `path:line:col: message`, anything else after the path.
func reportFormatError(file string, err error) {
	var pe parser.ParseError
	if errors.As(err, &pe) {
		fmt.Fprintf(os.Stderr, "%s:%d:%d: %s\n", file, pe.Line, pe.Col, pe.Message)
		for _, h := range pe.Hints {
			fmt.Fprintf(os.Stderr, "%s:%d:%d: help: %s\n", file, pe.Line, pe.Col, frontend.OneLineHint(h))
		}
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %v\n", file, err)
}

// runFmt implements the `nomi fmt` subcommand. It supports:
//
//	nomi fmt <paths>...        # format to stdout
//	nomi fmt -w <paths>...     # write in place
//	nomi fmt -l <paths>...     # list files that would change
//	nomi fmt -d <paths>...     # show diff, don't write
//	nomi fmt                   # stdin -> stdout
func runFmt(args []string) error {
	fset := flag.NewFlagSet("fmt", flag.ContinueOnError)
	writeInPlace := fset.Bool("w", false, "write result to source file instead of stdout")
	listChanged := fset.Bool("l", false, "list files that would change")
	showDiff := fset.Bool("d", false, "show diff instead of writing")
	fset.SetOutput(os.Stderr)
	fset.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: nomi fmt [-w|-l|-d] [paths...]")
		fset.PrintDefaults()
	}
	if err := fset.Parse(args); err != nil {
		return err
	}
	paths := fset.Args()

	// Enforce at most one mode flag.
	modeCount := 0
	for _, b := range []bool{*writeInPlace, *listChanged, *showDiff} {
		if b {
			modeCount++
		}
	}
	if modeCount > 1 {
		return fmt.Errorf("conflicting flags: at most one of -w, -l, -d may be set")
	}

	// No paths: read from stdin, write to stdout.
	if len(paths) == 0 {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		out, err := format.Format(string(buf))
		if err != nil {
			return err
		}
		_, _ = os.Stdout.WriteString(out)
		return nil
	}

	var files []string
	for _, p := range paths {
		expanded, err := expandNomiPaths(p)
		if err != nil {
			return fmt.Errorf("nomi fmt: %w", pathError(p, err))
		}
		files = append(files, expanded...)
	}

	var firstErr error
	for _, f := range files {
		srcBytes, err := os.ReadFile(f)
		if err != nil {
			reportFormatError(f, err)
			firstErr = errFormatFailed
			continue
		}
		src := string(srcBytes)
		out, err := format.Format(src)
		if err != nil {
			reportFormatError(f, err)
			firstErr = errFormatFailed
			continue
		}
		if src == out {
			continue
		}
		switch {
		case *listChanged:
			fmt.Println(f)
		case *writeInPlace:
			if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
				reportFormatError(f, err)
				firstErr = errFormatFailed
			}
		case *showDiff:
			// Minimal diff output: label the file and print the formatted content.
			// A proper unified diff is a future enhancement.
			fmt.Printf("--- %s\n", f)
			fmt.Print(out)
		default:
			fmt.Print(out)
		}
	}
	return firstErr
}

// expandNomiPaths returns a slice of .nomi file paths for the given input path.
// If the input is a file, it is returned as-is. If it is a directory, it is
// walked recursively and all .nomi files are collected.
func expandNomiPaths(p string) ([]string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{p}, nil
	}
	var out []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".nomi") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}
