package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/std"
)

// arityDiagnostic matches the generic-arity rule's own diagnostics and nothing
// else. The BACKTICKED name is the discriminator: the two pre-existing arity
// messages in the tree are spelled without backticks — `List expects 1 type
// argument, got 2` from ResolveTypeExpr's compiler-known types, and
// `Iter expects 1 type argument(s), got 2` from the impl-header check in
// recordImplTypeArgsFromBlock. TestGenericArity_TreeIsClean plants a positive
// and requires this pattern to match it, so the pattern and the message cannot
// drift apart silently.
var arityDiagnostic = regexp.MustCompile("^`[^`]+` expects [0-9]+ type arguments?, got [0-9]+$")

// plantedUnderApplication is a program that under-applies a generic in a
// declaration position. Used to prove the census instrument is not blind.
const plantedUnderApplication = `struct Box<T> {
  inner: T
}

fn take(b: Box): Int {
  b.inner
}
`

// TestGenericArity_TreeIsClean is BOTH the blast-radius census for the
// generic-arity rule and the standing guard that the tree stays clean.
//
// It runs the production pipeline — analysis.DocumentManager, which is what the
// LSP opens a file with and which folds BuildTypes, CheckTypes, the concurrency
// passes and FinalizeCoherence into one TypeErrors slice — over every `.nomi`
// file in std/ and tests/, plus every fenced Nomi block in the tour.
// Only this rule's diagnostics are counted, so a file that carries
// other diagnostics for its own reasons does not pollute the count.
//
// Before the rule was written, an instrument that
// recorded every site instead of rejecting it: 87 sites across the four trees,
// and EVERY ONE of them was either an `impl` interface header (34) or a struct
// literal's type name (53) — the two positions the rule deliberately exempts.
// In the positions it does police the count was zero, which is why the rule
// could be written without editing a single shipping file.
//
// Run with -v for the by-bucket table.
func TestGenericArity_TreeIsClean(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	lib := std.Load()
	newManager := func(workspace string) *analysis.DocumentManager {
		dm := analysis.NewDocumentManager()
		dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
		dm.SetWorkspaceRoot(workspace)
		return dm
	}

	type hit struct {
		bucket string
		where  string
		msg    string
	}
	var hits []hit
	counts := map[string]int{}
	scanned := map[string]int{}

	record := func(bucket, where string, errs []analysis.TypeError) {
		scanned[bucket]++
		for _, e := range errs {
			if !arityDiagnostic.MatchString(e.Message) {
				continue
			}
			counts[bucket]++
			hits = append(hits, hit{bucket: bucket, where: fmt.Sprintf("%s:%d:%d", where, e.Line, e.Col), msg: e.Message})
		}
	}

	// --- the instrument is not blind -------------------------------------
	// A zero below means nothing unless this same pipeline, pointed at a file
	// that DOES under-apply a generic, reports it and this pattern matches.
	func() {
		dir := t.TempDir()
		path := filepath.Join(dir, "main.nomi")
		if err := os.WriteFile(path, []byte(plantedUnderApplication), 0o644); err != nil {
			t.Fatal(err)
		}
		doc := newManager(dir).Open("file://"+path, plantedUnderApplication)
		if doc == nil || doc.Analysis == nil {
			t.Fatal("planted positive did not analyze")
		}
		found := false
		for _, e := range doc.Analysis.TypeErrors {
			if arityDiagnostic.MatchString(e.Message) {
				found = true
				t.Logf("planted positive matched: line %d col %d: %s", e.Line, e.Col, e.Message)
			}
		}
		if !found {
			t.Fatalf("the census instrument is BLIND: the planted under-application produced no matching diagnostic; got %v", doc.Analysis.TypeErrors)
		}
	}()

	// --- file trees ------------------------------------------------------
	for _, bucket := range []string{"std", "tests"} {
		dir := filepath.Join(root, bucket)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".nomi") {
				return err
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			dm := newManager(projectRootFor(root, path))
			doc := dm.Open("file://"+path, string(content))
			if doc == nil || doc.Analysis == nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			record(bucket, rel, doc.Analysis.TypeErrors)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", bucket, err)
		}
	}

	// --- tour ------------------------------------------------------------
	tourRoot := filepath.Join(root, "tour", "src", "content", "docs")
	tmp := t.TempDir()
	blockN := 0
	err = filepath.WalkDir(tourRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".mdx") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		for _, fence := range []struct{ lang, bucket string }{
			{"nomi-run", "tour (nomi-run, executed)"},
			{"nomi", "tour (nomi, highlight-only)"},
		} {
			for _, b := range doctest.ExtractBlocks(string(data), fence.lang) {
				if b.HasInfo("ignore") {
					continue
				}
				blockN++
				dir := filepath.Join(tmp, "block", strconv.Itoa(blockN))
				entry, writeErr := writeTourBlock(dir, b.Code)
				if writeErr != nil {
					t.Fatalf("stage tour block %s:L%d: %v", rel, b.Line, writeErr)
				}
				content, readErr := os.ReadFile(entry)
				if readErr != nil {
					t.Fatal(readErr)
				}
				doc := newManager(dir).Open("file://"+entry, string(content))
				if doc == nil || doc.Analysis == nil {
					continue
				}
				record(fence.bucket, rel+":L"+strconv.Itoa(b.Line), doc.Analysis.TypeErrors)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk tour: %v", err)
	}

	// --- report ----------------------------------------------------------
	buckets := make([]string, 0, len(scanned))
	for b := range scanned {
		buckets = append(buckets, b)
	}
	sort.Strings(buckets)
	total := 0
	for _, b := range buckets {
		total += counts[b]
		t.Logf("%-34s units=%-5d under-applied generics=%d", b, scanned[b], counts[b])
	}
	t.Logf("%-34s %30d", "TOTAL", total)

	if total == 0 {
		return
	}
	var detail strings.Builder
	for _, h := range hits {
		detail.WriteString("\n  [" + h.bucket + "] " + h.where + ": " + h.msg)
	}
	t.Fatalf("%d under-applied generic reference(s) in the tree:%s", total, detail.String())
}

