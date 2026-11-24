package vmhost

import (
	"context"
	"crypto/sha256"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Lowering the entry of a program the language server opened from its
// record (analysis/stored_lowering.go).
//
// The record holds the entry's lowering kept per file, as the server
// lowered it from the record's texts. A lowering of the entry starts from
// the kept lowering this process made last for the entry (storedKept), or
// else from the record's, and lowers each file whose text changed since on
// its own from its check against the others' interfaces, linking it with
// the kept objects of the others (relowerOne), a file after the files
// among them it imports. With the record's texts, the first lowering in a
// process lowers the entry alone, so the program links and its walk finds
// the effect calls it reaches, as after an edit. The program that links
// must reach nothing a machine cannot run and have no declined body, as a
// relink from file records must; its problems are then what CheckLowering
// would report, and the lowering is kept for the next run when it found
// nothing, which a run of the same texts answers with its calls. Anything
// else answers false, and the server lowers the entry's text. The program
// holds the syntax of its entry alone; a report about a call in another
// file finds the call's callee in that file's text, parsed when needed
// (Program.syntaxOf).

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
	// reached is the effect calls the kept lowering's program reaches
	// (reachedCalls), which a lowering of the same texts answers again.
	reached []analyzedlowering.Reached
	// appTypes are the program's application types as the last checks of
	// its files recorded them (Program.appTypes), which a later lowering
	// whose checks did not record them reads.
	appTypes []*analysis.StructType
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
func checkLoweringStored(ctx context.Context, cfg config, path, src string, in *analysis.StoredLowering) (found LoweringFound, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			found, ok, err = LoweringFound{}, false, nil
		}
	}()
	nodes, perr := parser.Parse(lexer.Lex(src))
	if perr != nil {
		return found, false, nil
	}
	tests := frontend.DeclaresTests(nodes)
	storedKept.mu.Lock()
	st := storedKept.docs[in.EntryPath]
	storedKept.mu.Unlock()
	base := st
	if base == nil || base.tests != tests || len(base.sums) != len(in.Files) {
		if in.Kept == nil || in.KeptTests != tests {
			return found, false, nil
		}
		kept, derr := irbuild.DecodeKeptLowering(in.Kept)
		if derr != nil {
			return found, false, nil
		}
		base = &storedKeptDoc{kept: kept, tests: tests, sums: map[string][32]byte{}, ifaces: map[string][32]byte{}, sigs: map[string][32]byte{}, impls: map[string][32]byte{}, headers: map[string][]analysis.ImplHeader{}}
		for _, f := range in.Files {
			base.sums[f.Key], base.ifaces[f.Key], base.sigs[f.Key], base.impls[f.Key] = f.BaseSum, f.BaseIfc, f.BaseSig, f.BaseImpls
			base.headers[f.Key] = f.BaseHeaders
		}
	}
	// The files lowered again: those whose texts changed, and those whose
	// checks read a file whose signature changed or see an impl that came,
	// went or changed (analysis.ProgramReads.Readers), which were lowered
	// against the old ones and need not import their files.
	files := map[string]bool{}
	lower := map[string]bool{}
	imports := map[string][]string{}
	headers := map[string][]analysis.ImplHeader{}
	signature := map[string]bool{}
	var keys []string
	var moved []analysis.ImplHeader
	for _, f := range in.Files {
		files[f.Key] = true
		keys = append(keys, f.Key)
		was, known := base.sums[f.Key]
		if !known {
			return found, false, nil
		}
		if was != f.Sum {
			lower[f.Key] = true
		}
		if base.impls[f.Key] != f.Impls {
			moved = append(append(moved, base.headers[f.Key]...), f.Headers...)
		}
		imports[f.Key], headers[f.Key] = f.Imports, f.Headers
		if base.sigs[f.Key] != f.Signature {
			signature[f.Key] = true
		}
	}
	reads := analysis.ProgramReads{
		Keys:    keys,
		Imports: func(key string) []string { return imports[key] },
		Headers: func(key string) []analysis.ImplHeader { return headers[key] },
	}
	for key := range reads.Readers(signature, moved) {
		lower[key] = true
	}
	if len(lower) > maxRelowered && len(lower)*4 > len(in.Files) {
		return found, false, nil
	}
	if len(lower) == 0 && base != st {
		// The record's texts: the entry is lowered again, which links the
		// program from the record's kept lowering, whose problems are the
		// record's answer and whose effect calls the walk finds.
		for _, f := range in.Files {
			if f.Path == in.EntryPath {
				lower[f.Key] = true
			}
		}
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
		// The kept lowering found nothing for these texts.
		return LoweringFound{Reached: st.reached}, true, nil
	}
	kept := base.kept
	var res *irbuild.Result
	apps := base.appTypes
	for _, f := range changed {
		if ctx.Err() != nil {
			return found, true, ctx.Err()
		}
		check := in.Check(f.Key, nil)
		if check == nil {
			return found, false, nil
		}
		if check.FA != nil && len(check.FA.Program().AppTypes) > 0 {
			apps = check.FA.Program().AppTypes
		}
		key := f.Key
		recheck := func(bodies map[string]bool) *analysis.FileCheck { return in.Check(key, bodies) }
		var why string
		if res, kept, _, _, why = relowerOne(check, f.Key, f.Path, in.Root, tests, kept, f.Iface != base.ifaces[f.Key], relowering, nil, files, nil, recheck, func(string, ...any) {}); why != "" {
			return found, false, nil
		}
		relowering[f.Path] = false
	}
	p, built := relinkedProgram(res, in.EntryPath, src, in.Root, tests, cfg)
	if !built || p.entry == nil && (p.HasMain() || tests) || !p.runsClean() || p.Unsupported() != nil {
		return found, false, nil
	}
	p.appTypes = apps
	found, err = loweringProblems(p, src)
	if found.Problems == nil && err == nil && cfg.loweringStopped() == nil {
		next.kept, next.reached, next.appTypes = kept, found.Reached, apps
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
	return found, true, err
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
