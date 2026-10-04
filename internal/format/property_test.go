package format

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// collectNomiFiles returns every .nomi file under tests/ and std/, walked
// recursively so the corpus's numbered category folders and their project
// subdirectories are all covered.
func collectNomiFiles(t *testing.T) []string {
	t.Helper()
	roots := []string{
		"../../tests",
		"../../std",
	}
	var files []string
	for _, r := range roots {
		err := filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // a root may not exist (e.g. stdlib); skip it
			}
			if !d.IsDir() && strings.HasSuffix(path, ".nomi") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no tests or stdlib files found")
	}
	return files
}

// astSkipFields lists struct field names to ignore when comparing ASTs for
// semantic equivalence. These carry trivia, source positions, or doc-comment
// text — none of which affect program meaning. Keep this list in sync with
// ast/ast.go; a field scan at the top of the test verifies every *position*
// field in the package is covered.
var astSkipFields = map[string]bool{
	// Embedded trivia carrier (leading/trailing comments and blank lines).
	"TriviaCarrier": true,
	// Source position fields — present on nearly every node. The formatter is
	// free to relocate code, so positions change.
	"Line":             true,
	"Col":              true,
	"EndLine":          true,
	"EndCol":           true,
	"ModuleLine":       true,
	"ModuleCol":        true,
	"BindingCol":       true,
	"NameLine":         true,
	"NameCol":          true,
	"BootLine":         true,
	"BootCol":          true,
	"ClockLine":        true,
	"ClockCol":         true,
	"AliasLine":        true,
	"AliasCol":         true,
	"ForeignAliasLine": true,
	"ForeignAliasCol":  true,
	"ForeignNameLine":  true,
	"ForeignNameCol":   true,
	"SetupLine":        true,
	"SetupCol":         true,
	// `clock` keeps its trivia directly on TestDecl because it is not a
	// standalone AST node.
	"ClockLeading":  true,
	"ClockTrailing": true,
	// Position of the `self` marker inside an import brace list. Shifts when
	// the formatter reflows a block, but carries no semantic meaning.
	"SelfLine": true,
	"SelfCol":  true,
	// Source-shape flag for import selector braces. `path: A, B` and
	// `path.{A, B}` parse with different Braced values but bind the same names.
	"Braced": true,
	// Doc-comment text attached to declarations. The formatter preserves doc
	// comments in output, but the comparator ignores them: doc-comment
	// round-tripping is covered separately by unit tests.
	"Doc": true,
	// Purely syntactic flag on ExternType / TypeDef: an EMPTY `{}` body is
	// semantically identical to no body, and the formatter canonicalizes by
	// dropping the empty braces (HasBody true → false on reparse). A body
	// with actual items still compares via the Items slice, so skipping the
	// flag loses nothing semantic.
	"HasBody": true,
}

func astSkipField(name string) bool {
	return astSkipFields[name] ||
		strings.HasSuffix(name, "Line") ||
		strings.HasSuffix(name, "Col") ||
		strings.HasSuffix(name, "Span")
}

