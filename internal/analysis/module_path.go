package analysis

import "path/filepath"

// ResolveModulePath converts a module import path to an absolute file path.
// For example, ["math"] with root "/project" becomes "/project/math.nomi".
func ResolveModulePath(projectRoot string, modulePath []string) string {
	return filepath.Join(append([]string{projectRoot}, modulePath...)...) + ".nomi"
}
