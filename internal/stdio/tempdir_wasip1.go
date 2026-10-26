//go:build wasip1

package stdio

import "os"

// A WASI module reaches only the directories its runtime preopens, and Go's
// os.TempDir answers `/tmp` whether or not one is there. So the temporary
// directory is the first of TMPDIR, `/tmp` and the working directory that
// is a directory the module can reach, and with none, temporary
// directories are Unsupported (noTempDir).

// osTempDir is where temporary directories are made, and whether the module
// can reach it.
func osTempDir() (string, bool) {
	var candidates []string
	if dir := os.Getenv("TMPDIR"); dir != "" {
		candidates = append(candidates, dir)
	}
	candidates = append(candidates, "/tmp")
	if wd, err := os.Getwd(); err == nil && wd != "" {
		candidates = append(candidates, wd)
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
	}
	return os.TempDir(), false
}
