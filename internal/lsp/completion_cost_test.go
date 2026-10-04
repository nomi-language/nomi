package lsp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// describeContext renders what a classification found, comparable across
// two reparses of one text: its scalars, and the node types, fields and
// indexes on the path to the sentinel.
func describeContext(ctx completionContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kind=%d prefix=%q word=%d-%d paren=%v with=%v clock=%v qual=%q slot=%d param=%q branch=%d importSeg=%d accSeg=%d",
		ctx.kind, ctx.prefix, ctx.start, ctx.end, ctx.nextIsParen, ctx.withTarget, ctx.groupClock,
		ctx.typeQualifier, ctx.paramSlot, ctx.paramName, ctx.branch, ctx.importSeg, ctx.accessorSeg)
	fmt.Fprintf(&b, " object=%T pipe=%T lit=%v case=%v import=%v acc=%v stub=%v",
		ctx.object, ctx.pipeLHS, ctx.structLit != nil, ctx.caseNode != nil, ctx.importStmt != nil, ctx.accessor != nil, ctx.implStub != nil)
	var has []string
	for k := range ctx.groupHas {
		has = append(has, k)
	}
	sort.Strings(has)
	fmt.Fprintf(&b, " groupHas=%v", has)
	if ctx.hit != nil {
		fmt.Fprintf(&b, " hit=%s:", ctx.hit.field)
		for _, s := range ctx.hit.path {
			t := "?"
			if s.v.IsValid() {
				t = s.v.Type().String()
			}
			fmt.Fprintf(&b, " %s.%s[%d]", t, s.field, s.index)
		}
	}
	return b.String()
}

// TestClassifyCompletion_WalkAtCursorAgrees classifies positions across
// the test corpus, in each file as written and with the rest of the
// cursor's line deleted (a construct being typed, often unclosed), once
// walking each reparse whole and once walking only the declarations at
// the cursor, and requires the same context from both.
func TestClassifyCompletion_WalkAtCursorAgrees(t *testing.T) {
	var files []string
	err := filepath.WalkDir(filepath.Join("..", "..", "tests"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".nomi") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found %d corpus files", len(files))
	}
	checked := 0
	for i, path := range files {
		if i%3 != 0 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		offs := lineOffsets(content)
		// Eight lines spread over the file: after the line's first word,
		// and at the line's end.
		for k := 1; k <= 8; k++ {
			line := len(offs) * k / 9
			start := offs[line]
			end := len(content)
			if line+1 < len(offs) {
				end = offs[line+1] - 1
			}
			word := start
			for word < end && (content[word] == ' ' || content[word] == '\t') {
				word++
			}
			for word < end && isWordByte(content[word]) {
				word++
			}
			for _, text := range []struct {
				content string
				off     int
			}{
				{content, word},
				{content, end},
				{content[:word] + content[end:], word},
			} {
				whole := describeContext(classifyCompletionWalking(text.content, text.off, false))
				atCursor := describeContext(classifyCompletionWalking(text.content, text.off, true))
				if whole != atCursor {
					t.Errorf("%s offset %d:\nwhole:     %s\nat cursor: %s", path, text.off, whole, atCursor)
				}
				checked++
			}
		}
	}
	t.Logf("checked %d positions in %d files", checked, (len(files)+2)/3)
}

// countNodes counts the struct values a findSentinel walk would visit
// from roots.
func countNodes(roots []ast.Node) int {
	n := 0
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Ptr:
			if v.IsNil() || seen[v.Pointer()] || v.Elem().Kind() != reflect.Struct {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem())
		case reflect.Struct:
			n++
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				walk(v.Index(i))
			}
		}
	}
	for _, r := range roots {
		walk(reflect.ValueOf(r))
	}
	return n
}

// TestCompletion_WorkIsBoundedAtTheCursor counts the work of a completion
// request in the generated file of 10 and of 300 functions: the sentinel
// walk visits the same number of nodes in both, the request converts
// positions through the cached line index and never scans the text from
// its top.
func TestCompletion_WorkIsBoundedAtTheCursor(t *testing.T) {
	visited := map[int]int{}
	for _, n := range []int{10, 300} {
		content, pos := splitCursor(t, generatedFile(n)+lspProbe)
		off := posToOffset(lineOffsets(content), int(pos.Line)+1, int(pos.Character)+1)
		spliced := content[:off] + completionSentinel + content[off:]
		roots, _, _ := parser.ParseResilient(lexer.Lex(spliced))
		visited[n] = countNodes(sentinelRoots(roots, int(pos.Line)+1))
		if whole := countNodes(roots); visited[n]*10 > whole {
			t.Errorf("%d functions: the walk at the cursor visits %d of %d nodes", n, visited[n], whole)
		}

		s, uri := openProject(t, map[string]string{"main.nomi": content}, "main.nomi")
		params := &protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		}}
		scans := nthLineScans.Load()
		for range 3 {
			res, err := s.textDocumentCompletion(nil, params)
			if err != nil {
				t.Fatal(err)
			}
			if items := completionItemsOf(t, res); !hasLabel(items, "items") {
				t.Fatalf("%d functions: completion after `it` lacks `items`", n)
			}
		}
		if d := nthLineScans.Load() - scans; d != 0 {
			t.Errorf("%d functions: 3 requests scanned the text from the top %d times, want 0", n, d)
		}
		if s.lines.builds != 1 {
			t.Errorf("%d functions: built the line index %d times, want once", n, s.lines.builds)
		}
	}
	if visited[10] != visited[300] {
		t.Errorf("the walk at the cursor visits %d nodes in 10 functions and %d in 300, want the same", visited[10], visited[300])
	}
}

func hasLabel(items []protocol.CompletionItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}
