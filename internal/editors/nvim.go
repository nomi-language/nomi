package editors

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/editors/nvim"
	grammar "github.com/nomi-language/nomi/tree-sitter-nomi"
)

var nvimEditor = editor{
	name:      "nvim",
	install:   installNvim,
	uninstall: uninstallNvim,
	status:    func(version string) []string { return statusOf(findNvim(), version, "nvim") },
	find:      findNvim,
}

// findNvim is what Neovim's package directory for Nomi holds.
func findNvim() found {
	pack, err := nvimPack()
	if err != nil {
		return found{err: err}
	}
	return markerIn(pack, pack)
}

// nvimPack is the package directory the plugin goes in, which Neovim
// loads on startup: site/pack/*/start/* under its data directory.
func nvimPack() (string, error) {
	data, err := host.nvimData()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "site", "pack", "nomi", "start", "nomi"), nil
}

func installNvim(p *plan) error {
	pack, err := nvimPack()
	if err != nil {
		return err
	}
	// Removing the directory first unlinks the parser a running Neovim has
	// mapped, which keeps it, rather than rewriting it under that Neovim.
	p.removeIfPresent(pack)
	if err := p.writeTree(nvim.Files, ".", pack); err != nil {
		return err
	}
	p.writeMarker(pack)

	cc, err := cCompiler()
	if err != nil {
		p.incomplete(fmt.Errorf("%v, so Neovim's Nomi parser is not built; the rest is installed.\n"+
			"Install a C compiler (on macOS, `xcode-select --install`), then run `nomi editor install nvim` again.", err))
		return nil
	}
	parser := filepath.Join(pack, "parser")
	src := filepath.Join(parser, "src")
	if err := p.writeTree(grammar.Files, "src", src); err != nil {
		return err
	}
	args := append(cc[1:], "-shared", "-fPIC", "-O2", "-I", src)
	for _, c := range cSources(grammar.Files) {
		args = append(args, filepath.Join(src, path.Base(c)))
	}
	args = append(args, "-o", filepath.Join(parser, "nomi.so"))
	p.run(nil, cc[0], args...)
	p.remove(src)
	return nil
}

// cCompiler is the C compiler and its own arguments: $CC, else cc.
func cCompiler() ([]string, error) {
	cc := strings.Fields(os.Getenv("CC"))
	if len(cc) == 0 {
		path, err := exec.LookPath("cc")
		if err != nil {
			return nil, fmt.Errorf("there is no C compiler: CC is not set and cc is not on PATH")
		}
		return []string{path}, nil
	}
	path, err := exec.LookPath(cc[0])
	if err != nil {
		return nil, fmt.Errorf("there is no C compiler: CC names %s, which is not on PATH", cc[0])
	}
	return append([]string{path}, cc[1:]...), nil
}

func uninstallNvim(p *plan) error {
	pack, err := nvimPack()
	if err != nil {
		return err
	}
	p.nothing = "Neovim has no Nomi support in " + pack
	if !present(pack) {
		return nil
	}
	// pack/nomi/start/nomi: remove start/ and nomi/ too when nothing else
	// is in them.
	target := pack
	for range 2 {
		parent := filepath.Dir(target)
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
			break
		}
		target = parent
	}
	p.remove(target)
	return nil
}
