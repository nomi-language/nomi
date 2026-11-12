package analysis

// CallTypeArgs is the type arguments the checker instantiated a generic
// callee at, at ref: the reference a call records at its callee's name,
// or the one a generic function used as a value records. Each of the
// callee's type parameters the instantiation fixes is keyed by its name
// and elaborated (Elaborate), so a variable the checker closed reads as
// its closed type. nil when ref carries no instantiation (CallType).
//
// A type argument is read off the instantiated signature, matched against
// the callee's declared one (elabMatch), or else taken from the
// substitution the checker recorded at the call (Symbol.CallTypeArgs),
// which holds a parameter the signature does not show: `T` of `fn first<T,
// I>(it: I) -> Int where I: Iter<T>`, which the impl the bound's subject
// has fixes. Both are the checker's answer; nothing here solves.
//
// The IR builder instantiates every generic call from this (internal/irbuild
// package doc, "Instantiation and dispatch"), and VerifyElaboration holds every generic
// call outside derived bodies to having each of its callee's parameters here.
func CallTypeArgs(ref *Symbol) map[string]Type {
	if ref == nil || ref.CallType == nil {
		return nil
	}
	out := map[string]Type{}
	if declFT := calleeDeclFuncType(ref); declFT != nil {
		if ct, ok := resolveTypeVar(Elaborate(ref.CallType)).(*FuncType); ok {
			solved := map[*TypeParam_]Type{}
			for i := range declFT.Params {
				if i < len(ct.Params) {
					elabMatch(declFT.Params[i], ct.Params[i], solved, 0)
				}
			}
			elabMatch(declFT.Return, ct.Return, solved, 0)
			for p, t := range solved {
				out[p.Name_] = Elaborate(t)
			}
		}
	}
	for name, t := range ref.CallTypeArgs {
		if _, ok := out[name]; !ok && t != nil {
			out[name] = Elaborate(t)
		}
	}
	return out
}

// calleeDeclFuncType is the declared, uninstantiated function type of the
// callee ref names, or nil.
func calleeDeclFuncType(ref *Symbol) *FuncType {
	base := ref
	for base.Resolved != nil && base.Resolved != base {
		base = base.Resolved
	}
	decl := base.Type
	if decl == nil {
		decl = ref.Type
	}
	ft, _ := decl.(*FuncType)
	return ft
}
