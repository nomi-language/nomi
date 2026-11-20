package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// implBlockFor is the impl block of the interface iface (declared by the
// file of origin) for recv, the receiver's type: the one block for its
// type, or among several, the one its type arguments select
// (implCandidates), and where recv implements the interface at several
// type arguments, the one at ifaceArgs, the interface instance the call
// goes through (nil when the call names none). Nil when recv is no known
// type or no single block answers. The one resolver of which impl of an
// interface a type has: a call's (interfaceImplAt), an operator's and a
// rendering's (implMethodFor).
func (fa *FileAnalysis) implBlockFor(recv Type, iface, origin string, ifaceArgs []Type) *ast.ImplBlock {
	t, _ := knownReceiver(recv)
	if t == nil || iface == "" {
		return nil
	}
	blocks := writtenOverSynthesized(fa.implBlocksOf(t, iface, origin))
	if len(blocks) == 1 {
		return blocks[0].Block
	}
	if len(blocks) == 0 {
		return nil
	}
	var found *ast.ImplBlock
	for _, cand := range fa.implCandidates(t, iface, origin) {
		if ifaceArgs != nil && !typesAllEqual(cand.args, ifaceArgs) {
			// `Holds.holds(b, x)` at a Box that implements Holds<Int>
			// and Holds<String>: the block of the call's instance.
			continue
		}
		for _, b := range blocks {
			if cand.info.Block != b.Block {
				continue
			}
			if found != nil && found != b.Block {
				return nil
			}
			found = b.Block
		}
	}
	return found
}

// writtenOverSynthesized is blocks with the synthesized ones left out when
// a written one is among them. A hand-written `impl Debug` ranks above a
// derived or synthesized one (spec §12): the declaring file's universal
// `impl Debug for Box` yields to the `impl Debug for Box` another file of
// the program writes.
func writtenOverSynthesized(blocks []*ImplTypeArgs) []*ImplTypeArgs {
	if len(blocks) < 2 {
		return blocks
	}
	var written []*ImplTypeArgs
	for _, b := range blocks {
		if !IsSynthesizedLine(b.Block.Line) {
			written = append(written, b)
		}
	}
	if len(written) == 0 {
		return blocks
	}
	return written
}

// ImplBlockArgsAt is the type each of impl block ib's type parameters
// stands for where ib serves recv, a receiver's type: ib's receiver as the
// type builder resolved it (ImplBlockTypeArgs), matched against recv, by
// the parameter's name. A subtype is its parent, whose impls it has
// (knownReceiver). fa is the analysis of the file that writes ib. ok is
// false when fa records no receiver for ib or recv does not match it.
// The IR builder reads which instance of a block a receiver reaches from
// it, and matches nothing by kinds itself.
func ImplBlockArgsAt(fa *FileAnalysis, ib *ast.ImplBlock, recv Type) (map[string]Type, bool) {
	info := ImplBlockTypeArgs(fa, ib)
	if info == nil || info.Receiver == nil || recv == nil {
		return nil, false
	}
	recv = resolveTypeVar(recv)
	for dt := refinementOf(recv); dt != nil && dt.Inner != nil; dt = refinementOf(recv) {
		recv = resolveTypeVar(DistinctInner(dt))
	}
	subs := map[*TypeParam_]Type{}
	if unifyFull(info.Receiver, recv, subs, fa.implsContext(), fa.implTypeArgsContext()) != nil {
		return nil, false
	}
	out := make(map[string]Type, len(subs))
	for p, t := range subs {
		out[p.Name_] = Substitute(t, subs)
	}
	return out, true
}

