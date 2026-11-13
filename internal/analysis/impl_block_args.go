package analysis

import "github.com/nomi-language/nomi/internal/ast"

// ImplBlockTypeArgs is the record of the interface type arguments impl
// block ib implements, as the type builder solved them (ImplTypeArgs: from
// the header, or from the items' signatures against the interface's), or
// nil when fa records none for ib. fa is the analysis of the file that
// writes ib. The IR builder instantiates the block's interface from this
// (internal/irbuild, ifaceinst.go) and solves nothing itself.
func ImplBlockTypeArgs(fa *FileAnalysis, ib *ast.ImplBlock) *ImplTypeArgs {
	if fa == nil || ib == nil {
		return nil
	}
	for _, byIface := range fa.ImplTypeArgSets {
		for _, set := range byIface {
			for _, info := range set {
				if info != nil && info.Block == ib {
					return info
				}
			}
		}
	}
	return nil
}
