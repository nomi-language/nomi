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

// DeriveTargetAllowed reports whether `derive iface for T` may name the type
// decl declares, as the lowering judges it (`FromJson` takes no enum).
func DeriveTargetAllowed(iface string, decl ast.Node) bool {
	return len(validateDeriveTarget(iface, decl, 0, 0)) == 0
}
