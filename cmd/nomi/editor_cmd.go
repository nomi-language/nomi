package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/editors"
)

const editorUsage = `usage: nomi editor [install [--force] [--installed] | uninstall] [--dry-run] [<editor>]

With no words, nomi editor lists each editor, whether its Nomi support is
installed and from which version, and where it goes. install writes an
editor's support from the files this nomi carries, so they match this
version; uninstall removes it. --dry-run prints every path the command
would write or remove and every command it would run, and changes nothing.
install --installed takes no editor: it installs for each editor that has
Nomi support already, VS Code when code --list-extensions lists the
extension.

  helix    the queries, the grammar and the Nomi block of languages.toml in
           Helix's configuration directory (~/.config/helix, or
           %AppData%\helix); builds the grammar with hx
  nvim     the plugin in Neovim's site/pack/nomi/start/nomi
           (~/.local/share/nvim, or %LocalAppData%\nvim-data); compiles the
           parser with $CC or cc
  vscode   the extension, with code --install-extension; a release nomi
           carries it, and make install-vscode builds it in a clone
  zed      the extension in Zed's extensions/installed/nomi (~/Library/
           Application Support/Zed, ~/.local/share/zed, or %LocalAppData%\Zed);
           a release nomi carries it, and make install-zed builds it in a
           clone. --force replaces an extension nomi did not write, such as a
           dev extension`

// runEditor is `nomi editor`.
func runEditor(args []string) error {
	opts := editors.Options{Out: os.Stdout, Version: buildversion.Name()}
	if len(args) == 0 {
		editors.Status(opts)
		return nil
	}
	action, words := args[0], args[1:]
	if action != "install" && action != "uninstall" {
		return errors.New(editorUsage)
	}
	var names []string
	installed := false
	for _, w := range words {
		switch {
		case w == "--dry-run":
			opts.DryRun = true
		case w == "--force" && action == "install":
			opts.Force = true
		case w == "--installed" && action == "install":
			installed = true
		case isFlag(w):
			return unknownFlag("nomi editor "+action, w, editorUsage)
		default:
			names = append(names, w)
		}
	}
	if installed {
		if len(names) > 0 {
			return fmt.Errorf("nomi editor install: --installed installs for each editor that has Nomi support, and takes no editor (%s)\n%s",
				strings.Join(names, " "), editorUsage)
		}
		return editors.Reinstall(opts)
	}
	if len(names) != 1 {
		return errors.New(editorUsage)
	}
	if action == "install" {
		return editors.Install(names[0], opts)
	}
	return editors.Uninstall(names[0], opts)
}
