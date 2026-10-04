package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `concurrent { }` and `std/tasks` — the structured-concurrency surface.
//
// The `concurrent { body }` block itself is lowered to IR in irconc.go. This
// file holds the `std/tasks` call rows and the helpers the builder reads for
// them.

// --- the `std/tasks` call arm ------------------------------------------------

// taskFn is one `Task` function this builder lowers.
//
// The shape is channelFn's and for channelFn's reason: every declaration in
// std/tasks is a `pub host fn ...<T>`, so `stdCandidateFor` short-circuits at
// `case generic:` before it asks anything about a signature and the stdlib index
// carries a refusal for all six. What differs from channels is where the type
// argument comes from — a Task function's first parameter or its lambda argument
// carries it, so no turbofish is needed and none appears in the corpus.
type taskFn struct {
	// rtCall is the rt function. Every one takes the frame first, because every
	// one either creates a task (and needs the scope) or waits on one (and
	// needs the caller's cancellation) — except `cancel`, which is the row that
	// makes `frame` a field rather than an assumption.
	rtCall string
	// args is the Nomi arity.
	args int
	// frame is whether the rt function takes `fr` ahead of the Nomi arguments.
	frame bool
	// spawn marks the rows whose argument is a CALLBACK rather than a value,
	// so the result type is read off the lambda's own kind rather than off the
	// checker's instantiated return.
	spawn bool
	// names is the declaration's parameter names, positionally, for placing a
	// NAMED argument. See taskPlacement for why this is a row rather than an
	// index lookup, and TestTask_RowShapeMatchesStdSource for the mirror that
	// stops it drifting from std/tasks.nomi.
	names []string
	// tags is the placement SHAPE: one tag per parameter. NOT a signature and
	// never used as one — `argSlotPlan` reads `len(params)` for the arity and
	// `params[i].tag` for exactly one thing, the trailing-callback route (rule
	// 2, sugar.go), and nothing here is passed to a coercion. A `Task<T>` is a
	// generic-std-host INSTANCE, so its tag is tagNamed.
	tags []tag
}

// taskFuncs is the lowered surface of `impl Task<T>`.
//
// Keyed by NAME rather than positional, so a row may be added anywhere; the
// append-only rule the spec tables carry does not apply here.
//
// `spawn` and `spawn_all` both take a CALLBACK and both therefore set `spawn`,
// which routes them to their own arms. What they do NOT share is which callback
// shapes they accept, and the reason is which thing carries the frame:
// `spawn`'s callback BECOMES the task body, so it must be one this builder
// lowered; `spawn_all`'s is CALLED BY rt with rt's own per-item frame, so a
// module function's own name works. See taskSpawnAllCall.
var taskFuncs = map[string]taskFn{
	"spawn": {rtCall: "rt.TaskSpawn", args: 1, frame: true, spawn: true,
		names: []string{"body"}, tags: []tag{tagFunc}},
	"spawn_all": {rtCall: "rt.TaskSpawnAll", args: 3, frame: true, spawn: true,
		names: []string{"source", "f", "max_running"},
		tags:  []tag{tagSeq, tagFunc, tagInt}},
	"await": {rtCall: "rt.TaskAwait", args: 1, frame: true,
		names: []string{"task"}, tags: []tag{tagNamed}},
	"outcome": {rtCall: "rt.TaskOutcomeOf", args: 1, frame: true,
		names: []string{"task"}, tags: []tag{tagNamed}},
	"cancel": {rtCall: "rt.TaskCancel", args: 1,
		names: []string{"task"}, tags: []tag{tagNamed}},
	"await_all": {rtCall: "rt.TaskAwaitAll", args: 1, frame: true,
		names: []string{"tasks"}, tags: []tag{tagList}},
}

// taskSpec is the `Task` row of the generic-std-host table, resolved by
// (origin, name) so a reorder cannot repoint it silently — channelSpec's form.
var taskSpec = genHostSpecFor("std/tasks", "Task")

// taskElem reads the payload kind off a `Task<T>`, by SPEC POINTER rather than
// by rendered name — so a user type spelled `Task` has its own def and answers
// false, which is the half a name check gets wrong.
func taskElem(k kind) (kind, bool) {
	spec, args, isHost := genHostOf(k)
	if !isHost || spec != taskSpec || len(args) != 1 {
		return kindInvalid, false
	}
	return args[0], true
}

// taskPlacement is the slot plan for a `Task.<method>` call, or false when the
// arguments cannot be placed against the declaration.
//
// # Why the names come off the row and not off the stdlib index
//
// `g.std.byType["Task."+method]` (stdnamedarg.go's source) answers in a USER
// module and answers NOTHING in std/tasks' own gen, so the `//!` prompt cases
// in std/tasks.nomi would refuse `named argument`. The mechanism:
// `lowerStdlibModule` lowers a module against `earlier`, the index of the
// modules lowered BEFORE it, which by construction excludes the module itself;
// `bindStdSiblings` adds the module's own candidates but SKIPS any that is
// neither settled nor rt-bound (stdlib.go's `if !settled[c] && f.rtCall ==
// ""`), and `Task.spawn_all` is an unsettled `pub host fn` whose rt binding
// lives in THIS file rather than in the index. So inside std/tasks there is no
// index entry to read names from, and that skip is correct: it is what stops
// an unlowered function being lent a Go name.
//
// A lookup like that is correct for an entry module and silently empty for the
// declaring module, and only the real stdlib-test path shows it; a check built
// on the complete index cannot.
//
// So the names live on the row, beside `args` — which is already the same datum
// (the declaration's arity) written here for the same reason — and the drift
// risk that motivates reading the index is closed by
// TestTask_RowShapeMatchesStdSource, which reads std/tasks.nomi through
// `std.Load()` and fails if any row's names or arity stop matching the
// declaration. That is a checked mirror rather than a copy.
func taskPlacement(fn taskFn, args []ast.Node) (argPlan, bool) {
	params := make([]kind, len(fn.tags))
	for i, tg := range fn.tags {
		params[i] = kind{tag: tg}
	}
	return argSlotPlan(args, params, fn.names)
}

// --- Supervisor.spawn ---------------------------------------------------------

// --- the unsolved channel constructor ----------------------------------------

// channelWantedInstance is the `Channel<T>` a node's coercion target names, for
// a constructor call the checker recorded no instantiation for.
//
// Claims ONLY a Channel, by spec pointer through channelElem, and only when the
// owner it reads back is `Channel` — a wanted `Sender<Int>` is not a
// constructor's result and answering one would spell `rt.ChannelUnbuffered` into
// a position that cannot hold it.
func (g *gen) channelWantedInstance(t *ast.Call) (kind, bool) {
	want, found := g.wantOf[t]
	// kindInvalid: lookup — asks whether a coercion target exists at all; a miss leaves the caller's own refusal to name the gap.
	if !found || want == kindInvalid {
		return kindInvalid, false
	}
	if _, owner, isChannel := channelElem(want); !isChannel || owner != "Channel" {
		return kindInvalid, false
	}
	return want, true
}

// taskHandleKind is the kind of `Task<payload>`.
//
// `genHostInstance` is a `*gen` method so that an instance at a
// package-RELATIVE type argument reaches a per-gen table rather than only the
// process-wide one. That per-gen fallback is needed: `Task<Request>` over a
// user enum is exactly the shape the process-wide table refuses, and a spawn
// whose body returns a user type is an ordinary program.
func (g *gen) taskHandleKind(payload kind) (kind, bool) {
	return g.genHostInstance(taskSpec, payload)
}
