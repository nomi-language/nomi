package analysis

import (
	"maps"
	"slices"
	"sort"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
)

// StdBase is the stdlib's share of a program's whole-program facts, worked
// out once per stdlib analysis rather than once per program: the stdlib's
// impl index with every identity pass run over it, the names its files
// declare for the orphan rule, and the conformance demands its bodies record.
//
// The stdlib's analysis is fixed once std.Load has checked it, and every
// program in a process is built against the same one, so every build used to
// derive the same stdlib facts again, the larger part of checking a small
// program. A build over a base (buildProjectWithCache, when the stdlib it is
// given is the base's) folds only its own files and lays them over the base:
// its index starts as a copy of the base's tables, and each table a program
// file adds to is copied before it is changed. A build never writes the base
// or anything it holds, so any number of builds may read it at once.
//
// What the base answers is what a build that folded the stdlib's files
// itself answers. A program could change the stdlib's share only by declaring
// a primitive under a name a stdlib file resolves, or by being a stdlib
// module; a build that does either works without the base (programShare).
type StdBase struct {
	// byName holds the stdlib's analyses by module name, as std.StdLib.Files
	// does; files holds them by module key ("std/iter").
	byName map[string]*FileAnalysis
	files  map[string]*FileAnalysis
	owns   map[*FileAnalysis]bool

	// index is the stdlib's impl index: buildProjectImplIndex over its files,
	// then every pass a project build runs over its index. Never written.
	index *ProjectImplIndex
	// covered and blocked are recordImplsByIdentity's answers over the
	// stdlib's impls, which a program's impls of the same pairs combine with.
	covered, blocked map[string]map[string]bool
	// primitives is primitiveOriginIndex over the stdlib's files.
	primitives map[string]string
	// names is every name a stdlib file's scope binds and every receiver
	// name a stdlib impl names: the names the stdlib's identity passes look a
	// primitive's origin up by.
	names map[string]bool
	// collisionGroups holds the stdlib's impls by (interface, method) slot
	// and then by the group detectImplCollisions puts each in.
	collisionGroups map[string]map[string]map[string][]slotImpl
	// enumDecls is PopulateEnumDecls' table with each declaration's file key.
	enumDecls map[string][]keyedSymbol
	// declModules is declaringModuleIndex over the stdlib's files.
	declModules map[string][]string
	// demands is the union of the stdlib files' own ImplManifests, each
	// bucket deduplicated in file-key order, and pairs their (type,
	// interface) pairs.
	demands map[string]map[string][]Recording
	pairs   map[implPair]bool
	// wanted is the buckets of demands unionDemands keeps: those some
	// recording outside the stdlib demands.
	wanted []implPair
}

// keyedSymbol is a declaration and the key of the file that declares it.
type keyedSymbol struct {
	key string
	sym *Symbol
}

// BuildStdBase works out the stdlib's share of the whole-program facts from
// its checked analyses (std.StdLib.Files, by module name) and records it on
// their Program, where every later build over the same analyses finds it.
// std.Load calls it once, after checking every module and before anything
// else reads the analyses. It answers nil, and records nothing, when the
// stdlib does not fit a base: when one of its type-promoted methods has no
// type or two of its impls collide, a build answers those from every file
// together.
func BuildStdBase(byName map[string]*FileAnalysis) *StdBase {
	if !stdBaseEnabled.Load() || len(byName) == 0 {
		return nil
	}
	b := &StdBase{
		byName: byName,
		files:  make(map[string]*FileAnalysis, len(byName)),
		owns:   make(map[*FileAnalysis]bool, len(byName)),
	}
	for name, fa := range byName {
		if fa == nil || fa.program == nil {
			return nil
		}
		b.files["std/"+name] = fa
		b.owns[fa] = true
	}
	idx := buildProjectImplIndex(b.files, nil, nil)
	b.primitives = primitiveOriginIndex(b.files)
	populateQualifiedReceivers(idx, b.files, b.primitives)
	PopulateImplIfaceOrigins(idx, b.files)
	b.covered, b.blocked = recordImplsByIdentity(idx, b.files, b.primitives)
	idx.implNamesWithIdentity = coveredNotBlocked(b.covered, b.blocked)
	addImplTypeArgs(idx, b.filesInKeyOrder())
	PopulateProjectImplFuncTypes(idx, b.files)
	PopulateProjectTypeMethodSymbols(idx, b.files)
	populateTypeMethodIdentities(idx, b.files, b.primitives)
	b.enumDecls = keyedEnumDecls(b.files)
	idx.EnumDecls = flattenEnumDecls(b.enumDecls)
	b.index = idx
	if !b.fits() {
		return nil
	}
	b.names = b.lookupNames()
	b.collisionGroups = stdCollisionGroups(idx)
	b.declModules = declaringModuleIndex(b.files, "", nil, nil, nil)
	b.demands, b.pairs, b.wanted = stdDemands(b.filesInKeyOrder())
	for _, fa := range b.files {
		fa.program.formsBase = b
	}
	return b
}

