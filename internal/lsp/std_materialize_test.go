package lsp

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/stdcache"
	"github.com/nomi-language/nomi/std"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// stdSourceFS is the checkout's std .nomi files as an in-memory tree, which
// a test may change to simulate another std version.
func stdSourceFS(t *testing.T) fstest.MapFS {
	t.Helper()
	root, err := analysis.StdlibPath()
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	err = fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".nomi") {
			return err
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		out[p] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// materializedServer is a server whose navigation into std lands in a
// materialized directory under cacheRoot built from files, as an installed
// server without a source tree does.
func materializedServer(cacheRoot string, files fs.FS) *Server {
	s := NewServer()
	s.stdNav = stdcache.NewNavigator("", stdcache.New(cacheRoot, files))
	return s
}

func (c *rpcClient) definition(uri string, pos protocol.Position) protocol.Location {
	c.t.Helper()
	var raw json.RawMessage
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.conn.Call(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": pos,
	}, &raw); err != nil {
		c.t.Fatalf("definition: %v", err)
	}
	var loc protocol.Location
	if json.Unmarshal(raw, &loc) == nil && loc.URI != "" {
		return loc
	}
	var locs []protocol.Location
	if json.Unmarshal(raw, &locs) == nil && len(locs) > 0 {
		return locs[0]
	}
	c.t.Fatalf("definition answered %s", raw)
	return protocol.Location{}
}

// lineAt is line (zero-based) of the file uri names.
func lineAt(t *testing.T, uri protocol.DocumentUri, line uint32) string {
	t.Helper()
	data, err := os.ReadFile(strings.TrimPrefix(string(uri), "file://"))
	if err != nil {
		t.Fatalf("definition target: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	if int(line) >= len(lines) {
		t.Fatalf("%s has %d lines, definition names line %d", uri, len(lines), line)
	}
	return lines[line]
}

const someImport = "import std/maybe.Maybe.{Some, None}\n\nfn main() {\n    _ = Some(1)\n}\n"

// Two servers built from different std sources, sharing one cache root,
// materialize to two directories: the second writes nothing the first's
// editor buffers were opened from. Go-to-definition into std lands on the
// declaration's line in the server's own directory.
func TestStdMaterialize_TwoServersWriteTwoVersions(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "std")
	current := stdSourceFS(t)
	newer := stdSourceFS(t)
	maybe := newer["maybe.nomi"]
	newer["maybe.nomi"] = &fstest.MapFile{Data: append([]byte("// a newer std\n"), maybe.Data...)}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(someImport), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + path
	somePos := protocol.Position{Line: 0, Character: 24}

	a := startRPC(t, materializedServer(cacheRoot, current), dir)
	a.open(uri, someImport)
	a.waitDiagnostics(uri)
	locA := a.definition(uri, somePos)

	b := startRPC(t, materializedServer(cacheRoot, newer), dir)
	b.open(uri, someImport)
	b.waitDiagnostics(uri)
	locB := b.definition(uri, somePos)

	pathA := strings.TrimPrefix(string(locA.URI), "file://")
	pathB := strings.TrimPrefix(string(locB.URI), "file://")
	if filepath.Base(pathA) != "maybe.nomi" || filepath.Base(pathB) != "maybe.nomi" {
		t.Fatalf("definitions land in %s and %s, want maybe.nomi", pathA, pathB)
	}
	if filepath.Dir(pathA) == filepath.Dir(pathB) {
		t.Fatalf("both servers materialize to %s", filepath.Dir(pathA))
	}
	for _, p := range []string{pathA, pathB} {
		if rel, err := filepath.Rel(cacheRoot, p); err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("%s is outside the cache root %s", p, cacheRoot)
		}
	}
	got, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatal(err)
	}
	if embedded, _ := std.ReadFile("maybe"); string(got) != string(embedded) {
		t.Error("the first server's maybe.nomi no longer holds its own std's text after the second server materialized")
	}
	line := lineAt(t, locA.URI, locA.Range.Start.Line)
	if !strings.HasPrefix(line[locA.Range.Start.Character:], "Some") {
		t.Errorf("definition of Some lands on %q (col %d)", line, locA.Range.Start.Character)
	}
	// The materialized file is a std file to the server: opened, it is
	// analyzed as std, without the prelude, and reports nothing.
	a.open(string(locA.URI), string(got))
	if p := a.waitDiagnostics(string(locA.URI)); p.n != 0 {
		t.Errorf("the opened std file reports %v", p.msgs)
	}
}

const regexUser = "import std/regex.Regex\n\nfn main() {\n    _ = Regex.compile(\"a\")\n}\n"

// newerRegex is std/regex as a newer std might have it: every declaration
// a line lower, and `compile` renamed, so a user file analyzed against it
// would report an unknown function.
func newerRegex(text string) string {
	return "// a newer std\n" + strings.ReplaceAll(text, "fn compile(", "fn compile_pattern(")
}

