package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Nominal identity on the missing-impl SUPPLY side.
//
// `DetectMissingImpls` asks "is there an impl of Iface for T" by indexing
// `idx.Impls[typeName][iface]`, and `Impls` is keyed by BARE type name. So a
// type borrows an interface from a same-named type in another module, the
// demand is judged satisfied, and a program with no impl at all is accepted.
//
// # The defect, measured rather than assumed
//
// `std/calendar.nomi` and `std/random.nomi` each declare their own
// `pub enum Error` and carry `derive Equatable for Error`. Keyed by bare name,
// `Impls["Error"]["Equatable"]` is true for ANY type called `Error`, so
// `Equatable.equal` over a user's own `Error` with no impl type-checks.
//
// Two identical programs, differing only in the type's name:
//
//	pub enum Zonk  { Bad String }   -> "no impl of `Equatable` for `Zonk`"
//	pub enum Error { Bad String }   -> accepted, then faults at run time
//
// Accepted, it has no meaning to run: a dispatch keyed by bare name would run
// a stdlib module's synthesized body against a value of another type, and a
// structural comparison would ignore that the program demanded an impl.
// Neither is the program's meaning.
//
// # Why a PARALLEL index, and not a re-key
//
// The same reason as `TypeMethodByIdentity` and `QualifiedReceiver`: `Impls`,
// `ImplTypeArgs`, `TypeMethods` and `ImplManifest` are queried by BASE NAME
// from all over the checker and unifier, which frequently hold nothing else.
// Identity goes where it is needed, keyed the same way the table it qualifies
// is keyed, and nothing else changes shape. This is the sixth such index.
//
// # What it keys
//
// Every interface impl, a FuncDef in `IfaceMethodImpls`, a `host fn` in
// `IfaceMethodImplExterns`, or a derive's block that writes no function in
// `ImplBlocks` (a derived Equatable, Hashable, Debug or Display), under its receiver's declaring origin and base
// name, and its interface's declaring origin and name as those maps,
// `Impls` and `ImplManifest` spell it: the declaration's bare name
// (implInterfaceName, so an import alias in the header names the interface
// it imports), with no type arguments. So `impl Add<Days, Date> for Date` is (std/calendar, Date,
// std/add, Add), the key a `+` on a Date asks by, and `impl a.Show for
// User` supplies no demand of b's Show. Each impl is keyed with an
// unresolved interface origin too, the bottom a demand that knows only
// the interface's name asks by. Which arguments an impl takes is the
// bare-name match's question (`ImplTypeArgs`), asked before this one;
// this one asks only whose impl it is. Over the stdlib every pair `Impls`
// claims is covered (TestImplIdentity_EveryStdConformanceIsJudgedByIdentity).
//
// # Where it is read
//
// The demand side reads it after the check (`unsuppliedRecordings`), and the
// conformance questions the checker asks while checking read it too
// (`suppliesReceiver`, `suppliesOrigin`): the unifier's
// `interfaceImplemented`, through `ImplTables.Receivers`, `hasOwnImpl`, and
// `interfacesImplementedBy`, which lists the interfaces a type implements
// and under which `interfacesDeclaringMethod` lists those whose functions
// `Type.f` reaches. So `d: Display = Name("a")` for a program's `type Name
// String` is a type mismatch even with std/xml, whose own `Name` has `impl
// Display`, loaded, and a program's `enum Weekday` has no `try_from` when
// another file writes `impl TryFrom<Int> for Weekday` for its own. A typed
// literal's handler, which must be one function and not a yes or no, is
// chosen among the impls by the same identity (`literalTagType`,
// `ownReceiverImpls`), and so are the checks that a tag has one handler
// (`checkOneLiteralHandler`, `fromFragmentsRival`).
//
// # Why an unknown origin ACCEPTS
//
// `OriginUnresolved` is the bottom of the identity lattice. A demand whose
// receiver is a primitive, a type parameter, or a type this build could not
// resolve carries no origin, and a build that never ran
// `PopulateImplsByIdentity` has an empty index. Both answer "supplied", so
// every path that does not populate this index behaves exactly as it did
// before the index existed. The check can only ever REJECT when the demand's
// origin is known, the index covers the name, and no supplier matches.
//
// A receiver declared in a file the index did not fold (a file checked on
// its own against an index over the stdlib, a module a body's import loaded
// after the build; unindexedFiles) keeps the bare-name answer where one of
// those files writes an impl of the interface's name for a type of its
// name: the index holds none of their impls, so it cannot say which
// declaration that impl is for. Where none of them writes one, the impls
// of the name are all in the index, which judges the receiver as any
// other: a file's own `struct Html` checked on its own does not take std's
// `impl ToHtml for Html`.
//
// A struct, enum or distinct type declared inside a body has a known
// origin, its declaration's (BodyOrigin), and no file of any build is keyed
// by it. No impl can name such a type, so the index never holds an entry
// under that origin, and a demand on it that matched a std or module-level
// type of its name by bare name is judged unsupplied, for every interface,
// a pair the index leaves uncovered included (implSuppliedByIdentity). An
// interface declared inside a body is judged the same way: no impl header
// can name it, so no type supplies it.
//
// A pair is left uncovered, and keeps the bare-name answer, when some
// supplier of it has a receiver or an interface whose origin did not
// resolve, or when
// `FinalizeCoherence` unions in an impl the index never walked
// (`UncoverImplIdentity`).
//
// A primitive receiver is keyed by the file that declares it, wherever the
// impl is written: `declaringOriginOfType` reads an imported PrimitiveType's
// origin from `primitiveOriginIndex`, so `impl ToJson for Int` in std/json
// registers under std/int. `nominalOrigin` reports no origin for a built-in
// primitive, so a demand on one lands at the bottom and is accepted. A
// non-generic host type is not a built-in: it carries its own Origin, and a
// demand on it is judged like a demand on a struct.

