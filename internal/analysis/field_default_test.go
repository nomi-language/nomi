package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// fieldDefaultDiagnostic matches the field-value rule's own diagnostics. It is
// the phrasing shared with the two constructor-call record forms
// (checkStructLitAgainstStruct / checkAnonStructTypeAgainstStruct), so a
// match may come from a bad CONSTRUCTION as well as from a bad DEFAULT. That
// is deliberate — the two positions were given one wording on purpose — and the
// census below is a clean instrument anyway, because the constructor forms were
// already policed and the four trees carry no violation of either.
// TestFieldDefault_TreeIsClean plants a positive and requires this pattern to
// match it, so the pattern and the message cannot drift apart silently.
var fieldDefaultDiagnostic = regexp.MustCompile(`^field '[^']+' of [^:]+: expected .+, got .+$`)

// plantedBadFieldDefault is a struct whose field default is the wrong type. It
// analyzed clean and RAN, printing `port=not an int`, until this rule landed.
const plantedBadFieldDefault = `struct Cfg {
  port: Int = "not an int"
}

fn main() {
  _ = Cfg{}.port
}
`

// TestFieldDefault_TreeIsClean is BOTH the blast-radius census for the
// field-default rule and the standing guard that the tree stays clean.
//
// It runs the production pipeline — analysis.DocumentManager, which is what the
// LSP opens a file with and which folds BuildTypes, CheckTypes, the concurrency
// passes and FinalizeCoherence into one TypeErrors slice — over every `.nomi`
// file in std/ and tests/, plus every fenced Nomi block in the tour.
//
// Before the rule rejected anything, with the check
// diverted into a recording sink so no rejection could cascade: 46 field
// defaults across the four trees and ZERO that a correct check refuses.
//
//	bucket                            units  defaults  rejected
//	std                                  52        16         0
//	tests                               224        23         0
//	examples                             49         5         0
//	tour (nomi-run, executed)           118         2         0
//	tour (nomi, highlight-only)          32         0         0
//	TOTAL                                          46         0
//
// By position: 37 top-level struct declarations, 8 top-level enum record
// variants, 1 block-local struct declaration.
//
// The 46 was cross-checked against an INDEPENDENT denominator — a reflection
// walk over the parsed AST counting every `ast.StructField` with a non-nil
// `Default`, which cannot miss a container the way a per-node walk can — and it
// agreed bucket for bucket. That is what rules out the zero being an artifact
// of a position the check never visits.
//
// Run with -v for the by-bucket table.
func TestFieldDefault_TreeIsClean(t *testing.T) {
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
			if !fieldDefaultDiagnostic.MatchString(e.Message) {
				continue
			}
			counts[bucket]++
			hits = append(hits, hit{bucket: bucket, where: fmt.Sprintf("%s:%d:%d", where, e.Line, e.Col), msg: e.Message})
		}
	}

	// --- the instrument is not blind -------------------------------------
	// A zero below means nothing unless this same pipeline, pointed at a file
	// that DOES carry an ill-typed field default, reports it and this pattern
	// matches.
	func() {
		dir := t.TempDir()
		path := filepath.Join(dir, "main.nomi")
		if err := os.WriteFile(path, []byte(plantedBadFieldDefault), 0o644); err != nil {
			t.Fatal(err)
		}
		doc := newManager(dir).Open("file://"+path, plantedBadFieldDefault)
		if doc == nil || doc.Analysis == nil {
			t.Fatal("planted positive did not analyze")
		}
		found := false
		for _, e := range doc.Analysis.TypeErrors {
			if fieldDefaultDiagnostic.MatchString(e.Message) {
				found = true
				t.Logf("planted positive matched: line %d col %d: %s", e.Line, e.Col, e.Message)
			}
		}
		if !found {
			t.Fatalf("the census instrument is BLIND: the planted ill-typed field default produced no matching diagnostic; got %v", doc.Analysis.TypeErrors)
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
		t.Logf("%-34s units=%-5d bad field values=%d", b, scanned[b], counts[b])
	}
	t.Logf("%-34s %30d", "TOTAL", total)

	if total == 0 {
		return
	}
	var detail strings.Builder
	for _, h := range hits {
		detail.WriteString("\n  [" + h.bucket + "] " + h.where + ": " + h.msg)
	}
	t.Fatalf("%d ill-typed field value(s) in the tree:%s", total, detail.String())
}

