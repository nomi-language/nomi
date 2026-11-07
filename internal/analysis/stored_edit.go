package analysis

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/parser"
)

// Editing a program opened from its record.
//
// A program opened from its record (stored_program.go) stays opened from
// it across edits that leave it with nothing to report, as `nomi check`
// relinks a kept program from its file records (vmhost/filerecord.go). When
// a file holds a text other than the one the program's state was made for,
// advanceStored:
//
//   - extracts each changed file's new interface and keeps the impl
//     headers of one whose interface hash is its old one's;
//   - requires the program's files, the entry and what it imports, to be
//     the ones it had;
//   - checks again against the others' interfaces the changed files, the
//     files that import one whose interface hash changed, directly or
//     through others, and the files that can see an impl that came, went
//     or changed (the relevant impl set), each with nothing to report and
//     no keyword declaration, whose expansion reads more than the file;
//   - runs the program's reductions over bodies over the checked files'
//     new facts and every other file's stored ones (ReduceFacts), with the
//     missing-impl supply worked out again when impls changed, and requires
//     them to find nothing. A program with an application type also
//     requires each checked file's body summary to be its old one, since
//     its application-field roots follow calls into other files' bodies by
//     a walk no file's facts reproduce.
//
// The checks made are kept (storedProgram.fresh) for the analyses of the
// open documents and closed files among them, and the program's state is
// the new one: each changed file's interface, text, facts and body summary.
// An open document the edit rechecks must import a changed file, so that
// the propagation of the edit to the files that import it analyzes it
// again; a closed file's cached analysis among them is dropped. A document
// whose lowering the record answered no longer has that answer when the
// edit reaches a file it reads.
//
// Anything else (a syntax error, a diagnostic, a reduction that finds
// something, an edit that reaches more than maxStoredRechecked files and a
// quarter of the program, a file that came or went) forgets the program
// opened from its record, and the program is checked whole, as before.
// The state is the program's in memory only: the record in the artifact
// cache is written again only when the server's lowering finds nothing
// with every file on disk as the state holds it (NoteLoweredClean).

// maxStoredRechecked is how many files an edit of a program opened from
// its record checks again before it takes an edit that reaches a quarter
// of the program as a reason to check the whole program: each file's check
// against interfaces registers its closure, so many of them cost more than
// one check of the whole. vmhost's relink keeps the same bound.
const maxStoredRechecked = 16

// storedChanges answers the texts of the files of sp's program that do not
// hold the text its state was made for, by module key, reading the open
// documents' texts from open and the others from disk; ok is false when a
// file cannot be read.
func (dm *DocumentManager) storedChanges(sp *storedProgram, open map[string]string) (changed map[string]string, ok bool) {
	// An input of the files' expansions that holds another text (an open
	// catalog's edit, a catalog changed on disk) changes what files the
	// record does not say expand to.
	for path, in := range sp.rec.Inputs {
		text, found := dm.readInput(path)
		if found != in.Found || found && sha256.Sum256([]byte(text)) != in.Sum {
			return nil, false
		}
	}
	changed = map[string]string{}
	for key, f := range sp.rec.Files {
		text, isOpen := open[key]
		if !isOpen {
			data, err := dm.readProjectFile(filepath.Join(sp.rec.Root, filepath.FromSlash(key)+".nomi"))
			if err != nil {
				return nil, false
			}
			text = data
		}
		if was, seen := sp.texts[key]; seen && was == text {
			continue
		}
		if sha256.Sum256([]byte(text)) == f.Sum {
			sp.texts[key] = text
			continue
		}
		changed[key] = text
	}
	return changed, true
}

