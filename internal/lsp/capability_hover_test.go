package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// reachLines are the lines of a hover that say what a call reaches
// outside the machine (loweringChecks.reachedIn, hoverdoc.AppendReach).
func reachLines(hover string) []string {
	var out []string
	for _, line := range strings.Split(hover, "\n") {
		if strings.HasPrefix(line, "Reaches `") || strings.HasPrefix(line, "Binds Go: `") {
			out = append(out, line)
		}
	}
	return out
}

// waitReachAt waits until uri, whose text is text, has a finished lowering
// run of that text, then until the hover at needle's first character says
// what want says of what the call reaches (no line for an empty want).
func waitReachAt(t *testing.T, s *Server, c *rpcClient, uri, text, needle string, want ...string) {
	t.Helper()
	at := positionOf(t, text, needle, false)
	deadline := time.Now().Add(60 * time.Second)
	for {
		s.lowering.mu.Lock()
		run, ran := s.lowering.reached[uri]
		s.lowering.mu.Unlock()
		if ran && run.content == text {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no lowering of the text at %q finished", needle)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var got []string
	for {
		var hover protocol.Hover
		if err := c.conn.Call(context.Background(), "textDocument/hover", map[string]any{
			"textDocument": map[string]any{"uri": uri}, "position": at,
		}, &hover); err != nil {
			t.Fatal(err)
		}
		var markup protocol.MarkupContent
		raw, _ := json.Marshal(hover.Contents)
		_ = json.Unmarshal(raw, &markup)
		got = reachLines(markup.Value)
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			s.lowering.mu.Lock()
			run := s.lowering.reached[uri]
			s.lowering.mu.Unlock()
			t.Fatalf("hover at %q (%+v) says %q of what it reaches; want %q; the run found %+v", needle, at, got, want, run.calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startCapabilityServer serves dir with a short lowering delay and opens
// each of files, by its path under dir.
func startCapabilityServer(t *testing.T, dir string, files map[string]string) (*Server, *rpcClient) {
	t.Helper()
	s := NewServer()
	s.lowering.delay = 50 * time.Millisecond
	c := startRPC(t, s, dir)
	for name, text := range files {
		c.open(fileuri.FromPath(filepath.Join(dir, name)), text)
	}
	return s, c
}

// Hovering a call that reaches outside the machine adds one line: what it
// reaches, and whether nomi.toml's tables allow it, naming the table that
// does or does not, or saying that no table holds it. The line is
// on the callee's owner and its name alike; a call that reaches nothing
// outside has none.
func TestCapabilityHover_StdCall(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const module = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"
	main := strings.Replace(capabilityMain, "fn main()\n", "fn twice(n: Int) -> Int\n    n * 2\nend\n\nfn main()\n    io.print(\"${twice(2)}\")\n", 1)
	cases := []struct {
		name, manifest, want string
	}{
		{"no table", module, "Reaches `std::fs`."},
		{"allowed", module + "\n[capabilities.self]\nstd = [\"std::io\", \"std::fs\"]\n", "Reaches `std::fs`, which `nomi.toml` allows in `[capabilities.self]`."},
		{"held by no table", module + "\n[capabilities.deps]\nstd = []\n", "Reaches `std::fs`, which no table in `nomi.toml` holds."},
		{"refused", module + "\n[capabilities.self]\nstd = [\"std::io\"]\n", "Reaches `std::fs`, which `nomi.toml` does not allow in `[capabilities.self]`."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, map[string]string{
				"main.nomi": main, "nomi.toml": tc.manifest,
				"go.mod": "module example.com/app\n\ngo 1.27.0\n",
			})
			s, c := startCapabilityServer(t, dir, map[string]string{"main.nomi": main})
			uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
			waitReachAt(t, s, c, uri, main, "fs.read_text", tc.want)
			waitReachAt(t, s, c, uri, main, "read_text", tc.want)
			waitReachAt(t, s, c, uri, main, "twice(2)")
		})
	}
}

// A `go` binding binds Go, which the line names by the package. The walk
// finds the crossing at the binding's declaration, where a refusal points
// too, so the line is on the declared name.
func TestCapabilityHover_GoBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const goMain = "import std::io\n\ngopkg \"strings\" as gostrings\n\nfn upper(s: String) -> String go gostrings.ToUpper\n\nfn main()\n    io.print(upper(\"hi\"))\nend\n"
	const module = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"
	for _, tc := range []struct{ name, manifest, want string }{
		{"no table", module, "Binds Go: `go:strings`."},
		{"allowed", module + "\n[capabilities.self]\nstd = [\"std::io\"]\ngo = true\n", "Binds Go: `go:strings`, which `nomi.toml` allows in `[capabilities.self]`."},
		{"refused", module + "\n[capabilities.self]\nstd = [\"std::io\"]\n", "Binds Go: `go:strings`, which `nomi.toml` does not allow in `[capabilities.self]`."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, map[string]string{
				"main.nomi": goMain, "nomi.toml": tc.manifest,
				"go.mod": "module example.com/app\n\ngo 1.27.0\n",
			})
			s, c := startCapabilityServer(t, dir, map[string]string{"main.nomi": goMain})
			uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
			waitReachAt(t, s, c, uri, goMain, "upper(s", tc.want)
			waitReachAt(t, s, c, uri, goMain, "pper(s", tc.want)
			waitReachAt(t, s, c, uri, goMain, "gostrings.ToUpper")
		})
	}
}

