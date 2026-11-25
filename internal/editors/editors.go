package editors

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Options are what Install, Uninstall and Status take.
type Options struct {
	// Out receives a line per step and each command's own output.
	Out io.Writer
	// Version is this nomi's version, which an install writes into its
	// marker and Status compares a marker with.
	Version string
	// DryRun prints the steps and changes nothing.
	DryRun bool
	// Force replaces what an install would otherwise leave alone: a Zed
	// extension this command did not write.
	Force bool
}

// editor is one editor `nomi editor` knows.
type editor struct {
	name string
	// install and uninstall add their steps to p. An error they return
	// stops the command before any step runs; what the steps cannot do is
	// p.incomplete's.
	install, uninstall func(p *plan) error
	// status is what `nomi editor` says of the editor after its name.
	status func(version string) []string
	// find is what the editor's directory holds of Nomi support, nil for
	// VS Code, whose install writes no marker: `code` keeps its extension.
	find func() found
}

// found is what an editor's directory holds of Nomi support.
type found struct {
	// dir is the directory `nomi editor` names for the editor.
	dir string
	// version is what the marker an install writes names, "" when there is
	// none.
	version string
	// other says what holds Nomi support there that nomi did not write,
	// which an install leaves alone (Zed's dev extension); "" otherwise.
	other string
	// err is why the directory is unknown or its marker unreadable.
	err error
}

// markerIn is what markerDir's marker says, for the editor whose directory
// is dir.
func markerIn(markerDir, dir string) found {
	data, err := os.ReadFile(filepath.Join(markerDir, markerName))
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist):
		return found{dir: dir}
	case err != nil:
		return found{dir: dir, err: err}
	}
	return found{dir: dir, version: strings.TrimSpace(string(data))}
}

// Written is an editor's Nomi support that `nomi editor install` wrote.
type Written struct {
	// Editor is the editor's name.
	Editor string
	// Version is the version of nomi that wrote it, as Options.Version
	// spells it.
	Version string
	// Dir is the directory `nomi editor` names for it.
	Dir string
}

// Other is an editor's Nomi support that nomi did not write and an install
// leaves alone, such as Zed's dev extension.
type Other struct {
	// Editor is the editor's name, and What says what is there.
	Editor, What string
}

// support is one editor's Nomi support.
type support struct {
	editor string
	found
}

// supports is what each editor's directory holds of Nomi support, for the
// editors that hold any, in table order: support nomi wrote (version) or
// support it did not (other). An editor whose directory is unknown or
// unreadable is left out, and so is VS Code, whose extension records no
// version (VSCodeListed).
func supports() []support {
	var out []support
	for _, e := range table {
		if e.find == nil {
			continue
		}
		if f := e.find(); f.err == nil && (f.other != "" || f.version != "") {
			out = append(out, support{e.name, f})
		}
	}
	return out
}

// Found answers each editor whose Nomi support `nomi editor install` wrote,
// and each whose Nomi support nomi did not write, in table order
// (supports).
func Found() (written []Written, others []Other) {
	for _, s := range supports() {
		if s.other != "" {
			others = append(others, Other{s.editor, s.other})
		} else {
			written = append(written, Written{s.editor, s.version, s.dir})
		}
	}
	return written, others
}

// Refreshing is which editors' Nomi support Refresh rewrites, and how.
type Refreshing struct {
	// VSCode includes VS Code's extension when `code` lists it
	// (VSCodeListed).
	VSCode bool
	// Others includes each editor whose Nomi support nomi did not write,
	// which its install refuses unless Options.Force is set. Unset, Refresh
	// names each in a line and leaves it alone.
	Others bool
	// Steps prints each install's steps to Options.Out under a line that
	// names the editor, as `nomi editor install <editor>` prints them, in
	// place of one line per editor rewritten.
	Steps bool
	// Stale says whether support nomi wrote is to be rewritten. Nil, it is
	// support from a version other than Options.Version.
	Stale func(Written) bool
	// Install rewrites one editor, its output to the writer, for a caller
	// that has another nomi write the files, as `nomi upgrade` does after
	// it replaces itself. Nil, it is Install by this process.
	Install func(name string, out io.Writer) error
}

