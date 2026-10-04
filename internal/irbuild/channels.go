package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `std/channels` — the two constructors on `Channel<T>` and the three
// operations on its halves.
//
// # Why this is an owner-keyed arm and not the stdlib path
//
// sets.go's header states the rule and this file is its third case, with one
// difference: `Set` and `Range` reach the index and are refused
// there under `stdlib generic function`, whereas EVERY channel declaration is a
// `pub host fn ...<T>` — so `stdCandidateFor` short-circuits at `case generic:`
// before it asks anything about a signature, and the index carries a refusal for
// all five. stdgenstruct.go supplies `Channel<T>`'s kind and stdgenhost.go
// supplies the halves', but neither can supply the INSTANTIATION at a call site,
// which is what this file reads.
//
// # THE INSTANTIATION COMES FROM THE CHECKER, NOT FROM THIS FILE
//
// setCall and rangeCall derive the element type from the RECEIVER's lowered
// kind, because a Set method always takes a Set. That does not work here:
// `Channel.buffered<Int>(4)` takes an Int and returns a `Channel<Int>`, so no
// argument carries the element type at all — which is exactly why the turbofish
// is mandatory and why dropping it is a front-end error rather than a style
// choice (`binding 'ch' type is not locally determined`).
//
// So the type argument is read off `Symbol.CallType`, the INSTANTIATED signature
// the checker solved at the call. prelude.go's `preludeArgs` reads the same
// channel and states the reason this is not merely convenient: "re-deriving the
// instantiation from the written arguments would be a second inference pass that
// could disagree with the front end about the same call". Over
// turbofish_test.nomi, the checker records
//
//	Channel.buffered<Int>(4)      (Int) -> Channel<Int>
//	Sender.send(ch.sender, 7)     (Sender<Int>, Int) -> Result<Unit, ChannelClosed>
//	Receiver.receive(ch.receiver) (Receiver<Int>) -> Maybe<Int>
//
// — every parameter and every result, already solved. So this file writes NO
// result table and NO element-type inference: it projects the checker's own
// answer through `g.project` and coerces each argument to the projected
// parameter. `Result<Unit, ChannelClosed>` is therefore not assembled here
// either, which is what keeps one shape of Result in the builder rather than
// two.
//
// # The one spelling that does not record an instantiation
//
// `ch: Channel<Int> = Channel.unbuffered()` records NO CallType: the binding's
// annotation is not propagated into the constructor's instantiation, the same
// case `preludeArgs` documents for `Outcome{parsed: Ok(9)}`. The annotation
// reaches the builder only as a coercion TARGET, so irchannels.go's
// channelCtorFromTarget takes the instantiation from the target published for
// the call, and declines when there is none.
//
// # THE SPLIT IS THE POINT AND NOTHING HERE MAY BYPASS IT
//
// std declares NO `Channel.send`, `Channel.receive` or `Channel.close`, and
// `close` is on `Sender` ALONE — a consumer closing what it reads ends the
// stream for every producer still writing, which is the exact bug the split
// exists to prevent. So channelFuncs keys its operations on `Sender` and
// `Receiver`, and the whole of the `Channel` surface is two constructors. An arm
// reachable from the pair would make the split bypassable, and being bypassable
// is the only way the split could fail to pay.
//
// `unlowered Channel function` and its two siblings are how a method std DOES
// declare and this builder does NOT lower reports — listCall's rule — so a std
// edit adding a fourth operation is named rather than reported as `qualified
// call`, which would say "no such thing" when the truth is "std declares that".
//
// # No equality, hashing or rendering
//
// std declares no `Equatable`, no `Hashable`, no `Display` and no `Debug` for
// `Channel<T>` or either half, so `a == b` on one and a `Map<Sender<Int>, _>`
// refuse under the ordinary named-type keys. `rtOpaque` on the halves' defs is
// what makes them refuse rather than answer: without it, the arms written for a
// zero-sized marker would answer for a value with contents, and `a == b` would
// be constantly true (see stdhost.go).