// ImplBlockServesAt reports whether impl block ib applies at the receiver
// its type parameters stand for at args (by name): whether those types meet
// the bounds its `where` puts on them, decided as the checker decides
// whether an impl applies at a receiver (implReceiverMatches). body is the
// analysis of the file that writes ib, and fa that of a file whose
// program's impls args' types have (a program file, for a std block at a
// program's types), as CalleeAt asks. decided is false when body records
// no receiver for ib or args leave one of its parameters open.
func ImplBlockServesAt(fa, body *FileAnalysis, ib *ast.ImplBlock, args map[string]Type) (serves, decided bool) {
	info := ImplBlockTypeArgs(body, ib)
	if info == nil || info.Receiver == nil {
		return false, false
	}
	recv, all := ReplaceTypeParams(info.Receiver, func(p *TypeParam_) (Type, bool) {
		t := args[p.Name_]
		return t, t != nil
	})
	if !all || ContainsTypeParam(recv) || containsTypeVar(recv) {
		return false, false
	}
	return implReceiverMatches(info, recv, fa.implsContext(), fa.implTypeArgsContext()), true
}

// implBlocksOf is every impl block of the interface iface (declared by the
// file of origin) for t, a nominal receiver type (nominalReceiver):
// those for a type of its name whose receiver is t's declaration (a
// program's `Error` is not std/json's, and a type declared inside a body,
// which no impl can name, has none of a std type of its name) and whose
// interface is the one origin declares, each once.
func (fa *FileAnalysis) implBlocksOf(t Type, iface, origin string) []*ImplTypeArgs {
	t, name := nominalReceiver(t)
	want, nominal := nominalOrigin(t)
	var out []*ImplTypeArgs
	seen := map[string]bool{}
	for _, table := range fa.implBlockTables() {
		for _, b := range table[name][iface] {
			if origin != "" && !sameNominalIdentity(origin, iface, b.Origin, iface) {
				continue
			}
			if r, ok := nominalOrigin(resolveTypeVar(b.Receiver)); nominal && ok && r != want {
				continue
			}
			// A block recorded by two analyses of one file is one.
			key := implBlockKey(b.Block)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, b)
		}
	}
	return out
}

// ownImpl reports whether impl, an operator's recorded impl, is one of t's,
// the operand's type (implOfOwnType). TestElaborationGate holds every
// operator record to it. An operator's interface is a name the prelude
// reserves, so any interface of the name is it.
func (fa *FileAnalysis) ownImpl(impl *OperatorImpl, t Type) bool {
	return impl != nil && fa.implOfOwnType(impl.Method, impl.Interface, OriginUnresolved, t)
}

// implOfOwnType reports whether m, an impl's function, is one of t's: the
// block that declares it is one of the blocks of the interface iface
// (declared by the file of origin, any of the name for OriginUnresolved)
// whose receiver is t's declaration (implBlocksOf), so not another
// module's type of t's name, nor, for a type declared inside a body, any
// type's.
func (fa *FileAnalysis) implOfOwnType(m ast.ImplMethodDecl, iface, origin string, t Type) bool {
	held := fa.implBlockHolding(m)
	if held == nil {
		return false
	}
	for _, b := range fa.implBlocksOf(t, iface, origin) {
		if b.Block == held || implBlockKey(b.Block) == implBlockKey(held) {
			return true
		}
	}
	return false
}

// implBlockHolding is the interface impl block that declares decl, an
// impl's `fn` or `host fn`, or nil for any other function.
func (fa *FileAnalysis) implBlockHolding(decl ast.Node) *ast.ImplBlock {
	if info := fa.implBlockInfoHolding(decl); info != nil {
		return info.Block
	}
	return nil
}