// An open std buffer whose text is not the std this server runs never feeds
// another document's analysis, and it reports one note saying so instead of
// the duplicate impls its own analysis against the server's std finds.
//
// Two shapes: the materialized copy go-to-definition opens, and the file in
// the std source tree, which is where std imports resolve from.
func TestStdBuffer_ChangedTextStaysInItsOwnBuffer(t *testing.T) {
	stdRoot, err := analysis.StdlibPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{"materialized", "source tree"} {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.nomi")
			if err := os.WriteFile(path, []byte(regexUser), 0o644); err != nil {
				t.Fatal(err)
			}
			uri := "file://" + path
			s := materializedServer(filepath.Join(t.TempDir(), "std"), stdSourceFS(t))
			c := startRPC(t, s, dir)
			c.open(uri, regexUser)
			if p := c.waitDiagnostics(uri); p.n != 0 {
				t.Fatalf("the user file reports %v before any std buffer is open", p.msgs)
			}

			stdURI := "file://" + filepath.Join(stdRoot, "regex.nomi")
			if shape == "materialized" {
				stdURI = string(c.definition(uri, protocol.Position{Line: 0, Character: 18}).URI)
				if !strings.HasSuffix(stdURI, "/regex.nomi") || strings.HasPrefix(stdURI, "file://"+stdRoot) {
					t.Fatalf("definition of Regex lands in %s, want the materialized regex.nomi", stdURI)
				}
			}
			embedded, _ := std.ReadFile("regex")
			c.open(stdURI, string(embedded))
			if p := c.waitDiagnostics(stdURI); p.n != 0 {
				t.Fatalf("the unchanged std buffer reports %v", p.msgs)
			}

			c.change(stdURI, 2, newerRegex(string(embedded)))
			p := c.waitErrors(stdURI)
			if p.n != 1 || !strings.Contains(p.msgs[0], "differs from the std this language server runs") {
				t.Errorf("the changed std buffer reports %v, want the one note", p.msgs)
			}

			edited := regexUser + "\nfn other(): Bool {\n    Regex.compile(\"b\") |> Result.ok?()\n}\n"
			c.change(uri, 2, edited)
			for {
				p := c.waitDiagnostics(uri)
				if p.n != 0 {
					t.Fatalf("the user file reports %v after the std buffer changed", p.msgs)
				}
				if snap := s.docs.Snapshot(uri); snap != nil && snap.Current() && snap.Content == edited {
					break
				}
			}
		})
	}
}

const someAndRegex = "import std/maybe.Maybe.{Some}\nimport std/regex.Regex\n\nfn main() {\n    _ = Some(1)\n    _ = Regex.compile(\"a\")\n}\n"

// Go-to-definition into std lands in the checkout's std source tree only for
// a file whose content is the std the server runs. A checkout file changed
// under a running server sends the jump to the materialized copy, on the
// line the server's analysis names, and leaves every other module in the
// checkout; restoring the file sends the jump back.
func TestStdCheckout_ChangedFileJumpsToTheMaterializedCopy(t *testing.T) {
	files := stdSourceFS(t)
	checkout := filepath.Join(t.TempDir(), "std")
	for p, f := range files {
		target := filepath.Join(checkout, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cacheRoot := filepath.Join(t.TempDir(), "std")
	s := NewServer()
	s.stdNav = stdcache.NewNavigator(checkout, stdcache.New(cacheRoot, files))

	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(someAndRegex), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + path
	c := startRPC(t, s, dir)
	c.open(uri, someAndRegex)
	c.waitDiagnostics(uri)
	somePos := protocol.Position{Line: 0, Character: 24}
	regexPos := protocol.Position{Line: 1, Character: 18}

	// jump asks for the definition at pos and checks that it lands in
	// under's file rel, at a column where name starts.
	jump := func(pos protocol.Position, under, rel, name string) {
		t.Helper()
		loc := c.definition(uri, pos)
		got := strings.TrimPrefix(string(loc.URI), "file://")
		if r, err := filepath.Rel(under, got); err != nil || r != rel {
			t.Fatalf("definition of %s lands in %s, want %s under %s", name, got, rel, under)
		}
		line := lineAt(t, loc.URI, loc.Range.Start.Line)
		if !strings.HasPrefix(line[loc.Range.Start.Character:], name) {
			t.Fatalf("definition of %s lands on %q (col %d) in %s", name, line, loc.Range.Start.Character, got)
		}
	}
	versionDir := filepath.Join(cacheRoot, stdcache.Version(files))

	jump(somePos, checkout, "maybe.nomi", "Some")
	jump(regexPos, checkout, "regex.nomi", "Regex")

	// Two lines merged above every declaration: the checkout's lines no
	// longer match the server's analysis.
	maybePath := filepath.Join(checkout, "maybe.nomi")
	changed := append([]byte("// merged after the server started\n\n"), files["maybe.nomi"].Data...)
	if err := os.WriteFile(maybePath, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	jump(somePos, versionDir, "maybe.nomi", "Some")
	jump(regexPos, checkout, "regex.nomi", "Regex")

	if err := os.WriteFile(maybePath, files["maybe.nomi"].Data, 0o644); err != nil {
		t.Fatal(err)
	}
	jump(somePos, checkout, "maybe.nomi", "Some")

	// Same size, different bytes: the new modification time alone makes
	// the server compare again.
	same := append([]byte(nil), files["maybe.nomi"].Data...)
	same[0] ^= 0x20
	if err := os.WriteFile(maybePath, same, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(maybePath, later, later); err != nil {
		t.Fatal(err)
	}
	jump(somePos, versionDir, "maybe.nomi", "Some")
}
