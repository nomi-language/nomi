package vmhost

// File records: a kept program's files, kept so that the next command over
// the program after an edit relowers only the files the edit reaches.
//
// Beside the program's record (cache.go), a load that keeps a program also
// keeps a record of its files (kind "files"): each project file's
// interface (analysis.StoredInterface), the SHA-256 of its text, its facts
// for the program's reductions over bodies (analysis.FileFacts) and its
// advice (fileAdvice), which a relink keeps for a file it does not check
// again and works out for one it does; the program's lowering kept per
// file (irbuild.KeptLowering), the supply side of its missing-impl check
// (analysis.ProgramSupply), and the application types the program's check
// worked out. When the program's record misses
// because sources changed, came or went and nothing else did (the
// manifest and every other input as they were: progcache's
// ChangedSources), relink checks and lowers just the files the edit
// reaches:
//
//   - each changed file must be one of the program's, which are the only
//     sources whose text the record holds (sourceTree);
//   - each changed or added file's new interface is extracted, and the
//     program's files, the entry and what it imports, must be the kept ones
//     less the removed with the added: an added file no file imports is a
//     miss, since a fresh load may read it through an import that named a
//     directory;
//   - the files checked again are the changed and added ones, those that
//     import a file whose interface changed, and those that can see an
//     impl that came, went or changed (the relevant impl set); none, when
//     only sources the program does not load went, is a miss, and so is an
//     edit that reaches more than maxRelowered files and a quarter of the
//     program;
//   - each is checked against the interfaces of the files it reads
//     (analysis.NewStoredInterfaces), with no diagnostic, its own or one
//     its build works out about the program's impls (a duplicate impl
//     block, the orphan rules: analysis.FileCheck.Clean);
//   - the program's reductions over bodies (boot-only calls,
//     application-field roots, whole-file imports, missing impls) run over
//     the checked files' new facts and the others' stored ones, with the
//     stdlib base the checks' program is built over (analysis.ReduceFacts,
//     which refuses a program over none), and find nothing, the supply worked out again
//     from every file's interface when impls changed. For a program with
//     an application type, whose application-field roots no file's facts
//     answer, the walk those roots take of each checked file's bodies
//     (analysis.AppWalkSummary) must also be the kept one and find nothing
//     in them, so the roots find nothing as the kept program's did;
//   - each is lowered on its own and linked with the kept objects of the
//     others (irbuild.LowerFileAmong), after the files it imports among
//     them, which declines whatever it cannot answer as a whole lowering
//     would; an instance of another file's generic declaration no kept
//     object holds is made from that file read with its bodies;
//   - every typed literal the program checks at load is checked again
//     (checkLiterals): the relowered files' from their new lowering, the
//     others' from the kept lowering's record of them
//     (irbuild.KeptLowering.Literals), since an edit to a handler can
//     change the answer of a literal whose text it did not touch;
//   - an instance a file's lowering drops because its generic body
//     changed, which kept objects of other files name, is asked for again
//     by one of those files, added to the files lowered again
//     (chooseAskers), and must be defined once the last file is lowered.
//
// The program that links runs as a kept program does, after the same
// checks that it reaches nothing a machine cannot run. Both records are
// then written again for the new texts. Anything relink cannot do is a
// miss, and the command checks and lowers the whole program as it does
// with no cache. So is any edit of a program whose [effects.self] holds its
// own generic code (fileRecord.Abstract), which only a whole lowering's
// abstract bodies answer for every type, and what one reaches may be in
// any file. A file that declares keyword declarations (`phrase`,
// `noun`, `embed`) is never relowered alone, since what they expand to
// reads more than the file.

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/progcache"
	"github.com/nomi-language/nomi/internal/vm"
)

// filesKind names the record of a program's files beside its load record.
const filesKind = "files"

