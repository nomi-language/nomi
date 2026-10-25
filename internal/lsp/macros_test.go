package lsp

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// macroFixture copies the macro fixture (testdata/macros:
// module csvdata declares `table`, module rates uses it) into a
// fresh directory and answers rates' root.
func macroFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "macros")
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
		return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "rates")
}

func openFile(t *testing.T, s *Server, path string) (uri, text string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	uri = pathToURI(path)
	s.docs.Open(uri, string(data))
	return uri, string(data)
}

func positionParams(uri, text string, off int) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Position:     offsetPosition(text, off),
	}
}

// What a macro reports about a table, and a type error the checker
// reports in the code it wrote, are published on the table's URI, and
// cleared once the table is fixed on disk.
func TestMacros_DiagnosticsArePublishedOnTheirInput(t *testing.T) {
	root := macroFixture(t)
	csv := filepath.Join(root, "data", "rates.csv")
	if err := os.WriteFile(csv, []byte("code,basis_points\nusd,9200\ngbp,1.5\neur,1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	tablesURI, _ := openFile(t, s, filepath.Join(root, "tables.nomi"))
	s.publish(s.notify, tablesURI)
	diags := published()[pathToURI(csv)]
	if len(diags) != 1 || diags[0].Range.Start.Line != 3 || !strings.Contains(diags[0].Message, "this row has 3 cells") {
		t.Fatalf("the table's diagnostics are %+v, want the bad row on line 4", diags)
	}

	// With the rows right, the checker's error in the generated code is the
	// table's: the Float cell of an Int table.
	if err := os.WriteFile(csv, []byte("code,basis_points\nusd,9200\ngbp,1.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.docs.InputsChanged(csv)
	s.publish(s.notify, tablesURI)
	diags = published()[pathToURI(csv)]
	if len(diags) != 1 || diags[0].Range.Start.Line != 2 || diags[0].Range.Start.Character != 4 || !strings.Contains(diags[0].Message, "expected Maybe<Int>, got Maybe<Float>") {
		t.Fatalf("the table's diagnostics are %+v, want the type error at 3:5", diags)
	}

	if err := os.WriteFile(csv, []byte("code,basis_points\nusd,9200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.docs.InputsChanged(csv)
	s.publish(s.notify, tablesURI)
	if diags, ok := published()[pathToURI(csv)]; !ok || len(diags) != 0 {
		t.Errorf("the fixed table's diagnostics are %+v, want them cleared", diags)
	}
	for _, d := range published()[tablesURI] {
		t.Errorf("tables.nomi shows %+v", d)
	}
}

// A macro whose `expand` reaches an effect is refused at each declaration,
// and the refusals are reported alone, as a declaration the macro
// declined is: main.nomi's calls of the functions the tables would have
// declared are not errors too.
func TestMacros_AnEffectRefusalIsReportedAlone(t *testing.T) {
	root := macroFixture(t)
	src := filepath.Join(root, "..", "csvdata", "csvdata.nomi")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "    std/json.Json\n", "    std/io\n    std/json.Json\n", 1)
	text = strings.Replace(text, "        decl = call.decl\n", "        decl = call.decl\n        io.print(\"expanding\")\n", 1)
	if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	mainURI, _ := openFile(t, s, filepath.Join(root, "main.nomi"))
	tablesURI, _ := openFile(t, s, filepath.Join(root, "tables.nomi"))
	s.publish(s.notify, mainURI)
	s.publish(s.notify, tablesURI)
	for _, d := range published()[mainURI] {
		t.Errorf("main.nomi shows %q", d.Message)
	}
	diags := published()[tablesURI]
	if len(diags) != 2 {
		t.Fatalf("tables.nomi shows %+v, want the two refusals", diags)
	}
	for _, d := range diags {
		if !strings.Contains(d.Message, "its `expand` has effects, which a macro may not have while a program is checked (CsvTable.expand calls io.print)") {
			t.Errorf("tables.nomi shows %q, want the refusal", d.Message)
		}
	}
}

// A call of a function a declaration expanded to goes to the declaration,
// and hovers as the function; the keyword hovers as the macro; inside
// the body, the macro's Navigation impl answers hover, definition and
// completion.
func TestMacros_Navigation(t *testing.T) {
	root := macroFixture(t)
	s := NewServer()
	s.docs.SetWorkspaceRoot(root)
	mainURI, main := openFile(t, s, filepath.Join(root, "main.nomi"))
	tablesURI, tables := openFile(t, s, filepath.Join(root, "tables.nomi"))

	call := strings.Index(main, "rate(") + 1
	def, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: positionParams(mainURI, main, call)})
	if err != nil {
		t.Fatal(err)
	}
	loc, ok := def.(*protocol.Location)
	if !ok || string(loc.URI) != tablesURI || loc.Range.Start.Line != 3 || loc.Range.Start.Character != 19 {
		t.Fatalf("definition %+v, want tables.nomi 4:20, the declaration's name", def)
	}
	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: positionParams(mainURI, main, call)})
	if err != nil || hover == nil {
		t.Fatalf("no hover on the call: %v", err)
	}
	if text := hover.Contents.(protocol.MarkupContent).Value; !strings.Contains(text, "rate(code: String) -> Maybe<Int>") || !strings.Contains(text, "Exchange rates to the euro") {
		t.Errorf("hover %q, want the function's signature and the declaration's doc", text)
	}

	keyword := strings.Index(tables, "pub csvdata.table! rate") + len("pub csvdata.")
	hover, err = s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: positionParams(tablesURI, tables, keyword)})
	if err != nil || hover == nil {
		t.Fatalf("no hover on the keyword: %v", err)
	}
	if text := hover.Contents.(protocol.MarkupContent).Value; !strings.Contains(text, "macro table: CsvTable") || !strings.Contains(text, "A lookup function over the rows of a CSV file.") {
		t.Errorf("keyword hover %q, want the macro's declaration and doc", text)
	}

	body := strings.Index(tables, "`rates.csv`") + 3
	hover, err = s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: positionParams(tablesURI, tables, body)})
	if err != nil || hover == nil {
		t.Fatalf("no hover in the body: %v", err)
	}
	if text := hover.Contents.(protocol.MarkupContent).Value; text != "`rate`: 3 rows from data/rates.csv" {
		t.Errorf("body hover %q, want the macro's", text)
	}
	def, err = s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: positionParams(tablesURI, tables, body)})
	if err != nil {
		t.Fatal(err)
	}
	if loc, ok := def.(*protocol.Location); !ok || uriToPath(string(loc.URI)) != filepath.Join(root, "data", "rates.csv") || loc.Range.Start.Line != 0 {
		t.Errorf("body definition %+v, want data/rates.csv's first line", def)
	}
	list, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{TextDocumentPositionParams: positionParams(tablesURI, tables, body)})
	if err != nil {
		t.Fatal(err)
	}
	cl, ok := list.(*protocol.CompletionList)
	if !ok || len(cl.Items) != 1 || cl.Items[0].Label != "data/rates.csv" {
		t.Errorf("body completions %+v, want the macro's one", list)
	}
}

