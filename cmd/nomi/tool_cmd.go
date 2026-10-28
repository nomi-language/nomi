package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
)

const toolUsage = `usage: nomi tool <module> <name> [args...]

tool runs the tool <name> of the module <module>: the program
tools/<name>.nomi at the module's root, with the arguments after its name.
<module> is the current module's name or a module its go.mod requires. A
tool runs with every effect a program has, in the current directory: it is
how a module ships a code generator, such as sqlkit's
` + "`nomi tool sqlkit generate`" + `.`

// toolPath is the file of the tool name of the module named module, as
// seen from dir.
func toolPath(dir, module, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `\`) || strings.Contains(name, "..") || filepath.IsAbs(name) {
		return "", fmt.Errorf("%q is not a tool name", name)
	}
	root := moduleRootOf(dir)
	if root == "" {
		return "", fmt.Errorf("%s is in no module: there is no nomi.toml above it", dir)
	}
	modRoot := ""
	if m, err := analysis.LoadManifest(root); err == nil && m.Name == module {
		modRoot = root
	} else {
		index, err := analysis.BuildModuleIndex(root)
		if err != nil {
			return "", err
		}
		modRoot = index[module]
	}
	if modRoot == "" {
		return "", fmt.Errorf("no module %s: it is neither this module nor one its go.mod requires; add it with `nomi add <module path>`, or `nomi add <directory>` for one on disk", module)
	}
	path := filepath.Join(modRoot, "tools", filepath.FromSlash(name)+".nomi")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("module %s has no tool %s: there is no tools/%s.nomi in %s", module, name, name, modRoot)
	}
	return path, nil
}

// runTool runs `nomi tool`.
func runTool(args []string) error {
	if len(args) < 2 || isFlag(args[0]) || isFlag(args[1]) {
		return errors.New(toolUsage)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	path, err := toolPath(wd, args[0], args[1])
	if err != nil {
		return err
	}
	runFileVM(path, args[2:]...)
	return nil
}
