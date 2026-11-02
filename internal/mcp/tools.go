package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/format"
)

// tool is one MCP tool: its name, what a model reads about it, its input
// schema, and what runs it.
type tool struct {
	name        string
	description string
	schema      map[string]any
	run         func(ctx context.Context, s *Server, args json.RawMessage) (map[string]any, error)
}

func object(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

var positionProps = map[string]any{
	"file": prop("string", "The .nomi file."),
	"line": prop("integer", "1-based line."),
	"col":  prop("integer", "1-based byte column, as `nomi check` prints it."),
}

var tools = []tool{
	{
		name: "check",
		description: "Check a .nomi file or a directory as `nomi check` does, without running anything. " +
			"Answers each diagnostic with its span, message, hints and, where the compiler knows one, a fix as text edits; " +
			"each file's status; and a summary. Run it after every edit.",
		schema: object([]string{"path"}, map[string]any{"path": prop("string", "A .nomi file or a directory of them.")}),
		run:    runCheck,
	},
	{
		name: "type_at",
		description: "The type and documentation at a position, as an editor's hover shows them: " +
			"a binding's or expression's type, a function's signature and doc comment.",
		schema: object([]string{"file", "line", "col"}, positionProps),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (map[string]any, error) {
			return s.atPosition(ctx, args, "textDocument/hover")
		},
	},
	{
		name:        "definition",
		description: "Where the name at a position is declared. A stdlib declaration answers a file under the std cache.",
		schema:      object([]string{"file", "line", "col"}, positionProps),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (map[string]any, error) {
			return s.atPosition(ctx, args, "textDocument/definition")
		},
	},
	{
		name:        "references",
		description: "Every use of the name at a position across the program, its declaration included.",
		schema:      object([]string{"file", "line", "col"}, positionProps),
		run: func(ctx context.Context, s *Server, args json.RawMessage) (map[string]any, error) {
			return s.atPosition(ctx, args, "textDocument/references")
		},
	},
	{
		name: "search_std",
		description: "Search the standard library's public functions. A query with `->` is a type signature, " +
			"such as `String, String -> List<String>` or `List<T>, (T) -> Bool -> Maybe<T>`: parameters match in any order, " +
			"and a single capital letter stands for any type. Any other query matches names (`trim`, `Map.get`) and doc text. " +
			"Each result is the function as a program calls it (`String.split`, `io.print`), its signature and its first doc paragraph.",
		schema: object([]string{"query"}, map[string]any{
			"query": prop("string", "A name, a word from the docs, or a signature with `->`."),
			"limit": prop("integer", "At most this many results; 20 by default."),
		}),
		run: func(_ context.Context, _ *Server, args json.RawMessage) (map[string]any, error) {
			var a struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return map[string]any{"results": searchStd(a.Query, a.Limit)}, nil
		},
	},
	{
		name: "run_tests",
		description: "Run a file's or a directory's `test` blocks as `nomi test` does. " +
			"Answers each test with its outcome and any failure, and a summary. `line` runs only the test at that line.",
		schema: object([]string{"path"}, map[string]any{
			"path": prop("string", "A .nomi file or a directory."),
			"line": prop("integer", "Run only the test whose block holds this line."),
		}),
		run: runTests,
	},
	{
		name: "format",
		description: "Format Nomi source as `nomi fmt` does. Give `source` to get the formatted text back, " +
			"or `file` to get the formatted text and whether it differs from the file. Never writes the file.",
		schema: object(nil, map[string]any{
			"source": prop("string", "Nomi source text."),
			"file":   prop("string", "A .nomi file to format."),
		}),
		run: runFormat,
	},
	{
		name:        "explain",
		description: "Explain a diagnostic's `code`, as `check` reports it (`unused-import`, `unreachable-code`, ...). With no code, lists them.",
		schema:      object(nil, map[string]any{"code": prop("string", "A diagnostic code.")}),
		run:         runExplain,
	},
}

// jsonLines reads the JSON Lines `nomi check` and `nomi test` write into
// lists keyed by each record's type made plural (`diagnostics`, `files`,
// `tests`), and `summary`.
func jsonLines(out []byte) (map[string]any, error) {
	result := map[string]any{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("reading nomi's output: %w: %s", err, line)
		}
		kind, _ := rec["type"].(string)
		delete(rec, "type")
		if kind == "summary" {
			result["summary"] = rec
			continue
		}
		key := kind + "s"
		list, _ := result[key].([]any)
		result[key] = append(list, rec)
	}
	return result, sc.Err()
}

