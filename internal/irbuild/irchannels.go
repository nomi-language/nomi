package irbuild

// `std/channels` in retained bodies.
//
// Channel.buffered, Channel.unbuffered, Sender.send, Sender.close and
// Receiver.receive are host crossings named `Owner.method`, spelled with an
// `rt` target and the checker's solved signature; a constructor whose
// signature the checker did not solve takes it from the coercion target (see
// channelCtorFromTarget). A `Channel<T>`'s `sender` and `receiver` are field
// projections spelled `(ch).Sender`. The VM runs an `rt` channel and holds a
// Channel as std's `channels.Channel` struct of its two halves.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irConcHandleKind is a task or channel handle over a retained payload:
// `Task<T>`, `Sender<T>`, `Receiver<T>` or the `Channel<T>` holding a pair.
func irConcHandleKind(k kind) bool {
	payload := func(p kind) bool { return p == kindUnit || irRetainedValueKind(p) }
	if elem, owner, isChannel := channelElem(k); isChannel {
		return owner != "" && payload(elem)
	}
	if elem, isTask := taskElem(k); isTask {
		return payload(elem)
	}
	return false
}

// irChannelFieldKind is a channel handle held in a struct field or a
// struct-shaped variant's field: `inbox: Channel<Request>`,
// `Get {reply: Channel<Int>}`.
//
// A channel is a handle, so the layout never recurses through it, but the
// admission check does: the element's own fields are checked in turn, and
// `enum Request { Get {reply: Channel<Request>} }` would recurse forever. So
// the element is Unit, a leaf, or a named declaration that reaches no channel
// field of its own whose element is anything but Unit or a leaf. The
// element's check then ends at those leaf channels. Composite elements stay
// outside.
func irChannelFieldKind(k kind) bool {
	elem, owner, isChannel := channelElem(k)
	if !isChannel || owner == "" {
		return false
	}
	if elem == kindUnit || irRetainedLeafKind(elem) {
		return irConcHandleKind(k)
	}
	if elem.tag != tagNamed || elem.def == nil || irReachesNominalChannel(elem.def, 0) {
		return false
	}
	return irConcHandleKind(k)
}

// irReachesNominalChannel reports whether d, through its unboxed fields,
// variant payloads and embedded types, holds a channel whose element is not
// Unit or a leaf. It answers true past a small depth, which only declines.
func irReachesNominalChannel(d *typeDef, depth int) bool {
	if depth > 8 {
		return true
	}
	visit := func(k kind) bool {
		if elem, _, isChannel := channelElem(k); isChannel {
			return elem != kindUnit && !irRetainedLeafKind(elem)
		}
		if k.tag == tagNamed && k.def != nil && k.def != d {
			return irReachesNominalChannel(k.def, depth+1)
		}
		return false
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
		if e := d.variants[i].embeds; e != nil && irReachesNominalChannel(e, depth+1) {
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

// channelCall lowers one `std/channels` call. Its owner resolved to std's
// declaration (stdIntrinsicOwner), so a local type of the same name never
// reaches here.
func (bl *irScalarBuilder) channelCall(t *ast.Call, owner, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.rowsUnrecorded(t) || namedArgNode(t.Args) != nil {
		return no()
	}
	fn, known := channelFuncs[owner+"."+method]
	if !known {
		return no()
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
		if bl.g.project(sig.Params[i]) != k {
			return no()
		}
	}
	result := bl.g.project(sig.Return)
	if result != kindUnit && !irCallableValueKind(result) {
		return no()
	}
	if fn.explicitTypeArg {
		_, got, isChannel := channelElem(result)
		if !isChannel || got != "Channel" {
			return no()
		}
	}
	return bl.concHostEmit(t, owner+"."+method, result, args.temps...)
}

// channelCtorFromTarget lowers a channel constructor whose instantiation the
// checker did not record. It takes the instantiation from the coercion target
// published for the call, and its capacity is std's declared Int.
func (bl *irScalarBuilder) channelCtorFromTarget(t *ast.Call, fn channelFn, owner, method string) (ir.Temp, kind, bool, bool) {
	want, wanted := bl.g.channelWantedInstance(t)
	if fn.owner != "Channel" || !wanted || !irCallableValueKind(want) {
		return ir.NoTemp, kindInvalid, false, false
	}
	_, got, isChannel := channelElem(want)
	if !isChannel || got != "Channel" {
		return ir.NoTemp, kindInvalid, false, false
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