// fileRecord is what the files record holds.
type fileRecord struct {
	Entry, Root string
	HasTests    bool
	AppTypes    []analysis.AppTypeName
	// Files are the project files by module key.
	Files map[string]keptFile
	// Kept is the program's lowering kept per file, encoded.
	Kept []byte
	// Supply is the supply side of the program's missing-impl check.
	Supply *analysis.ProgramSupply
	// Abstract reports that [effects.self] holds the program's public
	// functions and its own files declare generic code that only abstract
	// bodies answer for every type (irbuild.Result.AbstractOwn), which a
	// relink does not build: an edit anywhere may change what one reaches.
	Abstract bool
}

// keptFile is one project file of a file record.
type keptFile struct {
	Iface analysis.StoredInterface
	// Sum is the SHA-256 of the file's text, and Body its body summary.
	Sum, Body [32]byte
	// Facts is the file's facts for the program's reductions over bodies.
	Facts *analysis.FileFacts
	// Keyword says the file declares keyword declarations.
	Keyword bool
	// Advice is the file's advice (fileAdvice), which depends on its own
	// signatures and bodies alone, so a relink that does not check the
	// file again keeps it.
	Advice Diagnostics
}

const fileRecordMagic = "NOMIFILES9"

func (r *fileRecord) encode() []byte {
	var b bytes.Buffer
	b.WriteString(fileRecordMagic)
	if gob.NewEncoder(&b).Encode(r) != nil {
		return nil
	}
	return b.Bytes()
}

func decodeFileRecord(data []byte) (*fileRecord, error) {
	rest, ok := bytes.CutPrefix(data, []byte(fileRecordMagic))
	if !ok {
		return nil, errors.New("vmhost: not a file record")
	}
	r := &fileRecord{}
	if err := gob.NewDecoder(bytes.NewReader(rest)).Decode(r); err != nil {
		return nil, err
	}
	return r, nil
}

// relinks counts the programs this process relinked from file records, for
// tests.
var relinks atomic.Int64

// Relinks is how many programs this process linked from file records
// rather than checking and lowering the whole program.
func Relinks() int64 { return relinks.Load() }

// keepFiles writes p's file record beside its load record, when its front
// end can give its files' interfaces and its lowering can be kept.
// files is the record's entry (Cache.fileEntry), which the caller opened.
func (p *Program) keepFiles(files *progcache.Entry) {
	if files == nil {
		return
	}
	pi := p.prog.Interfaces()
	if pi == nil {
		return
	}
	stored, ok := pi.Stored()
	if !ok {
		return
	}
	kept, err := p.res.Keep()
	if err != nil {
		files.Logf("not kept: %v", err)
		return
	}
	rec := &fileRecord{Entry: p.prog.Entry().Path, Root: p.prog.Root, HasTests: p.prog.HasTests,
		Files: map[string]keptFile{}, Kept: kept.Encode(),
		Abstract: p.res.AbstractOwn() && p.effects != nil && p.effects.Self != nil}
	if fa := p.prog.Entry().FA; fa != nil {
		for _, st := range fa.Program().AppTypes {
			rec.AppTypes = append(rec.AppTypes, analysis.AppTypeName{Origin: st.Origin, Name: st.Name})
		}
	}
	byPath := map[string]*irbuild.Module{}
	for i := range p.prog.Modules {
		byPath[p.prog.Modules[i].Path] = &p.prog.Modules[i]
	}
	project := map[string][]ast.Node{}
	entryKey := ""
	for key, si := range stored {
		m := byPath[si.Path]
		if m == nil || m.FA == nil {
			return
		}
		project[key] = m.Nodes
		if si.Path == rec.Entry {
			entryKey = key
		}
	}
	namer := analysis.NewFactsNamer(project, entryKey)
	advice := map[string]Diagnostics{}
	for _, d := range p.Advice() {
		advice[d.Path] = append(advice[d.Path], d)
	}
	for key, si := range stored {
		m := byPath[si.Path]
		data, err := os.ReadFile(si.Path)
		if err != nil {
			return
		}
		rec.Files[key] = keptFile{Iface: si, Sum: sha256.Sum256(data), Body: appWalkOf(m.FA, m.Nodes),
			Keyword: declaresKeywords(m.Nodes), Facts: analysis.FileFactsOf(key, m.FA, m.Nodes, namer, len(rec.AppTypes) > 0),
			Advice: advice[si.Path]}
	}
	if fa := p.prog.Entry().FA; fa != nil {
		rec.Supply = analysis.SupplyOf(fa)
	}
	payload := rec.encode()
	if payload == nil {
		return
	}
	trees, inputs := p.inputs()
	files.Store(trees, inputs, payload)
}