// The function a declaration names carries the declaration's doc comment,
// once, in place of a doc the macro wrote for it.
func TestMacros_TheDeclarationsDocReplacesTheMacros(t *testing.T) {
	root := macroFixture(t)
	csvdata := filepath.Join(filepath.Dir(root), "csvdata", "csvdata.nomi")
	src, err := os.ReadFile(csvdata)
	if err != nil {
		t.Fatal(err)
	}
	const write = `Source.add("${visibility}fn ${decl.name}(")`
	if !strings.Contains(string(src), write) {
		t.Fatalf("csvdata no longer writes %s", write)
	}
	edited := strings.Replace(string(src), write, `Source.add("/// The macro's own doc.\n/// ${decl.doc}\n${visibility}fn ${decl.name}(")`, 1)
	if err := os.WriteFile(csvdata, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.docs.SetWorkspaceRoot(root)
	mainURI, main := openFile(t, s, filepath.Join(root, "main.nomi"))
	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: positionParams(mainURI, main, strings.Index(main, "rate(")+1)})
	if err != nil || hover == nil {
		t.Fatalf("no hover on the call: %v", err)
	}
	text := hover.Contents.(protocol.MarkupContent).Value
	if strings.Count(text, "Exchange rates to the euro") != 1 || strings.Contains(text, "The macro's own doc") {
		t.Errorf("hover %q, want the declaration's doc once and not the macro's", text)
	}
}

