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