// fits reports whether every program's build may take the stdlib's share
// from b: every type-promoted method of the stdlib is typed, so no program
// method replaces one, and no two stdlib impls collide, so the stdlib's own
// dispatch slots hold no diagnostic a build would have to place among its
// own.
func (b *StdBase) fits() bool {
	for _, byMethod := range b.index.TypeMethods {
		for _, sym := range byMethod {
			if sym.Type == nil {
				return false
			}
		}
	}
	index, files := CollisionImplIndex(nil, nil, b.index)
	return len(detectImplCollisions(index, files, b.index.QualifiedReceiver, b.index.ImplBlockInterfaceKey, b.index.ImplIfaceOrigin)) == 0
}

// filesInKeyOrder answers the base's analyses in module-key order.
func (b *StdBase) filesInKeyOrder() []*FileAnalysis {
	out := make([]*FileAnalysis, 0, len(b.files))
	for _, key := range stdlibFirstKeys(b.files) {
		out = append(out, b.files[key])
	}
	return out
}

// lookupNames answers b.names.
func (b *StdBase) lookupNames() map[string]bool {
	names := map[string]bool{}
	for _, fa := range b.files {
		if fa.ModuleScope != nil {
			for name := range fa.ModuleScope.Symbols {
				names[name] = true
			}
		}
	}
	idx := b.index
	for _, recv := range idx.ImplBlockReceiver {
		names[typeNameHead(recv)] = true
	}
	for _, byMethod := range idx.IfaceMethodImpls {
		for _, fns := range byMethod {
			for _, fn := range fns {
				names[typeNameHead(idx.ReceiverOf(fn))] = true
			}
		}
	}
	for _, recv := range idx.ImplBlockReceiverExtern {
		names[typeNameHead(recv)] = true
	}
	for typeName := range idx.TypeMethods {
		names[typeNameHead(typeName)] = true
	}
	return names
}

// stdDemands answers the union of files' own ImplManifests, each bucket's
// recordings deduplicated in the order files gives, the set of their (type,
// interface) pairs, and the buckets unionDemands keeps for them: those some
// recording outside the stdlib demands.
func stdDemands(files []*FileAnalysis) (map[string]map[string][]Recording, map[implPair]bool, []implPair) {
	type bucketRec struct {
		pair implPair
		rec  Recording
	}
	demands := map[string]map[string][]Recording{}
	pairs := map[implPair]bool{}
	seen := map[bucketRec]bool{}
	for _, fa := range files {
		for iface, byType := range fa.ImplManifest {
			for typeName, recs := range byType {
				pair := implPair{typeName: typeName, iface: iface}
				pairs[pair] = true
				for _, rec := range recs {
					if seen[bucketRec{pair, rec}] {
						continue
					}
					seen[bucketRec{pair, rec}] = true
					if demands[iface] == nil {
						demands[iface] = map[string][]Recording{}
					}
					demands[iface][typeName] = append(demands[iface][typeName], rec)
				}
			}
		}
	}
	var wanted []implPair
	kept := map[implPair]bool{}
	for _, fa := range files {
		if isStdlibRecording(Recording{Pos: Pos{File: fa.FilePath}}) {
			continue
		}
		for iface, byType := range fa.ImplManifest {
			for typeName, recs := range byType {
				pair := implPair{typeName: typeName, iface: iface}
				if kept[pair] {
					continue
				}
				for _, rec := range recs {
					if !isStdlibRecording(rec) {
						kept[pair] = true
						wanted = append(wanted, pair)
						break
					}
				}
			}
		}
	}
	return demands, pairs, wanted
}

