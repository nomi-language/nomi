package lsp

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// sharedLoweringFiles are corpus files whose programs are one file, so the
// lowering check lowers them from their analysis; each is lowered with
// loweringProbe and loweringCaptureProbe appended, which add a declined
// body, a generic std instantiation and a checked typed literal.
var sharedLoweringFiles = []string{
	"tests/02-testing/io_capture_test.nomi",
	"tests/17-typed-literals/toml_literal_test.nomi",
	"tests/06-collections/maps_test.nomi",
	"tests/16-concurrency/concurrent_runtime_test.nomi",
	"tests/12-derives-and-standard-interfaces/derives_test.nomi",
}

func sharedLoweringDoc(t *testing.T, s *Server, rel string) (path, uri, text string, a *analyzedlowering.Analyzed) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text = string(data) + loweringProbe + loweringCaptureProbe
	uri = pathToURI(path)
	s.docs.Open(uri, text)
	a, _ = s.loweringAnalysis(uri, text)
	if a == nil {
		t.Fatalf("%s has no analysis to lower", rel)
	}
	return path, uri, text, a
}

// lowerAnalyzed lowers a, which must be lowered from the analysis, and
// must report lowering diagnostics.
func lowerAnalyzed(t *testing.T, path, text string, a *analyzedlowering.Analyzed) {
	problems, ok, err := analyzedlowering.Check(path, text, a)
	if !ok || err != nil || problems == nil {
		t.Errorf("%s: lowered from its analysis %v, problems %v, err %v; want problems from the analysis", path, ok, problems, err)
	}
}

// Lowering a document from its analysis writes nothing the analysis can
// reach: every object reachable from the entry's nodes and its analysis
// (the stdlib's shared analysis included) is the same, field for field,
// after the lowering as before it. The process's first lowering, which
// lowers the stdlib once, runs before the first fingerprint.
func TestLoweringFromAnalysis_LeavesTheAnalysisUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	s := NewServer()
	warm, _, warmText, _ := sharedLoweringDoc(t, s, "tests/01-foundations/literals_test.nomi")
	if _, err := vmhost.CheckLowering(warm, warmText); err != nil {
		t.Fatal(err)
	}
	for _, rel := range sharedLoweringFiles {
		path, _, text, a := sharedLoweringDoc(t, s, rel)
		before := fingerprint(a)
		lowerAnalyzed(t, path, text, a)
		after := fingerprint(a)
		if changed := before.diff(after); len(changed) > 0 {
			t.Errorf("%s: lowering changed objects the analysis reaches, of types:\n  %s", rel, strings.Join(changed, "\n  "))
		}
		t.Logf("%s: %d objects", rel, len(before))
		if len(before) < 10000 {
			t.Errorf("%s: the analysis reaches %d objects; the fingerprint misses most of it", rel, len(before))
		}
		// The fingerprint sees a change to one field.
		var sym *analysis.Symbol
		for _, d := range a.FA.Definitions {
			sym = d
			break
		}
		if sym == nil {
			t.Fatalf("%s: no definitions", rel)
		}
		saved := sym.Name
		sym.Name += "'"
		if len(before.diff(fingerprint(a))) == 0 {
			t.Errorf("%s: the fingerprint misses a renamed symbol", rel)
		}
		sym.Name = saved
	}
}

// The lowering check reads a document's installed analysis while request
// handlers read it, other documents are analyzed, and the front end checks
// another program, all over the one stdlib analysis. Run with -race.
func TestLoweringFromAnalysis_ConcurrentWithRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	s := NewServer()
	path, uri, text, a := sharedLoweringDoc(t, s, sharedLoweringFiles[0])
	otherPath, otherURI, otherText, _ := sharedLoweringDoc(t, s, sharedLoweringFiles[1])
	ctx := &glsp.Context{Notify: func(string, any) {}}
	doc := protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}
	lines := strings.Split(text, "\n")
	var positions []protocol.Position
	for i, line := range lines {
		for j := 0; j < len(line); j += 7 {
			positions = append(positions, protocol.Position{Line: uint32(i), Character: uint32(j)})
		}
	}
	const rounds = 3
	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	run(func() {
		for range rounds * 4 {
			lowerAnalyzed(t, path, text, a)
		}
	})
	run(func() {
		for range rounds {
			for _, pos := range positions {
				tdp := protocol.TextDocumentPositionParams{TextDocument: doc, Position: pos}
				_, _ = s.textDocumentHover(ctx, &protocol.HoverParams{TextDocumentPositionParams: tdp})
				_, _ = s.textDocumentDefinition(ctx, &protocol.DefinitionParams{TextDocumentPositionParams: tdp})
			}
		}
	})
	run(func() {
		for range rounds {
			_, _ = s.textDocumentSemanticTokensFull(ctx, &protocol.SemanticTokensParams{TextDocument: doc})
			_, _ = s.textDocumentDocumentSymbol(ctx, &protocol.DocumentSymbolParams{TextDocument: doc})
			_, _ = s.textDocumentCodeLens(ctx, &protocol.CodeLensParams{TextDocument: doc})
			for _, pos := range positions[:min(len(positions), 200)] {
				tdp := protocol.TextDocumentPositionParams{TextDocument: doc, Position: pos}
				_, _ = s.textDocumentCompletion(ctx, &protocol.CompletionParams{TextDocumentPositionParams: tdp})
			}
		}
	})
	run(func() {
		for i := range rounds * 2 {
			s.docs.Update(otherURI, otherText+strings.Repeat("\n", i%2))
		}
	})
	run(func() {
		for range rounds {
			if _, err := vmhost.CheckLowering(otherPath, otherText); err != nil {
				t.Errorf("checking %s: %v", otherPath, err)
			}
		}
	})
	wg.Wait()
}