// channelFn is one channel function this builder lowers.
//
// No result tag and no element-type source, because both come from the
// checker's instantiated signature — see the file header. What a row carries is
// the rt symbol, the arity, and whether Go needs the type argument written out.
type channelFn struct {
	// owner is the Nomi type qualifying the call: `Channel`, `Sender` or
	// `Receiver`. Part of the row rather than implied, because which of the
	// three owns a function IS the design question this type encodes.
	owner string
	// rtCall is the rt function, which takes the same arguments in the same
	// order as the Nomi declaration.
	rtCall string
	// args is the Nomi arity.
	args int
	// explicitTypeArg is whether the call's Go-spelled `expr` names the type
	// argument. True for exactly the two constructors, and for the same reason
	// the Nomi side needs a turbofish: no parameter mentions T, so Go's own
	// inference cannot supply it either.
	explicitTypeArg bool
	// frame is whether the rt function takes `fr` ahead of the Nomi arguments.
	//
	// True for exactly the two operations that BLOCK. A blocking wait is where
	// Nomi observes cancellation — std/channels documents both as ending early
	// when the enclosing block's context does — and the frame is where the
	// context travels. `Sender.close` and the two constructors never block, so
	// they take no frame and could not use one.
	frame bool
}

// channelFuncs is the lowered surface, and it is the WHOLE surface std declares:
// two constructors on the pair and three operations on the halves.
var channelFuncs = map[string]channelFn{
	"Channel.buffered":   {owner: "Channel", rtCall: "rt.ChannelBuffered", args: 1, explicitTypeArg: true},
	"Channel.unbuffered": {owner: "Channel", rtCall: "rt.ChannelUnbuffered", args: 0, explicitTypeArg: true},
	"Sender.send":        {owner: "Sender", rtCall: "rt.SenderSend", args: 2, frame: true},
	"Sender.close":       {owner: "Sender", rtCall: "rt.SenderClose", args: 1},
	"Receiver.receive":   {owner: "Receiver", rtCall: "rt.ReceiverReceive", args: 1, frame: true},
}

// channelSpec is the `Channel` row of the generic-std-struct table, resolved by
// (origin, name) so a reorder cannot repoint it silently — setSpec's form.
var channelSpec = func() *stdGenStructSpec {
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		if s.origin == "std/channels" && s.nomi == "Channel" {
			return s
		}
	}
	return nil
}()

// channelElem reads the element kind off a `Channel<T>`, a `Sender<T>` or a
// `Receiver<T>`, and reports which of the three it was.
//
// By SPEC POINTER through genStructOf / genHostOf, never by rendered name: a
// user type spelled `Sender` has its own def and answers false, which is the
// half a name check gets wrong.
func channelElem(k kind) (elem kind, owner string, ok bool) {
	if spec, args, isStruct := genStructOf(k); isStruct && spec == channelSpec && len(args) == 1 {
		return args[0], "Channel", true
	}
	spec, args, isHost := genHostOf(k)
	if !isHost || len(args) != 1 {
		return kindInvalid, "", false
	}
	switch spec {
	case senderSpec:
		return args[0], "Sender", true
	case receiverSpec:
		return args[0], "Receiver", true
	}
	return kindInvalid, "", false
}

// --- the call arm ------------------------------------------------------------

// channelSignature is the INSTANTIATED signature the checker solved at this
// call, or nothing.
//
// Read off `Symbol.CallType` at the METHOD's position, which is where
// prelude.go's preludeAt reads a constructor's — the qualifier `Channel` is a
// type reference and carries no call.
//
// A miss is the one spelling that records no instantiation (see the file
// header), and this function does not refuse for it: the caller routes to
// channelCtorFromTarget, which answers the constructor case and carries the
// refusal for every other, so one position reports once.
//
// The arity check here is against the DECLARED row rather than the written
// arguments, so a signature the checker solved to a different shape than std
// declares — which would mean this file's row and std's declaration disagree —
// misses instead of lowering against the row's assumption about which parameter
// is which. That lands on channelCtorFromTarget, whose own arity handling is
// std's declaration and not the checker's, so the disagreement still cannot
// produce a wrong call.
func (g *gen) channelSignature(t *ast.Call, fn channelFn, owner, method string) (*analysis.FuncType, bool) {
	fa, isField := t.Func.(*ast.FieldAccess)
	if !isField || fa.Field == nil || g.fa == nil {
		return nil, false
	}
	sym := g.fa.References[analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
	if sym == nil || sym.CallType == nil {
		return nil, false
	}
	sig, isFunc := sym.CallType.(*analysis.FuncType)
	if !isFunc || len(sig.Params) != fn.args || sig.Return == nil {
		return nil, false
	}
	return sig, true
}
