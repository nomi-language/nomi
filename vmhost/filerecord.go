package vmhost

// File records: a kept program's files, kept so that the next command over
// the program after an edit relowers only the files the edit reaches.
//
// Beside the program's record (cache.go), a load that keeps a program also
// keeps a record of its files (kind "files"): each project file's
// interface (analysis.StoredInterface), the SHA-256 of its text, its facts
// for the program's reductions over bodies (analysis.FileFacts), the
// program's lowering kept per file (irbuild.KeptLowering), the supply side
// of its missing-impl check (analysis.ProgramSupply), and the application
// types the program's check worked out. When the program's record misses
// because sources changed, came or went and nothing else did (the
// manifest and every other input as they were: progcache's
// ChangedSources), relink checks and lowers just the files the edit
// reaches:
//
//   - each changed or added file's new interface is extracted, and the
//     program's files, the entry and what it imports, must be the kept ones
//     less the removed with the added;
//   - the files checked again are the changed and added ones, those that
//     import a file whose interface changed, and those that can see an
//     impl that came, went or changed (the relevant impl set); an edit that
//     reaches a quarter of the program is a miss;
//   - each is checked against the interfaces of the files it reads
//     (analysis.NewStoredInterfaces), with no diagnostic;
//   - the program's reductions over bodies (boot-only calls,
//     application-field roots, whole-file imports, missing impls) run over
//     the checked files' new facts and the others' stored ones
//     (analysis.ReduceFacts) and find nothing, the supply worked out again
//     from every file's interface when impls changed. For a program with
//     an application type, whose application-field roots no file's facts
//     answer, the walk those roots take of each checked file's bodies
//     (analysis.AppWalkSummary) must also be the kept one and find nothing
//     in them, so the roots find nothing as the kept program's did;
//   - each is lowered on its own and linked with the kept objects of the
//     others (irbuild.LowerFileAmong), after the files it imports among
//     them, which declines whatever it cannot answer as a whole lowering
//     would; an instance of another file's generic declaration no kept
//     object holds is made from that file read with its bodies.
//
// The program that links runs as a kept program does, after the same
// checks that it reaches nothing a machine cannot run. Both records are
// then written again for the new texts. Anything relink cannot do is a
// miss, and the command checks and lowers the whole program as it does
// with no cache. A file that declares keyword declarations (`phrase`,
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
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
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
}