// A call in a dependency's file, opened beside the entry that reaches it,
// has the line the entry's lowering found, naming [capabilities.deps] and
// the project's entry for the dependency, and so do the dependency's
// function that makes it and the entry's call of that function. The
// dependency's file has no roots of its own.
func TestCapabilityHover_InADependencysFile(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	main := "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n"
	rules := "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n"
	dir := writeProject(t, map[string]string{
		"app/go.mod": "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.deps]\nstd = [\"std::io\", \"std::fs\"]\n\n" +
			"[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n",
		"app/main.nomi":      main,
		"pricing/go.mod":     "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": rules,
	})
	s, c := startCapabilityServer(t, filepath.Join(dir, "app"), map[string]string{})
	mainURI := fileuri.FromPath(filepath.Join(dir, "app", "main.nomi"))
	rulesURI := fileuri.FromPath(filepath.Join(dir, "pricing", "rules.nomi"))
	c.open(mainURI, main)
	c.open(rulesURI, rules)
	const line = "Reaches `std::fs`, which `nomi.toml` allows in `[capabilities.deps]` and in the entry for `example.com/pricing`."
	waitReachAt(t, s, c, rulesURI, rules, "fs.read_text", line)
	// The dependency's function, and the entry's call of it, say what the
	// function can reach.
	waitReachAt(t, s, c, rulesURI, rules, "load(path", line)
	waitReachAt(t, s, c, mainURI, main, "rules.load", line)
	waitReachAt(t, s, c, mainURI, main, "\"prices.txt\"")
}

