package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `nomi editor install nvim` writes the plugin and the parser into a home's
// Neovim, `nomi editor` then says it is this version's, and uninstall
// removes it; the words it does not take are usage errors.
func TestEditor_InstallsIntoAHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cc is a shell script")
	}
	root := t.TempDir()
	home, bin := filepath.Join(root, "home"), filepath.Join(root, "bin")
	mustWrite(t, filepath.Join(bin, "cc"), "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then out=\"$2\"; fi\n  shift\ndone\nprintf parser > \"$out\"\n")
	if err := os.Chmod(filepath.Join(bin, "cc"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=" + bin}
	pack := filepath.Join(home, ".local", "share", "nvim", "site", "pack", "nomi", "start", "nomi")

	o := runProcess(t, env, root, "", nomiBin, "editor", "install", "nvim")
	if o.exit != 0 || !strings.Contains(o.stdout, "wrote "+filepath.Join(pack, "lsp", "nomi.lua")+"\n") {
		t.Fatalf("nomi editor install nvim: exit %d\n%s", o.exit, o.transcript())
	}
	if data, err := os.ReadFile(filepath.Join(pack, "parser", "nomi.so")); err != nil || string(data) != "parser" {
		t.Errorf("the parser is not built: %v", err)
	}
	o = runProcess(t, env, root, "", nomiBin, "editor")
	if want := "nvim    installed, from " + currentVersion(t) + "\n"; o.exit != 0 || !strings.Contains(o.stdout, want) {
		t.Errorf("nomi editor: exit %d\n%s\nwant a line\n%s", o.exit, o.transcript(), want)
	}
	o = runProcess(t, env, root, "", nomiBin, "editor", "uninstall", "--dry-run", "nvim")
	if o.exit != 0 || !strings.HasPrefix(o.stdout, "would remove "+filepath.Join(home, ".local", "share", "nvim", "site", "pack", "nomi")+"\n") {
		t.Errorf("nomi editor uninstall --dry-run nvim: exit %d\n%s", o.exit, o.transcript())
	}
	o = runProcess(t, env, root, "", nomiBin, "editor", "uninstall", "nvim")
	if o.exit != 0 || exists(pack) {
		t.Errorf("nomi editor uninstall nvim: exit %d\n%s", o.exit, o.transcript())
	}
	for _, args := range [][]string{
		{"editor", "install"},
		{"editor", "remove", "nvim"},
		{"editor", "install", "nvim", "helix"},
		{"editor", "uninstall", "--force", "nvim"},
	} {
		o := runProcess(t, env, root, "", nomiBin, args...)
		if o.exit != 1 || !strings.Contains(o.stderr, editorUsage) {
			t.Errorf("nomi %s: exit %d\n%s", strings.Join(args, " "), o.exit, o.transcript())
		}
	}
}

// currentVersion is what the test's nomi writes in an install's marker.
func currentVersion(t *testing.T) string {
	t.Helper()
	o := runProcess(t, nil, t.TempDir(), "", nomiBin, "version")
	// nomi <version> (<os>/<arch>)
	fields := strings.Fields(o.stdout)
	if o.exit != 0 || len(fields) < 2 {
		t.Fatalf("nomi version: exit %d\n%s", o.exit, o.transcript())
	}
	return fields[1]
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// `nomi editor install --installed` installs for each editor a home has
// Nomi support for and prints each one's steps under its name: with none it
// says so and exits 0; Neovim's support is written again; --dry-run prints
// the steps and changes nothing; an editor it cannot install for is an
// error after the others were installed; and an editor's name beside the
// flag is refused. internal/editors' TestReinstall covers a dev extension
// with and without --force, which needs a nomi that carries Zed's
// extension.
func TestEditor_InstalledInstallsForEachEditorSetUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cc is a shell script")
	}
	root := t.TempDir()
	home, bin := filepath.Join(root, "home"), filepath.Join(root, "bin")
	mustWrite(t, filepath.Join(bin, "cc"), "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then out=\"$2\"; fi\n  shift\ndone\nprintf parser > \"$out\"\n")
	if err := os.Chmod(filepath.Join(bin, "cc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=" + bin}
	pack := filepath.Join(home, ".local", "share", "nvim", "site", "pack", "nomi", "start", "nomi")
	plugin := filepath.Join(pack, "lsp", "nomi.lua")

	o := runProcess(t, env, root, "", nomiBin, "editor", "install", "--installed", "--force")
	if want := "No editor has Nomi support. `nomi editor install <editor>` sets one up.\n"; o.exit != 0 || o.stdout != want {
		t.Errorf("nomi editor install --installed in a home with no editor set up: exit %d\n%s\nwant exit 0 and\n%s", o.exit, o.transcript(), want)
	}
	if got := dirNames(t, home); len(got) != 0 {
		t.Errorf("with no editor set up, the home now holds %q", got)
	}

	if o := runProcess(t, env, root, "", nomiBin, "editor", "install", "nvim"); o.exit != 0 {
		t.Fatalf("nomi editor install nvim: exit %d\n%s", o.exit, o.transcript())
	}
	if err := os.Remove(plugin); err != nil {
		t.Fatal(err)
	}
	o = runProcess(t, env, root, "", nomiBin, "editor", "install", "--installed", "--dry-run")
	if o.exit != 0 || !strings.HasPrefix(o.stdout, "nvim:\nwould remove "+pack+"\n") || !strings.Contains(o.stdout, "would write "+plugin+"\n") {
		t.Errorf("nomi editor install --installed --dry-run: exit %d\n%s", o.exit, o.transcript())
	}
	if exists(plugin) {
		t.Errorf("--dry-run wrote %s", plugin)
	}
	o = runProcess(t, env, root, "", nomiBin, "editor", "install", "--installed")
	if o.exit != 0 || !strings.HasPrefix(o.stdout, "nvim:\nremoved "+pack+"\n") || !strings.Contains(o.stdout, "wrote "+plugin+"\n") || !exists(plugin) {
		t.Errorf("nomi editor install --installed: exit %d\n%s", o.exit, o.transcript())
	}

	// A dev extension in Zed, which this nomi, built without the
	// extension, cannot install over: Neovim's support is still written.
	zedDir := filepath.Join(home, ".local", "share", "zed", "extensions", "installed", "nomi")
	if runtime.GOOS == "darwin" {
		zedDir = filepath.Join(home, "Library", "Application Support", "Zed", "extensions", "installed", "nomi")
	}
	checkout := filepath.Join(root, "checkout", "editors", "zed")
	for _, dir := range []string{checkout, filepath.Dir(zedDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(checkout, zedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(plugin); err != nil {
		t.Fatal(err)
	}
	o = runProcess(t, env, root, "", nomiBin, "editor", "install", "--installed", "--force")
	if o.exit != 1 || !strings.Contains(o.stderr, "zed: this nomi carries no Zed extension") || !exists(plugin) {
		t.Errorf("nomi editor install --installed --force with a Zed extension it cannot write: exit %d\n%s\nwant exit 1, Zed's error, and Neovim's support written", o.exit, o.transcript())
	}
	if target, err := os.Readlink(zedDir); err != nil || target != checkout {
		t.Errorf("the dev extension is now %q, %v", target, err)
	}

	o = runProcess(t, env, root, "", nomiBin, "editor", "install", "--installed", "nvim")
	if want := "nomi editor install: --installed installs for each editor that has Nomi support, and takes no editor (nvim)\n" + editorUsage; o.exit != 1 || !strings.Contains(o.stderr, want) {
		t.Errorf("nomi editor install --installed nvim: exit %d\n%s\nwant exit 1 and\n%s", o.exit, o.transcript(), want)
	}
	o = runProcess(t, env, root, "", nomiBin, "editor", "uninstall", "--installed")
	if o.exit != 1 || !strings.Contains(o.stderr, "--installed") || !exists(plugin) {
		t.Errorf("nomi editor uninstall --installed: exit %d\n%s\nwant exit 1 and nothing removed", o.exit, o.transcript())
	}
}
