package main

import (
	"errors"
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/dap"
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
	"time"
)

var (
	errCheckFailed = errors.New("check failed")
	errTestsFailed = errors.New("tests failed")
)

// usage is the command summary `nomi help` prints.
const usage = `usage:
  nomi                                  start the REPL (bindings, functions and types carry between inputs)
  nomi run <file> [args]                run a program on the VM, from its source or
                                        from a compiled program (.nomic) nomi build --no-vm wrote
  nomi run --watch <file> [args]        run the program, and stop it and run it again
                                        whenever a file it is built from changes
  nomi run --web [--port N] [--open] [--static <dir>] <file>
                                        serve the program in a browser on localhost
                                        (port 8000 or the next free one), and rebuild
                                        and reload the page when its source changes;
                                        serves the files of the web/ folder beside the
                                        program (or --static's) too, a web/index.html
                                        in place of the generated page
  nomi <file> [args]                    the same as nomi run, for a file that ends in
                                        .nomi or starts with #!; with a first line of
                                        #!/usr/bin/env nomi it runs as an executable script
  nomi test [path] [--line N] [--format text|json] [--timeout <duration>] [--watch]
                                        run test files on the VM; --watch runs them
                                        again whenever a .nomi file under path, or a
                                        file the tests load, changes; a case that runs
                                        longer than --timeout (default 10s, 0 for none)
                                        is stopped and fails
  nomi check [--format full|short] <path>
                                        analyze and lower without running, test
                                        files and their cases included; short prints
                                        path:line:col: lines, not source snippets
                                        (NOMI_DIAGNOSTICS=short does so for every command)
  nomi fmt [-w|-l] <paths>              format source
  nomi new <module path> [dir]          start a project: nomi.toml, go.mod and a
                                        main.nomi, in dir (default: the path's last element)
  nomi init [module path]               make the current directory a project: write the
                                        nomi.toml and go.mod it lacks (the path is go.mod's
                                        when there is one), listing each file with a fn main
                                        as an entry point
  nomi add <module>[@version]           add a dependency: its latest release when no
                                        version is given, downloaded, with its checksums
                                        recorded in go.sum
  nomi add <directory>                  add the Nomi module in a directory, such as
                                        nomi add ../shared
  nomi remove <module>                  remove a dependency no file imports any more
  nomi update [module]                  move one dependency, or all of them, to its
                                        latest release of the same module path
  nomi fetch [--target <goos>/<goarch>]... [dir]
                                        download everything the project needs: the
                                        modules go.mod requires and, for Go bindings,
                                        Go itself when there is no go on PATH and each
                                        --target's runner, so --offline works later
  --offline                             on run, test, check, build, fetch and add:
                                        download nothing, and name what is missing
  nomi build <file> [-o <out>] [--target <goos>/<goarch>]
                                        build a single executable: nomi-vm with the
                                        program's IR appended (runs at nomi run speed)
  nomi build <file> --no-vm [-o <out>]  write the compiled program alone, as <file>.nomic,
                                        which any nomi-vm of the same program format
                                        runs: nomi-vm app.nomic [args]
  nomi build <file> --web [-o <dir>] [--static <dir>]
                                        build a folder that runs the program in a browser,
                                        with the files of its web/ folder (or --static's)
                                        copied in
  nomi build-vm [--target <goos>/<goarch>] [--compiler] [-o <out>]
                                        write nomi-vm itself, with no program (default nomi-vm)
  nomi build-vm --reactor [--compiler] [-o <out>]
                                        write the WASI reactor with no program (default nomi-vm-reactor.wasm),
                                        which a runner instantiates per .nomic and hands it
                                        through nomi_load, with nomi-wasi.js (and its types,
                                        nomi-wasi.d.ts) beside it: the loader that runs .nomic
                                        programs in it in a browser or Node
  nomi dap                              serve the Debug Adapter Protocol on stdin and
                                        stdout, for an editor's debugger
  nomi tool <module> <name> [args]      run a module's tool, tools/<name>.nomi,
                                        such as a code generator
  nomi messages accept <locale> <msg>   mark a locale's translation of a message
                                        current, recording the message's text as
                                        the source it was translated from
  nomi version                          print the version
`