// stdBaseOf answers the base the analyses byName (by module name, as
// std.StdLib.Files holds them) form, or nil when they form none: they were
// not given to BuildStdBase together, or one of them was replaced.
func stdBaseOf(byName map[string]*FileAnalysis) *StdBase {
	if !stdBaseEnabled.Load() || len(byName) == 0 {
		return nil
	}
	var b *StdBase
	for _, fa := range byName {
		if fa != nil && fa.program != nil {
			b = fa.program.formsBase
		}
		break
	}
	if b == nil || len(b.byName) != len(byName) {
		return nil
	}
	for name, fa := range byName {
		if b.byName[name] != fa {
			return nil
		}
	}
	return b
}

// Owns reports whether fa is one of the base's analyses.
func (b *StdBase) Owns(fa *FileAnalysis) bool {
	return b != nil && b.owns[fa]
}

// ownsAll reports whether files holds every one of the base's analyses.
func (b *StdBase) ownsAll(files []*FileAnalysis) bool {
	if b == nil {
		return false
	}
	seen := make(map[*FileAnalysis]bool, len(b.owns))
	for _, fa := range files {
		if b.owns[fa] {
			seen[fa] = true
		}
	}
	return len(seen) == len(b.owns)
}

// stdBaseEnabled turns building over a base on. Only a test that compares
// the two ways of building a program turns it off.
var stdBaseEnabled atomic.Bool

func init() { stdBaseEnabled.Store(true) }

// SetStdBaseEnabled turns building over the stdlib's base (StdBase) on or
// off for the whole process and answers whether it was on. Off, every build
// folds the stdlib's files itself, as it did before the base existed: the
// tests that hold the two ways of building to one answer use it.
func SetStdBaseEnabled(on bool) bool {
	return stdBaseEnabled.Swap(on)
}

// countingStdlibWork turns on stdlibWork, which counts each visit a
// program-wide pass makes to a stdlib file or to an impl a stdlib file
// declares. Only a test counts: it holds a program's build to no such visit.
var (
	countingStdlibWork atomic.Bool
	stdlibWork         atomic.Int64
)

// noteStdlibWork counts a visit to the file with module key key, or to an
// impl that file declares, when counting is on and the file is the
// stdlib's.
func noteStdlibWork(key string) {
	if countingStdlibWork.Load() && IsStdlibKey(key) {
		stdlibWork.Add(1)
	}
}

// stdlibFirstKeys answers m's keys, the stdlib's first and then the rest,
// each in order.
func stdlibFirstKeys(m map[string]*FileAnalysis) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		si, sj := IsStdlibKey(keys[i]), IsStdlibKey(keys[j])
		if si != sj {
			return si
		}
		return keys[i] < keys[j]
	})
	return keys
}