// ImplIdentityKey is `Impls`' key with the receiver's and the interface's
// declaring modules attached: the supply side's nominal identity rather
// than its base names.
type ImplIdentityKey struct {
	// Origin is the build key of the file that declared the receiver type
	// ("std/calendar", "std/random", OriginEntry for the project entry).
	Origin string
	// Type is the receiver's base name, spelled as `Impls` spells it.
	Type string
	// IfaceOrigin is the build key of the file that declared the
	// interface: ImplIfaceOrigin for a `fn`, the declaration the impl's
	// home file binds the name to for a `host fn`. Every impl is keyed
	// with OriginUnresolved here too, the bottom, which a demand that
	// knows the interface only by its name asks by.
	IfaceOrigin string
	// Iface is the interface base name, spelled as `Impls` spells it.
	Iface string
}

// PopulateImplsByIdentity fills idx.ImplsByIdentity and
// idx.implNamesWithIdentity.
//
// Must run AFTER BuildTypes (Sweep C-types), for `PopulateQualifiedReceivers`'
// reason and by the same mechanism: the origin is read off the receiver type's
// `Origin`, and a type symbol carries no `Type` pointer — hence no `Origin` —
// until then. Run earlier it yields an empty index, which the file comment's
// lattice rule then reads as "everything supplied".
//
// The origin is resolved through the impl's HOME file, not through whichever
// FileAnalysis happens to be iterated: the per-file impl maps are broadcast,
// so every FA holds every file's impls and only `ImplFiles` says where an impl
// was written. That is `PopulateQualifiedReceivers`' second recorded trap.
func PopulateImplsByIdentity(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	covered, blocked := recordImplsByIdentity(idx, filesByKey, filesByKey, primitiveOriginIndex(filesByKey))
	idx.implNamesWithIdentity = coveredNotBlocked(covered, blocked)
}