// TestFieldDefault_RejectsAtTheDeclaration pins the rule: WHICH positions it
// fires at, and WHERE the diagnostic lands.
//
// The position is the whole point. Without this rule both reproductions
// analyzed CLEAN. The scalar one ran and printed `port=not an int`; the interface one
// ran and trapped with `Clock.at: no implementation for type 'NoClock'` — a
// run-time failure for a mistake fully visible in the declaration.
func TestFieldDefault_RejectsAtTheDeclaration(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			// Reproduction 1. Ran and printed the String.
			name: "scalar mismatch on a top-level struct",
			src: `struct Cfg {
  port: Int = "not an int"
}
fn main() { _ = Cfg{}.port }
`,
			want: "field 'port' of Cfg: expected Int, got String",
		},
		{
			// Reproduction 2. Ran and trapped at the first dispatch.
			name: "interface-typed field defaulted to a non-implementer",
			src: `interface Clock {
  fn at(c: self): Int
}
struct NoClock {
  t: Int
}
struct Cfg {
  clock: Clock = NoClock{t: 7}
}
fn main() { _ = Cfg{} }
`,
			want: "field 'clock' of Cfg: expected Clock, got NoClock",
		},
		{
			// Not only interfaces: a scalar in an interface-typed field.
			name: "interface-typed field defaulted to a scalar",
			src: `interface Clock {
  fn at(c: self): Int
}
struct Cfg {
  clock: Clock = 5
}
fn main() { _ = Cfg{} }
`,
			want: "field 'clock' of Cfg: expected Clock, got Int",
		},
		{
			// Position 2 of 3. A record variant's fields are the same
			// ast.StructField a struct's are and were unchecked for the
			// same reason.
			name: "enum record variant field",
			src: `enum Status {
  Active {since: Int = "bad"}
  Gone
}
fn main() { _ = Status.Gone }
`,
			want: "field 'since' of Status.Active: expected Int, got String",
		},
		{
			// Position 3 of 3. Without the checkNode arm this is the
			// bypass for the entire rule: CheckTypes's walk sees only
			// top-level nodes.
			name: "block-local struct declaration",
			src: `fn f(): Int {
  struct Local {
    v: Int = "bad"
  }
  Local{}.v
}
fn main() { _ = f() }
`,
			want: "field 'v' of Local: expected Int, got String",
		},
		{
			// A dotted-name declaration (`pub struct ToJson.Options` in
			// std/json) is a top-level node with a dotted name, not a
			// declaration nested inside another body — the parser refuses
			// `struct` inside a struct body outright. Two of the 16 std
			// sites are this shape, so it needs its own case.
			name: "dotted-name top-level declaration",
			src: `struct Owner {
  n: Int
}
struct Owner.Options {
  flag: Int = "bad"
}
fn main() { _ = Owner{n: 1} }
`,
			want: "field 'flag' of Owner.Options: expected Int, got String",
		},
		{
			// THE GENERIC DECISION, refusing half. A default for a field
			// typed by a bare type parameter cannot be checked against
			// anything concrete, and accepting it unchecked is unsound:
			// `Box<String>{}` would hold an Int in a String field. The
			// refusal needs no special arm — argMatchesParam has nothing
			// to unify, because Int carries no type variable.
			name: "concrete default for a bare type-parameter field",
			src: `struct Box<T> {
  v: T = 0
}
fn main() { }
`,
			want: "field 'v' of Box: expected T, got Int",
		},
		{
			// Same decision one level in: the container matches, the
			// element type does not.
			name: "concrete element for a List<T> field",
			src: `struct Box<T> {
  items: List<T> = [1]
}
fn main() { }
`,
			want: "field 'items' of Box: expected List<T>, got List<Int>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := diagnosticsFor(t, tc.src)
			for _, e := range errs {
				if e.Message == tc.want {
					t.Logf("line %d col %d: %s", e.Line, e.Col, e.Message)
					return
				}
			}
			t.Fatalf("want %q, got %v", tc.want, errs)
		})
	}
}

