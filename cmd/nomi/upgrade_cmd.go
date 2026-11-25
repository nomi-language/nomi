package main

// `nomi upgrade`. A release nomi outside a package manager finds the
// release (internal/runners' LatestRelease, or the version named),
// downloads its nomi checked against the release's checksums (ReleaseNomi),
// renames it over itself (replaceExecutable), and runs the new one with
// upgradeEditorsFlag, since the editor files are the binary's own: the new
// nomi rewrites the support of each editor nomi wrote from another version
// (internal/editors' Refresh). Any other nomi prints the command that
// updates it and rewrites the editors' support itself. `nomi --version`
// names each editor whose support is from another version
// (staleEditorLines).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/editors"
	"github.com/nomi-language/nomi/internal/runners"
)

const upgradeUsage = `usage: nomi upgrade [--check] [<version>]

Replaces this nomi with the latest release, or with the release named, an
older one too, once the download matches the release's checksums. Then the
new nomi rewrites the Nomi support of each editor nomi editor install set
up from another version (it runs nomi editor install for each). --check prints this version, the latest release
and the editors an upgrade would rewrite, and changes nothing.

A nomi installed by Homebrew or built from source is not replaced: upgrade
prints the command that updates it, rewrites the editors' support from this
nomi, and exits 1.`

// errUpgradeRefused is a nomi that a package manager or a build from source
// updates; runUpgrade printed the command that does.
var errUpgradeRefused = errors.New("nomi upgrade refused")

// upgradeExecutable is this process's executable. A variable so a test can
// stand in an installed nomi.
var upgradeExecutable = os.Executable

