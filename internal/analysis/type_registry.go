package analysis

import "strings"

// TypeRegistry maps type names to their Type representations.
// Registries form a chain via Parent so that nested type declarations
// (struct/enum/typealias/interface inside a function body) can be
// resolved against an enclosing scope without polluting it.
type TypeRegistry struct {
	types  map[string]Type
	parent *TypeRegistry
	// lazy holds a builder per name whose type is not built yet; Lookup
	// runs it on the first miss (RegisterLazy).
	lazy map[string]func()
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
	if build, ok := r.lazy[name]; ok {
		delete(r.lazy, name)
		build()
		if t, ok := r.types[name]; ok {
			return t
		}
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

// RegisterLazy makes build run at the first Lookup of name that finds no
// type, and once at most. build is expected to Register the name. A name
// that already has a lazy builder keeps the first.
func (r *TypeRegistry) RegisterLazy(name string, build func()) {
	if r.lazy == nil {
		r.lazy = map[string]func(){}
	}
	if _, ok := r.lazy[name]; !ok {
		r.lazy[name] = build
	}
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