func runCheck(ctx context.Context, s *Server, args json.RawMessage) (map[string]any, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, fmt.Errorf("check needs a path")
	}
	out, exit, err := s.opts.RunCLI(ctx, s.opts.Root, "check", "--format", "json", a.Path)
	if err != nil {
		return nil, err
	}
	result, err := jsonLines(out)
	if err != nil {
		return nil, err
	}
	if _, ok := result["diagnostics"]; !ok {
		result["diagnostics"] = []any{}
	}
	result["ok"] = exit == 0
	return result, nil
}

func runTests(ctx context.Context, s *Server, args json.RawMessage) (map[string]any, error) {
	var a struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, fmt.Errorf("run_tests needs a path")
	}
	cliArgs := []string{"test", a.Path, "--format", "json"}
	if a.Line > 0 {
		cliArgs = append(cliArgs, "--line", strconv.Itoa(a.Line))
	}
	out, exit, err := s.opts.RunCLI(ctx, s.opts.Root, cliArgs...)
	if err != nil {
		return nil, err
	}
	result, err := jsonLines(out)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 && exit != 0 {
		// The program did not load: nomi test said why on stderr, which
		// `nomi check` reports as diagnostics.
		return nil, fmt.Errorf("nomi test failed before running a test (exit %d); run the check tool on %s for the reason", exit, a.Path)
	}
	result["ok"] = exit == 0
	return result, nil
}

func runFormat(_ context.Context, s *Server, args json.RawMessage) (map[string]any, error) {
	var a struct {
		Source *string `json:"source"`
		File   string  `json:"file"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	var src string
	switch {
	case a.Source != nil:
		src = *a.Source
	case a.File != "":
		data, err := os.ReadFile(s.path(a.File))
		if err != nil {
			return nil, err
		}
		src = string(data)
	default:
		return nil, fmt.Errorf("format needs source or file")
	}
	formatted, err := format.Format(src)
	if err != nil {
		return nil, err
	}
	return map[string]any{"formatted": formatted, "changed": formatted != src}, nil
}

func (s *Server) path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.opts.Root, p)
}

// atPosition asks the language server method at a file position.
func (s *Server) atPosition(ctx context.Context, args json.RawMessage, method string) (map[string]any, error) {
	var a struct {
		File string `json:"file"`
		Line int    `json:"line"`
		Col  int    `json:"col"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.File == "" {
		return nil, fmt.Errorf("a file is needed")
	}
	c, err := s.language()
	if err != nil {
		return nil, err
	}
	uri, text, err := c.sync(ctx, s.path(a.File))
	if err != nil {
		return nil, err
	}
	pos, err := toLSP(text, a.Line, a.Col)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}
	if method == "textDocument/references" {
		params["context"] = map[string]any{"includeDeclaration": true}
	}
	var raw json.RawMessage
	if err := c.conn.Call(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	if method != "textDocument/hover" {
		locs, err := locations(raw)
		if err != nil {
			return nil, err
		}
		return map[string]any{"locations": locs}, nil
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{"found": false, "text": ""}, nil
	}
	if err := json.Unmarshal(raw, &hover); err != nil {
		return nil, err
	}
	return map[string]any{"found": true, "text": hoverText(hover.Contents)}, nil
}

// hoverText reads hover contents: MarkupContent, a string, or a list of
// MarkedStrings.
func hoverText(raw json.RawMessage) string {
	var markup struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &markup) == nil && markup.Value != "" {
		return markup.Value
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			parts = append(parts, hoverText(item))
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func runExplain(_ context.Context, _ *Server, args json.RawMessage) (map[string]any, error) {
	var a struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Code == "" {
		codes := make([]string, 0, len(explanations))
		for c := range explanations {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		return map[string]any{"codes": codes}, nil
	}
	text, ok := explanations[a.Code]
	if !ok {
		return nil, fmt.Errorf("no diagnostic has the code %q; call explain with no code for the list", a.Code)
	}
	return map[string]any{"code": a.Code, "explanation": text}, nil
}