// A type a macro declares (cafe's `choices.one_of! Size`) is at the
// declaration's name for a use of it in another file, for go to
// definition and hover, and a use of one of its variants is at the word
// of the body it was made from.
func TestMacros_NavigationToADeclaredType(t *testing.T) {
	root := filepath.Join(filepath.Dir(macroFixture(t)), "cafe")
	s := NewServer()
	s.docs.SetWorkspaceRoot(root)
	mainURI, main := openFile(t, s, filepath.Join(root, "main.nomi"))
	sizesURI, sizes := openFile(t, s, filepath.Join(root, "sizes.nomi"))

	use := strings.Index(main, "size: Size") + len("size: ")
	def, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: positionParams(mainURI, main, use)})
	if err != nil {
		t.Fatal(err)
	}
	name := offsetPosition(sizes, strings.Index(sizes, "Size `"))
	loc, ok := def.(*protocol.Location)
	if !ok || string(loc.URI) != sizesURI || loc.Range.Start != name {
		t.Fatalf("definition %+v, want sizes.nomi %+v, the declaration's name", def, name)
	}
	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: positionParams(mainURI, main, use)})
	if err != nil || hover == nil {
		t.Fatalf("no hover on the use: %v", err)
	}
	if text := hover.Contents.(protocol.MarkupContent).Value; !strings.Contains(text, "enum Size") || !strings.Contains(text, "How big a coffee is.") {
		t.Errorf("hover %q, want the enum and the declaration's doc", text)
	}

	variant := strings.Index(main, "Size.Medium ->") + len("Size.")
	def, err = s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: positionParams(mainURI, main, variant)})
	if err != nil {
		t.Fatal(err)
	}
	word := offsetPosition(sizes, strings.Index(sizes, "medium"))
	if loc, ok := def.(*protocol.Location); !ok || string(loc.URI) != sizesURI || loc.Range.Start != word {
		t.Errorf("variant definition %+v, want sizes.nomi %+v, the body's word", def, word)
	}
}

