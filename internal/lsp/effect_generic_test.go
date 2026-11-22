package lsp

import (
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
)

const effectGenericMain = `import std::io

fn shout<T>(x: T) where T: Display
    io.print("${x}!")
end

fn main()
    io.print("hi")
end
`

// A call inside a generic function nothing instantiates gets the same
// hover line and effect modifier as one in a function a program calls:
// the editor's lowering is the whole program's, whose abstract bodies
// read the generic function at its type parameters, and the function's
// name in its declaration is marked too.
func TestSemanticTokens_AnUninstantiatedGenericFunctionIsMarked(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{
		"main.nomi": effectGenericMain,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	s, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectGenericMain})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	waitReachAt(t, s, c, uri, effectGenericMain, "print(\"${x}", "Has effects: `std::io`.")
	toks := semanticTokensOf(t, c, uri)
	if !effectAt(t, toks, effectGenericMain, "io.print(\"${x}", "print") {
		t.Errorf("the call of io.print inside the uninstantiated `shout` carries no effect modifier")
	}
	if !effectAt(t, toks, effectGenericMain, "fn shout", "shout") {
		t.Errorf("the declaration of the uninstantiated `shout` carries no effect modifier")
	}
}
