package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A derive names its interface by identity (spec §12 *Derive*). Lowering
// writes the derived impl block with the interface as the derive spells it
// (`ToJson`, `json.ToJson`), and the block's header resolves through the
// file's scope as a hand-written one does. A derive writes only the
// interfaces of derivableInterfaces, so once types resolve the header must
// be the one of its name that std declares: a file that declares its own
// `interface ToJson` and writes `derive ToJson` names its own interface,
// which no derive can write. That is one error at the derive, and the
// block is not held to the interface it names, which would only repeat
// the error in other words.

// derivableInterface reports whether it, the interface a derive's header
// resolves to, is the one of derivableInterfaces of its name. An
// interface of unresolved origin is accepted, by the identity lattice
// (interface_identity.go).
func derivableInterface(it *InterfaceType) bool {
	home, ok := deriveProtocolHomeModule[it.Name]
	return ok && sameNominalIdentity(it.Origin, it.Name, home, it.Name)
}

// derivesStdInterface reports whether te, a derive's interface as fa's
// file writes it, means a derivable interface. A name that resolves to no
// interface is reported by the scope check (checkDeriveInScope), and
// answers true here.
func derivesStdInterface(fa *FileAnalysis, te ast.TypeExpr) bool {
	it := interfaceThrough(fa, implBlockInterfaceQualifier(te), TypeExprBaseName(te))
	return it == nil || derivableInterface(it)
}

// checkDerivedInterface reports the error at the derive that n, a block a
// derive lowered to, names iface, an interface derive does not write, and
// whether it did: the block is then not checked against iface.
func (c *checker) checkDerivedInterface(n *ast.ImplBlock, iface *InterfaceType) bool {
	written := derivedInterface(n)
	if written == nil || derivableInterface(iface) {
		return false
	}
	line, col := typeExprPos(written, n.Line, n.Col)
	c.addError(line, col, derivedInterfaceMessage(written, TypeExprBaseName(n.Receiver), iface, c.fa.Origin))
	return true
}

// derivedInterfaceMessage is the error for `derive <written> for <recv>`
// whose interface resolves to iface, an interface declared at an origin
// that is not the std module derive writes it from; fileOrigin is the
// deriving file's.
func derivedInterfaceMessage(written ast.TypeExpr, recv string, iface *InterfaceType, fileOrigin string) string {
	home := deriveProtocolHomeModule[iface.Name]
	module := strings.ReplaceAll(home, "/", ast.PathSeparator)
	alias := home[strings.LastIndex(home, "/")+1:]
	declarer := "this file"
	if iface.Origin != fileOrigin {
		declarer = "`" + strings.ReplaceAll(iface.Origin, "/", ast.PathSeparator) + "`"
	}
	return fmt.Sprintf("`derive %s for %s`: `%s` here is the interface %s declares, and derive writes only std's `%s` (`%s`); to derive std's, write `derive %s.%s for %s` with `import %s`",
		written.TypeString(), recv, written.TypeString(), declarer, iface.Name, module, alias, iface.Name, recv, module)
}
