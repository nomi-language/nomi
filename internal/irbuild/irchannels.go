package irbuild

// `std/channels` in retained bodies.
//
// Channel.buffered, Channel.unbuffered, Sender.send, Sender.close and
// Receiver.receive are host crossings named `Owner.method`, spelled with an
// `rt` target and the checker's solved signature; a constructor whose
// signature the checker did not solve takes it from the type the checker
// recorded for the call (see channelCtorFromTarget). A `Channel<T>`'s `sender` and `receiver` are field
// projections spelled `(ch).Sender`. The VM runs an `rt` channel and holds a
// Channel as std's `channels.Channel` struct of its two halves.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irConcHandleKind is a task or channel handle over a retained payload:
// `Task<T>`, `Sender<T>`, `Receiver<T>` or the `Channel<T>` holding a pair.
// A function value is a payload like any other (`Channel<(Int) -> Int>`):
// the rt channel carries whatever value it is sent.
func irConcHandleKind(k kind) bool {
	payload := func(p kind) bool {
		return p == kindUnit || irCallableValueKind(p)
	}
	if elem, owner, isChannel := channelElem(k); isChannel {
		return owner != "" && payload(elem)
	}
	if elem, isTask := taskElem(k); isTask {
		return payload(elem)
	}
	return false
}

// irHandleFieldKind is a channel or task handle held in a struct field or a
// struct-shaped variant's field: `inbox: Channel<Request>`,
// `Get {reply: Channel<Int>}`, `state: Channel<Map<String, Int>>`,
// `job: Task<List<Int>>`.
//
// A handle never puts its element in the layout, but the admission check
// looks through it: the element's own fields are checked in turn, and
// `enum Request { Get {reply: Channel<Request>} }` would recurse forever. So
// the element must end (irHandleElemEnds): Unit, a leaf, a structural
// composite whose parts end, or a named declaration whose own handles end
// and none of which leads back (irReachesNominalChannel). The element's
// check then ends where those handles do. A handle whose element does lead
// back to a type the walk is deciding is decided before this rule, with
// the walk's stack (irHandleOnWalk).
func irHandleFieldKind(k kind) bool {
	elem, owner, isChannel := channelElem(k)
	if isChannel && owner == "" {
		return false
	}
	if !isChannel {
		var isTask bool
		if elem, isTask = taskElem(k); !isTask {
			return false
		}
	}
	return irHandleElemEnds(elem, nil) && irConcHandleKind(k)
}

// irHandleElemEnds reports whether a handle's element elem holds no handle
// that leads back to a declaration on path, the declarations whose walk is
// open (nil outside one): Unit, a leaf, a structural composite
// (`Map<String, Int>`, `List<List<Int>>`, `(Int, String)`) whose parts end,
// or a named declaration off the path whose own handles end. A function
// element ends when its parameters and result do. However deep the element
// nests, the walk ends: each named declaration it enters joins the path,
// and one already on it ends the walk there.
func irHandleElemEnds(elem kind, path typePath) bool {
	if elem == kindUnit || irRetainedLeafKind(elem) || irHeldKind(elem) {
		return true
	}
	if elem.tag == tagNamed && elem.def != nil {
		return !path.on(elem.def) && !irReachesNominalChannel(elem.def, path)
	}
	if elem.comp == nil {
		return false
	}
	for _, p := range elem.comp.parts {
		if !irHandleElemEnds(p, path) {
			return false
		}
	}
	return true
}

// irReachesNominalChannel reports whether d, through its unboxed fields,
// variant payloads, embedded types and a distinct's inner value, holds a
// channel or task handle whose element does not end (irHandleElemEnds): a
// handle of d itself, or a chain of handles that comes back round. A
// declaration it reaches that is already on path, other than d itself
// through its own fields, is such a chain or a cycle of declarations the
// handle check would follow round, and answers true, which declines.
func irReachesNominalChannel(d *typeDef, path typePath) bool {
	path = append(path, d)
	reaches := func(e *typeDef) bool {
		return path.on(e) || irReachesNominalChannel(e, path)
	}
	var visit func(k kind) bool
	visit = func(k kind) bool {
		elem, _, isHandle := channelElem(k)
		if !isHandle {
			elem, isHandle = taskElem(k)
		}
		if isHandle {
			// A handle of a declaration is admitted when that
			// declaration's own handles end, as its check will ask in
			// turn: `reply: Channel<Result<Int, String>>` inside the
			// element of `inbox: Channel<Request>`. One that leads back
			// to d is a cycle the check would follow forever.
			return !irHandleElemEnds(elem, path)
		}
		if k.tag == tagNamed && k.def != nil && k.def != d {
			return reaches(k.def)
		}
		// A structural composite's parts: `Both (Channel<E>, Int)`.
		for _, p := range kindParts(k) {
			if visit(p) {
				return true
			}
		}
		return false
	}
	if d.isDistinct && visit(d.inner) {
		// `type Inbox Sender<Request>`: the handle it wraps.
		return true
	}
	for i := range d.fields {
		if visit(d.fields[i].k) {
			return true
		}
	}
	for i := range d.variants {
		for _, p := range d.variants[i].payloads {
			if visit(p.k) {
				return true
			}
		}
		if e := d.variants[i].embeds; e != nil && reaches(e) {
			return true
		}
	}
	return false
}

