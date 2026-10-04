package irbuild

// irStructFieldNames is the Nomi field name each of a struct's operands is the
// value of, in DECLARATION order — the list `ir.MakeStruct` carries and the
// field names the struct's record ends up with.
func irStructFieldNames(d *typeDef) []string {
	names := make([]string, len(d.fields))
	for i := range d.fields {
		names[i] = d.fields[i].nomi
	}
	return names
}
