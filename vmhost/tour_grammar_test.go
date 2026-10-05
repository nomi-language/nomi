package vmhost_test

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file checks that the committed grammar wasm matches the committed
// parser source and that every committed query names only nodes it has.
//
// tour_bundle_test.go compares the staged grammar wasm against the committed
// one (`TestTourWasmBundleIsTheCurrentSources`, the
// `tree-sitter-nomi.wasm` row). It does not ask whether the committed one is
// what `tree-sitter-nomi/src/` currently generates. This file does.
//
// The check is needed because scripts/build-tour-wasm.sh copies the committed
// grammar wasm instead of building it: recent tree-sitter-cli binaries want a
// newer glibc than Cloudflare's build image, and older ones want
// Docker/Emscripten. So `tree-sitter generate` does not refresh the committed
// wasm, and scripts/sync-zed-grammar.sh does not either. A grammar change can
// leave it stale, and it keeps working only while no query names a node the
// stale parser lacks.
//
// When a query does name such a node, the failure is total. A tree-sitter
// query naming a node the language does not have fails to compile, and
// `ts_query_new` returning an error takes every other pattern in the file
// down with it: the tour renders as unhighlighted plain text, not as "one
// rule missing". TestTourWasmBundleIsTheCurrentSources cannot catch this,
// because the staged copy is a faithful copy of the stale committed original.
//
// Skip versus fail decides the host choice below.
//
// The obvious way to check "can this wasm compile this query" is to do it:
// load both under node with web-tree-sitter and call `new Query`. That is
// what prehighlight.mjs and tour-client.mjs do, so it is the exact browser
// operation. It is not used, and the reason is the skip.
//
// web-tree-sitter is npm-vendored into `tour/public/nomi/` by
// build-tour-wasm.sh, and `public/nomi/` is gitignored. A node-hosted check
// therefore has to skip when that directory is absent. A fresh clone has no
// bundle and could have a stale committed wasm at the same time, so the skip
// condition and the failure condition overlap, and a skipped check would hide
// the failure it exists to catch; tour_bundle_test.go avoids the same shape
// for the absent-bundle case. Fetching web-tree-sitter
// from npm inside a test instead makes the row need the network, which is a
// worse version of the same problem: it skips (or flakes) offline.
//
// So the checks below read the wasm in pure Go and have no skip condition at
// all. Both inputs, `tree-sitter-nomi/tree-sitter-nomi.wasm` and
// `tree-sitter-nomi/src/parser.c`, are committed files in this repository.
// The only way to reach a missing input is a broken checkout, and that is a
// failure here, never a skip. There is no tree-sitter CLI, no node, no browser,
// no emscripten and no network in the path.
//
// Reading the wasm in pure Go is simpler than it sounds.
// A tree-sitter grammar wasm is an emscripten side module exporting
// `tree_sitter_nomi()`, whose whole body is `global.get __memory_base;
// i32.const ADDR; i32.add; return`: the address of a static `TSLanguage`.
// The struct layout is committed right here in
// `tree-sitter-nomi/src/tree_sitter/parser.h`, and its first field is the ABI
// version. So: find the export, decode the constant, read the struct out of
// the data segment, follow `symbol_names` and `field_names`. Roughly 150
// lines, and it recovers the parser's actual node vocabulary rather than a
// proxy for it.
//
// Reading the vocabulary rather than grepping the wasm for strings matters.
// A NUL-terminated-string scan over the data segment is a superset test:
// short token names like `and`, `pub`, `+` appear in a 1.2MB emscripten
// binary for reasons unrelated to the grammar, so a scan can report a node
// present that the parser does not have. The struct walk cannot: it reads
// the same pointer array `ts_language_symbol_for_name` reads.
//
// The layout assumption checks itself. If the offsets below were wrong,
// the fields would not read as the values parser.c defines. The header check
// requires version/symbol_count/alias_count/token_count/external_token_count/
// state_count/large_state_count/production_id_count/field_count/
// max_alias_sequence_length to all equal the committed `#define`s, so the
// probability of a wrong layout producing ten correct numbers is nil.
//
// Do not rename this file to anything ending `_wasm_test.go`. Go reads the
// suffix before `_test` as an implicit GOARCH constraint, so `grammar_wasm_
// test.go` compiles only for GOARCH=wasm, and `go test` reports "no
// tests to run" with the file sitting right there. The same trap catches
// `_js_test.go`, `_linux_test.go` and every other GOOS/GOARCH name.

// grammarDir is the tree-sitter grammar package, a sibling of .
const grammarDir = "../tree-sitter-nomi"

// grammarQueryDirs holds every directory in the repository containing
// committed tree-sitter queries for Nomi. All three sets run against the same
// grammar — the tour and Helix read tree-sitter-nomi/queries directly, Zed
// keeps a reverse-priority copy — so one symbol table validates all of them.
var grammarQueryDirs = []string{
	"../tree-sitter-nomi/queries",
	"../editors/zed/languages/nomi",
	"../editors/helix/runtime/queries/nomi",
}

const grammarRebuildInstruction = "run `make build-tour-grammar-wasm` and commit tree-sitter-nomi/tree-sitter-nomi.wasm"