// A macro's quick fix that runs one of its module's tools is a code
// action with the command that runs it; running the command runs the tool
// in the declaring module's root, and checks what reads its inputs again.
// The server runs only a tool run it offered.
func TestMacros_QuickFixRunsATool(t *testing.T) {
	root := macroFixture(t)
	csv := filepath.Join(root, "data", "rates.csv")
	if err := os.Remove(csv); err != nil {
		t.Fatal(err)
	}
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	var ran []string
	s.tools.run = func(_ context.Context, dir string, args []string) (string, error) {
		ran = append([]string{dir}, args...)
		return "created data/rates.csv", os.WriteFile(csv, []byte("code,basis_points\nusd,9200\n"), 0o644)
	}
	tablesURI, _ := openFile(t, s, filepath.Join(root, "tables.nomi"))
	s.publish(s.notify, tablesURI)
	var missing []protocol.Diagnostic
	for _, d := range published()[tablesURI] {
		if strings.HasPrefix(d.Message, "no file data/rates.csv") {
			missing = append(missing, d)
		}
	}
	if len(missing) != 1 {
		t.Fatalf("tables.nomi's diagnostics are %+v, want the missing table", published()[tablesURI])
	}
	got, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(tablesURI)},
		Range:        missing[0].Range,
		Context:      protocol.CodeActionContext{Diagnostics: missing},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := got.([]protocol.CodeAction)
	var fix *protocol.CodeAction
	for i := range actions {
		if actions[i].Title == "Create data/rates.csv with `nomi tool csvdata create`" {
			fix = &actions[i]
		}
	}
	if fix == nil || fix.Command == nil || fix.Edit != nil {
		t.Fatalf("actions %+v, want the macro's tool run with no edits", got)
	}
	if fix.Command.Command != runToolCommand {
		t.Fatalf("command %+v", fix.Command)
	}

	if _, err := s.workspaceExecuteCommand(nil, &protocol.ExecuteCommandParams{Command: runToolCommand, Arguments: []any{root, "csvdata", "rm", "-rf"}}); err == nil || ran != nil {
		t.Fatalf("a tool run the server did not offer ran: %v, %v", err, ran)
	}
	if _, err := s.workspaceExecuteCommand(nil, &protocol.ExecuteCommandParams{Command: runToolCommand, Arguments: fix.Command.Arguments}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, " ") != root+" csvdata create data/rates.csv" {
		t.Errorf("ran %q, want `nomi tool csvdata create data/rates.csv` in the module root", ran)
	}
	for _, d := range published()[tablesURI] {
		t.Errorf("after the tool ran, tables.nomi still shows %+v", d)
	}
}

// The macro's quick fix for a diagnostic it reported in a table: the
// edits its Navigation impl answers, offered on the table's URI.
func TestMacros_QuickFixInTheInput(t *testing.T) {
	root := macroFixture(t)
	csv := filepath.Join(root, "data", "rates.csv")
	if err := os.WriteFile(csv, []byte("code,basis_points\nusd,9200\nusd,9300\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	tablesURI, _ := openFile(t, s, filepath.Join(root, "tables.nomi"))
	s.publish(s.notify, tablesURI)
	diags := published()[pathToURI(csv)]
	if len(diags) != 1 || !strings.HasPrefix(diags[0].Message, "duplicate key usd") {
		t.Fatalf("the table's diagnostics are %+v, want the duplicate key", diags)
	}
	got, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(pathToURI(csv))},
		Range:        diags[0].Range,
		Context:      protocol.CodeActionContext{Diagnostics: diags},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, ok := got.([]protocol.CodeAction)
	if !ok || len(actions) != 1 || actions[0].Title != "Remove the duplicate row" {
		t.Fatalf("actions %+v, want the macro's fix", got)
	}
	edits := actions[0].Edit.Changes[protocol.DocumentUri(pathToURI(csv))]
	if len(edits) != 1 || edits[0].NewText != "" || edits[0].Range.Start.Line != 2 || edits[0].Range.End.Line != 3 {
		t.Errorf("edits %+v, want line 3 removed", edits)
	}
}

// `csvdata.` at the start of a top-level declaration completes the file's
// macros, the keywords it may qualify; elsewhere it completes the
// file's values. The qualifier is a reference to the imported file, so
// hover and go to definition on it reach the file.
func TestMacros_QualifiedKeywordCompletion(t *testing.T) {
	root := macroFixture(t)
	s := NewServer()
	s.docs.SetWorkspaceRoot(root)
	path := filepath.Join(root, "tables.nomi")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	text := string(data) + "\npub csvdata.\n"
	s.docs.Open(uri, text)
	line := strings.Count(text, "\n") - 1
	labels := completionLabels(t, s, uri, line, len("pub csvdata."))
	if !labels["table!"] {
		t.Errorf("completion after `pub csvdata.` offers %v, want table!", labels)
	}

	s2 := NewServer()
	s2.docs.SetWorkspaceRoot(root)
	tablesURI, tables := openFile(t, s2, path)
	qualifier := strings.Index(tables, "pub csvdata.table! rate") + len("pub ")
	def, err := s2.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: positionParams(tablesURI, tables, qualifier)})
	if err != nil {
		t.Fatal(err)
	}
	loc, ok := def.(*protocol.Location)
	if !ok || !strings.HasSuffix(string(loc.URI), "csvdata/csvdata.nomi") {
		t.Errorf("definition of the qualifier is %+v, want csvdata/csvdata.nomi", def)
	}
}