// addImplTypeArgs adds each of files' interface type-argument templates to
// idx, in order: a later file's template for one (type, interface) pair
// replaces an earlier one's, and every file's template sets are appended.
func addImplTypeArgs(idx *ProjectImplIndex, files []*FileAnalysis) {
	for _, fa := range files {
		noteStdlibWork(fa.Origin)
		for typeName, byIface := range fa.ImplTypeArgs {
			if idx.ImplTypeArgs[typeName] == nil {
				idx.ImplTypeArgs[typeName] = make(map[string]*ImplTypeArgs)
			}
			for ifaceName, ita := range byIface {
				idx.ImplTypeArgs[typeName][ifaceName] = ita
			}
		}
		for typeName, byIface := range fa.ImplTypeArgSets {
			if idx.ImplTypeArgSets[typeName] == nil {
				idx.ImplTypeArgSets[typeName] = make(map[string][]*ImplTypeArgs)
			}
			for ifaceName, set := range byIface {
				idx.ImplTypeArgSets[typeName][ifaceName] = append(idx.ImplTypeArgSets[typeName][ifaceName], set...)
			}
		}
	}
}

// keyedEnumDecls answers every enum declaration of filesByKey by bare name,
// in key order and, within a file, in declaration order, each declaration
// once: PopulateEnumDecls' table with each declaration's file key.
func keyedEnumDecls(filesByKey map[string]*FileAnalysis) map[string][]keyedSymbol {
	keys := make([]string, 0, len(filesByKey))
	for key := range filesByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := map[string][]keyedSymbol{}
	seen := map[*Symbol]bool{}
	for _, key := range keys {
		fa := filesByKey[key]
		noteStdlibWork(key)
		if fa == nil {
			continue
		}
		var syms []*Symbol
		for _, sym := range fa.Definitions {
			if sym != nil && sym.Kind == SymbolEnum && !seen[sym] {
				seen[sym] = true
				syms = append(syms, sym)
			}
		}
		sort.Slice(syms, func(i, j int) bool { return earlierDeclaration(syms[i], syms[j]) })
		for _, sym := range syms {
			out[sym.Name] = append(out[sym.Name], keyedSymbol{key: key, sym: sym})
		}
	}
	return out
}

// flattenEnumDecls drops the keys of a keyedEnumDecls table.
func flattenEnumDecls(keyed map[string][]keyedSymbol) map[string][]*Symbol {
	out := make(map[string][]*Symbol, len(keyed))
	for name, decls := range keyed {
		syms := make([]*Symbol, len(decls))
		for i, d := range decls {
			syms[i] = d.sym
		}
		out[name] = syms
	}
	return out
}

// programShare is one build's own part of its whole-program facts over a
// StdBase: the build's files that are not the base's, their impl index, and
// what the identity passes found over it.
type programShare struct {
	base *StdBase
	// files is the build's files by key, the base's left out.
	files map[string]*FileAnalysis
	// index folds files alone, and the identity passes fill it from them.
	index *ProjectImplIndex
	// covered and blocked are recordImplsByIdentity's answers over index.
	covered, blocked map[string]map[string]bool
	// primitives is primitiveOriginIndex over the base's files and files.
	primitives map[string]string
}

// newProgramShare answers the share of a build whose files by key are
// filesByKey, stdlibFAs the stdlib it was given (by module name) and
// entryOrigin its entry's Origin, or nil when the build must fold the
// stdlib's files itself: no base, a stdlib file other than the base's, or an
// entry that is a stdlib module.
func newProgramShare(stdlibFAs, filesByKey map[string]*FileAnalysis, entryOrigin string) *programShare {
	b := stdBaseOf(stdlibFAs)
	if b == nil || IsStdlibKey(entryOrigin) {
		return nil
	}
	files := make(map[string]*FileAnalysis, len(filesByKey))
	for key, fa := range filesByKey {
		if IsStdlibKey(key) {
			if b.files[key] != fa {
				return nil
			}
			continue
		}
		if b.owns[fa] {
			return nil
		}
		files[key] = fa
	}
	return &programShare{base: b, files: files}
}