// irStdMarkerKind is a std marker such as `ChannelClosed`, carried as a
// prelude payload: nothing inside it is read, and the VM holds it as a
// payload-free distinct.
func irStdMarkerKind(k kind) bool {
	if k.tag != tagNamed || k.def == nil {
		return false
	}
	for _, d := range stdMarkerDefs() {
		if d == k.def {
			return true
		}
	}
	return false
}

// channelCall lowers one `std/channels` call: its record names std's
// function owner.method (intrinsicArm), so a local type of the same name
// never reaches here.
func (bl *irScalarBuilder) channelCall(t *ast.Call, owner, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.rowsUnrecorded(t) || namedArgNode(t.Args) != nil {
		// call sends a call with a named argument to namedIntrinsicCall,
		// which comes back with the positional form, and wraps one in an
		// assertion subject in recordedQualCall.
		panic("irbuild: channelCall reached with a named argument or unrecorded rows: " + owner + "." + method)
	}
	fn, known := channelFuncs[owner+"."+method]
	if !known {
		panic("irbuild: channelCall: channelFuncs lists all five std/channels functions (buffered, unbuffered, send, close, receive) and the owner already resolved to std's declaration")
	}
	sig, solved := bl.g.channelSignature(t, fn, owner, method)
	if !solved {
		return bl.channelCtorFromTarget(t, fn, owner, method)
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok || len(args.kinds) != fn.args {
		return no()
	}
	for i, k := range args.kinds {
		if want := bl.g.project(sig.Params[i]); want != k {
			// A value entering the solved parameter: an empty literal takes
			// its type, a concrete value its interface (`Sender.send(tx, 7
			// as Display)` over a `Sender<Display>`).
			v, got, ok := bl.convertTo(t.Args[i], args.temps[i], k, want)
			if !ok {
				return no()
			}
			args.temps[i], args.kinds[i] = v, got
		}
	}
	result := bl.g.project(sig.Return)
	if result != kindUnit && !irCallableValueKind(result) {
		return no()
	}
	if fn.explicitTypeArg {
		_, got, isChannel := channelElem(result)
		if !isChannel || got != "Channel" {
			panic("irbuild: channelCall: an explicitTypeArg constructor (buffered/unbuffered) always answers Channel<T> from std's signature")
		}
	}
	return bl.concHostEmit(t, owner+"."+method, result, args.temps...)
}

// channelCtorFromTarget lowers a channel constructor whose callee's
// instantiation the checker did not record. It is the `Channel<T>` the
// checker recorded as the call's type (recordedKind), and its capacity is
// std's declared Int.
func (bl *irScalarBuilder) channelCtorFromTarget(t *ast.Call, fn channelFn, owner, method string) (ir.Temp, kind, bool, bool) {
	want, recorded := bl.g.recordedKind(t)
	if fn.owner != "Channel" || !recorded {
		return ir.NoTemp, kindInvalid, false, false
	}
	if _, got, isChannel := channelElem(want); !isChannel || got != "Channel" || !irConcHandleKind(want) {
		// The checker types a Channel constructor's call as the Channel<T>
		// std declares it to answer.
		panic("irbuild: channelCtorFromTarget: a Channel constructor recorded as " + want.nomi())
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok || len(args.kinds) != fn.args {
		return ir.NoTemp, kindInvalid, false, false
	}
	for _, k := range args.kinds {
		if k != kindInt {
			return ir.NoTemp, kindInvalid, false, false
		}
	}
	return bl.concHostEmit(t, owner+"."+method, want, args.temps...)
}

// channelHalf reads a Channel's `sender` or `receiver`.
func (bl *irScalarBuilder) channelHalf(at ast.Node, name string, subj ir.Temp, k kind, mobile bool) (ir.Temp, kind, bool, bool) {
	f := k.def.field(name)
	if f == nil || f.boxed || !irConcHandleKind(f.k) {
		return ir.NoTemp, kindInvalid, false, false
	}
	p := ir.NewProjField(bl.g.irNodePos(at), bl.f.NewTemp(), subj,
		bl.g.irTypes().Symbol(f, name), name, irParamShape(f.k))
	bl.b.Append(p)
	bl.side(p.Dst(), irScalarSide{k: f.k})
	return p.Dst(), f.k, mobile, true
}
