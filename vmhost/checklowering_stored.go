package vmhost

import (
	"context"
	"crypto/sha256"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Lowering the entry of a program the language server opened from its
// record (analysis/stored_lowering.go).
//
// The record holds the entry's lowering kept per file, as the server
// lowered it from the record's texts. A lowering of the entry after an
// edit starts from the kept lowering this process made last for the entry
// (storedKept), or else from the record's, and lowers each file whose text
// changed since on its own from its check against the others' interfaces,
// linking it with the kept objects of the others (relowerOne), a file after
// the files among them it imports. The program that links must reach
// nothing a machine cannot run and have no declined body, as a relink from
// file records must; its problems are then what CheckLowering would report,
// and the lowering is kept for the next edit when it found nothing.
// Anything else answers false, and the server lowers the entry's text.

// storedKept holds, by entry path, the last kept lowering of a program
// opened from its record that found nothing, with the texts and
// interfaces it was made from.
var storedKept struct {
	mu   sync.Mutex
	docs map[string]*storedKeptDoc
}

// maxStoredKept is how many entries' lowerings storedKept holds.
const maxStoredKept = 4

// storedKeptDoc is one entry's kept lowering: its files' text and
// interface sums, by module key, and their paths.
type storedKeptDoc struct {
	kept                      *irbuild.KeptLowering
	tests                     bool
	sums, ifaces, sigs, impls map[string][32]byte
	paths                     map[string]string
	// headers holds each file's impl headers, by module key.
	headers map[string][]analysis.ImplHeader
}

// storedRelowers counts the lowerings that lowered only the changed files
// of a program opened from its record.
var storedRelowers atomic.Int64

// StoredRelowersForTest is how many lowerings of a program opened from its
// record lowered only the files that changed.
func StoredRelowersForTest() int64 { return storedRelowers.Load() }

// checkLoweringStored is CheckLowering for the entry at path whose text is
// src, of a program opened from its record (see above). ok is false when
// it did not lower.
func checkLoweringStored(ctx context.Context, cfg config, path, src string, in *analysis.StoredLowering) (problems error, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			problems, ok, err = nil, false, nil
		}
	}()
	nodes, perr := parser.Parse(lexer.Lex(src))
	if perr != nil {
		return nil, false, nil
	}
	tests := frontend.DeclaresTests(nodes)
	storedKept.mu.Lock()
	st := storedKept.docs[in.EntryPath]
	storedKept.mu.Unlock()
	base := st
	if base == nil || base.tests != tests || len(base.sums) != len(in.Files) {
		if in.Kept == nil || in.KeptTests != tests {
			return nil, false, nil
		}
		kept, derr := irbuild.DecodeKeptLowering(in.Kept)
		if derr != nil {
			return nil, false, nil
		}
		base = &storedKeptDoc{kept: kept, tests: tests, sums: map[string][32]byte{}, ifaces: map[string][32]byte{}, sigs: map[string][32]byte{}, impls: map[string][32]byte{}, headers: map[string][]analysis.ImplHeader{}}
		for _, f := range in.Files {
			base.sums[f.Key], base.ifaces[f.Key], base.sigs[f.Key], base.impls[f.Key] = f.BaseSum, f.BaseIfc, f.BaseSig, f.BaseImpls
			base.headers[f.Key] = f.BaseHeaders
		}
	}
	// The files lowered again: those whose texts changed, those that import
	// a file whose signature changed, directly or through others, which were
	// lowered against its old one, and those that can see an impl that came,
	// went or changed, which need not import its file.
	files := map[string]bool{}
	lower := map[string]bool{}
	importers := map[string][]string{}
	imports := map[string][]string{}
	var moved []analysis.ImplHeader
	var queue []string
	for _, f := range in.Files {
		files[f.Key] = true
		was, known := base.sums[f.Key]
		if !known {
			return nil, false, nil
		}
		if was != f.Sum {
			lower[f.Key] = true
		}
		if base.impls[f.Key] != f.Impls {
			moved = append(append(moved, base.headers[f.Key]...), f.Headers...)
		}
		imports[f.Key] = f.Imports
		if base.sigs[f.Key] != f.Signature {
			queue = append(queue, f.Key)
		}
		for _, imp := range f.Imports {
			importers[imp] = append(importers[imp], f.Key)
		}
	}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, from := range importers[k] {
			if !lower[from] {
				lower[from] = true
				queue = append(queue, from)
			}
		}
	}
	if len(moved) > 0 {
		for key := range files {
			if lower[key] {
				continue
			}
			visible := importClosureOf(key, imports)
			for _, h := range moved {
				if seesHeader(h, visible) {
					lower[key] = true
					break
				}
			}
		}
	}
	if len(lower) > maxRelowered && len(lower)*4 > len(in.Files) {
		return nil, false, nil
	}
	relowering := map[string]bool{}
	var changed []analysis.StoredLoweringFile
	for _, f := range in.Files {
		if lower[f.Key] {
			changed = append(changed, f)
			relowering[f.Path] = true
		}
	}
	next := &storedKeptDoc{kept: base.kept, tests: tests, sums: map[string][32]byte{}, ifaces: map[string][32]byte{}, sigs: map[string][32]byte{}, impls: map[string][32]byte{}, paths: map[string]string{}, headers: map[string][]analysis.ImplHeader{}}
	for _, f := range in.Files {
		next.sums[f.Key], next.ifaces[f.Key], next.sigs[f.Key], next.impls[f.Key], next.paths[f.Key] = f.Sum, f.Iface, f.Signature, f.Impls, f.Path
		next.headers[f.Key] = f.Headers
	}
	if len(changed) == 0 {
		if st == nil {
			// The record's texts: its answer is the record's.
			return nil, false, nil
		}
		// The kept lowering found nothing for these texts.
		return nil, true, nil
	}
	kept := base.kept
	var res *irbuild.Result
	for _, f := range changed {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}
		check := in.Check(f.Key, nil)
		if check == nil {
			return nil, false, nil
		}
		key := f.Key
		recheck := func(bodies map[string]bool) *analysis.FileCheck { return in.Check(key, bodies) }
		var why string
		if res, kept, _, _, why = relowerOne(check, f.Key, f.Path, in.Root, tests, kept, f.Iface != base.ifaces[f.Key], relowering, nil, files, nil, recheck, func(string, ...any) {}); why != "" {
			return nil, false, nil
		}
		relowering[f.Path] = false
	}
	p, built := relinkedProgram(res, in.EntryPath, src, in.Root, tests, cfg)
	if !built || p.entry == nil && (p.HasMain() || tests) || !p.runsClean() || p.Unsupported() != nil {
		return nil, false, nil
	}
	problems, err = loweringProblems(p, src)
	if problems == nil && err == nil && cfg.loweringStopped() == nil {
		next.kept = kept
		storedKept.mu.Lock()
		if storedKept.docs == nil {
			storedKept.docs = map[string]*storedKeptDoc{}
		}
		if len(storedKept.docs) >= maxStoredKept {
			for k := range storedKept.docs {
				delete(storedKept.docs, k)
				break
			}
		}
		storedKept.docs[in.EntryPath] = next
		storedKept.mu.Unlock()
		storedRelowers.Add(1)
	}
	return problems, true, err
}

