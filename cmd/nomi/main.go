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
	"io"
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
  nomi <file> [args]                    the same as nomi run, for a file that ends in
                                        .nomi or starts with #!; with a first line of
                                        #!/usr/bin/env nomi it runs as an executable script
  nomi test [path] [--line N] [--format text|json]
                                        run test files on the VM
  nomi check [--format full|short] <path>
                                        analyze and lower without running, test
                                        files and their cases included; short prints
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
	} else if len(os.Args) > 1 && isScriptPath(os.Args[1]) {
		// `nomi hi.nomi a b` is `nomi run hi.nomi a b`, which is what the
		// kernel runs for a `#!/usr/bin/env nomi` script invoked as
		// `./hi.nomi a b`, or as `hi a b` for an extensionless one on PATH.
		runFileVM(os.Args[1], os.Args[2:]...)
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

// isScriptPath reports whether `nomi`'s first argument names a program file
// to run rather than a command. Subcommands are checked first. A name ending
// in `.nomi` is a file whether or not it exists, so a missing one is reported
// as missing rather than as an unknown command; no subcommand ends in `.nomi`,
// so `nomi test` is the command and `nomi test.nomi` runs the file. Any other
// name is a file only when it exists and starts with `#!`, as an
// extensionless script on PATH does: the kernel runs `hi a b` as
// `nomi /home/me/bin/hi a b`.
func isScriptPath(arg string) bool {
	if isFlag(arg) {
		return false
	}
	return strings.HasSuffix(arg, ".nomi") || startsWithShebang(arg)
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
	files, through, singleFile, err := discoverCheckFiles(path)
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
	failed := map[string]bool{}
	for _, file := range files {
		if err := checkFile(file); err != nil {
			failed[file] = true
			vmhost.WriteFailLine(os.Stderr, vmhost.DisplayPath(file), err)
			continue
		}
		fmt.Printf("%s %s\n", termcolor.Green("ok"), vmhost.DisplayPath(file))
	}
	// A file checked through its importers is ok when every one of them is.
	// When one fails, its diagnostics name the file they are in.
	helpers := make([]string, 0, len(through))
	for helper := range through {
		helpers = append(helpers, helper)
	}
	sort.Strings(helpers)
	for _, helper := range helpers {
		importers := through[helper]
		ok := true
		for _, importer := range importers {
			ok = ok && !failed[importer]
		}
		if ok {
			fmt.Printf("%s %s (through %s)\n", termcolor.Green("ok"), vmhost.DisplayPath(helper), filepath.Base(importers[0]))
		}
	}
	if len(failed) > 0 {
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
// builds, or nil when it can: it must be an existing .nomi file, or a file
// of any name whose first line is a `#!` line (an extensionless script).
func entryFileError(cmd, path string) error {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fmt.Errorf("nomi %s: %w", cmd, pathError(path, err))
	case info.IsDir():
		return fmt.Errorf("nomi %s: %s is a directory; name the program's .nomi file", cmd, path)
	case filepath.Ext(path) != ".nomi" && !startsWithShebang(path):
		return fmt.Errorf("nomi %s: %s is not a .nomi file and does not start with a #! line", cmd, path)
	}
	return nil
}

// startsWithShebang reports whether path is a regular file whose first two
// bytes are `#!`.
func startsWithShebang(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 2)
	n, _ := io.ReadFull(f, head)
	return n == 2 && string(head) == "#!"
}

// ownLines reports whether err's text is lines that each stand on their own,
// such as diagnostics that each name their file.
func ownLines(err error) bool {
	var own interface{ OwnLines() bool }
	return errors.As(err, &own) && own.OwnLines()
}

// checkFile checks one file as `nomi run` or `nomi test` loads it, and runs
// nothing. A test file's cases are lowered as `nomi test` lowers them, so a
// case `nomi test` would report BLOCKED is an error here.
func checkFile(absPath string) error {
	res, err := ffirun.Prepare(absPath)
	if err != nil {
		return err
	}
	if !res.FastPath {
		return ffirun.Check(res, absPath)
	}
	return vmhost.Check(absPath)
}

// discoverCheckFiles answers the files `nomi check root` checks as programs,
// and, for a directory without entry points, the files it checks only through
// those programs, each with the programs that load it (programRoots).
func discoverCheckFiles(root string) (files []string, through map[string][]string, singleFile bool, err error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil, false, err
	}
	if !info.IsDir() {
		if !strings.HasSuffix(root, ".nomi") && !startsWithShebang(root) {
			return nil, nil, false, fmt.Errorf("%s is not a .nomi file and does not start with a #! line", root)
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, nil, false, err
		}
		return []string{abs}, nil, true, nil
	}
	if mfst, err := analysis.LoadManifest(root); err == nil && len(mfst.EntryPoints) > 0 {
		files := make([]string, 0, len(mfst.EntryPoints))
		for _, entry := range mfst.EntryPoints {
			entryPath := filepath.FromSlash(entry)
			// An entry names its .nomi file without the extension, or an
			// extensionless `#!` script beside nomi.toml when no such
			// .nomi file exists.
			if filepath.Ext(entryPath) != ".nomi" {
				bare := filepath.Join(root, entryPath)
				if _, err := os.Stat(bare + ".nomi"); err == nil || !startsWithShebang(bare) {
					entryPath += ".nomi"
				}
			}
			abs, err := filepath.Abs(filepath.Join(root, entryPath))
			if err != nil {
				return nil, nil, false, err
			}
			files = append(files, abs)
		}
		// The module's test files, and files that declare tests, are
		// checked with its entries, as `nomi test` would load them.
		tests, err := discoverTestFiles(root)
		if err != nil {
			return nil, nil, false, err
		}
		seen := map[string]bool{}
		for _, f := range files {
			seen[f] = true
		}
		for _, f := range tests {
			if !seen[f] {
				files = append(files, f)
			}
		}
		sort.Strings(files)
		return files, nil, false, nil
	} else if err != nil && !errors.Is(err, analysis.ErrManifestMissing) {
		return nil, nil, false, err
	}

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
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		files = append(files, abs)
		return nil
	})
	if err != nil {
		return nil, nil, false, err
	}
	sort.Strings(files)
	files, through = programRoots(files)
	return files, through, false, nil
}