const fileRecordMagic = "NOMIFILES3"

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
func (p *Program) keepFiles(c *Cache) {
	if c.fileEntry() == nil {
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
		c.files.Logf("not kept: %v", err)
		return
	}
	rec := &fileRecord{Entry: p.prog.Entry().Path, Root: p.prog.Root, HasTests: p.prog.HasTests,
		Files: map[string]keptFile{}, Kept: kept.Encode()}
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
	for key, si := range stored {
		m := byPath[si.Path]
		data, err := os.ReadFile(si.Path)
		if err != nil {
			return
		}
		rec.Files[key] = keptFile{Iface: si, Sum: sha256.Sum256(data), Body: appWalkOf(m.FA, m.Nodes),
			Keyword: declaresKeywords(m.Nodes), Facts: analysis.FileFactsOf(key, m.FA, m.Nodes, namer, len(rec.AppTypes) > 0)}
	}
	if fa := p.prog.Entry().FA; fa != nil {
		rec.Supply = analysis.SupplyOf(fa)
	}
	payload := rec.encode()
	if payload == nil {
		return
	}
	trees, files := p.inputs()
	c.files.Store(trees, files, payload)
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
		if r := recover(); r != nil {
			p, ok, why = nil, false, "a panic relinking"
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
	changed, added, removed, same := c.files.ChangedSources()
	if !same || len(changed)+len(added)+len(removed) == 0 {
		why = "a file other than a source changed, or no file changed"
		return nil, false
	}
	byPath := map[string]string{}
	for key, f := range rec.Files {
		byPath[f.Iface.Path] = key
	}
	// The program's files whose texts changed, and those removed. A source
	// that is not one of them changes the program only if a changed file
	// now imports it.
	var changedKeys, removedKeys, addedKeys []string
	for _, path := range changed {
		if key, ok := byPath[path]; ok {
			if rec.Files[key].Keyword {
				why = path + " declares keyword declarations"
				return nil, false
			}
			changedKeys = append(changedKeys, key)
		}
	}
	for _, path := range removed {
		if key, ok := byPath[path]; ok {
			removedKeys = append(removedKeys, key)
		}
	}
	addedPaths := map[string]string{}
	for _, path := range append(append([]string(nil), added...), changed...) {
		if _, ok := byPath[path]; ok {
			continue
		}
		rel, err := filepath.Rel(rec.Root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
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
	fc.SourceBoundProvided = true
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
	// the kept ones less the removed, with the added that are imported.
	reach := pi.ImportClosure(entryKey)
	for _, key := range addedKeys {
		if !reach[key] {
			pi.RemoveFile(key)
			delete(ifaceChanged, key)
		}
	}
	addedKeys = slices.DeleteFunc(addedKeys, func(k string) bool { return !reach[k] })
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
	// The files to check again: the changed and added ones, the files that
	// import a file whose interface changed, and the files that can see an
	// impl that came, went or changed (the relevant impl set).
	recheck := map[string]bool{}
	for _, key := range append(append([]string(nil), changedKeys...), addedKeys...) {
		recheck[key] = true
	}
	var ifaceKeys []string
	for key := range ifaceChanged {
		ifaceKeys = append(ifaceKeys, key)
	}
	sort.Strings(ifaceKeys)
	for key := range pi.Importers(ifaceKeys) {
		if reach[key] {
			recheck[key] = true
		}
	}
	implsChanged := len(addedKeys) > 0 || len(removedKeys) > 0
	var headers []analysis.ImplHeader
	for _, key := range ifaceKeys {
		var now []analysis.ImplHeader
		if reach[key] {
			now = pi.ImplHeadersOf(key)
		}
		was := rec.Files[key].Iface.Headers
		if !sameHeaderFiles(was, now) {
			implsChanged = true
			headers = append(append(headers, was...), now...)
		}
	}
	if len(headers) > 0 {
		for key := range reach {
			visible := pi.ImportClosure(key)
			for _, h := range headers {
				if seesHeader(h, visible) {
					recheck[key] = true
					break
				}
			}
		}
	}
	keys := make([]string, 0, len(recheck))
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
	kept, err := irbuild.DecodeKeptLowering(rec.Kept)
	if err != nil {
		why = "the kept lowering does not read"
		return nil, false
	}
	var res *irbuild.Result
	newRec := *rec
	newRec.Files = map[string]keptFile{}
	for key, f := range rec.Files {
		if want[key] {
			newRec.Files[key] = f
		}
	}
	supply := rec.Supply
	if implsChanged {
		supply = pi.Supply(entryKey)
		newRec.Supply = supply
	}
	checks := map[string]*analysis.FileCheck{}
	facts := map[string]*analysis.FileFacts{}
	for key, f := range newRec.Files {
		if f.Facts == nil {
			why = key + " has no facts"
			return nil, false
		}
		facts[key] = f.Facts
	}
	appTypes := len(rec.AppTypes) > 0
	var std analysis.StdContext
	for _, key := range keys {
		check := pi.Check(key)
		if len(check.Build)+len(check.Check)+len(check.EntryChecks)+len(check.FrontEnd) > 0 {
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
		checks[key] = check
		if s, ok := analysis.StdContextOf(check.FA); ok {
			std = s
		} else {
			why = "the program is not built over the stdlib's base"
			return nil, false
		}
	}
	red := analysis.ReduceFacts(facts, supply, std)
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
		recheck := func(bodies map[string]bool) *analysis.FileCheck {
			pi.SetBodies(bodies)
			defer pi.SetBodies(nil)
			return pi.Check(key)
		}
		var check *analysis.FileCheck
		if res, kept, check, why = relowerOne(checks[key], key, path, rec.Root, rec.HasTests, kept, moved[key], relowering, reach, recheck, c.files.Logf); why != "" {
			return nil, false
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
		newRec.Files[key] = f
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
	if p.entry == nil && (p.HasMain() || rec.HasTests) || !p.runsClean() || p.Unsupported() != nil {
		why = "the program reaches something a machine cannot run"
		return nil, false
	}
	relinks.Add(1)
	c.files.Logf("relinked %d of %d files", len(keys), len(newRec.Files))
	newRec.Kept = kept.Encode()
	trees, files := c.files.Inputs()
	if cfg.cacheable() {
		a := artifact{entry: c.path, root: rec.Root, hasTests: rec.HasTests}
		a.phrases, a.nouns = p.phraseTables()
		if p.HasMain() || rec.HasTests {
			if image, err := p.cacheImage(); err == nil {
				a.image = image
			}
		}
		c.entry.Store(trees, files, a.encode())
		if payload := newRec.encode(); payload != nil {
			c.files.Store(trees, files, payload)
		}
	}
	return p, true
}

// sameHeaderFiles reports whether two lists of a file's impl headers name
// the same files, impl by impl, whatever their ordinals.
func sameHeaderFiles(a, b []analysis.ImplHeader) bool {
	key := func(hs []analysis.ImplHeader) []string {
		out := make([]string, len(hs))
		for i, h := range hs {
			out[i] = strings.Join(h.Files, "\x00")
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(key(a), key(b))
}

// seesHeader reports whether a check that reads the files in visible sees
// the impl h heads: every project file its header names is among them.
func seesHeader(h analysis.ImplHeader, visible map[string]bool) bool {
	for _, k := range h.Files {
		if !visible[k] {
			return false
		}
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
