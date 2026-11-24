//go:build js || wasip1

package stdsystem

import "github.com/nomi-language/nomi/rt"

// A js/wasm or wasip1 module has no hostname, no executable path, no home
// or user directories and no user it runs as. Each answers Unsupported
// when it is called, so a program that reads one still builds for these
// targets.

// Hostname is std/system/host's `hostname`: Unsupported here.
func Hostname() rt.Result[string, *rt.List[string]] {
	return rt.Err[string](unsupported("system.hostname"))
}

// Executable is std/system/host's `executable`: Unsupported here.
func Executable() rt.Result[string, *rt.List[string]] {
	return rt.Err[string](unsupported("system.executable"))
}

// HomeDir is std/system/host's `home_dir`: Unsupported here.
func HomeDir() rt.Result[string, *rt.List[string]] {
	return rt.Err[string](unsupported("system.home_dir"))
}

// CacheDir is std/system/host's `cache_dir`: Unsupported here.
func CacheDir() rt.Result[string, *rt.List[string]] {
	return rt.Err[string](unsupported("system.cache_dir"))
}

// ConfigDir is std/system/host's `config_dir`: Unsupported here.
func ConfigDir() rt.Result[string, *rt.List[string]] {
	return rt.Err[string](unsupported("system.config_dir"))
}

// DataDir is std/system/host's `data_dir`: Unsupported here.
func DataDir() rt.Result[string, *rt.List[string]] {
	return rt.Err[string](unsupported("system.data_dir"))
}

// User is std/system/host's `user`: Unsupported here.
func User() rt.Result[*rt.List[string], *rt.List[string]] {
	return rt.Err[*rt.List[string]](unsupported("system.user"))
}
