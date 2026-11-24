package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// systemProgram prints what std::system answers, a line each.
const systemProgram = `import
    std::io
    std::system
end

fn main()
    io.print(system.home_dir() |> Result.with_default("none"))
    io.print(system.cache_dir() |> Result.with_default("none"))
    io.print(system.config_dir() |> Result.with_default("none"))
    io.print(system.data_dir() |> Result.with_default("none"))
    io.print(system.hostname() |> Result.with_default("none"))
    io.print(system.cpu_count())
    io.print(system.executable() |> Result.with_default("none"))
    io.print("${system.os()}/${system.arch()}")
end
`

// std::system's directories are read from the environment the program
// runs with, which a parent sets for it: a nomi started with HOME and the
// XDG variables (USERPROFILE, AppData and LocalAppData on Windows) moved
// answers the moved directories. Its hostname and CPU count are Go's own,
// and its executable is the nomi running it.
func TestSystem_DirectoriesFollowTheEnvironmentTheProgramRunsWith(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(program, []byte(systemProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	at := func(name string) string { return filepath.Join(dir, name) }
	cmd := exec.Command(nomiBin, "run", program)
	cmd.Env = append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home,
		"XDG_CACHE_HOME="+at("cache"), "XDG_CONFIG_HOME="+at("config"), "XDG_DATA_HOME="+at("data"),
		"LocalAppData="+at("local"), "AppData="+at("roaming"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nomi run: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(lines) != 8 {
		t.Fatalf("printed %d lines, want 8:\n%s", len(lines), out)
	}

	var cache, config, data string
	switch runtime.GOOS {
	case "darwin":
		support := filepath.Join(home, "Library", "Application Support")
		cache, config, data = filepath.Join(home, "Library", "Caches"), support, support
	case "windows":
		cache, config, data = at("local"), at("roaming"), at("local")
	default:
		cache, config, data = at("cache"), at("config"), at("data")
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "none"
	}
	want := []string{home, cache, config, data, hostname, strconv.Itoa(runtime.NumCPU()), "", runtime.GOOS + "/" + runtime.GOARCH}
	names := []string{"home_dir", "cache_dir", "config_dir", "data_dir", "hostname", "cpu_count", "executable", "os/arch"}
	for i, got := range lines {
		if names[i] == "executable" {
			if !sameFile(t, got, nomiBin) {
				t.Errorf("executable is %q, want the nomi that ran it, %q", got, nomiBin)
			}
			continue
		}
		if got != want[i] {
			t.Errorf("%s is %q, want %q", names[i], got, want[i])
		}
	}
}

// sameFile reports whether the paths a and b name one file.
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(ai, bi)
}
