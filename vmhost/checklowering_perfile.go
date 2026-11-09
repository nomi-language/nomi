package vmhost

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Lowering an edited document's program per file.
//
// The language server lowers a document's program from its analysis after
// each burst of typing (checkLoweringAnalyzed). A whole lowering keeps
// itself per file (irbuild.Result.Keep), with each file's text and
// interface, by the document's path. The next lowering of the document,
// when the program holds the same files and at most maxAnalyzedRelowered
// of them changed text, lowers those files on their own from the analysis,
// with the files their interface or impl changes reach (importersToo), and
// links them with the kept objects of the others (irbuild.LowerFileAmong),
// as `nomi check` relinks a kept program after an edit (filerecord.go). The analysis already checked the
// whole program, so the program's reductions over bodies are in its
// diagnostics, and a program with any diagnostic is not lowered from its
// analysis at all.
//
// A whole lowering costs nothing more for being kept: its result is held,
// and encoded as objects (Keep) by the first lowering that lowers files on
// their own. Whether a changed file's interface changed, and whether its
// declarations moved, is read from its old and new texts parsed afresh,
// since the analysis rewrites an edited file's bodies in place; and the
// changed files' declarations are fingerprinted from their old texts, so
// what one of them made from a declaration whose text changed (an
// instance another file asked for) is never carried.
//
// The kept lowering is used only while it records no declined body and no
// refused construct: a program that reaches one reports it at the code the
// builder stopped at, which a whole lowering words. Anything LowerFile
// cannot answer as a whole lowering would is a miss, and the program is
// lowered whole and kept again.

// analyzedKept holds, by document path, the last kept lowering of the
// document's program from its analysis, for the maxAnalyzedKept documents
// lowered last: a kept lowering holds the whole program's IR, and a
// server lowers every document it opens.
var analyzedKept keptDocs

// maxAnalyzedKept is how many documents' lowerings analyzedKept holds.
const maxAnalyzedKept = 4

// keptDocs is a few documents' kept lowerings, by path, the oldest
// dropped first.
type keptDocs struct {
	mu    sync.Mutex
	order []string
	docs  map[string]*keptAnalyzed
}

func (k *keptDocs) Load(path string) (*keptAnalyzed, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	st, ok := k.docs[path]
	return st, ok
}

func (k *keptDocs) Store(path string, st *keptAnalyzed) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.docs == nil {
		k.docs = map[string]*keptAnalyzed{}
	}
	k.drop(path)
	k.docs[path] = st
	k.order = append(k.order, path)
	for len(k.order) > maxAnalyzedKept {
		k.drop(k.order[0])
	}
}

func (k *keptDocs) Delete(path string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.drop(path)
}

// drop forgets path's lowering. k.mu is held.
func (k *keptDocs) drop(path string) {
	delete(k.docs, path)
	k.order = slices.DeleteFunc(k.order, func(p string) bool { return p == path })
}

// analyzedRelowers counts the lowerings that lowered only the changed
// files, for tests and measurement.
var analyzedRelowers atomic.Int64

// AnalyzedRelowersForTest is how many lowerings from a document's analysis
// lowered only its changed files.
func AnalyzedRelowersForTest() int64 { return analyzedRelowers.Load() }

// maxAnalyzedRelowered is how many changed files a lowering from the
// analysis lowers on their own before it lowers the whole program instead.
const maxAnalyzedRelowered = 4

// keptAnalyzed is a document's program as its last whole lowering, or a
// lowering of its changed files since, kept it.
type keptAnalyzed struct {
	mu sync.Mutex
	// res is the whole lowering, until the first lowering of changed
	// files keeps it (kept).
	res      *irbuild.Result
	kept     *irbuild.KeptLowering
	root     string
	hasTests bool
	// texts holds each file's text, and names its module name, by path.
	texts, names map[string]string
	// headers holds the project files each of a file's interface impls
	// names in its header, by path (analysis.ImplHeaderFiles).
	headers map[string][][]string
}

// analyzedTexts answers each file's text of the program a analyzes, the
// document's src among them, by path.
func analyzedTexts(abs, src string, a *analyzedlowering.Analyzed) map[string]string {
	texts := map[string]string{abs: src}
	for _, f := range a.Files {
		texts[f.Path] = f.Text
	}
	return texts
}

// parsedInterface answers the interface of the file with module name key
// at path whose text is text, parsed afresh, encoded, with its hash; ok is
// false when the text does not parse.
func parsedInterface(key, path, text string) (enc []byte, hash string, nodes []ast.Node, ok bool) {
	nodes, err := parser.Parse(lexer.Lex(text))
	if err != nil {
		return nil, "", nil, false
	}
	fi := analysis.ExtractInterface(key, path, nodes)
	return fi.Encode(), fi.Hash, nodes, true
}

// keepAnalyzed keeps p, the whole lowering of prog from the document's
// analysis, for the document at abs, or forgets what was kept when p
// cannot be kept.
func keepAnalyzed(abs, src string, a *analyzedlowering.Analyzed, prog *irbuild.Program, p *Program) {
	names := map[string]string{}
	for _, m := range prog.Modules {
		names[m.Path] = m.Name
	}
	analyzedKept.Store(abs, &keptAnalyzed{res: p.res, root: a.Root, hasTests: prog.HasTests,
		texts: analyzedTexts(abs, src, a), names: names, headers: implHeadersOf(prog)})
}