// fold folds the build's own files (buildProjectImplIndex over s.files, with
// the build's own impl tables) and answers the program's index: a copy of
// the base's with the build's impls laid over it.
func (s *programShare) fold(own *implTables, owned map[*FileAnalysis]bool) *ProjectImplIndex {
	s.index = buildProjectImplIndex(s.files, own, owned)
	x := s.base.index
	idx := &ProjectImplIndex{
		Impls:                       maps.Clone(x.Impls),
		ImplManifest:                maps.Clone(x.ImplManifest),
		IfaceMethodImpls:            maps.Clone(x.IfaceMethodImpls),
		ImplFiles:                   maps.Clone(x.ImplFiles),
		ImplTypeArgs:                maps.Clone(x.ImplTypeArgs),
		ImplTypeArgSets:             maps.Clone(x.ImplTypeArgSets),
		ImplFuncTypes:               maps.Clone(x.ImplFuncTypes),
		TypeMethods:                 maps.Clone(x.TypeMethods),
		TypeMethodModule:            maps.Clone(x.TypeMethodModule),
		ImplBlockReceiver:           maps.Clone(x.ImplBlockReceiver),
		ImplBlockInterfaceKey:       maps.Clone(x.ImplBlockInterfaceKey),
		QualifiedReceiver:           maps.Clone(x.QualifiedReceiver),
		ImplIfaceOrigin:             maps.Clone(x.ImplIfaceOrigin),
		IfaceOriginByPair:           maps.Clone(x.IfaceOriginByPair),
		ImplsByIdentity:             maps.Clone(x.ImplsByIdentity),
		implNamesWithIdentity:       maps.Clone(x.implNamesWithIdentity),
		TypeMethodByIdentity:        maps.Clone(x.TypeMethodByIdentity),
		TypeMethodModuleByIdentity:  maps.Clone(x.TypeMethodModuleByIdentity),
		TypeMethodIdentityConflicts: slices.Clip(x.TypeMethodIdentityConflicts),
		IfaceMethodImplExterns:      maps.Clone(x.IfaceMethodImplExterns),
		ImplExternFiles:             maps.Clone(x.ImplExternFiles),
		ImplBlockReceiverExtern:     maps.Clone(x.ImplBlockReceiverExtern),
		ImplBlockInterfaceKeyExtern: maps.Clone(x.ImplBlockInterfaceKeyExtern),
		EnumDecls:                   maps.Clone(x.EnumDecls),
	}
	s.layOver(idx)
	return idx
}

// identitiesFit reports whether the identity passes may take the stdlib's
// answers from the base, and sets s.primitives: none of the build's own
// files declares a primitive under a name the stdlib's passes look up, so
// every stdlib impl's identity is what the base found. Run after BuildTypes,
// which gives the build's declarations their types.
func (s *programShare) identitiesFit() bool {
	own := primitiveOriginIndex(s.files)
	for name := range own {
		if s.base.names[name] {
			return false
		}
	}
	s.primitives = maps.Clone(s.base.primitives)
	maps.Copy(s.primitives, own)
	return true
}

// populate runs the identity passes over the build's own impls (filesByKey
// is every file of the build, which the passes resolve names in), adds
// typeArgFiles' type-argument templates, in order, to the build's own, and
// lays the result over idx, the program's index fold answered.
func (s *programShare) populate(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis, typeArgFiles []*FileAnalysis) {
	u := s.index
	populateQualifiedReceivers(u, filesByKey, s.primitives)
	PopulateImplIfaceOrigins(u, filesByKey)
	s.covered, s.blocked = recordImplsByIdentity(u, filesByKey, s.primitives)
	addImplTypeArgs(u, typeArgFiles)
	PopulateProjectImplFuncTypes(u, filesByKey)
	PopulateProjectTypeMethodSymbols(u, s.files)
	s.layOver(idx)
	// The base's methods are in idx already, and these contend with them by
	// the pass's own rule.
	populateTypeMethodIdentities(idx, s.files, s.primitives)
	s.layOverEnums(idx)
}