// TestGenericArity_RejectsAtTheDeclaration pins the rule: WHICH positions it
// fires at, WHERE the diagnostic lands, and — the half that decides whether the
// rule is the right one — which positions it must leave alone.
//
// The value of the rule is the position, not the rejection. `fn take(b: Box)`
// was accepted at its declaration and failed at the first USE, reporting
// `return type mismatch: expected Int, got T` on the body's line. The cases
// below assert the diagnostic now lands on the type the author wrote.
func TestGenericArity_RejectsAtTheDeclaration(t *testing.T) {
	const decls = `struct Box<T> {
  inner: T
}

interface Shown<T> {
  fn show(b: T): String
}

`
	fires := []struct {
		name    string
		src     string
		want    string
		line    int
		col     int
		nothing []string
	}{
		{
			name: "return type",
			src:  "fn make(): Box {\n  Box{inner: 1}\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 9, col: 12,
			// No `nothing` here: an unresolvable RETURN type falls back to
			// Unit and the body then reports `expected Unit`. That cascade is
			// pre-existing and not this rule's — `fn f(): Nope { 1 }` reports
			// `unknown type "Nope"` followed by
			// `return type mismatch: expected Unit, got Int`.
		},
		{
			name: "parameter type",
			src:  "fn take(b: Box): Int {\n  b.inner\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 9, col: 12,
			nothing: []string{"return type mismatch"},
		},
		{
			name: "struct field type",
			src:  "struct Holder {\n  b: Box\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 10, col: 6,
		},
		{
			name: "nested type argument",
			src:  "fn each(_xs: List<Box>): Int {\n  0\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 9, col: 19,
		},
		{
			name: "binding annotation",
			src:  "fn go(): Int {\n  held: Box = Box{inner: 1}\n  1\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 10, col: 9,
		},
		{
			name: "enum variant payload",
			src:  "enum Carrier {\n  Wraps Box\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 10, col: 9,
		},
		{
			name: "enum variant field type",
			src:  "enum Carrier {\n  Holds { held: Box }\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 10, col: 17,
		},
		{
			name: "distinct-type inner",
			src:  "type Wrapper Box\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 9, col: 14,
		},
		{
			name: "typealias target",
			src:  "typealias Alias Box\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 9, col: 17,
		},
		{
			name: "interface field type",
			src:  "interface Holds {\n  field b: Box\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 10, col: 12,
		},
		{
			name: "interface method signature",
			src:  "interface Handles {\n  fn handle(b: Box): Int\n}\n",
			want: "`Box` expects 1 type argument, got 0",
			line: 10, col: 16,
		},
		{
			name: "where-clause bound",
			src:  "fn shown<T>(_x: T): Int where T: Shown {\n  0\n}\n",
			want: "`Shown` expects 1 type argument, got 0",
			line: 9, col: 34,
		},
		{
			name: "too many type arguments",
			src:  "fn take(_b: Box<Int, Int>): Int {\n  0\n}\n",
			want: "`Box` expects 1 type argument, got 2",
			line: 9, col: 13,
		},
		{
			// A dotted type name is ONE name whose spelling contains a dot,
			// and it reaches the resolver as an *ast.QualifiedType rather than
			// a SimpleType — a second spelling that needed its own arm. The
			// diagnostic lands on the leading segment, which is where the
			// unknown-type error for the same node already lands.
			name: "dotted type name",
			src:  "pub struct Probe.Held<T> {\n  inner: T\n}\n\nfn take(_h: Probe.Held): Int {\n  0\n}\n",
			want: "`Probe.Held` expects 1 type argument, got 0",
			line: 13, col: 13,
		},
	}

	for _, tc := range fires {
		t.Run(tc.name, func(t *testing.T) {
			errs := diagnosticsFor(t, decls+tc.src)
			var got []string
			found := false
			for _, e := range errs {
				got = append(got, fmt.Sprintf("line %d col %d: %s", e.Line, e.Col, e.Message))
				if e.Message == tc.want && e.Line == tc.line && e.Col == tc.col {
					found = true
				}
			}
			if !found {
				t.Errorf("want %q at line %d col %d; got:\n  %s", tc.want, tc.line, tc.col, strings.Join(got, "\n  "))
			}
			for _, absent := range tc.nothing {
				for _, e := range errs {
					if strings.Contains(e.Message, absent) {
						t.Errorf("the rule should have pre-empted the late diagnostic %q, but it still fired: line %d: %s", absent, e.Line, e.Message)
					}
				}
			}
		})
	}
}