// TestTourGrammarWasmIsTheGeneratedParser fails when the committed grammar
// wasm was built from a different `src/parser.c` than the one committed
// beside it.
//
// It goes red as soon as the grammar gains or renames a node and the wasm is
// not rebuilt, whether or not any query mentions that node yet.
//
// It compares the wasm's own `TSLanguage` against parser.c on two axes:
//
//   - the ten scalar header fields, each against parser.c's `#define`
//   - the full `ts_symbol_names` and `ts_field_names` tables, in symbol
//     order, element by element
//
// That is the parser's entire externally-visible vocabulary, and it is where
// a query resolves its names, so it covers the whole class that breaks
// highlighting. What it does not cover is the parse tables and the external
// scanner: a grammar edit that reshuffles states, or an edit to
// src/scanner.c, can leave every count and every name identical.
//
// Rebuilding the wasm and comparing bytes would be a stronger check. It is
// not done, for these reasons:
//
//   - The rebuild is reproducible: rebuilding with the CLI vendored in
//     tree-sitter-nomi/node_modules reproduces the committed artifact byte
//     for byte.
//   - It needs Docker or emscripten. The whole reason
//     scripts/build-tour-wasm.sh copies the wasm is that the
//     deployment image cannot build one, and `make deploy-tour` runs on a
//     developer machine, so a required rebuild would make deploying
//     impossible on any machine without Docker.
//   - So it would have to skip when the toolchain is absent, and unlike
//     tour_bundle_test.go's absent-bundle skip, the skip condition cannot
//     be made disjoint from the failure condition. Staleness lives in the
//     artifact and the skip lives in the toolchain; they are independent,
//     so a machine with no Docker and a stale wasm is silent. The
//     NOMI_REQUIRE_TOUR_WASM escape does not rescue it either, for the
//     deploy-path reason above.
//   - What it would add is changes to the parse tables or the external
//     scanner that leave the node vocabulary intact. Those are rare next to
//     changes that move a header count or a name, and they cannot break query
//     compilation, which is the failure this file guards against.
//
// A developer who wants the byte answer already has it without any test:
// `make build-tour-grammar-wasm && git diff --stat tree-sitter-nomi/`.
func TestTourGrammarWasmIsTheGeneratedParser(t *testing.T) {
	lang := readGrammarWasm(t)
	gen := readGeneratedParser(t)

	for _, row := range []struct {
		name string
		wasm uint32
		def  string
	}{
		{"version", lang.version, "LANGUAGE_VERSION"},
		{"symbol_count", lang.symbolCount, "SYMBOL_COUNT"},
		{"alias_count", lang.aliasCount, "ALIAS_COUNT"},
		{"token_count", lang.tokenCount, "TOKEN_COUNT"},
		{"external_token_count", lang.externalTokenCount, "EXTERNAL_TOKEN_COUNT"},
		{"state_count", lang.stateCount, "STATE_COUNT"},
		{"large_state_count", lang.largeStateCount, "LARGE_STATE_COUNT"},
		{"production_id_count", lang.productionIDCount, "PRODUCTION_ID_COUNT"},
		{"field_count", lang.fieldCount, "FIELD_COUNT"},
		{"max_alias_sequence_length", uint32(lang.maxAliasSequenceLength), "MAX_ALIAS_SEQUENCE_LENGTH"},
	} {
		want, ok := gen.defines[row.def]
		if !ok {
			t.Fatalf("%s/src/parser.c has no #define %s; the generated parser is not the shape this check was written against", grammarDir, row.def)
		}
		if row.wasm != want {
			t.Errorf(`COMMITTED GRAMMAR WASM IS STALE.

  TSLanguage.%s reads %d in %s/tree-sitter-nomi.wasm
  #define %s is %d in %s/src/parser.c

The wasm was built from a different parser.c than the one committed beside
it. scripts/build-tour-wasm.sh COPIES this file rather than building it, so
`+"`tree-sitter generate`"+` does not refresh it and neither does
scripts/sync-zed-grammar.sh. Fix: %s`,
				row.name, row.wasm, grammarDir,
				row.def, want, grammarDir,
				grammarRebuildInstruction)
		}
	}
	if t.Failed() {
		// The tables below are indexed by the counts above. Comparing them
		// after a count mismatch reports the same one fact hundreds of times.
		return
	}

	compareGrammarTable(t, "ts_symbol_names", lang.symbolNames, gen.symbolNames)
	compareGrammarTable(t, "ts_field_names", lang.fieldNames, gen.fieldNames)
	t.Logf("committed wasm matches src/parser.c: ABI %d, %d symbols (+%d alias), %d fields, %d states",
		lang.version, lang.symbolCount, lang.aliasCount, lang.fieldCount, lang.stateCount)
}

// compareGrammarTable reports the first few differing entries by index rather
// than dumping two 307-element lists.
func compareGrammarTable(t *testing.T, what string, wasm, generated []string) {
	t.Helper()
	if len(wasm) != len(generated) {
		t.Errorf("%s has %d entries in the committed wasm and %d in src/parser.c. Fix: %s",
			what, len(wasm), len(generated), grammarRebuildInstruction)
		return
	}
	var diffs []string
	for i := range wasm {
		if wasm[i] != generated[i] {
			diffs = append(diffs, fmt.Sprintf("    [%d] wasm %q  parser.c %q", i, wasm[i], generated[i]))
		}
	}
	if len(diffs) == 0 {
		t.Logf("%s matches src/parser.c (%d entries)", what, len(wasm))
		return
	}
	shown := diffs
	if len(shown) > 10 {
		shown = append(append([]string{}, shown[:10]...), fmt.Sprintf("    ... and %d more", len(diffs)-10))
	}
	t.Errorf(`COMMITTED GRAMMAR WASM IS STALE.

  %s differs from %s/src/parser.c in %d of %d entries:
%s

Fix: %s`, what, grammarDir, len(diffs), len(wasm), strings.Join(shown, "\n"), grammarRebuildInstruction)
}

