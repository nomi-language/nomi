package analysis

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
)

// DerivableInterfaces lists the interfaces `derive` synthesizes, in the
// order the lowering's "unknown derivable protocol" error names them.
func DerivableInterfaces() []string {
	return slices.Clone(deriveSupportedList)
}

// DeriveHomeModule is the stdlib module that declares a derivable
// interface (`std/json` for `ToJson`), or "".
func DeriveHomeModule(iface string) string {
	return deriveProtocolHomeModule[iface]
}

// DerivedJSONKeys reports whether decl, a type declaration, derives iface
// (`ToJson` or `FromJson`), and the JSON object key the derived impl uses
// for a field of the given Nomi name, after its `rename_all` option.
func DerivedJSONKeys(decl ast.Node, iface string) (key func(field string) string, derived bool) {
	var decorators []ast.Decorator
	switch d := decl.(type) {
	case *ast.StructDef:
		decorators = d.Decorators
	case *ast.EnumDef:
		decorators = d.Decorators
	case *ast.TypeDef:
		decorators = d.Decorators
	default:
		return nil, false
	}
	for _, dec := range decorators {
		if dec.Name != "derive" {
			continue
		}
		for _, arg := range dec.Args {
			if st, ok := arg.(*ast.SimpleType); ok && st.Name == iface {
				opts := parseDeriveOptionsForSynthesis(iface, dec.Options)
				return func(field string) string { return jsonFieldName(field, opts) }, true
			}
		}
	}
	return nil, false
}
