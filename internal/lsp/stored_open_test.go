package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/nomi-language/nomi/internal/token"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The harness a program opened from its record is held to
// (analysis/stored_program.go). A server whose document manager wrote the
// program's record (NoteLoweredClean, after a full check) leaves it in the
// artifact cache; a second server opens the same documents from it, and a
// third checks them as a server with no record does. Each server lowers
// its open documents as it does in the background (lowerOpen). Every
// request a reader makes of the open documents must answer alike in the
// two: the published diagnostics, the lowering's among them, and hover,
// with what a call or a function reaches outside the machine, definition,
// type definition, implementation, document highlights, references,
// prepare-rename and rename, completion and signature help at every
// identifier, hover at every function's name, and the document's symbols,
// semantic tokens, with the ones that reach outside the machine marked,
// inlay hints, code lenses, links and call hierarchy.

// storedProject is a project on disk: its directory and its .nomi files'
// module keys, the entry first.
type storedProject struct {
	name string
	dir  string
	keys []string
}

func (p storedProject) uri(key string) string {
	return fileuri.FromPath(filepath.Join(p.dir, filepath.FromSlash(key)+".nomi"))
}

// copyProject copies the directory src, with every file in it, under a
// new directory and answers the project, its entry main or else its first
// test file.
func copyProject(tb testing.TB, src, name string) storedProject {
	tb.Helper()
	dir := tb.TempDir()
	p := storedProject{name: name, dir: dir}
	var entry string
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".nomi" {
			key := strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
			p.keys = append(p.keys, key)
			if key == "main" {
				entry = key
			}
		}
		return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
	})
	if err != nil {
		tb.Fatal(err)
	}
	sort.Strings(p.keys)
	if entry == "" {
		for _, key := range p.keys {
			if strings.HasSuffix(key, "_test") {
				entry = key
				break
			}
		}
	}
	if entry == "" {
		return storedProject{}
	}
	for i, key := range p.keys {
		if key == entry {
			p.keys = append([]string{entry}, append(p.keys[:i:i], p.keys[i+1:]...)...)
			break
		}
	}
	return p
}

// storedCorpusProjects are the corpus directories with more than one .nomi
// file.
func storedCorpusProjects(tb testing.TB) []storedProject {
	tb.Helper()
	dirs, err := filepath.Glob(filepath.Join("..", "..", "tests", "*", "*"))
	if err != nil {
		tb.Fatal(err)
	}
	var out []storedProject
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		nomi, _ := filepath.Glob(filepath.Join(dir, "*.nomi"))
		sub, _ := filepath.Glob(filepath.Join(dir, "*", "*.nomi"))
		if len(nomi)+len(sub) < 2 {
			continue
		}
		p := copyProject(tb, dir, strings.TrimPrefix(filepath.ToSlash(dir), "../../tests/"))
		if p.dir != "" {
			out = append(out, p)
		}
	}
	return out
}

// openServer is a server over p's directory with the documents with keys
// open, each analyzed.
func openServer(p storedProject, keys []string) *Server {
	s := NewServer()
	s.docs.SetEditorFolder(p.dir)
	for _, key := range keys {
		text, err := os.ReadFile(fileuri.ToPath(p.uri(key)))
		if err != nil {
			panic(err)
		}
		s.docs.Open(p.uri(key), string(text))
		s.docs.AnalyzeLatest(context.Background(), p.uri(key))
	}
	return s
}

// writeRecord writes p's record as a server does after the entry's
// lowering finds nothing, with the entry's kept lowering, and reports
// whether it was written.
func writeRecord(p storedProject) bool {
	s := openServer(p, p.keys[:1])
	text, _ := os.ReadFile(fileuri.ToPath(p.uri(p.keys[0])))
	_, before := s.docs.StoredPrograms()
	lowerOpen(s, p, p.keys[:1])
	s.docs.NoteLoweredClean(p.uri(p.keys[0]), string(text), analyzedlowering.Kept)
	_, after := s.docs.StoredPrograms()
	return after > before
}

