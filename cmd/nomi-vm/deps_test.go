package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
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

// The runner built with the nomi_nonet tag, which `nomi build` appends a
// program that reaches no network crossing to, links none of net/http,
// crypto/tls, crypto/x509 or net: its network adapters are stubs
// (internal/stdlibadapters/adapters_nonet_gen.go). On every target, since
// the point is the size of each runner. A change that imports one of them
// into the VM another way puts them back in every program, and fails here.
func TestNoNetworkVMImportsNoNetworkCode(t *testing.T) {
	forbidden := []string{"net/http", "crypto/tls", "crypto/x509", "net"}
	for _, target := range [][2]string{{"js", "wasm"}, {"wasip1", "wasm"}, {"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		cmd := exec.Command("go", "list", "-tags", "nomi_nonet", "-deps", ".")
		cmd.Env = append(os.Environ(), "GOOS="+target[0], "GOARCH="+target[1])
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("GOOS=%s go list -deps: %v\n%s", target[0], err, out)
		}
		deps := map[string]bool{}
		for _, line := range strings.Fields(string(out)) {
			deps[line] = true
		}
		if !deps["github.com/nomi-language/nomi/internal/stdlibadapters"] {
			t.Fatalf("GOOS=%s: go list did not report the adapters; this test is checking nothing", target[0])
		}
		for _, pkg := range forbidden {
			if deps[pkg] {
				t.Errorf("%s/%s with nomi_nonet: the VM imports %s", target[0], target[1], pkg)
			}
		}
	}
}