// TestGenericArity_LeavesTheLegitimateFormsAlone is the half that makes the
// rule correct rather than merely strict. Each case below is a bare generic
// reference the tree relies on: 34 impl headers and 53
// struct literals across std/, examples/, tests/ and the tour.
func TestGenericArity_LeavesTheLegitimateFormsAlone(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// The declaration's own type parameter. `inner: T` is a type
			// PARAMETER reference in a field position; a rule that cannot tell
			// it from an under-applied generic is the wrong rule.
			name: "type parameter inside its own declaration",
			src: `struct Box<T> {
  inner: T
}

enum Pick<T> {
  One T
  Named { held: T }
}

fn unwrap<T>(b: Box<T>): T {
  b.inner
}
`,
		},
		{
			// `impl Iter for List<T>` — 34 sites. The interface's argument is
			// SOLVED from the block's own method signatures by
			// recordImplTypeArgsFromBlock, so it is recovered rather than
			// missing. std/lists, std/maps, std/sets, std/vectors,
			// std/strings, std/ranges, std/bytes, std/iter, std/regex,
			// std/toml and std/calendar all depend on this spelling.
			name: "impl interface header with no type arguments",
			src: `interface Shown<T> {
  fn show(x: T): String
}

struct Tag {
  name: String
}

impl Shown for Tag {
  fn show(_x: Int): String {
    "tagged"
  }
}
`,
		},
		{
			// `Box{inner: 1}` — 53 sites. The argument comes from the field
			// values, and there is no turbofish on a struct literal:
			// `Box<Int>{…}` parses as a comparison. No spelling would satisfy
			// a rule here.
			name: "struct literal type name",
			src: `struct Box<T> {
  inner: T
}

fn go(): Int {
  b = Box{inner: 1}
  b.inner
}
`,
		},
		{
			name: "a fully applied generic",
			src: `struct Box<T> {
  inner: T
}

fn take(b: Box<Int>): Int {
  b.inner
}

struct Holder {
  b: Box<String>
}
`,
		},
		{
			// A `where` bound on a generic interface, supplying its argument —
			// the shape std/ranges.nomi:127 writes as
			// `where T: Comparable and Steppable<S>`.
			name: "where bound on a generic interface with its argument",
			src: `interface Shown<T> {
  fn show(x: T): String
}

fn render<T, U>(_x: T): String where T: Shown<U> {
  "ok"
}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, e := range diagnosticsFor(t, tc.src) {
				if arityDiagnostic.MatchString(e.Message) {
					t.Errorf("the rule fired on a legitimate form: line %d col %d: %s", e.Line, e.Col, e.Message)
				}
			}
		})
	}
}

// diagnosticsFor analyzes one source through the production DocumentManager.
func diagnosticsFor(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	dm.SetWorkspaceRoot(dir)
	doc := dm.Open("file://"+path, src)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("source did not analyze")
	}
	return doc.Analysis.TypeErrors
}
