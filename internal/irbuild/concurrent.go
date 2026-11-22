package irbuild

import ()

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
	// args is the Nomi arity.
	args int
	// spawn marks the rows whose argument is a CALLBACK rather than a value,
	// so the result type is read off the lambda's own kind rather than off the
	// checker's instantiated return.
	spawn bool
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
// module function's own name works. See taskSpawnAll.
var taskFuncs = map[string]taskFn{
	"spawn":     {args: 1, spawn: true},
	"spawn_all": {args: 3, spawn: true},
	"await":     {args: 1},
	"outcome":   {args: 1},
	"cancel":    {args: 1},
	"await_all": {args: 1},
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

// --- Supervisor.spawn ---------------------------------------------------------

// taskHandleKind is the kind of `Task<payload>`.
//
// `genHostInstance` is a `*gen` method so that an instance at a
// package-RELATIVE type argument reaches a per-gen table rather than only the
// process-wide one. That per-gen fallback is needed: `Task<Request>` over a
// user enum is exactly the shape the process-wide table refuses, and a spawn
// whose body returns a user type is an ordinary program.
//
// payload is a lowered function's result or Unit, a valid kind, and the
// instance forms over any: genHostInstance refuses only an invalid argument.
func (g *gen) taskHandleKind(payload kind) kind {
	handle, built := g.genHostInstance(taskSpec, payload)
	if !built {
		panic("irbuild: Task<" + payload.nomi() + "> did not form over a lowered function's result")
	}
	return handle
}
