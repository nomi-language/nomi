//go:build !(js && wasm)

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/nodeenv"
)

// The language tour runs visitors' programs on this binary, so the effects a
// program can reach here are the tour's attack surface. The one that would
// reach outside the worker is replaced in the browser build:
//
//   - io.read_file / io.write_file (nomi/stdio file_browser.go): refused by the
//     build rather than by wasm_exec.js's fs shim happening to answer ENOSYS.
//
// This test holds the replacement in place from an ordinary host `go test`.

// TestBrowserBuild_EffectsRefuse runs the browser halves' own tests under
// js/wasm when Node is available, so their behaviour is checked by the
// ordinary `go test ./...` and not only by someone remembering the GOOS=js
// invocation.
func TestBrowserBuild_EffectsRefuse(t *testing.T) {
	if testing.Short() {
		t.Skip("builds js/wasm test binaries")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH; a js/wasm test binary needs it")
	}
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	wasmDir := filepath.Join(strings.TrimSpace(string(root)), "lib", "wasm")
	cmd := exec.Command("go", "test", "-count=1", "-v", "-run", "TestBrowser_", "github.com/nomi-language/nomi/internal/stdio")
	// go_js_wasm_exec hands Node's environment to the test binary, so the
	// command gets only what go and Node need, not the caller's whole one.
	var goVars []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GO") || strings.HasPrefix(name, "XDG_") {
			goVars = append(goVars, name)
		}
	}
	cmd.Env = nodeenv.Minimal(append(os.Environ(), "GOOS=js", "GOARCH=wasm",
		"PATH="+wasmDir+string(os.PathListSeparator)+os.Getenv("PATH")), append(goVars, "GOOS", "GOARCH")...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("js/wasm go test: %v\n%s", err, out)
	}
	for _, name := range []string{
		"TestBrowser_FileAccessRefuses",
	} {
		if !strings.Contains(string(out), "--- PASS: "+name) {
			t.Errorf("js/wasm run did not pass %s:\n%s", name, out)
		}
	}
}
