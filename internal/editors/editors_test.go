package editors

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/nomi-language/nomi/editors/helix"
	"github.com/nomi-language/nomi/editors/nvim"
	"github.com/nomi-language/nomi/editors/zed"
	grammar "github.com/nomi-language/nomi/tree-sitter-nomi"
)

const testVersion = "v9.9.9 (0123456789ab)"

// sandbox is a home directory, an empty PATH but for the fake commands a
// test adds, and the log those commands append their arguments to.
type sandbox struct {
	home, bin, log string
}

// newSandbox points every variable an editor's directory is found from
// at a temporary home, and PATH at a directory of fake commands only.
func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake hx, cc and code are shell scripts")
	}
	root := t.TempDir()
	s := &sandbox{home: filepath.Join(root, "home"), bin: filepath.Join(root, "bin"), log: filepath.Join(root, "log")}
	for _, dir := range []string{s.home, s.bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", s.home)
	t.Setenv("PATH", s.bin)
	t.Setenv("FAKE_LOG", s.log)
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "FLATPAK_XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA", "CC"} {
		t.Setenv(v, "")
	}
	return s
}

// fake adds a command to the sandbox's PATH that logs its name and
// arguments, then runs body. It uses only shell builtins, since PATH holds
// nothing else.
func (s *sandbox) fake(t *testing.T, name, body string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s\\n' \"" + name + " $*\" >> \"$FAKE_LOG\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(s.bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The fakes: hx records the first line of the languages.toml it builds
// with and writes a parser through the linked grammars directory; cc
// writes its -o file; code records the first line of the extension.
const (
	fakeHx = `if [ "$1" = --grammar ]; then
  read -r first < "$XDG_CONFIG_HOME/helix/languages.toml"
  printf 'languages: %s\n' "$first" >> "$FAKE_LOG"
  printf 'parser' > "$XDG_CONFIG_HOME/helix/runtime/grammars/nomi.so"
fi`
	fakeCC = `while [ $# -gt 0 ]; do
  if [ "$1" = -o ]; then out="$2"; fi
  shift
done
printf 'parser' > "$out"`
	fakeCode = `if [ "$1" = --install-extension ]; then
  read -r first < "$2"
  printf 'vsix: %s\n' "$first" >> "$FAKE_LOG"
fi`
)

func (s *sandbox) logged(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(s.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// tree is every file and link under dir, by slash path, with its content
// or its link's target.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == dir {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			out[filepath.ToSlash(rel)] = "-> " + target
		case !d.IsDir():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for name, content := range want {
		if g, ok := got[name]; !ok {
			t.Errorf("%s: %s is missing", what, name)
		} else if g != content {
			t.Errorf("%s: %s differs", what, name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: %s is extra", what, name)
		}
	}
}

// embedded is fsys's files under root, by path under root, with prefix
// before each.
func embedded(t *testing.T, fsys fs.FS, root, prefix string, into map[string]string) {
	t.Helper()
	err := fs.WalkDir(fsys, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		rel := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
		if root == "." {
			rel = name
		}
		into[prefix+rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, install bool, name string, opts Options) (string, error) {
	t.Helper()
	var out bytes.Buffer
	opts.Out = &out
	if opts.Version == "" {
		opts.Version = testVersion
	}
	var err error
	if install {
		err = Install(name, opts)
	} else {
		err = Uninstall(name, opts)
	}
	return out.String(), err
}

func mustRun(t *testing.T, install bool, name string, opts Options) string {
	t.Helper()
	out, err := run(t, install, name, opts)
	if err != nil {
		t.Fatalf("install=%v %s: %v\n%s", install, name, err, out)
	}
	return out
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHelix_InstallReinstallUninstall(t *testing.T) {
	s := newSandbox(t)
	s.fake(t, "hx", fakeHx)
	cfg := filepath.Join(s.home, ".config", "helix")
	user := "use-grammars = { except = [\"zig\"] }\n\n[[language]]\nname = \"go\"\nauto-format = true\n"
	write(t, filepath.Join(cfg, "languages.toml"), user)
	write(t, filepath.Join(cfg, "runtime", "queries", "go", "highlights.scm"), "; mine\n")
	before := tree(t, cfg)

	out := mustRun(t, true, "helix", Options{})
	grammarDir := filepath.Join(cfg, "runtime", "grammars", "sources", "nomi")
	want := map[string]string{}
	for k, v := range before {
		want[k] = v
	}
	embedded(t, helix.Files, "runtime/queries", "runtime/queries/", want)
	embedded(t, grammar.Files, "src", "runtime/grammars/sources/nomi/src/", want)
	want["runtime/grammars/sources/nomi/nomi-version"] = testVersion + "\n"
	want["runtime/grammars/nomi.so"] = "parser"
	got := tree(t, cfg)
	languages := got["languages.toml"]
	delete(got, "languages.toml")
	delete(want, "languages.toml")
	sameTree(t, "after install", got, want)

	// The user's text, a blank line, then the block, whose grammar is the
	// copy, and the whole file is TOML Helix can read.
	if !strings.HasPrefix(languages, user+"\n"+blockBegin+"\n") || !strings.HasSuffix(languages, blockEnd+"\n") {
		t.Errorf("languages.toml after install:\n%s", languages)
	}
	if source := `source = { path = "` + filepath.ToSlash(grammarDir) + `" }`; !strings.Contains(languages, "\n"+source+"\n") {
		t.Errorf("languages.toml has no %s:\n%s", source, languages)
	}
	var parsed map[string]any
	if _, err := toml.Decode(languages, &parsed); err != nil {
		t.Errorf("languages.toml after install is not TOML: %v\n%s", err, languages)
	}
	wantLog := []string{
		"hx --grammar build",
		`languages: use-grammars = { only = ["nomi"] }`,
		"hx --health nomi",
	}
	if got := s.logged(t); !slices.Equal(got, wantLog) {
		t.Errorf("commands run:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantLog, "\n"))
	}
	for _, line := range []string{
		"wrote " + filepath.Join(grammarDir, "src", "parser.c"),
		"wrote " + filepath.Join(cfg, "runtime", "queries", "nomi", "highlights.scm"),
		"wrote " + filepath.Join(cfg, "languages.toml"),
		"run hx --health nomi",
	} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("the install does not print %q:\n%s", line, out)
		}
	}

	// A second install changes nothing.
	installed := tree(t, cfg)
	mustRun(t, true, "helix", Options{})
	sameTree(t, "after reinstall", tree(t, cfg), installed)

	// Uninstall leaves what the user had, byte for byte.
	mustRun(t, false, "helix", Options{})
	sameTree(t, "after uninstall", tree(t, cfg), before)
	if out := mustRun(t, false, "helix", Options{}); out != "Helix has no Nomi support in "+cfg+"\n" {
		t.Errorf("a second uninstall printed %q", out)
	}
}

func TestHelix_WithoutHxEverythingButTheGrammarIsInstalled(t *testing.T) {
	s := newSandbox(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(s.home, "xdg"))
	out, err := run(t, true, "helix", Options{})
	want := "hx is not on PATH, so Nomi's grammar is not built; the rest is installed.\n" +
		"Install Helix, then run `nomi editor install helix` again."
	if err == nil || err.Error() != want {
		t.Fatalf("install without hx: %v, want\n%s", err, want)
	}
	cfg := filepath.Join(s.home, "xdg", "helix")
	for _, file := range []string{"languages.toml", "runtime/queries/nomi/highlights.scm", "runtime/grammars/sources/nomi/src/parser.c"} {
		if !present(filepath.Join(cfg, file)) {
			t.Errorf("%s is not written:\n%s", file, out)
		}
	}
	if strings.Contains(out, "run ") {
		t.Errorf("the install ran a command:\n%s", out)
	}
}

func TestNvim_InstallReinstallUninstall(t *testing.T) {
	s := newSandbox(t)
	s.fake(t, "cc", fakeCC)
	site := filepath.Join(s.home, ".local", "share", "nvim", "site")
	write(t, filepath.Join(site, "pack", "other", "start", "x", "plugin", "x.lua"), "-- mine\n")
	before := tree(t, site)

	mustRun(t, true, "nvim", Options{})
	pack := filepath.Join(site, "pack", "nomi", "start", "nomi")
	want := map[string]string{}
	embedded(t, nvim.Files, ".", "", want)
	want["nomi-version"] = testVersion + "\n"
	want["parser/nomi.so"] = "parser"
	sameTree(t, "the package directory", tree(t, pack), want)
	src := filepath.Join(pack, "parser", "src")
	wantLog := []string{"cc -shared -fPIC -O2 -I " + src + " " + filepath.Join(src, "parser.c") + " " +
		filepath.Join(src, "scanner.c") + " -o " + filepath.Join(pack, "parser", "nomi.so")}
	if got := s.logged(t); !slices.Equal(got, wantLog) {
		t.Errorf("commands run:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantLog, "\n"))
	}

	installed := tree(t, site)
	mustRun(t, true, "nvim", Options{})
	sameTree(t, "after reinstall", tree(t, site), installed)

	mustRun(t, false, "nvim", Options{})
	sameTree(t, "after uninstall", tree(t, site), before)
	if present(filepath.Join(site, "pack", "nomi")) {
		t.Error("uninstall leaves pack/nomi")
	}
}

func TestNvim_CCNamesTheCompiler(t *testing.T) {
	s := newSandbox(t)
	s.fake(t, "clang-x", fakeCC)
	t.Setenv("CC", "clang-x -m64")
	mustRun(t, true, "nvim", Options{})
	if got := s.logged(t); len(got) != 1 || !strings.HasPrefix(got[0], "clang-x -m64 -shared ") {
		t.Errorf("commands run: %q", got)
	}
}

func TestNvim_WithoutACompilerThePluginIsInstalled(t *testing.T) {
	for _, tc := range []struct{ cc, want string }{
		{"", "there is no C compiler: CC is not set and cc is not on PATH"},
		{"gcc-13", "there is no C compiler: CC names gcc-13, which is not on PATH"},
	} {
		s := newSandbox(t)
		t.Setenv("CC", tc.cc)
		_, err := run(t, true, "nvim", Options{})
		want := tc.want + ", so Neovim's Nomi parser is not built; the rest is installed.\n" +
			"Install a C compiler (on macOS, `xcode-select --install`), then run `nomi editor install nvim` again."
		if err == nil || err.Error() != want {
			t.Errorf("CC=%q: %v, want\n%s", tc.cc, err, want)
		}
		pack := filepath.Join(s.home, ".local", "share", "nvim", "site", "pack", "nomi", "start", "nomi")
		if !present(filepath.Join(pack, "lsp", "nomi.lua")) || present(filepath.Join(pack, "parser")) {
			t.Errorf("CC=%q: the package directory holds %v", tc.cc, tree(t, pack))
		}
	}
}

// withExtensions makes this nomi carry a VS Code extension and a Zed
// extension's WebAssembly, as a release does, until t ends.
func withExtensions(t *testing.T) {
	savedVSIX, savedZed := vsix, zedWasm
	vsix, zedWasm = []byte("fake vsix\n"), []byte("fake wasm\n")
	t.Cleanup(func() { vsix, zedWasm = savedVSIX, savedZed })
}

func TestVSCode(t *testing.T) {
	s := newSandbox(t)
	if _, err := run(t, true, "vscode", Options{}); err != errNoVSIX {
		t.Errorf("install from a build without the extension: %v", err)
	}
	withExtensions(t)
	want := "code is not on PATH. In VS Code, run \"Shell Command: Install 'code' command in PATH\",\nthen run this again."
	if _, err := run(t, true, "vscode", Options{}); err == nil || err.Error() != want {
		t.Errorf("install with no code: %v", err)
	}
	s.fake(t, "code", fakeCode)
	mustRun(t, true, "vscode", Options{})
	mustRun(t, false, "vscode", Options{})
	got := s.logged(t)
	if len(got) != 3 || !strings.HasPrefix(got[0], "code --install-extension ") || !strings.HasSuffix(got[0], "/nomi.vsix --force") ||
		got[1] != "vsix: fake vsix" || got[2] != "code --uninstall-extension "+vscodeExtension {
		t.Fatalf("commands run:\n%s", strings.Join(got, "\n"))
	}
	file := strings.TrimSuffix(strings.TrimPrefix(got[0], "code --install-extension "), " --force")
	if present(filepath.Dir(file)) {
		t.Errorf("the extension's temporary directory %s is left", filepath.Dir(file))
	}
}

// The extension's identifier is editors/vscode's package.json's.
func TestVSCodeExtensionIsThePackages(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "editors", "vscode", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Publisher, Name string }
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if got := pkg.Publisher + "." + pkg.Name; got != vscodeExtension {
		t.Errorf("editors/vscode is %s, vscodeExtension %s", got, vscodeExtension)
	}
}

func zedTestDir(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Zed", "extensions", "installed", "nomi")
	}
	return filepath.Join(home, ".local", "share", "zed", "extensions", "installed", "nomi")
}

func TestZed_InstallReinstallUninstall(t *testing.T) {
	s := newSandbox(t)
	if _, err := run(t, true, "zed", Options{}); err != errNoZedWasm {
		t.Errorf("install from a build without the extension: %v", err)
	}
	withExtensions(t)
	installed := filepath.Dir(zedTestDir(s.home))
	write(t, filepath.Join(installed, "other", "extension.toml"), "id = \"other\"\n")
	before := tree(t, installed)

	out := mustRun(t, true, "zed", Options{})
	if !strings.HasSuffix(out, "Restart Zed to load the change.\n") {
		t.Errorf("install does not say to restart Zed:\n%s", out)
	}
	want := map[string]string{}
	embedded(t, zed.Files, ".", "", want)
	wasm, err := fs.ReadFile(grammar.Files, "tree-sitter-nomi.wasm")
	if err != nil {
		t.Fatal(err)
	}
	want["grammars/nomi.wasm"] = string(wasm)
	want["extension.wasm"] = "fake wasm\n"
	want["nomi-version"] = testVersion + "\n"
	sameTree(t, "the extension", tree(t, zedTestDir(s.home)), want)

	after := tree(t, installed)
	mustRun(t, true, "zed", Options{})
	sameTree(t, "after reinstall", tree(t, installed), after)

	mustRun(t, false, "zed", Options{})
	sameTree(t, "after uninstall", tree(t, installed), before)
}

// An extension nomi did not write, a dev extension's link or a registry
// install, is left alone, unless an install is forced.
func TestZed_LeavesAnExtensionItDidNotWrite(t *testing.T) {
	s := newSandbox(t)
	withExtensions(t)
	dir := zedTestDir(s.home)
	checkout := filepath.Join(s.home, "nomi", "editors", "zed")
	write(t, filepath.Join(checkout, "extension.toml"), "id = \"nomi\"\n")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(checkout, dir); err != nil {
		t.Fatal(err)
	}
	home := tree(t, s.home)
	todo := "Uninstall it on Zed's extensions page, or run `nomi editor install zed --force` to replace it."
	wantLinked := dir + " is a dev extension, a link to " + checkout + ", which nomi leaves alone.\n"
	if _, err := run(t, true, "zed", Options{}); err == nil || err.Error() != wantLinked+todo {
		t.Errorf("install over a dev extension: %v", err)
	}
	if _, err := run(t, false, "zed", Options{}); err == nil || err.Error() != wantLinked+"Uninstall it on Zed's extensions page." {
		t.Errorf("uninstall of a dev extension: %v", err)
	}
	sameTree(t, "after refusing", tree(t, s.home), home)

	mustRun(t, true, "zed", Options{Force: true})
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || !present(filepath.Join(dir, "extension.wasm")) {
		t.Errorf("a forced install does not replace the link with the extension: %v", err)
	}
	if !present(filepath.Join(checkout, "extension.toml")) {
		t.Error("a forced install over a link removed what it linked to")
	}

	// A directory with no marker, as Zed's registry unpacks one.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "extension.toml"), "id = \"nomi\"\n")
	wantOther := dir + " holds a Nomi extension nomi did not write, from Zed's registry or by hand, which nomi leaves alone.\n"
	if _, err := run(t, true, "zed", Options{}); err == nil || err.Error() != wantOther+todo {
		t.Errorf("install over a registry extension: %v", err)
	}
	if _, err := run(t, false, "zed", Options{}); err == nil || err.Error() != wantOther+"Uninstall it on Zed's extensions page." {
		t.Errorf("uninstall of a registry extension: %v", err)
	}
	if got := tree(t, dir); len(got) != 1 {
		t.Errorf("the registry extension was changed: %v", got)
	}
}

// A dry run prints the steps of the real one, each as "would ...", and
// changes nothing: not on install, and not on uninstall of what an
// install wrote.
func TestDryRunChangesNothing(t *testing.T) {
	s := newSandbox(t)
	withExtensions(t)
	s.fake(t, "hx", fakeHx)
	s.fake(t, "cc", fakeCC)
	s.fake(t, "code", fakeCode)
	for _, install := range []bool{true, false} {
		for _, name := range Names() {
			home := tree(t, s.home)
			out := mustRun(t, install, name, Options{DryRun: true})
			for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
				if !strings.HasPrefix(line, "would ") {
					t.Errorf("install=%v %s --dry-run prints %q", install, name, line)
				}
			}
			sameTree(t, name+" --dry-run", tree(t, s.home), home)
			if got := s.logged(t); got != nil {
				t.Errorf("install=%v %s --dry-run ran %q", install, name, got)
			}
			// The real run after the dry one, so the uninstall's dry run has
			// something to remove.
			if install {
				mustRun(t, true, name, Options{})
				if err := os.Remove(s.log); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
		}
	}
	// What the dry run said is what the real run does.
	dry := mustRun(t, false, "nvim", Options{DryRun: true})
	real := mustRun(t, false, "nvim", Options{})
	if strings.ReplaceAll(dry, "would remove ", "removed ") != real {
		t.Errorf("dry run\n%s\nreal run\n%s", dry, real)
	}
}

func TestHelixBlock(t *testing.T) {
	grammarDir := "/g"
	entry, err := helixEntry(grammarDir)
	if err != nil {
		t.Fatal(err)
	}
	block := blockBegin + "\n" + entry + blockEnd + "\n"
	for _, tc := range []struct{ name, before, installed, uninstalled string }{
		{"empty", "", block, ""},
		{"ends in a newline", "a = 1\n", "a = 1\n\n" + block, "a = 1\n"},
		{"ends in a blank line", "a = 1\n\n", "a = 1\n\n\n" + block, "a = 1\n\n"},
		{"no final newline", "a = 1", "a = 1\n\n" + block, "a = 1\n"},
		{"an old block between the user's lines", "a = 1\n\n" + blockBegin + "\nold\n" + blockEnd + "\nb = 2\n",
			"a = 1\n\n" + block + "b = 2\n", "a = 1\nb = 2\n"},
	} {
		got, err := withHelixBlock(tc.before, grammarDir)
		if err != nil || got != tc.installed {
			t.Errorf("%s: installed %q, %v\nwant %q", tc.name, got, err, tc.installed)
			continue
		}
		if again, _ := withHelixBlock(got, grammarDir); again != got {
			t.Errorf("%s: a second install changes %q to %q", tc.name, got, again)
		}
		out, found, err := withoutHelixBlock(got)
		if err != nil || !found || out != tc.uninstalled {
			t.Errorf("%s: uninstalled %q, %v, %v\nwant %q", tc.name, out, found, err, tc.uninstalled)
		}
	}
	// A marker inside a line is not one, and a block with one marker is
	// refused, the file untouched.
	if got, _ := withHelixBlock("# x "+blockBegin+"\n", grammarDir); !strings.HasPrefix(got, "# x "+blockBegin+"\n\n"+blockBegin) {
		t.Errorf("a marker inside a line counts: %q", got)
	}
	for _, text := range []string{blockBegin + "\nx\n", "x\n" + blockEnd + "\n"} {
		if _, err := withHelixBlock(text, grammarDir); err == nil || !strings.Contains(err.Error(), "fix the block by hand") {
			t.Errorf("withHelixBlock(%q): %v", text, err)
		}
		if _, _, err := withoutHelixBlock(text); err == nil {
			t.Errorf("withoutHelixBlock(%q) has no error", text)
		}
	}
}

func TestDirsAreWhereEachEditorReads(t *testing.T) {
	type want struct{ helix, nvim, zed string }
	for _, tc := range []struct {
		goos string
		env  map[string]string
		want want
	}{
		{"linux", nil, want{"/h/.config/helix", "/h/.local/share/nvim", "/h/.local/share/zed"}},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/c", "XDG_DATA_HOME": "/d"}, want{"/c/helix", "/d/nvim", "/d/zed"}},
		{"linux", map[string]string{"FLATPAK_XDG_DATA_HOME": "/f", "XDG_DATA_HOME": "/d"}, want{"/h/.config/helix", "/d/nvim", "/f/zed"}},
		{"darwin", nil, want{"/h/.config/helix", "/h/.local/share/nvim", "/h/Library/Application Support/Zed"}},
		{"darwin", map[string]string{"XDG_CONFIG_HOME": "/c", "XDG_DATA_HOME": "/d"}, want{"/c/helix", "/d/nvim", "/h/Library/Application Support/Zed"}},
		{"windows", map[string]string{"APPDATA": "/r", "LOCALAPPDATA": "/l"}, want{"/r/helix", "/l/nvim-data", "/l/Zed"}},
		{"windows", map[string]string{"APPDATA": "/r", "LOCALAPPDATA": "/l", "XDG_DATA_HOME": "/d"}, want{"/r/helix", "/d/nvim-data", "/l/Zed"}},
	} {
		d := dirs{tc.goos, func(k string) string { return tc.env[k] }, func() (string, error) { return "/h", nil }}
		var got want
		var err error
		for _, f := range []struct {
			to  *string
			get func() (string, error)
		}{{&got.helix, d.helixConfig}, {&got.nvim, d.nvimData}, {&got.zed, d.zedData}} {
			if *f.to, err = f.get(); err != nil {
				t.Fatal(err)
			}
			*f.to = filepath.ToSlash(*f.to)
		}
		if got != tc.want {
			t.Errorf("%s %v: %+v, want %+v", tc.goos, tc.env, got, tc.want)
		}
	}
	d := dirs{"windows", func(string) string { return "" }, func() (string, error) { return "/h", nil }}
	if _, err := d.helixConfig(); err == nil || err.Error() != "APPDATA is not set, so the editor's directory is unknown" {
		t.Errorf("helix on Windows with no APPDATA: %v", err)
	}
}

// Every file nomi carries is its source in the repository, byte for byte:
// the embedding packages read them from where they are, and this holds
// them there. The generated queries among them are held to their
// generators by internal/docscheck's TestZedHighlightsAreGenerated and
// TestHelixAndNeovimQueriesAreGenerated.
func TestEmbeddedFilesAreTheSources(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, c := range []struct {
		dir  string
		fsys fs.FS
		must []string
	}{
		{"editors/helix", helix.Files, []string{"languages.toml", "runtime/queries/nomi/highlights.scm", "runtime/queries/nomi-catalog/highlights.scm"}},
		{"editors/nvim", nvim.Files, []string{"lsp/nomi.lua", "queries/nomi/highlights.scm", "plugin/nomi.lua"}},
		{"editors/zed", zed.Files, []string{"extension.toml", "languages/nomi/highlights.scm", "debug_adapter_schemas/Nomi.json"}},
		{"tree-sitter-nomi", grammar.Files, []string{"src/parser.c", "src/scanner.c", "src/tree_sitter/parser.h", "tree-sitter-nomi.wasm"}},
	} {
		files := map[string]string{}
		embedded(t, c.fsys, ".", "", files)
		for name, content := range files {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.dir), filepath.FromSlash(name)))
			if err != nil {
				t.Errorf("%s/%s is embedded and has no source: %v", c.dir, name, err)
			} else if string(data) != content {
				t.Errorf("%s/%s differs from its source", c.dir, name)
			}
		}
		for _, name := range c.must {
			if _, ok := files[name]; !ok {
				t.Errorf("%s/%s is not embedded", c.dir, name)
			}
		}
	}
}

func TestStatus(t *testing.T) {
	s := newSandbox(t)
	s.fake(t, "cc", fakeCC)
	pack := filepath.Join(s.home, ".local", "share", "nvim", "site", "pack", "nomi", "start", "nomi")
	status := func() string {
		var out bytes.Buffer
		Status(Options{Out: &out, Version: testVersion})
		return out.String()
	}
	if want := "nvim    not installed\n        " + pack + "\n"; !strings.Contains(status(), want) {
		t.Errorf("status before an install:\n%s\nwant\n%s", status(), want)
	}
	mustRun(t, true, "nvim", Options{})
	if want := "nvim    installed, from " + testVersion + "\n"; !strings.Contains(status(), want) {
		t.Errorf("status after an install:\n%s\nwant\n%s", status(), want)
	}
	mustRun(t, true, "nvim", Options{Version: "v0.0.1"})
	if want := "nvim    installed from another version (v0.0.1); run `nomi editor install nvim` again\n"; !strings.Contains(status(), want) {
		t.Errorf("status after an install from another version:\n%s\nwant\n%s", status(), want)
	}
}

func TestUnknownEditor(t *testing.T) {
	_, err := run(t, true, "emacs", Options{})
	if want := `unknown editor "emacs"; the editors are helix, nvim, vscode and zed`; err == nil || err.Error() != want {
		t.Errorf("install emacs: %v", err)
	}
}