// advanceStored makes sp's state the program's after the changed texts
// (see above), or answers why it cannot. building is the module key of
// the open document whose analysis asked, "" for a closed file's. The
// caller holds analyzeMu, and forgets sp when it answers a reason.
func (dm *DocumentManager) advanceStored(sp *storedProgram, changed map[string]string, building string) (why string) {
	defer func() {
		if r := recover(); r != nil {
			why = fmt.Sprint("a panic: ", r)
		}
	}()
	// A check the last edit made that no analysis took is of other texts.
	sp.fresh = nil
	rec := sp.rec
	if rec.Supply == nil {
		return "the record has no supply"
	}
	for key := range rec.Files {
		if rec.Files[key].Facts == nil {
			return key + " has no facts"
		}
	}
	keys := slices.Sorted(maps.Keys(changed))
	parsed := map[string]*parsedFile{}
	for _, key := range keys {
		pf := parseFile(changed[key])
		if expandsKeywords(pf.nodes) {
			return key + " declares keyword declarations"
		}
		parsed[key] = pf
	}
	// From here sp's interfaces are the new texts': a failure forgets sp.
	oldTexts := map[string]string{}
	for _, key := range keys {
		oldTexts[key] = sp.texts[key]
		sp.texts[key] = changed[key]
	}
	pi := sp.pi
	ifaceChanged := map[string]bool{}
	for _, key := range keys {
		was := rec.Files[key].Iface
		if !pi.AddFile(key, was.Path) {
			return key + " does not read"
		}
		si, _ := pi.StoredOfEncoded(key)
		if si.Hash != was.Hash {
			ifaceChanged[key] = true
		} else {
			pi.SetImplHeaders(key, was.Headers)
			if InferredOncesChanged(was.Encoded, si.Encoded) {
				ifaceChanged[key] = true
			}
		}
	}
	// The program's files must be the ones it had.
	reach := pi.ImportClosure(rec.Entry)
	if len(reach) != len(rec.Files) {
		return "the program's files changed"
	}
	for key := range reach {
		if _, ok := rec.Files[key]; !ok {
			return "the program's files changed"
		}
	}
	// The files to check again.
	recheck := map[string]bool{}
	for _, key := range keys {
		recheck[key] = true
	}
	ifaceKeys := slices.Sorted(maps.Keys(ifaceChanged))
	for key := range pi.Importers(ifaceKeys) {
		recheck[key] = true
	}
	implsChanged := false
	var headers []ImplHeader
	for _, key := range ifaceKeys {
		was, now := rec.Files[key].Iface.Headers, pi.ImplHeadersOf(key)
		if !sameImplHeaderFiles(was, now) {
			implsChanged = true
			headers = append(append(headers, was...), now...)
		}
	}
	if len(headers) > 0 {
		for key := range reach {
			visible := pi.ImportClosure(key)
			for _, h := range headers {
				if h.relevantTo(visible) {
					recheck[key] = true
					break
				}
			}
		}
	}
	if len(recheck) > maxStoredRechecked && len(recheck)*4 > len(reach) {
		return fmt.Sprintf("the edit reaches %d of the program's %d files", len(recheck), len(reach))
	}
	// An open document rechecked must be analyzed again by the edit's
	// propagation, which reaches the files that import a changed file.
	open := dm.openKeys(rec.Root)
	for key := range recheck {
		if !open[key] || key == building || changed[key] != "" {
			continue
		}
		closure := pi.ImportClosure(key)
		reaches := false
		for _, k := range keys {
			reaches = reaches || closure[k]
		}
		if !reaches {
			return "an open document that imports no changed file sees a changed impl"
		}
	}
	appTypes := len(rec.AppTypes) > 0
	facts := map[string]*FileFacts{}
	checks := map[string]*FileCheck{}
	factsMoved := false
	bodies := map[string][32]byte{}
	var std StdContext
	for _, key := range slices.Sorted(maps.Keys(recheck)) {
		var c *FileCheck
		if inst := sp.installed[key]; key == building && inst != nil && !ifaceChanged[key] && sp.current(inst, key) {
			// The open document's installed check, its bodies rebuilt in
			// place (stored_inplace.go).
			why, written := dm.rebuildStoredBodies(sp, key, oldTexts[key], parsed[key], inst.c)
			if why == "" {
				c = inst.c
				dm.programCounts.storedInPlace.Add(1)
			} else {
				dm.programCounts.note("not rebuilt in place: " + why)
				if written {
					delete(sp.installed, key)
				}
			}
		}
		if inst := sp.installed[key]; c == nil && inst != nil && inst.c.closure != nil && sp.fresh[key] == nil {
			// The open document's installed check, the analyses of the files
			// the edit cannot reach taken from it (CheckAfter), which hands
			// them its program: readers of the installed analysis are kept
			// away until the build's answer is installed, as an in-place
			// rebuild keeps them.
			if sp.held == nil && dm.lockView() {
				sp.held = dm.viewMu.Unlock
			}
			if sp.held != nil {
				dm.programCounts.built.Add(1)
				c = sp.pi.CheckAfter(key, inst.c, keys, headers)
				delete(sp.installed, key)
				if c.reused > 0 {
					dm.programCounts.checksAfter.Add(1)
				}
			}
		}
		if c == nil {
			c = sp.check(dm, key)
		}
		if expandsKeywords(c.Nodes) {
			return key + " declares keyword declarations"
		}
		if len(c.FA.Program().Errors) > 0 {
			// The program's own errors (impl collisions, the orphan rule) are
			// the entry's too, and a check of one file sees only the impls it
			// can see.
			return key + "'s check reports an error about the program's impls"
		}
		if syntax, damaged := sp.syntaxOf(key, parsed[key]); !c.clean() || len(syntax)+len(damaged) > 0 {
			if appTypes {
				return key + " has diagnostics, and the program has an application type"
			}
			if implsChanged {
				return key + " has diagnostics, and the edit changed an impl"
			}
		}
		if appTypes {
			// The application-field roots' walk crosses into other files'
			// bodies, which no file's facts hold: the walk of each checked
			// file's bodies must be its old one (AppWalkSummary), and find
			// nothing in them.
			var errs []TypeError
			bodies[key], errs = AppWalkSummary(c.FA, c.Nodes)
			if len(errs) > 0 {
				return key + "'s application-field roots report something, and the program has an application type"
			}
			if bodies[key] != rec.Files[key].Body {
				return key + "'s application-field walk changed, and the program has an application type"
			}
		}
		var namer *FactsNamer
		if c.namer != nil {
			// The check rebuilt in place reads the same files: only its own
			// declarations' names are made again.
			namer = c.namer
			namer.add(key, c.Nodes)
		} else {
			files := map[string][]ast.Node{key: c.Nodes}
			for k, n := range c.FileNodes {
				files[k] = n
			}
			namer = NewFactsNamer(files, rec.Entry)
			c.namer = namer
		}
		facts[key] = FileFactsOf(key, c.FA, c.Nodes, namer, appTypes)
		was, err := sp.factsOf(key)
		if err != nil {
			return key + "'s facts do not read"
		}
		if !sameFactsButPositions(facts[key], was) {
			factsMoved = true
		}
		var ok bool
		if std, ok = StdContextOf(c.FA); !ok {
			return "the program is not built over the stdlib's base"
		}
		checks[key] = c
	}
	supply := rec.Supply
	if implsChanged {
		// The entry sees every impl, so a changed impl checks it again; its
		// check reads every other file whole when the program is its closure,
		// and registers what the entry built against every other file's
		// interface registers (ProgramInterfaces.Supply), which costs a
		// second build of the whole program's declarations.
		if ec := checks[rec.Entry]; ec != nil && len(ec.closure)+1 == len(reach) && ec.closure != nil {
			supply = SupplyOf(ec.FA)
			if supplyChecks.on.Load() {
				supplyChecks.note(supply, pi.Supply(rec.Entry))
			}
		} else {
			supply = pi.Supply(rec.Entry)
		}
		if supply == nil {
			return "the program has no impl index"
		}
	}
	// The reductions found what the state holds over the stored facts.
	// Facts that differ from them in positions alone, over the same supply,
	// find nothing again when the state's reductions found nothing: a
	// position is only where a diagnostic is reported.
	var red *Reductions
	if factsMoved || implsChanged || sp.anyReduced() {
		all := maps.Clone(facts)
		for key := range rec.Files {
			if all[key] == nil {
				f, err := sp.factsOf(key)
				if err != nil {
					return key + "'s facts do not read"
				}
				all[key] = f
			}
		}
		red = ReduceFacts(all, supply, std)
		switch {
		case len(red.Coherence) > 0:
			// The entry reports every missing impl, and each file those its
			// own demands find, which no reduction over facts words apart.
			return "the program's reductions over bodies find a missing impl"
		case appTypes && !red.Clean(appTypes):
			return "the program's reductions over bodies find something, and the program has an application type"
		case !appTypes && !red.AppExact:
			return "a file's application-field roots are not its own"
		}
		// A file not checked again keeps its document: what the reductions
		// report in it must be what they reported.
		for key := range rec.Files {
			if checks[key] == nil && !reflect.DeepEqual(red.of(key), sp.reports[key].reductions()) {
				return "the reductions report something else in " + key + ", which the edit does not check again"
			}
		}
	}
	reports := map[string]*storedReport{}
	for key, c := range checks {
		syntax, damaged := sp.syntaxOf(key, parsed[key])
		rep := &storedReport{Syntax: syntax, Damaged: damaged}
		if red != nil {
			rep.Reduced = red.of(key)
		}
		rep.Errs = storedDocumentErrors(c, key == rec.Entry, sp.entryOwn, rep.Reduced, damaged)
		settleStoredImplImports(c.FA, rep.Reduced)
		if !rep.empty() {
			reports[key] = rep
		}
	}

	// Decided: the state is the new texts'.
	next := *rec
	// The kept lowering is of the texts before the edit.
	next.Kept, next.KeptTests = nil, false
	next.Files = maps.Clone(rec.Files)
	next.Supply = supply
	for key := range checks {
		f := next.Files[key]
		si, ok := pi.StoredOf(key)
		if !ok {
			return key + " has no interface"
		}
		f.Iface, f.Facts = si, facts[key].Encode()
		// What its analysis says now is its new check's.
		f.Refs = nil
		if appTypes {
			f.Body = bodies[key]
		}
		if text, ok := changed[key]; ok {
			f.Sum = sha256.Sum256([]byte(text))
		}
		next.Files[key] = f
	}
	// A document's lowering reads every file it imports.
	next.Lowered = map[string]bool{}
	for doc := range rec.Lowered {
		closure := pi.ImportClosure(doc)
		reached := false
		for key := range checks {
			reached = reached || closure[key]
		}
		if !reached {
			next.Lowered[doc] = true
		}
	}
	sp.rec = &next
	if sp.facts == nil {
		sp.facts = map[string]*FileFacts{}
	}
	for key := range checks {
		sp.facts[key] = facts[key]
	}
	if sp.reports == nil {
		sp.reports = map[string]*storedReport{}
	}
	for key := range checks {
		if reports[key] != nil {
			sp.reports[key] = reports[key]
		} else {
			delete(sp.reports, key)
		}
	}
	sp.fresh = checks
	sp.edits++
	sp.gen++
	if sp.textAt == nil {
		sp.textAt, sp.changedAt = map[string]int{}, map[string]int{}
	}
	if sp.fingerprints == nil {
		sp.fingerprints = map[string]string{}
	}
	for _, key := range keys {
		sp.textAt[key] = sp.gen
		sp.fingerprints[key] = parsed[key].fingerprint
		if !bytes.Equal(rec.Files[key].Iface.Encoded, next.Files[key].Iface.Encoded) {
			sp.changedAt[key] = sp.gen
		}
	}
	dm.programCounts.storedEdits.Add(1)
	dm.mu.Lock()
	for key := range checks {
		dm.closed.remove(fileuri.FromPath(filepath.Join(rec.Root, filepath.FromSlash(key)+".nomi")))
	}
	dm.mu.Unlock()
	return ""
}

