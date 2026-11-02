package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const fixture = `fn double(n: Int) -> Int {
    n * 2
}

fn main() {
    answer = double(21)
    dbg answer
}
`

// client drives a Server over in-memory pipes, as an MCP client does over
// stdio.
type client struct {
	t   *testing.T
	w   io.Writer
	r   *bufio.Scanner
	id  atomic.Int64
	srv *Server
}

func start(t *testing.T, opts Options) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := New(opts)
	done := make(chan struct{})
	go func() {
		s.Serve(context.Background(), inR, outW)
		outW.Close()
		close(done)
	}()
	t.Cleanup(func() {
		inW.Close()
		<-done
	})
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &client{t: t, w: inW, r: sc, srv: s}
}

func (c *client) call(method string, params any) map[string]any {
	c.t.Helper()
	id := c.id.Add(1)
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	if !c.r.Scan() {
		c.t.Fatalf("%s: no reply: %v", method, c.r.Err())
	}
	var reply map[string]any
	if err := json.Unmarshal(c.r.Bytes(), &reply); err != nil {
		c.t.Fatalf("%s: %v: %s", method, err, c.r.Bytes())
	}
	if reply["error"] != nil {
		c.t.Fatalf("%s: %v", method, reply["error"])
	}
	return reply["result"].(map[string]any)
}

// tool calls a tool and answers its structured result, failing the test
// when the tool reports an error.
func (c *client) tool(name string, args map[string]any) map[string]any {
	c.t.Helper()
	res := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if res["isError"] == true {
		c.t.Fatalf("%s: %v", name, res["content"])
	}
	return res["structuredContent"].(map[string]any)
}

