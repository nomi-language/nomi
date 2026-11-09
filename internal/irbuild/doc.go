// Package irbuild builds the IR the VM runs. It takes a checked Nomi program
// ([Analyze], [AnalyzeSource], [AnalyzeVirtual]: the same front end `nomi
// run` runs) and lowers every body the program runs into an [ir.Func]
// ([GenerateIR]): module and impl functions, `once` initializers, test
// bodies, lambdas and stdlib bodies. `nomi run`, `nomi test`, `nomi check`,
// the REPL and the tour execute the result on internal/vm through vmhost.
//
// Generic functions, types and impls are instantiated per type-argument
// tuple a program reaches; generic stdlib bodies per program, never in the
// stdlib lowered once for the process. A program that reaches a body the
// builder declines fails before its first effect.
//
// # The builder's representation
//
// The builder's own type is `kind` (native.go): a tag plus pointers to the
// declaration a named type, an existential's interface or a type parameter
// is, spelled in Nomi by `kind.nomi` for a refusal's text. A value is an
// `expr`: its IR temporary and its kind. Do not add Go text or Go names to
// the builder. Every IR temporary has a stored value type, and a retained
// graph must pass [ir.Lint] ([ir.LintModule] for a module). The stdlib
// lowered once for the process skips lint outside `go test`
// (`lintEmbeddedStdlib`), since it is the same in every process of a
// binary; TestStdlibLowersTheSameWithoutLint requires it, and a std bundle
// of it decoded, to pass lint and retain what the linted one retains.
//
// # Unit packages
//
// Each module's gen carries a unit package name (`nomimod<i>`,
// `unitPackage`), shared by every file of one strongly connected component
// of the Nomi import graph (component.go, `componentOwners`). The name is a
// namespace key, not a compiled artifact: a gen compares it to tell a
// declaration of its own unit from another file's (literal.go,
// irstdmethodcall.go). Nomi permits cyclic file imports, and the analyzer
// resolves names whole-program, so import order never matters.
//
// The partition reads the import graph rather than the reference graph
// (`fileIndex.reaches`), because building the file index needs each unit's
// name first. It is safe in the direction that matters: every reference
// edge is an import edge, since a qualified `owner.member`, a bare name
// from a selective import and a type reference all need the name in scope,
// and a user file's name reaches another file's scope only through an
// `import`. TestReferenceEdgesAreImportEdges holds that containment over the
// corpus. `pub` is enforced by the front end, so two files sharing a unit
// change no visibility decision.
//
// # Tuples
//
// Items are read by ordinal name (`pair.first`). The ordinals live once in
// rt ([rt.TupleOrdinal], rt/ordinal.go), read by the debugger's variable
// tree and forwarded by internal/ast. A struct may declare a field `first`,
// so the checker records each access it typed as a tuple's or named tuple's
// item in [analysis.FileAnalysis].TupleItems with the index, and the builder
// reads that slot, unwrapping a named tuple's distinct first (`tupleItem`,
// irtuple.go). An accessor (`.second`) has no record and decides by the kind
// it reads from. The parser reports `t.0` as an error suggesting `t.first`
// and continues with the ordinal, so no later layer sees a numeric field.
//
// # The stdlib
//
// The standard library is lowered once per process (`stdlibLowering`), and
// a program's IR links every cached stdlib module (Result.IRModules). Its
// lowering is decoded from a std bundle when the binary embeds or caches
// one (below). The builder asks std for a module's checked bodies
// (`stdBodies`, std.StdLib.Bodies) before it reads one, since the stdlib is
// built from its interfaces and checks a module's bodies only when
// something reads them (std/shared.go's file comment).
//
// A std module in `programStdModules` is the exception. It is not lowered
// with the shared stdlib: it is declared with each program that imports
// it, as one more module (programstd.go), so its types and interfaces are
// the program's (one descriptor each, dispatched by the VM's ordinary
// path), while its positions are stdlib positions, so a trap inside it
// reports at the program's call. TestProgramStdModulesQualify holds each
// listed module to the rule: no `host fn` or `host type`, no shared std
// module imports it (the prelude, which only re-exports, may), and it
// checks and lowers with no decline. The prelude's import of std/phrase's
// `Phrase` and `Noun` reaches the module only from a file that names one of
// them anywhere, a parameter's type included (`importedStdModules`,
// `preludeListedNames`), or through a `phrase` or `noun` expansion's hidden
// import. Otherwise every program would lower std/phrase and std/money,
// whose checked `Currency` literals keep a program from a kept lowering
// (Result.Keep).
//
// The listed modules' bodies are lowered once per process, all of them as
// one program, and linked into each program by name (programstdlink.go's
// file comment has the rules). A program takes the part its reached listed
// modules make (`restrict`): their modules, the std instances whose type
// arguments are declared only in them (`stdInstances.needs`), and their
// literal sites and refusals. It still builds its type registry, file index
// and impl index over those modules, but their gens are `linked`:
// `emitModule` skips their walk, a synthesized impl block takes the kept
// verdict, and every table resolves a linkage name the kept lowering
// defines to the kept symbol ([ir.Table.SetLinkage]). A linked gen still
// lowers what the kept lowering lacks (a generic instance at the program's
// types), linked beside it; a body both define is kept once
// (`dropLinkedDuplicates`). With the listed modules kept, the HTTP chapter's
// first tour block lowers in about 17 ms instead of about 105 ms; the rest
// is `closeReferences`, the type and signature waves and the tail graphs.
// kindmemo.go memoizes the retention predicates, `stdHostOriginKind` is an
// index, the lint describes an instruction only for a violation, and a
// generic std instance's module binding clones the earlier modules' index.
//
// Not linked: a program whose entry is a listed module, a shared module's
// cases that import listed modules, a reference editor's edited module with
// its twins (below, "Linkage names"), and a program that reaches a
// test-only fixture module.
//
// # Defaulted parameters across files
//
// A call into another file that omits a defaulted parameter fills it at the
// call when every name the default mentions means the same there, and
// otherwise calls an accessor the declaring file builds, one per defaulted
// parameter, taking the parameters before it (siblingdefault.go). A file
// builds them for each `pub fn`, and for a private `fn` when its partner
// test is in the program. A monomorphic impl's `pub` members get theirs as
// the impl is emitted, a generic member with its own type parameters per
// instance (`methodInstAccessors`), and an impl function of a generic
// type's instance (`Job.new` from a file that never imports the `Duration`
// its `timeout` default names) on request, built under the instance's frame
// in the late-instance flush (instimplaccessor.go). A concrete operand of an
// interface-typed parameter of a type-qualified call enters by erasure
// before its slot is matched (`qualEraseSlotted`).
//
// # Regex
//
// A `Regex<C>`'s capture type C is phantom: every `Regex<X>` lowers as one
// host handle, a std function whose type parameters appear only inside
// `Regex<...>` is not generic to it, and `String.captures` builds the C
// value from the groups by C's shape through a VM intrinsic.
//
// # Declines
//
// A declined body is not retained, and a program that reaches one fails
// before its first effect. `nomi run` prints `nomi check`'s diagnostic at
// the expression the builder stopped at, worded for the author ("this call
// to `f` is not supported yet, so `fn g` cannot run"); `nomi test` reports
// the case BLOCKED with it. NOMI_DEBUG_LOWERING=1 adds the builder's own
// reason as a hint. The places the builder can decline are listed in
// testdata/lowering-declines.txt (internal/declinesites).
//
// The shared stdlib lowering records no decline of a std body: the function
// is marked unlowered and a program's call declines at the call. In a
// stdlib module's own cases (`nomi test` and `nomi check` on
// `std/<module>.nomi`), vmhost follows such a call into std (`stdBlocker`):
// [StdlibDeclines] lowers the callee's module afresh with a census of each
// body's decline, and the report follows calls to unlowered std functions
// to the innermost construct. A program-std module's declines reach the
// program's hooks; a diagnostic in a stdlib file is worded from its
// module's nodes (`stdModuleAt`). Before a module's embedded cases are
// built, [frontend.StdlibErrors] reports every check error the shared
// analysis recorded, so a case fails with the error rather than blocking.
//
// A test about how a blocked program is reported blocks on a planted
// decline, a call to `planted_decline` that [PlantDeclineForTest] makes the
// builder decline, never on a real gap.
//
// A dispatched call reaches every retained implementation of its method.
// One with none blocks the program only when some module implements the
// method without a dispatch entry ([ir.Module.ImplementUnretained]: the
// body was declined, or the type's values do not dispatch). A method
// nothing implements is not reported: no value of an implementing type
// exists (`Handler.kind` over an empty `List<Handler>`), and a reached call
// would stop with the VM's dispatch miss.
//
// # The kind memo
//
// `irRetainedStructKind` and `irRetainedEnumKind` keep each typeDef's last
// result, stamped with a process-wide epoch (kindmemo.go). A function that
// writes an existing typeDef's fields, variants, slots, layout or
// lowerability opens a `changingTypes` bracket: the memo is off while one
// is open, and opening or closing one invalidates every result. One that
// only builds a new typeDef opens a `constructingType` bracket, which turns
// the memo off without invalidating. Under `go test` every memoized result
// is recomputed and compared (`kindMemoVerify`), so a write outside a
// bracket fails the first test that reads the stale result; the
// TestKindMemo_ tests count one lowering's walks and plant such a write.
//
// # The std bundle
//
// The shared stdlib lowering and the kept lowering of the listed modules
// are the same in every process of one binary, so a process decodes them
// from a std bundle (stdbundle.go, stdbundlelisted.go; stdbundle.go's file
// comment describes the sections and the checks). The shared section is the
// shared modules' IR as one ir image plus, per module, each `stdFunc`'s and
// `stdOnce`'s outcome (lowered or not, refusal, arity and default wrappers,
// IR body) and the unlowered table, recorded once every module is filed,
// so each outcome is the whole stdlib's (filing another module's
// declaration of one spelling may refuse it, `addOverload`).
//
// Reading the bundle and decoding its shared section's IR and outcomes
// needs no stdlib, so a caller about to check and lower a program starts
// it on another goroutine (PrefetchStdBundle, which vmhost's lowering
// calls) while the front end builds the stdlib's analysis, and the first
// lowering takes the result (`takeStdBundleRead`). Restoring reads the
// stdlib and runs on the lowering's goroutine.
//
// A process decodes the bundle once and restores a module when the shared
// stdlib has built it (`stdlibIndex.withModules`, run by `stdlibLowering`
// when std.Shared has grown): it builds the module's view as
// `buildStdlibIndex` does from std's declarations (built from interfaces,
// no body checked), restores its outcome (`restoreStdModule`), and files
// every restored module again in the bundle's order into a new index, so a
// gen holding the old one reads it unchanged. Filing part of the stdlib
// marks no declaration the whole would not. The listed section names the
// shared modules' symbols by linkage name and is decoded against the
// process's shared lowering with an [ir.Linker] when a program first
// reaches a listed module; two std instance modules defining one instance
// keep one body.
//
// A binary built with the `nomi_stdbundle` tag embeds a bundle that
// internal/irbuild/stdbundlegen lowers from the same tree just before the
// build and supplies with `go build -overlay`, with the stdlib's interfaces
// and facts (std/shared.go) and the base modules' state (std/state.go)
// beside it. No bundle file is in the source
// tree, and the tag without the overlay does not compile.
// scripts/build-tour-wasm.sh and scripts/release.sh build that way. Any
// other binary uses the artifact cache (internal/artifactcache:
// os.UserCacheDir()/nomi/artifacts/std/, or $NOMI_ARTIFACT_CACHE) under a
// name from its Go build ID. Writes go through a temporary file renamed
// into place and prune bundles unused for a day (`stdBundleUnused`). Under
// `go test`, and in a child a test starts, the cache is under the system
// temp directory.
//
// A module that does not restore is lowered (in a test it panics).
// `decodeStdBundle`, which tests use, restores every module and checks the
// order too. A failing cached bundle is ignored and rewritten, a failing
// listed section costs only itself, and a failing embedded bundle is
// reported on stderr. NOMI_DEBUG_STD_BUNDLE=1 reports why a bundle was not
// used. Decoding is roughly a tenth of lowering natively (about 5 ms
// against 40-45 ms shared, 22-27 ms against 240 ms listed) and under
// js/wasm; a binary's first run with an empty cache pays the lowering.
//
// # Linkage names
//
// A function's identity in memory is its [ir.Symbol], interned per gen in
// an [ir.Table]. A symbol another module can name also carries a linkage
// name (link.go) built from the analyzer's declaration names
// ([analysis.DeclarationNames]: `parse`, `Invoice.total`, `impl Display for
// Invoice.to_string`, `once rates`, `parse/step` for a function inside a
// body) qualified by the declaring module: `std/http.get`,
// `billing/invoice.impl Display for Invoice[...].to_string`.
//
// A generic instance adds its type arguments, spelled by `portableArgsKey`,
// which names every declaration by its linkage path (a program's from the
// names its lowering holds while it runs, `holdDeclKeys`) and a
// process-wide std interface (`stdIfaceDefs`) by its spec's module and
// name, so a bundle's reader and writer name `impl CheckedLiteral<Display,
// Url> for Url`'s functions alike. An impl function adds its block's
// receiver and substitution, a default accessor its slot; an interface
// method a program interns for a std interface (`stdIfaceMethodToken`) is
// the program's.
//
// The name depends on the declaration, not the lowering, so two lowerings
// of one module name its declarations alike, which lets a program import
// the kept lowering's symbols. A symbol only its module names (a local, a
// field's projection, a type's symbol, whose run-time identity is its
// spelling) has none. A def's run-time spelling is qualified by its
// declaring file, so a std instance's gen spells fs's `Entry` as
// `fs.Entry`.
//
// The image codec writes each symbol's linkage name, so a module encoded
// alone ([ir.EncodeObject]) is an object that an [ir.Linker] links by name
// (internal/ir package doc, "Objects"). NOMI_LINK_OBJECTS=1, or
// [LinkThroughObjects] in a test, makes every lowering encode each module
// (the program's, kept and shared std modules, std instances) as an object
// and link them again before the VM sees them (objects.go).
//
// A listed module's reference editor checks the module from edited source,
// and the listed modules its editor imports are lowered beside it from the
// shared analysis (std/storage_testing beside an edited std/storage). Their
// references to the shared copy resolve to the edited module's declaration
// of the same kind and name (`implFuncTwins`): types in the type registry,
// module scope and file-level functions in the file index
// (`indexEditedStdTwins`), so `storage.check_key` in storage_testing calls
// the edited function.
//
// # Lowering one file
//
// A whole program's lowering can be kept (Result.Keep), and [LowerFile]
// lowers one file F against F's check against its imports' interfaces and
// the kept objects of every other module, into the program a whole
// lowering makes. perfile.go's file comment describes what is kept, how F
// is lowered (the other files are `fromObjects`), what is carried from F's
// kept object (`carryKept`) and when the answer is a [FileMiss], on which
// the caller lowers the whole program. F is not the entry unless it is, so
// its gen builds no boot, main failure or test table, and whether the
// program starts an app is the kept result. [Rekeep] replaces F's object
// and fingerprints in the kept lowering for the next file's lowering.
//
// [LowerFileAmong] lowers one of several files a relink lowers again after
// one edit (vmhost package doc, "Kept programs"). A file whose
// positions moved is not a miss for the object of another file lowered
// again too. What the file's old object held from a declaration that
// changed or went (a default accessor of a parameter it no longer has, an
// impl it no longer declares, an instance of a generic function whose body
// changed) is dropped rather than a miss, unless a kept object of a file
// not lowered again, of one already lowered again, or a kept instance
// names it. So is a function the file would carry that names what no
// module defines (`carryLinked`): for example the forwarding function of a
// generic interface default that one file asked another's block for, which
// calls an instance the declaring file's lowering dropped when the
// default's body changed; the asking file, lowered after, makes both
// again. A name kept objects of files not lowered again name is dropped
// too when the caller says a file it lowers later asks for it again
// (`asked`); otherwise the miss's [FileMiss].Dropped lists each such name
// and the files that name it, so the caller can lower one of them again
// and check with [Result.Undefined] that the name came back. A file the
// check read with its bodies is lowered as a linked library is, so the
// file can ask it for an instance no kept object holds,
// and [Rekeep] adds each function that module makes and the file's object
// lacks to that object (`mergeLinked`), so a later edit carries or drops it
// by its declaration. The module of a file with no object is kept beside
// the std instances.
//
// TestLowerFileMatchesTheWholeLowering lowers every file of three generated
// projects this way and requires each module to equal the whole lowering's
// part by part ([ir.Parts]: each function and cell encoded on its own, each
// entry by the names it holds, so order and a type symbol made twice do
// not count), the linked program to define every name the whole one does,
// and no file to miss. TestRelinkAfterABodyEditInAnInterfaceDefaultsFile
// (cmd/nomi) edits the bodies of an interface's defaults and helpers one
// after another and requires each edit to relink. internal/expectation's
// TestFilesLowerOnTheirOwnAsInTheProgram holds every recorded program to
// it.
//
// [LowerSessionFile] (session.go) is the REPL's form: a session's newest
// file lowered against the modules the earlier files lowered, kept in
// memory, with a miss taken as it comes (vmhost/session_files.go).
//
// # Instantiation and dispatch
//
// A generic function, type or impl is specialized to each set of concrete
// type arguments a program uses it at. Inside a body, `T` is a known type,
// and `T.format(x)` calls that type's implementation directly. A bounded
// generic function's declaration lowers no body of its own (`funcDecl`):
// every call, function value and other file's call names an instance. A
// call's type arguments are solved against the argument kinds
// (`unifyTypeParams`), and an argument that is a generic type's instance
// gives its own type arguments; a mirror of another file's instance keeps
// none, so they are read off the declaring file's def and imported
// (`instanceArgs`), and `fn show<T>(p: Pair<T>)` solves `T` from another
// file's `Pair<Int>`.
//
// Interface calls on existentials dispatch at run time. An interface-typed
// parameter, field or collection element erases its concrete type (spec
// §4). Values carry their type identity, and the call selects the
// implementation by the receiver's identity. What a program may do with a
// value of interface type is spec §12 *Interface values*.
//
// # Std interfaces held as values
//
// A std interface held as a value is lowered as a program's own is
// (stdprogiface.go). Each file that names one builds a def from std's
// declaration, and a generic one is instantiated per type arguments as a
// program template is. The signatures are std's source: while they
// resolve, `srcFA` returns std's file, so a position-keyed lookup
// (`Maybe<String>` in `Matcher.find_in`) reads what the checker recorded
// there, and a type expression naming no substituted type parameter
// resolves in the declaring module's scope (`stdTypeKind`), so `ToJson`'s
// `Json` needs no import and stays std's in a file that declares its own
// `Json`. A type expression neither resolves is looked up by name in this
// file's tables only when its head name means the same declaration here as
// in std's file (`stdSrcShadowed`); otherwise that function is a signature
// gap. A file that binds a prelude enum's name to its own type (`struct
// Fragment`) keeps std's declaration as a by-position anchor
// (`preludeAnchorsOf`).
//
// Its dispatch symbols are the program's, interned in the first file's
// table, so a value boxed in one file dispatches to an impl recorded in
// another. A program type's impl is recorded as any impl is. A builtin
// scalar or a std struct or enum enters unboxed, its run-time value naming
// its type; a std container or leaf (`List<Int>`, `Range`, `Regex`,
// `Bytes`) is boxed under its type's name (`irBoxPlan`), and so is a tuple
// or anonymous record (`h: Hashable = (1, 2)`, `irStructuralBoxes`), whose
// `Hashable.hash` thunk is the derive mix a direct call lowers
// (`tupleHashOf`, irtuplehash.go), whose Display is a container's
// structural rendering (`irDisplayErasedContainer`, the VM's
// `displayStructural`), and whose `ToJson.to_json` thunk is the object of
// its fields a direct call lowers (`recordToJsonOf`, irrecordjson.go, which
// also lowers a bounded `T.from_json` at a record).
//
// Each dispatch entry is a thunk the program builds once per file, type
// and function, whose body is the call `Iface.method(...)`, so std's impl
// is whatever a direct call with that type reaches (a container family's
// operation, an instance of std's generic body, a retained body or a host
// function). Where the file binds the interface's bare name to something
// else, the thunk calls the std function whose impl header names the
// interface's declaration. A thunk is built with the gen's refusals and the
// decline bookkeeping set aside (`stdProgIsolated`), so a failed one leaves
// the boxing site's refusal. Its nodes sit at line 1, column 1, where no
// reference is recorded. `Hashable.hash` of a value held as `Hashable`
// returns what the call on the concrete value returns
// (TestStdProgIface_InterfaceCallsAnswerAsTheDirectCall). A Map key or Set
// element of interface type, and `Hashable.hash` of a value held as some
// other interface, hash with the VM's `hash_value` (internal/vm/keys.go),
// which has only to agree with `==`. `Display`, `Comparable` and `Debug`
// keep their `stdIfaceSpecs` rows, `Struct` and `Iter` their own
// representations. The stdlib lowered once for the process builds no such
// def, so a std function that takes or returns one (`String.split(s,
// separator: Matcher)`, `Json.encode_as(value: ToJson)`) is built per
// program as an instance with no type arguments, whose interface is its
// caller's def (stdprogiface.go, stdProgSigWhy).
//
// # Equality bounds
//
// A declared struct, enum or distinct type with no `Equatable` or
// `Hashable` impl of its own meets a `where T: Equatable` or `where T:
// Hashable` bound by its equality (spec §5 *Equality bounds*). The checker
// decides it in `structuralEqualityConformance`
// (internal/analysis/structural_bounds.go) over `noEqualityPartWith`, the
// one definition of what `==` accepts, wherever a bound is checked
// (`typeImplementsInterface`, `typeSatisfiesInterfaceForImplMatch`), and
// records no demand for the pair. An existential position (`h: Hashable`)
// still takes only an implementor (`partMeetsInterfaceValue`). A derive's
// field check asks the same of each component from its declaration
// (`deriveComponentHasEquality`), since types are not resolved when derives
// are checked.
//
// In the builder, `meetsByEquality` establishes that no file and nothing in
// std writes or derives the impl (`noImplOf`, `declaresImpl`);
// `Equatable.equal` then lowers as `==` (`compareOperands`) and
// `Hashable.hash` as the VM's `hash_value` (irscalaroperator.go).
// `declaresImpl` counts a generic type's impls from its template, since
// they are registered per instance and never in `implOrder`. A std instance
// gen (`Maybe.hash<Point>`) asks its program's registry (`programReg`), so
// a derived impl wins there too.
//
// # Interface defaults
//
// A default is lowered in the file that declares it, once per implementing
// type, with `self` bound to that type (`gen.defaultSelf`), so its names (a
// private function of that file, its imports, its `once` values) are that
// file's wherever the implementing block is. Each implementing block gets
// one function per default it does not write (`inheritDefaults`), so a
// type-qualified call, the dispatch table, a dictionary and a function
// value all name it as they name a written one. A block in the declaring
// file lowers the body as that function. A block in another file makes it
// one tail call of the declaring file's instance (`irImplForwardDefault`,
// ifacedefault.go), which the declaring gen queues and builds in its own
// scope (`flushIfaceDefaults`, late instances included).
//
// In the instance, a call of the interface's own function at the receiver,
// bare or qualified (`area(s)`, `Shape.describe(s)`), is the implementing
// block's function (`defaultInstImplPlan`), so an `open` override the block
// writes is the one called. A `self(n)` pattern names the receiver directly
// (`selfHeadDef`), since the declaring file may have no name for it. The
// instance is keyed by the default's declaration, the receiver and its type
// arguments (the interface's, and a generic default's own, instantiated per
// call in the implementing block first, methodinst.go), and links by the
// interface function's linkage name with them
// (`shape.Shape.describe<Square>`).
//
// A call in another file that leaves out a parameter default of an
// interface function (reaching a block's function, an erased receiver's
// dispatch or a named-argument call) fills it as a sibling file's `fn`
// default is (ifaceslotdefault.go): at the call under the declaring file's
// names (`lowerNamesFrom`) when every name it mentions resolves to the same
// declaration in both files (`defaultNamesAgree`), and otherwise through an
// accessor the declaring file builds on request, keyed by the interface
// function, the slot, the parameter kinds up to it and the type arguments,
// and linked by the function's linkage name with them and the slot. A
// generic interface's parameter default may name its type parameters (`x:
// List<T> = pick()`), so it is lowered in a frame binding the interface's
// and the function's type parameters, solved by matching the declared
// parameter types against the call's kinds (`ifaceDefaultFrame`). One the
// parameters leave unsolved while an enclosing generic binds its name is
// declined rather than read as the enclosing function's type.
//
// # Std interface defaults
//
// A default a std interface declares with a Nomi body
// (`Discrete.steps_between`) is std's code, resolving names in its
// declaring module. A std module is lowered once per process, so its
// default is a template over `self`, instantiated per receiver kind and
// program as a generic std function is (stdifacedefault.go, stdinst.go): a
// gen of the declaring module's retained view builds it with `self` bound
// to the receiver, and a bare call of the interface's own function in the
// body (`compare(b, a)`) is that function at the receiver
// (`stdIfaceOwnCall`), reaching std's, the program's or a derived impl.
//
// A call reaches the template when the checker resolved it to the
// interface's function and no route that finds a written impl function
// answered it (`stdIfaceDefaultCall`, the last arm of `qualCallLowered`):
// `Day.steps_between(a, b)`, `Discrete.steps_between(a, b)`, and either
// inside a generic body once its argument is known. So an `open` override
// is found first (`stdIfaceOverride` admits it beside a `stdIfaceSpecs`
// row, whose table holds the row's functions alone). A row's anchor ignores
// the interface's Nomi defaults (`stdIfaceSpec.matches`), so
// `Comparable.compare` keeps its table when Comparable gains defaults; a
// default on an erased receiver declines. The instance links by the
// interface function's name with its receiver
// (`std/discrete.Discrete.steps_between<Day>`).
//
// A std body that calls a default is not retained by the shared lowering,
// which builds no instances; a program builds it as an instance with no
// arguments (`stdUnretainedMonoBody`), including a call that leaves out a
// parameter with a literal default (`stdUnretainedShortCall`). A default
// that is no literal (`max_fraction: Int = Int.max(Decimal.scale(d),
// min_fraction)` in `Decimal.format`) is Nomi of the function's module, so
// the instance's gen builds the function's arity wrappers and default
// accessors beside its body and plans the call against them
// (`stdUnretainedDefaultedCall`). They link by the names the shared
// lowering would give them (`Decimal.format arity 3`), which it never
// defines for a body it does not retain: `irStdArityRetain` and
// `emitStdSlotDefaults` build only beside the body. The shared lowering
// builds no instance of a default even at a receiver only std declares
// (`Int.min`), so it never defines a name a program's own `Int.min` call
// defines too.
//
// Range's and Iter's own protocols (`known_count`, `is_infinite`,
// `steps_between` as a Range's step count) keep their lowering in iter.go
// and irrange.go: the VM drives them with the impl's functions as values
// and answers an inherited default statically, and `Iter` is generic, which
// the template does not cover.
//
// # Type argument conversion
//
// A std collection converts to wider type arguments (spec §5 *Type argument
// conversion*) only where the conversion costs nothing, so an ordinary call
// never converts a list element by element behind the reader's back. What
// one element's conversion costs decides it:
//
//   - the same type: no work.
//   - `Int`, `Float`, `Bool`, `String`, a declared struct, a declared enum
//     with no `embeds` variant or a declared distinct type into an
//     interface it implements: no work. The existential is the value, and
//     a dispatch reads the value's own type name (`erasesUnboxed`).
//   - a function into one whose parameters and result convert as above: no
//     work. A call passes and returns the same values.
//   - a std container, a `Maybe` or `Result`, a std leaf (`Decimal`,
//     `Bytes`), an `embeds` enum, or a tuple into a std interface, into an
//     interface: a box per element (`irBoxPlan`).
//   - an embedded type into its enum: a record around it
//     (irembedsrebuild.go).
//   - a function that needs its argument or result converted another way:
//     an adapter closure (`funcWiden`).
//   - a collection into an `Iter`: a view.
//
// The checker admits the conversions of the first three kinds under a
// List, Vector, Set, Map (keys and values), Maybe or Result, nested and
// through a tuple's elements and a record's fields (`unifyAssignArg`,
// `carriesOwnType` in internal/analysis; `unifyAssignFree` inside a
// function type there), and refuses the others with help that names the
// explicit form. Two conversions that do work per element are still
// admitted: an embedded type entering its enum under a type argument
// (`List<Circle>` as `List<Shape>`), and a collection of collections
// entering a collection of `Iter`s.
//
// The builder returns an admitted value with one `ir.Copy` typed at the
// wider kind (`relabels`, `irCopyRelabel`, irrelabel.go), in `coerceEmpty`
// before any rebuild is tried and in `qualSignature`, so a std or sibling
// function's argument passes as it is. [ir.ValType.Accepts] accepts the
// copy: two instances of Maybe or of Result (`sameCovariantEnum`), and a
// function parameter either way round. A relabelled record may hold an
// `Int` in a word slot where its new type says a reference, so the VM
// reads a field through the record's own descriptor (`frame.writeField`,
// `opProjF`, `opProjP`), and equality and hashing read each slot by its own
// layout (rt's `Keys.recordsEqual`, `hashSlot`).
// TestRelabel_ConvertsNoElement counts it: converting a list of 10,000
// Ints, structs, functions or lists, or a map of 10,000 entries, enters one
// function body and returns the very list it was given, where
// `Iter.map(xs, keep) |> Iter.to_list()` enters 10,001.
//
// A declared generic type's type arguments stay exact. Its instance's
// descriptor names them ([rt.TypeDesc].Inst, `main.Box<Int>`), and a
// dispatch on an erased instance (the VM's `implOf`) and an erased Display
// rendering select by that name, so a relabelled `Box<Int>` held as
// `Box<Display>` would reach `Box<Int>`'s impls, which need not exist.
// Converting one for free would need impls recorded for every instance a
// conversion reaches, and a checker rule that `Box<Display>`'s impls imply
// `Box<Pt>`'s, which does not hold: an impl's `where T: Equatable` holds
// for any interface value and need not for `Pt`.
//
// Where nothing says which way values flow (two typed collections joined
// with `+` or `-`, two typed spreads, the branches of an `if` or `case`
// with no expected type, the elements of an unannotated list), their types
// must be one, and the error carries a help and a fix naming the
// annotation that makes them one (internal/analysis/annotation_help.go).
// The type is found by walking the two types together: where they differ,
// a type alias the file can name whose type both parts convert to (the one
// the enclosing signature names wins a tie; an unbroken tie suggests
// nothing), else the walk goes into a List, Map, tuple or function result,
// else the part the other converts to; failing that, one side's whole
// type. The candidate is spelled with the names the file writes
// (`http.Handler` under `import std::http`, `spellType`) and kept only when
// the value checks against it as a binding's value would
// (`annotationTrialPasses`, a trial check whose errors and recorded types
// are put back). For `+` and spreads the values that change must read a
// binding with no annotation, and that binding is the one annotated.
//
// # Correctness
//
// The IR is checked by what the VM does with it, against the golden files
// in testdata/expectations (internal/expectation), and every retained graph
// must pass [ir.LintModule]. The harnesses that hold the builder to its
// properties are doc comments on their tests: FuzzFrontEndAcceptsSoItLowers
// and FuzzLoweredProgramsRun (vmhost), TestDeclineSitesAreListed
// (internal/declinesites), TestObjectsLinkToTheSameProgram and
// TestFilesLowerOnTheirOwnAsInTheProgram (internal/expectation), and
// TestStdBundle_DecodesToTheLowering (here).
package irbuild