// InferredOncesChanged reports whether two encoded interfaces of one file
// differ in an unannotated `once`'s value, positions aside. The interface
// hash leaves a `once`'s value out, as the language server's fingerprint
// does, since a value with the same type changes nothing another file
// reads; but an unannotated `once`'s type is its value's, so a file whose
// hash is unchanged may still give the files that import it another type.
// A build that checks only the edited file then checks its importers too.
// It answers true when either does not decode.
func InferredOncesChanged(was, now []byte) bool {
	values := func(data []byte) (string, bool) {
		fi, err := DecodeInterface(data)
		if err != nil {
			return "", false
		}
		var b strings.Builder
		for _, n := range fi.Nodes {
			if o, ok := n.(*ast.OnceBinding); ok && o.TypeAnnotation == nil {
				b.WriteString(o.Name)
				b.WriteByte('=')
				fingerprintValue(&b, reflect.ValueOf(o.Value))
				b.WriteByte('\n')
			}
		}
		return b.String(), true
	}
	a, ok1 := values(was)
	b, ok2 := values(now)
	return !ok1 || !ok2 || a != b
}

// sameFactsButPositions reports whether two files' facts say the same but
// for the lines and columns they hold (isPositionField).
func sameFactsButPositions(a, b *FileFacts) bool {
	if a == nil || b == nil {
		return a == b
	}
	return samePositionFree(reflect.ValueOf(a).Elem(), reflect.ValueOf(b).Elem())
}