// runNewNomi runs the nomi an upgrade wrote, its output to out. A variable
// so a test can record the call.
var runNewNomi = func(out io.Writer, exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// installKind is how the running nomi was installed, which decides who
// updates it.
type installKind int

const (
	// installRelease is a release archive's nomi outside a package manager,
	// which upgrades itself.
	installRelease installKind = iota
	installHomebrew
	// installCheckout is built in a clone of the repository.
	installCheckout
	// installGoInstall is `go install ...@version`, with no clone.
	installGoInstall
)

// installKindOf is how a nomi at exe, its symlinks resolved, was installed:
// stamped is buildversion.BuiltByReleaseScript and info its build info.
func installKindOf(exe string, stamped bool, info *debug.BuildInfo) installKind {
	switch {
	case slices.Contains(strings.Split(filepath.ToSlash(exe), "/"), "Cellar"):
		return installHomebrew
	case stamped:
		return installRelease
	case buildversion.FromCheckout(info):
		return installCheckout
	}
	return installGoInstall
}

// updateCommand is what updates a nomi of kind that does not upgrade
// itself, to version when it is not "".
func (k installKind) updateCommand(version string) string {
	const notReplaced = ", so nomi upgrade does not replace it.\n"
	switch k {
	case installHomebrew:
		return "This nomi was installed by Homebrew" + notReplaced + "To update it: brew upgrade nomi"
	case installCheckout:
		// make build-cli installs nomi as the repository's own builds do;
		// Windows has no make.
		build := "make build-cli"
		if runtime.GOOS == "windows" {
			build = "go install ./cmd/nomi"
		}
		return "This nomi was built from source" + notReplaced + "To update it, in the clone: git pull && " + build
	}
	if version == "" {
		version = "latest"
	}
	return "This nomi was built with go install" + notReplaced + "To update it: go install " + runners.CompilerModulePath + "/cmd/nomi@" + version
}

// thisInstall is the running nomi's executable, its symlinks resolved, and
// how it was installed.
func thisInstall() (string, installKind, error) {
	exe, err := upgradeExecutable()
	if err != nil {
		return "", 0, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	info, _ := debug.ReadBuildInfo()
	return exe, installKindOf(exe, buildversion.BuiltByReleaseScript(), info), nil
}

// runUpgrade is `nomi upgrade`, its report to out and a refusal's command
// to errOut.
func runUpgrade(out, errOut io.Writer, args []string) error {
	var check bool
	version := ""
	for _, arg := range args {
		switch {
		case arg == "--check":
			check = true
		case isFlag(arg):
			return unknownFlag("nomi upgrade", arg, upgradeUsage)
		case version != "":
			return fmt.Errorf("nomi upgrade: unexpected argument %q\n%s", arg, upgradeUsage)
		default:
			version = arg
		}
	}
	if version != "" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if version != "" && !buildversion.IsRelease(version) {
		return fmt.Errorf("nomi upgrade: %s is not a release version such as v0.0.1", version)
	}
	exe, kind, err := thisInstall()
	if err != nil {
		return fmt.Errorf("nomi upgrade: finding this nomi: %w", err)
	}
	if check {
		return checkUpgrade(out, exe, kind, version)
	}
	if kind != installRelease {
		fmt.Fprintln(errOut, kind.updateCommand(version))
		if err := rewriteEditors(out, false, nil, nil); err != nil {
			fmt.Fprintln(errOut, err)
		}
		return errUpgradeRefused
	}
	current := buildversion.Release()
	named := version != ""
	if !named {
		if version, err = runners.LatestRelease(); err != nil {
			return fmt.Errorf("nomi upgrade: finding the latest release: %w", err)
		}
	}
	if version == current {
		fmt.Fprintln(out, alreadyLine(current, exe, named))
		return rewriteEditors(out, false, nil, nil)
	}
	data, err := runners.ReleaseNomi(version, runners.HostTarget())
	if err != nil {
		return fmt.Errorf("nomi upgrade: %w", err)
	}
	if err := replaceExecutable(exe, data, runtime.GOOS); err != nil {
		return fmt.Errorf("nomi upgrade: %w", err)
	}
	fmt.Fprintf(out, "Upgraded nomi from %s to %s at %s\n", current, version, exe)
	// The editor files are the new binary's, so the new binary writes them,
	// by the command every release has.
	stale := func(w editors.Written) bool { return buildversion.ReleaseOfName(w.Version) != version }
	install := func(name string, log io.Writer) error {
		return runNewNomi(log, exe, "editor", "install", name)
	}
	if err := rewriteEditors(out, true, stale, install); err != nil {
		return fmt.Errorf("nomi %s is installed; %w", version, err)
	}
	return nil
}

// checkUpgrade is `nomi upgrade --check`: this nomi, the latest release, and
// what an upgrade to version, or the latest release, would rewrite.
func checkUpgrade(out io.Writer, exe string, kind installKind, version string) error {
	latest, err := runners.LatestRelease()
	if err != nil {
		return fmt.Errorf("nomi upgrade: finding the latest release: %w", err)
	}
	fmt.Fprintf(out, "This nomi is %s, at %s\n", buildversion.Name(), exe)
	fmt.Fprintf(out, "The latest release is %s\n", latest)
	named := version != ""
	if !named {
		version = latest
	}
	// What the nomi that rewrites the editors' support answers to: the new
	// release, or this nomi when it is not replaced.
	stale := func(w editors.Written) bool { return w.Version != buildversion.Name() }
	replaces := kind == installRelease && version != buildversion.Release()
	switch {
	case kind != installRelease:
		fmt.Fprintln(out, kind.updateCommand(version))
	case replaces:
		fmt.Fprintf(out, "nomi upgrade would install %s\n", version)
		stale = func(w editors.Written) bool { return buildversion.ReleaseOfName(w.Version) != version }
	default:
		fmt.Fprintln(out, alreadyLine(version, exe, named))
	}
	written, others := editors.Found()
	var names []string
	for _, w := range written {
		if stale(w) {
			names = append(names, fmt.Sprintf("%s (from %s)", w.Editor, w.Version))
		}
	}
	if replaces && editors.VSCodeListed() {
		names = append(names, "vscode")
	}
	if len(names) > 0 {
		fmt.Fprintf(out, "It would rewrite the Nomi support of %s\n", joinAnd(names))
	}
	for _, o := range others {
		fmt.Fprintf(out, "It would leave %s alone (%s)\n", o.Editor, o.What)
	}
	return nil
}

// alreadyLine says that the nomi at exe is release, which an upgrade was to
// install: the release named, or the latest.
func alreadyLine(release, exe string, named bool) string {
	if named {
		return fmt.Sprintf("nomi at %s is already %s", exe, release)
	}
	return fmt.Sprintf("nomi at %s is already the latest release, %s", exe, release)
}

// rewriteEditors rewrites, from this nomi's files, the Nomi support of each
// editor nomi wrote from another version (editors.Refresh), and VS Code's
// extension when withVSCode is set. stale and install, when not nil, have
// another nomi write the files (editors.Refresh).
func rewriteEditors(out io.Writer, withVSCode bool, stale func(editors.Written) bool, install func(string, io.Writer) error) error {
	n, err := editors.Refresh(editors.Options{Out: out, Version: buildversion.Name()},
		editors.Refreshing{VSCode: withVSCode, Stale: stale, Install: install})
	if n > 0 {
		fmt.Fprintln(out, "Restart your editor to load the new files.")
	}
	if err != nil {
		return fmt.Errorf("nomi upgrade: %w", err)
	}
	return nil
}

// replaceExecutable makes data the contents of exe: it writes a temporary
// file in exe's directory and renames it over exe, so a process running exe
// keeps its file. Windows cannot rename over a running executable, so there
// exe is first renamed aside (asideName), which the next nomi to start
// removes (removeUpgradeAside). goos is a parameter so a test runs the
// Windows path on any system.
func replaceExecutable(exe string, data []byte, goos string) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".nomi-upgrade-*")
	if err != nil {
		return fmt.Errorf("%s, the directory nomi is in, is not writable: %w", dir, err)
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("writing the new nomi in %s: %w", dir, err)
	}
	if goos == "windows" {
		aside := asideName(exe)
		_ = os.Remove(aside)
		if err := os.Rename(exe, aside); err != nil {
			_ = os.Remove(tmp.Name())
			return fmt.Errorf("moving the running nomi aside: %w", err)
		}
		if err := os.Rename(tmp.Name(), exe); err != nil {
			_ = os.Rename(aside, exe)
			_ = os.Remove(tmp.Name())
			return fmt.Errorf("moving the new nomi to %s: %w", exe, err)
		}
		return nil
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("moving the new nomi to %s: %w", exe, err)
	}
	return nil
}