// recordImplsByIdentity fills idx.ImplsByIdentity from idx's impls and
// answers the (T, Iface) pairs some supplier of which resolved an origin
// (covered) and those some supplier of which did not (blocked).
// primitives is primitiveOriginIndex over filesByKey, and own the files
// idx folds, by key.
func recordImplsByIdentity(idx *ProjectImplIndex, filesByKey, own map[string]*FileAnalysis, primitives map[string]string) (covered, blocked map[string]map[string]bool) {
	if idx.ImplsByIdentity == nil {
		idx.ImplsByIdentity = make(map[ImplIdentityKey]bool)
	}
	// One origin lookup per (home module, receiver head) rather than per
	// impl: a module's impls share a home file, and declaringOriginOfType
	// walks a scope and an import chain on every call.
	type memoKey struct{ module, qualifier, head string }
	memo := make(map[memoKey]string)
	originOf := func(module, qualifier, recv string) string {
		noteStdlibWork(module)
		k := memoKey{module: module, qualifier: qualifier, head: typeNameHead(recv)}
		origin, seen := memo[k]
		if !seen {
			origin = implReceiverOrigin(filesByKey[k.module], k.qualifier, k.head, primitives)
			memo[k] = origin
		}
		return origin
	}

	// A pair is COVERED once some supplier of it resolved an origin, and
	// BLOCKED for good if any supplier of it did not. Blocked wins: an
	// unresolved supplier may be the very declaration a demand names, so
	// answering "that origin has no entry" would reject a conformance that
	// exists. An impl with no home file (filesByKey[""] is nil) is one.
	covered = make(map[string]map[string]bool)
	blocked = make(map[string]map[string]bool)
	mark := func(into map[string]map[string]bool, recv, iface string) {
		if into[recv] == nil {
			into[recv] = make(map[string]bool)
		}
		into[recv][iface] = true
	}
	record := func(module, qualifier, recv, ifaceOrigin, iface string) {
		if recv == "" || iface == "" {
			return
		}
		origin := originOf(module, qualifier, recv)
		if origin == OriginUnresolved || ifaceOrigin == OriginUnresolved {
			mark(blocked, recv, iface)
			return
		}
		idx.ImplsByIdentity[ImplIdentityKey{Origin: origin, Type: recv, IfaceOrigin: ifaceOrigin, Iface: iface}] = true
		idx.ImplsByIdentity[ImplIdentityKey{Origin: origin, Type: recv, IfaceOrigin: OriginUnresolved, Iface: iface}] = true
		mark(covered, recv, iface)
	}

	// The interface is spelled as the file comment's "What it keys" says:
	// IfaceMethodImpls' key, the bare name a lookup asks by, with the
	// origin of the declaration the impl's header names.
	for iface, byMethod := range idx.IfaceMethodImpls {
		for _, fns := range byMethod {
			for _, fn := range fns {
				if fn != nil {
					home := idx.ImplFiles[fn]
					record(home, implReceiverModule(filesByKey[home], fn), idx.ReceiverOf(fn), idx.ImplIfaceOrigin[fn], iface)
				}
			}
		}
	}
	// Extern impls (`impl Iface for T { host fn ... }`) have no FuncDef and
	// travel on the parallel extern maps, which the FuncDef maps deliberately
	// stay free of. They are real conformances, so a pass that skipped them
	// would block every pair std supplies that way — `String`'s `impl Iter`
	// among them — and quietly shrink the check to nothing.
	for iface, byMethod := range idx.IfaceMethodImplExterns {
		for _, exs := range byMethod {
			for _, ex := range exs {
				if ex != nil {
					home := idx.ImplExternFiles[ex]
					record(home, "", idx.ImplBlockReceiverExtern[ex], externImplIfaceOrigin(filesByKey[home], ex, iface), iface)
				}
			}
		}
	}
	// A derive's block that writes no function (a derived Equatable,
	// Hashable, Debug or Display, derivesByShape) is on neither map, and supplies its
	// interface all the same: each such block of the files idx folds
	// (own), which their types record (FileAnalysis.ImplBlocks).
	for home, fa := range own {
		if fa == nil {
			continue
		}
		for recv, byIface := range fa.ImplBlocks {
			for iface, blocks := range byIface {
				for _, b := range blocks {
					if b == nil || b.Block == nil || !derivesByShape(b.Block) {
						continue
					}
					module := ""
					if q, qualified := b.Block.Receiver.(*ast.QualifiedType); qualified && !ast.IsPublic(q.Module) {
						module = q.Module
					}
					record(home, module, recv, b.Origin, iface)
				}
			}
		}
	}

	return covered, blocked
}