// TestFieldDefault_LeavesTheLegitimateFormsAlone is the half that makes the
// rule correct rather than merely strict. Each case is a spelling the four
// trees rely on, taken from the census by name, plus the `App` shape
// AppStructLanding's held cutover rests on.
//
// The last two cases are the ACCEPTING half of the generic decision. A `T`-typed
// field default is not refused categorically — it is refused when its type is
// concrete. A default whose type IS the parameter, or that solves through it,
// is admitted.
func TestFieldDefault_LeavesTheLegitimateFormsAlone(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// The one the `App`-as-a-struct landing rests on: nobody
			// writes `Context.root()`, so it lives in the one position
			// that was unchecked. A CALL, not a literal.
			name: "a call returning exactly the field type",
			src: `
pub struct Cfg {
  context: Context = Context.root()
  port: Int = 3000
}
fn main() { _ = Cfg{}.port }
`,
		},
		{
			// The same shape on a GENERIC struct — `App<T>`'s exact
			// form. The generic-std-struct family is taught to
			// model a field default rather than refuse one, which is
			// what makes this position reachable in the IR builder.
			name: "a call default beside a required type-parameter field",
			src: `
pub struct MyApp<T> {
  context: Context = Context.root()
  config: T
}
fn main() { _ = MyApp{config: 42}.config }
`,
		},
		{
			// The interface case that must still pass. Reproduction 2
			// with an implementer in place of the non-implementer:
			// nothing but the impl distinguishes them, which is what
			// makes this the control for that rejection.
			name: "interface-typed field defaulted to an implementer",
			src: `interface Clock {
  fn at(c: self): Int
}
struct FakeClock {
  t: Int
}
impl Clock for FakeClock {
  fn at(c: FakeClock): Int { c.t }
}
struct Cfg {
  clock: Clock = FakeClock{t: 1}
}
fn main() { _ = Clock.at(Cfg{}.clock) }
`,
		},
		{
			// Dot-variant shorthand: no type name at all on the right.
			// Works only because the declared field type is pushed as
			// the expected type. std/supervisors and
			// tests/07-structs-and-enums both rely on it.
			name: "dot-variant shorthand default",
			src: `enum Restart {
  Temporary
  Permanent
}
struct Cfg {
  restart: Restart = .Temporary
}
fn main() { _ = Cfg{} }
`,
		},
		{
			// An untyped constructor whose element types are solved from
			// the field.
			name: "constructor with type arguments solved from the field",
			src: `struct Cfg {
  tasks: Map<Int, String> = Map.empty()
}
fn main() { _ = Cfg{} }
`,
		},
		{
			// A defaulted field on a generic struct whose own type
			// mentions no parameter.
			name: "concrete default on a generic struct",
			src: `struct Box<T> {
  label: String = "unnamed"
  inner: T
}
fn main() { _ = Box{inner: 7}.label }
`,
		},
		{
			// ACCEPTING half of the generic decision: an empty list's
			// element type variable binds to the parameter, so the
			// default solves through `T` rather than against something
			// concrete.
			name: "empty list default for a List<T> field",
			src: `struct Box<T> {
  items: List<T> = []
  inner: T
}
fn main() { _ = Box{inner: 7}.items }
`,
		},
		{
			// ACCEPTING half, second direction: the default's own type IS
			// the type parameter, so there is nothing concrete to
			// disagree with.
			name: "type-parameter-typed default for a type-parameter field",
			src: `interface Zeroed {
  fn zero(): self
}
struct Box<T> where T: Zeroed {
  v: T = T.zero()
}
fn main() { }
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ZERO diagnostics, not merely zero field-default-shaped ones.
			// Matching only this rule's own phrasing made these cases pass
			// vacuously: a mutant that dropped the expected-type push below
			// left `restart: Restart = .Temporary` reporting `.Temporary
			// requires a determinable enum type at this position` — a
			// different message, so the weaker assertion held while the
			// spelling was in fact broken.
			if errs := diagnosticsFor(t, tc.src); len(errs) != 0 {
				t.Fatalf("legitimate field default produced diagnostics: %v", errs)
			}
		})
	}
}

