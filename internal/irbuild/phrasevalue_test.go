package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A program that declares no `phrase` names `Phrase` only in a parameter's
// type, and takes the function as a value. Before, this reported "using
// `show` here is not supported yet".
func TestPhraseParameter_FunctionValue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std::io

fn show(_p: Phrase) -> Int
    1
end

fn main()
    _ = show
    io.print("ok")
end
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	p.Declines.Observed = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				verifyLambdaProgram(t, src, "ok\n")
				return
			}
		}
	}
	t.Fatalf("main was not retained (declines: %v)", declined)
}