// TestTourGrammarQueriesNameRealNodes fails when a committed `.scm` names a
// node, anonymous token or field the committed grammar wasm does not have.
//
// This is the user-visible failure stated in its own terms. The browser
// compiles queries/highlights.scm against tree-sitter-nomi.wasm on every tour
// page, and one unknown name makes `ts_query_new` fail, which drops all
// highlighting rather than one rule. The same names are also what Zed and
// Helix compile, so all twelve query files are checked against the same
// table, rather than relying on validating them by hand.
//
// Checked per reference: named nodes `(foo)`, anonymous tokens `"fn"`, field
// names `name:` and negated fields `!interface`. Not checked, because the
// language does not resolve them through the symbol table: `_` and `(_)`
// wildcards, the built-in `ERROR` and `MISSING` nodes (confirmed absent from
// ts_symbol_names, so exempting them is required rather than convenient),
// capture names, and everything inside a `(#predicate? ...)` form — a
// predicate's bare words are its own arguments (`(#set! tag nomi-run)`), not
// node names.
func TestTourGrammarQueriesNameRealNodes(t *testing.T) {
	lang := readGrammarWasm(t)

	nodes := make(map[string]bool, len(lang.symbolNames))
	for _, n := range lang.symbolNames {
		nodes[n] = true
	}
	fields := make(map[string]bool, len(lang.fieldNames))
	for _, f := range lang.fieldNames {
		if f != "" {
			fields[f] = true
		}
	}

	files := grammarQueryFiles(t)
	totalNodes, totalFields := 0, 0
	// A scanner that silently matched nothing would report zero missing
	// names for every file, which is the same output as a clean tree. Each
	// file therefore has to yield at least one reference, and the per-file
	// counts are logged so the totals can be checked against a hand count
	// (the four editors/zed files were hand-validated at 112 references;
	// this prints the same four numbers).
	for _, path := range files {
		path := path
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			refs, err := scanQueryReferences(string(src))
			if err != nil {
				t.Fatalf(`QUERY FILE DOES NOT PARSE.

  %s: %v

tree-sitter would reject this file outright, taking every pattern in it down
with the malformed one.`, path, err)
			}
			if len(refs) == 0 {
				t.Fatalf("%s produced no node or field references. Every committed query names at least one node, so an empty scan means the scanner stopped early rather than that the file is clean.", path)
			}
			fileNodes, fileFields := 0, 0
			for _, ref := range refs {
				if ref.kind == refNode {
					fileNodes++
				} else {
					fileFields++
				}
			}
			t.Logf("%s: %d node references, %d field references", path, fileNodes, fileFields)
			totalNodes += fileNodes
			totalFields += fileFields
			for _, ref := range refs {
				switch ref.kind {
				case refNode:
					if !nodes[ref.name] {
						t.Errorf(`QUERY NAMES A NODE THE COMMITTED GRAMMAR DOES NOT HAVE.

  %s:%d names %q

tree-sitter fails the WHOLE query file on an unknown node name, so this drops
every other pattern in it too: the tour renders as unhighlighted plain text,
and Zed/Helix lose Nomi highlighting entirely.

Either the name is wrong, or %s/tree-sitter-nomi.wasm is older than the
grammar that introduced it. build-tour-wasm.sh COPIES that wasm rather than
building it, so it does not move when src/parser.c is regenerated. If the
name is right: %s`,
							path, ref.line, ref.name, grammarDir, grammarRebuildInstruction)
					}
				case refField:
					if !fields[ref.name] {
						t.Errorf(`QUERY NAMES A FIELD THE COMMITTED GRAMMAR DOES NOT HAVE.

  %s:%d names field %q

Same blast radius as an unknown node: the whole file fails to compile.
Known fields: %s

Fix the name, or if the grammar has it: %s`,
							path, ref.line, ref.name, strings.Join(sortedKeys(fields), " "), grammarRebuildInstruction)
					}
				}
			}
		})
	}
	t.Logf("%d query files: %d node references and %d field references, all resolved against the committed wasm's %d symbols and %d fields",
		len(files), totalNodes, totalFields, len(lang.symbolNames), len(fields))
}

// grammarQueryFiles enumerates the committed `.scm` files. Globbing rather
// than listing them means a query file added later is covered without anyone
// remembering this test; an empty or missing directory is a failure, so a
// moved query directory cannot make the row pass vacuously by matching
// nothing.
func grammarQueryFiles(t *testing.T) []string {
	t.Helper()
	var all []string
	for _, dir := range grammarQueryDirs {
		found, err := filepath.Glob(filepath.Join(dir, "*.scm"))
		if err != nil {
			t.Fatalf("globbing %s: %v", dir, err)
		}
		if len(found) == 0 {
			t.Fatalf("%s holds no .scm files. Either the query directory moved (update grammarQueryDirs) or the checkout is broken; an empty match would make this test pass by checking nothing.", dir)
		}
		sort.Strings(found)
		all = append(all, found...)
	}
	return all
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestTourRunWorkerImportsAreExplicitlyRelative pins one defect. It is not
// coverage of the class the defect belongs to, and it should not be read as
// such.
//
// The class is "browser-only loading semantics that no host in this
// repository exercises". run-worker.js is a classic Web Worker; the only
// thing that ever executes its `importScripts` is a browser.
// TestTourWasmAnswersTheTourBlocks loads the same staged bundle under node
// and reaches nomi.wasm without going through importScripts at all. When
// Chromium rejects `importScripts("wasm_exec.js")` as an invalid bare
// specifier, every runnable block in the dev server and in the built dist
// stops working, and every other test in the repository stays green. Covering the class
// needs a browser, and a browser dependency is not worth it for a page with
// one worker.
//
// What is worth it is making that one specifier unable to regress. The rule
// is a real requirement rather than a photograph of the current text: a
// worker's importScripts argument is resolved as a URL, and a bare word is
// not a valid relative URL in Chromium.
//
// Comments are stripped first. run-worker.js explains the defect by quoting
// it: `importScripts("wasm_exec.js")` appears in the explanatory comment two
// lines above the fixed call, and a plain scan of the file text would report
// that comment as the violation against a file that is correct. Stripping
// first also removes the opposite error, where a fix commented out still
// reads as present.
func TestTourRunWorkerImportsAreExplicitlyRelative(t *testing.T) {
	const worker = "../tour/src/lib/run-worker.js"
	raw, err := os.ReadFile(worker)
	if err != nil {
		t.Fatalf("reading %s: %v", worker, err)
	}
	src := stripJSComments(string(raw))
	calls := regexp.MustCompile(`importScripts\(\s*"([^"]*)"`).FindAllStringSubmatch(src, -1)
	if len(calls) == 0 {
		t.Fatalf("%s has no importScripts call outside its comments. If the worker stopped being a classic worker this test is obsolete; delete it deliberately rather than leaving it matching nothing.", worker)
	}
	bad := 0
	for _, c := range calls {
		spec := c[1]
		if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/") || strings.Contains(spec, "://") {
			continue
		}
		bad++
		t.Errorf(`WORKER IMPORT IS A BARE SPECIFIER.

  %s: importScripts(%q)

Chromium resolves this argument as a URL and rejects a bare word:
`+"`SyntaxError: The URL '%s' is invalid`"+`. The worker then fails to load and
every runnable tour block shows "worker error:" instead of its output, in the
dev server and in the built dist alike. Nothing else in this repository can
see it: the bundle tests load the same files under node, which never goes
through importScripts.

Fix: write it as "./%s".`, worker, spec, spec, spec)
	}
	if bad == 0 {
		t.Logf("%d importScripts specifier(s) in %s, all explicitly relative", len(calls), filepath.Base(worker))
	}
}

