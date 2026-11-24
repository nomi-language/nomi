package analysis

import (
	"maps"
	"slices"
	"sort"
	"sync/atomic"
)

// programDemands is the missing-impl check over a program's demanders:
// what each file's bodies demand, the union of those demands bucket by
// bucket, and the missing-impl diagnostic each bucket draws. MissingImpls
// works one out afresh; FinalizeCoherence keeps one on the program, up to
// date file by file. A file's part is valid while the program holds the
// same analysis with the same body part (the analysis pointer and
// FileBodies.generation); a recheck that rebuilds a file's bodies
// (walkBodiesAgain) gives them a new generation. Each call reads again only
// the files whose part is no longer valid, and works out again only the
// buckets those files demanded before or demand now. A reduction over
// stored facts (reduceCoherence) gathers its buckets from the facts and
// settles each by the same rule (missingInBucket, missingInOrder).
//
// The supply side, the program's impl index and every file's own Impls,
// comes from the program's declarations, which a body edit does not change.
// When the index, the base or the list of files differs from the ones the
// cache was built over, or a re-read file's own Impls differ, the cache is
// built again.
type programDemands struct {
	// entry, when set, is the file whose bodies' demands count: every other
	// file contributes only what its declarations demand (manifest).
	entry *FileAnalysis
	// stdOwn is whether the check is a std module's own (stdMissingImpls),
	// whose std demands count. A program's check leaves them to it (wants).
	stdOwn bool
	// idx is the impl index and files the demanders the cache was built
	// over. owner is the program's stdlib base, whose files' impls idx
	// holds, and base is owner when files holds every one of its files, or
	// nil.
	idx         *ProjectImplIndex
	files       []*FileAnalysis
	owner, base *StdBase
	// own is each file's part, by its place in files.
	own []fileDemands
	// buckets holds every bucket a file or the base demands.
	buckets map[implPair]*demandBucket
	// extra is the union of the Impls of the program's files owner does not
	// own.
	extra map[string]map[string]bool
	// supply is the snapshot a bucket is read against (missingImpl): the
	// index's impls and identity coverage of each type a bucket names, with
	// extra's impls laid over them and uncovered.
	supply *ProjectImplIndex
	// missing is the diagnostic of each bucket that draws one.
	missing map[implPair]TypeError
}

// fileDemands is one file's part of a programDemands.
type fileDemands struct {
	generation uint64
	// has is every bucket of the file's own ImplManifest, and wants those
	// a recording the check reads demands (programDemands.wants). A file
	// base owns has neither: the base answers for it.
	has, wants []implPair
	// impls is a copy of the file's own Impls when it was read, nil for a
	// file owner owns.
	impls map[string]map[string]bool
}

// demandBucket is one (interface, type) bucket of a programDemands.
type demandBucket struct {
	// from holds the places in files of the files whose ImplManifest holds
	// the bucket, in order, and wanted how many of them want it, one more
	// when the base wants it.
	from   []int
	wanted int
	// recs is the bucket's recordings, each once in position order, while
	// wanted is above zero.
	recs []Recording
}

// staleDemands, when set, makes programDemands keep a file's part after
// the file's bodies are rebuilt. Only the test that holds the incremental
// harness to catching a stale part sets it.
var staleDemands atomic.Bool

// finalizeCoherence answers MissingImpls(p, p.Files()) from p's cache of
// per-file demands, bringing it up to date first.
func (p *Program) finalizeCoherence() []TypeError {
	return p.missingImplsKept(&p.demands, nil)
}

// entryCoherence answers MissingImpls over every file of p with the demands
// entry's bodies make and only those the other files' declarations make
// (FileAnalysis.declManifest), from p's cache of them.
func (p *Program) entryCoherence(entry *FileAnalysis) []TypeError {
	return p.missingImplsKept(&p.entryDemands, entry)
}

// missingImplsKept answers the missing impls of the cache *kept, bringing it
// up to date first, or building it over entry's view (programDemands.entry)
// when it has none, or one over another view.
func (p *Program) missingImplsKept(kept **programDemands, entry *FileAnalysis) []TypeError {
	if p == nil || p.ImplIndex == nil {
		return nil
	}
	p.demandsMu.Lock()
	defer p.demandsMu.Unlock()
	if d := *kept; d == nil || d.entry != entry || !d.update(p) {
		*kept = newProgramDemands(p, entry, p.Files(), false)
	}
	return (*kept).diagnostics()
}