// Refresh rewrites the Nomi support editors have: of each editor `nomi
// editor install` wrote that r.Stale picks, of each whose support nomi did
// not write when r.Others is set, and VS Code's extension when r.VSCode is
// set and `code` lists it. It answers how many it rewrote. Every editor is
// tried, and the error names each one not rewritten. Without r.Steps it
// prints a line to opts.Out per editor rewritten, and what an install
// prints goes in the error when it fails.
func Refresh(opts Options, r Refreshing) (int, error) {
	stale, install := r.Stale, r.Install
	if stale == nil {
		stale = func(w Written) bool { return w.Version != opts.Version }
	}
	if install == nil {
		install = func(name string, out io.Writer) error {
			quiet := opts
			quiet.Out = out
			return Install(name, quiet)
		}
	}
	var names []string
	for _, s := range supports() {
		switch {
		case s.other != "" && !r.Others:
			fmt.Fprintf(opts.Out, "%s: left alone (%s)\n", s.editor, s.other)
		case s.other != "" || stale(Written{s.editor, s.version, s.dir}):
			names = append(names, s.editor)
		}
	}
	if r.VSCode && VSCodeListed() {
		names = append(names, vscodeEditor.name)
	}
	rewrote := 0
	var errs []error
	for _, name := range names {
		var log strings.Builder
		out := io.Writer(&log)
		if r.Steps {
			fmt.Fprintf(opts.Out, "%s:\n", name)
			out = opts.Out
		}
		if err := install(name, out); err != nil {
			errs = append(errs, fmt.Errorf("%s: %s%w", name, log.String(), err))
			continue
		}
		rewrote++
		if !r.Steps {
			fmt.Fprintf(opts.Out, "Rewrote %s's Nomi support\n", name)
		}
	}
	return rewrote, errors.Join(errs...)
}

// Reinstall is `nomi editor install --installed`: it installs for each
// editor that has Nomi support in any form, as Install does for one, and
// prints each one's steps. An editor whose support nomi did not write is
// refused with Install's error unless opts.Force is set, and the others
// are still installed. With no such editor it says so.
func Reinstall(opts Options) error {
	n, err := Refresh(opts, Refreshing{VSCode: true, Others: true, Steps: true, Stale: func(Written) bool { return true }})
	if n == 0 && err == nil {
		fmt.Fprintln(opts.Out, "No editor has Nomi support. `nomi editor install <editor>` sets one up.")
	}
	return err
}

// table is every editor, in the order `nomi editor` lists them.
var table = []editor{helixEditor, nvimEditor, vscodeEditor, zedEditor}

// Names are the editors' names, in table order.
func Names() []string {
	names := make([]string, len(table))
	for i, e := range table {
		names[i] = e.name
	}
	return names
}

func lookup(name string) (editor, error) {
	for _, e := range table {
		if e.name == name {
			return e, nil
		}
	}
	names := Names()
	return editor{}, fmt.Errorf("unknown editor %q; the editors are %s and %s",
		name, strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
}

// Install writes the named editor's Nomi support.
func Install(name string, opts Options) error {
	e, err := lookup(name)
	if err != nil {
		return err
	}
	return runPlan(e.install, opts)
}

// Uninstall removes what Install wrote for the named editor.
func Uninstall(name string, opts Options) error {
	e, err := lookup(name)
	if err != nil {
		return err
	}
	return runPlan(e.uninstall, opts)
}

// Status prints each editor, whether its Nomi support is installed and
// from which version, and where it goes.
func Status(opts Options) {
	for _, e := range table {
		for i, line := range e.status(opts.Version) {
			label := ""
			if i == 0 {
				label = e.name
			}
			fmt.Fprintf(opts.Out, "%-8s%s\n", label, line)
		}
	}
}

func runPlan(build func(*plan) error, opts Options) error {
	p := &plan{opts: opts}
	if err := build(p); err != nil {
		return err
	}
	if len(p.steps) == 0 && p.after == nil {
		fmt.Fprintln(opts.Out, p.nothing)
		return nil
	}
	for _, s := range p.steps {
		if opts.DryRun {
			fmt.Fprintf(opts.Out, "would %s %s\n", s.verb, s.object)
			continue
		}
		if s.verb == "run" {
			fmt.Fprintf(opts.Out, "run %s\n", s.object)
		}
		if err := s.do(opts.Out); err != nil {
			return err
		}
		switch s.verb {
		case "write":
			fmt.Fprintf(opts.Out, "wrote %s\n", s.object)
		case "remove":
			fmt.Fprintf(opts.Out, "removed %s\n", s.object)
		}
	}
	if p.note != "" && !opts.DryRun {
		fmt.Fprintln(opts.Out, p.note)
	}
	return p.after
}

// plan is the steps of one install or uninstall.
type plan struct {
	opts  Options
	steps []step
	// after is what the steps could not do, reported once they ran.
	after error
	// nothing is what an uninstall with no steps prints.
	nothing string
	// note is printed after the steps of a run that changed something.
	note string
}

// step is one thing a plan does: its verb (write, remove or run) and its
// object (a path, or a command line) are what it prints.
type step struct {
	verb, object string
	do           func(out io.Writer) error
}

func (p *plan) incomplete(err error) { p.after = err }

// write writes data to file, making its directory.
func (p *plan) write(file string, data []byte) {
	p.steps = append(p.steps, step{"write", file, func(io.Writer) error {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		return os.WriteFile(file, data, 0o644)
	}})
}

// writeTree writes every file under root in fsys to the same path under
// dir.
func (p *plan) writeTree(fsys fs.FS, root, dir string) error {
	return fs.WalkDir(fsys, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
		if root == "." {
			rel = name
		}
		p.write(filepath.Join(dir, filepath.FromSlash(rel)), data)
		return nil
	})
}

