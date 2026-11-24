package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/vmhost"
)

const toolUsage = `usage: nomi tool <module> <name> [args...]

tool runs the tool <name> of the module <module>: the program
tools/<name>.nomi at the module's root, with the arguments after its name.
<module> is the current module's name or a dependency's name in its nomi.toml,
which the tool finds in NOMI_TOOL_MODULE and reads its table by. A
tool runs in the current directory, with the same access to files, the
network and output that nomi run gives a program. It is how a module ships
a code generator, such as sqlkit's
` + "`nomi tool sqlkit generate`" + `.`

// toolPath is the file of the tool name of the module named module, as
// seen from dir, and the root of dir's module, the project the tool runs
// for.
func toolPath(dir, module, name string) (path, root string, err error) {
	if name == "" || strings.ContainsAny(name, `\`) || strings.Contains(name, "..") || filepath.IsAbs(name) {
		return "", "", fmt.Errorf("%q is not a tool name", name)
	}
	root = moduleRootOf(dir)
	if root == "" {
		return "", "", fmt.Errorf("%s is in no module: there is no nomi.toml above it", dir)
	}
	modRoot, err := analysis.ModuleRoot(root, module)
	if err != nil {
		return "", "", err
	}
	if modRoot == "" {
		return "", "", fmt.Errorf("no module %s: it is neither this module nor a dependency its nomi.toml names; add it with `nomi add <repository>`, or `nomi add <directory>` for one on disk", module)
	}
	path = filepath.Join(modRoot, "tools", filepath.FromSlash(name)+".nomi")
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("module %s has no tool %s: there is no tools/%s.nomi in %s", module, name, name, modRoot)
	}
	return path, root, nil
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
	path, root, err := toolPath(wd, args[0], args[1])
	if err != nil {
		return err
	}
	// A dependency's tool runs in this project's build: its modules are the
	// ones this project's nomi.toml and nomi.lock select, and the nomi.toml
	// at the tool's own root is not read as the main module's.
	if err := os.Setenv(modcache.MainModuleEnv, root); err != nil {
		return err
	}
	// The tool reads its table, and writes its imports, under the name
	// this project gives its module.
	if err := os.Setenv(vmhost.ToolModuleEnv, args[0]); err != nil {
		return err
	}
	runFileVM(path, args[2:]...)
	return nil
}