func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.nomi"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMCP_InitializeAndListTools(t *testing.T) {
	c := start(t, Options{Root: t.TempDir()})
	init := c.call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}})
	if init["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want the client's 2025-03-26", init["protocolVersion"])
	}
	if got := c.call("initialize", map[string]any{"protocolVersion": "1999-01-01"})["protocolVersion"]; got != protocolVersions[0] {
		t.Errorf("an unknown revision answered %v, want %s", got, protocolVersions[0])
	}
	var names []string
	for _, tl := range c.call("tools/list", nil)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	want := []string{"check", "type_at", "definition", "references", "search_std", "run_tests", "format", "explain"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

func TestMCP_LanguageServerTools(t *testing.T) {
	dir := project(t)
	c := start(t, Options{Root: dir})
	c.call("initialize", map[string]any{})

	// `answer` on line 7: its type.
	hover := c.tool("type_at", map[string]any{"file": "main.nomi", "line": 7, "col": 9})
	if text, _ := hover["text"].(string); !strings.Contains(text, "Int") {
		t.Errorf("type_at answer = %q, want it to name Int", text)
	}

	// `double` in `double(21)`, line 6 column 14: declared on line 1.
	def := c.tool("definition", map[string]any{"file": "main.nomi", "line": 6, "col": 14})
	locs := def["locations"].([]any)
	if len(locs) != 1 || locs[0].(map[string]any)["line"] != float64(1) {
		t.Fatalf("definition = %v, want one location on line 1", def)
	}
	if file := locs[0].(map[string]any)["file"].(string); filepath.Base(file) != "main.nomi" {
		t.Errorf("definition file = %s", file)
	}

	refs := c.tool("references", map[string]any{"file": "main.nomi", "line": 1, "col": 4})
	if n := len(refs["locations"].([]any)); n != 2 {
		t.Errorf("references = %v, want the declaration and the call", refs)
	}

	// An edit on disk is seen by the next call.
	edited := strings.Replace(fixture, "answer = double(21)", "answer = \"text\"", 1)
	if err := os.WriteFile(filepath.Join(dir, "main.nomi"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	hover = c.tool("type_at", map[string]any{"file": "main.nomi", "line": 7, "col": 9})
	if text, _ := hover["text"].(string); !strings.Contains(text, "String") {
		t.Errorf("after the edit, type_at answer = %q, want it to name String", text)
	}

	bad := c.call("tools/call", map[string]any{"name": "type_at", "arguments": map[string]any{"file": "main.nomi", "line": 99, "col": 1}})
	if bad["isError"] != true {
		t.Errorf("a line outside the file answered %v, want an error result", bad)
	}
}

func TestMCP_SearchStd(t *testing.T) {
	c := start(t, Options{Root: t.TempDir()})
	names := func(query string) []string {
		var out []string
		for _, r := range c.tool("search_std", map[string]any{"query": query, "limit": 50})["results"].([]any) {
			out = append(out, r.(map[string]any)["name"].(string))
		}
		return out
	}
	for _, tc := range []struct{ query, want string }{
		{"trim", "String.trim"},
		{"io.print", "io.print"},
		{"String, String -> List<String>", "String.split"},
		// The parameters in either order.
		{"String, Map<String, Int> -> Maybe<Int>", "Map.get"},
		{"K, Map<K, V> -> Maybe<V>", "Map.get"},
		// A concrete type instantiates a generic one.
		{"List<Int> -> Maybe<Int>", "List.head"},
		{"List<T> -> Maybe<T>", "List.head"},
		// A List is an Iter (std's impl), and a defaulted parameter may be
		// left out (String.join's separator).
		{"List<T> -> Int", "Iter.count"},
		{"List<String> -> String", "String.join"},
		{"Set<Int>, (Int) -> Bool -> Bool", "Iter.any"},
		// A result that implements the interface asked for.
		{"List<T> -> Iter<T>", "Iter.reverse"},
	} {
		if got := names(tc.query); !slices.Contains(got, tc.want) {
			t.Errorf("search_std(%q) = %v, want it to include %s", tc.query, got, tc.want)
		}
	}
	// Exact and instantiating matches rank before ones through an impl.
	if got := names("List<T> -> Maybe<T>"); len(got) == 0 || got[0] != "List.head" {
		t.Errorf("search_std(List<T> -> Maybe<T>) = %v, want List.head first", got)
	}
	// A bounded type variable takes only a type that implements its bound.
	if got := names("Int -> Int"); slices.Contains(got, "Iter.count") {
		t.Errorf("search_std(Int -> Int) = %v, want no Iter function: Int is no Iter", got)
	}
	if got := names("Int -> NoSuchType"); len(got) != 0 {
		t.Errorf("search_std for a type nothing returns = %v, want none", got)
	}
}

func TestMCP_FormatAndExplain(t *testing.T) {
	dir := project(t)
	c := start(t, Options{Root: dir})
	got := c.tool("format", map[string]any{"source": "fn main() {\n  dbg   1\n}\n"})
	if got["formatted"] != "fn main() {\n    dbg 1\n}\n" || got["changed"] != true {
		t.Errorf("format = %v", got)
	}
	if got := c.tool("format", map[string]any{"file": "main.nomi"}); got["changed"] != false {
		t.Errorf("format of a formatted file = %v, want changed false", got)
	}
	if got := c.tool("explain", map[string]any{"code": "unused-import"}); !strings.Contains(got["explanation"].(string), "import") {
		t.Errorf("explain = %v", got)
	}
	if codes := c.tool("explain", nil)["codes"].([]any); len(codes) != len(explanations) {
		t.Errorf("explain with no code listed %d codes, want %d", len(codes), len(explanations))
	}
}

// check and run_tests run the CLI and read its JSON Lines.
func TestMCP_CheckAndRunTestsReadTheCLI(t *testing.T) {
	var calls [][]string
	c := start(t, Options{Root: t.TempDir(), RunCLI: func(_ context.Context, _ string, args ...string) ([]byte, int, error) {
		calls = append(calls, args)
		if args[0] == "check" {
			return []byte(`{"type":"diagnostic","file":"a.nomi","line":2,"col":5,"message":"Nomi has no ` + "`let`" + `","fix":{"title":"Remove let","edits":[]}}` + "\n" +
				`{"type":"file","file":"a.nomi","status":"failed"}` + "\n" +
				`{"type":"summary","files":1,"failed":1,"diagnostics":1}` + "\n"), 1, nil
		}
		return []byte(`{"type":"test","file":"a.nomi","line":3,"name":"adds","status":"passed"}` + "\n" +
			`{"type":"summary","passed":1,"failed":0,"blocked":0}` + "\n"), 0, nil
	}})
	check := c.tool("check", map[string]any{"path": "a.nomi"})
	if check["ok"] != false || len(check["diagnostics"].([]any)) != 1 || len(check["files"].([]any)) != 1 || check["summary"] == nil {
		t.Errorf("check = %v", check)
	}
	tests := c.tool("run_tests", map[string]any{"path": "a.nomi", "line": 3})
	if tests["ok"] != true || len(tests["tests"].([]any)) != 1 {
		t.Errorf("run_tests = %v", tests)
	}
	want := [][]string{{"check", "--format", "json", "a.nomi"}, {"test", "a.nomi", "--format", "json", "--line", "3"}}
	if !slices.EqualFunc(calls, want, slices.Equal) {
		t.Errorf("nomi ran with %v, want %v", calls, want)
	}
}

// Every code a diagnostic can carry has an explanation.
func TestEveryDiagnosticCodeIsExplained(t *testing.T) {
	code := regexp.MustCompile(`Code\s*=\s*"([a-z-]+)"`)
	seen := map[string]bool{}
	for _, dir := range []string{"..", "../../vmhost"} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range code.FindAllStringSubmatch(string(data), -1) {
				seen[m[1]] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) == 0 {
		t.Fatal("found no diagnostic codes; the pattern no longer matches how they are declared")
	}
	for c := range seen {
		if _, ok := explanations[c]; !ok {
			t.Errorf("diagnostic code %q has no explanation in explanations", c)
		}
	}
	for c := range explanations {
		if !seen[c] {
			t.Errorf("explanations has %q, which no diagnostic declares", c)
		}
	}
}
