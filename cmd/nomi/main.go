package main

import (
	"errors"
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/dap"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/termcolor"
	"github.com/nomi-language/nomi/internal/vmcmd"
	"github.com/nomi-language/nomi/vmhost"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
                                        from a compiled program (.nomic) nomi build --no-vm wrote;
                                        a project directory whose nomi.toml lists one
                                        entry point runs that entry
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
  nomi new <name> [dir]                 start a project: nomi.toml and a main.nomi, in
                                        dir (default: the name)
  nomi new <module path> [dir]          the same, for a module others download by that
                                        path, with the go.mod it is served with; dir
                                        defaults to the path's last element
  nomi init [name | module path]        make the current directory a project: write its
                                        nomi.toml, listing each file with a fn main as an
                                        entry point, and a module path's go.mod
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
                                        a project directory builds its one entry point;
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
  nomi help [command]                   print this, or a command's usage, as
                                        nomi <command> --help does
  nomi version                          print the version
`

// command is one `nomi` subcommand. main runs the one its first argument
// names with the words after it, and answers `--help` or `-h` among the
// command's own words, and `nomi help <name>`, with its usage on stdout.
type command struct {
	name string
	// aliases are other spellings of the command: `--help` and `-h` for
	// help, `--version` for version.
	aliases []string
	// usage is the command's usage, which --help prints and an error in
	// its arguments ends with.
	usage string
	// programArgs is set for a command whose words after its first one
	// that is not a flag are a program's own, run's program's or tool's
	// tool's: only the flags before that word are the command's.
	programArgs bool
	// offline and allCalls are set for a command that takes --offline
	// (offlineFlag) or --all-calls (allCallsFlag).
	offline, allCalls bool
	// downloads is set for a command that may download a module the
	// project needs (enableDownloads). The debugger is started by an
	// editor, which never downloads, and the others build nothing.
	downloads bool
	// libNote is set for a command that checks or runs a program, whose
	// project's first fetch notes the lib/ modules off this release
	// (noteLibRelease).
	libNote bool
	// run runs the command. An error it returns is printed, unless it says
	// the command printed its reasons already (alreadyReported), and the
	// process exits 1.
	run func(args []string, offline bool) error
}

// commandTable is every `nomi` subcommand. It is a function, not a
// variable, since help's run reads it.
func commandTable() []command {
	return []command{
		{name: "run", usage: runUsage, programArgs: true, offline: true, allCalls: true, downloads: true, libNote: true,
			run: func(args []string, _ bool) error { return runCommand(args) }},
		{name: "test", usage: testUsage, offline: true, allCalls: true, downloads: true, libNote: true,
			run: func(args []string, _ bool) error { return runTest(args) }},
		{name: "check", usage: checkUsage, offline: true, allCalls: true, downloads: true, libNote: true,
			run: func(args []string, _ bool) error {
				path, err := parseCheckArgs(args)
				if err != nil {
					return err
				}
				return runCheck(path)
			}},
		{name: "build", usage: buildUsage, offline: true, allCalls: true, downloads: true, libNote: true,
			run: func(args []string, _ bool) error { return runBuild(args) }},
		{name: "effects", usage: effectsUsage, offline: true, downloads: true,
			run: func(args []string, _ bool) error { return runEffects(os.Stdout, args) }},
		{name: "types", usage: typesUsage, offline: true, downloads: true,
			run: func(args []string, _ bool) error { return runTypes(args) }},
		{name: "fmt", usage: fmtUsage,
			run: func(args []string, _ bool) error { return runFmt(args) }},
		{name: "new", usage: newUsage,
			run: func(args []string, _ bool) error { return runNew(args) }},
		{name: "init", usage: initUsage,
			run: func(args []string, _ bool) error { return runInit(args) }},
		{name: "add", usage: addUsage, offline: true, downloads: true, run: runAdd},
		{name: "remove", usage: removeUsage,
			run: func(args []string, _ bool) error { return runRemove(args) }},
		{name: "update", usage: updateUsage, downloads: true,
			run: func(args []string, _ bool) error { return runUpdate(args) }},
		{name: "fetch", usage: fetchUsage, offline: true, downloads: true, run: runFetch},
		{name: "build-vm", usage: buildVMUsage, downloads: true,
			run: func(args []string, _ bool) error { return runBuildVM(args) }},
		{name: "dap", usage: dapUsage,
			run: func(args []string, _ bool) error { os.Exit(runDAP(args)); return nil }},
		{name: "mcp", usage: mcpUsage,
			run: func(args []string, _ bool) error { os.Exit(runMCP(args)); return nil }},
		// Hidden: what a debugger's terminal runs to carry a program's
		// input and output (internal/dap's RunTerminal).
		{name: dap.TerminalCommand, usage: terminalUsage,
			run: func(args []string, _ bool) error {
				if len(args) != 1 {
					return errors.New(terminalUsage)
				}
				os.Exit(dap.RunTerminal(args[0], os.Stdin, os.Stdout))
				return nil
			}},
		{name: "tool", usage: toolUsage, programArgs: true, downloads: true,
			run: func(args []string, _ bool) error { return runTool(args) }},
		{name: "phrases", usage: phrasesUsage,
			run: func(args []string, _ bool) error { return runPhrases(args) }},
		{name: "version", aliases: []string{"--version"}, usage: versionUsage,
			run: func([]string, bool) error { runVersion(); return nil }},
		{name: "help", aliases: []string{"--help", "-h"}, usage: helpUsage, run: runHelp},
	}
}

const terminalUsage = "usage: nomi " + dap.TerminalCommand + " <socket>"

// lookupCommand is the command that name, or one of its aliases, spells.
func lookupCommand(name string) (command, bool) {
	for _, c := range commandTable() {
		if c.name == name || slices.Contains(c.aliases, name) {
			return c, true
		}
	}
	return command{}, false
}

// helpRequested reports whether `--help` or `-h` is among c's own words in
// args.
func helpRequested(c command, args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
		if c.programArgs && !isFlag(arg) {
			return false
		}
	}
	return false
}

const helpUsage = "usage: nomi help [command]"

// runHelp is `nomi help`: the command summary, or with a command's name
// that command's usage, as `nomi <command> --help` prints it.
func runHelp(args []string, _ bool) error {
	switch len(args) {
	case 0:
		fmt.Print(usage)
		return nil
	case 1:
		c, ok := lookupCommand(args[0])
		if !ok {
			return fmt.Errorf("nomi help: unknown command %q\nrun `nomi help` for usage", args[0])
		}
		fmt.Println(c.usage)
		return nil
	}
	return errors.New(helpUsage)
}

// alreadyReported reports whether err says that the command printed its
// reasons already, so main prints nothing more.
func alreadyReported(err error) bool {
	for _, done := range []error{errCheckFailed, errTestsFailed, errBuildRefused, errTypesFailed, errFormatFailed} {
		if errors.Is(err, done) {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		enableDownloads(false, false, os.Stderr)
		runReplVM()
		return
	}
	name, args := os.Args[1], os.Args[2:]
	c, ok := lookupCommand(name)
	if !ok {
		if isScriptPath(name) {
			// `nomi hi.nomi a b` is `nomi run hi.nomi a b`, which is what the
			// kernel runs for a `#!/usr/bin/env nomi` script invoked as
			// `./hi.nomi a b`, or as `hi a b` for an extensionless one on PATH.
			enableDownloads(false, true, os.Stderr)
			runFileVM(name, args...)
			return
		}
		// The REPL takes no argument, so anything else is a mistake rather
		// than a REPL session.
		if isFlag(name) {
			fmt.Fprintln(os.Stderr, unknownFlag("nomi", name, "run `nomi help` for usage"))
		} else {
			fmt.Fprintf(os.Stderr, "nomi: unknown command %q\nrun `nomi help` for usage\n", name)
		}
		os.Exit(1)
	}
	if helpRequested(c, args) {
		fmt.Println(c.usage)
		return
	}
	offline := false
	if c.offline {
		args, offline = takeFlag(c, offlineFlag, args)
	}
	if c.allCalls {
		var allCalls bool
		if args, allCalls = takeFlag(c, allCallsFlag, args); allCalls {
			_ = os.Setenv(vmhost.AllCallsEnv, "1")
		}
	}
	if c.downloads {
		enableDownloads(offline, c.libNote, os.Stderr)
	}
	if err := c.run(args, offline); err != nil {
		if !alreadyReported(err) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

// runCommand is `nomi run`. Everything after the file is the program's own
// arguments. A flag before it is --watch's (watch_cmd.go) or the browser
// dev loop's (run_web.go), whose flags may also follow the file.
func runCommand(args []string) error {
	args = takeProfileFlag(args)
	if len(args) < 1 {
		return errors.New(runUsage)
	}
	if isFlag(args[0]) {
		watch, entry, progArgs, err := parseRunWatchArgs(args)
		if err != nil {
			return err
		}
		if watch {
			os.Exit(runWatchCommand(entry, progArgs))
		}
		return runWebCommand(args)
	}
	runFileVM(args[0], args[1:]...)
	return nil
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
const checkUsage = "usage: nomi check [--format full|short|json] [--hints] <path>"

func parseCheckArgs(args []string) (string, error) {
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

// programEntry is the absolute path of the program `nomi <cmd> path` runs
// or builds from source, or why path cannot be one. path is a .nomi file, a
// file of any name whose first line is a `#!` line (an extensionless
// script), or a project's directory whose nomi.toml lists one entry point
// (projectEntry). A test file and a stdlib module are not programs.
func programEntry(cmd, path string) (string, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		entry, err := projectEntry(cmd, path)
		if err != nil {
			return "", err
		}
		path = entry
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return "", fmt.Errorf("nomi %s: %w", cmd, pathError(path, err))
	case info.IsDir():
		return "", fmt.Errorf("nomi %s: %s is a directory; name the program's .nomi file", cmd, path)
	case filepath.Ext(path) != ".nomi" && !frontend.StartsWithShebang(path):
		return "", fmt.Errorf("nomi %s: %s is not a .nomi file and does not start with a #! line", cmd, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(filepath.Base(absPath), "_test.nomi") {
		return "", fmt.Errorf("nomi %s: %s is a test file; use `nomi test %s`",
			cmd, vmhost.DisplayPath(absPath), vmhost.DisplayPath(absPath))
	}
	if isStdlibTestPath(absPath) {
		// A stdlib file is a module, not a program: it has no `main`, and
		// its `//!` prompts are tests.
		return "", fmt.Errorf("nomi %s: %s is a stdlib module, not a program; to run its tests, use `nomi test %s`",
			cmd, vmhost.DisplayPath(absPath), vmhost.DisplayPath(absPath))
	}
	return absPath, nil
}

// projectEntry is the file of the one entry point that dir's nomi.toml
// lists (frontend.EntryFile), as `nomi check dir` finds its entries, or why
// dir names no one program: it has no nomi.toml, its nomi.toml lists no
// entry point, or it lists several, which the error names.
func projectEntry(cmd, dir string) (string, error) {
	mfst, err := analysis.LoadManifest(dir)
	if errors.Is(err, analysis.ErrManifestMissing) || err == nil && len(mfst.EntryPoints) == 0 {
		return "", fmt.Errorf("nomi %s: %s is a directory; name the program's .nomi file", cmd, dir)
	}
	if err != nil {
		return "", fmt.Errorf("nomi %s: %w", cmd, err)
	}
	files := make([]string, len(mfst.EntryPoints))
	for i, entry := range mfst.EntryPoints {
		abs, err := frontend.EntryFile(dir, entry)
		if err != nil {
			return "", err
		}
		files[i] = vmhost.DisplayPath(abs)
	}
	if len(files) > 1 {
		return "", fmt.Errorf("nomi %s: %s's nomi.toml lists %d entry points, %s; name the one to %s, such as `nomi %s %s`",
			cmd, dir, len(files), joinAnd(files), cmd, cmd, files[0])
	}
	return files[0], nil
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
