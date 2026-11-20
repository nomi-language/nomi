package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// callDefaults fills the parameters a call leaves out: explicit values are
// already evaluated, then defaults see module functions and earlier
// parameters, never caller locals.
// Module value references remain outside this retained path.
func (bl *irScalarBuilder) callDefaults(sig *fnSig, args []ir.Temp) bool {
	return bl.callDefaultsThrough(sig, args, len(args))
}

// callDefaultsThrough is callDefaults over the first n parameters only: a
// partial application's open slot needs its own default and the defaults
// before it, whatever the slots after it hold.
func (bl *irScalarBuilder) callDefaultsThrough(sig *fnSig, args []ir.Temp, n int) bool {
	if len(sig.decl.Params) != len(args) || n > len(args) {
		panic("irbuild: callDefaultsThrough: every caller builds args at the declaration's parameter count, and a partial's slot is one of them")
	}
	bound, kinds, parent, defaults := bl.bound, bl.boundK, bl.parent, bl.defaultScope
	scopes := bl.g.scopes
	bl.bound, bl.boundK = map[string]ir.Temp{}, map[string]kind{}
	bl.parent, bl.defaultScope = nil, true
	bl.g.scopes = []map[string]local{scopes[0], {}}
	defer func() {
		bl.bound, bl.boundK, bl.parent, bl.defaultScope = bound, kinds, parent, defaults
		bl.g.scopes = scopes
	}()
	if inst := sig.instance; inst != nil {
		// A generic callee's default is lowered at the instance's type
		// arguments: `f: (T) -> T = fn y -> y` takes its parameter type from T.
		bl.g.pushMonoSubst(inst)
		defer bl.g.popMonoSubst()
	}
	for i, p := range sig.decl.Params[:n] {
		if args[i] == ir.NoTemp {
			if p.Default == nil {
				return false
			}
			var src ir.Temp
			var ok bool
			if o, iface := bl.g.ifaceParamDefault(p.Default); iface {
				// An interface function's default, in a frame binding its
				// type parameters; another file's has that file's names
				// (ifaceslotdefault.go).
				frame, bound := sig.defaultFrame, len(sig.defaultFrame) > 0 || !ifaceDefaultGeneric(o)
				switch {
				case !bound:
					return false
				case o.unit != bl.g.fileUnit:
					src, _, ok = bl.ifaceSlotDefault(o, p, sig.params, frame, sig.defaultTypes, args, i)
				default:
					// Lowered for the call's implementor, as the
					// interface's default bodies are (gen.defaultSelf).
					prev := bl.g.defaultSelf
					if ds := bl.g.paramDefaultSelf(o, sig); ds != nil {
						bl.g.defaultSelf = ds
					}
					src, _, ok = bl.defaultInFrame(p, frame, sig.defaultTypes, sig.params[i])
					bl.g.defaultSelf = prev
				}
			} else {
				src, _, ok = bl.defaultInFrame(p, sig.defaultFrame, sig.defaultTypes, sig.params[i])
			}
			// Each route lowers at the parameter's kind (lowerWant, or the
			// declaring gen's accessor at it), so k is sig.params[i].
			if !ok {
				return false
			}
			args[i] = src
		}
		if p.Destructure == nil && !ast.IsDiscardName(p.Name) {
			bl.bound[p.Name], bl.boundK[p.Name] = args[i], sig.params[i]
		}
		if p.Destructure != nil && slices.Contains(args[i+1:n], ir.NoTemp) {
			if !bl.bindParamPattern(p.Destructure, args[i], sig.params[i]) {
				return false
			}
		}
	}
	return true
}

// paramDefaultSelf is the implementor the defaults of interface function o,
// which this file declares, are filled for: the kind sig passes at the
// declaration's receiver (its first `self` parameter) and the call's
// record of it (fnSig.defaultSelfType). Nil when the declaration has no
// receiver or this gen has no def of the interface.
func (g *gen) paramDefaultSelf(o ifaceParamDefault, sig *fnSig) *irDefaultSelf {
	if g.fa == nil {
		return nil
	}
	d, found := g.solvedIface(g.fa.Origin, o.iface.Name)
	at := selfShapeOf(sig.decl.Params, nil).recvAt()
	if !found || d == nil || d.decl != o.iface || at < 0 || at >= len(sig.params) {
		return nil
	}
	return &irDefaultSelf{iface: d, recv: sig.params[at], typ: sig.defaultSelfType}
}

// bindParamPattern binds the names destructuring parameter pattern pat
// introduces from its argument arg, as the callee's prologue binds them for
// its body, so a later parameter's default may read them
// (`fn area((w, h): (Int, Int), scale: Int = w)`). The pattern is
// irrefutable, so its mismatch edge is a NoMatch no value reaches.
func (bl *irScalarBuilder) bindParamPattern(pat ast.Node, arg ir.Temp, k kind) bool {
	if !irHeldValue(bl.f, arg, bl.sides) {
		c := ir.NewCopy(bl.g.irNodePos(pat), bl.f.NewTemp(), arg)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: k, copy: irCopyHold})
		arg = c.Dst()
	}
	pos := bl.g.irNodePos(pat)
	cont := bl.f.NewBlock(pos, "parameter destructured")
	nomatch := bl.f.NewBlock(pos, "pattern mismatch")
	nomatch.Append(ir.NewNoMatch(pos))
	nomatch.SetTerm(ir.NewJump(pos, cont.ID()))
	if !bl.patternValueTest(pat, arg, k, cont, nomatch) {
		bl.g.dc.patternFailed(pat)
		return false
	}
	bl.b = cont
	return true
}
