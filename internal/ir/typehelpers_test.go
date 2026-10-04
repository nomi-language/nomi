package ir

// typedParam declares a parameter of f with the value type ty, the way a
// producer does: the shape the operand-shape rule reads, and the stored type.
func typedParam(f *Func, sym *Symbol, ty *ValType) Temp {
	t := f.AddParam(sym, ty.Shape())
	f.SetType(t, ty)
	return t
}

// valued states ty's value type and answers ty, for a fixture's slot or cell.
func valued(ty *Type, v *ValType) *Type {
	ty.SetVal(v)
	return ty
}