// implHeadersOf answers the project files each interface impl of each of
// prog's files names in its header, by path.
func implHeadersOf(prog *irbuild.Program) map[string][][]string {
	keyOf := map[string]string{}
	for _, m := range prog.Modules {
		keyOf[m.Path] = m.Name
	}
	out := map[string][][]string{}
	for _, m := range prog.Modules {
		if m.FA != nil {
			out[m.Path] = analysis.ImplHeaderFiles(m.FA, m.Nodes, func(path string) string { return keyOf[path] })
		}
	}
	return out
}

// sameHeaderLists reports whether two files' impl header lists name the
// same files, impl by impl, whatever their order.
func sameHeaderLists(a, b [][]string) bool {
	key := func(hs [][]string) []string {
		out := make([]string, len(hs))
		for i, h := range hs {
			out[i] = strings.Join(h, "\x00")
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(key(a), key(b))
}

// relowerAnalyzed lowers the files of prog, the document's program from
// its analysis, whose texts changed since its kept lowering, and links them
// with the others' kept objects. It answers false when it cannot, and the
// caller lowers the whole program.
func relowerAnalyzed(cfg config, abs, src string, a *analyzedlowering.Analyzed, prog *irbuild.Program) (p *Program, ok bool) {
	st, found := analyzedKept.Load(abs)
	if !found {
		return nil, false
	}
	if !st.mu.TryLock() {
		return nil, false
	}
	defer st.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			p, ok = nil, false
		}
	}()
	if st.root != a.Root || st.hasTests != prog.HasTests {
		return nil, false
	}
	texts := analyzedTexts(abs, src, a)
	if len(texts) != len(st.texts) {
		return nil, false
	}
	changed := map[string]bool{}
	for path, text := range texts {
		was, ok := st.texts[path]
		if !ok {
			return nil, false
		}
		if was != text {
			changed[path] = true
		}
	}
	if len(changed) == 0 || len(changed) > maxAnalyzedRelowered {
		return nil, false
	}
	moved := map[string]bool{}
	oldDecls := map[string]map[string][32]byte{}
	// The files whose interfaces changed, which the files that import them
	// were lowered against, and the headers of the impls that came, went or
	// changed, whose files see them without importing their file.
	signature := map[string]bool{}
	var movedImpls [][]string
	var nowHeaders map[string][][]string
	for path := range changed {
		name := st.names[path]
		was, wasHash, wasNodes, ok1 := parsedInterface(name, path, st.texts[path])
		now, nowHash, nowNodes, ok2 := parsedInterface(name, path, texts[path])
		if !ok1 || !ok2 {
			return nil, false
		}
		// An unannotated `once` whose value changed may have another type.
		if wasHash != nowHash || analysis.InferredOncesChanged(was, now) {
			// A changed impl is seen by files that need not import this one.
			if analysis.ImplsFingerprint(wasNodes) != analysis.ImplsFingerprint(nowNodes) {
				if nowHeaders == nil {
					nowHeaders = implHeadersOf(prog)
				}
				was, known := st.headers[path]
				if !known {
					return nil, false
				}
				if !sameHeaderLists(was, nowHeaders[path]) {
					movedImpls = append(append(movedImpls, was...), nowHeaders[path]...)
				}
			}
			signature[path] = true
		}
		moved[path] = !bytes.Equal(was, now)
		oldDecls[path] = irbuild.DeclFingerprints(wasNodes)
	}
	paths, ok := importersToo(prog, changed, signature, seersOf(prog, movedImpls))
	if !ok {
		return nil, false
	}
	relowering := map[string]bool{}
	for _, path := range paths {
		relowering[path] = true
	}
	if st.kept == nil {
		kept, err := st.res.Keep()
		if err != nil || len(kept.Declines) > 0 || len(kept.Refusals) > 0 {
			analyzedKept.Delete(abs)
			return nil, false
		}
		st.kept, st.res = kept, nil
	}
	kept := st.kept.WithDecls(oldDecls)
	var res *irbuild.Result
	for _, path := range paths {
		one := &irbuild.Program{Root: prog.Root, HasTests: prog.HasTests}
		for _, m := range prog.Modules {
			if m.Path == path {
				one.Modules = append([]irbuild.Module{m}, one.Modules...)
			} else {
				one.Modules = append(one.Modules, m)
			}
		}
		var err error
		if res, err = irbuild.LowerFileAmong(one, kept, moved[path], relowering, nil); err != nil {
			return nil, false
		}
		relowering[path] = false
		if kept, err = res.Rekeep(kept); err != nil {
			return nil, false
		}
	}
	p = newProgram(prog, res, map[string]string{}, cfg)
	if p.entry == nil && (p.HasMain() || prog.HasTests) {
		return nil, false
	}
	st.kept, st.texts = kept, texts
	if nowHeaders != nil {
		for path := range changed {
			st.headers[path] = nowHeaders[path]
		}
	}
	analyzedRelowers.Add(1)
	return p, true
}