// TestFieldDefault_RecordsConformance is the guard on SHARING rather than on
// the verdict, and it exists because the reimplementation mutant passed all
// seventeen other cases.
//
// `argMatchesParam` reaches `ifaceParamAdmits`, which does two things: it
// decides admission via `c.unify` (the impl-aware `UnifyWithImpls`), and it
// calls `recordConformanceIfConcrete` so the impl manifest records which
// (Type, Iface) pairs the program demands. A hand-written `c.unify(declared, valTy,
// nil) == nil` in place of the call gets EVERY VERDICT RIGHT — it is the same
// unifier — and silently drops the recording. Planted, it passed
// TestFieldDefault_TreeIsClean and both of the case tables above, all 17 cases.
// This is the case that kills it.
//
// `RealClock` is constructed NOWHERE but the default. The equivalent test in
// partial_iface_field_test.go was first written with a type the program also
// built in a struct literal, and the literal path's own recording made the
// assertion hold no matter what the path under test did.
func TestFieldDefault_RecordsConformance(t *testing.T) {
	const src = `pub interface Clock {
  fn at(c: self): Int
}

pub struct FakeClock {
  t: Int
}

impl Clock for FakeClock {
  fn at(c: FakeClock): Int { c.t }
}

pub struct RealClock {
  base: Int
}

impl Clock for RealClock {
  fn at(c: RealClock): Int { c.base }
}

pub struct Cfg {
  clock: Clock = RealClock{base: 42}
  spare: Clock = FakeClock{t: 1}
}

fn main() {
  _ = Clock.at(Cfg{}.clock)
}
`
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
	if len(doc.Analysis.TypeErrors) != 0 {
		t.Fatalf("unexpected diagnostics: %v", doc.Analysis.TypeErrors)
	}
	if doc.Analysis.ImplManifest == nil {
		t.Fatal("no impl manifest produced")
	}
	types, ok := doc.Analysis.ImplManifest["Clock"]
	if !ok {
		t.Fatalf("the field default admitted no Clock conformance; manifest: %v", doc.Analysis.ImplManifest)
	}
	if _, ok := types["RealClock"]; !ok {
		t.Fatalf("RealClock is constructed only in a field default, so a missing "+
			"(RealClock, Clock) entry means the default path admitted without recording — "+
			"which is what a reimplementation of ifaceParamAdmits drops; got %v", types)
	}
}

// fieldDefaultDenominator is the number of struct/variant field defaults each
// tree carries, counted by an instrument INDEPENDENT of the rule:
// a reflection walk over the parsed AST for every `ast.StructField` with a
// non-nil `Default`.
//
// It is here because TestFieldDefault_TreeIsClean's zero can only speak for
// positions the checker visits. A field default sitting somewhere the walk
// never reaches records nothing and reads as clean by construction. These
// numbers are the denominator that zero divides; they match the
// checker's examined count bucket for bucket, which is what makes the zero a
// measurement rather than an absence of measurement.
//
// `tests` reads 27 rather than 23 because this
// slice's own corpus case,
// `07-structs-and-enums/field_default_types/field_default_types_test.nomi`,
// declares four of them (Configured.clock, Configured.port, Box.items,
// Box.label). The other 23 are untouched.
//
// `std` reads 11: std/assertions 3, std/compiler 2, std/json 2,
// std/startup 2 (`Startup` declares `env` and `args` with empty defaults, so
// `Startup{}` is an empty Startup) and std/supervisors 2 (the fields of the
// `Backoff.Exponential` record variant).
//
// `tour (nomi-run)` reads 1 rather than 2 because the same cutover removed
// the other site. `capabilities-and-context.md`'s "basic shape" block declared
// `struct MyApp { context: Context = Context.root(); port: Int }` plus an
// `impl App for MyApp`; migrated, the payload is `struct Config { port: Int }`
// with no default and the `context` default lives on stdlib's `App`. The one
// survivor the census prints is `structs-enums-distinct.md:L29`'s `age@3:3`.
var fieldDefaultDenominator = map[string]int{
	"std":             12,
	"tests":           28,
	"tour (nomi-run)": 1,
	"tour (nomi)":     0,
}

