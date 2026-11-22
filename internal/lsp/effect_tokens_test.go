package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/fileuri"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const effectTokensMain = `import
    std::io
    store
end

fn twice(n: Int) -> Int
    n * 2
end

fn read_settings(path: String) -> String
    store.load(path)
end

fn load_config() -> String
    read_settings("settings.txt")
end

fn apply(f: (String) -> String, path: String) -> String
    f(path)
end

fn main()
    io.print(load_config())
    io.print(apply(read_settings, "other.txt"))
    io.print("${twice(2)}")
end
`

const effectTokensStore = `import std::fs

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`

// effectAt reports whether a semantic token starts at word, inside the
// first occurrence of needle in text, and carries the effect modifier.
func effectAt(t *testing.T, toks []decodedSemanticToken, text, needle, word string) bool {
	t.Helper()
	i := strings.Index(text, needle)
	j := strings.Index(needle, word)
	if i < 0 || j < 0 {
		t.Fatalf("%q is not in the text, or %q not in it", needle, word)
	}
	at := i + j
	line := strings.Count(text[:at], "\n")
	col := at - (strings.LastIndex(text[:at], "\n") + 1)
	for _, tok := range toks {
		if tok.line == line && tok.col == col {
			return tok.modifiers&modEffect != 0
		}
	}
	return false
}

// semanticTokensOf is the server's semantic tokens of uri, decoded.
func semanticTokensOf(t *testing.T, c *rpcClient, uri string) []decodedSemanticToken {
	t.Helper()
	var got protocol.SemanticTokens
	if err := c.conn.Call(context.Background(), "textDocument/semanticTokens/full", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	}, &got); err != nil {
		t.Fatal(err)
	}
	return decodeSemanticTokenData(got.Data)
}

// The callee of each call the compiler cannot show is pure carries the
// effect modifier once the lowering run has finished, as the hover there
// says "Has effects": a direct effect call, a call of the program's own
// function that reaches one through others, a call passed a function that
// acts, and such a function's name in its declaration, in the entry and in
// another file of its program. A qualifier before the name, a function
// named as a value, a call through a function value (which the walk counts
// where the value is made) and a call that has no effects carry none. A
// finished run asks the editor for the tokens again.
func TestSemanticTokens_EffectModifier(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{
		"main.nomi":  effectTokensMain,
		"store.nomi": effectTokensStore,
		"nomi.toml":  "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	s, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectTokensMain, "store.nomi": effectTokensStore})
	mainURI := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	storeURI := fileuri.FromPath(filepath.Join(dir, "store.nomi"))
	waitReachAt(t, s, c, mainURI, effectTokensMain, "print(load_config", "Has effects: `std::io`.")
	waitReachAt(t, s, c, storeURI, effectTokensStore, "read_text", "Has effects: `std::fs`.")
	mainToks := semanticTokensOf(t, c, mainURI)
	storeToks := semanticTokensOf(t, c, storeURI)
	for _, tc := range []struct {
		toks         []decodedSemanticToken
		text         string
		needle, word string
		want         bool
		what         string
	}{
		{mainToks, effectTokensMain, "io.print(load_config", "print", true, "a direct effect call"},
		{mainToks, effectTokensMain, "io.print(load_config", "io", false, "the qualifier of an effect call"},
		{mainToks, effectTokensMain, "print(load_config()", "load_config", true, "a call of an own function that has effects"},
		{mainToks, effectTokensMain, "fn load_config", "load_config", true, "the declaration of an own function that has effects"},
		{mainToks, effectTokensMain, "apply(read_settings", "read_settings", false, "a function used as a value"},
		{mainToks, effectTokensMain, "apply(read_settings", "apply", true, "a call passed a function that acts"},
		{mainToks, effectTokensMain, "f(path)", "f", false, "a call through a function value"},
		{mainToks, effectTokensMain, "fn apply", "apply", false, "a function whose calls run only what it is passed"},
		{mainToks, effectTokensMain, "store.load(path)", "load", true, "a call of another file's function"},
		{mainToks, effectTokensMain, "${twice(2)", "twice", false, "a pure call"},
		{mainToks, effectTokensMain, "fn twice", "twice", false, "a pure function's declaration"},
		{storeToks, effectTokensStore, "fs.read_text", "read_text", true, "a direct effect call in a file other than the entry"},
		{storeToks, effectTokensStore, "fs.read_text", "fs", false, "a qualifier in a file other than the entry"},
		{storeToks, effectTokensStore, "pub fn load", "load", true, "a declaration in a file other than the entry"},
	} {
		if got := effectAt(t, tc.toks, tc.text, tc.needle, tc.word); got != tc.want {
			t.Errorf("%s (%q in %q): effect is %v, want %v", tc.what, tc.word, tc.needle, got, tc.want)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for c.refreshes.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no workspace/semanticTokens/refresh after the lowering run finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// effectTokensUnreached's main reaches nothing but io.print: backup and
// its caller restore are functions just written, which nothing calls yet.
const effectTokensUnreached = `import
    std::fs
    std::io
end