// layOver sets each entry of idx the build's own index adds to: the base's
// entry with the build's laid over it, as one fold over the base's files and
// then the build's gives. Each table it changes is a new one, so the base's
// are never written. It may run again once the build's index has grown.
func (s *programShare) layOver(idx *ProjectImplIndex) {
	base, u := s.base.index, s.index
	unionInner(idx.Impls, base.Impls, u.Impls)
	for iface, byType := range u.ImplManifest {
		merged := maps.Clone(base.ImplManifest[iface])
		if merged == nil {
			merged = make(map[string][]Recording, len(byType))
		}
		for typeName, recs := range byType {
			merged[typeName] = mergeRecordings(base.ImplManifest[iface][typeName], recs)
		}
		idx.ImplManifest[iface] = merged
	}
	appendInner(idx.IfaceMethodImpls, base.IfaceMethodImpls, u.IfaceMethodImpls)
	appendInner(idx.IfaceMethodImplExterns, base.IfaceMethodImplExterns, u.IfaceMethodImplExterns)
	maps.Copy(idx.ImplFiles, u.ImplFiles)
	maps.Copy(idx.ImplExternFiles, u.ImplExternFiles)
	maps.Copy(idx.ImplBlockReceiver, u.ImplBlockReceiver)
	maps.Copy(idx.ImplBlockInterfaceKey, u.ImplBlockInterfaceKey)
	maps.Copy(idx.ImplBlockReceiverExtern, u.ImplBlockReceiverExtern)
	maps.Copy(idx.ImplBlockInterfaceKeyExtern, u.ImplBlockInterfaceKeyExtern)
	unionInner(idx.ImplTypeArgs, base.ImplTypeArgs, u.ImplTypeArgs)
	appendInner(idx.ImplTypeArgSets, base.ImplTypeArgSets, u.ImplTypeArgSets)
	// A stdlib method is folded first and is typed (StdBase.fits), so a
	// program's method of the same type and name never replaces it.
	for typeName, byMethod := range u.TypeMethods {
		methods := maps.Clone(base.TypeMethods[typeName])
		if methods == nil {
			methods = make(map[string]*Symbol, len(byMethod))
		}
		modules := maps.Clone(base.TypeMethodModule[typeName])
		for name, sym := range byMethod {
			if _, present := methods[name]; present {
				continue
			}
			methods[name] = sym
			if module, ok := u.TypeMethodModule[typeName][name]; ok {
				if modules == nil {
					modules = make(map[string]string)
				}
				modules[name] = module
			}
		}
		idx.TypeMethods[typeName] = methods
		if modules != nil {
			idx.TypeMethodModule[typeName] = modules
		}
	}
	maps.Copy(idx.ImplFuncTypes, u.ImplFuncTypes)
	maps.Copy(idx.QualifiedReceiver, u.QualifiedReceiver)
	maps.Copy(idx.ImplIfaceOrigin, u.ImplIfaceOrigin)
	unionInner(idx.IfaceOriginByPair, base.IfaceOriginByPair, u.IfaceOriginByPair)
	maps.Copy(idx.ImplsByIdentity, u.ImplsByIdentity)
	s.layOverCoverage(idx)
}

// layOverCoverage settles, for each pair the build's own impls supply, whether
// the identity index can judge it: some supplier, the stdlib's or the
// build's, resolved an origin and none failed to.
func (s *programShare) layOverCoverage(idx *ProjectImplIndex) {
	b := s.base
	settle := func(recv string, ifaces map[string]bool) {
		judged := maps.Clone(idx.implNamesWithIdentity[recv])
		if judged == nil {
			judged = make(map[string]bool)
		}
		for iface := range ifaces {
			if (b.covered[recv][iface] || s.covered[recv][iface]) && !b.blocked[recv][iface] && !s.blocked[recv][iface] {
				judged[iface] = true
			} else {
				delete(judged, iface)
			}
		}
		if len(judged) == 0 {
			delete(idx.implNamesWithIdentity, recv)
			return
		}
		idx.implNamesWithIdentity[recv] = judged
	}
	for recv, ifaces := range s.covered {
		settle(recv, ifaces)
	}
	for recv, ifaces := range s.blocked {
		settle(recv, ifaces)
	}
}

