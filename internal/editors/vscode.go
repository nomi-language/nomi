package editors

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// vscodeExtension is the extension's identifier, editors/vscode's
// package.json publisher and name.
const vscodeExtension = "nomi-language.nomi"

var errNoVSIX = errors.New("this nomi carries no VS Code extension: only a release build does, since the extension is built with Node.\n" +
	"From a clone of the repository, `make install-vscode` builds and installs it.")

var vscodeEditor = editor{
	name:      "vscode",
	install:   installVSCode,
	uninstall: uninstallVSCode,
	status: func(string) []string {
		if vsix == nil {
			return []string{"this nomi carries no extension; `make install-vscode` in a clone installs it"}
		}
		return []string{"this nomi carries the extension; `code --list-extensions` lists " + vscodeExtension + " once it is installed"}
	},
}

// vscodeCommand is the `code` on PATH.
func vscodeCommand() (string, error) {
	code, err := exec.LookPath("code")
	if err != nil {
		return "", errors.New("code is not on PATH. In VS Code, run \"Shell Command: Install 'code' command in PATH\",\n" +
			"then run this again.")
	}
	return code, nil
}

// VSCodeListed reports whether `code --list-extensions` lists Nomi's
// extension, which only `nomi editor install vscode` installs, since it is
// on no marketplace. The extension records no version of nomi, so whether
// it is this version's is unknown.
func VSCodeListed() bool {
	code, err := exec.LookPath("code")
	if err != nil {
		return false
	}
	out, err := exec.Command(code, "--list-extensions").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Fields(string(out)) {
		if strings.EqualFold(line, vscodeExtension) {
			return true
		}
	}
	return false
}

func installVSCode(p *plan) error {
	if vsix == nil {
		return errNoVSIX
	}
	code, err := vscodeCommand()
	if err != nil {
		return err
	}
	p.runFunc("code --install-extension nomi.vsix --force, nomi.vsix being the extension this nomi carries, in a temporary directory",
		func(out io.Writer) error {
			tmp, err := os.MkdirTemp("", "nomi-vscode-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmp)
			file := filepath.Join(tmp, "nomi.vsix")
			if err := os.WriteFile(file, vsix, 0o644); err != nil {
				return err
			}
			return command(out, nil, code, "--install-extension", file, "--force")
		})
	return nil
}

func uninstallVSCode(p *plan) error {
	code, err := vscodeCommand()
	if err != nil {
		return err
	}
	p.run(nil, code, "--uninstall-extension", vscodeExtension)
	return nil
}
