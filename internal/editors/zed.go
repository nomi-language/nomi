package editors

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nomi-language/nomi/editors/zed"
	grammar "github.com/nomi-language/nomi/tree-sitter-nomi"
)

var errNoZedWasm = errors.New("this nomi carries no Zed extension: only a release build does, since the extension's WebAssembly is built with cargo.\n" +
	"From a clone of the repository, `make install-zed` builds and installs it.")

var zedEditor = editor{
	name:      "zed",
	install:   installZed,
	uninstall: uninstallZed,
	status: func(version string) []string {
		dir, err := zedDir()
		if err != nil {
			return []string{err.Error()}
		}
		switch owner, target := zedOwner(dir); owner {
		case zedLinked:
			return []string{"a dev extension, linked to " + target, dir}
		case zedOther:
			return []string{"installed, not by nomi editor install", dir}
		case zedAbsent:
			if zedWasm == nil {
				return []string{"not installed; this nomi carries no extension, `make install-zed` in a clone installs it", dir}
			}
		}
		return []string{markerStatus(dir, version, "zed"), dir}
	},
}

// zedDir is where Zed keeps an installed extension named nomi: the
// directory a registry install unpacks into, which Zed loads on start.
func zedDir() (string, error) {
	data, err := host.zedData()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "extensions", "installed", "nomi"), nil
}

// zedOwnership is who put what is at Zed's extension directory.
type zedOwnership int

const (
	zedAbsent zedOwnership = iota
	// zedOurs holds the marker an install writes.
	zedOurs
	// zedLinked is a link: Zed's "install dev extension" of a checkout.
	zedLinked
	// zedOther is a directory with no marker: from Zed's registry, or
	// written by hand.
	zedOther
)

// zedOwner is who put dir there, and for a link what it links to.
func zedOwner(dir string) (zedOwnership, string) {
	info, err := os.Lstat(dir)
	switch {
	case err != nil:
		return zedAbsent, ""
	case info.Mode()&fs.ModeSymlink != 0:
		target, _ := os.Readlink(dir)
		return zedLinked, target
	case present(filepath.Join(dir, markerName)):
		return zedOurs, ""
	}
	return zedOther, ""
}

const zedRestart = "Restart Zed to load the change."

func installZed(p *plan) error {
	if zedWasm == nil {
		return errNoZedWasm
	}
	dir, err := zedDir()
	if err != nil {
		return err
	}
	switch owner, target := zedOwner(dir); owner {
	case zedOurs:
		p.remove(dir)
	case zedLinked, zedOther:
		if !p.opts.Force {
			return zedNotOurs(dir, owner, target, "Uninstall it on Zed's extensions page, or run `nomi editor install zed --force` to replace it.")
		}
		p.remove(dir)
	}
	if err := p.writeTree(zed.Files, ".", dir); err != nil {
		return err
	}
	wasm, err := fs.ReadFile(grammar.Files, "tree-sitter-nomi.wasm")
	if err != nil {
		return err
	}
	p.write(filepath.Join(dir, "grammars", "nomi.wasm"), wasm)
	p.write(filepath.Join(dir, "extension.wasm"), zedWasm)
	p.writeMarker(dir)
	p.note = zedRestart
	return nil
}

func uninstallZed(p *plan) error {
	dir, err := zedDir()
	if err != nil {
		return err
	}
	p.nothing = "Zed has no Nomi extension in " + dir
	switch owner, target := zedOwner(dir); owner {
	case zedOurs:
		p.remove(dir)
		p.note = zedRestart
	case zedLinked, zedOther:
		return zedNotOurs(dir, owner, target, "Uninstall it on Zed's extensions page.")
	}
	return nil
}

// zedNotOurs is the error for a Zed extension nomi did not write, which it
// leaves alone, ending with what to do instead.
func zedNotOurs(dir string, owner zedOwnership, target, todo string) error {
	if owner == zedLinked {
		return fmt.Errorf("%s is a dev extension, a link to %s, which nomi leaves alone.\n%s", dir, target, todo)
	}
	return fmt.Errorf("%s holds a Nomi extension nomi did not write, from Zed's registry or by hand, which nomi leaves alone.\n%s", dir, todo)
}