// remove removes target, a file, a link or a directory with everything
// in it.
func (p *plan) remove(target string) {
	p.steps = append(p.steps, step{"remove", target, func(io.Writer) error { return os.RemoveAll(target) }})
}

// removeIfPresent is remove for a target that is there now.
func (p *plan) removeIfPresent(target string) {
	if present(target) {
		p.remove(target)
	}
}

// run runs a command, its output to the plan's.
func (p *plan) run(env []string, name string, args ...string) {
	line := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	p.runFunc(line, func(out io.Writer) error { return command(out, env, name, args...) })
}

// runFunc adds a step that runs a command shown as line, which do runs.
func (p *plan) runFunc(line string, do func(io.Writer) error) {
	p.steps = append(p.steps, step{"run", line, do})
}

// command runs name with args and env added to this process's
// environment, its output to out.
func command(out io.Writer, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}

func present(target string) bool {
	_, err := os.Lstat(target)
	return err == nil
}

// markerName is the file an install writes its version into.
const markerName = "nomi-version"

// writeMarker writes the plan's version into dir's marker.
func (p *plan) writeMarker(dir string) {
	p.write(filepath.Join(dir, markerName), []byte(p.opts.Version+"\n"))
}

// statusOf is an editor's status for what its directory holds: whether
// nomi installed its support, from which version, and where it goes.
func statusOf(f found, version, name string) []string {
	var first string
	switch {
	case f.dir == "":
		return []string{f.err.Error()}
	case f.err != nil:
		first = f.err.Error()
	case f.other != "":
		first = f.other
	case f.version == "":
		first = "not installed"
	case f.version != version:
		first = fmt.Sprintf("installed from another version (%s); run `nomi editor install %s` again", f.version, name)
	default:
		first = "installed, from " + version
	}
	return []string{first, f.dir}
}

// dirs are where each editor reads its files, from the environment of a
// system: the host's, or a test's.
type dirs struct {
	goos   string
	getenv func(string) string
	home   func() (string, error)
}

var host = dirs{runtime.GOOS, os.Getenv, os.UserHomeDir}

// helixConfig is Helix's configuration directory.
func (d dirs) helixConfig() (string, error) {
	if d.goos == "windows" {
		return d.under("APPDATA", "", "helix")
	}
	return d.under("XDG_CONFIG_HOME", ".config", "helix")
}

// nvimData is Neovim's data directory, stdpath('data').
func (d dirs) nvimData() (string, error) {
	if d.goos == "windows" {
		if d.getenv("XDG_DATA_HOME") != "" {
			return d.under("XDG_DATA_HOME", "", "nvim-data")
		}
		return d.under("LOCALAPPDATA", "", "nvim-data")
	}
	return d.under("XDG_DATA_HOME", filepath.Join(".local", "share"), "nvim")
}

// zedData is Zed's data directory, paths::data_dir in Zed's source.
func (d dirs) zedData() (string, error) {
	switch d.goos {
	case "darwin":
		home, err := d.home()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Zed"), nil
	case "windows":
		return d.under("LOCALAPPDATA", "", "Zed")
	}
	if dir := d.getenv("FLATPAK_XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "zed"), nil
	}
	return d.under("XDG_DATA_HOME", filepath.Join(".local", "share"), "zed")
}

// under is name in the directory the variable env names, else in
// fallback under the home directory. With no fallback the variable must
// be set.
func (d dirs) under(env, fallback, name string) (string, error) {
	if dir := d.getenv(env); dir != "" {
		return filepath.Join(dir, name), nil
	}
	if fallback == "" {
		return "", fmt.Errorf("%s is not set, so the editor's directory is unknown", env)
	}
	home, err := d.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback, name), nil
}

// subdirs are the names of the directories in fsys's dir.
func subdirs(fsys fs.FS, dir string) []string {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		panic(err) // the embedded files are fixed at build time
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// cSources are the names of the C files in fsys's src/.
func cSources(fsys fs.FS) []string {
	names, err := fs.Glob(fsys, "src/*.c")
	if err != nil || len(names) == 0 {
		panic(fmt.Sprintf("the grammar carries no C files: %v", err))
	}
	return names
}