// answers renders every request's answer of s for the open document with
// key, one line per request and position: every position-free request,
// and the position requests at every every-th identifier.
func answers(t *testing.T, s *Server, p storedProject, key string, every int) []string {
	t.Helper()
	uri := p.uri(key)
	release := s.docs.View()
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		release()
		t.Fatalf("%s has no analysis", key)
	}
	content := snap.Content
	var out []string
	add := func(what string, v any, err error) {
		data, _ := json.Marshal(v)
		line := what + " " + strings.ReplaceAll(string(data), p.dir, "<dir>")
		if err != nil {
			line += " error " + err.Error()
		}
		out = append(out, line)
	}
	var published []string
	notify := func(method string, params any) {
		if d, ok := params.(*protocol.PublishDiagnosticsParams); ok {
			data, _ := json.Marshal(d.Diagnostics)
			published = append(published, strings.ReplaceAll(string(data), p.dir, "<dir>"))
		}
	}
	s.publishViewed(notify, snap)
	release()
	out = append(out, "diagnostics "+strings.Join(published, " "))
	doc := protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}
	syms, err := s.textDocumentDocumentSymbol(nil, &protocol.DocumentSymbolParams{TextDocument: doc})
	add("symbols", syms, err)
	toks, err := s.textDocumentSemanticTokensFull(nil, &protocol.SemanticTokensParams{TextDocument: doc})
	add("tokens", toks, err)
	if toks != nil {
		for _, tok := range decodeSemanticTokenData(toks.Data) {
			if tok.modifiers&modEffect != 0 {
				effectTokenAnswers.Add(1)
				break
			}
		}
	}
	var ih InlayHintParams
	ih.TextDocument.URI = uri
	ih.Range.End.Line = uint32(strings.Count(content, "\n") + 1)
	hints, err := s.textDocumentInlayHint(nil, &ih)
	add("inlay", hints, err)
	lenses, err := s.textDocumentCodeLens(nil, &protocol.CodeLensParams{TextDocument: doc})
	add("lens", lenses, err)
	links, err := s.textDocumentDocumentLink(nil, &protocol.DocumentLinkParams{TextDocument: doc})
	add("links", links, err)
	ident := 0
	lexed := lexer.Lex(content)
	for i, tok := range lexed {
		if tok.Type != token.IDENT && tok.Type != token.TYPE_IDENT {
			continue
		}
		at := protocol.TextDocumentPositionParams{TextDocument: doc, Position: protocol.Position{Line: uint32(tok.Line - 1), Character: uint32(tokenUTF16Col(content, tok.Line, tok.Col))}}
		where := fmt.Sprintf("%d:%d %s", tok.Line, tok.Col, tok.Lexeme)
		hover := func() {
			h, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: at})
			add("hover "+where, h, err)
			if h != nil && strings.Contains(out[len(out)-1], "Has effects: `") &&
				(i > 0 && lexed[i-1].Type == token.FN || strings.Contains(out[len(out)-1], " through `")) {
				functionReachHovers.Add(1)
			}
		}
		if ident++; (ident-1)%every != 0 {
			// A function's name is always asked about on hover, which
			// says what the function can reach outside the machine.
			if i > 0 && lexed[i-1].Type == token.FN {
				hover()
			}
			continue
		}
		hover()
		d, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: at})
		add("definition "+where, d, err)
		td, err := s.textDocumentTypeDefinition(nil, &protocol.TypeDefinitionParams{TextDocumentPositionParams: at})
		add("type definition "+where, td, err)
		hl, err := s.textDocumentDocumentHighlight(nil, &protocol.DocumentHighlightParams{TextDocumentPositionParams: at})
		add("highlight "+where, hl, err)
		if answersInRecord && interfaceMethodAt(s, uri, tok.Line, tok.Col) {
			// References to an interface method check the document whole.
			continue
		}
		refs, err := s.textDocumentReferences(nil, &protocol.ReferenceParams{TextDocumentPositionParams: at, Context: protocol.ReferenceContext{IncludeDeclaration: true}})
		sortLocations(refs)
		add("references "+where, refs, err)
		pr, err := s.textDocumentPrepareRename(nil, &protocol.PrepareRenameParams{TextDocumentPositionParams: at})
		add("prepare rename "+where, pr, err)
		if i%3 == 0 {
			rn, err := s.textDocumentRename(nil, &protocol.RenameParams{TextDocumentPositionParams: at, NewName: "renamed_" + strings.ToLower(tok.Lexeme)})
			if rn != nil {
				for _, edits := range rn.Changes {
					sort.Slice(edits, func(a, b int) bool {
						x, y := edits[a].Range.Start, edits[b].Range.Start
						return x.Line < y.Line || x.Line == y.Line && x.Character < y.Character
					})
				}
			}
			add("rename "+where, rn, err)
		}
		end := at
		end.Position.Character += uint32(len(tok.Lexeme))
		c, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{TextDocumentPositionParams: end})
		add("completion "+where, c, err)
		sh, err := s.textDocumentSignatureHelp(nil, &protocol.SignatureHelpParams{TextDocumentPositionParams: end})
		add("signature "+where, sh, err)
		ch, err := s.textDocumentPrepareCallHierarchy(nil, &protocol.CallHierarchyPrepareParams{TextDocumentPositionParams: at})
		add("call hierarchy "+where, ch, err)
		for _, item := range ch {
			in, err := s.callHierarchyIncomingCalls(nil, &protocol.CallHierarchyIncomingCallsParams{Item: item})
			sort.Slice(in, func(a, b int) bool { return in[a].From.URI+in[a].From.Name < in[b].From.URI+in[b].From.Name })
			add("incoming "+where, in, err)
			outc, err := s.callHierarchyOutgoingCalls(nil, &protocol.CallHierarchyOutgoingCallsParams{Item: item})
			add("outgoing "+where, outc, err)
		}
	}
	if answersInRecord {
		return out
	}
	// Last, since an implementation request checks a document opened from
	// its record whole (CheckWhole), and the requests above should read
	// the document as it was opened.
	ident = 0
	for _, tok := range lexer.Lex(content) {
		if tok.Type != token.IDENT && tok.Type != token.TYPE_IDENT {
			continue
		}
		if ident++; (ident-1)%every != 0 {
			continue
		}
		at := protocol.TextDocumentPositionParams{TextDocument: doc, Position: protocol.Position{Line: uint32(tok.Line - 1), Character: uint32(tokenUTF16Col(content, tok.Line, tok.Col))}}
		im, err := s.textDocumentImplementation(nil, &protocol.ImplementationParams{TextDocumentPositionParams: at})
		if locs, ok := im.([]protocol.Location); ok {
			sortLocations(locs)
		}
		add(fmt.Sprintf("implementation %d:%d %s", tok.Line, tok.Col, tok.Lexeme), im, err)
	}
	return out
}

