package analysis

import "path/filepath"

// ResolveModulePath converts a module import path to an absolute file path.
// For example, ["math"] with root "/project" becomes "/project/math.nomi".
func ResolveModulePath(projectRoot string, modulePath []string) string {
	return filepath.Join(append([]string{projectRoot}, modulePath...)...) + ".nomi"
}

// StdFilesDir is the directory the stdlib's files are named under in a
// process that finds the stdlib in root (StdlibPath, "" when it finds
// none): root itself, or with none (the browser, a release installed
// without the stdlib beside it) the relative directory `std`, so that
// std/channels is `std/channels.nomi` there. Every std file's path comes
// from it: a build's (importDest.file), and the stdlib's state and facts
// read back (std.stdBuildDir), so a module built over the state is named
// beside the prelude the state holds.
func StdFilesDir(root string) string {
	if root == "" {
		return "std"
	}
	return root
}