// The line follows the latest lowering: an edit that removes the call
// removes it, and one that brings the call back brings it back.
func TestCapabilityHover_FollowsEdits(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	without := "import std::io\n\nfn main()\n    io.print(\"notes.txt\")\nend\n"
	dir := writeProject(t, map[string]string{"main.nomi": capabilityMain, "nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	s, c := startCapabilityServer(t, dir, map[string]string{"main.nomi": capabilityMain})
	waitReachAt(t, s, c, uri, capabilityMain, "fs.read_text", "Reaches `std::fs`.")
	c.change(uri, 2, without)
	waitReachAt(t, s, c, uri, without, "io.print", "Reaches `std::io`.")
	waitReachAt(t, s, c, uri, without, "\"notes.txt\"")
	c.change(uri, 3, capabilityMain)
	waitReachAt(t, s, c, uri, capabilityMain, "fs.read_text", "Reaches `std::fs`.")
}

// In a program opened from its record, an edit is lowered from the
// record's kept lowering (vmhost's checklowering_stored.go), and that
// lowering finds the outside calls at their callees, and what main can
// reach at its name, as a whole lowering does; a second run of the same
// text answers them from the lowering it kept.
func TestCapabilityHover_AProgramOpenedFromItsRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	dir := writeProject(t, storedProjectFiles)
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	text := storedProjectFiles["main.nomi"]
	writer := NewServer()
	writer.docs.SetWorkspaceRoot(dir)
	writer.docs.Open(uri, text)
	if diags := lowerAsARun(writer, uri, text); len(diags) != 0 {
		t.Fatalf("the entry's lowering reports %v", diags)
	}
	writer.docs.NoteLoweredClean(uri, text, analyzedlowering.Kept)

	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.Open(uri, text)
	if opened, _ := s.docs.StoredPrograms(); opened != 1 {
		t.Fatalf("the server opened %d programs from records, want 1", opened)
	}
	edited := strings.Replace(text, "Circle(2)", "Circle(3)", 1)
	s.docs.Update(uri, edited)
	at := positionOf(t, edited, "io.print", false)
	mainAt := positionOf(t, edited, "main()", false)
	want := []analyzedlowering.Reached{{Path: fileuri.ToPath(uri), Line: int(at.Line) + 1, Col: int(at.Character) + 1,
		EndLine: int(at.Line) + 1, EndCol: int(at.Character) + 1 + len("io.print"), Text: "Reaches `std::io`."},
		{Path: fileuri.ToPath(uri), Line: int(mainAt.Line) + 1, Col: int(mainAt.Character) + 1,
			EndLine: int(mainAt.Line) + 1, EndCol: int(mainAt.Character) + 1 + len("main"), Text: "Reaches `std::io`."}}
	for _, run := range []string{"the edit", "the edit again"} {
		was := vmhost.StoredRelowersForTest()
		stored := s.docs.StoredLowering(uri, edited)
		if stored == nil {
			t.Fatalf("%s: the entry has no stored lowering", run)
		}
		found := func() loweringFound {
			defer s.docs.View()()
			return s.lowerDocument(context.Background(), uri, edited, &analyzedlowering.Analyzed{Stored: stored})
		}()
		if run == "the edit" && vmhost.StoredRelowersForTest() == was {
			t.Fatalf("%s was not lowered from the record's kept lowering", run)
		}
		if !slices.Equal(found.reached, want) {
			t.Errorf("after %s the lowering reaches %+v; want %+v", run, found.reached, want)
		}
	}
}

// A program opened from its record says what its calls reach as one
// checked whole does, in every file: once it opens, before any edit, the
// entry's lowering links the record's kept lowering (vmhost's
// checklowering_stored.go), and a call in another file than the entry has
// the line at its callee, before and after an edit of the entry, as do
// each function's name and its calls.
func TestCapabilityHover_EveryFileOfAProgramOpenedFromItsRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	main := "import\n    std::io\n    files\nend\n\nfn main()\n    io.print(files.load(\"notes.txt\"))\nend\n"
	files := "import std::fs\n\n/// The text at path, or \"\" when it cannot be read.\npub fn load(path: String) -> String\n    read(path) |> String.trim()\nend\n\nfn read(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n"
	dir := writeProject(t, map[string]string{
		"main.nomi": main, "files.nomi": files,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	mainURI := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	filesURI := fileuri.FromPath(filepath.Join(dir, "files.nomi"))

	first := NewServer()
	first.lowering.delay = 10 * time.Millisecond
	c1 := startRPC(t, first, dir)
	c1.open(mainURI, main)
	waitStored(t, first, 1)

	s, c := startCapabilityServer(t, dir, nil)
	c.open(mainURI, main)
	c.open(filesURI, files)
	// What each function can reach, at its name and at its calls.
	functions := func(main string) {
		t.Helper()
		const load = "Reaches `std::fs` through `read`."
		waitReachAt(t, s, c, mainURI, main, "files.load", load)
		waitReachAt(t, s, c, mainURI, main, "main()", "Reaches `std::fs`, `std::io`.")
		waitReachAt(t, s, c, filesURI, files, "load(path", load)
		waitReachAt(t, s, c, filesURI, files, "read(path) |>", "Reaches `std::fs`.")
		waitReachAt(t, s, c, filesURI, files, "read(path: String", "Reaches `std::fs`.")
	}
	waitReachAt(t, s, c, mainURI, main, "io.print", "Reaches `std::io`.")
	waitReachAt(t, s, c, filesURI, files, "fs.read_text", "Reaches `std::fs`.")
	functions(main)
	if opened, _ := s.docs.StoredPrograms(); opened != 1 {
		t.Fatalf("the server opened %d programs from records, want 1", opened)
	}
	edited := strings.Replace(main, "notes.txt", "other.txt", 1)
	c.change(mainURI, 2, edited)
	waitReachAt(t, s, c, mainURI, edited, "io.print", "Reaches `std::io`.")
	waitReachAt(t, s, c, filesURI, files, "fs.read_text", "Reaches `std::fs`.")
	functions(edited)
	if s.docs.StoredEdits() == 0 {
		t.Fatalf("the edit was not taken in the program opened from its record: %v", s.docs.FullCheckReasons())
	}
}

// An unsaved edit of nomi.toml that allows the call turns the line from
// refused to allowed; closing the buffer turns it back.
func TestCapabilityHover_FollowsAnUnsavedManifest(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\nstd = [\"std::io\"]\n"
	dir := writeProject(t, map[string]string{"main.nomi": capabilityMain, "nomi.toml": manifest})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	manifestURI := fileuri.FromPath(filepath.Join(dir, "nomi.toml"))
	s, c := startCapabilityServer(t, dir, map[string]string{"main.nomi": capabilityMain})
	waitReachAt(t, s, c, uri, capabilityMain, "fs.read_text", "Reaches `std::fs`, which `nomi.toml` does not allow in `[capabilities.self]`.")
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(manifestURI), LanguageID: "toml", Version: 1,
		Text: strings.Replace(manifest, `["std::io"]`, `["std::io", "std::fs"]`, 1),
	}})
	waitReachAt(t, s, c, uri, capabilityMain, "fs.read_text", "Reaches `std::fs`, which `nomi.toml` allows in `[capabilities.self]`.")
	c.notify("textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(manifestURI)}})
	waitReachAt(t, s, c, uri, capabilityMain, "fs.read_text", "Reaches `std::fs`, which `nomi.toml` does not allow in `[capabilities.self]`.")
}

// manifestAt is the position just inside needle's first character on line
// (1-based) of text, a nomi.toml.
func manifestAt(t *testing.T, text string, line int, needle string) protocol.Position {
	t.Helper()
	l := strings.Split(text, "\n")[line-1]
	i := strings.Index(l, needle)
	if i < 0 {
		t.Fatalf("line %d %q has no %q", line, l, needle)
	}
	return protocol.Position{Line: uint32(line - 1), Character: uint32(i + 1)}
}

// manifestHoverText is the hover at pos in the nomi.toml at uri, "" for
// none.
func manifestHoverText(t *testing.T, s *Server, uri string, pos protocol.Position) string {
	t.Helper()
	h, err := s.textDocumentHover(&glsp.Context{}, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		return ""
	}
	return h.Contents.(protocol.MarkupContent).Value
}

// A hover on a std name in nomi.toml's lists shows the calls that need it
// in that list, as `nomi capabilities --why` prints them: the project's own
// calls under [capabilities.self], the dependencies' under
// [capabilities.deps], and a module's own under its entry. References
// answer the same calls, at their callees. A name nothing there reaches
// says so, and has none.
func TestCapabilityHover_ANameInNomiToml(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n" +
		"[capabilities.self]\nstd = [\"std::io\", \"std::net\"]\n\n" +
		"[capabilities.deps]\nstd = [\"std::fs\", \"std::io\"]\n\n" +
		"[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\", \"std::instant\"]\n"
	app := manifestProject(t, manifest)
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	cases := []struct {
		name   string
		line   int
		needle string
		want   string
	}{
		{"a call the project's own code makes", 6, `"std::io"`, "```text\n1 call reaches std::io:\n  main.nomi:7:5\n    reached from `main`\n```"},
		{"nothing in the project's own code", 6, `"std::net"`, "Nothing in this module's own code reaches std::net."},
		{"a dependency's call", 9, `"std::fs"`, "```text\n1 call reaches std::fs:\n  rules.nomi:4:5 in example.com/pricing\n    reached from `main`; `main` calls `load`\n```"},
		{"no dependency's call", 9, `"std::io"`, "No dependency reaches std::io."},
		{"in the module's entry", 12, `"std::fs"`, "```text\n1 call reaches std::fs:\n  rules.nomi:4:5 in example.com/pricing\n    reached from `main`; `main` calls `load`\n```"},
		{"nothing in the module", 12, `"std::instant"`, "Nothing in example.com/pricing reaches std::instant."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := manifestHoverText(t, s, uri, manifestAt(t, manifest, tc.line, tc.needle)); got != tc.want {
				t.Errorf("hover on %s at line %d says\n%s\nwant\n%s", tc.needle, tc.line, got, tc.want)
			}
		})
	}
	if computed := s.reaches.computed.Load(); computed != 1 {
		t.Errorf("the project's reach was worked out %d times for %d hovers; want once", computed, len(cases))
	}
	refs := func(line int, needle string) []protocol.Location {
		got, err := s.textDocumentReferences(&glsp.Context{}, &protocol.ReferenceParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: manifestAt(t, manifest, line, needle)}})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	rules := protocol.DocumentUri(fileuri.FromPath(filepath.Join(filepath.Dir(app), "pricing", "rules.nomi")))
	want := []protocol.Location{{URI: rules, Range: protocol.Range{Start: protocol.Position{Line: 3, Character: 4}, End: protocol.Position{Line: 3, Character: 16}}}}
	if got := refs(12, `"std::fs"`); !slices.Equal(got, want) {
		t.Errorf("references of std::fs in the entry are %+v; want %+v", got, want)
	}
	if got := refs(6, `"std::net"`); len(got) != 0 {
		t.Errorf("references of std::net, which nothing reaches, are %+v; want none", got)
	}
	// An edit of a Nomi file on disk is seen once the server hears of it.
	if err := os.WriteFile(filepath.Join(app, "main.nomi"), []byte("import std::net\n\nfn main()\n    _ = net.lookup_host(\"example.com\")\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.reaches.changed()
	if got, want := manifestHoverText(t, s, uri, manifestAt(t, manifest, 6, `"std::io"`)), "Nothing in this module's own code reaches std::io."; got != want {
		t.Errorf("after main stops printing, hover on std::io says %q; want %q", got, want)
	}
}