// functionReachHovers counts the hovers answers asked for that say what a
// function of the program can reach outside the machine, on its name or
// through another function (vmhost's reachedFuncs), so a harness can
// require that it compared some.
var functionReachHovers atomic.Int64

// effectTokenAnswers counts the semantic token answers answers asked for
// that mark a token as reaching outside the machine (modEffect), so a
// harness can require that it compared some.
var effectTokenAnswers atomic.Int64

// sortLocations sorts locations by file and then position, the order a
// map's iteration does not keep.
func sortLocations(locs []protocol.Location) {
	sort.Slice(locs, func(a, b int) bool {
		x, y := locs[a], locs[b]
		if x.URI != y.URI {
			return x.URI < y.URI
		}
		if x.Range.Start.Line != y.Range.Start.Line {
			return x.Range.Start.Line < y.Range.Start.Line
		}
		return x.Range.Start.Character < y.Range.Start.Character
	})
}

// tokenUTF16Col is the UTF-16 column, from 0, of the byte column col (from 1)
// of line (from 1) of text.
func tokenUTF16Col(text string, line, col int) int {
	lines := strings.Split(text, "\n")
	if line-1 >= len(lines) {
		return col - 1
	}
	l := lines[line-1]
	if col-1 > len(l) {
		return col - 1
	}
	n := 0
	for _, r := range l[:col-1] {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// compareStored opens p's documents, in order, from its record in one
// server and with no record in another, and fails at the first answer
// that differs, asking at every every-th identifier. It reports whether
// the record was used.
func compareStored(t *testing.T, p storedProject, every int) (opened, refs bool) {
	t.Helper()
	full := openServerWithout(t, p, p.keys)
	stored := openServerFromRecord(p, p.keys)
	n, _ := stored.docs.StoredPrograms()
	for _, key := range p.keys {
		want, got := answers(t, full, p, key, every), answers(t, stored, p, key, every)
		for i := range max(len(want), len(got)) {
			w, g := "(none)", "(none)"
			if i < len(want) {
				w = want[i]
			}
			if i < len(got) {
				g = got[i]
			}
			if w != g {
				t.Fatalf("%s: %s opened from its record answers\n%s\nand checked whole\n%s", p.name, key, g, w)
			}
		}
	}
	return n > 0, stored.docs.RefsFromRecord() > 0
}

// openServerWithout is openServer with the artifact cache off, so nothing
// is opened from a record, and its documents lowered with the cache off
// too, so nothing writes one.
func openServerWithout(t *testing.T, p storedProject, keys []string) *Server {
	t.Helper()
	was := os.Getenv(artifactcache.RootEnv)
	os.Setenv(artifactcache.RootEnv, "off")
	defer os.Setenv(artifactcache.RootEnv, was)
	s := openServer(p, keys)
	if opened, _ := s.docs.StoredPrograms(); opened > 0 {
		t.Fatal("a server with the cache off opened a record")
	}
	lowerOpen(s, p, keys)
	return s
}

// openServerFromRecord is openServer, with its documents lowered.
func openServerFromRecord(p storedProject, keys []string) *Server {
	s := openServer(p, keys)
	lowerOpen(s, p, keys)
	return s
}

// lowerOpen runs the lowering of each open document with keys in s, one
// after another, as the server runs it in the background after an open or
// an edit (runLoweringChecks), so that the answers read what the runs
// found: the lowering's diagnostics, and on hover what a call reaches.
func lowerOpen(s *Server, p storedProject, keys []string) {
	for _, key := range keys {
		uri := p.uri(key)
		snap := s.docs.Snapshot(uri)
		if snap == nil {
			continue
		}
		c := &s.lowering
		c.mu.Lock()
		c.init()
		ctx, run := c.start(uri, snap.Text)
		gen := c.gen[uri]
		c.mu.Unlock()
		s.runLoweringChecks(ctx, uri, run, gen)
	}
}

// storedGenerated is how much of a generated project the stored-record
// tests ask about: one of 1,500 lines at every identifier for the whole
// walk, and for the sample the smallest projectgen makes (about 400
// lines) at every storedIdentStride-th identifier.
func storedGenerated(walk rotation.Walk) (lines, every int) {
	if walk.Full {
		return 1500, 1
	}
	return 400, storedIdentStride
}

// storedIdentStride is the sample of a generated project's identifiers
// the stored-record tests ask at by default: one in this many.
const storedIdentStride = 5

// storedStride is the sample of multi-file corpus projects
// TestOpenFromRecordMatchesAFullCheck opens by default: one in this many.
const storedStride = 20

// TestOpenFromRecordMatchesAFullCheck holds a program opened from its
// record to the program checked whole (see the comment at the top), for
// every multi-file corpus project and a generated one, with its entry
// open and then with every file open. By default it opens one corpus
// project in storedStride and the sample's generated project
// (storedGenerated); NOMI_CORPUS_WALKS=full opens every corpus project and
// asks of the generated one's 1,500 lines at every identifier.
func TestOpenFromRecordMatchesAFullCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("runs every request over many documents, about 10 seconds; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	walk := rotation.CorpusWalk(t, storedStride)
	var projects []storedProject
	for i, p := range storedCorpusProjects(t) {
		if walk.Takes(i) {
			projects = append(projects, p)
		}
	}
	// A project whose entry calls a function that reaches outside the
	// machine through another, so hovers on such calls are compared.
	reaching := t.TempDir()
	for name, text := range map[string]string{
		"nomi.toml":  "[package]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi":  "import\n    std::io\n    store\nend\n\nfn main()\n    io.print(store.load(\"notes.txt\"))\nend\n",
		"store.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    read(path) |> String.trim()\nend\n\nfn read(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	} {
		if err := os.WriteFile(filepath.Join(reaching, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	projects = append(projects, copyProject(t, reaching, "reaching"))
	lines, every := storedGenerated(walk)
	gen := projectgen.Generate(projectgen.Config{Seed: 3, Lines: lines})
	dir := t.TempDir()
	if err := gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	g := copyProject(t, dir, "generated")
	g.keys = []string{gen.Entry, gen.Hub(), gen.Leaf()}
	projects = append(projects, g)
	used, refs := 0, 0
	reachHovers := functionReachHovers.Load()
	effectTokens := effectTokenAnswers.Load()
	for i, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			if !writeRecord(p) {
				t.Skip("no record: the program has something to report or reads what its record does not track")
			}
			// Each corpus project is asked at every identifier.
			ask := 1
			if i == len(projects)-1 {
				ask = every
			}
			opened, fromRefs := compareStored(t, p, ask)
			if opened {
				used++
			}
			if fromRefs {
				refs++
			}
		})
	}
	if used == 0 {
		t.Error("no project was opened from its record")
	}
	// The generated project leaves files closed, whose references the
	// record answers.
	if refs == 0 {
		t.Error("no closed file's references were answered from a record")
	}
	// The generated project's functions reach outside through others, so
	// what a function can reach is among the hovers compared.
	if functionReachHovers.Load() == reachHovers {
		t.Error("no hover compared said what a function of the program reaches")
	}
	if effectTokenAnswers.Load() == effectTokens {
		t.Error("no semantic tokens compared marked a token that reaches outside the machine")
	}
}