// keptLoweringOf is analysis.KeptLoweringFunc over this process's kept
// lowerings: the entry's kept by checkLoweringStored, else the
// document's last whole lowering from its analysis (keepAnalyzed).
func keptLoweringOf(path string) (data []byte, tests bool, sums map[string][32]byte, ok bool) {
	storedKept.mu.Lock()
	st := storedKept.docs[path]
	storedKept.mu.Unlock()
	if st != nil {
		sums = map[string][32]byte{}
		for k, s := range st.sums {
			sums[st.paths[k]] = s
		}
		return st.kept.Encode(), st.tests, sums, true
	}
	a, found := analyzedKept.Load(path)
	if !found {
		return nil, false, nil, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kept == nil {
		if a.res == nil {
			return nil, false, nil, false
		}
		// Kept as a relink from file records keeps it: a lowering of one
		// file against it misses whenever its declines or refusals are not
		// the whole lowering's.
		kept, err := a.res.Keep()
		if err != nil {
			return nil, false, nil, false
		}
		data := kept.Encode()
		sums = map[string][32]byte{}
		for p, text := range a.texts {
			sums[p] = sha256.Sum256([]byte(text))
		}
		return data, a.hasTests, sums, true
	}
	sums = map[string][32]byte{}
	for p, text := range a.texts {
		sums[p] = sha256.Sum256([]byte(text))
	}
	return a.kept.Encode(), a.hasTests, sums, true
}

// importClosureOf answers the module keys of the files the file with key
// imports, directly or through others, with key itself, imports holding
// each file's imports.
func importClosureOf(key string, imports map[string][]string) map[string]bool {
	seen := map[string]bool{key: true}
	queue := slices.Clone(imports[key])
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if !seen[k] {
			seen[k] = true
			queue = append(queue, imports[k]...)
		}
	}
	return seen
}
