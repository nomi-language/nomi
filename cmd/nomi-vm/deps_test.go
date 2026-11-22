package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/stdlibbindings/families"
)

// The browser and WASI runners cannot start a process or serve a test
// server, so they import neither: std/process's crossings are stubs there
// (internal/stdprocess/unsupported.go), and std/http's listen and dial
// paths sit behind a constant that is false there (internal/stdhttp's
// hasSockets). A change that imports one of these packages into the VM on
// these targets grows every --web and wasip1 program, and fails here.
//
// go list sees packages, not functions, so this cannot hold net/http's
// server out: it shares a package with the client, which the browser needs.
// The linker drops the server because hasSockets is a constant.
func TestBrowserAndWASIVMsImportNoProcessOrTestServerCode(t *testing.T) {
	forbidden := []string{"os/exec", "net/http/httptest", "net/http/pprof", "net/http/cgi", "net/http/fcgi"}
	for _, goos := range []string{"js", "wasip1"} {
		cmd := exec.Command("go", "list", "-deps", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=wasm")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("GOOS=%s go list -deps: %v\n%s", goos, err, out)
		}
		deps := map[string]bool{}
		for _, line := range strings.Fields(string(out)) {
			deps[line] = true
		}
		if !deps["net/http"] {
			t.Fatalf("GOOS=%s: go list did not report net/http, which std/http's client needs; this test is checking nothing", goos)
		}
		for _, pkg := range forbidden {
			if deps[pkg] {
				t.Errorf("GOOS=%s: the VM imports %s, which a %s program can never use", goos, pkg, goos)
			}
		}
	}
}

// The lean runner, built with every optional family's tag, which `nomi
// build` appends a program that reaches no family's crossing to, links
// none of net/http, crypto/tls, crypto/x509, net or wazero: its families'
// adapters are stubs (internal/stdlibadapters/adapters_no*_gen.go). On
// every target, since the point is the size of each runner. A change that
// imports one of them into the VM another way puts them back in every
// program, and fails here. Without the tags a native runner links wazero;
// a wasip1 one does not, since no wasip1 runner has WebAssembly modules,
// and a js one does not, since std::wasm runs there on the host's own
// engine.
func TestLeanVMImportsNoFamilyCode(t *testing.T) {
	const wazero = "github.com/tetratelabs/wazero"
	deps := func(goos, goarch string, tags ...string) map[string]bool {
		t.Helper()
		args := []string{"list", "-deps"}
		if len(tags) > 0 {
			args = append(args, "-tags", strings.Join(tags, ","))
		}
		cmd := exec.Command("go", append(args, ".")...)
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("GOOS=%s go list -deps: %v\n%s", goos, err, out)
		}
		got := map[string]bool{}
		for _, line := range strings.Fields(string(out)) {
			got[line] = true
		}
		if !got["github.com/nomi-language/nomi/internal/stdlibadapters"] {
			t.Fatalf("GOOS=%s: go list did not report the adapters; this test is checking nothing", goos)
		}
		return got
	}
	lean := families.LeanTags()
	forbidden := []string{"net/http", "crypto/tls", "crypto/x509", "net", wazero}
	for _, target := range [][2]string{{"js", "wasm"}, {"wasip1", "wasm"}, {"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		got := deps(target[0], target[1], lean...)
		for _, pkg := range forbidden {
			if got[pkg] {
				t.Errorf("%s/%s with %s: the VM imports %s", target[0], target[1], strings.Join(lean, ","), pkg)
			}
		}
	}
	if !deps("linux", "amd64")[wazero] {
		t.Error("linux/amd64 without the tags: the VM does not import wazero, so no runner has std::wasm")
	}
	if deps("wasip1", "wasm")[wazero] {
		t.Error("wasip1/wasm without the tags: the VM imports wazero, which no wasip1 runner has")
	}
	if deps("js", "wasm")[wazero] {
		t.Error("js/wasm without the tags: the VM imports wazero, where std::wasm runs on the host's engine (internal/stdwasm's engine_js.go)")
	}
}
