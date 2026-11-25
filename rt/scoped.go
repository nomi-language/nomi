package rt

// AppFields is the application in force on a frame: each field's value and
// the Context field's. It is immutable. Boot publishes one, each `with` line
// makes a new one from the one in force, and every frame derived from a
// frame carries its pointer, so code that replaces nothing shares one value.
//
// A function value keeps the AppFields in force where it is made
// (KeptFields), and its body runs under them wherever it is called
// (EnterKeptFields): an app field is an argument the program did not write,
// a call passes it on, and a closure captures it as it captures any value in
// scope (spec §15 *Scoped application fields*).
type AppFields struct {
	fields  map[string]any
	context *Context
}

func (a *AppFields) withFields(fields map[string]any) *AppFields {
	next := AppFields{fields: fields}
	if a != nil {
		next.context = a.context
	}
	return &next
}

func (a *AppFields) withContext(context *Context) *AppFields {
	next := AppFields{context: context}
	if a != nil {
		next.fields = a.fields
	}
	return &next
}

// activeContext is the context these fields hold, or the root context.
func (a *AppFields) activeContext() Context {
	if a != nil && a.context != nil {
		return *a.context
	}
	return ContextRoot()
}

// EnterScopedField installs an immutable override inherited by callees and tasks.
func EnterScopedField[T any](parent *Frame, name string, value T) *Frame {
	child := *parent
	var old map[string]any
	if parent.app != nil {
		old = parent.app.fields
	}
	fields := make(map[string]any, len(old)+1)
	for key, v := range old {
		fields[key] = v
	}
	fields[name] = value
	child.app = parent.app.withFields(fields)
	return &child
}

// WithAppOf is fr reading the app from's lineage reads: its fields as the
// `with` replacements in force there leave them, its Context, and what
// boot published. An expression a debugger evaluates in a stopped frame
// runs on a frame of its own built this way, so it reads the app the
// stopped code reads at that point.
func WithAppOf(fr, from *Frame) *Frame {
	child := *fr
	child.app, child.booted = from.app, from.booted
	return &child
}

// PublishScoped installs a complete boot schema. The caller relinquishes
// fields. What boot published is also what a `once` initializer reads
// (forcingOnce), so the two are one value until a `with` replaces a field.
func PublishScoped(fr *Frame, fields map[string]any) {
	fr.booted = fr.booted.withFields(fields)
	fr.app = fr.booted
}

// LookupScopedField answers one application field from the calling lineage,
// and whether the lineage publishes it, for a host that reports its own
// failure rather than trapping.
func LookupScopedField(fr *Frame, name string) (any, bool) {
	if fr == nil || fr.app == nil {
		return nil, false
	}
	v, ok := fr.app.fields[name]
	return v, ok
}

// KeptFields is what a function value made on fr keeps: the AppFields in
// force. It is nil, and the function's body runs under the fields of
// whoever calls it, where no application exists to keep: while boot builds
// it (inBoot) and while a `once` initializer runs (forcing). Written out, a
// closure made there would take the application from its caller as a
// parameter, since nothing in force where it was written could supply it.
func KeptFields(fr *Frame) *AppFields {
	if fr == nil || fr.inBoot || fr.forcing != nil {
		return nil
	}
	return fr.app
}

// EnterKeptFields answers the frame a function value's body runs on when
// called from caller: caller with kept in force, as a `with` of every field
// would make it, and the release to call when the body leaves, which is nil
// when there is nothing to release.
//
// It answers caller itself when kept is nil or already in force, so a
// program that replaces no field, where every function value keeps the
// fields every caller has, allocates nothing per call.
//
// The kept context is floored by the deadline in force at the call, as a
// `with` of the Context field is (storeContext): a call cannot outlive its
// caller's deadline, so a closure made with no deadline and called under one
// runs under it. A kept deadline tighter than the caller's bounds the body's
// waits through EnterDeadline, and the release ends it.
func EnterKeptFields(caller *Frame, kept *AppFields) (*Frame, func()) {
	if kept == nil || kept == caller.app {
		return caller, nil
	}
	return enterKeptFields(caller, kept)
}

func enterKeptFields(caller *Frame, kept *AppFields) (*Frame, func()) {
	child := *caller
	child.app = kept
	if caller.app != nil && kept.context == caller.app.context {
		// The context in force already, so no floor and no deadline.
		return &child, nil
	}
	next := kept.activeContext()
	prev := chainBound(caller.app.activeContext())
	if prev.set() {
		floored := ContextWithFloor(caller.app.activeContext(), next)
		child.app = kept.withContext(&floored)
		next = floored
	}
	// A floored chain's bound is no looser than prev's on either clock, so an
	// equal one is the bound the caller's waits are already under.
	b := chainBound(next)
	if !b.set() || b == prev || child.ctx == nil {
		return &child, nil
	}
	return EnterDeadline(&child, next)
}

// WithKeptFields is fr with kept in force and nothing else changed: the
// frame work that leaves its caller starts from, a supervised task, whose
// context drops the deadline anyway (supervisorEnrol). nil keeps fr's.
func WithKeptFields(fr *Frame, kept *AppFields) *Frame {
	if kept == nil || kept == fr.app {
		return fr
	}
	child := *fr
	child.app = kept
	return &child
}