func samePositionFree(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return samePositionFree(a.Elem(), b.Elem())
	case reflect.Struct:
		t := a.Type()
		for i := range t.NumField() {
			if isPositionField(t.Field(i)) {
				continue
			}
			if !samePositionFree(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := range a.Len() {
			if !samePositionFree(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.Len() != b.Len() {
			return false
		}
		for _, k := range a.MapKeys() {
			v := b.MapIndex(k)
			if !v.IsValid() || !samePositionFree(a.MapIndex(k), v) {
				return false
			}
		}
		return true
	}
	return a.Equal(b)
}

// StoredInPlace counts the edits taken in programs opened from their
// records that rebuilt an open document's installed analysis in place.
func (dm *DocumentManager) StoredInPlace() int64 { return dm.programCounts.storedInPlace.Load() }

// openKeys answers the module keys of the open documents under root.
func (dm *DocumentManager) openKeys(root string) map[string]bool {
	out := map[string]bool{}
	for key := range dm.openTexts(root, "", "") {
		out[key] = true
	}
	return out
}

// expandsKeywords reports whether nodes hold a keyword declaration's
// expansion.
func expandsKeywords(nodes []ast.Node) bool {
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
		case *ast.KeywordDecl, *ast.EmbedDecl:
			return true
		}
	}
	return false
}

// sameImplHeaderFiles reports whether two lists of a file's impl headers
// name the same files, impl by impl, whatever their ordinals.
func sameImplHeaderFiles(a, b []ImplHeader) bool {
	key := func(hs []ImplHeader) []string {
		out := make([]string, len(hs))
		for i, h := range hs {
			out[i] = strings.Join(h.Files, "\x00")
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(key(a), key(b))
}

// ChecksAfterEdits counts the checks of open documents an edit of a
// program opened from its record made from their installed checks
// (ProgramInterfaces.CheckAfter), taking some files' analyses from them.
func (dm *DocumentManager) ChecksAfterEdits() int64 { return dm.programCounts.checksAfter.Load() }

// StoredEdits counts the edits the manager answered in programs opened
// from their records, with no check of the whole program.
func (dm *DocumentManager) StoredEdits() int64 { return dm.programCounts.storedEdits.Load() }

// factsOf answers the facts of the file with key in sp's state, decoded
// once.
func (sp *storedProgram) factsOf(key string) (*FileFacts, error) {
	if f := sp.facts[key]; f != nil {
		return f, nil
	}
	f, err := DecodeFileFacts(sp.rec.Files[key].Facts)
	if err != nil {
		return nil, err
	}
	if sp.facts == nil {
		sp.facts = map[string]*FileFacts{}
	}
	sp.facts[key] = f
	return f, nil
}

// storedReport is what a file of a program opened from its record reports
// in the program's state, when it reports anything (storedProgram.reports):
// its syntax errors and repaired declarations, the diagnostics its document
// carries, assembled as documentErrors assembles them in the program
// checked whole, and of those what the program's reductions over bodies
// found in it.
type storedReport struct {
	Syntax  []parser.ParseError
	Damaged []parser.Span
	Errs    []TypeError
	Reduced storedReduced
}

// storedReduced is what the program's reductions over bodies report in one
// file: its boot-only calls, its application-field roots and its unused
// whole-file imports.
type storedReduced struct {
	Boot, App, Imports []TypeError
}

// of answers what r reports in the file with key.
func (r *Reductions) of(key string) storedReduced {
	return storedReduced{Boot: r.Boot[key], App: r.AppRoots[key], Imports: r.ImplImports[key]}
}

// reductions answers what the reductions report in r's file, none for a
// file that reports nothing.
func (r *storedReport) reductions() storedReduced {
	if r == nil {
		return storedReduced{}
	}
	return r.Reduced
}

// settleStoredImplImports settles each whole-file import of fa, a file
// checked against interfaces, as red, the reductions over bodies, words it:
// the program checked whole settles it on the file's analysis
// (CheckImplImports), and the document reads it there.
func settleStoredImplImports(fa *FileAnalysis, red storedReduced) {
	for _, ii := range fa.ImplImports {
		err := unusedImportError(ii.UnusedImport)
		unused := slices.ContainsFunc(red.Imports, func(e TypeError) bool { return reflect.DeepEqual(e, err) })
		fa.Program().settleImplImport(ii, unused)
	}
}

// empty reports whether r's file reports nothing.
func (r *storedReport) empty() bool {
	d := r.Reduced
	return len(r.Syntax)+len(r.Damaged)+len(r.Errs)+len(d.Boot)+len(d.App)+len(d.Imports) == 0
}

// anyReduced reports whether the reductions over bodies report something
// in sp's state.
func (sp *storedProgram) anyReduced() bool {
	for _, r := range sp.reports {
		d := r.Reduced
		if len(d.Boot)+len(d.App)+len(d.Imports) > 0 {
			return true
		}
	}
	return false
}

// syntaxOf answers the syntax errors and repaired declarations of the file
// with key: pf's, its new text parsed, or else its state's.
func (sp *storedProgram) syntaxOf(key string, pf *parsedFile) ([]parser.ParseError, []parser.Span) {
	if pf != nil {
		return pf.errs, pf.damaged
	}
	if r := sp.reports[key]; r != nil {
		return r.Syntax, r.Damaged
	}
	return nil, nil
}

// clean reports whether sp's state reports nothing in any file.
func (sp *storedProgram) clean() bool { return len(sp.reports) == 0 }

// storedDocumentErrors assembles the diagnostics the document of c's file
// carries, as documentErrors assembles them from the program checked
// whole: the file's own, from its check against interfaces, and red, what
// the program's reductions over bodies report in it. The program's own
// errors, its missing impls and its declaration checks (nouns and
// messages) are none: an edit after which they would be something checks
// the program whole. entryOwn is how many of the entry's nodes are its
// own.
func storedDocumentErrors(c *FileCheck, entry bool, entryOwn int, red storedReduced, damaged []parser.Span) []TypeError {
	fa := c.FA
	own := ownNodesOf(fa, c.Nodes, entry, entryOwn)
	errs := slices.Clone(c.Build)
	if entry {
		errs = append(errs, c.Check...)
		errs = append(errs, red.App...)
		errs = append(errs, c.EntryChecks...)
		errs = append(errs, red.Boot...)
		errs = append(errs, red.Imports...)
		errs = append(errs, c.FrontEnd...)
	} else {
		errs = append(errs, c.Check...)
		errs = append(errs, red.App...)
		errs = append(errs, CheckConcurrentScope(fa, own)...)
		errs = append(errs, red.Boot...)
		errs = append(errs, CheckTaskLifetime(fa, own)...)
		errs = append(errs, red.Imports...)
		errs = append(errs, CheckDuplicateTestNames(own)...)
	}
	return suppressInDamagedSpans(errs, damaged)
}

// supplyChecks, when on, makes an edit that takes the missing-impl supply
// from the entry's check work it out from every file's interface too
// (ProgramInterfaces.Supply), and counts the edits and those whose two
// supplies differ (TestOpenFromRecordEditsMatchAFullCheck).
var supplyChecks supplyCheck

// supplyCheck is supplyChecks' state.
type supplyCheck struct {
	on          atomic.Bool
	made, apart atomic.Int64
}

// note counts one supply taken from the entry's check, got, beside want,
// the one worked out from every file's interface.
func (s *supplyCheck) note(got, want *ProgramSupply) {
	s.made.Add(1)
	if got == nil || want == nil || !reflect.DeepEqual(got, want) {
		s.apart.Add(1)
	}
}