// stubFailureExt is a macro `gen` whose every declaration fails: one with
// a body answers the body as a stub beside its error (sqlkit's `query!`
// with no snapshot), one without declares nothing (`database!`).
const stubFailureExt = `import {
    std/macro.{Declaration, ExpandCall, Expansion, Macro, Source}
}

pub macro gen: Gen

type Gen

impl Macro for Gen {
    fn expand(call: ExpandCall) -> Expansion {
        decl = call.decl

        case decl.body {
            Some(body) -> {
                e = Source.new() |> Source.add(String.replace(body.text, ";", "\n") + "\n") |> Source.expansion()
                #{..e, diagnostics: Declaration.error(decl, "no snapshot for ${decl.name}").diagnostics}
            }

            None -> Declaration.error(decl, "no snapshot for the tables")
        }
    }
}
`

// A failed declaration's stub that names a type another failed
// declaration would have declared is reported alone in the language
// server too, by a document's own build and by the kept program after an
// edit, with no panic.
func TestMacros_AStubNamingAFailedDeclarationsTypeIsReportedAlone(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, text string) string {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("ext/nomi.toml", "[module]\nname = \"ext\"\n")
	write("ext/go.mod", "module ext\n\ngo 1.27.0\n")
	write("ext/ext.nomi", stubFailureExt)
	write("app/nomi.toml", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	write("app/go.mod", "module app\n\ngo 1.27.0\n\nrequire ext v0.0.0\n\nreplace ext => ../ext\n")
	schema := write("app/schema.nomi", "import ext.gen\n\npub gen! tables\n")
	queries := write("app/queries.nomi", "import {\n    ext.gen\n    schema.Status\n}\n\npub gen! find(s: Status) -> Int `pub fn find(s: Status) -> Int {;    _ = s;    0;}`\n")
	main := write("app/main.nomi", "import {\n    queries\n    std/io\n}\n\nfn main() {\n    io.print(queries.find)\n}\n")

	var panics []string
	hook := func(where string, value any, stack []byte) {
		panics = append(panics, fmt.Sprintf("%s: %v\n%s", where, value, stack))
	}
	panicHook.Store(&hook)
	t.Cleanup(func() { panicHook.Store(nil) })

	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(filepath.Join(dir, "app"))
	check := func(when string) {
		t.Helper()
		pubs := published()
		for _, d := range pubs[pathToURI(main)] {
			t.Errorf("%s: main.nomi shows %q", when, d.Message)
		}
		for path, want := range map[string]string{queries: "no snapshot for find", schema: "no snapshot for the tables"} {
			diags := pubs[pathToURI(path)]
			if len(diags) != 1 || !strings.Contains(diags[0].Message, want) {
				t.Errorf("%s: %s shows %+v, want %q alone", when, filepath.Base(path), diags, want)
			}
		}
	}
	var queriesURI, queriesText string
	for _, path := range []string{queries, schema, main} {
		uri, text := openFile(t, s, path)
		if path == queries {
			queriesURI, queriesText = uri, text
		}
		s.publish(s.notify, uri)
	}
	check("opened")

	s.docs.Update(queriesURI, queriesText+"\n")
	for _, path := range []string{queries, schema, main} {
		s.publish(s.notify, pathToURI(path))
	}
	check("edited")
	for _, p := range panics {
		t.Errorf("the server recovered a panic: %s", p)
	}
}