// TestFieldDefault_CensusHasNoBlindPosition counts field defaults from the AST
// rather than from the rule, and fails when a tree's count moves.
//
// A failure here is not necessarily a defect — adding a field default to the
// tree is ordinary work. It means the census needs its number updated AND, if
// the new site is in a position the three known ones do not cover (top-level
// struct declaration including the dotted-name form, top-level enum record
// variant, block-local struct declaration), that TestFieldDefault_TreeIsClean's
// zero no longer speaks for it.
func TestFieldDefault_CensusHasNoBlindPosition(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, b := range []string{"std", "tests"} {
		dir := filepath.Join(root, b)
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".nomi") {
				return err
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
			hits := fieldDefaultsInAST(nodes)
			if len(hits) > 0 {
				rel, _ := filepath.Rel(root, path)
				t.Logf("%-72s %d  %v", rel, len(hits), hits)
			}
			got[b] += len(hits)
			return nil
		}); err != nil {
			t.Fatalf("walk %s: %v", b, err)
		}
	}
	tourRoot := filepath.Join(root, "tour", "src", "content", "docs")
	if err := filepath.WalkDir(tourRoot, func(path string, d os.DirEntry, err error) error {
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
		for _, lang := range []string{"nomi-run", "nomi"} {
			bucket := "tour (" + lang + ")"
			for _, b := range doctest.ExtractBlocks(string(data), lang) {
				if b.HasInfo("ignore") {
					continue
				}
				nodes, _ := parser.ParseWithRecovery(lexer.Lex(b.Code))
				hits := fieldDefaultsInAST(nodes)
				if len(hits) > 0 {
					t.Logf("%-72s %d  %v", rel+":L"+strconv.Itoa(b.Line)+" ("+lang+")", len(hits), hits)
				}
				got[bucket] += len(hits)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk tour: %v", err)
	}

	keys := make([]string, 0, len(fieldDefaultDenominator))
	for k := range fieldDefaultDenominator {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := 0
	for _, k := range keys {
		total += got[k]
		t.Logf("%-24s field defaults=%-4d want=%d", k, got[k], fieldDefaultDenominator[k])
		if got[k] != fieldDefaultDenominator[k] {
			t.Errorf("%s carries %d field defaults, census recorded %d — re-run the census "+
				"and update fieldDefaultDenominator; if the new site is in a position other than "+
				"a top-level struct declaration, a top-level enum record variant or a block-local "+
				"struct declaration, TestFieldDefault_TreeIsClean's zero does not cover it",
				k, got[k], fieldDefaultDenominator[k])
		}
	}
	t.Logf("%-24s field defaults=%d", "TOTAL", total)
	if total == 0 {
		t.Fatal("counted zero field defaults in the whole repo — the roots moved, so this gate is vacuous")
	}
}

// fieldDefaultsInAST walks a parsed file with reflection — every struct, slice,
// map, pointer and interface field, no per-node cases — and returns one entry
// per `ast.StructField` carrying a non-nil `Default`. Reflection rather than a
// typed walk on purpose: a typed walk would have to enumerate the containers a
// declaration can sit in, which is exactly the thing this is here to avoid
// assuming.
func fieldDefaultsInAST(nodes []ast.Node) []string {
	var out []string
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Ptr {
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
			}
			walk(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		case reflect.Struct:
			if sf, ok := v.Interface().(ast.StructField); ok && sf.Default != nil {
				out = append(out, fmt.Sprintf("%s@%d:%d", sf.Name, sf.Line, sf.Col))
			}
			for i := 0; i < v.NumField(); i++ {
				if !v.Type().Field(i).IsExported() {
					continue
				}
				walk(v.Field(i))
			}
		}
	}
	for _, n := range nodes {
		walk(reflect.ValueOf(n))
	}
	return out
}

// TestFieldDefault_AcrossAModuleBoundary checks the position the rest of this
// file approximates rather than exercises: the defaulted struct declared in one
// MODULE and reached from an IMPORTER.
//
// Asked for by AppStructLanding, whose held `App`-as-a-struct cutover rests on
// exactly this shape — `pub struct App<T> { context: Context = Context.root();
// config: T }` in `std/app.nomi`, read by every program. A user-declared copy
// of the same struct in the entry file is a good approximation and it is not
// the same object, for two reasons it named:
//
//   - `internal/irbuild` handles a stdlib field default as `fieldDef.stdDeflt`
//     rather than `fieldDef.deflt`, and the two are mutually exclusive by
//     assertion, because an AST default is evaluated in the DECLARING module's
//     scope. `types.go` refuses `sibling file field default` for that shape.
//     Whether the ANALYZER draws the same distinction where this rule runs was
//     the open question.
//   - `Context` is canonicalised PER BUILD and compared by pointer. A
//     regression of exactly that shape has shipped before: a `*StructType`
//     carried between builds brought the wrong `Context` instance and produced
//     `expected Context, got Context` — identical names, unequal pointers.
//
// MEASURED against the real object as well as against this fixture. Taking
// AppStructLanding's own `std/app.nomi` from `apx-work`, dropping it into this
// worktree's `std/` and analyzing an importing program: `Context.root()` is
// ADMITTED, and the same file with the default replaced by `5` reports
// `std/app.nomi:45:22: field 'context' of App: expected Context, got Int`
// through the importer. So the rule reaches a stdlib module's field default
// from an importer's build, and it does not see two `Context` instances.
// That measurement is now reproducible against the shipped `std/app.nomi`,
// which carries exactly this shape since the cutover; the fixture below stays
// because it is the half that does not depend on stdlib's contents.
func TestFieldDefault_AcrossAModuleBoundary(t *testing.T) {
	const moduleSrc = `
pub struct Cfg {
  context: Context = Context.root()
  port: Int = 3000
}

pub fn make(): Cfg {
  Cfg{}
}
`
	const badModuleSrc = `
pub struct Cfg {
  context: Context = 5
  port: Int = 3000
}

pub fn make(): Cfg {
  Cfg{}
}
`
	const importerSrc = `import cfg

fn main() {
  _ = cfg.make().port
}
`
	run := func(t *testing.T, module string) []analysis.TypeError {
		t.Helper()
		lib := std.Load()
		dm := analysis.NewDocumentManager()
		dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
		dir := t.TempDir()
		for name, src := range map[string]string{"cfg.nomi": module, "main.nomi": importerSrc} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		dm.SetWorkspaceRoot(dir)
		// Open the importer FIRST so the module is analyzed as part of a
		// project that has one, then read the MODULE's own diagnostics.
		// `Open` on the entry file returns only the entry file's
		// diagnostics — correct for an editor, since a sibling's belong to
		// the sibling's document — and the first draft of this test read
		// them, so its planted positive did not fire and its zero was
		// vacuous.
		entry := filepath.Join(dir, "main.nomi")
		if doc := dm.Open("file://"+entry, importerSrc); doc == nil || doc.Analysis == nil {
			t.Fatal("importer did not analyze")
		}
		modPath := filepath.Join(dir, "cfg.nomi")
		modDoc := dm.Open("file://"+modPath, module)
		if modDoc == nil || modDoc.Analysis == nil {
			t.Fatal("module did not analyze")
		}
		return modDoc.Analysis.TypeErrors
	}

	t.Run("a correct module-level default survives the boundary", func(t *testing.T) {
		if errs := run(t, moduleSrc); len(errs) != 0 {
			t.Fatalf("module-level `context: Context = Context.root()` read from an importer "+
				"produced diagnostics: %v", errs)
		}
	})

	// The planted positive behind that zero. Without it the case above passes
	// whether the rule reaches a module's declaration through an importer's
	// build or never runs there at all.
	t.Run("a wrong module-level default is still caught", func(t *testing.T) {
		errs := run(t, badModuleSrc)
		for _, e := range errs {
			if e.Message == "field 'context' of Cfg: expected Context, got Int" {
				t.Logf("line %d col %d: %s", e.Line, e.Col, e.Message)
				return
			}
		}
		t.Fatalf("the rule does not reach a module's field default from an importer's build; got %v", errs)
	})
}
