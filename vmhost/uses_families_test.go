package vmhost_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// UsesFamilies picks the runner `nomi build` appends a program to: a
// program that reaches no optional family's crossing goes on the lean
// runner (nomi_nonet and nomi_nowasm), and one that reaches any goes on
// the runner with them. Reaching is what decides: importing std/http and
// never calling it, or calling only std/url, needs no network, and
// compiling a module needs WebAssembly as much as running one does.
func TestUsesFamilies_PicksTheRunnerByWhatTheProgramReaches(t *testing.T) {
	const adder = `"0061736d0100000001070160027f7f017f030201000503010001071002036164640000066d656d6f727902000a09010700200020016a0b"`
	for _, tc := range []struct {
		name, src string
		exports   bool
		want      []string
	}{
		{"hello world", "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n", false, nil},
		{"std/url alone", "import std::io\nimport std::url.Url\n\nfn main()\n    io.inspect(Url.parse(\"http://a/b\"))\nend\n", false, nil},
		{"a client request", "import std::io\nimport std::http\nimport std::url.Url\n\nfn main()\n    io.inspect(http.get(Url\"http://a/b\"))\nend\n", false, []string{"net"}},
		{"std/http imported, never reached", "import std::io\nimport std::http\nimport std::url.Url\n\nfn unused()\n    io.inspect(http.get(Url\"http://a/b\"))\nend\n\nfn main()\n    io.print(\"hi\")\nend\n", false, nil},
		{"a request only an export makes", "import std::http\nimport std::url.Url\n\nexport fn fetch() -> Bool\n    Result.is_ok(http.get(Url\"http://a/b\"))\nend\n\nfn main() end\n", true, []string{"net"}},
		{"a module compiled", "import std::hex\nimport std::io\nimport std::wasm\n\nfn main()\n    io.print(\"${Result.is_ok(wasm.compile(hex.decode(" + adder + ")!))}\")\nend\n", false, []string{"wasm"}},
		{"std/wasm imported, never reached", "import std::io\nimport std::wasm\n\nfn unused(m: wasm.Module) -> Bool\n    Result.is_ok(wasm.instantiate(m))\nend\n\nfn main()\n    io.print(\"hi\")\nend\n", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.nomi")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := vmhost.Load(path)
			if err != nil {
				t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
			}
			if got := p.UsesFamilies(tc.exports); !slices.Equal(got, tc.want) {
				t.Fatalf("UsesFamilies(%v) = %v, want %v", tc.exports, got, tc.want)
			}
		})
	}
}
