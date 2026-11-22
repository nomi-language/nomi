package analysis

import "github.com/nomi-language/nomi/internal/ast"

// ImplBlockTypeArgs is the record of impl block ib (ImplTypeArgs): the
// identity of the interface it implements (Interface, Origin), its
// receiver as the type builder resolved it, and the interface's type
// arguments it implements (from the header, or from the items' signatures
// against the interface's; none for an interface with no type parameters).
// fa is the analysis of the file that writes ib. Every impl block of a
// file's declarations, written, derived or synthesized, has one, a std
// file's built from its interface included, since the type builder writes
// it from the block's header; nil for a block whose interface resolved to
// nothing, and for the universal Debug of a type declared in a body
// (FileBodies.BlockDebug), which is std's Debug by construction. The IR
// builder reads a block's interface and instantiates it from this
// (internal/irbuild, ifaceinst.go) and solves nothing itself.
// TestElaborationGate holds every block to having one.
func ImplBlockTypeArgs(fa *FileAnalysis, ib *ast.ImplBlock) *ImplTypeArgs {
	if fa == nil || ib == nil {
		return nil
	}
	for _, blocks := range fa.ImplBlocks[TypeExprBaseName(ib.Receiver)] {
		for _, info := range blocks {
			if info.Block == ib {
				return info
			}
		}
	}
	return nil
}

// Std is the std interface the block implements, the zero StdType for a
// program's interface.
func (a *ImplTypeArgs) Std() StdType {
	if a == nil || !IsStdlibKey(a.Origin) {
		return StdType{}
	}
	return StdType{Module: a.Origin, Name: a.Interface}
}
