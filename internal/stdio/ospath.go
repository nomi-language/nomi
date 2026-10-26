package stdio

import (
	"path/filepath"
	"strings"
)

// How a Nomi path meets the operating system's. Everything about the
// separator is in this file, so changing the rule is a change here.
//
// A Nomi path uses `/` on every system (std/path's rule). On Windows a path
// the program passes may use `\` too, and toOS hands Go the system's form;
// every path the backend answers (a temporary directory, the current
// directory) comes back through fromOS with `/`. A drive is `C:/Users`. On
// every other system both are the identity, since `/` is the separator and
// `\` is an ordinary character in a name.

// toOS is path as the operating system takes it.
func toOS(path string) string { return filepath.FromSlash(path) }

// fromOS is a path the operating system answered, as a Nomi path.
func fromOS(path string) string { return filepath.ToSlash(path) }

// fromOSName is a directory entry's name as a Nomi path names it.
func fromOSName(name string) string { return name }

// parentOf is the directory a Nomi path is in, or "" for a path with no
// directory part.
func parentOf(path string) string {
	path = strings.TrimRight(fromOS(toOS(path)), "/")
	i := strings.LastIndexByte(path, '/')
	switch {
	case i < 0:
		return ""
	case i == 0:
		return "/"
	}
	return path[:i]
}
