package analysis

import "fmt"

// std/parse's `ParseFields` is implemented by `Unit`, with an impl std
// writes, and by an anonymous struct whose every field implements `Parse` and
// `Display` (parseFieldNeeds), which no impl can say: the compiler supplies
// the record's functions (internal/irbuild/irparsefields.go). It is the bound
// on std/http's `Route.to_with` and `Route.path_with`, so `Route<{id: Uuid}>`
// reads its parameters and writes them into links. What the record asks of
// its fields is structuralDemands', as for every interface an anonymous
// struct meets through its fields.

// isParseFields reports whether iface is std/parse's ParseFields.
func isParseFields(iface *InterfaceType) bool {
	return iface != nil && iface.Name == "ParseFields" && (iface.Origin == "" || iface.Origin == "std/parse")
}

// parseFieldFault is the interface a field of type t lacks to be a
// ParseFields field, or "" when it has both.
func (c *checker) parseFieldFault(t Type, site Recording) string {
	for _, need := range parseFieldNeeds {
		if d := fieldDemand(t, need); !typeImplementsInterface(c, d.t, d.iface, site) {
			return need.Name
		}
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
		if lack := c.parseFieldFault(f.Type, Recording{Kind: RecordingKindCallSite}); lack != "" {
			return fmt.Sprintf("each field's type implements `Parse` and `Display`, and `%s`, the type of `%s`, does not implement `%s`", f.Type, f.Name, lack), true
		}
	}
	return "", false
}