// asideName is where an upgrade on Windows moves the running exe.
// scripts/install.ps1 moves an installed nomi.exe to the same name, so
// removeUpgradeAside removes what either left.
func asideName(exe string) string { return exe + ".old" }

// removeUpgradeAside removes the nomi an upgrade on Windows set aside, which
// was running then. It is best effort and says nothing: the file is gone, or
// is the running one of another nomi, which a later start removes.
func removeUpgradeAside() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		removeAside(exe)
	}
}

// removeAside removes exe's aside file, if there is one.
func removeAside(exe string) { _ = os.Remove(asideName(exe)) }

// staleEditorLines are what `nomi --version` prints after its line: one per
// editor whose Nomi support nomi wrote from another version, with what
// rewrites it. An editor nomi did not write, or whose directory cannot be
// read, gets none, and so does one a build from source wrote when this nomi
// is one too. It reads one marker file per editor and nothing else unless
// one is stale.
func staleEditorLines() []string {
	written, _ := editors.Found()
	name := buildversion.Name()
	var lines []string
	upgrades := -1 // whether this nomi upgrades itself, once an editor needs it
	for _, w := range written {
		if w.Version == name {
			continue
		}
		if buildversion.Release() == "" && buildversion.ReleaseOfName(w.Version) == "" {
			// Two builds from source, which differ at every commit: a
			// developer's rebuilds are not a version to catch up to.
			continue
		}
		if upgrades < 0 {
			upgrades = 0
			if _, kind, err := thisInstall(); err == nil && kind == installRelease {
				upgrades = 1
			}
		}
		fix := "nomi editor install " + w.Editor
		if upgrades == 1 {
			fix = "nomi upgrade"
		}
		from := w.Version
		if from == "unknown" {
			// A source build with no commit stamped wrote it.
			from = "another version"
		}
		lines = append(lines, fmt.Sprintf("%s: its Nomi support is from %s; run `%s`", w.Editor, from, fix))
	}
	return lines
}
