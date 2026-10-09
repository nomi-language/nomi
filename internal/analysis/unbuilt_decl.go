package analysis

// An instance of a generic struct, enum or interface (`Result<Int, String>`)
// is a copy of its declaration's type with TypeArgs filled in. The copy
// carries the declaration's body: an enum's Variants, a struct's Fields, an
// interface's Methods and Fields, and each one's TypeParams and
// TypeParamDefs. The body is built in BuildTypes, and a type annotation that
// names the declaration can be resolved before that: a signature in a file
// built earlier (std/maybe.nomi's `Maybe.to_result` returns std/results'
// Result, and the two files import each other), a field declared above the
// generic type it names, or a recursive variant (`Node(Tree<T>)`, resolved
// while Tree is being built). No order of building fixes the last one.
//
// So a declaration's shell carries an unbuiltDecl until its body is built.
// Every instance taken from it in that window is recorded there, and is
// completed with the body when the declaration is built.

// unbuiltDecl is the set of instances taken from one declaration whose body
// is not built yet, and for a distinct type what waits on its inner type.
// The shell and every recorded instance point at it; a built declaration and
// a completed instance have none.
type unbuiltDecl struct {
	instances []Type
	// onBuilt runs when the declaration is built (whenBuilt).
	onBuilt []func()
}

// recordInstance notes inst, taken from a declaration that is not built yet,
// so the declaration completes it when it is built. A nil u is a built
// declaration and records nothing.
func (u *unbuiltDecl) recordInstance(inst Type) *unbuiltDecl {
	if u != nil {
		u.instances = append(u.instances, inst)
	}
	return u
}

// enumBuilt completes every instance taken from et before its body was
// built, and marks et built.
func enumBuilt(et *EnumType) {
	u := et.unbuilt
	et.unbuilt = nil
	if u == nil {
		return
	}
	for _, inst := range u.instances {
		if inst, ok := inst.(*EnumType); ok {
			inst.Variants = et.Variants
			inst.TypeParams = et.TypeParams
			inst.TypeParamDefs = et.TypeParamDefs
			inst.unbuilt = nil
		}
	}
}

// structBuilt is enumBuilt for a struct.
func structBuilt(st *StructType) {
	u := st.unbuilt
	st.unbuilt = nil
	if u == nil {
		return
	}
	for _, inst := range u.instances {
		if inst, ok := inst.(*StructType); ok {
			inst.Fields = st.Fields
			inst.TypeParams = st.TypeParams
			inst.TypeParamDefs = st.TypeParamDefs
			inst.unbuilt = nil
		}
	}
}

// interfaceBuilt is enumBuilt for an interface.
func interfaceBuilt(it *InterfaceType) {
	u := it.unbuilt
	it.unbuilt = nil
	if u == nil {
		return
	}
	for _, inst := range u.instances {
		if inst, ok := inst.(*InterfaceType); ok {
			inst.Methods = it.Methods
			inst.TypeParams = it.TypeParams
			inst.TypeParamDefs = it.TypeParamDefs
			inst.SelfParam = it.SelfParam
			inst.unbuilt = nil
		}
	}
}

// A distinct type's inner type is set when its declaration is built, and an
// enum that `embeds` it reads that inner type as the variant's payload
// (`embeds UserId` where `type UserId Int` carries an Int). An enum built
// before the distinct type, above it in the file or in a file built
// earlier, would read a shell with no inner type and take the variant for a
// zero-sized one. So the enum hands the shell what to do once the inner type
// is known (whenBuilt), and building the distinct type runs it
// (distinctBuilt).

// whenBuilt runs f when the declaration is built: now, when u is nil (the
// declaration is built), or else from its *Built function.
func (u *unbuiltDecl) whenBuilt(f func()) {
	if u == nil {
		f()
		return
	}
	u.onBuilt = append(u.onBuilt, f)
}

// distinctBuilt marks dt built and runs what waited on its inner type.
func distinctBuilt(dt *DistinctType) {
	u := dt.unbuilt
	dt.unbuilt = nil
	if u == nil {
		return
	}
	for _, f := range u.onBuilt {
		f()
	}
}
