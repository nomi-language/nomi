package main

import (
	"errors"
	"fmt"
	"github.com/nomi-language/nomi/internal/dap"
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
  nomi run --profile[=<file>] <file> [args]
                                        run the program and report where its time went
                                        when it ends; =<file> also writes a pprof profile
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
  nomi test [path] [--line N] [--run <regexp>] [--reaching <file>:<line>] [--format text|json] [--timeout <duration>] [--all | --changed] [--watch]
                                        run test files on the VM; --run runs only the
                                        cases whose names (as the report prints them,
                                        after "file :: ") the regexp matches; --reaching
                                        only those whose code can run the function at
                                        that line; --watch runs them
                                        again whenever a .nomi file under path, or a
                                        file the tests load, changes; a case that runs
                                        longer than --timeout (default 10s, 0 for none)
                                        is stopped and fails; a case with no effects
                                        whose code is that of its last pass is not run
                                        and is reported unchanged; --all runs every
                                        case, --changed skips unchanged cases with
                                        effects too
  nomi check [--format full|short|json] [--hints] <path>
                                        analyze and lower without running, test
                                        files and their cases included; short prints
                                        path:line:col: lines, not source snippets
                                        (NOMI_DIAGNOSTICS=short does so for every command);
                                        --hints also reports the editor's hints
  nomi effects [--write | --check | --why <name> | --tree [--tests]] [--format text|json|mermaid] [path]
                                        print the effects the program has,
                                        its own code and its dependencies; --write writes
                                        the tightest [effects.self] and
                                        [effects.deps] tables that pass into
                                        nomi.toml; --check exits 1 when nomi.toml's
                                        tables are not those, wider or narrower;
                                        --why lists the calls that have an effect,
                                        or that a module's files make; --tree prints
                                        the calls from main and the tests down to
                                        each effect, as text or mermaid
  nomi types <paths> [-o <file>]        write TypeScript declarations of the JSON that
                                        the pub types of the named files and directories
                                        cross as, for a frontend; stdout by default
  nomi fmt [-w|-l|-d|-check] [paths]    format source, to standard output; -w writes each
                                        file, -l lists the files that would change, -d prints
                                        the changes, -check lists them and exits 1 if any would;
                                        with no path, formats standard input
  nomi new <module path> [dir]          start a project: nomi.toml and a main.nomi, in
                                        dir (default: the path's last element)
  nomi init [module path]               make the current directory a project: write its
                                        nomi.toml, listing each file with a fn main as an
                                        entry point, and a go.mod beside Go code
  nomi add [<name>=]<module>[@version]  add a dependency to nomi.toml's [dependencies]:
                                        its latest release when no version is given,
                                        downloaded and locked in nomi.lock; prints the
                                        effects its code has
  nomi add [<name>=]<directory>         add the Nomi module in a directory, such as
                                        nomi add ../shared
  nomi remove <name>                    remove a dependency no file imports any more
  nomi update [name]                    move one dependency, or all of them, to its
                                        latest release of the same module path
  nomi fetch [dir]                      download the modules the project needs and lock
                                        them in nomi.lock, so --offline works later
  --offline                             on run, test, check, build, fetch, add and
                                        effects: download nothing, and name what
                                        is missing
  --all-calls                           on run, test, check and build: print every call
                                        nomi.toml's [effects] lists refuse, not
                                        the first of each name
  nomi build <file> [-o <out>] [--target <goos>/<goarch>] [--allow-unsupported <class>]
                                        build a single executable: nomi-vm with the
                                        program's IR appended (runs at nomi run speed);
                                        a program that reaches a call its target cannot
                                        make (files in a browser) is refused, unless
                                        --allow-unsupported names the call's class
  nomi build <file> --no-vm [-o <out>]  write the compiled program alone, as <file>.nomic,
                                        which any nomi-vm of the same program format
                                        runs: nomi-vm app.nomic [args]
  nomi build <file> --web [-o <dir>] [--static <dir>]
                                        build a folder that runs the program in a browser,
                                        with the files of its web/ folder (or --static's)
                                        copied in
  nomi build-vm [--target <goos>/<goarch>] [--compiler] [--no-network] [-o <out>]
                                        write nomi-vm itself, with no program (default nomi-vm);
                                        --no-network writes the smaller one for programs
                                        that never use std::http, std::net, std::tls or std::wasm
  nomi build-vm --reactor [--compiler] [-o <out>]
                                        write the WASI reactor with no program (default nomi-vm-reactor.wasm),
                                        which a runner instantiates per .nomic and hands it
                                        through nomi_load, with nomi-wasi.js (and its types,
                                        nomi-wasi.d.ts) beside it: the loader that runs .nomic
                                        programs in it in a browser or Node
  nomi dap                              serve the Debug Adapter Protocol on stdin and
                                        stdout, for an editor's debugger
  nomi mcp                              serve the Model Context Protocol on stdin and
                                        stdout, so a coding agent can check, test,
                                        format and search Nomi through the compiler
  nomi tool <module> <name> [args]      run a module's tool, tools/<name>.nomi,
                                        such as a code generator
  nomi phrases accept <locale> <name>   mark a locale's translation of a phrase
                                        current, recording the phrase's text as
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
	case "run", "test", "check", "build", "fetch", "add", "types", "effects":
		var rest []string
		rest, offline = takeFlag(command, offlineFlag, os.Args[2:])
		os.Args = append([]string{os.Args[0], command}, rest...)
	}
	switch command {
	case "run", "test", "check", "build":
		rest, allCalls := takeFlag(command, allCallsFlag, os.Args[2:])
		os.Args = append([]string{os.Args[0], command}, rest...)
		if allCalls {
			_ = os.Setenv(vmhost.AllCallsEnv, "1")
		}
	}
	switch command {
	case "dap", "mcp", dap.TerminalCommand, "fmt", "phrases", "version", "help", "--help", "-h", "new", "init", "remove":
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
		args := takeProfileFlag(os.Args[2:])
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
	} else if command == "effects" {
		if err := runEffects(os.Stdout, os.Args[2:]); err != nil {
			if !errors.Is(err, errCheckFailed) {
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
	} else if command == "mcp" {
		os.Exit(runMCP(os.Args[2:]))
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
	} else if command == "types" {
		if err := runTypes(os.Args[2:]); err != nil {
			if !errors.Is(err, errTypesFailed) {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
	} else if command == "phrases" {
		if err := runPhrases(os.Args[2:]); err != nil {
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
	return strings.HasSuffix(arg, ".nomi") || frontend.StartsWithShebang(arg)
}

// isFlag reports whether a command-line word is spelled as a flag.
func isFlag(arg string) bool {
	return len(arg) > 1 && strings.HasPrefix(arg, "-")
}

// unknownFlag is the error a command gives for a flag it does not take.
func unknownFlag(cmd, arg, usage string) error {
	return fmt.Errorf("%s: unknown flag %q\n%s", cmd, arg, usage)
}

// checkJSONFormat is set by `nomi check --format json`: runCheck writes
// JSON Lines records (check_json.go) instead of text.
var checkJSONFormat bool

// checkHints is set by `nomi check --hints`: the check also reports the
// hints the language server shows (vmhost.WithHints).
var checkHints bool

// parseCheckArgs reads `nomi check [--format full|short|json] [--hints] <path>`:
// exactly one path, and the diagnostics format, which it selects for the
// process through NOMI_DIAGNOSTICS. With json, runCheck writes the
// records.
func parseCheckArgs(args []string) (string, error) {
	const checkUsage = "usage: nomi check [--format full|short|json] [--hints] <path>"
	var paths []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, isFormat := strings.CutPrefix(arg, "--format=")
		if arg == "--format" {
			if i+1 >= len(args) {
				return "", fmt.Errorf("nomi check: --format needs full, short or json\n%s", checkUsage)
			}
			i++
			value, isFormat = args[i], true
		}
		if isFormat {
			switch value {
			case "short", "full":
				_ = os.Setenv(frontend.DiagnosticsFormatEnv, value)
			case "json":
				checkJSONFormat = true
				_ = os.Setenv(frontend.DiagnosticsFormatEnv, "json")
			default:
				return "", fmt.Errorf("nomi check: unknown format %q; use full, short or json\n%s", value, checkUsage)
			}
			continue
		}
		if arg == "--hints" {
			checkHints = true
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
	if checkJSONFormat {
		return runCheckJSON(os.Stdout, path)
	}
	files, through, singleFile, err := frontend.CheckedFiles(path)
	if err != nil {
		return fmt.Errorf("nomi check: %w", pathError(path, err))
	}
	if singleFile {
		if err := checkFile(files[0], vmhost.WithWarnings(os.Stderr)); err != nil {
			if ownLines(err) {
				// Diagnostics name their own file.
				vmhost.WriteFailure(os.Stderr, err)
				return errCheckFailed
			}
			return fmt.Errorf("nomi check: %w", err)
		}
		fmt.Printf("%s %s\n", termcolor.Green("ok"), vmhost.DisplayPath(files[0]))
		noteUnreadGoFiles(os.Stderr, path)
		if file, err := goModFailure(path); err != nil {
			vmhost.WriteFailLine(os.Stderr, vmhost.DisplayPath(file), err)
			return errCheckFailed
		}
		return nil
	}

	if len(files) == 0 {
		fmt.Println("no Nomi files found")
		return nil
	}
	failed := map[string]bool{}
	reported := map[string]bool{}
	for _, file := range files {
		if err := checkFile(file, vmhost.WithWarnings(os.Stderr)); err != nil {
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
	noteUnreadGoFiles(os.Stderr, path)
	if file, err := goModFailure(path); err != nil {
		vmhost.WriteFailLine(os.Stderr, vmhost.DisplayPath(file), err)
		failed[file] = true
	}
	if len(failed) > 0 {
		return errCheckFailed
	}
	return nil
}

// writeCheckFailure writes the failed check of the program entry: a
// `FAIL <file>` line for each file that holds errors (a file the entry
// imports, or a phrase catalog), in the order its first error
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
	case filepath.Ext(path) != ".nomi" && !frontend.StartsWithShebang(path):
		return fmt.Errorf("nomi %s: %s is not a .nomi file and does not start with a #! line", cmd, path)
	}
	return nil
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
//
// A program the artifact cache keeps checked cleanly, and its files are as
// they were then, so it is not checked again (vmhost.Cache); its advice,
// kept with it, is written as a fresh check writes it.
//
// warn takes the program's warnings (vmhost.WithWarnings).
func checkFile(absPath string, warn vmhost.Option) error {
	opts := []vmhost.Option{warn}
	if checkHints {
		opts = append(opts, vmhost.WithHints())
	}
	cache := vmhost.OpenCache(absPath)
	if cache.Checked(opts...) {
		return nil
	}
	return vmhost.Check(absPath, append(opts, vmhost.WithCache(cache))...)
}

func runTest(args []string) error {
	parsed, err := parseTestArgs(args)
	if err != nil {
		return err
	}
	if parsed.watch {
		os.Exit(testWatchCommand(parsed))
	}
	return runTestVM(parsed)
}

func isStdlibTestPath(path string) bool { return vmcmd.IsStdlibPath(path) }

const testUsage = "usage: nomi test [path] [--line N] [--run <regexp>] [--reaching <file>:<line>] [--format text|json] [--timeout <duration>] [--all | --changed] [--watch]"

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
	// runs is --all or --changed: which cases a stored pass answers
	// (vmhost.RunPolicy).
	runs vmhost.RunPolicy
	// reaching is --reaching <file>:<line>: only the cases whose code
	// reaches the function there run (vmcmd.Tester.Reaching).
	reaching *vmhost.TestLocation
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
		case arg == "--run" || strings.HasPrefix(arg, "--run="):
			value, inline := strings.CutPrefix(arg, "--run=")
			if !inline {
				i++
				if i >= len(args) {
					return usage()
				}
				value = args[i]
			}
			re, err := vmhost.CompileTestRun(value)
			if err != nil {
				return testArgs{}, fmt.Errorf("nomi test: %w", err)
			}
			parsed.opts.Run = re
		case arg == "--watch":
			parsed.watch = true
		case arg == "--reaching" || strings.HasPrefix(arg, "--reaching="):
			value, inline := strings.CutPrefix(arg, "--reaching=")
			if !inline {
				i++
				if i >= len(args) {
					return usage()
				}
				value = args[i]
			}
			file, lineText, _ := strings.Cut(value, ":")
			line, err := strconv.Atoi(lineText)
			abs, absErr := filepath.Abs(file)
			if file == "" || err != nil || line <= 0 || absErr != nil {
				return testArgs{}, fmt.Errorf("nomi test: invalid --reaching %q: want <file>:<line>", value)
			}
			parsed.reaching = &vmhost.TestLocation{File: abs, Line: line}
		case arg == "--all" || arg == "--changed":
			runs := vmhost.RunAll
			if arg == "--changed" {
				runs = vmhost.RunChanged
			}
			if parsed.runs != vmhost.RunChangedOrEffects && parsed.runs != runs {
				return testArgs{}, fmt.Errorf("nomi test: --all and --changed cannot be used together\n%s", testUsage)
			}
			parsed.runs = runs
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
