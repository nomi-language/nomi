package main

import "testing"

// TestRelinkChecksTheImportersOfAOnceWhoseTypeChanged: an unannotated
// `once` whose value takes another type gives the files that import it
// another type, though the file's interface hash, which leaves `once`
// values out, is unchanged. A command after the edit must report the
// importer's type error as a fresh check does, and the commands after the
// edit is undone must answer as fresh ones.
func TestRelinkChecksTheImportersOfAOnceWhoseTypeChanged(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	lib := "pub once size = 3\n"
	p := cacheProject{name: "once-type", entry: "main.nomi", files: map[string]string{
		"nomi.toml": "[module]\nname = \"oncetype\"\nentry_points = [\"main\"]\n",
		"lib.nomi":  lib,
		"main.nomi": "import\n    std::io\n    lib\nend\n\nfn twice(n: Int) -> Int\n    n * 2\nend\n\nfn main()\n    io.print(\"${twice(lib.size)}\")\nend\n",
	}}
	_, relinked := runCacheEdits(t, p, []cacheEdit{
		{what: "the once takes a string", rel: "lib.nomi", text: "pub once size = \"three\"\n"},
		{what: "the once back", rel: "lib.nomi", text: lib},
		{what: "the once takes another int", rel: "lib.nomi", text: "pub once size = 4\n"},
	})
	if relinked == 0 {
		t.Error("no command relinked the program, so the edits that keep the once's type say nothing")
	}
}