// externImplIfaceOrigin is the origin of the interface named iface that
// ex, an item of an impl block of `host fn`s in home, implements: the
// declaration home's scope binds the header's name to, as
// declaringOriginOfImplInterface finds a `fn`'s unqualified header (the
// extern maps keep no header qualifier).
func externImplIfaceOrigin(home *FileAnalysis, ex *ast.ExternFunc, iface string) string {
	if ex.ImplIface != nil {
		iface = TypeExprBaseName(ex.ImplIface)
	}
	return declaringOriginOfInterface(home, iface)
}

// coveredNotBlocked answers the pairs of covered that blocked does not hold:
// the pairs the identity index can judge.
func coveredNotBlocked(covered, blocked map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(covered))
	for recv, ifaces := range covered {
		for iface := range ifaces {
			if blocked[recv][iface] {
				continue
			}
			if out[recv] == nil {
				out[recv] = make(map[string]bool)
			}
			out[recv][iface] = true
		}
	}
	return out
}

// UncoverImplIdentity drops a (T, Iface) pair from the identity index's
// covered set, so demands for it fall back to the bare-name answer.
//
// For supply that reaches `DetectMissingImpls` from somewhere
// `PopulateImplsByIdentity` never walked. `FinalizeCoherence` unions the
// entry FA's own `Impls` — the late struct-level registration pass and any
// post-snapshot cross-file merge — into the table it judges against, and
// those conformances have no identity entry. Left covered, an entry type's
// perfectly good `impl Equatable for Error` would read as "some other
// module's Error supplies this" and the program would be rejected.
func (idx *ProjectImplIndex) UncoverImplIdentity(typeName, iface string) {
	if idx == nil || idx.implNamesWithIdentity == nil {
		return
	}
	delete(idx.implNamesWithIdentity[typeName], iface)
}

// ImplIdentityCovers reports whether the identity index can judge a
// (T, Iface) pair at all. Exported for the guard that holds every std pair
// covered: a pair that falls back is a pair the check cannot reject.
func (idx *ProjectImplIndex) ImplIdentityCovers(typeName, iface string) bool {
	return idx != nil && idx.implNamesWithIdentity[typeName][iface]
}

// implSuppliedByIdentity answers DetectMissingImpls' supply question for a
// demand that already matched by bare name, and reports whether it could
// answer at all. The demand is for typeName of origin, and of the
// interface iface declared at ifaceOrigin.
//
// `known` is false — meaning "keep the bare-name answer" — whenever anything
// in the chain is missing: an index that was never populated, a demand with no
// receiver origin, or a (name, iface) pair this index does not cover. Only when
// the pair IS covered and the demand's own identity has no entry does this say
// the bare-name match was a borrow. A demand whose interface origin is
// unresolved asks by the bottom key, so any interface of the name the
// receiver implements supplies it.
//
// A body origin (BodyOrigin) is judged whatever the pair, on either side: no
// impl can name a type or an interface declared inside a body, so every
// bare-name match on one is a borrow, a pair some supplier of which left
// uncovered included.
func (idx *ProjectImplIndex) implSuppliedByIdentity(origin, typeName, ifaceOrigin, iface string) (supplied, known bool) {
	if idx == nil || len(idx.ImplsByIdentity) == 0 {
		return false, false
	}
	if IsBodyOrigin(origin) || IsBodyOrigin(ifaceOrigin) {
		return false, true
	}
	if origin == OriginUnresolved {
		return false, false
	}
	if !idx.implNamesWithIdentity[typeName][iface] {
		return false, false
	}
	return idx.ImplsByIdentity[ImplIdentityKey{Origin: origin, Type: typeName, IfaceOrigin: ifaceOrigin, Iface: iface}], true
}

