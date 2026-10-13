package gobuildinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestReadFile_EveryRunnerPlatform builds one small program for this
// platform, js/wasm and wasip1/wasm, stripped as scripts/release.sh strips
// them, and reads each one's main package and platform back.
func TestReadFile_EveryRunnerPlatform(t *testing.T) {
	if testing.Short() {
		t.Skip("builds three binaries; -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/tiny\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(\"tiny\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range [][2]string{{runtime.GOOS, runtime.GOARCH}, {"js", "wasm"}, {"wasip1", "wasm"}} {
		t.Run(target[0]+"_"+target[1], func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "tiny")
			cmd := exec.Command(goBin, "build", "-trimpath", "-ldflags", "-s -w", "-o", out, ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOOS="+target[0], "GOARCH="+target[1], "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, b)
			}
			info, err := ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			settings := map[string]string{}
			for _, s := range info.Settings {
				settings[s.Key] = s.Value
			}
			if info.Path != "example.com/tiny" || settings["GOOS"] != target[0] || settings["GOARCH"] != target[1] ||
				settings["-trimpath"] != "true" {
				t.Fatalf("read path %q and settings %v", info.Path, settings)
			}
		})
	}
}

func TestWasm_NotAGoModule(t *testing.T) {
	// A module with only a header and an empty custom section.
	module := append(append([]byte(nil), wasmMagic...), 0, 2, 1, 'x')
	if _, err := Wasm(module); err != errNoBuildInfo {
		t.Fatalf("got %v, want errNoBuildInfo", err)
	}
	if _, err := Wasm([]byte("not wasm")); err == nil {
		t.Fatal("read build info out of something that is not a module")
	}
}