// programRoots is the files of a directory check that are checked as
// programs. A file another file in the set imports is checked through that
// importer, as `nomi run` and `nomi test` load it: a helper that reads an
// application field is valid only under an entry whose boot returns the
// application, and its errors are reported by its importer's check. A file
// nothing in the set imports is checked on its own. Files that import each
// other with no importer outside their cycle are checked from the first, in
// path order. A stdlib file is always checked on its own, as its module.
func programRoots(files []string) (roots []string, through map[string][]string) {
	inSet := map[string]bool{}
	for _, f := range files {
		inSet[f] = true
	}
	imports := map[string][]string{}
	imported := map[string]bool{}
	for _, f := range files {
		if _, _, ok := frontend.StdlibFile(f); ok {
			continue
		}
		// An unreadable import graph leaves the file a root; its own check
		// reports why.
		paths, _ := frontend.ImportedFiles(f)
		for _, p := range paths {
			if inSet[p] && p != f {
				imports[f] = append(imports[f], p)
				imported[p] = true
			}
		}
	}
	// ImportedFiles is transitive, so a root's imports are every file its
	// program loads.
	isRoot := map[string]bool{}
	reached := map[string]bool{}
	addRoot := func(f string) {
		roots = append(roots, f)
		isRoot[f] = true
		reached[f] = true
		for _, p := range imports[f] {
			reached[p] = true
		}
	}
	for _, f := range files {
		if !imported[f] {
			addRoot(f)
		}
	}
	for _, f := range files {
		if !reached[f] {
			addRoot(f)
		}
	}
	sort.Strings(roots)
	through = map[string][]string{}
	for _, r := range roots {
		for _, p := range imports[r] {
			if !isRoot[p] {
				through[p] = append(through[p], r)
			}
		}
	}
	return roots, through
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
		if !strings.HasSuffix(root, ".nomi") && !startsWithShebang(root) {
			return nil, fmt.Errorf("%s is not a .nomi file and does not start with a #! line", root)
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
