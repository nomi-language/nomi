package analysis

import "fmt"

// std/parse's `ParseFields` is implemented by `Unit`, with an impl std
// writes, and by an anonymous struct whose every field implements `Parse` and
// `Display`, which no impl can say: the compiler supplies the record's
// functions (internal/irbuild/irparsefields.go). It is the bound on std/http's
// `Route.to_with` and `Route.path_with`, so `Route<.{id: Uuid}>` reads its
// parameters and writes them into links.

// isParseFields reports whether iface is std/parse's ParseFields.
func isParseFields(iface *InterfaceType) bool {
	return iface != nil && iface.Name == "ParseFields" && (iface.Origin == "" || iface.Origin == "std/parse")
}

// structuralParseFields decides whether concrete, an anonymous struct,
// implements ParseFields. decided is false for any other type or
// interface, whose conformance is decided as usual.
func (c *checker) structuralParseFields(concrete Type, iface *InterfaceType, recPos Pos, recKind RecordingKind) (implements, decided bool) {
	if !isParseFields(iface) {
		return false, false
	}
	anon, ok := resolveTypeVar(concrete).(*AnonStructType)
	if !ok {
		return false, false
	}
	for _, f := range anon.Fields {
		if c.parseFieldFault(f.Type, recPos, recKind) != "" {
			return false, true
		}
	}
	return true, true
}

// parseFieldFault is the interface a field of type t lacks to be a
// ParseFields field, or "" when it has both.
func (c *checker) parseFieldFault(t Type, recPos Pos, recKind RecordingKind) string {
	if !typeImplementsInterface(c, t, &InterfaceType{Name: "Parse", Origin: "std/parse"}, recPos, recKind) {
		return "Parse"
	}
	if !typeImplementsInterface(c, t, &InterfaceType{Name: "Display", Origin: "std/display"}, recPos, recKind) {
		return "Display"
	}
	return ""
}

// parseFieldsHelp is the help for an anonymous struct that is not
// ParseFields: the first field whose type lacks Parse or Display.
func (c *checker) parseFieldsHelp(concrete Type) (string, bool) {
	anon, ok := resolveTypeVar(concrete).(*AnonStructType)
	if !ok {
		return "", false
	}
	for _, f := range anon.Fields {
		if lack := c.parseFieldFault(f.Type, Pos{}, RecordingKindCallSite); lack != "" {
			return fmt.Sprintf("each field's type implements `Parse` and `Display`, and `%s`, the type of `%s`, does not implement `%s`", f.Type, f.Name, lack), true
		}
	}
	return "", false
}
