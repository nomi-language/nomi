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
// declaration of its own unit from another file's (irstdmethodcall.go). Nomi permits cyclic file imports, and the analyzer
// resolves names whole-program, so import order never matters.
//
// The partition reads the import graph. Every reference between files is
// an import edge, since a qualified `owner.member`, a bare name
// from a selective import and a type reference all need the name in scope,
// and a user file's name reaches another file's scope only through an
// `import`. An import's edge comes from what the checker bound it to
// (`resolvedImportEdges`) as well as from its path as written, since a path
// spelled through the module's own name (`import r3::value.Value` in module
// `r3`) matches no file key. The type groups of typecycle.go are found
// within these components, so a cycle the import graph missed would build
// its files one at a time and refuse the cycle. `pub` is enforced by the front end, so two files sharing a unit
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
// The shared lowering lowers std modules in dependency order
// (`stdLoweringOrder`), and a module's bodies resolve only against the
// modules lowered before it. The members of an import cycle are lowered
// as one unit (`stdLoweringUnits`, `lowerStdUnit`): in passes, each member
// against every other, until no member lowers more, keeping each
// declaration's symbol from pass to pass. So a std module may call into
// any member of its cycle by every call shape (an operator or interface
// dispatch to an impl, `Int.to_byte` from Byte's impls), and which bodies
// lower does not depend on the order of the members, which an edit to one
// member's imports may change (`TestStdLoweringIsTheSameInEveryCycleOrder`).
// A body that calls a function of a module outside its imports that is
// lowered after its own (a prelude type's method) is not retained; each
// program that reaches it builds it as an instance with no arguments,
// against every module (`stdUnretainedMonoBody`, `otherModuleCallee`).
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
// and check their `Currency` literals at every load.
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
// is the type and signature waves.
// kindmemo.go memoizes the retention predicates, `stdHostOriginKind` is an
// index, the lint describes an instruction only for a violation, and a
// generic std instance's module binding clones the earlier modules' index.
//
// Not linked: a program whose entry is a listed module, a shared module's
// cases that import listed modules, a reference editor's edited module with
// its twins (below, "Linkage names"), and a program that reaches a
// test-only fixture module.
//
// # Calls
//
// The builder decides nothing about where a call's arguments go, the
// order they are evaluated in, or which parameters are left to their
// defaults. The checker records each call (analysis.FileAnalysis.Calls, an
// analysis.ResolvedCall: what fills each of the callee's parameters, and
// the written arguments in evaluation order), and every route reads the
// record through `callPlan` (callplan.go): a direct call, a sibling file's
// function, a qualified call with named arguments or omitted defaults, a
// std short call, a call through a function value, a partial application,
// `Task.spawn_all`, a supervisor's functions and `Struct.update`. A pipe
// stage is lowered as its call with the head spliced in first, and reads
// the stage's record, in which the head fills a slot.
//
// A copy of a written call (the spliced stage, a call requalified through
// its module's alias) finds the record by the written call's position,
// and only while it holds the written call's arguments. A call the
// builder writes itself carries its record (`carryCall`): a `then` or
// `tap` stage's call of its function, which the checker records under the
// stage; a function value's call of the function it stands for and a
// loop's call of its step, which pass their parameters in order
// (analysis.PositionalCall); a named call rewritten over stand-ins in
// slot order; and a partial's body call, whose arguments the builder wrote
// at their slots. A call with no record, or with other arguments than its
// record's, is the builder's own error and panics.
//
// The record also classifies the callee (analysis.Callee): the
// declaration the call reaches, by the checker's symbol and node, and
// whether one of the program's files declares it. Four routes read it: a
// qualified call is planned from the recorded declaration (below,
// "Qualified calls from their records"), a partial application over a
// local function value takes its parameters from the recorded callee
// (`partialCalleeRecorded`), a call the record gives to a program's own
// `fn` stays out of the route for std's Range functions
// (`callsProgramFunc`), and under a test binary every route
// that ends in a call of a declared function is held to the record's
// declaration (`verifyCallee`), which fails the build on a difference:
// a `fn`, a `host fn`, an impl's function (a qualified plan's token
// answers it, `irQualPlanTarget`: an impl item's own `fn`, or for an
// inherited default the interface's function with the block, which must
// be the record's `Callee.Impl`), and a plan that dispatches where it
// runs only for a record with no impl (`verifyDispatched`). A call of an
// instance of a generic std function is held by the template the
// instance is built from (`stdInstCallAt`). An interpolation hole that
// calls an impl is held the same way to its record (`verifyImplUse`:
// DisplayRender.Method).
//
// # Qualified calls from their records
//
// A qualified call `Owner.method(args)` whose record names the function it
// reaches (analysis.Callee.Decl: a `fn`, a Go-bound `host fn`, or the
// interface's function the record's block, `Callee.Impl`, inherits) is
// planned from that declaration first: `recordedCallPlan`, the first route
// of `qualImplPlan`, which `typeQualCall` reaches after the std calls it
// lowers by a route of its own (`stdQualRoutes`) and the intrinsics. A
// program's function, or a std module's lowered with this gen, is the item
// a gen registered for the declaration it lowers (`implItemsByDecl`, filed
// by `addImplItem` for every impl and owner function, and for an inherited
// default under the interface's function), read as that gen's module
// declares it (`ownDecl`: an edited std module's twin of the shared
// parse's function). It is found at the receiver an operand is
// (`recordedImplItem`; another gen's generic instance at that gen's own
// def of it, `instanceIn`), else, with no operand of its receiver, as the
// owner function or at the instance the checker instantiated the call at,
// which the gen that declares the type builds (`recordedOwnerItem`,
// `templateCallInstance`), or at the instance an owner function's call
// through a type parameter carries (`instKind`). A function its block
// withholds for each call, and an inherited generic default, is built for
// the call by the gen whose block declares or inherits it
// (`recordedWithheldPlan`), and a Go-bound function of a generic type
// called with no instance crosses to its Go function
// (`hostTemplateImplPlan`). A program's block over a std container is
// registered at the container kind an operand is before the lookup
// (`stdRecvRecordedAt`, the on-demand registration `stdRecvImplAt`), and
// `stdRecvOwnerPlan`, which `typeQualCall` tries before the container
// routes, plans from the record first (`recordedImplPlan`). A std
// function is the one of its identity
// (`stdFuncByID`). The plan is built by the code each kind of function
// already had: `implItemPlan` for this gen's item, `siblingInstanceItemPlan`
// for another gen's generic instance, `siblingSitePlan` and
// `siblingOwnerPlanFor` for another file's, `callerItemPlan` for a
// program's item called from a std instance built for the program (a
// derived `Maybe<Written>.hash` calling `Written`'s), `siblingMethodPlan` for another
// gen's withheld function, `stdRecvItemPlan` for another file's impl on a
// receiver no file declares (`impl Multiply<Shares, Decimal> for
// Decimal`), `stdFuncPlan` and `stdSiblingShortPlan` for std's.
//
// Inside an instance the record is read at the instance's types
// (`calleeAt`, the one place `recordedCallee` reads it): a call the
// checker left to dispatch on a type parameter of the generic body
// (CalleeInterface, For naming the parameter) is answered by the checker
// at the checker's types the frames carry (analysis.FileAnalysis.CalleeAt
// over `genericSubstType`; in a std instance through the program file
// that made it, `instanceFA`), and where that names a function the call
// is planned from it like any other. An interface's default lowered for
// one implementor is such an instance (`inInstance`, which `calleeAt`,
// `operatorUseAt` and `displayAt` ask): a call on the default's `self`,
// bare or qualified, is for the interface's SelfParam (the type parameter
// `self`), which a std default's frame binds and a program default binds
// through `defaultSelf` (`irDefaultSelf.typ`, the implementing block's
// checker type, `implDef.recvType`). The type of a `self` value, which
// expressions, call type arguments and renderings record, is the
// interface marked DefaultSelf; analysis.ReplaceTypeParams answers it as
// SelfParam, so every record read at an instance (`checkerTypeArg`,
// CalleeAt, OperatorUseAt, DisplayAt) puts the implementor in for either
// form. `T.name(x)` respelled by
// `stdKindQualCall` as the owner's call carries the same record and is
// answered the same way. Under substVerify, `verifyCalleeAt` holds a call
// a later route plans to the record at the instance.
//
// A call the record leaves to dispatch where it runs (CalleeInterface with
// no Impl, read through `calleeAt`, so inside an instance whose type
// argument is an interface type too) whose receiver operand is held as an
// interface value, one interface's existential or a conjunction, is
// planned by `dispatchedCallPlan`, `qualImplPlan`'s second route. The
// function is the record's Decl among the functions of the interface the
// receiver is held as (`recordedMethod`: by declaration, a std function in
// another parse by its identity, and a std interface's def, resolved from
// an earlier parse of its module, by the declaration's position), and the
// receiver is the operand the placement record puts at the function's
// `self` (`receiverArg`); a conjunction's receiver enters as the member
// that declares the function (`conjReceiver`). `erasedInterfacePlan`
// builds the call, an `ir.NewDispatchCall` on the receiver's run-time
// type, placing the operands through the record and dispatching an
// omitted default through the receiver. It is the only route that plans a
// dispatch, and `stdModuleIfaceCall` (a std interface named through its
// module where this file declares the name) takes it too.
// `verifyDispatched` holds a dispatch plan to a record with no Impl.
//
// The routes after it choose by the owner's spelling, the receiver's kind
// or a name-keyed table, and plan a static call. They serve calls the
// record leaves to dispatch whose receiver the builder holds as a value of
// a known type: a call in an interface's default whose implementing
// block has no checker type (the impl index in `concreteInterfacePlan`), a
// value written `as` an interface, which the builder holds as the value it
// is although the checker records the interface (`foreignIfacePlan`,
// `siblingReceiverPlan`), and a type parameter's the instance's types do
// not resolve (a parameter whose frame has no checker type,
// `maxSubstWithoutCheckerTypes`, as a std interface default's instance at
// a std interface value's dispatch entry binds its `self` by kind alone).
// A receiver that implements a generic interface at several type
// arguments is resolved by the call's interface instance
// (Symbol.CallInterface), which the record carries. A call of a std
// interface's function the builder writes on a value whose checker type
// it holds (a tuple slot's `Hashable.hash`, a record field's
// `ToJson.to_json`, `debugOver`'s `Debug.inspect`) carries what the
// checker records for that call written at the type
// (`stdIfaceCallRecord`, analysis.FileAnalysis.InterfaceCallee); one on a
// value held by its kind alone (a std interface value's dispatch entry,
// `stdProgThunk`; a main's failure) names the interface's function and no
// type, and is planned by the routes after. They serve calls with no
// record (an interpolation hole rendered by kind, `Display.to_string`),
// and one kind of record-named call the declaration lookup does not
// reach: a function of a program's block over a std container called with
// no operand of the container (`List.from_sql(value)`), whose instance
// `stdRecvOwnerPlan` reads from the checked result.
// `TestQualImplPlanRoutesOnlyGoDown` counts `qualImplPlan`'s routes.
//
// # Operators from their records
//
// An operator (`a + b`, `a == b`, `a < b`, a prefix `~~~`) calls the
// function its record names (analysis.OperatorUse.Impl.Method), read once
// by `recordedOperatorImpl`: a program's impl, a derived one and one of a
// std module lowered with the program is the item the gens registered for
// that `fn` (`recordedImplItem`, over each gen's `implItemsByDecl`, which
// `resolveImplDef` fills for every impl block, a generic receiver's once
// per instance); std's own is the function of the recorded identity among
// the std index's functions of its owner and name (`stdFuncByID`). A
// qualified call reaches its function through the same two lookups
// (`recordedCallPlan`). The record does not
// say whether the operands are a kind the machine operates on itself: it
// names std's `host fn` for `Int + Int` too, so `binary` lowers Int,
// Float, Decimal and String arithmetic natively, and calls std's impl
// only where `stdOperatorNative` says the machine has no operation. An
// operator over a type parameter (OperatorPerInstance) is read inside an
// instance at the instance's types (`operatorUse` over `operatorUseAt`,
// analysis.FileAnalysis.OperatorUseAt): an impl, a structural comparison,
// or no record for a primitive, which the machine operates on as outside
// an instance. One over interface values, one whose type parameter has no
// checker type in the frame, and one the builder wrote itself name no
// impl: it is selected at the instance's kinds (`instanceOperatorImpl`,
// `stdlibOperatorFor`, `instanceEquatable`, `localCompareImpl`), a
// selection a recorded operator never reaches. `TestOperatorNameTestsOnlyGoDown` counts the
// tests of a name left in the operator-lowering files.
// A std function is one declaration in each parse of its module
// (`sameDecl`: one analysis.StdFunc, wherever it sits; an impl's
// function is told from a sibling impl's by its impl's interface and type
// arguments): a std module
// checked from its text is a second parse beside the shared analysis's. A
// shared module's cases are lowered against the shared lowering's tables,
// and a listed module lowered beside an edited one names the shared
// parse's functions, which reach the edited twins (`indexEditedStdTwins`).
//
// A call whose callee is a bare name is routed by the recorded callee: a
// function value (a parameter, a binding, a `once`, a `fn` declared inside
// a body) is called through the value; a module function is found by its
// declaration (`sigByDecl`, registered beside the name-keyed `funcs` by
// `declareFunc`), a std module's own sibling by its declaration
// (`stdSiblingDecls`, a `host fn`'s too), another file's function by its
// declaration (`fileIndex.byDecl`), and a std function a selective import
// brought in by the symbol the checker resolved the name to. A function
// named as a value (`funcRef`) and a partial's callee are found the same
// way. No local, `once`, function or sibling table is read by the name's
// spelling there. A call the builder writes over a function the source
// names carries that function's classification
// (analysis.FileAnalysis.CalleeOf; `positionalCallOf`, `rewrittenCallOf`).
//
// The record also says which std function a call reaches
// (analysis.Callee.Std: module, owner, the interface an impl's function
// is declared for, name). That is std's key for its own functions, and a
// declaration node is a program's: the std bundle keeps no declaration
// for a `host fn`, and builds the key from names it holds. A std module's call of a generic sibling
// (`stdGenericSiblingCall`), a file-qualified or selectively imported
// generic std function (`stdGenericFileCall`, `stdInstFuncOf`), the bare
// callees `callsGenericSibling` scans for, and `testing.check` are found
// by the triple, and the std functions the builder lowers by a route of
// its own are one table keyed by it (`stdRoutes`, stdroutes.go).
//
// The record also says what a qualified call is qualified with
// (analysis.Callee.Qualifier, Method), the type or interface a bare
// call's function is declared under (Owner; RouteOwner answers the one a
// call is routed by), and the type an interface's function is called for
// (For; a type parameter's, which an instance substitutes); TestElaborationGate
// fails a call whose qualifier or owner is not recorded. A call the
// builder writes over a written one carries its record (an instance's
// `Int.from_json` for `T.from_json`), and one it writes of a std
// interface's function names that interface (`stdIfaceCallRecord`). The
// head of `qualCallLowered` reaches a type qualifier by the record
// (`qualOwner`): by the declaration's own name where this file's scope
// and tables bind that name to it (an alias names the declaration), else
// through its std module (`stdModuleOwnerCall`) or, for a program's type,
// the function of its block the checker resolved the call to, an owner
// function or an impl's, at the instance the call is about
// (`resolvedOwnerPlan`). A name this file binds to another declaration
// of the same spelling never reaches it: a literal's check of another
// file's `Many` in a file that declares its own `Many` calls the other
// file's `try_from`.
//
// The std calls `typeQualCall` lowers by a route of its own (a seeded
// `Iter.reduce`, `Hashable.hash` over a tuple, `Display.to_string` over a
// scalar, `Maybe.is_some`) are a second table in stdroutes.go,
// `stdQualRoutes`, keyed by what the call is qualified with: the record's
// std type or interface (RouteOwner().Std) and the method, which
// `qualRoute` reads once per call. An exact key names one function; a key
// with no method is owner-wide and routes every other function of a std
// type to one lowering (Iter's to `iterCall`, Range's to `rangeCall`), so
// `Iter.loop` and a seeded `Iter.reduce` keep their exact routes ahead of
// Iter's. Its routes are values of the same enum
// as `stdRoutes`'; `typeQualCall` tests the route at the point each is
// lowered, since a route may hand the call on (`Hashable.hash` over a
// tuple first, over an interface value after the operands are lowered),
// and its order against the routes that are not std's is kept. A
// program's type or interface of a std name has no std identity, so no
// route tests that the name is not shadowed. A copy the builder respells
// with a concrete owner (`stdKindQualCall`'s `Map.contains` for
// `C.contains`, `Display.to_string` for `T.to_string` at a tuple, a
// turbofish's type argument) carries the written call's record qualified
// with that owner (`respelledCallOf`): for a kind, the std type it is an
// instance of (`kindQualifier`, by the checker's index of std
// declarations, whatever this file's scope binds), so a std module that
// imports no Map still routes `Map.contains`. Only code the checker never
// checked is keyed by the owner the builder wrote, resolved in this
// file's scope. A route that depends on more than the owner keeps that
// condition inside it: a program's impl over Range (the record's
// Callee.InProgram) stays out of Range's. Map, List, Vector and Set are
// owner-wide routes to their call plans and their std-body instance.
// TestTypeQualCallNameTestsOnlyGoDown counts the tests of a call's
// spelling `typeQualCall` still makes.
//
// A qualified call of an interface's function is answered by an impl,
// by dispatch on an interface value, or structurally (a tuple's hash, an
// anonymous struct's JSON, the equality of a type with no impl of its
// own), and the checker records which (analysis.Callee.Form, by the rules
// an operator's OperatorUse.Form follows). Within a route, `typeQualCall`
// still picks the structural routine by the operand's kind, since the
// routines also serve calls with no answer recorded: those the builder
// writes holding a kind alone (a main's failure rendering), a receiver
// the checker left with an open variable, and a per-instance record a std
// instance's frame cannot resolve. A call whose record names an impl is
// never answered structurally (`recordNamesImpl`), even where the impl's
// function is not in scope yet: an import cycle's first pass lowers a std
// module before the members whose impls it calls (`lowerStdUnit`), and
// the answer must not depend on that order. Under test `verifyCallForm`
// holds each pick to the record where there is one.
//
// Two sites lower code the checker never checked, and mark it so
// (`markUnchecked`; TestNameLookupsOnlyGoDown holds their count): the
// universal Debug of a type declared inside a body, which
// blocklocaltype.go synthesizes because an impl cannot name such a type,
// and parse_fields' per-field `F.parse(text)` over an anonymous struct,
// whose F irparsefields.go chooses from the field's kind because the
// checker checks `P.parse_fields` once, generically. A call in marked code
// has no record and is reached by its spelled owner. Removing the first
// needs the checker to check a block-local type's synthesized impl in its
// block's scope; the second, records the checker makes per instance.
// TestNameLookupsOnlyGoDown counts the functions and table reads that go
// by name, and the counts only go
// down. `callPlan` answers false
// when the record places an argument past the parameters of the callee a
// route chose, so the route declines.
//
// # Defaulted parameters
//
// Which parameters a call leaves to their defaults is the checker's: its
// record of the call marks each such slot analysis.SlotDefault, and
// `callPlan` hands them on as `argPlan.defaulted`, ascending, the order
// they run in after the written arguments. A route that fills defaults
// only once it has chosen a callee asks `leavesDefaults`; none counts the
// written arguments against the parameters, which assumes they fill the
// leading slots (TestArgCountDefaultTestsOnlyGoDown). A record the builder
// writes for a call of its own marks the slots it leaves out the same way
// (`PositionalCall`, `writtenAtSlots`).
//
// The checker also records, for each call that leaves a defaulted
// parameter out, the declaration whose defaults fill it
// (analysis.FileAnalysis.CallDefaults), and for a function named as a
// value where fewer parameters are expected, the slots its call fills
// (DefaultedRefs). Every call route places the written operands and hands
// the defaulted slots to one function, `fillCallDefaults` (irdefaults.go),
// which reads only those records; the routes supply where the callee is
// declared (`defaultHome`). The record names, per omitted slot, the
// declaration whose default fills it (`analysis.DefaultedCall.From`): an
// impl function's slots it does not default are its interface's. The
// slots are filled in runs of one declaration each (`defaultRuns`), each
// under that declaration's parameter names. A call through a local copy of a declared function (`g
// = parse`) goes through the function the record's Ref names. A call of
// an interface function on a type parameter takes, at each instance, the
// default of the impl function it reaches where that function declares one
// (`defaultParams`). A call on an
// interface value fills each omitted slot by a call dispatched on the
// receiver to that slot's own dispatch symbol, which each impl's entry
// answers with its accessor for the slot (`slotDispatchHome`,
// ifaceslotdispatch.go).
//
// A call into another file that omits a defaulted parameter calls an
// accessor the declaring file builds, one per defaulted parameter, taking
// the parameters before it (siblingdefault.go). A callee whose file builds
// none has its defaults lowered at the call when every name they mention
// means the same there (`defaultNamesAgree`). A file
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
// A call into a std module from outside it fills an omitted parameter
// through that module, since the default may name what the caller's file
// does not import. A call of a function the shared lowering retains that
// leaves parameters out, trailing or skipped by name, calls the module's
// accessor for each omitted slot and then the full body (stdslotfill.go,
// which also records what this costs against a wrapper of the shorter
// arity). The
// shared lowering retains no generic function, so for one (`Iter.sort_by(xs,
// key: f)`, past `direction: Direction = Direction.Ascending`) the program
// builds the accessor, in a gen of the declaring module at the call's type
// arguments (`stdInstances.defaultAccessor`), whether the function's body
// is instantiated from std's source or the builder lowers the operation
// itself. A short call of an instance the program builds (`Reader.chunks(r)`)
// fills its omitted slots with the same accessors at the instance's type
// arguments (`stdDefaultAccessors`, `fillStdInstDefaults`). A default is
// never written into the call as an argument, whatever its shape: every
// one runs through an accessor or the declaring scope's `callDefaults`.
//
// Every accessor, a program file's, an interface function's and std's
// (the shared lowering's, a per-program body's and a generic instance's),
// is built by one function, `irSlotAccessor` (siblingdefault.go): the
// parameters before the slot, destructured ones' names bound, then the
// slot's default lowered as a local call's is (`callDefaults`) in the
// declaring scope it is given. A caller holds every written operand, then
// calls the accessors of the omitted slots in ascending order, each over
// the operands before its slot, so a default may read any earlier
// parameter whichever file or module the call is in.
//
// So a default is evaluated in one of four ways, each by `callDefaults`
// (`callDefaultsThrough`) or by code the lambda builder owns:
//
//  1. At the call, in the callee's scope: a function of this file or a
//     std module's own function called inside it, a generic instance, an
//     interface's default whose names agree here, and another file's
//     function whose file builds no accessors.
//  2. In an accessor of the declaring file or module, called at the call
//     (`fillFromAccessors`, `fillStdInstDefaults`) or dispatched on an
//     interface value's receiver: another file's function, every std
//     function called from outside its module, and an interface function
//     whose impl is chosen where it runs.
//  3. In a supplier closure of a function value (`irLambdaGroup.suppliers`),
//     called through `indirectCall`: a lambda value and a partial's open
//     slot, whose callee is a value at run time. A partial's supplier
//     evaluates the callee's default by 1 or 2.
//  4. Inside the callee's own body: a lambda called at a shorter type and
//     an `Iter.loop` step's extra parameters, which no caller passes.
//
// # Interpolation
//
// The checker records how each value the program renders by Display
// renders: an interpolation hole's (analysis.FileAnalysis.HoleDisplays),
// the error a typed literal is refused with at compile time, a fixed
// literal's handler's or a checked literal's check's
// (analysis.TypedLiteral.Failure), and the error a failed `!` shows
// (FileAnalysis.UnwrapFailures). `holeDisplay` (irdisplayhole.go) reads a
// hole's record, `irErrorRenderFunc` a failure's (`literalRender`,
// `bangRender`), and `renderDisplay` lowers one: a String is itself, a
// scalar or a structural value is an `ir.Render`, an interface value is
// the erased render, a tuple or record with a nominal part renders each
// part by its own record and lays the texts out structurally, a std
// container calls std's instance, and a declared type calls the impl
// function the record names, reached from its declaration as a recorded
// call's function is (`displayImpl`, `recordedCallPlan`). The route is the
// record's; the builder declines a record its lowered kind does not fit.
// In an instance, the rendering of a type parameter is the checker's at
// the instance's types (`displayAt`, analysis.FileAnalysis.DisplayAt).
// A value no record describes (`io.print(x)`, `Display.to_string` over a
// tuple, ParseFields' texts, the error `main` returns), an interface
// default's `self`, and a type parameter where a frame has no checker
// type render by `kindDisplay`, the checker's rule over the lowered kind.
// Such a rendering names no impl, so `displayImpl` writes
// `Display.to_string(x)` and finds the impl by the kind's name: the one
// place the package writes that call (`TestDisplayToStringCallSitesOnlyGoDown`).
// The universal Debug of a block-local type is synthesized here and never
// checked, so its holes have no record; they are Strings, each its own
// text.
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
// reason as a hint.
//
// The places the builder can decline are listed in
// testdata/lowering-declines.txt, which internal/declinesites reads from
// this package's source and TestDeclineSitesAreListed holds to it. Each
// site has a class: reached by a suite (a), dead because no checked
// program reaches it (b), a gap pinned by a reproducer in vmhost's
// inventoryLoweringGaps (c), or undecided (d), with the cause it is there
// for. Three counts only go down: the total, the dead sites, and the
// undecided ones. A dead site is not left a decline: it becomes a panic,
// or its check is deleted.
//
// A function says in its signature which of its results can decline: one
// it leaves unnamed or names `ok`. A bool result with another name is an
// answer, whose false declines nothing: a predicate's (`relabels(have,
// want) (relabel bool)`), a lookup's (`importedVariantAt(...) (d, v,
// isVariant bool)`), a route's `handled`, or the first of two results
// (`endlessLoop(n) (endless, ok bool)`: whether n is such a node, and when
// it is, whether it lowered). A caller that declines on an answer is the
// site. A caller that only passes a callee's decline on (`if !ok { return
// ..., false }`, with or without a note naming it) is none.
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
// A decline is for a program the builder does not lower yet. A state no
// checked program reaches panics instead, naming what was missing, and the
// host reports it as an internal error (vmhost.InternalError, "internal
// compiler error: ..." from `nomi run`, `check`, `test` and `build`, and
// one diagnostic at the top of the file from the language server): a
// checker record the elaboration gate guarantees (a callee's reference, a
// call's CallDefaults, a hole's HoleDisplays; analysis package doc,
// "Elaboration"), a field the parser always sets, a shape std's own bodies
// fix (the private functions std/io, std/fs/host and std/http/host call,
// std/ranges' Range literal), the builder's own tables (a registry unit's
// gen, an instance over held arguments), and a graph that does not lint
// (irRequireLint, irScalarLower's rule). A site is made a panic only after
// programs aimed at it were rejected by the checker or lowered: the note
// "the checker rules it out" was wrong for a patch literal with a spread,
// a bare variant written with braces, a nested fn's named call to itself
// and `Range.method` for a program's own interface, each now a pinned gap.
//
// Two hand-written switches are held to the AST's node types, so a node
// type added later cannot fall out of one: nodeForms (nodeforms.go) gives
// every node type its forms, TestEveryNodeTypeHasAForm holds lowerNode's
// arms to the types classed a value and irScalarBlock's to those classed a
// statement, and what falls out of either panics. importKind's switch over
// a kind's tag is held to the tags the same way
// (TestImportKindHasAnArmForEveryTag). irScalarBlock answers a tail for
// every block, so no caller asks whether it did.
//
// The value domain (irValueDomainKind) is the kinds a lowered value may
// have: a call operand (irCallOperandKind), or the diverged marker of a
// region every arm of which leaves; a bare `None` is lowered at the type
// the checker gave it. The builder holds kinds to it where they enter a
// body: a declared function's or an instance's signature (signature,
// resolveMonoInstance) is unlowerable with a parameter or result outside
// it, funcDecl refuses one by name, irFuncShellAt builds no shell for one
// (a default's accessor among them), and lowerExact declines a lowered
// value outside it (inValueDomain). The one exception is a kind that
// mentions a bound-free type parameter, which only that generic
// declaration's own signature and shell hold.
//
// What the entry held is asserted, not asked again: irHeldOperand panics
// on a kind outside the call operands, irLoweredTupleRetained,
// irLoweredRecordRetained and irHeldUpdateBase on a tuple, record or update
// base the callable domain holds that is not retained (none is), and a
// construct's own panic on a kind built from held parts that its predicate
// refuses (a Set or Maybe over a held element, a List of a spread's held
// elements, a map update's entries). Held are a lowered value's kind; a
// lowered body's result once the diverged marker is replaced by the kind
// its returns settled (a lambda, a `concurrent` body, an `if` or `case`
// used for its value); a name a pattern binds from a held value; a held
// signature's parameters and result (a sibling's, a std body's, a callee's
// defaulted parameter); and the checker's type of a value the program
// holds, projected (project, typeOf): a capture's or a function
// reference's parameters, a field accessor's subject, a `once`, a setup's
// value, the error type a failure renders, a recursive nested fn's
// signature, a typed position's target, a variant literal's enum. Every
// type a checked program declares projects into the domain;
// TestTypeOperationGrid holds each value type to each operation, and a type
// the builder refuses is planted (PlantTypeDeclineForTest), whose
// declarations' signatures are refused before a body reads them. The
// projection is not asked this where a caller probes a reference's type for
// another route: checkedValueKinds declines a generic owner's function
// whose type parameters no frame binds.
//
// A pattern is held the same way: the checker types each pattern against
// its subject's type, so a pattern test asserts that the pattern is one its
// test builds over the subject's held kind (a variant's head names the
// subject's enum, a payload or field sub-pattern is one irFieldSubPattern
// tests, a tuple pattern's components are ones tupleCaseComponents tests, a
// list pattern over a named type is over a distinct of a list).
//
// The domain guards that remain ask what the entry did not hold: a kind
// joined from operands that differ (a map literal's entries), an instance
// that may fail to form (genStructInstance, listResultKind), a probe that
// takes another route, and the gen-level destructure's own refusals. A
// marker, std's own (`ChannelClosed`) among them, is a retained leaf
// (irRetainedMarker), so it passes each narrower predicate as any leaf
// does. A container that only carries
// its elements holds any value of the domain, Unit included: a List or
// Vector element (irRetainedValueKind), a Map value (irMapValueKind), an
// `Iter<Unit>` (irSeqKindOf), a channel or a task's payload
// (irConcHandleKind), a Maybe's or Result's payload
// (irRetainedPreludePayload), a list pattern's subject (irCaseListKind) and
// a Vector literal's items. What reads an element (equality, hashing,
// Debug) keeps its own check. A stage that builds a List from the elements
// it moves (a chunk, a flattened list) asks irHeldListKind.
//
// A test about how a blocked program is reported blocks on a planted
// decline, a call to `planted_decline` that [PlantDeclineForTest] makes the
// builder decline, never on a real gap. One about a refused signature
// plants a refused type, a struct [PlantTypeDeclineForTest] names.
//
// A call to a function whose signature the builder refused declines in the
// caller, since the callee's body is never attempted; its Decline carries
// the refused signature ([SignatureDecline]: the parameter or result, the
// builder's reason, and the declaration the refusal cascades from), and a
// host reports it there (signaturedecline.go).
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
// `stdOnce`'s outcome (lowered or not, refusal, default accessors,
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
// receiver and substitution, a default accessor its slot (after the type
// arguments, for a generic std function's); an interface method a program
// interns for a std interface (`stdIfaceMethodToken`) is the program's.
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
// shared analysis (std/storage/testing beside an edited std/storage). Their
// references to the shared copy resolve to the edited module's declaration
// of the same kind and name (`implFuncTwins`): types in the type registry,
// module scope and file-level functions in the file index
// (`indexEditedStdTwins`), so `storage.check_key` in std::storage::testing calls
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
// The typed literals a program checks at load are kept too
// ([KeptLiteral]): each site with its cell and failure renderer named by
// their index in its unit's object. A lowering of one file answers the
// program's sites as a whole lowering does, the kept units' resolved in
// their decoded objects, the file's from its new module and the listed
// std modules' from their kept lowering, so the caller checks every one
// again. A literal's cell is local to its module, so `carryKept` takes
// one the new lowering lacks as gone with its body unless a carried
// function reads it. A site the file's lowering puts in a std instance or
// in a file read with its bodies is a miss.
//
// So are each file's refinement literals and declared rules
// ([KeptRefinement], KeptUnit.Rules): a literal's value and the
// `try_from` of each step of its chain by linkage name, and a literal's
// Build and each generic step's Check, the functions that make its value
// and call a generic subtype's `try_from` at its type arguments, in an
// object of its own (KeptUnit.Builds). A declared generic rule's Check is
// not kept: its Instances prefix finds the instance it calls in the kept
// objects. A lowering of one
// file answers every unit's, the kept units' decoded and the file's from
// its new lowering, so vmhost evaluates each rule again over the linked
// modules: an edit of a rule, or of a function a rule calls, can break a
// literal in a file that was not lowered again.
//
// A body that renders values held as an interface through Debug
// (existentialDebugImpls: a derived Debug over a `List<Display>` field)
// builds a Render that names the Debug impl of every type any file of the
// program implements or boxes into the interface, files the renderer's
// check never reads among them. The kept lowering records each such
// interface, by spelling, with the files that render it and the files
// that implement it ([KeptDebugRendering]). A file that renders one
// another file implements misses, since its own lowering cannot name that
// file's impls; so does a file that implements one another unit renders,
// since that unit's kept object names the file's implementers as they
// were. TestLowerFileMissesARenderingOfAnotherFilesImplementers holds it.
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
// every call, function value and other file's call names an instance.
//
// An instance is built at the type arguments the checker instantiated the
// call at, and nothing here solves them. `calleeTypeArgs` reads them off
// the reference at the callee's name (analysis.CallTypeArgs: the
// instantiated signature matched against the declared one, and the
// recorded substitution for a parameter only a `where` bound names), in
// this gen's kinds. It serves a generic `fn` of this file (`monoInstanceAt`)
// or another (`siblingGenericInstance`, its kinds imported into the
// owner's), a function used as a value, a generic impl function
// (`genericMethodItem`, `siblingMethodInstance`), an impl block's
// instance named by a type-qualified call (`templateCallInstance`), a
// nested generic `fn` and a std template (`stdInstTypeArgs`). A struct or
// variant construction is built at the instance the checker typed it as
// (`literalTypeArgs`). An impl a receiver kind selects (`impl Display for
// List<T>` at `List<Int>`) is the one instance of the block that receiver
// is, read off its kind (`stdRecvTypeArgs`). The instance of a generic
// interface an impl block implements is its header's written arguments,
// else the ones the type builder solved from the block's items and
// recorded on it (`ifaceInstArgs`, analysis.ImplBlockTypeArgs). Among a
// type's impls of one generic interface at several type arguments, a call
// takes the one the checker selected (DispatchImpl), else the block at the
// arguments of the interface instance the call goes through
// (`callInterfaceArgs`, analysis Symbol.CallInterface), in `pickInstImpl`,
// `siblingInstPick` and `stdRecvPick`; nothing chooses by operand kinds or
// by the result. A derived or expanded body is
// no exception: the checker records its calls and expressions as it
// records written ones (the analysis package doc, "Elaboration").
//
// While an instance is lowered its type parameters are a frame on
// `g.genericSubst` (`substFrame`, entered by `pushSubst`), keyed by name,
// innermost frame first. A frame binds each parameter to a kind
// (`genericSubstKind`) and, where the entry that made the instance read
// one, to the checker's type it stands for (`genericSubstType`). The
// checker's type comes from the same record the kind is projected from:
// `calleeTypedArgs` for a generic `fn` of this file or another, a generic
// impl function, a nested generic `fn`, the receiver of a type-qualified
// call's instance and a std template called by name; `literalTypeArgs`,
// `projectGenericInstance` and `templateInstanceOf` for a generic type's
// instance; analysis.ImplBlockTypeArgs for the generic interface an impl
// block implements (`ifaceInstArgs`), which the std generic interface a
// program's block binds into (`stdProgInst`) and a generic interface a
// solved type names (`projectIfaceInst`) carry too; the call's
// `calleeTypedArgs` for a generic interface's parameter default
// (`ifaceDefaultFrameAt`, `fnSig.ifaceTypes`). An entry that reads a kind
// off something the checker typed reads the type at the same place: an
// operand's type in ExprTypes (`operandCheckerType`) for a std container
// impl a receiver selects, a std interface default's `self` and the value
// `io.print` or `dbg` renders; a rendering's Type for a hole; the call's
// `For` for a default's receiver no operand holds; the enclosing frame's
// type for `T.method(...)` (`stdKindQualCall`'s `kt`). An instance an
// annotation spells (`b: Box<Int>`, a field's `Pair<String, Int>`, a
// generic refinement's `Many<Int>`) reads the types the checker recorded
// for the annotation's argument type expressions
// (analysis.FileAnalysis.TypeExprTypes, `typeExprTypes`), and the impls
// of that instance carry them. Which of an impl
// block's parameters a receiver's type binds is the checker's match of
// the block's recorded receiver against it (analysis.ImplBlockArgsAt), for
// a std container's block (`stdRecvTypes`) and a program's block over one
// (`stdRecvImplAt`). A recorded type that names an
// enclosing instance's type parameters has that instance's types put in
// (`checkerTypeArg`, over analysis.ReplaceTypeParams). An instance is
// interned by its kinds, so it carries the types of the first construction
// that had them, and a generic type's instance adopts them from a later
// one. What has none: a generic typealias's target at a use's arguments,
// an argument written as a default in the declaring file, and an instance
// asked for at a kind with no
// expression behind it (a sort's comparator over an element kind, a
// runtime Debug renderer's nested parts, a box's or a std interface
// value's dispatch entry, a part of a checked result), with what those
// reach. The builder never builds a checker type from a kind. A call's and an operator's record is read at these
// types inside the instance ("Qualified calls from their records",
// "Operators from their records"). In a test binary, or
// with NOMI_SUBST_VERIFY=1, each is held to its kind (`verifySubst`: the
// type projected, in a std instance's gen through the program gen that
// asked for it, is the frame's kind, or the same std interface held
// through its other def, `sameStdIfaceHeld`), and
// TestIRGolden_RecordsMatchTheVM counts the parameters entered without one
// by entry (`maxSubstWithoutCheckerTypes`), a count that only goes down.
// The kind of a written type is the projection of the type the checker
// recorded for that type expression (`typeOf`, over `recordedTypeExpr`,
// which reads srcFA's TypeExprTypes, or the types of the universal Debug
// the builder writes for a block-local declaration, `synthTypeExprs`), and
// `project` is the builder's one conversion of a checker type to a kind
// (TestProjectNamesEveryTypeKind holds it to every kind of
// analysis.Type). A pattern's kind is the projection of its record in
// PatternTypes (`patternKind`), and the variant a pattern's head names is
// its record in PatternVariants (`patternVariant`, or `synthPatternVariants`
// for the block-local universal Debug): a destructuring parameter, a `case`
// arm, a binding's `else` and a distinct sub-pattern look no head up by
// name. A sibling's or std module's type the projection reaches is found by
// its declaration node (`siblingOwner`, `siblingIfaceOwner`, resolving the
// name in the declaring file's scope), never from that file's tables,
// which hold it only once its gen has built its types. A sibling's
// signature is lowered under its file's names (`lowerNamesFrom`) so its
// records are found. What still resolves a written name: sigreason.go's
// refusal walk, which names why a type has no kind; the construction sites
// that find a def by a spelled qualifier (`namedType`, the struct literal's
// qualified head); and the std signature resolvers (`stdTypeKind`), which
// read a shared std function's signature in its module's anchors.
//
// Interface calls on existentials dispatch at run time. An interface-typed
// parameter, field or collection element erases its concrete type (spec
// §4). Values carry their type identity, and the call selects the
// implementation by the receiver's identity. What a program may do with a
// value of interface type is spec §12 *Interface values*. A value held as an
// interface conjunction (`Priced and Costed`, spec §12 *Interface
// conjunctions*) is the same concrete value or box: its kind is the members'
// existentials interned as one (irConjKind), a call through a member
// relabels the receiver to that member's existential and dispatches as
// above, and widening to fewer members is a relabel (conjunction.go).
//
// # Std interfaces held as values
//
// A std interface held as a value is lowered as a program's own is
// (stdprogiface.go). Each file that names one builds a def from std's
// declaration, and a generic one is instantiated per type arguments as a
// program template is. The signatures are std's source: while they
// resolve, `srcFA` returns std's file, so a position-keyed lookup
// (`Maybe<String>` in `Matcher.find_in`) reads what the checker recorded
// there, and a type expression's kind is the projection of the type std's
// checker recorded for it (`typeOf`), so `ToJson`'s `Json` needs no import
// and stays std's in a file that declares its own `Json`. A file that binds a prelude enum's name to its own type (`struct
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
// `==` over a declared type reads the checker's record of the operator
// (analysis.FileAnalysis.Operators) through `equatableVerdict`
// (irequality.go): the impl it calls, the block of the recorded function
// (`recordedImplItem`; a derived impl of an enum or a distinct compares by
// shape, its body), or a comparison by shape. An
// operator over a type parameter has only its bound recorded, and
// `instanceEquatable` resolves it at the instance's kind by the lookups
// below.
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
// type-qualified call, the dispatch table and a function value all name it
// as they name a written one. A block in the declaring
// file lowers the body as that function. A block in another file makes it
// one tail call of the declaring file's instance (`irImplForwardDefault`,
// ifacedefault.go), which the declaring gen queues and builds in its own
// scope (`flushIfaceDefaults`, late instances included).
//
// In the instance, a call of the interface's own function at the receiver,
// bare or qualified (`area(s)`, `Shape.describe(s)`), is the implementing
// block's function: its record is for the default's `self`, which the
// instance binds to the block's checker type, and is planned from the
// record there ("Qualified calls from their records"), so an `open`
// override the block writes is the one called. A `self(n)` pattern matches the checker's `self`,
// which `project` answers with the receiver (`gen.defaultSelf`), since the
// declaring file may have no name for it. The
// instance is keyed by the default's declaration, the receiver and its type
// arguments (the interface's, and a generic default's own, instantiated per
// call in the implementing block first, methodinst.go), and links by the
// interface function's linkage name with them
// (`shape.Shape.describe<Square>`).
//
// A std interface's default (`ToJson.to_json_field`, `FromJson.from_missing`)
// is a template over `self` instantiated per receiver in its declaring
// module (stdifacedefault.go). A call no written impl answers reaches it by
// the checker's reference, by the qualifier (`Int.from_missing()`), by the
// type argument (`FromJson.from_missing<List<Int>>()`) or by the bound type
// of an instance's `T.from_missing()` (`stdIfaceDefaultAtKind`), as in
// std/json's `decode_field<T>`, which a derived FromJson calls per field.
// A program block of a std interface inherits no item for such a default: its
// dispatch entry, and a std type's, is a thunk that makes the direct call
// (`stdProgThunk`, recorded by `irRecordImpls`).
//
// A call that leaves out a parameter default of an interface function
// takes the default of the function that runs: the impl function's when
// it declares one for the slot, else the interface's. A call on an
// interface value chooses it by dispatch (ifaceslotdispatch.go): each
// defaulted parameter of a dispatchable interface function has a dispatch
// symbol interned beside the function's (`irIfaceSlotDispatchSym`, linked
// as the function's linkage name with ` default N`), and as each impl
// function is lowered (`irImplSlotDispatch`, from `implFunc`, so a generic
// block's instance is under its frame) its accessor for each such slot is
// built: its own default's (`irImplOwnSlotRetain`) or the interface's at
// its kinds (`irIfaceSlotSym`). `irRecordImpls` and `irRecordBoxedImpls`
// record each as an entry beside the function's own, unretained where the
// function's is or the accessor did not lower. The call dispatches to the
// slot's symbol over the slots before it, so the receiver precedes the slot.
// A call from another file whose receiver's type is known calls the same
// accessor of the impl function it reaches directly (`implSlotHome`,
// through `implMemberSite.defaultHome` and `reachedImplHome`), and so does
// the supplier of a partial's open slot (`partialIfaceDefaults`), which
// dispatches on an interface value instead. A partial over an interface
// function carries none of the interface's literal defaults as its own
// open slots' defaults, since the impl that runs may replace them
// (`partialCall.literal`).
//
// A call in another file that leaves out a parameter default of an
// interface function (reaching a block's function or a named-argument call)
// fills it as a sibling file's `fn`
// default is (ifaceslotdefault.go): at the call under the declaring file's
// names (`lowerNamesFrom`) when every name it mentions resolves to the same
// declaration in both files (`defaultNamesAgree`), and otherwise through an
// accessor the declaring file builds on request, keyed by the interface
// function, the slot, the parameter kinds up to it and the type arguments,
// and linked by the function's linkage name with them and the slot. A
// generic interface's parameter default may name its type parameters (`x:
// List<T> = pick()`), so it is lowered in a frame binding the interface's
// and the function's type parameters at the type arguments the checker
// instantiated the call at (`ifaceDefaultFrameAt`, through
// `calleeTypeArgs`), which the call's signature carries to the accessor
// (`fnSig.ifaceFrame`). A call whose record lacks one declines.
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
// arguments (`stdUnretainedMonoBody`). A default of such a function
// (`fraction: ClosedRange<NonNegativeInt> = scale_digits(Decimal.scale(d))`
// in `Decimal.format`) is Nomi of the function's module, so the instance's
// gen builds the function's default accessors beside its body, and a call
// that leaves a parameter out is planned against them
// (`stdUnretainedShortCall`). They link by the names the shared lowering
// would give them (`Decimal.format default 3`), which it never defines for
// a body it does not retain: `emitStdSlotDefaults` builds only beside the
// body. The shared lowering
// builds no instance of a default even at a receiver only std declares
// (`Int.min`), so it never defines a name a program's own `Int.min` call
// defines too.
//
// Range's own functions are intrinsics (irrange.go): `contains`,
// `is_bounded` and its parts `start`, `stop` and `is_inclusive`, which the
// VM reads off the range's record with no element method, so a subtype
// rule generic over a Comparable element (`ClosedRange`, `BoundedRange`)
// lowers and runs as any body does, at compile time too. Range's and
// Iter's own protocols (`known_count`, `is_infinite`, `steps_between` as a
// Range's step count) keep their lowering in iter.go and irrange.go: the VM drives them with the impl's functions as values
// and answers an inherited default statically, and `Iter` is generic, which
// the template does not cover.
//
// # Empty collections and conversions
//
// An expression's kind is computed bottom-up as it is lowered, from its
// operands, its callee's signature and its declarations. An empty
// collection literal (`[]`, `Set[]`, `Vector[]`, `{}`) has nothing below it
// to say what it holds, so it takes the kind of the type the checker
// recorded for it (emptyLiteral, the expression's ExprTypes entry through
// `project`, under the frame of a generic instance). The checker records
// every empty literal it accepts and closes an element type nothing
// determines (internal/analysis's closed_vars.go), so the record always
// names a collection. `emptyOf` builds the empty value at that kind from the
// start; nothing retypes it later. A literal the record does not type takes
// the kind of the position it is lowered for, which lowerTypedOperand
// passes: a copy the builder made of a literal (`Query[]` over its list,
// `Kvs{}` over its map, `Json.Obj{}`'s anonymous map), which has no record,
// and a generic struct's field default (`items: List<T> = []`), recorded in
// terms of the struct's `T` and built for an instance. A std field's empty
// default and a bare `..` at a missing key are built at the field's kind.
//
// `convertTo` converts a value of one kind to the representation a
// position of another kind holds: a value of Infallible retyped
// (irnever.go), an embedded value widened into its `embeds` variant, a value
// boxed into an interface or a conjunction (irboxed.go, conjunction.go), a
// record whose fields enter by erasure, a relabel or a rebuild at wider type
// arguments (below), a function adapter (irfuncwiden.go) and an `Iter` view
// (iterview.go). Each is a conversion the checker records (Coercions,
// Conversions, IterViews), so the two kinds differ only where the program
// converts a value; where they are the same, `convertTo` answers the value
// unchanged. A caller passes the position's kind and the value's own; it
// needs none when the two are equal by construction.
//
// # Covariant conversion
//
// A value converts to the same type at other type arguments by each
// parameter's variance (spec §5 *Covariant conversion*): a std
// collection's, a tuple's and a record's parts and a function's result are
// covariant, a function's parameters contravariant, and a declared struct's
// or enum's parameters have the variance the checker reads off its fields
// (analysis.TypeParamVariances). What one element's conversion costs
// decides how the builder does it:
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
//     interface: a box per element (`irBoxPlan`), by the impl the checker's
//     record of the conversion names (analysis.FileAnalysis.Conversions,
//     `recordedImpl`), or the program's one impl for the element's kind.
//   - an embedded type into its enum: a record around it.
//   - a function that needs its argument or result converted another way:
//     an adapter closure (`funcWiden`), which converts each argument and
//     the result as `convertTo` does.
//   - a collection into an `Iter`: a view.
//
// A value whose every element converts with no work is relabelled: the
// builder returns it with one `ir.Copy` typed at the wider kind
// (`relabels`, `irCopyRelabel`, irrelabel.go), in `convertTo` before any
// rebuild is tried and in `qualSignature`, so a std or sibling function's
// argument passes as it is. Where the checker recorded the conversion, its
// record decides (analysis.Conversion.Cost, `relabelsAt`): relabelled where
// it says free, rebuilt where it says rebuilt, and by the kinds only where
// a generic body's instances decide. The hover reads the same record
// (internal/hoverdoc/conversions.go), so it says what the builder does; a
// record that says free where the kinds cannot relabel is a decline, never
// a silent rebuild. [ir.ValType.Accepts] accepts the copy: two
// instances of Maybe or of Result (`sameCovariantEnum`), and a function
// parameter either way round. A relabelled record may hold an `Int` in a
// word slot where its new type says a reference, so the VM reads a field
// through the record's own descriptor (`frame.writeField`, `opProjF`,
// `opProjP`), and equality and hashing read each slot by its own layout
// (rt's `Keys.recordsEqual`, `hashSlot`). TestRelabel_ConvertsNoElement
// counts it: converting a list of 10,000 Ints, structs, functions or lists,
// or a map of 10,000 entries, enters one function body and returns the
// very list it was given, where `Iter.map(xs, keep) |> Iter.to_list()`
// enters 10,001.
//
// A std operation the builder lowers itself or crosses to the host
// (`List.concat`, `Vector.push`, `Set.union`, `Map.put`,
// `Maybe.with_default`, `Iter.concat`) types its operands at the checker's
// instantiation of the call (`recordedTypeArg`, `solvedParamKind`), not at
// its first operand's kind, and a std body instantiated at its receiver
// (`Map.merge_with`) takes the receiver at that instantiation. So an
// operand whose type parameter a later operand widened (spec §4 *Chains
// and conversion*: `List.concat(circles, shapes)` is a `List<Shape>`)
// converts as a user function's argument does.
//
// Any other value is rebuilt (`rebuildWidened`, irembedsrebuild.go): a
// List, Vector, Set or Map through `Iter.map`'s operation with an element
// adapter and the matching materializer, so a Set's elements and a Map's
// keys are hashed again (a box hashes and compares by its static type and
// then that type's equality, so the rebuilt collection has the size it
// had); a tuple, record or struct made again from its converted fields; a
// generic enum (Maybe, Result, a user enum) through an adapter that
// matches each variant. A declared generic type is never relabelled, even
// where each element would be: its instance's descriptor names its type
// arguments ([rt.TypeDesc].Inst, `main.Box<Int>`), and a dispatch on an
// erased instance (the VM's `implOf`) and an erased Display rendering
// select by that name, so a relabelled `Box<Int>` held as `Box<Display>`
// would reach `Box<Int>`'s impls. The rebuilt value is a `Box<Display>`.
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
// # What an in-place update reads from the graph
//
// The VM writes a struct update into its base, and a collection operation
// into its operand, when ir.Owns finds that nothing reads the old value
// again (ir package doc, "Updates written in place"). The analysis sees
// only the graph, so the builder owes it a graph in which every holder of
// a value is a temporary an instruction reads: a binding is a Bind of the
// value's temporary and a later read uses that temporary, a branch's
// result and an inlined loop's state are Copies into a slot, a lambda's
// captures are its closure's operands, and a deferred call's operands are
// the Defer's. A parameter is read by a RefLocal of its symbol, which the
// analysis takes as a copy and a use of the parameter's temporary, so
// every read of a parameter must be one. A lowering that kept a value
// where the graph does not name it, read later by a symbol or by the
// consumer beside the graph, would let an update write over a value still
// held. A RefLocal naming a Bind's symbol is refused for that reason,
// since the analysis cannot place the read. `Iter.to_vector`, `to_map` and
// `to_set` and the collection literals count as new values, so their
// lowering must build one. A field read is a Proj of the value's
// temporary, never a second read of the record by another route, and an
// update names the fields it replaces, since the analysis lets a field
// read own its value only when nothing reads that field again. A fold's
// callback is the FuncValue its Iter reads, written once, and the fold's
// accumulator is the lambda's first parameter: the VM hands the
// accumulator to that body. TestInPlaceUpdates_Eligibility lowers a fixture
// of each shape (a literal's chain of updates, a parameter, a value handed
// to a call and returned, a capture, a call, a list, a channel, a branch, a
// loop's state, a loop of calls) and checks each update's verdict.
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
//
// # Subtypes and validated types
//
// A subtype (spec §4 *Subtypes*; refinement.go),
// `subtype Port Int when ...`, introduces a type the checker keeps apart, and no
// representation: a Port is an Int at run time. So the builder has no def
// for it, as it has none for a `typealias`: an annotation naming it
// (refinementKind) and a type the checker solved to it (project's
// DistinctType arm) are its base's kind, every conversion up to a parent or
// ancestor costs nothing, and a literal checked as a Port is the Int
// constant. Its call form `Port(8080)`, its `unchecked` and its
// destructuring `Port(n) = port` are the value itself. A subtype
// implements no interface (the checker refuses an impl), so no dispatch
// table can confuse it with its base.
//
// Its generated functions, `try_from` and `unchecked`
// (analysis/refinement_synthesis.go), are an inherent block with no
// receiver kind of its own, an owner block reached by the subtype's name
// as an interface's inherent block is (ownerimpl.go). `try_from` calls its
// rule through the VM intrinsic `vm.refinement_rule` (ast.RefinementRule),
// which answers False when the rule traps, so a value whose rule traps is
// None; under vm.Machine.Evaluate the trap goes through, and vmhost reports
// it at the literal. A parent named through a typealias is called by the
// target's name where the file sees it and otherwise by the declaration
// the checker resolved the call to (qualOwner, resolvedOwnerPlan). A validated type, or a `type` over a subtype, has a
// def, and its `try_from` is its TryFrom impl's; a generic one takes the
// base at its impl's parameters and is built at the
// type arguments the checker solved for the generated `unchecked` node.
//
// `struct Span ... when ... end` is an ordinary struct; only its
// construction is checked. Its `unchecked` builds it from the record of its
// fields, as the call form `Span({...})` does.
//
// Each literal the checker typed as a subtype
// (analysis.FileAnalysis.RefinedLiterals) becomes a RefinementSite: the
// literal, and the `try_from` of each subtype whose rule it must
// satisfy (analysis.RefinementChecks) by linkage name (implFuncLink: a
// shared std module's by the function's declaration, a listed one's and
// a program's by its block, with an interface impl's written arguments,
// `impl TryFrom<base(Email)> for Email`'s) with the path of parents to
// it, which vmhost calls on the literal before the program runs and names
// in its diagnostic. A literal whose value is no Go constant, a struct, a
// collection, a typed literal (`Duration"15m"`), a Decimal, a codepoint,
// or any of these grouped or negated, has a Build instead, a function of
// no arguments that makes the value as its literal lowers
// (refinementBuild). The builds run in source order: a typed literal's
// build declares its cell in the unit's module, so the module's cells and
// functions, and the std bundle written from them, follow that order.
//
// Every recorded literal becomes a site, and so does every declared rule.
// One whose check the builder cannot make, a Build or a generic check
// that does not lower or a `try_from` it cannot name, keeps its site with
// Unchecked saying why, and vmhost reports it at the literal (or the
// `when`) as an internal error, never a literal let through:
// PlantMissingRefinementCheckForTest makes the builder miss one, so a
// test holds that.
//
// A generic subtype is its base at its type arguments: an annotation
// `NonEmptyList<Int>` is List<Int>'s kind (project's subtype arm, over the
// checker's type for the annotation), and so is a type the checker solved
// to one. In a std
// signature, `BoundedRange<Int>` in `Generator.int`, it is the base's
// signature kind at the written arguments (stdGenericRefinementKind),
// which the shared lowering retains as it does a body over the base. A
// literal of one
// is checked by a function of no arguments that builds the literal and
// calls the subtype's `try_from` instance at the literal's type
// arguments, the call the checker typed (RefinementSiteStep.Check,
// refinementCheckBuild), its owner the declaration the checker recorded
// where the file binds the subtype's name to nothing or to another type
// (qualOwner: resolvedOwnerPlan, or its std module); the sites are built before the late instances
// other files asked for are flushed, so an instance such a call asks
// another file for is built. Its declaration's RefinementRuleSite has a
// function of the base's value calling its `try_from` at each set of type
// arguments the checker typed (analysis.RefinementRuleCheck,
// refinementRuleCheckBuild), which vmhost never runs and only reads for
// effects; each lowers its instance into the unit's module. The site also
// names the prefix of every instance's linkage name and the interfaces of
// the bounds, so vmhost checks every instance and every impl of them the
// linked program has (ImplLinkInterface reads an impl function's interface
// from its linkage name), which the kept lowering keeps.
//
// A std signature that names a subtype, `Int.pow(n: Int, exp:
// NonNegativeInt)`, has its base's kind (stdRefinementKind), a scalar, a
// Decimal or a type std declares, and its host adapter takes the base's
// value (internal/hostgen's shape of a subtype is its parent's). A
// subtype of any other base, a collection or range at fixed arguments
// (`Ratios`, `List<Int>`; `FiniteRange`, `Range<Float>`), a struct or an
// enum std declares (`FieldBits`, `JsonObject`), has the kind of its first
// parent as its declaration writes it.
package irbuild