// stripJSComments blanks out `//` and block comments, leaving string and
// template literals intact so a URL's `//` is not mistaken for one.
// Replacing comment bytes with spaces rather than deleting them keeps every
// other offset in the file where it was.
func stripJSComments(src string) string {
	out := []byte(src)
	blank := func(i int) {
		if out[i] != '\n' {
			out[i] = ' '
		}
	}
	for i := 0; i < len(src); i++ {
		switch c := src[i]; c {
		case '"', '\'', '`':
			quote := c
			i++
			for i < len(src) && src[i] != quote {
				if src[i] == '\\' {
					i++
				}
				if i < len(src) && src[i] == '\n' && quote != '`' {
					break
				}
				i++
			}
		case '/':
			if i+1 >= len(src) {
				continue
			}
			switch src[i+1] {
			case '/':
				for ; i < len(src) && src[i] != '\n'; i++ {
					blank(i)
				}
			case '*':
				blank(i)
				blank(i + 1)
				i += 2
				for ; i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/'); i++ {
					blank(i)
				}
				if i+1 < len(src) {
					blank(i)
					blank(i + 1)
					i++
				}
			}
		}
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// The committed grammar wasm, read as data.
// ---------------------------------------------------------------------------

// tsLanguage is the subset of `struct TSLanguage` this file reads. Field
// order and offsets come from tree-sitter-nomi/src/tree_sitter/parser.h,
// which is committed, for ABI version 14 on wasm32 (4-byte pointers).
type tsLanguage struct {
	version                uint32
	symbolCount            uint32
	aliasCount             uint32
	tokenCount             uint32
	externalTokenCount     uint32
	stateCount             uint32
	largeStateCount        uint32
	productionIDCount      uint32
	fieldCount             uint32
	maxAliasSequenceLength uint16

	// symbolNames has symbolCount+aliasCount entries, in symbol order.
	symbolNames []string
	// fieldNames has fieldCount+1 entries; index 0 is NULL, read as "".
	fieldNames []string
}

// Byte offsets into struct TSLanguage on wasm32. Nine uint32 run 0..35, then
// a uint16 at 36 with two bytes of padding, then the pointers from 40.
const (
	offVersion            = 0
	offSymbolCount        = 4
	offAliasCount         = 8
	offTokenCount         = 12
	offExternalTokenCount = 16
	offStateCount         = 20
	offLargeStateCount    = 24
	offProductionIDCount  = 28
	offFieldCount         = 32
	offMaxAliasSeqLen     = 36
	offSymbolNames        = 56 // after parse_table, small_parse_table, small_parse_table_map, parse_actions
	offFieldNames         = 60
)

// tsLanguageABI is the only ABI whose layout the offsets above describe.
// parser.h changes field order between ABI versions, so a different version
// must fail loudly rather than read garbage.
const tsLanguageABI = 14

func readGrammarWasm(t *testing.T) tsLanguage {
	t.Helper()
	path := filepath.Join(grammarDir, "tree-sitter-nomi.wasm")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(`COMMITTED GRAMMAR WASM IS MISSING.

  %s: %v

This is a committed file (force-added past tree-sitter-nomi/.gitignore's
*.wasm), and the tour serves it to every visitor. Its absence is a broken
checkout, not a state this check may skip on.`, path, err)
	}
	lang, err := parseGrammarWasm(raw)
	if err != nil {
		t.Fatalf(`CANNOT READ THE COMMITTED GRAMMAR WASM.

  %s: %v

This check decodes the module's `+"`tree_sitter_nomi`"+` export and reads the
static TSLanguage it returns (see the file header). A decode failure means
the artifact is damaged, or it was produced by a toolchain that emits a shape
this reader does not know — in which case fix the reader; do not weaken it to
a string scan, which would report nodes present that the parser lacks.`, path, err)
	}
	if lang.version != tsLanguageABI {
		t.Fatalf(`GRAMMAR WASM USES AN ABI THIS CHECK CANNOT READ.

  %s declares TSLanguage.version = %d; this reader knows ABI %d only.

struct TSLanguage's field order changes between ABI versions, so every offset
below the version field would be wrong. Update the offsets against
%s/src/tree_sitter/parser.h and re-run.`, path, lang.version, tsLanguageABI, grammarDir)
	}
	return lang
}

