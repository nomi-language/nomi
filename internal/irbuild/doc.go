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
// namespace key, not a compiled artifact. A gen compares it with a std
// function's `pkg` to tell a function of its own std module from another
// module's (irstdmethodcall.go, irsupervisor.go, recordedimpl.go), which
// only a std module's gen can match: a user file's name never equals a std
// function's. For user files the partition serves the type groups of
// typecycle.go (below). Nomi permits cyclic file
// imports, and the analyzer resolves names whole-program, so import order
// never matters.
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
// The std index (`stdlibIndex`) files each std function under its
// identity, the analysis.StdFunc a call's record carries (`byID`): a
// module-level function and an impl's, of every module. A record reaches
// std's function there and nowhere else (`stdFuncByID`). An impl's
// function is also filed under the interface function it implements, by
// the identity of the interface the checker recorded for its block
// (analysis.ImplBlockTypeArgs), and the receiver's kind (`byIface`, read
// through `stdIfaceImpls`): the std function of an interface's function
// for a receiver, which a use that names the interface by its record or
// by std's own identity (`stdDebug`) asks. The index also files functions
// under `Owner.name` (`byType`), for the routes that choose a container's
// generic template by its kind (`stdGenericTemplate`) and for the builder's
// own `Struct.update`. A std module's own gen
// reads an index of its own functions laid over the earlier modules'
// (`bindStdSiblings`, `underlay`). Under one spelling its own entries hide
// an earlier module's (std/calendar's `Error.inspect` hides std/locale's);
// an identity carries its module, so in `byID` and `byIface` nothing
// hides.
//
// A generic std function is lowered only as the instances programs ask
// for (stdinst.go). `StdAbstractBodies` asks for one more of each generic
// function a shared module declares, its abstract body (abstract.go): the
// instance at its own type parameters, left open and held as their
// bounds, through the same instance table a program's calls use, from the
// module's own gen. Its instances are linked into no program: the effects
// walk reads them for what a body does whatever its type arguments are
// (vmhost.Program.StdEffects). One whose closure declines is left out, its
// declines noted nowhere. A function every call of which a family arm
// lowers to an operation of the machine (an Iter stage, a Range or Set
// function; `StdLowersToMachine`, machineop.go) has a body no program runs,
// and the effects walk answers it by the operation instead.
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
// # Std spec rows
//
// A std type whose Go type rt declares (a record struct, an enum, a
// distinct over a scalar, a host type, an interface with an rt table) has a
// spec row: what the builder believes std declares, and the layout it
// builds the type's one process-wide def with. A row is the contract
// between std's declaration and rt's Go type, and it is held to each side
// by its own check. Against std's source, a row's `matches` (a struct
// row's `mismatch`) reads std's declaration: its names, order, clauses and
// defaults, and every type it writes (a field's, a payload's, an opaque
// type's inner, an interface method's parameters and result) by its
// spelling, compared as typeText renders it with the row's own. A row
// states that spelling where it is not the kind's own name (`decl`: a
// subtype `month: Int64`, a container, a std newtype) and derives it for
// the five scalars (kind.nomi). No validator resolves a type, so a row
// needs no other row admitted first, two rows that name each other are
// each checked on their own, and an interface's anchor does not depend on
// what else the anchoring module imports. A row that fails produces no
// anchor, and every mention refuses through the ordinary paths;
// TestStdStructSpecsMatchStdSource names a struct row's field and both
// spellings. What a spelling stands for is held by
// TestStdSpecSpellingsAreTheirKinds: the checker's type for each annotation
// a row covers, projected with no gen, is the row's kind. Against rt, each
// family's kinds are held to its Go types by reflection
// (TestStdStructGoWidthMatchesTheDeclaredField and its siblings).
//
// A struct, enum or distinct type a shared std module declares in Nomi with
// no row has one process-wide def too, built from its declaration by the
// code that builds a program file's (stdtypes.go): a gen of the module's own
// runs buildTypes over the module's nodes once per process, and admits each
// def that lowers and holds only package-neutral kinds. The module's shared
// gen adopts those defs (`adoptedStdDef`) as it adopts a row's, and every
// gen's projection answers them by the declaration the checker's (origin,
// name) names (`localNamed`, `stdDeclaredDef`), so `Json.Case` is one
// pointer in std/json's signatures and in every program, spelled
// `json.Json.Case` at run time (stdSpecOf). A generic declaration and a
// type that holds an interface value have none, and a literal outside its
// module that omits a defaulted field of one declines, since the default is
// std's source (fieldDefault).
//
// # What a name names
//
// What an identifier the builder lowers names is the checker's reference
// at it (analysis.FileAnalysis.References, in the file whose source is
// lowered, `srcFA`), read in one place, `identRef`. A qualifier names a
// file when the reference is a module (`qualifierScope`, which
// `lookupQualifier` and `stdFileQualifier` read), and a qualifier or
// callee names a value of the body when the reference is a parameter, a
// binding or a function declared in a body (`namesLocal`). So a binding
// named like an import is the binding while it is in scope: over a local
// `io`, `io.print(s)` calls the local's field, and std/io's `print` again
// once the binding's block ends.
//
// An identifier the builder writes itself (a hole's name, a partial
// application's operands) goes through `madeIdent`, which marks it as
// code the checker never checked
// (`markUnchecked`). It often stands at a written node's position, where
// the reference names that node's symbol, so `identRef` never reads one
// for it. One that reads a local the builder declares carries that
// local's key (`madeRead`); any other names no local, and only a qualifier
// among them is resolved by its spelling, in the module scope
// (`scopeOfRef`).
//
// A declaration is found by the one the checker recorded: a call's or a
// reference's callee (analysis.Callee, `stdFuncAtRef`), a type's identity
// (`solvedIface`, `ifaceOfType`, `stdSharedIface`), an impl block's
// interface (`implBlockRecord`) and receiver type (`recordedTypeExpr`), a
// bound's type (`boundIfaces`), and a type name in a declaration by the
// reference at it (`typeRefAt`, the type-cycle walk). The builder resolves
// no name the program wrote by its spelling. A type identity (module,
// name) names its declaration in the declaring module's own scope
// (`declNode`, `qualifierDecl`, `stdProgIfaceAt`), which is what the
// identity means. Code of another file the builder lowers here (a
// sibling's generic interface signature, a parameter default) is read
// from that file's records (`lowerNamesFrom`, `srcFA`), so it means what it
// means there whatever this file binds its names to; a sibling interface
// function's parameter default is lowered by the declaring file, through
// its accessor, since it may name what only that file can. The builder
// also looks up names it makes itself: a qualifier it writes (`scopeOfRef`)
// and a container's generic std template by the base name of the kind it
// holds (`stdGenericTemplate`), since a container kind names no
// declaration. TestSpellingResolutionsOnlyGoDown and
// TestNameLookupsOnlyGoDown count the places that read a name, and list
// apart (`builderSpellings`, `notResolutions`) the ones that resolve none:
// an index built from declarations under their own names, a declaration's
// own entry read back and checked against it, a type identity's
// declaration, and a name the builder makes.
//
// Each body holds its locals by key, and a read finds its value by its
// key (`readKey`, `identRead`). A written read's key is the position of
// the declaration the checker resolved it to, when that declaration is a
// body's (`bodyBinding`): `declareLocal` declares a binding, a pattern's
// name, a lambda's parameter, a capture or a nested fn there, and the
// shell's `declared` holds the function's own parameters, each with its
// declaration identity (`localAt`). A callee's default lowered at a call
// site declares the callee's parameters there, each holding the placed
// argument, and a recursive nested fn's body declares the fn as the
// closure's own capture parameter. A local the builder declares, which no
// source declares, takes a fresh key below every source line
// (`madeLocal`): a pipe's held value, a test stage's, an `in` test's
// operand, a named Iter.loop step and its state, a partial's entry, a
// wrapper's parameters, a generic nested fn's instance, a pattern binding
// the builder names (`patternBinding` answers the key it declared under),
// and the parameters of a lambda the builder makes (`madeLambda`,
// `paramKey`), whose parameters stand at one written node's position. A
// refinement's literal is declared at its check's argument, where the
// checker's symbol stands. The table is never restored at a scope's end,
// since two declarations never share a key, and is swapped for an empty
// one where a callee's or a field's default is lowered inside the caller.
//
// A read the body holds no local for inside a closure is captured from the
// enclosing body (`capture`), once per closure for each local key, or, for
// what no body binds (a function read as a value), per declaration
// (`captureKey`). Nothing else finds a local: the builder keeps no table of
// names, and a scope's end restores nothing, since a read can only find the
// declaration the checker resolved it to. So the checker's References are
// the one source of what a read names, and a written read with no
// reference, or of a local no body holds, fails the lowering
// (`readWithoutKey`), in every binary. A local's name is data: its debug
// variable carries it (`declareVar`, `declareBind`, `declareCapture`), and
// each declaration has an `ir.Symbol` of its own, a parameter's held with
// its local (`irLocal.sym`) for the reads that name it.
//
// A function value the program writes also keeps the app fields in force
// where it is made (spec §15 *Closures and tasks*), and its `ir.FuncValue`
// or `ir.RefFunc` says so (`KeepFields`): a lambda, a capture, a partial or
// a nested fn (lambdaFunction), and a named function, an owner's function,
// a std function or a field accessor read as a value. A function value the
// builder makes to call the program's impl functions on a caller's behalf
// (an Iter view's `each_while` and `known_count`, a comparator, a Range's
// `compare` and `next`, a source view, an adapter) keeps none, since those
// calls are the caller's.
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
// the stage's record, in which the head fills a slot. A bare `if` or
// `case` stage is lowered as the stage itself, the head standing for its
// condition or scrutinee (irpipe.go, `subjectOf`), so it reads every
// record the checker keyed by the stage: its type, which the checker
// records for the stage as for a written region, and whether it always
// exits.
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
// record's, is the builder's own error and panics. A named call's
// stand-in holds its written operand already evaluated, and the records
// the checker keyed by that operand, the interface value it becomes and
// its conversion, are read at the operand (`recordNode`).
//
// The record also classifies the callee (analysis.Callee): the
// declaration the call reaches, by the checker's symbol and node, and
// whether one of the program's files declares it. Five routes read it: a
// qualified call is planned from the recorded declaration (below,
// "Qualified calls from their records"), a call of a std function the VM
// implements itself reaches `call`'s arm for it from the std function the
// record names (`intrinsicArm` over `stdIntrinsicOwners`: Task's, Channel's,
// Sender's, Receiver's and Arm's functions, `Supervisor.spawn` and
// `Reader.from_chunks`), so a program's own `Task` and an owner imported
// under another name are told apart by identity, a partial application over a
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
// instance is built from (`stdInstCallAt`).
//
// # Qualified calls from their records
//
// A call qualified with a type, an interface or a type parameter is one
// route, `typeQualCall`, whatever the qualifier's spelling: `Duration.x`,
// an alias (`Doodad.make` for `Gadget`), a type through its module
// (`net.Error.of_tls`, `json.Json.parse`) or a directory path
// (`db::schema.Post.blank`), and a bare call of a function declared under
// a type or an interface (`bareImplCall`, `bareStdMethodCall`). What the
// call is qualified with is its record's (analysis.Callee.RouteOwner):
// `qualCallLowered` sends a call there by the record's kind and reads
// nothing of the qualifier, and a chain of fields is an owner rather than
// a value read by the chain where the record says so
// (`qualCalleeIsValue`), so a name this file binds to another
// declaration of the same spelling (a local `struct Duration` beside a
// block's `import std::duration.Duration`) never captures the call. The
// owner's name is only the plan's label (`qualOwnerName`): every plan
// interns its callee by the declaration it calls, so one function has one
// symbol however its calls spell it. A qualified call with no record of
// its qualifier declines, which no checked program reaches
// (TestElaborationGate).
//
// A qualified call `Owner.method(args)` whose record names the function it
// reaches (analysis.Callee.Decl: a `fn`, a `host fn`, or the
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
// (`recordedWithheldPlan`). A program's block over a std container is
// registered at the container kind an operand is before the lookup
// (`stdRecvRecordedAt`, the on-demand registration `stdRecvImplAt`), and
// one called with no operand of the container (`List.from_sql(value)`) at
// the instance the record is for (`Callee.For`); `stdRecvOwnerPlan`, which
// `typeQualCall` tries before the container routes, is that plan. A std
// function is the one of its identity (`stdFuncByID`), however the call
// names its owner; only `stdIfacePlan` still picks among
// std's overloads by the operands' kinds (`stdPick`). A std function a program
// instantiates for itself (a generic one, or one whose body std does not
// retain: `Generator.bool()`, `Maybe.hash(x)`) is instantiated from the
// function the record names (`stdGenericQualCall` over
// `recordedStdTemplate`), as a module's function is (`stdGenericFileCall`);
// a call its record qualifies with an interface that reaches an impl's
// function (`Debug.inspect(xs)` over a List) is left to the renderers
// after it, and one qualified with a type parameter is the impl's function
// the record names at the instance (`T.mean(xs)` at a Decimal).
// The plan is built by the code each kind of function already had:
// `implItemPlan` for this gen's item, `siblingInstanceItemPlan`
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
// form. `T.name(x)`, whose record's owner is a type parameter
// (Qualifier QualTypeParam), is the call at the type the instance binds
// it to (`stdKindQualCall`): at a declared type it is planned from that
// record (`kindImplCall`), with the instance carried beside the call
// when no operand is of it (`instCall`); at a scalar it is the call as
// written, which the routes after plan from the same record at the
// instance (the template, the default, the function it names) and route
// as a call qualified with the scalar (`recordedQualRoute`), where a machine
// route renders the scalar itself (`displayCall`). A turbofish's or a
// solved result's scalar type argument (`FromJson.from_json<A>(j)` at an
// Int) is the same: the call as written, planned from its record. Under
// substVerify,
// `verifyCalleeAt` holds a call a later route plans to the record at the
// instance.
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
// dispatch.
// `verifyDispatched` holds a dispatch plan to a record with no Impl.
//
// The routes after it choose by the receiver's kind or a name-keyed table,
// and plan a static call. They serve calls the record leaves to dispatch
// whose receiver the builder holds as a value of a known type: a value
// written `as` an interface, which the builder holds as the value it is
// although the checker records the interface (the impl index in
// `concreteInterfacePlan`). A REPL input's derive for a type an earlier
// input declared is the type's one impl in the record, written at the type
// or at an instance (analysis.ImplPrecedence ranks it above the name-only
// Debug an opaque type's file holds), so the first route plans its calls. An
// interface's default lowered for one implementor, and each of its
// parameter defaults, is lowered with the implementor's checker type
// bound to `self` (`implDef.recvType`; at a call, the type the call's
// record is for, `fnSig.defaultSelfType`), so a call on `self` there is
// planned from the record at that type.
// A receiver that implements a generic interface at several type
// arguments is resolved by the call's interface instance
// (Symbol.CallInterface), which the record carries. A call of a std
// interface's function the builder writes (a tuple slot's
// `Hashable.hash`, a record field's `ToJson.to_json`, `to_json_field`,
// `F.from_json` and `Parse.parse` (parseCall, planned from the function
// the record names), `debugOver`'s `Debug.inspect`, a rendering's
// `Display.to_string`, a std interface value's dispatch entry,
// `stdProgThunk`) carries what the checker records for that call written
// at the value's type (`stdIfaceCallRecord`,
// analysis.FileAnalysis.InterfaceCallee): the type a value entering an
// interface has is the coercion's record of it, else the type the checker
// recorded for the expression (`valueCheckerType`), and a collection
// relabelled as one of an interface passes each element's down
// (`relabels`). One on a value held by its kind alone (an instance a kind
// walk asks for, `checkSubstCensus`) names the interface's function and no
// type, and is planned by the routes after. Either way the record names
// the std interface, and the routes that would read the interface off the
// call's spelling read it off the record instead (`recordedStdIface`):
// where this file declares an interface of the same name, the spelling
// reaches that declaration, which is then not the call's. A call qualified
// by a type parameter (`T.from_json(json)` in a std instance) goes through
// the interface its record names as the function's owner, its bound's, so
// a type with impls of std's FromJson and a program's reaches std's. The
// routes after serve calls whose record names no function (an
// interpolation hole rendered by kind, `Display.to_string`).
// `TestQualImplPlanRoutesOnlyGoDown` counts `qualImplPlan`'s routes. The
// call route census (`noteCallRoute`, callroutecensus.go) counts the
// distinct sites each route after the record's answers, and the sites
// `stdPick`'s choice among overloads answers, split into calls written in
// the source, written for a derive, and made by the builder;
// `maxCallRouteSites` holds each to a maximum that only goes down.
//
// The impl tables (`implsByIface`, `instImpls`, and the sibling sites'
// `implMemberSite.iface`) are keyed by the interface's identity, its
// declaring file's analyzer origin and its name (`ifaceKey`), never by its
// spelling, so impls of two interfaces of one name (two files' `Show`, a
// file's own `ToJson` and std's) for one type are two entries in one file
// and never a duplicate. The key is the same in every gen, so a lookup in
// another gen's tables (`callerImplPlan`, `implForDecl`, `mirroredImpl`,
// `instanceImplAnywhere`, `siblingImplSiteBy`) uses the key this gen
// computes. One function family produces it: `implsKey` for a def this
// gen holds (a local declaration, a sibling's mirror, a std interface's
// def or an instance of any of them), `stdIfaceImplKey` for a std
// interface the builder names (`==` reads Equatable's impls, a `for`
// Iter's), and `implIfaceKey` for an impl block, which takes its
// interface's def and, where the builder holds none (std's generic `Iter`
// in a std module), the interface the checker recorded for the block. Two
// impls of one interface declaration for one type are a duplicate the
// checker rejects; `resolveImplDef` refuses both if it ever sees them.
//
// An impl block's interface is the one the checker recorded for it
// (`implBlockRecord`, analysis.ImplBlockTypeArgs), however its header
// spells it: a program's interface, a sibling's through its mirror and a
// std module's lowered with the program by their declaration
// (`solvedIface`), a generic one's template instantiated at the block's
// type arguments (`genericTemplateDecl`), an operator interface by its
// spec (`operIfaceOf`), and std's own by its identity (`stdSharedIface`,
// `stdProgIfaceForImpl`). The name-only Debug the front end writes for an
// opaque type declared in a body (FileBodies.BlockDebug) has no record and
// is std's Debug by construction.
//
// # Operators from their records
//
// An operator (`a + b`, `a == b`, `a < b`, a prefix `~~~`) calls the
// function its record names (analysis.OperatorUse.Impl.Method), read once
// by `recordedOperatorImpl`: a program's impl, a derived one and one of a
// std module lowered with the program is the item the gens registered for
// that `fn` (`recordedImplItem`, over each gen's `implItemsByDecl`, which
// `resolveImplDef` fills for every impl block, a generic receiver's once
// per instance); std's own is the std index's function of the recorded
// identity (`stdFuncByID`). A
// qualified call reaches its function through the same two lookups
// (`recordedCallPlan`).
//
// An operator lowered as a call of an impl (an `equal` std's or one the
// record names in `implEquality`, any `compare` in `rankedCompare`) and
// a Set's, Map's or Vector's `+`
// (`stdGenericOperatorCall`) is the call its record means: the builder
// writes `Iface.method(a, b)` over the operands, carrying the record a
// call of the impl function the operator's record names has
// (`operatorCall`, analysis.FileAnalysis.ImplCallee, the record
// `displayImpl` carries for a rendering's impl), and a route that lowers
// a recorded call plans it: `recordedCallPlan` for a program's impl and
// std's, `containerCompareAny` for a List's or a Vector's compare, and
// `ifaceContainerCall` for a std container's generic `+`. The function
// a plan calls is held to that record as a written call's is
// (`verifyCalleeIn`).
//
// The record names std's `host fn` for `Int + Int` too, and std's `a ==
// b` for two Lists. Which impls the machine computes itself, and how, is
// one table keyed by the identity the record carries, `machineImpls`
// (machineimpl.go): std's function (analysis.StdFunc) natively (Int's,
// Float's, Decimal's and String's operators, Decimal's `==` among them,
// Bool's derived impls) or by shape (the Equatable of Unit, Byte, Bytes,
// Dynamic and the collections, and std's Debug of the scalars, the
// collections, Dynamic, Context, Unit, a channel's halves, an Arm and a
// Task, which the machine renders, and std's
// Display of Int, Float, Bool, True, False, String and Decimal, which
// `Display.to_string` renders as an interpolation hole does:
// `displayCall`), and a
// derived impl by its interface and the form of its type (a struct's, an
// enum's, a marker's or a distinct's Equatable, a marker's Debug and the
// Debug of a distinct over one scalar, by shape).
// List's `+` is native too: its impl calls `List.concat`, whose host
// operation `arithmetic` lowers. `machineRouteOf` reads it for an
// operator's record, by the identity of the impl in the parse of std the
// record was answered in (`operatorImplStd`: a std body's own, or at an
// instance the program's parse the instance's types are from), and an
// operator whose record names a row takes the row's route: `arithmetic`
// and `bitwiseNot` lower the machine's operation, `equality` and
// `ordering` compare natively or by shape, and a rendering renders the
// value (`debugByMachine`, below). A by-impl record of any
// other impl is a call of it. `TestMachineImplsAreComplete` requires a
// row for every std operator impl only the machine computes (a `host
// fn` no host table binds, a body that is its own operator, which a call
// of the impl from its own body would reach again), and
// `TestMachineImplsComputeTheirBodies` runs each row's operators, and
// each Debug row's rendering, beside a call of its body.
//
// `==` and `!=` are one switch on the record (`equality`, irequality.go),
// with one arm for each form: by dispatch, two interface values compared
// by their run-time types (ir.ValIface); by shape (OperatorStructural,
// and a by-impl record of an impl `machineImpls` computes by shape), an
// ir.Compare of the operands' shape or a comparison through their parts
// (`shapeEquality`); by an impl the machine computes natively, an
// ir.Compare of the scalar or the Decimal, or by shape over a value held
// as no scalar (True's or False's marker that `Bool.False(x)` binds); by
// any other impl, a call of the one the record names (`implEquality`:
// the program's item, else std's function planned as the operator's
// call). The kind chooses only the IR shape of a comparison by shape. A
// part the builder compares itself there, a tuple's slot or a distinct's
// inner value (`structuralEqual`), is compared by the checker's record of
// `Equatable.equal` at the part's checker type (`partEqualBody`, through
// `recordedIfaceFnOf`, the resolution a comparator and a nested Debug
// body read) by the same rule: an impl the operator would call is
// called, and a part the record answers by shape, by dispatch, by a
// `machineImpls` row (every derived impl but a std newtype's) or by std's
// own impl is compared by its shape, as rt.Equal compares a part inside a
// container, through the written bodies the key table names
// (`irRecordKeyImpls`, vm/keys.go). An `==` the builder writes
// for a call (`Maybe.equal(a, b)`, `Equatable.equal(a, b)`) carries the
// call's record (`carryUse`).
//
// `<`, `<=`, `>` and `>=` are one switch on the record too (`ordering`,
// ircompare.go): by an impl the machine computes natively (Int's,
// Float's and Bool's compare), an ir.Compare of the scalar; by shape, a
// tuple's lexicographic chain (`tupleRankedCompare`); by any other impl,
// a call of the compare the record names (`rankedCompare`) and a test of
// the rank of the Ordering it answers. An ordering by dispatch, over
// interface values, has no arm: one written over them, and a generic call
// that would instantiate one at an interface type, are checker errors
// (spec §12 *Interface values*). Only the instance a generic refinement's
// declaration check types at its bounds' interfaces reaches the switch,
// for the rule's effect walk, and declines.
//
// The arithmetic, bitwise and shift operators are one switch on the
// record (`arithmetic`, iruseroperator.go), and so is a prefix `~~~`
// (`bitwiseNot`): by an impl the machine computes natively, the machine's
// operation (`nativeArithmetic`: List's concatenation, ir.Concat,
// ir.Arith; ir.UnaryArith for Int's `~~~`); by any other impl, a call of
// the one the record names (`implArithmetic`: the program's item, std's
// function, or std's generic impl of a Set, a Map or a Vector at the
// receiver's element type; `bitwiseNotImpl`). No such operator is
// answered by shape or by dispatch. The Int arithmetic the builder writes
// for a tuple's hash carries Int's records (`carryOperatorUse`).
//
// A qualified call of an operator interface's function (`Equatable.equal(a,
// b)`, `Int.add(a, b)`, `Point.equal(p, q)`, `Hashable.hash(x)`,
// `Comparable.compare(a, b)`) is one switch on the call's record too
// (`interfaceCall`, ifacecall.go, which `typeQualCall` asks first), by the
// interface the function is declared for (analysis.FileAnalysis.
// CalleeInterfaceFunc) and the form the record answers it with
// (analysis.Callee.Form, at the instance's types: `calleeAt`): by an impl
// the machine computes, as the operator it is, with the call's record
// carried onto the operator (`operatorUseOfCall`, `carryUse`), so `a == b`
// and `Equatable.equal(a, b)` end in one arm of `equality` (the call is
// lowered as the written `==` is, operands and all, by `compare`, so
// `Maybe.equal(Some(circle), Some(shape))` widens its Circle into Shape
// as `==` does), and `Int.add(a, b)` in `arithmetic`'s; by any other
// impl, the call of it the record names, which the routes after the
// switch plan (`recordedCallPlan`); by shape, Equatable's as `==` by
// shape, Hashable's as a tuple's or a
// record's mix (`tupleHashCall`) or the hash a Map key of the type takes
// (`hashValue`), Comparable's as a tuple's lexicographic compare
// (`tupleCompareCall`); by dispatch, Equatable's as `==` over interface
// values, Hashable's as the Map key hash of the held value, or on a value
// held as std's Hashable itself, the dispatched call
// (`dispatchedCallPlan`). The calls of any other interface's function
// (Display, Debug, ToJson, a program's interface) take the routes after.
//
// Each family's switch (`familySwitches`: `equality`, `ordering`,
// `arithmetic`, `bitwiseNot`, `interfaceCall`) has, for every form the
// checker records (analysis.OperatorForms), an arm or the reason it has
// none, one table (`formArms`, operatorform.go), and
// TestEveryOperatorFormHasAnArm holds each switch to it: a form added to
// the checker fails there for every family until each says what it does
// with it. An operator or such a call with no record its switch reads
// declines: under substVerify `noteUnreadOperator` and `noteUnreadCall`
// count it by its family and by why (`unreadAnswers`, from `formArms`:
// written by the builder with no record carried onto it, unanswered, and
// each form the family has no arm for, a per-instance record its instance
// leaves unresolved among them), and TestIRGolden_RecordsMatchTheVM holds
// each class to `maxOperatorFormSites`, a count that only goes down. No
// operator, and no call of an operator interface's function, is lowered by
// an impl its operands' kinds select.
//
// An operator over a type parameter (OperatorPerInstance) is read inside
// an instance at the instance's types (`operatorUse` over
// `operatorUseAt`, analysis.FileAnalysis.OperatorUseAt): the record an
// operator written at those types has, std's `host fn` for a primitive
// included, an impl of several of a type's narrowed by the interface's
// type arguments there, or a structural comparison.
// `TestOperatorNameTestsOnlyGoDown` counts the tests of a name left in
// the operator-lowering files.
//
// An operation that orders values with no operator written (`Iter.sort`,
// `Iter.sort_by`'s key, a Range's walk, `contains` and `known_count`, a
// tuple's element, a List's or a Vector's compare, `containerCompare`)
// takes the element's `Comparable.compare` as a function value
// (`comparatorOperand`) or calls it (`elemOrdering`).
// `sortComparator` chooses it from the checker's record of
// `Comparable.compare` at the element's checker type
// (`recordedComparator`, over `stdIfaceCallRecord`): this file's impl,
// another unit's (a sibling file's impl, through `recordedItemPlan`; the
// instance of a std container's impl, `comparatorInstance`), std's own
// function by its identity (`stdFuncByID`), or for a tuple, answered
// structurally, the builder's lexicographic compare. A std `host fn`
// compare (Decimal's) becomes a value through a forwarding body
// (`irHostForward`). An element with no checker type where the
// comparator is asked for has no record to read, and its comparator is
// chosen by its kind. Under substVerify `noteElemImpl` counts, by
// distinct site, each comparator and each part's equality chosen with no
// checker type or with a record its instance leaves unresolved, and
// TestIRGolden_RecordsMatchTheVM holds the counts to `maxElemImplSites`,
// counts that only go down.
//
// A rendering of a container, a tuple or a record (`dbg`, `Debug.inspect`,
// `io.debug`) names the Debug body of each nested declared part for rt's
// renderer (`nestedDebugImpls`, ir.Render's DebugImpls): std's own impl of
// a std type, the instance of std's generic impl, a distinct's or a
// declared type's impl, and an `embeds` variant's value's. Each part has
// its checker type within the whole (`partCheckerType`, a prelude enum's
// payload by `preludePayloadType`, an `embeds` variant's by
// `embeddedVariantType`), and its body is the one the checker's record of
// `Debug.inspect` at that type names (`recordedDebugBody`, through
// `recordedIfaceFnAt`, the resolution the comparator reads too). The
// part's kind decides only how it renders: a container, a tuple and a
// prelude enum through their parts. A part whose record names an impl the
// machine renders (`machineImpls`: a scalar's, a collection's, Unit's, a
// derived marker's, a derived distinct's over one scalar, Dynamic's,
// Context's, a channel half's, an arm's and a task's) names no body of its
// own, and only its parts are walked (`debugByMachine`); so does a part
// whose Debug is its shape (`debugByShape`, below).
// A top-level rendering has one rule, `debugRender`, whatever its
// spelling: a `dbg` reads the record the checker made of it, the Debug use
// at its operand's type (analysis.FileAnalysis.Operators), and
// `Debug.inspect(x)` its own record; `io.inspect(x)` reads the record of
// `Debug.inspect` at its operand's type (`stdIfaceCallRecord`). By its
// form: where the impl it names has a row, rt renders the value by its
// shape; where it names any other impl, that body is called (a
// `Debug.inspect` of this unit's own body is planned from that function
// as any recorded call is, `recordedCallPlan`); by dispatch, an interface
// value renders through its implementors' bodies; by shape, a function
// value is its name-free constant and any other value renders by its
// shape. Only a use in an instance whose frame has no checker type for
// the operand, whose record stays per instance, renders by the operand's
// kind (`viaDebugNoRecord`). A body named for rt's renderer is only ever a
// nested part's, which has no call site of its own. `T.inspect(x)` for a
// type with no Debug block is a call of Debug's own function, which the
// record says by its owner (analysis.Callee.Owner), and renders as
// `Debug.inspect(x)` does (`inspectCall`).
//
// A struct, enum or distinct type with no written or derived Debug has no
// Debug body, declared at module level or in a body: the record of
// `Debug.inspect` at it answers structurally, rt renders it as the literal
// that builds it, and only the bodies its fields' and payloads' records
// name are named (`debugByShape`, `parts`). A part with no checker type has
// no record to ask, and its Debug is its shape when no unit of the program
// has a Debug block for its declaration (`debugBlockless`). Such a value
// held as std's Debug enters it as itself, as a tuple and an anonymous
// record do (`bindsImpl`, `irErases`), and renders through the bodies its
// parts name. An opaque type's and a host type's Debug is the name-only
// body the front end writes; an opaque one declared in a body has it
// written and checked in its block (analysis.FileBodies.BlockDebug), and
// its record names that body as a module-level type's names its block;
// where the record cannot read the block (a file lowered from its
// interface) it answers structurally and the body is found by the type's
// kind, as the body of a part with no checker type is. Two arms name a set of bodies no
// single record answers: a part held as an interface value, which renders
// through every implementor's body (`existentialDebugImpls`, an index
// query over the program's impls and boxes), and an `Iter<T>` part, which
// renders as any source it may view. Under substVerify `noteDebugImpl`
// counts each choice by distinct site, and TestIRGolden_RecordsMatchTheVM
// holds those two arms, the parts of a type declared in a body and the
// parts with no checker type to `maxDebugImplSites`, counts that only go
// down.
//
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
// brought in by the std function the record names (`bareStdMethodCall`:
// Callee.Std, the index's byID). A function named as a value (`funcRef`)
// and a partial's callee are found the same way, and so is another file's
// function named through its file (`file.name`, by the reference at the
// member: `siblingFuncAt`), and a std type's function named through its
// module (`lists.List.first`, by the reference at the member:
// `stdOwnerFuncRef`). Another file's owner functions (`Box.of(1)`) are
// keyed by the declaration the checker recorded each block's receiver as
// naming (`receiverOwnerDecl`, fileIndex.ownerMembers). A read of
// a `once`, bare or qualified, finds the binding the reference resolves to
// (`declaredOnceValue`: `oncesByDecl`, or the declaring file's through
// `fileIndex.byDecl`). No local, `once`, function or sibling table is read
// by the name's spelling there. A call the builder writes over a function
// the source names carries that function's classification
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
// head of `qualCallLowered` sends a call qualified with a type, an
// interface or a type parameter to `typeQualCall` by the record alone
// ("Qualified calls from their records"), so a name this file binds to
// another declaration of the same spelling never reaches it: a literal's
// check of another file's `Many` in a file that declares its own `Many`
// calls the other file's `try_from`.
//
// The std calls `typeQualCall` lowers by a route of its own (a seeded
// `Iter.reduce`, `ToJson.to_json` over a record, `Display.to_string` over a
// scalar, `Maybe.is_some`) are a second table in stdroutes.go,
// `stdQualRoutes`, keyed by what the call is qualified with: the record's
// std type or interface (RouteOwner().Std) and the method, which
// `recordedQualRoute` reads once per call. An exact key names one function; a key
// with no method is owner-wide and routes every other function of a std
// type to one lowering (Iter's to `iterCall`, Range's to `rangeCall`), so
// `Iter.loop` and a seeded `Iter.reduce` keep their exact routes ahead of
// Iter's. Its routes are values of the same enum
// as `stdRoutes`'; `typeQualCall` tests the route at the point each is
// lowered, since a route may hand the call on (`Display.to_string` over a
// scalar after the operands are lowered, over a std container's impl
// after that), and its order against the routes that are not std's is
// kept. A
// program's type or interface of a std name has no std identity, so no
// route tests that the name is not shadowed. A qualifier that is a type
// parameter the instance binds to a scalar routes as that scalar. A copy
// the builder respells with a concrete owner (`stdKindQualCall`'s
// `Map.contains` for `C.contains`, `Display.to_string` for `T.to_string`
// at a tuple) carries the written call's record qualified
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
// an operator's OperatorUse.Form follows). Every call the checker checked
// records its receiver and its answer (analysis.Callee.For, Form;
// TestElaborationGate). The calls of an operator interface's function
// are lowered by one switch on it (`interfaceCall`, "Operators from their
// records"). The renderings (`debugRender`, `displayCall`) and the JSON
// of an anonymous struct (`recordToJsonCall`, `recordJsonKindCall`, taken
// where the record answers ToJson's or FromJson's function structurally)
// pick their routine by the record's form. A rendering also serves calls
// with no answer recorded: those the builder writes holding a kind alone
// (a main's failure rendering) and a per-instance record an instance
// entered with no checker type cannot resolve, which render by the
// operand's kind (`viaDebugNoRecord`). The dispatch entry of an anonymous
// struct boxed as a ToJson value is built for the boxed kind
// (stdprogiface.go). Under test `verifyCallForm` holds a routine's pick
// to the record where the routine reads another. A dispatched record is answered according to
// how the builder holds the value the checker types as an interface
// value, which one predicate says (`heldAs`): boxed (an existential or a
// conjunction), answered only by a routine that dispatches on the held
// value's run-time type; as an `Iter`'s view of its source, or as a std
// value held unboxed by its own kind (`stdProgHeldType`), answered by the
// routine that kind takes, since the kind names what is held. A value
// held no way an interface value is fails. An ordering over interface
// values has no dispatching route: the checker rejects one written over
// an interface that does not require Comparable and over values held as
// `Comparable`, whose `compare` takes two `self` arguments (spec §12
// *Interface values*), and `ordering` declines one a generic body reaches
// at an interface value.
//
// One site lowers a body the checker never checked, and marks it so
// (`markUnchecked`; TestNameLookupsOnlyGoDown holds the count, with
// `madeIdent`'s, "What a name names"): parse_fields' per-field
// `F.parse(text)` over an anonymous struct, whose F irparsefields.go
// chooses from the field's kind because the checker checks
// `P.parse_fields` once, generically. A call in marked code has no record
// and is reached by its spelled owner. Removing it needs records the
// checker makes per instance. The name-only Debug of an opaque type
// declared inside a body is written and checked by the front end, as a
// module-level type's is: the checker checks it in the declaring block
// (analysis.FileBodies.BlockDebug), and blocklocaltype.go registers it.
// TestNameLookupsOnlyGoDown counts the functions and table reads that go
// by name, and the counts only go
// down. TestASTConstructionSitesOnlyGoDown counts the AST nodes the
// builder constructs, per node type, and the `ast.Call` literals in a
// function that gives them no record (`carryCall`, `carryType`,
// `carryPattern`, `markUnchecked`); those counts only go down too. `callPlan` answers false
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
// none has its defaults lowered at the call from the declaring file's
// records (`lowerNamesFrom`), whatever this file binds their names to. A file
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
//
// # Regex
//
// A `Regex<C>`'s capture type C is phantom: every `Regex<X>` lowers as one
// host handle, a std function whose type parameters appear only inside
// `Regex<...>` is not generic to it, and `String.captures` builds the C
// value from the groups by C's shape through a VM intrinsic.
//
// # Patterns
//
// One function tests a pattern and binds its names: patternTest
// (irpattern.go), given the pattern, the value it matches, the value's kind
// and the block a failed test branches to. Every position calls it: a
// `case` arm and a binding-else arm (caseArmTest), a pattern `if`
// (patternIfRegion), `is` (isTest), a binding with or without `else`
// (patternBindingStmt), `assert P = v` (patternAssert), a function's,
// impl's or std body's destructuring parameter (irScalarBuild), a lambda's
// (lambdaFunction), a test's setup pattern (testDestructure), an Iter.loop
// state and a parameter pattern a later default reads; and each pattern
// test calls it for its sub-patterns. A position that needs more keeps it
// around the one call: an arm its guard and its NoMatch, an assertion its
// mismatch report, a binding its `else` and the fallback a path of the
// `else` may write (fallbackBinding). A position whose pattern the checker
// records as matching every value (a parameter, a binding with no `else`, a
// setup pattern, a loop state) calls it through bindPattern, whose failure
// edge is a NoMatch no value reaches.
//
// The node type chooses the test. The checker's records decide what the
// node cannot: the variant a head names (PatternVariants, variantOf),
// whether a head names a subtype (PatternTypes, patternRefines: a
// subtype's value is its base's, so `Port(n)` binds the value itself),
// whether the pattern can fail at all (IrrefutablePatterns), and what a
// bracket pattern takes apart (ListPattern.Kind). The value's kind shapes
// only the IR of the test the node chose: a field read off a struct or off
// a record, a distinct's inner value read before a bare collection pattern
// is matched against it, the projection of a variant's payload
// (variantPatternValue: a struct-shaped variant's record, an `embeds` of a
// distinct's inner value, or the one payload).
//
// A test leaves the builder on the block its last check passed into, open,
// with the names bound there, and the position goes on from it: nothing
// jumps to an arm block, and a pattern with nothing to test binds in the
// block it starts in. Sub-values are read only on the success edge of the
// tests before them: a field after the fields before it, a payload after
// its tag, an element after the length test, and a `_` is not read at all.
//
// Whether a pattern can fail is read by patternAlwaysMatches wherever a
// test could be left out: an arm whose pattern always matches ends the arms
// after it (caseArmTest), a pattern that always matches and binds nothing
// is `_`, a one-variant enum's variant pattern tests no tag
// (variantTagTest), `assert P = v` adds no assertion, and an `or` tests no
// alternative after one that always matches. A binding's `else` falls back
// to its pattern's payload where the checker recorded a payload to fall
// back to (BindingFallbacks, elseFallback). A pattern the builder makes in
// place of a checked one carries that pattern's record (carryPattern); the
// census of answers given with no record is irrefutableform.go's.
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
// arms to the types classed a value and leadStatement's to those classed a
// statement, and what falls out of either panics. importKind's switch over
// a kind's tag is held to the tags the same way
// (TestImportKindHasAnArmForEveryTag). irScalarBlock answers a tail for
// every block, so no caller asks whether it did. A block's last statement
// that the table classes a statement and not a value (a binding, a
// destructure, a `defer`, `subtests`, a declaration) is lowered by
// leadStatement as any earlier statement is, and the block's tail is Unit,
// the type the checker gives a body ending in a statement.
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
// lowered body's result, the kind the checker's records give it (a
// lambda, a `concurrent` body, an `if` or `case` used for its value); a
// name a pattern binds from a held value; a held
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
// its subject's type, so a pattern test panics on a state the checker rules
// out (a variant head that names no variant of the subject's enum, a field
// the struct does not have, a tuple pattern of another arity) instead of
// declining (Patterns, below).
//
// The domain guards that remain ask what the entry did not hold: a kind
// joined from operands that differ (a map literal's entries), an instance
// that may fail to form (genStructInstance, a List function's Maybe), a probe that
// takes another route. A
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
// comment describes the sections and the checks). Every std module is an
// object there ([ir.EncodeObject]). The shared section holds, per module,
// its object, each `stdFunc`'s and `stdOnce`'s outcome (lowered or not,
// refusal, default accessors, IR body) and the unlowered table, recorded
// once every module is filed, so each outcome is the whole stdlib's (filing
// another module's declaration of one spelling may refuse it,
// `addOverload`). An outcome names its IR body, accessors and cell by
// linkage name (below). Each module's object and outcomes are a body of
// their own in the shared section, after a table of contents that lists,
// per module, the modules whose objects define a name its body names. No
// name is defined by two shared modules, so a function is in the same
// module whatever order a process decodes them in.
//
// Reading the bundle, checking it and reading the table of contents needs
// no stdlib, so a caller about to check and lower a program starts it on
// another goroutine (PrefetchStdBundle, which vmhost's lowering calls)
// while the front end builds the stdlib's analysis, and the first lowering
// takes the result (`takeStdBundleRead`). Restoring reads the stdlib and
// runs on the lowering's goroutine.
//
// A process restores a module when the shared stdlib has built it
// (`stdlibIndex.withModules`, run by `stdlibLowering` when std.Shared has
// grown), and decodes its body then, with the bodies of the modules it
// depends on at any depth (`stdObjects.decode`, the one place a shared
// object is decoded), all with one [ir.Linker]. Dependencies may form
// cycles, the members of an import cycle naming each other's functions:
// the set is closed first, the linker links a name read before its
// definition is decoded to the symbol the definition then has, and the
// outcomes are resolved once the whole set is decoded. Restoring builds
// the module's view as `buildStdlibIndex` does from std's declarations
// (built from interfaces, no body checked), restores its outcome
// (`restoreStdModule`), and files every restored module again in the
// bundle's order into a new index, so a gen holding the old one reads it
// unchanged. Filing part of the stdlib marks no declaration the whole
// would not. An index holds the IR of every module decoded when it was
// made, restored or not, and `stdlibLowering` replaces one made before a
// later decode.
//
// A program links the shared modules of its own closure, never everything
// the process has decoded (`linkedShared`, Result.IRModules): the base
// modules, the std modules its files import, and each module whose object
// defines a name the program's modules (its own, the listed modules it
// links, its instances) write, closed under import and bundle dependency.
// A value of a type declared outside the closure cannot exist in the
// program, since the code that makes a value is in a module that declares
// its type or imports the module that does, so a call through an
// interface selects no impl outside it. The walk that prunes an image
// follows impls only in the linked modules, so a program's image is the
// same bytes in every process, whatever it decoded before
// (TestStdObjects_AProgramBuildsTheSameImageInEveryProcess). The closure
// is the program's, not a file's: a file lowered on its own (LowerFile)
// seeds it with its own imports and every other file's as the kept
// lowering records them (KeptUnit.Imports), since its check reads only the
// files it imports, so it links what the whole lowering links.
//
// The listed section is decoded when a program first reaches a listed
// module, with a linker over the shared one ([ir.NewLinkerOver]), so a
// listed module's call of a shared one is to the shared module's symbol. It
// names the shared modules to decode first: those whose objects define a
// name it writes and, for a name no shared module defines, those that
// write it too, so a shared module decoded after it links that name to the
// same symbol. A lowering takes the stdlib's index after linking the listed
// modules (GenerateIR, LowerFile), so it holds what that decode added. Two
// std instance modules defining one instance keep one body. A process that
// lowers the stdlib encodes it as a bundle and runs on the bundle decoded,
// as every later process does.
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
// A module that does not restore or decode is lowered (in a test it
// panics). `decodeStdBundle`, which tests use, restores every module, so it
// decodes every one, and checks the order too. The checksum covers the
// whole shared section and is checked when the bundle is read, so a body
// that fails to decode later is a defect, not a corrupt file.
// TestStdObjects_AModuleDecodesWithWhatItNames decodes each module alone
// and requires its dependencies to define what it names, and
// TestStdObjects_DecodedWhenNeededLowersAsAllDecoded holds every
// irbuild.expect program's lowering to its lowering with every module
// decoded (stdDecodeEagerly). A failing cached bundle is ignored and
// rewritten, a failing listed section costs only itself, and a failing
// embedded bundle is reported on stderr. NOMI_DEBUG_STD_BUNDLE=1 reports
// why a bundle was not used. Decoding is roughly a tenth of lowering
// natively (about 5 ms against 40-45 ms shared, 22-27 ms against 240 ms
// listed) and under js/wasm; a binary's first run with an empty cache pays
// the lowering. A process that builds only the base modules and what they
// import (hello world) decodes 24 of the 42 shared modules with IR, 630 of
// their 769 functions.
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
// Url> for Url`'s functions alike. An instance that reads a subtype, one
// whose type parameter is bounded by an interface that makes a value of its
// type and stands for a type that holds a subtype, is an instance of its own,
// and after each such argument its name adds `!` and where the argument
// holds its subtypes, each by its declaring file and name
// (`std/json.Json.decode_as<2!std/int.NonNegativeInt>`; subtypeinst.go); every other
// instance at a subtype is its base's. An impl function adds its block's
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
// A symbol's name is display only: effect messages, reach chains, traces,
// profiles and the debugger print it. A function of an impl block is named
// by its block, `Display.to_string for Loud` for an impl of an interface
// and `Loud.twice` for an inherent function, with a generic block's
// instance's receiver at its type arguments (`Display.to_string for
// Box<Int>`). implFuncOf decides it from the token alone (a program's
// implItem, an interface default lowered for another file's implementor, a
// std function by the block its declaration names, an instance's at its
// receiver), and irCalleeSym
// interns such a token through [ir.Table.ImplSymbol], which carries the
// block on the symbol ([ir.ImplFunc]) and reads no name a call site
// offers, so the name does not depend on which use lowers first. A reader
// that needs the type or the function's own name reads the ImplFunc. A
// default accessor is named after its function (`… default 2`). Every
// other symbol keeps the name its producer gives it.
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
// and check with [Result.Undefined] that the name came back. A dropped
// function a dispatch entry of the old object reached, from a declaration
// the file still has (`entriesReaching`: an instance of its generic impl
// that another file boxes into an interface), is asked for by holding its
// type erased, which names nothing a file lowered again could ask for, and
// is a miss. A file the
// check read with its bodies is lowered as a linked library is, so the
// file can ask it for an instance no kept object holds,
// and [Rekeep] adds each function that module makes and the file's object
// lacks to that object (`mergeLinked`), so a later edit carries or drops it
// by its declaration. A file with no object gets one, as a file whose
// declarations are all generic, which lowers nothing of its own, does once
// an edit of its bodies drops every instance it held: every function made
// from a project file's declaration is in that file's object, and the std
// instances hold only std's.
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
// generic function's declaration lowers no body a program runs
// (`funcDecl`): every call, function value and other file's call names an
// instance. What a generic function does for every type is its abstract
// body's: one more instance, at its own type parameters, each held as its
// bounds' interface (an existential over Debug for one with no bound),
// lowered only for the effect walk, on first use and into modules no
// program links (abstract.go, Result.Abstract). There a per-instance
// record over a type parameter left open is read as the checker answers
// it at that interface, so a call with one receiver is a dispatch on it
// and a rendering the erased one, and a call of a function no one value of
// the type parameter selects an implementation of (`T.from_sql(v)`) is a
// call through the bound, ir.NewBoundCall (boundcall.go). An instance an
// abstract body asks for at its type parameters keeps them open, so
// another generic function it calls, and an impl its record resolves at
// `Maybe<T>`, is read the same way. The impl blocks over a generic type
// are built at its instance at its own type parameters, and a generic
// function of a block at its own.
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
// `calleeTypeArgs` for a generic `fn` of this file or another, a generic
// impl function, a nested generic `fn`, the receiver of a type-qualified
// call's instance and a std template called by name; `literalTypeArgs`,
// `projectGenericInstance` and `templateInstanceOf` for a generic type's
// instance; analysis.ImplBlockTypeArgs for the generic interface an impl
// block implements (`ifaceInstArgs`), which the std generic interface a
// program's block binds into (`stdProgInst`) and a generic interface a
// solved type names (`projectIfaceInst`) carry too; the call's
// `calleeTypeArgs` for a generic callee's parameter default
// (`ifaceDefaultFrameAt`, `callFrameAt`, `fnSig.defaultTypes`). An entry that reads a kind
// off something the checker typed reads the type at the same place: an
// operand's type in ExprTypes (`operandCheckerType`) for a std container
// impl a receiver selects, a std interface default's `self` and the value
// `io.print` or `dbg` renders; a rendering's Type for a hole; the call's
// `For` for a default's receiver no operand holds; the enclosing frame's
// type for `T.method(...)` (`stdKindQualCall`'s `kt`). An instance an
// annotation spells (`b: Box<Int>`, a field's `Pair<String, Int>`, a
// generic refinement's `Many<Int>`) reads the types the checker recorded
// for the annotation (analysis.FileAnalysis.TypeExprTypes, which `typeOf`
// projects), and the impls of that instance carry them. Which of an impl
// block's parameters a receiver's type binds is the checker's match of
// the block's recorded receiver against it (analysis.ImplBlockArgsAt), for
// a std container's block (`stdRecvTypes`) and a program's block over one
// (`stdRecvImplAt`), whose kind and type come from the same place: an
// operand, a box's coercion, the call's record (`firstOperandType`,
// `concreteInterfacePlan`). An instance the builder names by its receiver
// rather than at a call (`stdRecvInstance`: a box's Display, a renderer's
// nested Debug, a sort's comparator) reads the receiver's type where it
// is asked for: a view's element (analysis.IterView.Elem), a sort key's
// type argument (`recordedTypeArg`), and a part's type within its whole
// (`partCheckerType`: a tuple's element, a record's field, a container's
// type argument), as a record's `from_json` reads each field's. A
// recorded type that names an enclosing instance's type parameters has
// that instance's types put in (`checkerTypeArg`, over
// analysis.ReplaceTypeParams). An instance is interned by its kinds, so it
// carries the types of the first construction that had them, and a
// generic type's instance adopts them from a later one. What has none: a
// generic typealias's target at a use's arguments, an argument written as
// a default in the declaring file, and the few instances asked for at a
// kind no expression has (`checkSubstCensus` names them). The builder
// never builds a checker type from a kind. A call's and an operator's
// record is read at these types inside the instance ("Qualified calls from
// their records", "Operators from their records"), through the file whose
// program holds the impls the types have (`instanceFA`: a std instance's
// caller, and for a listed std module lowered with the program the
// program's first file, since its own analysis is std's). In a test
// binary, or with NOMI_SUBST_VERIFY=1, each is held to its kind
// (`verifySubst`: the type projected, in a std instance's gen through the
// program gen that asked for it, is the frame's kind, or the same std
// interface held through its other def, `sameStdIfaceHeld`), and
// TestIRGolden_RecordsMatchTheVM fails for any instance a program's
// lowering requests whose parameters are entered without one
// (`checkSubstCensus`), counted the same whatever the artifact cache
// holds, since std's own lowerings are not counted.
// The kind of a written type is the projection of the type the checker
// recorded for that type expression (`typeOf`, over `recordedTypeExpr`,
// which reads srcFA's TypeExprTypes), and
// `project` is the builder's one conversion of a checker type to a kind
// (TestProjectNamesEveryTypeKind holds it to every kind of
// analysis.Type). With no gen (`projectShared`) it answers the types every
// gen shares, for the std signature pass, which runs before any gen: each
// parameter and written result of a std function and each std `once` is
// the projection of the type the checker recorded for it (`stdParamType`:
// the annotation's TypeExprTypes record, a destructuring parameter's
// PatternTypes record, or the symbol's type for a parameter typed by its
// default; `stdTypeExprType`; `onceSymbolType` for an unannotated `once`),
// so a std signature and a program's are read the same way. A position the
// projection refuses (a type a program-std module declares, `Iter<T>`, a
// std interface with no rt table held as a value, a std type whose def
// holds one) refuses the function as a shared one;
// TestStdSignaturePositionsProject holds every position to a record and
// counts those refusals by class. A
// parameter's kind is its annotation's, its pattern's record, or its
// symbol's type (`paramKind`, for a declared function's and a lambda's
// alike). A pattern's kind is the projection of its record in
// PatternTypes (`patternKind`), and the variant a pattern's head names is
// its record in PatternVariants (`patternVariant`): a destructuring parameter, a `case`
// arm, a binding's `else` and a distinct sub-pattern look no head up by
// name. A nominal type the projection reaches, which the checker names by
// its origin and name, is found by its declaration: the node the name
// resolves to in the module scope of the file with that origin (`declNode`;
// the registry's `fileOf` gives the program file of each origin).
// `programTypeDef` answers a program file's declaration: a block's, by the
// checker's type for it (`blockLocalOfType`, `blockLocalTemplateOf`), this
// file's, the gen's def of that node, or a sibling's or listed std module's,
// mirrored from the declaring gen's def (`siblingOwner`, and
// `siblingIfaceOwner` for an interface). `nominalDef` adds a std type's def
// from its projection, and `typeDeclDef` answers the declaration a
// checker's symbol names. project's arms for a declared type read them
// (`localNamed`, `solvedIface`), and for a type no program file declares
// that a shared std module declares in Nomi, the process's def
// (`stdDeclaredDef`; "Std spec rows"). A gen's def of a declaration is filed by
// its node where its shell is made (`defsByDecl`, `ifacesByDecl`: every type
// and interface the gen declares, block-local ones included, and no mirror;
// `templatesByDecl`: each module-level generic declaration's template) and
// another gen reads it there (`typeRegistry.defOf`, `ifaceDefOf`,
// `foreignTemplateOf`). None reads a gen's tables by name (`types`,
// `ifaces`, `genericTemplates`), which hold a sibling's declaration only
// once its gen has built its types, and the mirrors a gen reached by a
// name; under substVerify defOf and ifaceDefOf hold the declaring gen's
// table by name to the same def for the declaration's name. A sibling's
// signature is lowered under its file's names (`lowerNamesFrom`) so its
// records are found.
//
// A written type a declaration or a construction names is the checker's
// too: an embedded variant's type and the named types of a refused
// annotation by the type recorded for the type expression
// (`writtenTypeDef`); a namespaced or module-qualified type by the same
// record (`namespacedType`, which answers a namespaced declaration of this
// file or another program file's type); a struct literal's and a
// struct-shaped variant literal's type, and a marker value's, by the type
// the checker gave the expression; the distinct of a constructor, bare or
// qualified (`Meters(5)`, `Day.Hours(48)`, `ids.UserId(3)`), a struct's
// call form (`Span({...})`), the prefix of an attached literal and the
// `Context` of `Context.value` by the checker's reference at the written
// name; the value a refinement's `unchecked` builds by the type the
// checker gave its body (`uncheckedRefinement`); the enum of a written
// `Owner.Variant` by the checker's reference at the owner's last name
// (`variantOwnerDef`: `Shape` in `shapes.Shape.Circle`, `Reading` in
// `Probe.Reading.Spike`); and the enum of the `Enum.Variant` the builder
// writes for a `.Variant` shorthand or a literal-attach form
// (`.Items[1, 2]`, `Json.Obj{...}`) by the type the checker gave the
// shorthand or the literal (`noteVariantOwner`, `dotOwners`); and the
// type whose handler builds a typed literal, tagged or untagged, by the
// checker's record of it (`TypedLiteral.Tag`, `literalTagDef`). So `Day.Hours(Day(3))`,
// where `Hours` is a function of `Day` beside a namespaced `Day.Hours`, is
// the function's call.
//
// What still resolves a written name: the owner a respelled copy of a
// call is written with, which is a container's base or an interface's name,
// never a declared type's or a scalar's. Why a written type has
// no kind is named from the type the checker recorded for it, the record
// typeOf reads (sigreason.go's refusal walk). An impl block's receiver is
// the type the checker recorded for it: the template it names
// (`receiverTemplate`) and which of its arguments are the block's type
// parameters (`receiverTypeParamNames`). Whether a dotted chain in
// expression position (`Probe.Reading.Steady`, `shapes.Colour.Red`) names
// a type is the checker's reference at its last name
// (`dottedTypeName`, `dottedTypeRef`). A generic struct literal's,
// a struct call form's and a struct-shaped variant literal's template is
// the one the type the checker gave the construction names
// (`genericTemplateOf`, `templateAt`), as an inferred instance's is
// (`projectGenericInstance`): a block's by its type parameters, this
// module's by its declaration, another file's from the declaring gen. A
// generic variant's template is the one its enum names: the enum of the
// variant the checker resolved a written `Enum.Variant`'s name to, or the
// enum the checker typed a shorthand or a literal-attach form at
// (`variantEnumType`, `dotOwners`). The template an impl item's signature
// names (`expansiveImplItem`) is the one the type the checker recorded for
// the type expression names.
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
// function). The call carries the record the checker makes for it
// written at the held value's type (`stdIfaceCallRecord`): the boxed
// value's coercion's Value (`intoExistential`, `irBoxPlanFor`), or for a
// std default a program's block does not write, the block's receiver
// (`implDef.recvType`), so the instances it reaches are entered with that
// type. A thunk is keyed by the box's type name, so the first value to
// ask for it gives its type, and two checker types of one kind share it:
// what projection drops never changes which body a value reaches, since
// a subtype has its base's impls (spec §4 *Subtypes*), an alias is its
// target and a host type's phantom argument (`Regex<C>`) has one
// representation and std's impl is generic over it. Where the file binds
// the interface's bare name to something else, the thunk calls the std
// function whose impl header names the interface's declaration. A thunk is built with the gen's refusals and the
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
// `==` over a declared type is lowered by the checker's record of the
// operator (analysis.FileAnalysis.Operators, `equality` in
// irequality.go): a comparison by shape where the record says so or
// names an impl `machineImpls` computes by shape (a derived impl of a
// struct, an enum or a distinct), else the impl it calls, the block of
// the recorded function (`recordedImplItem`). An operator over a type
// parameter is read at its instance's types (`operatorUseAt`).
//
// A call of `Equatable.equal` or `Hashable.hash` over such a type is
// answered by shape (analysis.Callee.Form), and `interfaceCall`
// (ifacecall.go) lowers it from that record: `Equatable.equal` as `==` by
// shape, `Hashable.hash` as the VM's `hash_value`, the hash a Map key of
// the type takes.
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
// module (stdifacedefault.go). A call no written impl answers reaches it
// from its record (`recordedStdDefault`): the record names the interface's
// function, by its std identity, as the function the call runs, and the
// type the call is for, at the instance's types inside a generic body
// (`Int.from_missing()`, `FromJson.from_missing<List<Int>>()`, and an
// instance's `T.from_missing()`, as in std/json's `decode_field<T>`, which
// a derived FromJson calls per field). A program interface's inherited
// default is instead an item of the implementing block, planned from the
// record as any impl function is (`recordedImplPlan`): a program's file is
// lowered with the program, a std module once per process, so a std
// default is a template each program instantiates. A program block of a std interface inherits no item for such a default: its
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
// accessor of the impl function it reaches directly, whichever of the two
// files is lowered first (`implSlotHome`, through
// `implMemberSite.defaultHome` and `reachedImplHome`), and so does
// the supplier of a partial's open slot (`partialIfaceDefaults`), which
// dispatches on an interface value instead. A partial over an interface
// function carries none of the interface's literal defaults as its own
// open slots' defaults, since the impl that runs may replace them
// (`partialCall.literal`).
//
// A call in another file that leaves out a parameter default of an
// interface function (reaching a block's function or a named-argument call)
// fills it through an accessor the declaring file builds on request
// (ifaceslotdefault.go), since the default may name what only that file
// can (a private function), keyed by the interface
// function, the slot, the parameter kinds up to it and the type arguments,
// and linked by the function's linkage name with them and the slot. A
// generic interface's parameter default may name its type parameters (`x:
// List<T> = pick()`), so it is lowered in a frame binding the interface's
// and the function's type parameters at the type arguments the checker
// instantiated the call at (`ifaceDefaultFrameAt`, through
// `calleeTypeArgs`), which the call's signature carries to the accessor
// (`fnSig.defaultFrame`). A call whose record lacks one declines. The
// defaults of any generic callee no instance of which is lowered at the
// call (an impl function of a generic type's instance) are lowered in the
// same kind of frame, binding every type parameter the call's record
// instantiates (`callFrameAt`, `defaultInFrame`), and an interface
// function's defaults lowered in its own file are lowered for the call's
// implementor as the interface's default bodies are (`paramDefaultSelf`,
// `gen.defaultSelf`), so a read of `self` in one is the implementor's.
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
// A call reaches the template when its record names the interface's
// function as the one it runs and no route that finds a written impl
// function answered it (`stdIfaceDefaultCall`, the last arm of
// `typeQualCall`):
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
// Some expressions take their kind from the checker's record, read through
// one reader of it (`recordedType`, `recordedKind`, and `settledKind` for
// the type a value of Infallible takes from its position), and lower their
// parts at that kind: an `if` or `case` used for its value (`regionSig`),
// a block used as an operand (`blockKind`), a `concurrent` body, a bare
// payload-free prelude variant with no expected type (`bareVariantOwnKind`:
// `None == None`) and an `Iter.loop`'s state, the type the checker
// instantiated its S at, which the starting state enters as an argument
// enters its parameter (`iterLoop`; `d: Display = Iter.loop(Set[1, 2],
// ...)` loops over a Display), through `valueKind`, a lambda's result
// (`lambdaResultKind`), a `todo` or `unreachable` (`trapKind`), a type name
// used as a value, the X of the `Type<X>` the checker records for it
// (`witnessTypeKind`), and a channel constructor whose
// callee's instantiation is not recorded, the call's type
// (`channelCtorFromTarget`). A lambda's or a `concurrent` body's result is
// the result every `return` in it is lowered at (`returnKind`), as a
// declared one is a `fn`'s, and every arm of a region is lowered at the
// region's kind. A node the builder writes in place of source carries the
// checker's type of the source node it stands for (`carryType`, which
// `recordedType` reads): the lambda a capture or a partial stands for
// takes the capture's type, a partial's entry the capture's too, and the
// closure of a nested `fn` its symbol's; the call a piped stage is lowered
// as takes the pipe's type, a literal's join of its spreads the literal's,
// the `Iter.to_list` that collects a spread the collection of the elements
// the checker records the spread walks (analysis's SpreadCollected), a std
// interface's thunk for a held type the interface function's result at it
// (InterfaceResult), the `List.concat` a List `+` is lowered as the
// operator's, and the call
// a function value's body makes the result of the reference's function
// type (`carryRefResult`). A List, Map or Vector function's call takes its
// result from its record (listCallPlan, mapCallPlan, vectorCallPlan).
//
// A collection, tuple or record literal is built at the type the checker's
// records give its value (`literalKind`, `convertedKind`): its own type, the
// join of its items (`[c, shape]` is a List<Shape>) or, where it is checked
// against an expected type, that type (`[c]` where a List<Shape> is
// expected), or the type its Conversion converts it to (`(1, Pt{x: 5})`
// returned as an `(Int, Named)`). Each item is lowered against its part of
// that kind, as an argument is against its parameter, and converted to it
// where it differs (`lowerItemsAt`, `convertTo`): an embedded value widens,
// a value enters an interface, a source is viewed as an Iter, a nested
// empty literal is typed. A spread enters the literal's kind before it is
// joined (`collectSpread`). An empty collection literal (`[]`, `Set[]`,
// `Vector[]`, `{}`) is built the same way (emptyLiteral, then `emptyOf`),
// and the checker records every empty literal it accepts and closes an
// element type nothing determines (internal/analysis's closed_vars.go), so
// the record always names a collection; nothing retypes it later. A literal
// the builder made has no record (`madeLiteral`) and takes the kind of the
// position it is lowered for, which lowerTypedOperand passes: a copy of a
// literal under a type's name (`Query[]` over its list, `Kvs{}` over its
// map, `Json.Obj{...}`'s anonymous map), the items of a constructor's tuple
// payload, a run of a literal with spreads, a derived body's literal, and a
// generic struct's field default (`items: List<T> = []`), recorded in terms
// of the struct's `T` and built for an instance. A std field's empty default
// and a bare `..` at a missing key are built at the field's kind.
//
// Where such a node has no record and carries none, a block takes its
// tail's kind, a List, Map or Vector function's call its row's result
// (listResultKind, mapResultKind, vectorResultKind: a std interface's thunk
// for a value held by its kind alone; a thunk for a value of a checker type
// carries the interface function's result at it, analysis's
// InterfaceResult), a tuple or record
// literal with no position its parts' kinds, and a region, a lambda, a
// `concurrent` body, a bare prelude variant, an `Iter.loop` and a
// collection literal with no position decline (an `Iter.loop` in a generic
// declaration's own body, whose state is the declaration's type parameter,
// has a kind only in an instance);
// `noteUnrecordedKind` counts each such site by family and by why it has no
// record, held by `maxUnrecordedKindSites`.
//
// Every other expression's kind is the kind its lowering produces: a
// name's is its binding's, a call's its callee's result at the recorded
// instance, an operator's its record's, a field's its declaration's. None
// is chosen by the builder: `verifyExprKind` holds each one with a record
// to that record, with no exception, and the census counts the ones with
// none. Two places still give an operand a kind from another operand
// rather than its own record: an equality's bare prelude variant beside a
// typed operand takes that operand's (`x == None`, `None == x`,
// `bareEqualityOperands`), and so does a prelude constructor call there
// (`x == Maybe.Some(v)`, `preludeCallWant`).
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
// Under substVerify `verifyExprKind` holds the kind `lowerExact` answers
// for each expression to what the checker's records say the value is there,
// through the same reader: the expression's ExprTypes entry through
// `project` under the current frame (a read the checker narrowed is
// recorded at the narrowed type), or for an expression of Infallible the
// type its position settled (ExpectedTypes), or the type its Conversion or
// Coercion converts it to when the builder lowers it at that type directly
// (a lambda written where a wider function type is expected). For the
// expressions that read their kind from the record (above) the comparison
// holds by construction; it still holds every other one. It reads
// ExprTypes and not `checkedKind`, whose answer for a name is its
// declaration's type, before narrowing and before defaults are filled. A
// node with no record (one markUnchecked marks, a pattern's literal, a
// stand-in for a placed argument, a copy, a node the builder writes) is not
// compared, and `exprKindCensus` counts it by why. Any disagreement fails
// the lowering, naming the expression, both kinds and its position.
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
// TestStdBundle_DecodesToTheLowering and TestStdObjects_* (here).
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
// declaration the checker resolved the call to (typeQualCall), whether or
// not the file has a name for it. A validated type, or a `type` over a
// subtype, has a def, and its `try_from` is its TryFrom impl's; a generic one takes the
// base at its impl's parameters and is built at the
// type arguments the checker solved for the generated `unchecked` node.
//
// `struct Span ... when ... end` is an ordinary struct; only its
// construction is checked. Its `unchecked` builds it from the record of its
// fields, as the call form `Span({...})` does.
//
// A read of a subtype (subtyperead.go) is a call of FromJson's function at
// a subtype, which reaches its base's impl: the checker's record of the
// call at the instance's types (analysis.FileAnalysis.SubtypeReadAt)
// names each `try_from` that decides the value, and the builder calls
// them on what the base's impl answered (typeQualCall's readNarrowed, and
// the field reads irrecordjson.go writes), answering the record's breach,
// a ShapeError or None, when one is None. A record read from texts checks
// each field of a subtype after its base's Parse (irparsefields.go), a
// breach the record's None. A container needs nothing more: its impl
// reads each element through the element's own call, in an instance that
// reads the subtype and so is its own (subtypeinst.go). A `try_from` is
// called by the record the checker made of it, its reference carried at
// the callee (carryCall, calleeRef) for a generic subtype's type
// arguments, and reached from another module, a private subtype's
// included (fileOwnFunc).
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
// signature, `BoundedRange<Int>` in `Generator.int`, it is the same
// projection of the checker's type (projectShared), which the shared
// lowering retains as it does a body over the base. A
// literal of one
// is checked by a function of no arguments that builds the literal and
// calls the subtype's `try_from` instance at the literal's type
// arguments, the call the checker typed (RefinementSiteStep.Check,
// refinementCheckBuild), its owner the declaration the checker recorded,
// whatever the file binds the subtype's name to (typeQualCall); the sites
// are built before the late instances
// other files asked for are flushed, so an instance such a call asks
// another file for is built. Its declaration's RefinementRuleSite has a
// function of the base's value calling its `try_from` at each set of type
// arguments the checker typed (analysis.RefinementRuleCheck,
// refinementRuleCheckBuild), which vmhost never runs and only reads for
// effects; each lowers its instance into the unit's module. The site also
// names the prefix of every instance's linkage name and the interfaces of
// the bounds, so vmhost checks every instance and every impl of them the
// linked program has (an impl function's symbol carries its interface,
// ir.Symbol.Impl), which the kept lowering keeps.
//
// A std signature that names a subtype, `Int.pow(n: Int, exp:
// NonNegativeInt)`, has its base's kind (projectShared), a scalar, a
// Decimal or a type std declares, and its host adapter takes the base's
// value (internal/hostgen's shape of a subtype is its parent's). A
// subtype of any other base, a collection or range at fixed arguments
// (`Ratios`, `List<Int>`; `FiniteRange`, `Range<Float>`), a struct or an
// enum std declares (`FieldBits`, `JsonObject`), has the kind of its first
// parent as its declaration writes it.
package irbuild