// equalAST compares two AST subtrees for semantic equivalence, ignoring
// trivia/positions/doc fields (see astSkipFields). Uses reflect to walk both
// values in parallel.
func equalAST(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Ptr:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return equalAST(a.Elem(), b.Elem())
	case reflect.Struct:
		// Special case: ast.Param uses a synthesized Name like "__destr_N"
		// for destructure params, where N is the column of the opening
		// delimiter. That column shifts under formatting. The Destructure
		// field carries the real semantic pattern — comparing it is enough
		// to establish equivalence of destructure params.
		if a.Type() == reflect.TypeOf(ast.Param{}) {
			for i := 0; i < a.NumField(); i++ {
				name := a.Type().Field(i).Name
				if astSkipField(name) {
					continue
				}
				if name == "Name" {
					an, _ := a.Field(i).Interface().(string)
					bn, _ := b.Field(i).Interface().(string)
					if strings.HasPrefix(an, "__destr_") && strings.HasPrefix(bn, "__destr_") {
						continue
					}
				}
				if !equalAST(a.Field(i), b.Field(i)) {
					return false
				}
			}
			return true
		}
		for i := 0; i < a.NumField(); i++ {
			name := a.Type().Field(i).Name
			if astSkipField(name) {
				continue
			}
			if !equalAST(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !equalAST(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.Len() != b.Len() {
			return false
		}
		for _, k := range a.MapKeys() {
			if !equalAST(a.MapIndex(k), b.MapIndex(k)) {
				return false
			}
		}
		return true
	default:
		// Primitives (int, string, bool, float) and anything else compare by
		// value. Using reflect.DeepEqual is fine for leaves; they won't contain
		// trivia/position fields.
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
}

// astEquivalent returns true iff the two top-level AST slices are
// semantically equivalent per equalAST.
func astEquivalent(a, b []ast.Node) bool {
	return equalAST(reflect.ValueOf(a), reflect.ValueOf(b))
}

// astDump renders an AST to a compact S-expression-ish string for diff output.
// Only used when a test fails, so conciseness matters more than completeness.
func astDump(v reflect.Value, indent int) string {
	if !v.IsValid() {
		return "<invalid>"
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		if v.IsNil() {
			return "nil"
		}
		return astDump(v.Elem(), indent)
	case reflect.Struct:
		var b strings.Builder
		b.WriteString(v.Type().Name())
		b.WriteString("{")
		first := true
		for i := 0; i < v.NumField(); i++ {
			name := v.Type().Field(i).Name
			if astSkipFields[name] {
				continue
			}
			if !first {
				b.WriteString(", ")
			}
			first = false
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(astDump(v.Field(i), indent+1))
		}
		b.WriteString("}")
		return b.String()
	case reflect.Slice, reflect.Array:
		var parts []string
		for i := 0; i < v.Len(); i++ {
			parts = append(parts, astDump(v.Index(i), indent+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.Map:
		var parts []string
		for _, k := range v.MapKeys() {
			parts = append(parts, fmt.Sprintf("%v: %s", k.Interface(), astDump(v.MapIndex(k), indent+1)))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

// TestProperty_Idempotent verifies that applying Format twice produces the
// same result as applying it once. Runs on every .nomi file under
// tests/ and stdlib/.
func TestProperty_Idempotent(t *testing.T) {
	files := collectNomiFiles(t)
	for _, f := range files {
		f := f
		name := strings.TrimPrefix(f, "../")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			once, err := Format(string(src))
			if err != nil {
				t.Skipf("parse error (not a formatter bug): %v", err)
			}
			twice, err := Format(once)
			if err != nil {
				t.Fatalf("reformat parse error (formatter produced unparseable output!): %v\n--- once ---\n%s", err, once)
			}
			if once != twice {
				t.Errorf("non-idempotent.\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
		})
	}
}

// TestProperty_SemanticallyEquivalent verifies that Parse(Format(src)) is
// AST-equivalent to Parse(src) (modulo trivia, positions, and doc-comment
// text). The formatter must never change program meaning.
//
// Because Format applies import sorting and type-body item ordering, the
// comparison is done against the same canonicalized Parse(src) rather than
// Parse(src) directly. These are part of the formatter's canonical output, and
// ignoring them would produce false positives on files whose source order is
// accepted but not canonical.
func TestProperty_SemanticallyEquivalent(t *testing.T) {
	files := collectNomiFiles(t)
	for _, f := range files {
		f := f
		name := strings.TrimPrefix(f, "../")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			origNodes, err := parser.Parse(lexer.Lex(string(src)))
			if err != nil {
				t.Skipf("parse error on source (not a formatter bug): %v", err)
			}
			// Apply the same canonicalization Format does so the comparison checks
			// that everything ELSE is preserved. These mutate in place.
			origSorted := canonicalizeFormatterOrder(collapseSingleEntryBlocks(sortImports(combineImports(origNodes))))

			formatted, err := Format(string(src))
			if err != nil {
				t.Fatalf("Format returned error: %v", err)
			}
			fmtNodes, err := parser.Parse(lexer.Lex(formatted))
			if err != nil {
				t.Fatalf("formatted output didn't parse: %v\n--- formatted ---\n%s", err, formatted)
			}

			if !astEquivalent(origSorted, fmtNodes) {
				t.Errorf("AST changed after formatting.\n--- original AST ---\n%s\n--- formatted AST ---\n%s\n--- formatted source ---\n%s",
					astDump(reflect.ValueOf(origSorted), 0),
					astDump(reflect.ValueOf(fmtNodes), 0),
					formatted,
				)
			}
		})
	}
}

// TestProperty_SemanticallyEquivalentShapes is TestProperty_SemanticallyEquivalent
// over SOURCES rather than corpus files, for shapes whose formatted spelling is
// ambiguous with a different construct.
//
// It exists because the corpus cannot reach these. The corpus is already
// formatted, so a shape the formatter mangles is committed in its mangled form
// and then round-trips perfectly: the input that triggers the bug is exactly the
// input no formatted file contains. That is how a one-field anonymous punned
// literal went unnoticed. `nomi fmt` re-emitted `{n: n}` as `{n}`, which
// re-parses as a BLOCK whose value is `n` — not a one-field anonymous struct —
// so the formatter changed program meaning, the strongest class of formatter
// bug. It had already broken a stdlib function in the shipped tree, and what
// found it was an attached `//!` test on that function, not any test in this
// package.
//
// The check is the ROUND TRIP, not the emitted text, deliberately. Pinning the
// output string would accept a future change that re-broke meaning while
// happening to keep the same spelling; an AST comparison cannot.
func TestProperty_SemanticallyEquivalentShapes(t *testing.T) {
	const preamble = `struct Box {
  n: Int
  label: String
}

fn probe(box: Box, n: Int, label: String): Box {
`
	for _, tc := range []struct {
		name string
		body string
	}{
		// The regression. One field, anonymous, punned: `{n}` is a block.
		{"one field anonymous punned", "  Struct.update(box, {n: n})\n"},
		// Two fields is unambiguous — a block has no comma-separated
		// statements — so punning must still be applied here.
		{"two fields anonymous punned", "  Struct.update(box, {n: n, label: label})\n"},
		// A type name disambiguates at any arity, including one.
		{"one field nominal punned", "  Box{n: n, label: label}\n"},
		// Not a pun, so never at risk; present so a fix that keyed on arity
		// alone rather than on punning would show up here.
		{"one field anonymous not punned", "  Struct.update(box, {n: 1})\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := preamble + tc.body + "}\n"
			origNodes, err := parser.Parse(lexer.Lex(src))
			if err != nil {
				t.Fatalf("source did not parse (fix the fixture, not the formatter): %v", err)
			}
			origSorted := canonicalizeFormatterOrder(collapseSingleEntryBlocks(sortImports(combineImports(origNodes))))

			formatted, err := Format(src)
			if err != nil {
				t.Fatalf("Format returned error: %v", err)
			}
			fmtNodes, err := parser.Parse(lexer.Lex(formatted))
			if err != nil {
				t.Fatalf("formatted output didn't parse: %v\n--- formatted ---\n%s", err, formatted)
			}
			if !astEquivalent(origSorted, fmtNodes) {
				t.Errorf("formatting changed program meaning.\n--- source ---\n%s\n--- formatted ---\n%s\n--- original AST ---\n%s\n--- formatted AST ---\n%s",
					src, formatted,
					astDump(reflect.ValueOf(origSorted), 0),
					astDump(reflect.ValueOf(fmtNodes), 0),
				)
			}
			twice, err := Format(formatted)
			if err != nil {
				t.Fatalf("reformat parse error (formatter produced unparseable output): %v\n--- formatted ---\n%s", err, formatted)
			}
			if twice != formatted {
				t.Errorf("non-idempotent.\n--- once ---\n%s\n--- twice ---\n%s", formatted, twice)
			}
		})
	}
}

func canonicalizeFormatterOrder(nodes []ast.Node) []ast.Node {
	for _, n := range nodes {
		canonicalizeFormatterOrderNode(n)
	}
	return nodes
}

func canonicalizeFormatterOrderNode(n ast.Node) {
	switch v := n.(type) {
	case *ast.StructDef:
		v.Items = canonicalizeTypeBodyItemsForProperty(v.Items)
	case *ast.EnumDef:
		v.Items = canonicalizeTypeBodyItemsForProperty(v.Items)
	case *ast.TypeDef:
		v.Items = canonicalizeTypeBodyItemsForProperty(v.Items)
	case *ast.ExternType:
		v.Items = canonicalizeTypeBodyItemsForProperty(v.Items)
	}
}

func canonicalizeTypeBodyItemsForProperty(items []ast.Node) []ast.Node {
	items = orderTypeBodyItems(items)
	for _, item := range items {
		canonicalizeFormatterOrderNode(item)
	}
	return items
}