func parseGrammarWasm(b []byte) (tsLanguage, error) {
	var zero tsLanguage
	if len(b) < 8 || string(b[:4]) != "\x00asm" {
		return zero, fmt.Errorf("not a wasm module")
	}
	if v := binary.LittleEndian.Uint32(b[4:8]); v != 1 {
		return zero, fmt.Errorf("wasm binary version %d, want 1", v)
	}

	var importSec, exportSec, codeSec, dataSec []byte
	p := 8
	for p < len(b) {
		id := b[p]
		p++
		size, n, err := uleb(b[p:])
		if err != nil {
			return zero, fmt.Errorf("section %d size: %w", id, err)
		}
		p += n
		if p+int(size) > len(b) {
			return zero, fmt.Errorf("section %d runs past end of module", id)
		}
		body := b[p : p+int(size)]
		switch id {
		case 2:
			importSec = body
		case 7:
			exportSec = body
		case 10:
			codeSec = body
		case 11:
			dataSec = body
		}
		p += int(size)
	}
	for name, sec := range map[string][]byte{"import": importSec, "export": exportSec, "code": codeSec, "data": dataSec} {
		if sec == nil {
			return zero, fmt.Errorf("module has no %s section", name)
		}
	}

	importedFuncs, err := countImportedFuncs(importSec)
	if err != nil {
		return zero, err
	}
	langFuncIdx, err := exportedFuncIndex(exportSec, "tree_sitter_nomi")
	if err != nil {
		return zero, err
	}
	if langFuncIdx < importedFuncs {
		return zero, fmt.Errorf("tree_sitter_nomi resolves to imported function %d", langFuncIdx)
	}
	body, err := functionBody(codeSec, langFuncIdx-importedFuncs)
	if err != nil {
		return zero, err
	}
	addr, err := constantAddress(body)
	if err != nil {
		return zero, fmt.Errorf("decoding tree_sitter_nomi's body: %w", err)
	}
	image, err := dataImage(dataSec)
	if err != nil {
		return zero, err
	}

	u32 := func(off uint32) (uint32, error) {
		if int(off)+4 > len(image) {
			return 0, fmt.Errorf("read at %d runs past the %d-byte data image", off, len(image))
		}
		return binary.LittleEndian.Uint32(image[off : off+4]), nil
	}
	read := func(field uint32) uint32 {
		v, e := u32(addr + field)
		if e != nil && err == nil {
			err = fmt.Errorf("TSLanguage+%d: %w", field, e)
		}
		return v
	}
	var lang tsLanguage
	lang.version = read(offVersion)
	lang.symbolCount = read(offSymbolCount)
	lang.aliasCount = read(offAliasCount)
	lang.tokenCount = read(offTokenCount)
	lang.externalTokenCount = read(offExternalTokenCount)
	lang.stateCount = read(offStateCount)
	lang.largeStateCount = read(offLargeStateCount)
	lang.productionIDCount = read(offProductionIDCount)
	lang.fieldCount = read(offFieldCount)
	if err != nil {
		return zero, err
	}
	if int(addr+offMaxAliasSeqLen)+2 > len(image) {
		return zero, fmt.Errorf("TSLanguage+%d runs past the data image", offMaxAliasSeqLen)
	}
	lang.maxAliasSequenceLength = binary.LittleEndian.Uint16(image[addr+offMaxAliasSeqLen:])

	// Guard the table reads before allocating from a count read out of the
	// module. A damaged artifact should report a decode error, not consume
	// memory proportional to a garbage uint32.
	const sane = 1 << 20
	if lang.symbolCount+lang.aliasCount > sane || lang.fieldCount > sane {
		return zero, fmt.Errorf("implausible symbol_count=%d alias_count=%d field_count=%d; the struct address (%d) is probably wrong",
			lang.symbolCount, lang.aliasCount, lang.fieldCount, addr)
	}

	symbolNamesPtr := read(offSymbolNames)
	fieldNamesPtr := read(offFieldNames)
	if err != nil {
		return zero, err
	}
	lang.symbolNames, err = readCStringTable(image, symbolNamesPtr, int(lang.symbolCount+lang.aliasCount), false)
	if err != nil {
		return zero, fmt.Errorf("ts_symbol_names: %w", err)
	}
	lang.fieldNames, err = readCStringTable(image, fieldNamesPtr, int(lang.fieldCount)+1, true)
	if err != nil {
		return zero, fmt.Errorf("ts_field_names: %w", err)
	}
	return lang, nil
}

// readCStringTable follows an array of `count` 4-byte pointers into the data
// image and reads a NUL-terminated string at each. allowNull covers
// ts_field_names, whose index 0 is literally NULL.
func readCStringTable(image []byte, base uint32, count int, allowNull bool) ([]string, error) {
	out := make([]string, 0, count)
	for i := range count {
		off := base + uint32(4*i)
		if int(off)+4 > len(image) {
			return nil, fmt.Errorf("entry %d at %d runs past the %d-byte data image", i, off, len(image))
		}
		ptr := binary.LittleEndian.Uint32(image[off : off+4])
		if ptr == 0 {
			if !allowNull {
				return nil, fmt.Errorf("entry %d is NULL", i)
			}
			out = append(out, "")
			continue
		}
		if int(ptr) >= len(image) {
			return nil, fmt.Errorf("entry %d points at %d, past the %d-byte data image", i, ptr, len(image))
		}
		end := int(ptr)
		for end < len(image) && image[end] != 0 {
			end++
		}
		if end == len(image) {
			return nil, fmt.Errorf("entry %d is unterminated", i)
		}
		out = append(out, string(image[ptr:end]))
	}
	return out, nil
}