// newProgramDemands reads every file of files, p's files or some of them,
// and works out every bucket. The supply is p's whole: its index, and the
// own impls of every file of p the index does not answer for. stdOwn makes
// it a std module's own check (programDemands.stdOwn).
func newProgramDemands(p *Program, entry *FileAnalysis, files []*FileAnalysis, stdOwn bool) *programDemands {
	d := &programDemands{
		entry:   entry,
		stdOwn:  stdOwn,
		idx:     p.ImplIndex,
		files:   slices.Clone(files),
		owner:   p.stdBase,
		own:     make([]fileDemands, len(files)),
		buckets: map[implPair]*demandBucket{},
		extra:   map[string]map[string]bool{},
		missing: map[implPair]TypeError{},
	}
	if p.stdBase.ownsAll(files) {
		d.base = p.stdBase
		for _, b := range d.base.wanted {
			d.bucket(b).wanted++
		}
	}
	read := make(map[*FileAnalysis]bool, len(files))
	for i, fa := range files {
		d.own[i] = d.read(fa)
		d.add(i)
		read[fa] = true
		d.addExtra(d.own[i].impls)
	}
	for _, fa := range p.Files() {
		if !read[fa] && !d.owner.Owns(fa) {
			noteStdlibWork(fa.Origin)
			d.addExtra(fa.Impls)
		}
	}
	d.supply = &ProjectImplIndex{
		Impls:                 map[string]map[string]bool{},
		ImplsByIdentity:       d.idx.ImplsByIdentity,
		implNamesWithIdentity: map[string]map[string]bool{},
	}
	for b := range d.buckets {
		d.settle(b)
	}
	return d
}

// addExtra lays impls, one file's own, over d.extra.
func (d *programDemands) addExtra(impls map[string]map[string]bool) {
	for t, ifaces := range impls {
		if d.extra[t] == nil {
			d.extra[t] = map[string]bool{}
		}
		maps.Copy(d.extra[t], ifaces)
	}
}

// update brings the cache up to date with p, reading again the files whose
// bodies were rebuilt, and answers false when it must be built again
// instead.
func (d *programDemands) update(p *Program) bool {
	files := p.Files()
	if d.idx != p.ImplIndex || d.owner != p.stdBase || len(files) != len(d.files) {
		return false
	}
	var changed []int
	for i, fa := range files {
		if fa != d.files[i] {
			return false
		}
		if d.own[i].generation != fa.generation {
			changed = append(changed, i)
		}
	}
	if staleDemands.Load() {
		return true
	}
	touched := map[implPair]bool{}
	for _, i := range changed {
		next := d.read(d.files[i])
		if !maps.EqualFunc(next.impls, d.own[i].impls, maps.Equal) {
			return false
		}
		d.remove(i)
		for _, b := range d.own[i].has {
			touched[b] = true
		}
		d.own[i] = next
		d.add(i)
		for _, b := range next.has {
			touched[b] = true
		}
	}
	for b := range touched {
		d.settle(b)
	}
	return true
}

// read answers fa's part.
func (d *programDemands) read(fa *FileAnalysis) fileDemands {
	part := fileDemands{generation: fa.generation}
	if d.owner.Owns(fa) && d.base != nil {
		return part
	}
	noteStdlibWork(fa.Origin)
	if !d.owner.Owns(fa) {
		part.impls = cloneImpls(fa.Impls)
	}
	if d.base.Owns(fa) {
		return part
	}
	// A file's checker records at the file's own path.
	leftToStd := !d.wants(Recording{Pos: Pos{File: fa.FilePath}})
	for iface, byType := range d.manifest(fa) {
		for typeName, recs := range byType {
			b := implPair{typeName: typeName, iface: iface}
			part.has = append(part.has, b)
			if leftToStd {
				continue
			}
			for _, rec := range recs {
				if d.wants(rec) && !fa.demandOfNoType(typeName, rec) {
					part.wants = append(part.wants, b)
					break
				}
			}
		}
	}
	return part
}

// wants reports whether the demand rec makes its bucket one the check
// reads. A program's check reads the demands made outside std. Std's own
// demands, of every kind, are its own check's (stdMissingImpls), which
// reports a gap once, where std is checked, and not in every program that
// loads the module.
func (d *programDemands) wants(rec Recording) bool {
	return d.stdOwn || !isStdlibRecording(rec)
}

// manifest is the demands fa contributes: all of its own, or under an
// entry's view, those of its declarations when it is another file a
// project build kept. A stdlib module built from its interface keeps its
// declarations too, but its demands are every body's (std.Shared).
func (d *programDemands) manifest(fa *FileAnalysis) map[string]map[string][]Recording {
	if _, isStd := stdlibModuleForPath(fa.FilePath); d.entry != nil && fa != d.entry && fa.keptDecls() != nil && !isStd {
		return fa.keptDecls().manifest
	}
	return fa.ImplManifest
}

// cloneImpls copies a file's Impls down to its inner maps.
func cloneImpls(m map[string]map[string]bool) map[string]map[string]bool {
	if m == nil {
		return nil
	}
	out := make(map[string]map[string]bool, len(m))
	for t, ifaces := range m {
		out[t] = maps.Clone(ifaces)
	}
	return out
}

// bucket answers b's bucket, making it when there is none.
func (d *programDemands) bucket(b implPair) *demandBucket {
	bk := d.buckets[b]
	if bk == nil {
		bk = &demandBucket{}
		d.buckets[b] = bk
	}
	return bk
}

// add enters file i's part in its buckets.
func (d *programDemands) add(i int) {
	for _, b := range d.own[i].has {
		bk := d.bucket(b)
		at, _ := slices.BinarySearch(bk.from, i)
		bk.from = slices.Insert(bk.from, at, i)
	}
	for _, b := range d.own[i].wants {
		d.bucket(b).wanted++
	}
}