// implBlockInfoHolding is the record of the interface impl block that
// declares decl (implBlockHolding), with its resolved receiver.
func (fa *FileAnalysis) implBlockInfoHolding(decl ast.Node) *ImplTypeArgs {
	recv := ""
	switch d := decl.(type) {
	case *ast.FuncDef:
		recv = fa.ImplBlockReceiver[d]
		if p := fa.Program(); recv == "" && p != nil && p.ImplIndex != nil {
			recv = p.ImplIndex.ImplBlockReceiver[d]
		}
	case *ast.ExternFunc:
		recv = fa.ImplBlockReceiverExtern[d]
		if p := fa.Program(); recv == "" && p != nil && p.ImplIndex != nil {
			recv = p.ImplIndex.ImplBlockReceiverExtern[d]
		}
	}
	if recv == "" {
		return nil
	}
	return fa.implBlockInfoWhere(recv, func(b *ast.ImplBlock) bool {
		for _, item := range b.Items {
			if item == decl {
				return true
			}
		}
		return false
	})
}

// implBlockInfo is the record of b, an interface impl block, with its
// resolved receiver; nil where no table holds it.
func (fa *FileAnalysis) implBlockInfo(b *ast.ImplBlock) *ImplTypeArgs {
	if b == nil {
		return nil
	}
	for _, table := range fa.implBlockTables() {
		for _, byIface := range table {
			for _, blocks := range byIface {
				for _, info := range blocks {
					if info.Block == b {
						return info
					}
				}
			}
		}
	}
	return nil
}

// implBlockInfoWhere is the first record, among the impl blocks for a
// receiver named recv, of a block match accepts.
func (fa *FileAnalysis) implBlockInfoWhere(recv string, match func(*ast.ImplBlock) bool) *ImplTypeArgs {
	for _, table := range fa.implBlockTables() {
		for _, blocks := range table[recv] {
			for _, b := range blocks {
				if match(b.Block) {
					return b
				}
			}
		}
	}
	return nil
}

// implBlockTables is the impl block tables a lookup reads: the file's own,
// then the program's.
func (fa *FileAnalysis) implBlockTables() []map[string]map[string][]*ImplTypeArgs {
	tables := []map[string]map[string][]*ImplTypeArgs{fa.ImplBlocks}
	if p := fa.Program(); p != nil && p.ImplIndex != nil {
		tables = append(tables, p.ImplIndex.ImplBlocks)
	}
	return tables
}

// implBlockKey identifies an impl block by its source, so the blocks of two
// parses of one std file are one. Two blocks of different files at one
// position with one header for one receiver are a duplicate impl either
// way.
func implBlockKey(b *ast.ImplBlock) string {
	if b == nil || b.Interface == nil || b.Receiver == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%s:%s", b.Line, b.Col, b.Interface.TypeString(), b.Receiver.TypeString())
}

// recordImplBlock notes info, an interface impl block of the file with its
// resolved receiver, under its receiver's and its interface's base names
// (FileAnalysis.ImplBlocks).
func (fa *FileAnalysis) recordImplBlock(info *ImplTypeArgs, recv, iface string) {
	if recv == "" || iface == "" || info.Block == nil {
		return
	}
	if fa.ImplBlocks == nil {
		fa.ImplBlocks = map[string]map[string][]*ImplTypeArgs{}
	}
	if fa.ImplBlocks[recv] == nil {
		fa.ImplBlocks[recv] = map[string][]*ImplTypeArgs{}
	}
	for _, b := range fa.ImplBlocks[recv][iface] {
		if b.Block == info.Block {
			return
		}
	}
	fa.ImplBlocks[recv][iface] = append(fa.ImplBlocks[recv][iface], info)
}

// addImplBlocks adds fa's impl blocks to idx.
func addImplBlocks(idx *ProjectImplIndex, fa *FileAnalysis) {
	if idx.ImplBlocks == nil {
		idx.ImplBlocks = map[string]map[string][]*ImplTypeArgs{}
	}
	for recv, byIface := range fa.ImplBlocks {
		if idx.ImplBlocks[recv] == nil {
			idx.ImplBlocks[recv] = map[string][]*ImplTypeArgs{}
		}
		for iface, blocks := range byIface {
			idx.ImplBlocks[recv][iface] = append(idx.ImplBlocks[recv][iface], blocks...)
		}
	}
}