func countImportedFuncs(sec []byte) (uint32, error) {
	p := 0
	n, adv, err := uleb(sec[p:])
	if err != nil {
		return 0, fmt.Errorf("import count: %w", err)
	}
	p += adv
	var funcs uint32
	for i := range n {
		for range 2 { // module name, then field name
			l, a, err := uleb(sec[p:])
			if err != nil {
				return 0, fmt.Errorf("import %d name: %w", i, err)
			}
			p += a + int(l)
		}
		if p >= len(sec) {
			return 0, fmt.Errorf("import %d: truncated", i)
		}
		kind := sec[p]
		p++
		switch kind {
		case 0x00: // func: type index
			_, a, err := uleb(sec[p:])
			if err != nil {
				return 0, err
			}
			p += a
			funcs++
		case 0x01: // table: reftype + limits
			p++
			var a int
			flags := sec[p]
			p++
			_, a, err = uleb(sec[p:])
			if err != nil {
				return 0, err
			}
			p += a
			if flags&1 != 0 {
				_, a, err = uleb(sec[p:])
				if err != nil {
					return 0, err
				}
				p += a
			}
		case 0x02: // memory: limits
			flags := sec[p]
			p++
			_, a, err := uleb(sec[p:])
			if err != nil {
				return 0, err
			}
			p += a
			if flags&1 != 0 {
				_, a, err = uleb(sec[p:])
				if err != nil {
					return 0, err
				}
				p += a
			}
		case 0x03: // global: valtype + mutability
			p += 2
		default:
			return 0, fmt.Errorf("import %d has unknown kind 0x%02x", i, kind)
		}
	}
	return funcs, nil
}

func exportedFuncIndex(sec []byte, want string) (uint32, error) {
	p := 0
	n, adv, err := uleb(sec[p:])
	if err != nil {
		return 0, fmt.Errorf("export count: %w", err)
	}
	p += adv
	for i := range n {
		l, a, err := uleb(sec[p:])
		if err != nil {
			return 0, err
		}
		p += a
		if p+int(l) > len(sec) {
			return 0, fmt.Errorf("export %d name truncated", i)
		}
		name := string(sec[p : p+int(l)])
		p += int(l)
		kind := sec[p]
		p++
		idx, a, err := uleb(sec[p:])
		if err != nil {
			return 0, err
		}
		p += a
		if name == want {
			if kind != 0 {
				return 0, fmt.Errorf("export %q is kind 0x%02x, not a function", want, kind)
			}
			return idx, nil
		}
	}
	return 0, fmt.Errorf("module does not export %q", want)
}

func functionBody(sec []byte, idx uint32) ([]byte, error) {
	p := 0
	n, adv, err := uleb(sec[p:])
	if err != nil {
		return nil, fmt.Errorf("code count: %w", err)
	}
	p += adv
	if idx >= n {
		return nil, fmt.Errorf("code section has %d bodies, want index %d", n, idx)
	}
	for i := range n {
		size, a, err := uleb(sec[p:])
		if err != nil {
			return nil, err
		}
		p += a
		if p+int(size) > len(sec) {
			return nil, fmt.Errorf("body %d truncated", i)
		}
		if i == idx {
			return sec[p : p+int(size)], nil
		}
		p += int(size)
	}
	return nil, fmt.Errorf("unreachable")
}

// constantAddress decodes the body of a function whose whole job is to return
// the address of a static. Emscripten emits one of two shapes for a side
// module, depending on whether the static is relocated against
// `__memory_base`:
//
//	(local decls = 0) global.get $__memory_base  i32.const N  i32.add  end
//	(local decls = 0) i32.const N  end
//
// Either way the answer is N, because the data segment is itself placed at
// `__memory_base` and pointers stored inside it are relocated by the same
// amount. Reading the image as if the base were zero makes every pointer a
// direct index into it.
func constantAddress(body []byte) (uint32, error) {
	p := 0
	decls, a, err := uleb(body[p:])
	if err != nil {
		return 0, fmt.Errorf("local declarations: %w", err)
	}
	p += a
	if decls != 0 {
		return 0, fmt.Errorf("function has %d local declaration groups; expected a body that only returns a constant", decls)
	}
	if p < len(body) && body[p] == 0x23 { // global.get
		p++
		_, a, err := uleb(body[p:])
		if err != nil {
			return 0, err
		}
		p += a
	}
	if p >= len(body) || body[p] != 0x41 { // i32.const
		return 0, fmt.Errorf("expected i32.const at offset %d, found 0x%02x", p, safeByte(body, p))
	}
	p++
	v, a, err := sleb(body[p:])
	if err != nil {
		return 0, err
	}
	p += a
	if v < 0 {
		return 0, fmt.Errorf("negative address %d", v)
	}
	if p < len(body) && body[p] == 0x6a { // i32.add
		p++
	}
	if p >= len(body) || body[p] != 0x0b { // end
		return 0, fmt.Errorf("expected `end` at offset %d, found 0x%02x; the body does more than return a constant", p, safeByte(body, p))
	}
	return uint32(v), nil
}

func safeByte(b []byte, i int) byte {
	if i < 0 || i >= len(b) {
		return 0
	}
	return b[i]
}

// dataImage returns the module's single active data segment. Requiring
// exactly one, at offset `global.get 0` (the imported `env.__memory_base`),
// is what makes "treat the base as zero" correct: every pointer stored in
// the segment is `__memory_base + offset-into-this-image`.
func dataImage(sec []byte) ([]byte, error) {
	p := 0
	n, a, err := uleb(sec[p:])
	if err != nil {
		return nil, fmt.Errorf("data segment count: %w", err)
	}
	p += a
	if n != 1 {
		return nil, fmt.Errorf("module has %d data segments; this reader assumes the single-segment layout emscripten emits for a side module", n)
	}
	flags, a, err := uleb(sec[p:])
	if err != nil {
		return nil, err
	}
	p += a
	if flags != 0 {
		return nil, fmt.Errorf("data segment flags %d; want 0 (active, memory 0)", flags)
	}
	if p >= len(sec) {
		return nil, fmt.Errorf("data segment offset expression truncated")
	}
	switch sec[p] {
	case 0x23: // global.get — the __memory_base import
		p++
		g, a, err := uleb(sec[p:])
		if err != nil {
			return nil, err
		}
		p += a
		if g != 0 {
			return nil, fmt.Errorf("data segment is based on global %d; want global 0 (__memory_base)", g)
		}
	case 0x41: // i32.const
		p++
		v, a, err := sleb(sec[p:])
		if err != nil {
			return nil, err
		}
		p += a
		if v != 0 {
			return nil, fmt.Errorf("data segment sits at a non-zero constant offset %d; pointers into it would need that bias applied", v)
		}
	default:
		return nil, fmt.Errorf("data segment offset opcode 0x%02x is neither global.get nor i32.const", sec[p])
	}
	if p >= len(sec) || sec[p] != 0x0b {
		return nil, fmt.Errorf("data segment offset expression does not end at %d", p)
	}
	p++
	l, a, err := uleb(sec[p:])
	if err != nil {
		return nil, err
	}
	p += a
	if p+int(l) > len(sec) {
		return nil, fmt.Errorf("data segment of %d bytes runs past the section", l)
	}
	return sec[p : p+int(l)], nil
}