// layOverEnums sets idx.EnumDecls for each name a build's file declares an
// enum by: every declaration of the name, the base's and the build's, in
// file-key order, each once.
func (s *programShare) layOverEnums(idx *ProjectImplIndex) {
	for name, own := range keyedEnumDecls(s.files) {
		stdDecls := s.base.enumDecls[name]
		var syms []*Symbol
		seen := map[*Symbol]bool{}
		add := func(d keyedSymbol) {
			if !seen[d.sym] {
				seen[d.sym] = true
				syms = append(syms, d.sym)
			}
		}
		i, j := 0, 0
		for i < len(stdDecls) || j < len(own) {
			if j == len(own) || (i < len(stdDecls) && stdDecls[i].key < own[j].key) {
				add(stdDecls[i])
				i++
				continue
			}
			add(own[j])
			j++
		}
		idx.EnumDecls[name] = syms
	}
}

// unionInner sets dst[k], for each k over holds, to a new map holding base[k]'s
// entries and then over[k]'s.
func unionInner[K, K2 comparable, V any](dst, base, over map[K]map[K2]V) {
	for k, inner := range over {
		m := make(map[K2]V, len(base[k])+len(inner))
		maps.Copy(m, base[k])
		maps.Copy(m, inner)
		dst[k] = m
	}
}

// appendInner sets dst[k], for each k over holds, to a new map holding
// base[k]'s lists with over[k]'s appended, each in a list of its own.
func appendInner[K, K2 comparable, V any](dst, base, over map[K]map[K2][]V) {
	for k, inner := range over {
		m := maps.Clone(base[k])
		if m == nil {
			m = make(map[K2][]V, len(inner))
		}
		for k2, vs := range inner {
			m[k2] = append(slices.Clip(base[k][k2]), vs...)
		}
		dst[k] = m
	}
}

// mergeRecordings answers base's recordings and then over's, each once, in
// source order (sortRecordings).
func mergeRecordings(base, over []Recording) []Recording {
	out := make([]Recording, 0, len(base)+len(over))
	for _, rec := range base {
		if !slices.Contains(out, rec) {
			out = append(out, rec)
		}
	}
	for _, rec := range over {
		if !slices.Contains(out, rec) {
			out = append(out, rec)
		}
	}
	sortRecordings(out)
	return out
}

