package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/vmhost"
)

const toolUsage = `usage: nomi tool <package> <name> [args...]

tool runs the tool <name> of the package <package>: the program
tools/<name>.nomi at the package's root, with the arguments after its name.
<package> is the current package's name or a dependency's name in its nomi.toml,
which the tool finds in NOMI_TOOL_PACKAGE and reads its table by. A
tool runs in the current directory, with the same access to files, the
network and output that nomi run gives a program. It is how a package ships
a code generator, such as sqlkit's
` + "`nomi tool sqlkit generate`" + `.`

// toolPath is the file of the tool name of the package named module, as
// seen from dir, and the root of dir's package, the project the tool runs
// for.
func toolPath(dir, module, name string) (path, root string, err error) {
	if name == "" || strings.ContainsAny(name, `\`) || strings.Contains(name, "..") || filepath.IsAbs(name) {
		return "", "", fmt.Errorf("%q is not a tool name", name)
	}
	root = packageRootOf(dir)
	if root == "" {
		return "", "", fmt.Errorf("%s is in no package: there is no nomi.toml above it", dir)
	}
	modRoot, err := analysis.PackageRoot(root, module)
	if err != nil {
		return "", "", err
	}
	if modRoot == "" {
		return "", "", fmt.Errorf("no package %s: it is neither this package nor a dependency its nomi.toml names; add it with `nomi add <repository>`, or `nomi add <directory>` for one on disk", module)
	}
	path = filepath.Join(modRoot, "tools", filepath.FromSlash(name)+".nomi")
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("package %s has no tool %s: there is no tools/%s.nomi in %s", module, name, name, modRoot)
	}
	return path, root, nil
}

// runTool runs `nomi tool`.
func runTool(args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	// A tool runs for the package it runs in, and a workspace's root is none.
	if err := workspaceRootError("tool", wd, "run nomi tool in the member it runs for"); err != nil {
		return err
	}
	if len(args) < 2 || isFlag(args[0]) || isFlag(args[1]) {
		return errors.New(toolUsage)
	}
	path, root, err := toolPath(wd, args[0], args[1])
	if err != nil {
		return err
	}
	// A dependency's tool runs in this project's build: its packages are the
	// ones this project's nomi.toml and nomi.lock select, and the nomi.toml
	// at the tool's own root is not read as the main package's.
	if err := os.Setenv(pkgcache.MainPackageEnv, root); err != nil {
		return err
	}
	// The tool reads its table, and writes its imports, under the name
	// this project gives its package.
	if err := os.Setenv(vmhost.ToolPackageEnv, args[0]); err != nil {
		return err
	}
	runFileVM(path, args[2:]...)
	return nil
}