func main() {
	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	offline := false
	switch command {
	case "run", "test", "check", "build", "fetch", "add":
		var rest []string
		rest, offline = takeOffline(command, os.Args[2:])
		os.Args = append([]string{os.Args[0], command}, rest...)
	}
	switch command {
	case "dap", dap.TerminalCommand, "fmt", "messages", "version", "help", "--help", "-h", "new", "init", "remove":
		// The debugger is started by an editor, which never downloads, and
		// the others build nothing.
	default:
		enableDownloads(offline, os.Stderr)
	}

	if versionRequested(os.Args) {
		runVersion()
	} else if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Print(usage)
	} else if len(os.Args) > 1 && os.Args[1] == "run" {
		args := os.Args[2:]
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, runUsage)
			os.Exit(1)
		}
		// Everything after the file is the program's own arguments. A flag
		// before it is --watch's (watch_cmd.go) or the browser dev loop's
		// (run_web.go), whose flags may also follow the file.
		if isFlag(args[0]) {
			watch, entry, progArgs, err := parseRunWatchArgs(args)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if watch {
				os.Exit(runWatchCommand(entry, progArgs))
			}
			if err := runWebCommand(args); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
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
	} else if command == "fetch" {
		if err := runFetch(os.Args[2:], offline); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if command == "add" {
		if err := runAdd(os.Args[2:], offline); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if command == "new" {
		if err := runNew(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if command == "init" {
		if err := runInit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if command == "remove" {
		if err := runRemove(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if command == "update" {
		if err := runUpdate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if len(os.Args) > 1 && os.Args[1] == "build-vm" {
		if err := runBuildVM(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if len(os.Args) > 1 && os.Args[1] == "test" {
		if err := runTest(os.Args[2:]); err != nil {
			if !errors.Is(err, errTestsFailed) {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
	} else if len(os.Args) > 1 && os.Args[1] == "dap" {
		os.Exit(runDAP(os.Args[2:]))
	} else if len(os.Args) > 1 && os.Args[1] == dap.TerminalCommand {
		// Hidden: what a debugger's terminal runs to carry a program's
		// input and output (internal/dap's RunTerminal).
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "usage: nomi %s <socket>\n", dap.TerminalCommand)
			os.Exit(1)
		}
		os.Exit(dap.RunTerminal(os.Args[2], os.Stdin, os.Stdout))
	} else if command == "tool" {
		if err := runTool(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else if command == "messages" {
		if err := runMessages(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
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
	reported := map[string]bool{}
	for _, file := range files {
		if err := checkFile(file); err != nil {
			failed[file] = true
			writeCheckFailure(os.Stderr, file, err, reported)
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

// writeCheckFailure writes the failed check of the program entry: a
// `FAIL <file>` line for each file that holds errors (a file the entry
// imports, or a message catalog), in the order its first error
// comes, with that file's errors below it. An error an earlier entry's
// check already printed is not printed again (reported keeps them); an
// entry whose errors were all printed says so. A failure that is not a
// list of diagnostics is the entry's.
func writeCheckFailure(w io.Writer, entry string, err error, reported map[string]bool) {
	var ds vmhost.Diagnostics
	if !errors.As(err, &ds) || len(ds) == 0 {
		vmhost.WriteFailLine(w, vmhost.DisplayPath(entry), err)
		return
	}
	var order []string
	byFile := map[string]vmhost.Diagnostics{}
	for _, d := range ds {
		key := d.String()
		if reported[key] {
			continue
		}
		reported[key] = true
		path := d.Path
		if path == "" {
			path = entry
		}
		if _, seen := byFile[path]; !seen {
			order = append(order, path)
		}
		byFile[path] = append(byFile[path], d)
	}
	if len(order) == 0 {
		fmt.Fprintf(w, "FAIL %s: its errors are printed above\n", vmhost.DisplayPath(entry))
		return
	}
	for _, path := range order {
		vmhost.WriteFailLine(w, vmhost.DisplayPath(path), byFile[path])
	}
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
			if stdFixturesDir(path) {
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

// stdFixturesDir reports whether dir is the standard library's `_fixtures`
// tree. Its modules are std modules only in a test binary run with
// NOMI_STD_FIXTURES=1 (std.FixturesLoaded); this nomi's stdlib does not
// hold them, so a check would read their bare std imports as other files'
// and report type identities that differ only by path.
func stdFixturesDir(dir string) bool {
	if filepath.Base(dir) != "_fixtures" {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	_, stdRoot, ok := frontend.StdlibFile(abs)
	return ok && filepath.Dir(abs) == filepath.Clean(stdRoot)
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
	if parsed.watch {
		os.Exit(testWatchCommand(parsed))
	}
	return runTestVM(parsed.root, parsed.opts, parsed.format, parsed.timeout)
}

func isStdlibTestPath(path string) bool { return vmcmd.IsStdlibPath(path) }

const testUsage = "usage: nomi test [path] [--line N] [--format text|json] [--timeout <duration>] [--watch]"

// testArgs is `nomi test`'s parsed command line. Flags may appear in any order
// around the path.
type testArgs struct {
	root   string
	opts   vmhost.TestOptions
	format vmhost.TestFormat
	// watch is --watch: run the tests again on every change (watch_cmd.go).
	watch bool
	// timeout is --timeout: how long each case may run, or zero for no
	// limit (vmhost.DefaultTestTimeout unless given).
	timeout time.Duration
}

func parseTestArgs(args []string) (testArgs, error) {
	parsed := testArgs{root: ".", format: vmhost.TestFormatText, timeout: vmhost.DefaultTestTimeout}
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
		case arg == "--watch":
			parsed.watch = true
		case arg == "--timeout" || strings.HasPrefix(arg, "--timeout="):
			value, inline := strings.CutPrefix(arg, "--timeout=")
			if !inline {
				i++
				if i >= len(args) {
					return usage()
				}
				value = args[i]
			}
			timeout, err := parseTestTimeout(value)
			if err != nil {
				return testArgs{}, err
			}
			parsed.timeout = timeout
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

// parseTestTimeout reads a `--timeout` value: a Go duration (`30s`, `2m`,
// `500ms`), with `0` for no limit.
func parseTestTimeout(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("nomi test: invalid --timeout %q: want a duration such as 30s, 2m or 500ms, or 0 for no limit", value)
	}
	return d, nil
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
