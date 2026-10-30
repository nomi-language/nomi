package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// A project's own file named like a Go-backed std module, binding a host
// function std's module also declares, registers under the key std's row
// has (`hash.sha256`, `regex.compile`). The program uses both, and both run:
// a project's crossings are named apart from std's (hostadapt.ProjectName),
// so the machine binds the project's table beside std's and neither answers
// the other's calls.
func TestHosts_AProjectFileNamedLikeAStdModuleRunsBesideIt(t *testing.T) {
	const main = `import {
    std/hash as digest
    std/io
    std/regex.Regex
    hash
    regex
}

fn main() {
    io.print(Bytes.to_string(hash.sha256(String.to_bytes("x"))) == Ok("the project's"))
    io.print(digest.sha256_hex(String.to_bytes("abc")))
    io.print(regex.compile("a+"))
    io.print(Result.is_ok(Regex.compile("a+")))
}
`
	files := map[string]string{
		"hash":  "pub host fn sha256(data: Bytes) -> Bytes\n",
		"regex": "pub host fn compile(pattern: String) -> String\n",
	}
	table := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{
			"hash.sha256": func(_ *rt.Frame, _ []rt.Value) (rt.Value, error) {
				return rt.Bytes("the project's"), nil
			},
			"regex.compile": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				return "compiled " + args[0].(string), nil
			},
		}, nil
	}
	var out bytes.Buffer
	p, err := vmhost.LoadSource("main", main, vmhost.WithVirtualFiles(files), vmhost.WithHosts(table), vmhost.WithOutput(&out))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	want := "True\nba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\ncompiled a+\nTrue\n"
	if out.String() != want {
		t.Errorf("printed:\n%s\nwant:\n%s", out.String(), want)
	}
}
