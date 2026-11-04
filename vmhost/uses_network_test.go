package vmhost_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// UsesNetwork picks the runner `nomi build` appends a program to: a
// program that reaches none of the network crossings goes on the runner
// built without the network (nomi_nonet), and one that reaches any goes on
// the runner with it. Reaching is what decides: importing std/http and
// never calling it, or calling only std/url, needs no network.
func TestUsesNetwork_PicksTheRunnerByWhatTheProgramReaches(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		exports   bool
		want      bool
	}{
		{"hello world", "import std::io\n\nfn main() {\n    io.print(\"hi\")\n}\n", false, false},
		{"std/url alone", "import std::io\nimport std::url.Url\n\nfn main() {\n    io.inspect(Url.parse(\"http://a/b\"))\n}\n", false, false},
		{"a client request", "import std::io\nimport std::http\nimport std::url.Url\n\nfn main() {\n    io.inspect(http.get(Url`http://a/b`))\n}\n", false, true},
		{"std/http imported, never reached", "import std::io\nimport std::http\nimport std::url.Url\n\nfn unused() {\n    io.inspect(http.get(Url`http://a/b`))\n}\n\nfn main() {\n    io.print(\"hi\")\n}\n", false, false},
		{"a request only an export makes", "import std::http\nimport std::url.Url\n\nexport fn fetch() -> Bool {\n    Result.is_ok(http.get(Url`http://a/b`))\n}\n\nfn main() {\n}\n", true, true},
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
			if got := p.UsesNetwork(tc.exports); got != tc.want {
				t.Fatalf("UsesNetwork(%v) = %v, want %v", tc.exports, got, tc.want)
			}
		})
	}
}
