//go:build !js && !wasip1

package stdsystem

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/nomi-language/nomi/rt"
)

// Hostname is std/system/host's `hostname`: the name the kernel reports
// for the machine.
func Hostname() rt.Result[string, *rt.List[string]] {
	name, err := os.Hostname()
	return text("system.hostname", name, err)
}

// Executable is std/system/host's `executable`: the path of the program
// running, as the operating system reports it. A program `nomi run` runs
// is `nomi`'s own path.
func Executable() rt.Result[string, *rt.List[string]] {
	path, err := os.Executable()
	return text("system.executable", path, err)
}

// HomeDir is std/system/host's `home_dir`: Go's os.UserHomeDir, which
// reads $HOME (%USERPROFILE% on Windows) from the process's environment.
func HomeDir() rt.Result[string, *rt.List[string]] {
	dir, err := os.UserHomeDir()
	return text("system.home_dir", dir, err)
}

// CacheDir is std/system/host's `cache_dir`: Go's os.UserCacheDir,
// $XDG_CACHE_HOME or ~/.cache on Linux, ~/Library/Caches on macOS,
// %LocalAppData% on Windows.
func CacheDir() rt.Result[string, *rt.List[string]] {
	dir, err := os.UserCacheDir()
	return text("system.cache_dir", dir, err)
}

// ConfigDir is std/system/host's `config_dir`: Go's os.UserConfigDir,
// $XDG_CONFIG_HOME or ~/.config on Linux, ~/Library/Application Support
// on macOS, %AppData% on Windows.
func ConfigDir() rt.Result[string, *rt.List[string]] {
	dir, err := os.UserConfigDir()
	return text("system.config_dir", dir, err)
}

// DataDir is std/system/host's `data_dir`, by the rules Go's
// os.UserCacheDir and UserConfigDir follow, which Go has no function for:
// $XDG_DATA_HOME or ~/.local/share on Linux and the other Unix systems,
// ~/Library/Application Support on macOS, %LocalAppData% on Windows.
func DataDir() rt.Result[string, *rt.List[string]] {
	dir, err := dataDir()
	return text("system.data_dir", dir, err)
}

func dataDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		dir := os.Getenv("LocalAppData")
		if dir == "" {
			return "", errors.New("%LocalAppData% is not defined")
		}
		return dir, nil
	case "darwin", "ios":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case "plan9":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "lib"), nil
	}
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", errors.New("neither $XDG_DATA_HOME nor $HOME are defined")
		}
		return filepath.Join(home, ".local", "share"), nil
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("path in $XDG_DATA_HOME is relative")
	}
	return dir, nil
}

// User is std/system/host's `user`: the name and the id of the user the
// process runs as, from Go's os/user. On Unix the id is the numeric uid;
// on Windows the name is `DOMAIN\name` and the id the account's security
// identifier (`S-1-5-21-...`).
func User() rt.Result[*rt.List[string], *rt.List[string]] {
	u, err := user.Current()
	if err != nil {
		return rt.Err[*rt.List[string]](unavailable("system.user", err))
	}
	return rt.Ok[*rt.List[string], *rt.List[string]](rt.Cons(u.Username, rt.Cons(u.Uid, nil)))
}