func uleb(b []byte) (uint32, int, error) {
	var result uint32
	var shift uint
	for i := range b {
		if shift >= 32 {
			return 0, 0, fmt.Errorf("LEB128 value too wide for uint32")
		}
		result |= uint32(b[i]&0x7f) << shift
		if b[i]&0x80 == 0 {
			return result, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, fmt.Errorf("truncated LEB128")
}

func sleb(b []byte) (int32, int, error) {
	var result int32
	var shift uint
	for i := range b {
		if shift >= 32 {
			return 0, 0, fmt.Errorf("LEB128 value too wide for int32")
		}
		result |= int32(b[i]&0x7f) << shift
		shift += 7
		if b[i]&0x80 == 0 {
			if shift < 32 && b[i]&0x40 != 0 {
				result |= -1 << shift
			}
			return result, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("truncated LEB128")
}

// ---------------------------------------------------------------------------
// The generated parser, read as text.
// ---------------------------------------------------------------------------

type generatedParser struct {
	defines     map[string]uint32
	symbolNames []string
	fieldNames  []string
}

var (
	defineRe = regexp.MustCompile(`(?m)^#define ([A-Z_]+) (\d+)$`)
	// `  [sym_identifier] = "identifier",` and `  [0] = NULL,`
	tableEntryRe = regexp.MustCompile(`(?m)^\s*\[([A-Za-z0-9_]+)\] = (NULL|"(?:[^"\\]|\\.)*"),$`)
)

func readGeneratedParser(t *testing.T) generatedParser {
	t.Helper()
	path := filepath.Join(grammarDir, "src", "parser.c")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(`GENERATED PARSER IS MISSING.

  %s: %v

It is committed, and it is the preimage of the committed wasm. Its absence is
a broken checkout, not a state this check may skip on.`, path, err)
	}
	src := string(raw)

	out := generatedParser{defines: map[string]uint32{}}
	for _, m := range defineRe.FindAllStringSubmatch(src, -1) {
		v, err := strconv.ParseUint(m[2], 10, 32)
		if err != nil {
			continue
		}
		out.defines[m[1]] = uint32(v)
	}
	out.symbolNames = readParserTable(t, src, "ts_symbol_names")
	out.fieldNames = readParserTable(t, src, "ts_field_names")
	return out
}

// readParserTable extracts a `static const char * const NAME[] = { ... };`
// designated-initializer table, in declaration order. tree-sitter emits these
// in symbol order, which the wasm comparison relies on; a table that were not
// in order would show up as a diff at nearly every index rather than as a
// silent pass.
func readParserTable(t *testing.T, src, name string) []string {
	t.Helper()
	head := "static const char * const " + name + "[] = {"
	start := strings.Index(src, head)
	if start < 0 {
		t.Fatalf("%s/src/parser.c has no %s table; the generated parser is not the shape this check was written against", grammarDir, name)
	}
	start += len(head)
	end := strings.Index(src[start:], "\n};")
	if end < 0 {
		t.Fatalf("%s table in %s/src/parser.c is unterminated", name, grammarDir)
	}
	body := src[start : start+end]
	var out []string
	for _, m := range tableEntryRe.FindAllStringSubmatch(body, -1) {
		if m[2] == "NULL" {
			out = append(out, "")
			continue
		}
		unquoted, err := strconv.Unquote(m[2])
		if err != nil {
			t.Fatalf("%s entry %s in %s/src/parser.c does not unquote: %v", name, m[2], grammarDir, err)
		}
		out = append(out, unquoted)
	}
	if len(out) == 0 {
		t.Fatalf("%s table in %s/src/parser.c parsed as empty; an empty table would make the comparison vacuous", name, grammarDir)
	}
	return out
}

// ---------------------------------------------------------------------------
// Query files, read as s-expressions.
// ---------------------------------------------------------------------------

type queryRefKind int

const (
	refNode queryRefKind = iota
	refField
)

type queryRef struct {
	kind queryRefKind
	name string
	line int
}

// queryWildcards are the names a query may use that the symbol table does not
// hold. `_` is the wildcard; `ERROR` and `MISSING` are built-ins the query
// compiler resolves itself, and both are absent from this grammar's
// ts_symbol_names, so exempting them is required for the check to be correct
// rather than a convenience.
var queryWildcards = map[string]bool{"_": true, "ERROR": true, "MISSING": true}

// scanQueryReferences parses a tree-sitter query far enough to enumerate the
// names it asks the language to resolve: named nodes, anonymous tokens
// (written as string literals in node position), field names (`name:`) and
// negated field names (`!name`).
//
// Everything inside a `(#predicate? ...)` form is skipped. A predicate's
// arguments are bare words and strings that belong to the predicate, not to
// the grammar: `(#set! tag nomi-run)` would otherwise report `tag` and
// `nomi-run` as missing nodes, and `(#eq? @tag "Toml")` would report `Toml`
// as a missing anonymous token.
func scanQueryReferences(src string) ([]queryRef, error) {
	s := &queryScanner{src: src, line: 1}
	var refs []queryRef
	for {
		s.skipSpace()
		if s.eof() {
			return refs, nil
		}
		if err := s.pattern(&refs); err != nil {
			return nil, err
		}
	}
}

type queryScanner struct {
	src  string
	pos  int
	line int
}

func (s *queryScanner) eof() bool { return s.pos >= len(s.src) }

func (s *queryScanner) skipSpace() {
	for !s.eof() {
		c := s.src[s.pos]
		switch {
		case c == '\n':
			s.line++
			s.pos++
		case c == ' ' || c == '\t' || c == '\r':
			s.pos++
		case c == ';':
			for !s.eof() && s.src[s.pos] != '\n' {
				s.pos++
			}
		default:
			return
		}
	}
}

// pattern consumes exactly one query item at the current position.
func (s *queryScanner) pattern(refs *[]queryRef) error {
	s.skipSpace()
	if s.eof() {
		return nil
	}
	start := s.line
	switch c := s.src[s.pos]; {
	case c == '(':
		s.pos++
		s.skipSpace()
		if !s.eof() && s.src[s.pos] == '#' {
			return s.skipToClose('(', ')', start)
		}
		// A leading identifier names the node; anything else (a nested `(`,
		// a `[`, a string, a field) makes this a grouping.
		if name, line, ok := s.identifier(); ok {
			s.skipSpace()
			if !s.eof() && s.src[s.pos] == ':' {
				// Not a node after all: `(name: (_) @x)` is a grouping whose
				// first item is a field. Fall through to the child loop with
				// the field already recorded.
				s.pos++
				if !queryWildcards[name] {
					*refs = append(*refs, queryRef{refField, name, line})
				}
			} else if !queryWildcards[name] {
				*refs = append(*refs, queryRef{refNode, name, line})
			}
		}
		if err := s.children(refs, ')', start); err != nil {
			return err
		}
	case c == '[':
		s.pos++
		if err := s.children(refs, ']', start); err != nil {
			return err
		}
	case c == '"':
		lit, line, err := s.stringLiteral()
		if err != nil {
			return err
		}
		*refs = append(*refs, queryRef{refNode, lit, line})
	case c == '!':
		s.pos++
		name, line, ok := s.identifier()
		if !ok {
			return fmt.Errorf("line %d: `!` is not followed by a field name", s.line)
		}
		*refs = append(*refs, queryRef{refField, name, line})
	case c == '@':
		s.pos++
		s.identifier() // capture name; not resolved against the grammar
	case c == '.' || c == '?' || c == '*' || c == '+':
		s.pos++ // anchor and quantifiers
	case c == ')' || c == ']':
		return fmt.Errorf("line %d: unbalanced %q", s.line, string(c))
	default:
		name, line, ok := s.identifier()
		if !ok {
			return fmt.Errorf("line %d: unexpected %q", s.line, string(c))
		}
		s.skipSpace()
		if !s.eof() && s.src[s.pos] == ':' {
			s.pos++
			if !queryWildcards[name] {
				*refs = append(*refs, queryRef{refField, name, line})
			}
			return s.pattern(refs) // the field's value
		}
		if !queryWildcards[name] {
			*refs = append(*refs, queryRef{refNode, name, line})
		}
	}
	return nil
}

func (s *queryScanner) children(refs *[]queryRef, closer byte, openedAt int) error {
	for {
		s.skipSpace()
		if s.eof() {
			return fmt.Errorf("line %d: unclosed %q", openedAt, string(closer))
		}
		if s.src[s.pos] == closer {
			s.pos++
			return nil
		}
		if s.src[s.pos] == ')' || s.src[s.pos] == ']' {
			return fmt.Errorf("line %d: %q closes a form opened on line %d with %q", s.line, string(s.src[s.pos]), openedAt, string(closer))
		}
		if err := s.pattern(refs); err != nil {
			return err
		}
	}
}

// skipToClose consumes a predicate form, honouring nesting and strings so a
// `)` inside `(#match? @x ")")` does not end it early.
func (s *queryScanner) skipToClose(open, close byte, openedAt int) error {
	depth := 1
	for !s.eof() {
		switch c := s.src[s.pos]; c {
		case '"':
			if _, _, err := s.stringLiteral(); err != nil {
				return err
			}
			continue
		case ';':
			for !s.eof() && s.src[s.pos] != '\n' {
				s.pos++
			}
			continue
		case '\n':
			s.line++
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				s.pos++
				return nil
			}
		}
		s.pos++
	}
	return fmt.Errorf("line %d: unclosed predicate", openedAt)
}

func (s *queryScanner) identifier() (string, int, bool) {
	line := s.line
	start := s.pos
	for !s.eof() {
		c := s.src[s.pos]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '-' || c == '.' && s.pos > start {
			s.pos++
			continue
		}
		break
	}
	if s.pos == start {
		return "", line, false
	}
	return s.src[start:s.pos], line, true
}

func (s *queryScanner) stringLiteral() (string, int, error) {
	line := s.line
	s.pos++ // opening quote
	var b strings.Builder
	for !s.eof() {
		c := s.src[s.pos]
		switch c {
		case '\\':
			if s.pos+1 >= len(s.src) {
				return "", line, fmt.Errorf("line %d: string ends in a backslash", line)
			}
			esc := s.src[s.pos+1]
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(esc)
			}
			s.pos += 2
			continue
		case '"':
			s.pos++
			return b.String(), line, nil
		case '\n':
			return "", line, fmt.Errorf("line %d: unterminated string", line)
		}
		b.WriteByte(c)
		s.pos++
	}
	return "", line, fmt.Errorf("line %d: unterminated string", line)
}
