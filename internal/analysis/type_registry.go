package analysis

import "strings"

// TypeRegistry maps type names to their Type representations.
// Registries form a chain via Parent so that nested type declarations
// (struct/enum/typealias/interface inside a function body) can be
// resolved against an enclosing scope without polluting it.
type TypeRegistry struct {
	types  map[string]Type
	parent *TypeRegistry
}

func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{types: map[string]Type{
		"Int":        TypeInt,
		"Float":      TypeFloat,
		"Decimal":    TypeDecimal,
		"String":     TypeString,
		"Bool":       TypeBool,
		"Unit":       TypeUnit,
		"Infallible": TypeInfallible,
		"Any":        TypeAny,
		// True and False are zero-sized primitive singleton types declared in
		// bool.nomi itself, right beside the `embeds True` / `embeds False`
		// variants that consume them. They are pre-registered here so those
		// `embeds` clauses resolve where the declarations live in the same
		// file.
		// User code reaches True/False as Bool's enum variants via
		// prelude.nomi's `std/bool.Bool.{self, False, True}` import line.
		"True":  TypeTrue,
		"False": TypeFalse,
	}}
}

// NewChildTypeRegistry creates an empty child registry that falls back to
// parent for unknown names.
func NewChildTypeRegistry(parent *TypeRegistry) *TypeRegistry {
	return &TypeRegistry{types: map[string]Type{}, parent: parent}
}

func (r *TypeRegistry) Lookup(name string) Type {
	if t, ok := r.types[name]; ok {
		return t
	}
	if r.parent != nil {
		return r.parent.Lookup(name)
	}
	return nil
}

// Names are every type name the registry and its parents hold.
func (r *TypeRegistry) Names() []string {
	var names []string
	for reg := r; reg != nil; reg = reg.parent {
		for name := range reg.types {
			names = append(names, name)
		}
	}
	return names
}

func (r *TypeRegistry) Register(name string, typ Type) {
	r.types[name] = typ
}

// SuggestDotted finds a registered name whose last segment is `bare`. A
// dotted type carries its qualifier as part of its name, so someone who
// imported `Probe.Reading` and then wrote `Reading` has named nothing —
// pointing at the whole name is more useful than reporting it unknown.
func (r *TypeRegistry) SuggestDotted(bare string) string {
	suffix := "." + bare
	for reg := r; reg != nil; reg = reg.parent {
		for name := range reg.types {
			if strings.HasSuffix(name, suffix) {
				return name
			}
		}
	}
	return ""
}
