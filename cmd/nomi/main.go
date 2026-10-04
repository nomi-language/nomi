package main

import (
	"errors"
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/termcolor"
	"github.com/nomi-language/nomi/internal/vmcmd"
	"github.com/nomi-language/nomi/vmhost"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var (
	errCheckFailed = errors.New("check failed")
	errTestsFailed = errors.New("tests failed")
)

// usage is the command summary `nomi help` prints.
const usage = `usage:
  nomi                                  start the REPL (bindings, functions and types carry between inputs)
  nomi run <file> [args]                run a program on the VM
  nomi test [path] [--line N] [--format text|json]
                                        run test files on the VM
  nomi check [--format full|short] <path>
                                        analyze without running; short prints
                                        path:line:col: lines, not source snippets
                                        (NOMI_DIAGNOSTICS=short does so for every command)
  nomi fmt [-w|-l] <paths>              format source
  nomi build <file> [-o <out>] [--target <goos>/<goarch>]
                                        build a single executable: a VM runner with the
                                        program's IR appended (runs at nomi run speed)
  nomi version                          print the version
`

func main() {
	if versionRequested(os.Args) {
		runVersion()
	} else if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Print(usage)
	} else if len(os.Args) > 1 && os.Args[1] == "run" {
		args := os.Args[2:]
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: nomi run <file> [args]")
			os.Exit(1)
		}
		// Everything after the file is the program's own arguments; before
		// it, `nomi run` takes no flag.
		if isFlag(args[0]) {
			fmt.Fprintln(os.Stderr, unknownFlag("nomi run", args[0], "usage: nomi run <file> [args]"))
			os.Exit(1)
		}
		runFileVM(args[0], args[1:]...)
	} else if len(os.Args) > 1 && os.Args[1] == "check" {
		path, err := parseCheckArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := runCheck(path); err != nil {
			if !errors.Is(err, errCheckFailed) {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
	} else if len(os.Args) > 1 && os.Args[1] == "build" {
		if err := runBuild(os.Args[2:]); err != nil {
			if !errors.Is(err, errBuildRefused) {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
	} else if len(os.Args) > 1 && os.Args[1] == "test" {
		if err := runTest(os.Args[2:]); err != nil {
			if !errors.Is(err, errTestsFailed) {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
	} else if len(os.Args) > 1 && os.Args[1] == "fmt" {
		if err := runFmt(os.Args[2:]); err != nil {
			if !errors.Is(err, errFormatFailed) {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
	} else if len(os.Args) > 1 {
		// The REPL takes no argument, so anything else is a mistake rather
		// than a REPL session.
		arg := os.Args[1]
		if isFlag(arg) {
			fmt.Fprintln(os.Stderr, unknownFlag("nomi", arg, "run `nomi help` for usage"))
		} else {
			fmt.Fprintf(os.Stderr, "nomi: unknown command %q\nrun `nomi help` for usage\n", arg)
		}
		os.Exit(1)
	} else {
		runReplVM()
	}
}

// isFlag reports whether a command-line word is spelled as a flag.
func isFlag(arg string) bool {
	return len(arg) > 1 && strings.HasPrefix(arg, "-")
}

// unknownFlag is the error a command gives for a flag it does not take.
func unknownFlag(cmd, arg, usage string) error {
	return fmt.Errorf("%s: unknown flag %q\n%s", cmd, arg, usage)
}

// parseCheckArgs reads `nomi check [--format full|short] <path>`: exactly one
// path, and the diagnostics format, which it selects for the process (and
// any FFI wrapper it starts) through NOMI_DIAGNOSTICS.
func parseCheckArgs(args []string) (string, error) {
	const checkUsage = "usage: nomi check [--format full|short] <path>"
	var paths []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, isFormat := strings.CutPrefix(arg, "--format=")
		if arg == "--format" {
			if i+1 >= len(args) {
				return "", fmt.Errorf("nomi check: --format needs full or short\n%s", checkUsage)
			}
			i++
			value, isFormat = args[i], true
		}
		if isFormat {
			switch value {
			case "short", "full":
				_ = os.Setenv(frontend.DiagnosticsFormatEnv, value)
			default:
				return "", fmt.Errorf("nomi check: unknown format %q; use full or short\n%s", value, checkUsage)
			}
			continue
		}
		if isFlag(arg) {
			return "", unknownFlag("nomi check", arg, checkUsage)
		}
		paths = append(paths, arg)
	}
	if len(paths) == 0 {
		return "", errors.New(checkUsage)
	}
	if len(paths) > 1 {
		return "", fmt.Errorf("nomi check: unexpected argument %q\n%s", paths[1], checkUsage)
	}
	return paths[0], nil
}

func runCheck(path string) error {
	files, singleFile, err := discoverCheckFiles(path)
	if err != nil {
		return fmt.Errorf("nomi check: %w", pathError(path, err))
	}
	if singleFile {
		if err := checkFile(files[0]); err != nil {
			if ownLines(err) {
				// Diagnostics name their own file.
				vmhost.WriteFailure(os.Stderr, err)
				return errCheckFailed
			}
			return fmt.Errorf("nomi check: %w", err)
		}
		fmt.Printf("%s %s\n", termcolor.Green("ok"), vmhost.DisplayPath(files[0]))
		return nil
	}

	if len(files) == 0 {
		fmt.Println("no Nomi files found")
		return nil
	}
	failed := 0
	for _, file := range files {
		if err := checkFile(file); err != nil {
			failed++
			vmhost.WriteFailLine(os.Stderr, vmhost.DisplayPath(file), err)
			continue
		}
		fmt.Printf("%s %s\n", termcolor.Green("ok"), vmhost.DisplayPath(file))
	}
	if failed > 0 {
		return errCheckFailed
	}
	return nil
}

// pathError is err for a path the user named: a missing path says so
// plainly, rather than as the Go error of the call that found it missing.
func pathError(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: no such file or directory", path)
	}
	return err
}

// entryFileError is why path cannot be the program `nomi <cmd>` runs or
// builds, or nil when it can: it must be an existing .nomi file.
func entryFileError(cmd, path string) error {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fmt.Errorf("nomi %s: %w", cmd, pathError(path, err))
	case info.IsDir():
		return fmt.Errorf("nomi %s: %s is a directory; name the program's .nomi file", cmd, path)
	case filepath.Ext(path) != ".nomi":
		return fmt.Errorf("nomi %s: %s is not a .nomi file", cmd, path)
	}
	return nil
}

// ownLines reports whether err's text is lines that each stand on their own,
// such as diagnostics that each name their file.
func ownLines(err error) bool {
	var own interface{ OwnLines() bool }
	return errors.As(err, &own) && own.OwnLines()
}

func checkFile(absPath string) error {
	if strings.HasSuffix(filepath.Base(absPath), "_test.nomi") {
		return fmt.Errorf("%s is a test file; use `nomi test %s`",
			vmhost.DisplayPath(absPath), vmhost.DisplayPath(absPath))
	}
	res, err := ffirun.Prepare(absPath)
	if err != nil {
		return err
	}
	if !res.FastPath {
		return ffirun.Check(res, absPath)
	}
	return vmhost.Check(absPath)
}

func discoverCheckFiles(root string) ([]string, bool, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		if !strings.HasSuffix(root, ".nomi") {
			return nil, false, fmt.Errorf("%s is not a .nomi file", root)
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, false, err
		}
		return []string{abs}, true, nil
	}
	if mfst, err := analysis.LoadManifest(root); err == nil && len(mfst.EntryPoints) > 0 {
		files := make([]string, 0, len(mfst.EntryPoints))
		for _, entry := range mfst.EntryPoints {
			entryPath := filepath.FromSlash(entry)
			if filepath.Ext(entryPath) != ".nomi" {
				entryPath += ".nomi"
			}
			abs, err := filepath.Abs(filepath.Join(root, entryPath))
			if err != nil {
				return nil, false, err
			}
			files = append(files, abs)
		}
		sort.Strings(files)
		return files, false, nil
	} else if err != nil && !errors.Is(err, analysis.ErrManifestMissing) {
		return nil, false, err
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", ".astro":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".nomi") || strings.HasSuffix(d.Name(), "_test.nomi") {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		files = append(files, abs)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(files)
	return files, false, nil
}

func runTest(args []string) error {
	parsed, err := parseTestArgs(args)
	if err != nil {
		return err
	}
	return runTestVM(parsed.root, parsed.opts, parsed.format)
}

func isStdlibTestPath(path string) bool { return vmcmd.IsStdlibPath(path) }

const testUsage = "usage: nomi test [path] [--line N] [--format text|json]"

// testArgs is `nomi test`'s parsed command line. Flags may appear in any order
// around the path.
type testArgs struct {
	root   string
	opts   vmhost.TestOptions
	format vmhost.TestFormat
}

func parseTestArgs(args []string) (testArgs, error) {
	parsed := testArgs{root: ".", format: vmhost.TestFormatText}
	usage := func() (testArgs, error) { return testArgs{}, errors.New(testUsage) }
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--line":
			i++
			if i >= len(args) {
				return usage()
			}
			line, err := strconv.Atoi(args[i])
			if err != nil {
				return testArgs{}, fmt.Errorf("nomi test: invalid --line %q", args[i])
			}
			parsed.opts.Line = line
			parsed.opts.LineSet = true
		case arg == "--format" || strings.HasPrefix(arg, "--format="):
			value, inline := strings.CutPrefix(arg, "--format=")
			if !inline {
				i++
				if i >= len(args) {
					return usage()
				}
				value = args[i]
			}
			format, err := vmhost.ParseTestFormat(value)
			if err != nil {
				return testArgs{}, fmt.Errorf("nomi test: %w", err)
			}
			parsed.format = format
		case isFlag(arg):
			return testArgs{}, unknownFlag("nomi test", arg, testUsage)
		default:
			if parsed.root != "." {
				return testArgs{}, fmt.Errorf("nomi test: unexpected argument %q\n%s", arg, testUsage)
			}
			parsed.root = arg
		}
	}
	return parsed, nil
}

func discoverTestFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !strings.HasSuffix(root, ".nomi") {
			return nil, fmt.Errorf("%s is not a .nomi file", root)
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		return []string{abs}, nil
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", ".astro":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".nomi") {
			return nil
		}
		hasTests := strings.HasSuffix(d.Name(), "_test.nomi")
		if !hasTests {
			var err error
			hasTests, err = vmhost.FileDeclaresTests(path)
			if err != nil {
				return err
			}
		}
		if hasTests {
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			files = append(files, abs)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
