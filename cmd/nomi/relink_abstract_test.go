package main

import (
	"strings"
	"testing"
)

// TestRelinkAfterABodyEditOfAnUninstantiatedGeneric: lib.nomi declares a
// public generic function nothing instantiates, which [effects.self] holds
// through its abstract body, and main.nomi uses another function of the
// file. An edit that makes the generic function's body read a file is
// refused by a fresh `nomi check`, and a check with the artifact cache,
// which a relink from file records would answer with no abstract body,
// prints the same refusal; undoing the edit passes again.
func TestRelinkAfterABodyEditOfAnUninstantiatedGeneric(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	lib := `pub fn greeting() -> String
    "hi"
end

pub fn tell<T>(x: T) -> String
where T: Display
    "<${x}>"
end
`
	reads := "import std::fs\n\n" + strings.Replace(lib, `    "<${x}>"`, "    _ = fs.read_text(\"notes.txt\")\n    \"<${x}>\"", 1)
	p := cacheProject{name: "generic-effects", entry: "main.nomi", files: map[string]string{
		"nomi.toml": "[package]\nname = \"genericeffects\"\nentry_points = [\"main\"]\n\n[effects.self]\neffects = [\"std::io\"]\n",
		"lib.nomi":  lib,
		"main.nomi": `import
    std::io
    lib.greeting
end

fn main()
    io.print(greeting())
end
`,
	}}
	edits := []cacheEdit{
		{what: "the generic function's body reads a file", rel: "lib.nomi", text: reads},
		{what: "the original back", rel: "lib.nomi", text: lib},
	}
	runCacheEditsBy(t, p, edits)
}