// remove takes file i's part out of its buckets.
func (d *programDemands) remove(i int) {
	for _, b := range d.own[i].has {
		bk := d.buckets[b]
		if at, found := slices.BinarySearch(bk.from, i); found {
			bk.from = slices.Delete(bk.from, at, at+1)
		}
	}
	for _, b := range d.own[i].wants {
		d.buckets[b].wanted--
	}
}

// settle works out bucket b's recordings and diagnostic again: the
// recordings of the files that hold it in file order, then the base's
// (missingInBucket). A demand of a type with a part of no type, or a
// derive's demand of such a part, is left out (demandOfNoType) and makes
// no bucket wanted (read), as the reduction over stored facts leaves it
// out. It is linear in the bucket's recordings, which for a pair like
// (Add, Int) come from most files.
func (d *programDemands) settle(b implPair) {
	bk := d.buckets[b]
	delete(d.missing, b)
	if bk.wanted == 0 {
		bk.recs = nil
		if len(bk.from) == 0 {
			delete(d.buckets, b)
		}
		return
	}
	n := 0
	for _, i := range bk.from {
		n += len(d.manifest(d.files[i])[b.iface][b.typeName])
	}
	var baseRecs []Recording
	if d.base != nil {
		baseRecs = d.base.demands[b.iface][b.typeName]
	}
	recs := make([]Recording, 0, n+len(baseRecs))
	for _, i := range bk.from {
		fa := d.files[i]
		for _, rec := range d.manifest(fa)[b.iface][b.typeName] {
			if !fa.demandOfNoType(b.typeName, rec) {
				recs = append(recs, rec)
			}
		}
	}
	recs = append(recs, baseRecs...)
	var e TypeError
	var ok bool
	bk.recs, e, ok = missingInBucket(b, recs, func() *ProjectImplIndex {
		d.supplyFor(b.typeName)
		return d.supply
	})
	if ok {
		d.missing[b] = e
	}
}

// missingInBucket answers the recordings of bucket b, recs (the demanding
// files' in order, then the stdlib base's) each once and sorted by
// position, and the missing-impl diagnostic they draw against supply's
// answer for b's type, if any. A check (programDemands.settle) and a
// reduction over stored facts (reduceCoherence) settle each bucket here.
func missingInBucket(b implPair, recs []Recording, supply func() *ProjectImplIndex) ([]Recording, TypeError, bool) {
	sortRecordings(recs)
	recs = dropSortedDuplicates(recs)
	if !missingImplsChecked(b.iface) {
		return recs, TypeError{}, false
	}
	e, ok := supply().missingImpl(b.iface, b.typeName, recs, nil)
	return recs, e, ok
}

// dropSortedDuplicates drops each recording of recs, sorted by
// sortRecordings, that equals an earlier one, in place. Equal recordings
// sort alike, so each is in the run of recordings at its position, and the
// stable sort kept each run in the order the recordings came in: what is
// left is what dropping duplicates and then sorting leaves.
func dropSortedDuplicates(recs []Recording) []Recording {
	out := recs[:0]
	run := 0
	for _, rec := range recs {
		if len(out) > run {
			last := out[len(out)-1]
			if last.Pos != rec.Pos || last.Kind != rec.Kind {
				run = len(out)
			}
		}
		if !slices.Contains(out[run:], rec) {
			out = append(out, rec)
		}
	}
	return out
}

// supplyFor enters typeName's supply in d.supply, once: the index's impls
// and identity coverage of the type, with extra's impls of it laid over
// them, and each such pair the index does not hold uncovered.
func (d *programDemands) supplyFor(typeName string) {
	if _, done := d.supply.Impls[typeName]; done {
		return
	}
	impls := maps.Clone(d.idx.Impls[typeName])
	covered := maps.Clone(d.idx.implNamesWithIdentity[typeName])
	for iface := range d.extra[typeName] {
		if impls == nil {
			impls = map[string]bool{}
		}
		if !d.idx.Impls[typeName][iface] {
			delete(covered, iface)
		}
		impls[iface] = true
	}
	d.supply.Impls[typeName] = impls
	d.supply.implNamesWithIdentity[typeName] = covered
}

// diagnostics answers every bucket's diagnostic, ordered by interface and
// then type (missingInOrder).
func (d *programDemands) diagnostics() []TypeError {
	return missingInOrder(d.missing)
}

// missingInOrder answers the diagnostics of missing, ordered by interface
// and then type, as DetectMissingImpls orders them.
func missingInOrder(missing map[implPair]TypeError) []TypeError {
	if len(missing) == 0 {
		return nil
	}
	keys := make([]implPair, 0, len(missing))
	for b := range missing {
		keys = append(keys, b)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].iface != keys[j].iface {
			return keys[i].iface < keys[j].iface
		}
		return keys[i].typeName < keys[j].typeName
	})
	errs := make([]TypeError, len(keys))
	for i, b := range keys {
		errs[i] = missing[b]
	}
	return errs
}