// objectPrints maps each object a value reaches (a pointer's target, a
// map, a slice's elements), by its address and type, to a hash of its own
// fields: scalars by value, references by address.
type objectPrints map[string]uint64

// diff lists the types of the objects whose fields differ between p and
// q, or that only one of them reaches.
func (p objectPrints) diff(q objectPrints) []string {
	counts := map[string]int{}
	note := func(key string) {
		_, typ, _ := strings.Cut(key, " ")
		counts[typ]++
	}
	for k, h := range p {
		if h2, ok := q[k]; !ok || h2 != h {
			note(k)
		}
	}
	for k := range q {
		if _, ok := p[k]; !ok {
			note(k)
		}
	}
	var out []string
	for typ, n := range counts {
		out = append(out, fmt.Sprintf("%s (%d)", typ, n))
	}
	sort.Strings(out)
	return out
}

// fingerprint is the objectPrints of everything a reaches.
func fingerprint(a *analyzedlowering.Analyzed) objectPrints {
	w := &printWalker{prints: objectPrints{}}
	w.walk(reflect.ValueOf(a.Nodes))
	w.walk(reflect.ValueOf(a.FA))
	w.walk(reflect.ValueOf(a.Files))
	return w.prints
}

type printWalker struct {
	prints objectPrints
}

func (w *printWalker) object(key string, v reflect.Value) bool {
	key += " " + v.Type().String()
	if _, seen := w.prints[key]; seen {
		return false
	}
	h := fnv.New64a()
	shallow(h, v)
	w.prints[key] = h.Sum64()
	return true
}

// walk records every object v reaches.
func (w *printWalker) walk(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !w.object(fmt.Sprintf("%x", v.Pointer()), v.Elem()) {
			return
		}
		w.walk(v.Elem())
	case reflect.Interface:
		if !v.IsNil() {
			w.walk(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			w.walk(v.Field(i))
		}
	case reflect.Array:
		for i := range v.Len() {
			w.walk(v.Index(i))
		}
	case reflect.Slice:
		if v.IsNil() || !w.object(fmt.Sprintf("%x/%d", v.Pointer(), v.Len()), v) {
			return
		}
		for i := range v.Len() {
			w.walk(v.Index(i))
		}
	case reflect.Map:
		if v.IsNil() || !w.object(fmt.Sprintf("%x", v.Pointer()), v) {
			return
		}
		it := v.MapRange()
		for it.Next() {
			w.walk(it.Key())
			w.walk(it.Value())
		}
	}
}

type hasher interface{ Write([]byte) (int, error) }

// shallow hashes v's own content: scalars by value, references by
// address, a map's entries in an order of their own hashes.
func shallow(h hasher, v reflect.Value) {
	put := func(x uint64) {
		var b [8]byte
		for i := range b {
			b[i] = byte(x >> (8 * i))
		}
		h.Write(b[:])
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			put(1)
		} else {
			put(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		put(uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		put(v.Uint())
	case reflect.Float32, reflect.Float64:
		put(math.Float64bits(v.Float()))
	case reflect.Complex64, reflect.Complex128:
		put(math.Float64bits(real(v.Complex())))
		put(math.Float64bits(imag(v.Complex())))
	case reflect.String:
		h.Write([]byte(v.String()))
		put(uint64(v.Len()))
	case reflect.Pointer, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		put(uint64(v.Pointer()))
	case reflect.Map:
		put(uint64(v.Len()))
		if v.IsNil() {
			return
		}
		var entries []uint64
		it := v.MapRange()
		for it.Next() {
			e := fnv.New64a()
			shallow(e, it.Key())
			shallow(e, it.Value())
			entries = append(entries, e.Sum64())
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i] < entries[j] })
		for _, e := range entries {
			put(e)
		}
	case reflect.Slice:
		put(uint64(v.Pointer()))
		put(uint64(v.Len()))
		for i := range v.Len() {
			shallow(h, v.Index(i))
		}
	case reflect.Array:
		for i := range v.Len() {
			shallow(h, v.Index(i))
		}
	case reflect.Struct:
		for i := range v.NumField() {
			shallow(h, v.Field(i))
		}
	case reflect.Interface:
		if v.IsNil() {
			put(0)
			return
		}
		h.Write([]byte(v.Elem().Type().String()))
		shallow(h, v.Elem())
	}
}