// implementsInterfaceNamed reports whether the receiver of origin and
// typeName has an impl, by its own declaration, of some interface named
// iface: the bottom key. A demand of another interface of the name that
// it does not supply is then the interface's mismatch, not the type's.
func (idx *ProjectImplIndex) implementsInterfaceNamed(origin, typeName, iface string) bool {
	return idx != nil && origin != OriginUnresolved &&
		idx.ImplsByIdentity[ImplIdentityKey{Origin: origin, Type: typeName, IfaceOrigin: OriginUnresolved, Iface: iface}]
}

// unindexedFiles is what a program's impl index does not fold: the origins
// of the files it holds none of the impls of, and those files' impls by
// bare name (Program.unindexedOrigins, Program.unindexedImpls).
type unindexedFiles struct {
	origins map[string]bool
	impls   map[string]map[string]bool
}

// unindexed is the program's unindexedFiles.
func (p *Program) unindexed() unindexedFiles {
	if p == nil {
		return unindexedFiles{}
	}
	return unindexedFiles{origins: p.unindexedOrigins, impls: p.unindexedImpls}
}

// suppliesReceiver reports whether t, whose base name is name and which
// some type of that name implements iface, implements the interface
// ifaceOrigin declares by its own declaration. Where the index cannot judge
// the pair, or t has no origin, the bare-name answer stands. So it stands
// for a t declared in a file the index did not fold where one of those
// files writes an impl of iface for a type named name (unindexed): the
// index holds none of their impls, so finding no entry for t's origin says
// nothing. A file checked on its own against an index over the stdlib is
// such a file, and its `impl Equatable for Level` is not std/log's.
func (idx *ProjectImplIndex) suppliesReceiver(t Type, name, ifaceOrigin, iface string, unindexed unindexedFiles) bool {
	origin, ok := nominalOrigin(t)
	if !ok {
		origin = OriginUnresolved
	}
	return idx.suppliesOrigin(origin, name, ifaceOrigin, iface, unindexed)
}

// suppliesOrigin is suppliesReceiver for a receiver known by its base name
// and its declaring origin, as `Type.method` knows it.
func (idx *ProjectImplIndex) suppliesOrigin(origin, name, ifaceOrigin, iface string, unindexed unindexedFiles) bool {
	if unindexed.origins[origin] && unindexed.impls[name][iface] {
		return true
	}
	if supplied, known := idx.implSuppliedByIdentity(origin, name, ifaceOrigin, iface); known {
		return supplied
	}
	return true
}

// unsuppliedRecordings keeps only the demands whose OWN declaration has no
// impl of `iface`, out of a bucket that already matched by bare name.
//
// Filtering per RECORDING rather than judging the bucket is not a refinement,
// it is the whole mechanism. `ImplManifest[iface][typeName]` is keyed by bare
// name too, so one bucket holds demands for every same-named type: the
// `Equatable`/`Error` bucket holds the demands the stdlib raised about its own
// `Error` types and the ones the user raised about theirs. Asking whether "the
// bucket's origin" is supplied has no answer — the origins disagree — and any
// whole-bucket rule either accepts the borrow or rejects the stdlib's own
// correct demands.
//
// Returns nil — meaning "keep the bare-name answer, the pair is supplied" —
// for every recording the index cannot judge: no origin on the demand, or a
// (name, iface) pair the index does not cover. So a build that never
// populated the index, a primitive receiver, and an origin-less generic
// `host type` all behave exactly as they did before the index existed.
func (idx *ProjectImplIndex) unsuppliedRecordings(recs []Recording, typeName, iface string) []Recording {
	var out []Recording
	for _, rec := range recs {
		supplied, known := idx.implSuppliedByIdentity(rec.TypeOrigin, typeName, rec.IfaceOrigin, iface)
		if known && !supplied {
			out = append(out, rec)
		}
	}
	return out
}