// implCollisions answers detectImplCollisions' diagnostics for the program
// whose index is idx: own is the build's impl-method tables and receivers
// its impls' qualified receivers. Only the (interface, method) slots the
// build's own impls occupy are grouped. A slot the stdlib's impls alone
// occupy holds no collision (StdBase.fits), and every slot's diagnostics are
// placed by its interface, method and receiver, so the rest come out as a
// grouping of every slot gives them.
func (s *programShare) implCollisions(idx *ProjectImplIndex, own *implTables, receivers map[*ast.FuncDef]string) []TypeError {
	recvs := map[*ast.FuncDef]string{}
	keys := map[*ast.FuncDef]string{}
	note := func(fn *ast.FuncDef) {
		if recv, ok := receivers[fn]; ok {
			recvs[fn] = recv
		} else if recv, ok := idx.QualifiedReceiver[fn]; ok {
			recvs[fn] = recv
		}
		if key, ok := own.ImplBlockInterfaceKey[fn]; ok {
			keys[fn] = key
		} else if key, ok := idx.ImplBlockInterfaceKey[fn]; ok {
			keys[fn] = key
		}
	}
	slots := &ProjectImplIndex{
		IfaceMethodImpls: make(map[string]map[string][]*ast.FuncDef, len(s.index.IfaceMethodImpls)),
		ImplFiles:        make(map[*ast.FuncDef]string),
	}
	for iface, byMethod := range s.index.IfaceMethodImpls {
		slots.IfaceMethodImpls[iface] = make(map[string][]*ast.FuncDef, len(byMethod))
		for method, programFns := range byMethod {
			// The slot as the program's index holds it is the stdlib's impls
			// and then the program's. Of the stdlib's, only those in a group
			// a program impl joins can collide, so only those are grouped,
			// in their order in the slot.
			for _, fn := range own.IfaceMethodImpls[iface][method] {
				note(fn)
			}
			for _, fn := range programFns {
				note(fn)
			}
			groups := map[string]bool{}
			for _, fns := range [][]*ast.FuncDef{own.IfaceMethodImpls[iface][method], programFns} {
				for _, fn := range fns {
					groups[collisionGroupKey(iface, fn, recvs, keys)] = true
				}
			}
			var std []slotImpl
			for group := range groups {
				std = append(std, s.base.collisionGroups[iface][method][group]...)
			}
			sort.Slice(std, func(i, j int) bool { return std[i].at < std[j].at })
			fns := make([]*ast.FuncDef, 0, len(std)+len(programFns))
			for _, impl := range std {
				note(impl.fn)
				fns = append(fns, impl.fn)
			}
			fns = append(fns, programFns...)
			slots.IfaceMethodImpls[iface][method] = fns
			for _, fn := range fns {
				slots.ImplFiles[fn] = idx.ImplFiles[fn]
			}
		}
	}
	index, files := CollisionImplIndex(own.IfaceMethodImpls, own.IfaceMethodImplFiles, slots)
	return detectImplCollisions(index, files, recvs, keys, idx.ImplIfaceOrigin)
}

// slotImpl is a stdlib impl function and its place in its (interface,
// method) slot of the stdlib's index.
type slotImpl struct {
	fn *ast.FuncDef
	at int
}

// stdCollisionGroups answers, for each (interface, method) slot of idx, the
// impls in each group detectImplCollisions would put them in.
func stdCollisionGroups(idx *ProjectImplIndex) map[string]map[string]map[string][]slotImpl {
	out := make(map[string]map[string]map[string][]slotImpl, len(idx.IfaceMethodImpls))
	for iface, byMethod := range idx.IfaceMethodImpls {
		out[iface] = make(map[string]map[string][]slotImpl, len(byMethod))
		for method, fns := range byMethod {
			groups := map[string][]slotImpl{}
			for at, fn := range fns {
				key := collisionGroupKey(iface, fn, idx.QualifiedReceiver, idx.ImplBlockInterfaceKey)
				groups[key] = append(groups[key], slotImpl{fn: fn, at: at})
			}
			out[iface][method] = groups
		}
	}
	return out
}

// orphanRuleNames answers the names detectOrphanImpls looks up for index's
// impls: each interface's, and each impl's receiver's.
func orphanRuleNames(index map[string]map[string][]*ast.FuncDef, receivers map[*ast.FuncDef]string) map[string]bool {
	names := map[string]bool{}
	for iface, byMethod := range index {
		names[iface] = true
		for _, fns := range byMethod {
			for _, fn := range fns {
				if recv := receiverBaseName(fn, receivers); recv != "" {
					names[recv] = true
				}
			}
		}
	}
	return names
}

// declaringModules answers declaringModuleIndex's entries for names: the
// stdlib's modules from the base, then the build's own files'.
func (s *programShare) declaringModules(names map[string]bool, entryModuleName string, crossModuleSegments map[string]bool) map[string][]string {
	idx := make(map[string][]string, len(names))
	for name := range names {
		if mods := s.base.declModules[name]; mods != nil {
			idx[name] = slices.Clone(mods)
		}
	}
	seen := map[*Scope]bool{}
	for key, fa := range s.files {
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		writeDeclsFromScope(fa.ModuleScope, moduleNameFromKey(key, entryModuleName, crossModuleSegments), idx, seen)
	}
	return idx
}