fn backup(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn restore() -> String
    backup("notes.txt")
end

fn main()
    io.print("hello")
end
`

// A function nothing reaches is marked as one main reaches is, and its
// hover says so: the editor's walk starts from every function the program
// holds (vmhost's editorWalks), while the lists stay held where the
// program runs.
func TestSemanticTokens_EffectModifierOnFunctionsNothingReaches(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{
		"main.nomi": effectTokensUnreached,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	s, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectTokensUnreached})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	waitReachAt(t, s, c, uri, effectTokensUnreached, "read_text", "Has effects: `std::fs`.")
	waitReachAt(t, s, c, uri, effectTokensUnreached, "backup(\"notes", "Has effects: `std::fs`.")
	waitReachAt(t, s, c, uri, effectTokensUnreached, "restore", "Has effects: `std::fs` through `backup`.")
	toks := semanticTokensOf(t, c, uri)
	for _, tc := range []struct {
		needle, word string
		what         string
	}{
		{"fs.read_text", "read_text", "an effect call nothing reaches"},
		{"fn backup", "backup", "the declaration of a function nothing reaches"},
		{"backup(\"notes", "backup", "a call of it in another function nothing reaches"},
		{"fn restore", "restore", "the declaration of that function"},
		{"io.print(\"hello", "print", "an effect call main reaches"},
	} {
		if !effectAt(t, toks, effectTokensUnreached, tc.needle, tc.word) {
			t.Errorf("%s (%q in %q) carries no effect modifier", tc.what, tc.word, tc.needle)
		}
	}
}

// Before any lowering run there is nothing to mark, and a server whose
// lowering is off marks nothing, so the modifier never appears without
// the run that found it.
func TestSemanticTokens_NoEffectModifierWithoutALowering(t *testing.T) {
	saved := loweringChecksEnabled
	loweringChecksEnabled = false
	t.Cleanup(func() { loweringChecksEnabled = saved })
	dir := writeProject(t, map[string]string{
		"main.nomi":  effectTokensMain,
		"store.nomi": effectTokensStore,
		"nomi.toml":  "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	_, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectTokensMain})
	mainURI := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	c.waitDiagnostics(mainURI)
	toks := semanticTokensOf(t, c, mainURI)
	if len(toks) == 0 {
		t.Fatal("no semantic tokens")
	}
	for _, tok := range toks {
		if tok.modifiers&modEffect != 0 {
			t.Errorf("token at %d:%d carries the effect modifier with no lowering", tok.line+1, tok.col+1)
		}
	}
}

// effectTokensStreams writes through io.stdout and io.stderr, std's `once`
// values whose initializers ask for the standard streams: report and
// Writer.print act on the handle passed to them.
const effectTokensStreams = `import std::io.{self, Error, Writer}

fn report(to: Writer, count: Int) -> Result<Unit, Error>
    to |> Writer.print("${count} items")
end

fn main() -> Result<Unit, Error>
    report(io.stdout, 3)?
    report(io.stderr, 0)?
    Ok(Unit)
end
`

// effectTokensOnces declares a once whose initializer reads a file, one
// whose initializer reads that one, and a pure one, and reads an app
// field, which is no effect.
const effectTokensOnces = `import
    std::fs
    std::io
end

struct App
    name: String
end

fn boot() -> Result<App, String>
    Ok(App{name: "Ada"})
end

once settings: String = fs.read_text("settings.txt") |> Result.with_default("")

once banner: String = "${settings}!"

once limit = 6

fn main()
    io.print(banner)
    io.print("${limit} ${App.name}")
end
`

// markedWords is the text of each token of toks that carries the effect
// modifier, as `line:word`, in order.
func markedWords(toks []decodedSemanticToken, text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, tok := range toks {
		if tok.modifiers&modEffect != 0 {
			out = append(out, fmt.Sprintf("%d:%s", tok.line+1, lines[tok.line][tok.col:tok.col+tok.length]))
		}
	}
	return out
}

// A token is marked where the compiler cannot show a call is pure: a call
// that acts, through a handle it is passed too, and the name of a function
// whose body holds one. A read of a `once`, whose hover still names the
// effect of its initializer, is a value and is not marked, and neither is
// a once's name, a parameter, a pure once or an app field read; the call
// in a once's initializer is.
func TestSemanticTokens_EffectModifierOnOnceReads(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	for _, tc := range []struct {
		name, text, needle, hover string
		want                      []string
	}{
		{"std streams", effectTokensStreams, "stdout", "Has effects: `std::io`.", []string{"3:report", "4:print", "7:main", "8:report", "9:report"}},
		{"own onces", effectTokensOnces, "banner)", "Has effects: `std::fs` through `once settings`.",
			[]string{"14:read_text", "20:main", "21:print", "22:print"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeProject(t, map[string]string{
				"main.nomi": tc.text,
				"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
			})
			s, c := startEffectServer(t, dir, map[string]string{"main.nomi": tc.text})
			uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
			waitReachAt(t, s, c, uri, tc.text, tc.needle, tc.hover)
			if got := markedWords(semanticTokensOf(t, c, uri), tc.text); !slices.Equal(got, tc.want) {
				t.Errorf("the tokens with the effect modifier are %q, want %q", got, tc.want)
			}
		})
	}
}

// The program of the effects documentation: report and Writer.print act
// on the writer report is passed, so each call of report, report's name
// and main's are marked, and a hover on each says so; the reads of the
// streams and the parameter are not marked.
func TestSemanticTokens_EffectModifierOnHeldCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{
		"main.nomi": effectTokensStreams,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	s, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectTokensStreams})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	const held = "Has effects on what it is passed."
	for _, at := range []struct{ needle, want string }{
		{"report(to", held}, {"Writer.print", held}, {"print(\"", held}, {"report(io.stdout", held},
		{"report(io.stderr", held}, {"main()", "Has effects: `std::io`."}, {"stdout", "Has effects: `std::io`."},
		{"stderr", "Has effects: `std::io`."},
	} {
		waitReachAt(t, s, c, uri, effectTokensStreams, at.needle, at.want)
	}
	waitReachAt(t, s, c, uri, effectTokensStreams, "to |>")
	want := []string{"3:report", "4:print", "7:main", "8:report", "9:report"}
	if got := markedWords(semanticTokensOf(t, c, uri), effectTokensStreams); !slices.Equal(got, want) {
		t.Errorf("the tokens with the effect modifier are %q, want %q", got, want)
	}
}