// declaresKeywords reports whether nodes hold a keyword declaration's
// expansion.
func declaresKeywords(nodes []ast.Node) bool {
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			if d.Expansion != nil {
				return true
			}
		case *ast.OnceBinding:
			if d.Expansion != nil {
				return true
			}
		}
	}
	return false
}

// Relinked is the kept program relinked from its file records after some of
// its files' texts changed (relink), or false when the program's record
// holds it as its files are now or it cannot be relinked: what `nomi
// build` loads in place of checking and lowering the whole program.
func (c *Cache) Relinked(opts ...Option) (*Program, bool) {
	if c == nil || c.entry.Payload() != nil {
		return nil, false
	}
	cfg := newConfig(opts)
	if !cfg.cacheable() {
		return nil, false
	}
	return c.relink(cfg)
}

// relink is the kept program as its changed files make it now, or false:
// see the file comment.
func (c *Cache) relink(cfg config) (p *Program, ok bool) {
	if c == nil || c.fileEntry() == nil || irbuild.DeclinesPlanted() {
		return nil, false
	}
	prev := c.files.Previous()
	if prev == nil {
		return nil, false
	}
	why := ""
	defer func() {
		// A panic here is a bug in Nomi. The command checks the whole
		// program instead, which answers as it would with no cache, and
		// says so on stderr, which the cache's harness compares with a
		// command's that keeps nothing (cmd/nomi's artifact_cache_test.go).
		if r := recover(); r != nil {
			p, ok, why = nil, false, "a panic relinking"
			fmt.Fprintf(os.Stderr, "nomi: relinking %s from its file records failed, so the whole program is checked: %s\n", c.path, vm.InternalErrorText(r))
			c.files.Logf("the panic's stack:\n%s", debug.Stack())
		}
		if !ok && why != "" {
			c.files.Logf("not relinked: %s", why)
		}
	}()
	rec, err := decodeFileRecord(prev)
	if err != nil || rec.Entry != c.path {
		why = "the record does not read"
		return nil, false
	}
	if rec.Abstract {
		// [effects.self] holds the public generic functions through their
		// abstract bodies, which only a whole load builds, and what one
		// reaches may be in any file.
		why = "[effects.self] holds generic code only a whole load reads for every type"
		return nil, false
	}
	changed, added, removed, same := c.files.ChangedSources()
	if !same || len(changed)+len(added)+len(removed) == 0 {
		why = "a file other than a source changed, or no file changed"
		return nil, false
	}
	byPath := map[string]string{}
	for key, f := range rec.Files {
		byPath[f.Iface.Path] = key
	}
	// The program's files whose texts changed, and those removed: the
	// record holds the text of the program's own sources and of no other
	// (sourceTree), so any other file whose text changed is not a source.
	// A source that is not one of the program's and went changes nothing a
	// clean program reads; one that came changes the program only if a
	// changed file now imports it (below).
	var changedKeys, removedKeys, addedKeys []string
	for _, path := range changed {
		key, ok := byPath[path]
		if !ok {
			why = path + " changed, and is not one of the program's sources"
			return nil, false
		}
		if rec.Files[key].Keyword {
			why = path + " declares keyword declarations"
			return nil, false
		}
		changedKeys = append(changedKeys, key)
	}
	for _, path := range removed {
		if key, ok := byPath[path]; ok {
			removedKeys = append(removedKeys, key)
		}
	}
	addedPaths := map[string]string{}
	for _, path := range added {
		rel, err := filepath.Rel(rec.Root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			why = path + " came outside the program's root"
			return nil, false
		}
		key := strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
		addedPaths[key] = path
		addedKeys = append(addedKeys, key)
	}
	sort.Strings(addedKeys)
	if hasTests, _ := frontend.FileDeclaresTests(c.path); hasTests != rec.HasTests {
		why = "the entry's tests came or went"
		return nil, false
	}
	fc, err := cfg.frontendConfig()
	if err != nil {
		return nil, false
	}
	fe := frontend.New(fc)
	sp, err := fe.StoredSource(c.path, frontend.Mode{Tests: rec.HasTests}, rec.AppTypes)
	if err != nil || filepath.Clean(sp.Root) != filepath.Clean(rec.Root) {
		why = "the program's root moved"
		return nil, false
	}
	entryKey := byPath[c.path]
	ifaces := map[string]analysis.StoredInterface{}
	for key, f := range rec.Files {
		ifaces[key] = f.Iface
	}
	pi := analysis.NewStoredInterfaces(sp, ifaces)
	for _, key := range removedKeys {
		pi.RemoveFile(key)
	}
	// Each changed or added file's new interface. One whose hash is its
	// kept one's changes nothing another file's check reads.
	moved := map[string]bool{}
	sums := map[string][32]byte{}
	keyword := map[string]bool{}
	ifaceChanged := map[string]bool{}
	// Added files first, so a changed file's imports of them are known.
	for _, key := range append(append([]string(nil), addedKeys...), changedKeys...) {
		path := addedPaths[key]
		if path == "" {
			path = rec.Files[key].Iface.Path
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false
		}
		sums[key] = sha256.Sum256(data)
		nodes, ok := sp.Source(key)
		if !ok || fe.SyntaxErrors() {
			why = path + " does not parse"
			return nil, false
		}
		keyword[key] = declaresKeywords(nodes)
		if !pi.AddFile(key, path) {
			why = path + " does not read"
			return nil, false
		}
		si, _ := pi.StoredOfEncoded(key)
		was, kept := rec.Files[key]
		switch {
		case !kept:
			ifaceChanged[key] = true
		case si.Hash != was.Iface.Hash:
			ifaceChanged[key] = true
			moved[key] = true
		default:
			pi.SetImplHeaders(key, was.Iface.Headers)
			moved[key] = !bytes.Equal(si.Encoded, was.Iface.Encoded)
			// An unannotated `once`'s type is its value's, which the hash
			// leaves out: the files that import it are checked again.
			if analysis.InferredOncesChanged(was.Iface.Encoded, si.Encoded) {
				ifaceChanged[key] = true
			}
		}
	}
	for _, key := range removedKeys {
		ifaceChanged[key] = true
	}
	// The program's files now: the entry and what it imports. They must be
	// the kept ones less the removed, with the added. A file that came and
	// that no file imports may still be one a fresh load reads: an import
	// that named a directory names it once it is there (spec §14 *Directory
	// imports*), and a kept interface's imports name only the files that
	// were there.
	reach := pi.ImportClosure(entryKey)
	for _, key := range addedKeys {
		if !reach[key] {
			why = addedPaths[key] + " came, and no file of the program imports it"
			return nil, false
		}
	}
	want := map[string]bool{}
	for key := range rec.Files {
		want[key] = true
	}
	for _, key := range removedKeys {
		delete(want, key)
	}
	for _, key := range addedKeys {
		want[key] = true
	}
	if !maps.Equal(reach, want) {
		why = "the program's files are not the kept ones with the added and less the removed"
		return nil, false
	}
	for _, key := range addedKeys {
		if keyword[key] {
			why = addedPaths[key] + " declares keyword declarations"
			return nil, false
		}
	}
	// The files to check again: the changed and added ones, and the files
	// whose checks read a file whose interface changed or see an impl that
	// came, went or changed (analysis.ProgramInterfaces.Readers).
	recheck := map[string]bool{}
	for _, key := range append(append([]string(nil), changedKeys...), addedKeys...) {
		recheck[key] = true
	}
	var ifaceKeys []string
	for key := range ifaceChanged {
		ifaceKeys = append(ifaceKeys, key)
	}
	sort.Strings(ifaceKeys)
	implsChanged := len(addedKeys) > 0 || len(removedKeys) > 0
	var headers []analysis.ImplHeader
	for _, key := range ifaceKeys {
		var now []analysis.ImplHeader
		if reach[key] {
			now = pi.ImplHeadersOf(key)
		}
		was := rec.Files[key].Iface.Headers
		if !analysis.SameImplHeaders(was, now) {
			implsChanged = true
			headers = append(append(headers, was...), now...)
		}
	}
	for key := range pi.Readers(ifaceKeys, headers) {
		if reach[key] {
			recheck[key] = true
		}
	}
	// Only sources the program does not load went: a relink has no file
	// to check and lower again, and links nothing.
	if len(recheck) == 0 {
		why = "no file of the program changed"
		return nil, false
	}
	supply := rec.Supply
	if implsChanged {
		supply = pi.Supply(entryKey)
	}
	appTypes := len(rec.AppTypes) > 0
	var keys []string
	var res *irbuild.Result
	var kept *irbuild.KeptLowering
	var newRec fileRecord
	// A lowering that drops an instance of a changed generic declaration
	// that kept objects of files not lowered again name
	// (irbuild.FileMiss.Dropped) starts again with one of those files per
	// instance lowered too, after the declaring file and reading it with
	// its bodies, so that it asks for the instance again: asked holds the
	// instances, and askers the files read with their bodies by each file
	// lowered for them.
	checked := map[string]*analysis.FileCheck{}
	checkedFacts := map[string]*analysis.FileFacts{}
	asked := map[string]bool{}
	askers := map[string]map[string]bool{}
	for again := true; again; {
		again = false
		keys = make([]string, 0, len(recheck))
		for key := range recheck {
			if rec.Files[key].Keyword {
				why = key + " declares keyword declarations and reads a changed file"
				return nil, false
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		keys = importOrder(keys, pi)
		if len(keys) > maxRelowered && len(keys)*4 > len(reach) {
			why = fmt.Sprintf("the edit reaches %d of the program's %d files", len(keys), len(reach))
			return nil, false
		}
		kept, err = irbuild.DecodeKeptLowering(rec.Kept)
		if err != nil {
			why = "the kept lowering does not read"
			return nil, false
		}
		newRec = *rec
		newRec.Files = map[string]keptFile{}
		for key, f := range rec.Files {
			if want[key] {
				newRec.Files[key] = f
			}
		}
		newRec.Supply = supply
		facts := map[string]*analysis.FileFacts{}
		for key, f := range newRec.Files {
			if f.Facts == nil {
				why = key + " has no facts"
				return nil, false
			}
			facts[key] = f.Facts
		}
		// over is a check of the program, whose stdlib the reductions read.
		var over *analysis.FileAnalysis
		for _, key := range keys {
			check := checked[key]
			if check != nil {
				facts[key] = checkedFacts[key]
				over = check.FA
				continue
			}
			check = pi.Check(key)
			if !check.Clean() {
				why = key + " has diagnostics"
				return nil, false
			}
			if fe.SyntaxErrors() {
				why = "a file does not parse"
				return nil, false
			}
			was, isKept := rec.Files[key]
			if appTypes {
				sum, errs := analysis.AppWalkSummary(check.FA, check.Nodes)
				if !isKept || len(errs) > 0 || sum != was.Body {
					why = key + "'s application-field walk changed, and the program has an application type"
					return nil, false
				}
			}
			nodes := map[string][]ast.Node{key: check.Nodes}
			for k, n := range check.FileNodes {
				nodes[k] = n
			}
			facts[key] = analysis.FileFactsOf(key, check.FA, check.Nodes, analysis.NewFactsNamer(nodes, entryKey), appTypes)
			checked[key], checkedFacts[key] = check, facts[key]
			over = check.FA
		}
		red, ok := analysis.ReduceFacts(facts, supply, over)
		if !ok {
			why = "the program is not built over the stdlib's base"
			return nil, false
		}
		if !red.Clean(appTypes) {
			why = "the program's reductions over bodies find something"
			return nil, false
		}
		pathOf := func(key string) string {
			if p := addedPaths[key]; p != "" {
				return p
			}
			return rec.Files[key].Iface.Path
		}
		relowering := map[string]bool{}
		for _, key := range keys {
			relowering[pathOf(key)] = true
		}
		for _, key := range removedKeys {
			kept.RemoveUnit(rec.Files[key].Iface.Path)
		}
		for _, key := range addedKeys {
			kept.AddUnit(key, addedPaths[key])
		}
		for _, key := range keys {
			path := pathOf(key)
			withBodies := func(bodies map[string]bool) *analysis.FileCheck {
				pi.SetBodies(bodies)
				defer pi.SetBodies(nil)
				return pi.Check(key)
			}
			check := checked[key]
			if askers[key] != nil {
				check = withBodies(askers[key])
			}
			var dropped map[string][]string
			if res, kept, check, dropped, why = relowerOne(check, key, path, rec.Root, rec.HasTests, kept, moved[key], relowering, asked, reach, askers[key], withBodies, c.files.Logf); why != "" {
				if !chooseAskers(dropped, key, byPath, reach, asked, askers, recheck) {
					return nil, false
				}
				c.files.Logf("lowering again files that name what %s no longer makes: %s", key, why)
				why, again = "", true
				break
			}
			relowering[path] = false
			f := newRec.Files[key]
			si, ok := pi.StoredOf(key)
			if !ok {
				return nil, false
			}
			f.Iface, f.Facts = si, facts[key]
			if sum, ok := sums[key]; ok {
				f.Sum = sum
			}
			if !isKeptFile(rec, key) {
				f.Keyword = keyword[key]
			}
			if appTypes {
				f.Body = appWalkOf(check.FA, check.Nodes)
			}
			f.Advice = nil
			if ownedBy(rec.Root, path) {
				f.Advice = fileAdvice(path, check.FA, check.Nodes, func() string {
					data, _ := os.ReadFile(path)
					return string(data)
				})
			}
			newRec.Files[key] = f
		}
	}
	if link := res.Undefined(slices.Sorted(maps.Keys(asked))); link != "" {
		why = "no file lowered again asked for " + link + " again"
		return nil, false
	}
	src := ""
	if rec.HasTests {
		data, err := os.ReadFile(c.path)
		if err != nil {
			return nil, false
		}
		src = string(data)
	}
	if p, ok = relinkedProgram(res, c.path, src, rec.Root, rec.HasTests, cfg); !ok {
		return nil, false
	}
	byFile := map[string]Diagnostics{}
	for _, f := range newRec.Files {
		byFile[f.Iface.Path] = f.Advice
	}
	advice := joinAdvice(byFile)
	p.advice = &advice
	if p.entry == nil && (p.HasMain() || rec.HasTests) || !p.runsClean() || p.Unsupported() != nil {
		why = "the program reaches something a machine cannot run"
		return nil, false
	}
	// An edit to a body can reach what nomi.toml's [effects] lists do
	// not allow; the load that is made instead reports it.
	if p.effectError(p.checkedRoots()...) != nil {
		why = "nomi.toml's [effects] lists do not allow the effects the program has"
		return nil, false
	}
	// Every typed literal the program checks at load, kept or lowered
	// again, is checked again: an edit elsewhere (its handler, a module's
	// listed currencies) can change its answer. One that fails is reported
	// by the whole load.
	if p.checkLiterals() != nil {
		why = "a typed literal does not check"
		return nil, false
	}
	relinks.Add(1)
	c.files.Logf("relinked %d of %d files", len(keys), len(newRec.Files))
	newRec.Kept = kept.Encode()
	// The record names the program's sources on their own (sourceTree):
	// the added ones join them and the removed ones leave.
	trees, files := c.files.Inputs()
	for _, key := range removedKeys {
		files = slices.DeleteFunc(files, func(f string) bool { return f == rec.Files[key].Iface.Path })
	}
	for _, key := range addedKeys {
		files = append(files, addedPaths[key])
	}
	p.recordTrees, p.recordFiles = trees, files
	if cfg.cacheable() {
		a := artifact{entry: c.path, root: rec.Root, hasTests: rec.HasTests, advice: advice}
		a.phrases, a.nouns = p.phraseTables()
		if p.HasMain() || rec.HasTests {
			if image, err := p.cacheImage(); err == nil {
				a.image = image
			}
		}
		if payload := a.encode(); payload != nil {
			c.entry.Store(trees, files, payload)
		}
		if payload := newRec.encode(); payload != nil {
			c.files.Store(trees, files, payload)
		}
	}
	return p, true
}

// chooseAskers adds to recheck, for each linkage name in dropped (the
// instances the file with key decl no longer makes that kept objects of
// files not lowered again name: irbuild.FileMiss.Dropped), one of the
// files that name it, unless one already chosen does, to be lowered again
// reading decl with its bodies (askers), and adds the name to asked. It
// reports false when a name has no such file of the program.
func chooseAskers(dropped map[string][]string, decl string, byPath map[string]string, reach, asked map[string]bool, askers map[string]map[string]bool, recheck map[string]bool) bool {
	if len(dropped) == 0 {
		return false
	}
	for _, link := range slices.Sorted(maps.Keys(dropped)) {
		var keys []string
		for _, path := range dropped[link] {
			if k, ok := byPath[path]; ok && reach[k] {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			return false
		}
		sort.Strings(keys)
		chosen := keys[0]
		for _, k := range keys {
			if askers[k][decl] {
				chosen = k
				break
			}
		}
		if askers[chosen] == nil {
			askers[chosen] = map[string]bool{}
		}
		askers[chosen][decl] = true
		recheck[chosen] = true
		asked[link] = true
	}
	return true
}

// templateFile is the key of the program's file whose declaration the
// linkage name link continues ("billing/util.first_or<...>"), the longest
// key of files that it does, or "".
func templateFile(link string, files map[string]bool) string {
	best := ""
	for key := range files {
		if strings.HasPrefix(link, key+".") && len(key) > len(best) {
			best = key
		}
	}
	return best
}

// isKeptFile reports whether rec holds the file with key.
func isKeptFile(rec *fileRecord, key string) bool {
	_, ok := rec.Files[key]
	return ok
}

// maxRelowered is how many files a relink checks and lowers again before it
// takes an edit that reaches a quarter of the program as a reason to check
// the whole program instead: each file's check against interfaces and its
// lowering link against every other file, so many of them cost more than
// one check of the whole.
const maxRelowered = 16

// importOrder orders keys so that a file comes after the files among keys
// it imports, directly or through others, and otherwise as keys are: a
// file is lowered after the files whose new objects it links against.
func importOrder(keys []string, pi *analysis.ProgramInterfaces) []string {
	in := map[string]bool{}
	for _, k := range keys {
		in[k] = true
	}
	done := map[string]bool{}
	out := make([]string, 0, len(keys))
	var visit func(k string)
	visit = func(k string) {
		if done[k] {
			return
		}
		done[k] = true
		if si, ok := pi.StoredOfEncoded(k); ok {
			for _, imp := range si.Imports {
				if in[imp] {
					visit(imp)
				}
			}
		}
		out = append(out, k)
	}
	for _, k := range keys {
		visit(k)
	}
	return out
}

// appWalkOf is the walk the application-field roots take of a file's
// bodies (analysis.AppWalkSummary), zero for a program with no application
// type.
func appWalkOf(fa *analysis.FileAnalysis, nodes []ast.Node) [32]byte {
	if fa == nil {
		return [32]byte{}
	}
	sum, _ := analysis.AppWalkSummary(fa, nodes)
	return sum
}
