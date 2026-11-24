package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// A derive writes an impl of the interface for a type from the type's parts
// (spec §12 *Derive*), so each part must have the interface. The checker
// demands it of each part when it meets the derive's impl block, after
// types and impl templates are built, through the one recorder of a demand
// (recordConformanceRecording), as any use of the interface at the part's
// type demands it: the part's own impl, the where bounds of a written impl
// at the type arguments they bind, and what an impl that answers by shape
// asks of its type arguments (ImplAsksParts). So a field `Measure<Op>`
// whose `impl Equatable for Measure<P> where P: Unit2` writes a body meets
// a derived Equatable whatever Op holds, and a field `Measure<Bare>` is the
// error that Bare has no Unit2. A derived body's own calls at the parts
// make the same demands, and leave them to the derive (derivedBodyCall), so
// each cause is one error at the part.

// checkDerivedParts demands the interface a derive's block n implements, a
// std interface derive writes, at each part of the type n is for
// (eachDeclPart) that has a type, at the part's type over the block's own
// type parameters, which carry the bound the derive implies and its where
// clause. Each demand names the derive and the part
// (RecordingKindDeriveSynth, DeriveCtx) and is made where the part is
// written, or at the derive for a type another file declares (a partner
// test's derive, or a REPL input's). Any other block demands nothing here.
func (c *checker) checkDerivedParts(n *ast.ImplBlock) {
	ifaceTE := derivedInterface(n)
	if ifaceTE == nil || !derivesStdInterface(c.fa, ifaceTE) {
		return
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, n)
	iface, _ := ResolveTypeExpr(ifaceTE, c.reg, blockTP, nil)
	it, ok := iface.(*InterfaceType)
	recvTy, err := ResolveTypeExpr(n.Receiver, c.reg, blockTP, nil)
	sym := c.fa.ModuleScope.Lookup(TypeExprBaseName(n.Receiver))
	if !ok || err != nil || recvTy == nil || sym == nil {
		return
	}
	sym = resolveRef(sym)
	recvName := TypeExprBaseName(n.Receiver)
	line, col := typeExprPos(n.Interface, n.Line, n.Col)
	derivePos := c.recPos(line, col)
	foreign := sym.SourceFile != "" && sym.SourceFile != c.fa.FilePath
	prevImpl, prevTP := c.implBlock, c.fnTypeParams
	c.implBlock, c.fnTypeParams = n, blockTP
	defer func() { c.implBlock, c.fnTypeParams = prevImpl, prevTP }()
	parts := map[*DeriveCtx]bool{}
	eachDeclPart(sym.Node, func(p declPart) {
		t, ok := partType(recvTy, p.name)
		if !ok || t == nil {
			return
		}
		pos := derivePos
		if !foreign {
			pos = c.recPos(p.line, p.col)
		}
		ctx := &DeriveCtx{Iface: it.Name, TypeName: recvName, DerivePos: derivePos, FieldName: p.name, FieldPos: pos}
		if it.Name == "Equatable" || it.Name == "Hashable" {
			ctx.NoEquality = c.heldWithoutEquality(t)
		}
		parts[ctx] = true
		c.recordConformanceRecording(t, it.Name, Recording{Pos: pos, Kind: RecordingKindDeriveSynth, Derive: ctx, IfaceOrigin: it.Origin})
	})
}

// derivedBodyCall reports whether n is a call in the body a derive wrote,
// whose demands at the type's parts are the derive's (checkDerivedParts).
func (c *checker) derivedBodyCall(n *ast.Call) bool {
	return IsSynthesizedLine(n.Line) && c.implBlock != nil && derivedInterface(c.implBlock) != nil
}

// heldWithoutEquality says what t holds that has no equality, for the help
// of a derive's missing Equatable or Hashable (DeriveCtx.NoEquality), as
// "`(Int) -> Int`, a function value", or "" when t has equality or is
// itself the part with none.
func (c *checker) heldWithoutEquality(t Type) string {
	part, none := c.noEqualityPart(t, map[Type]bool{})
	if !none || part == resolveTypeVar(t) {
		return ""
	}
	return fmt.Sprintf("`%s`, %s", part, noEqualityWhat(part))
}

// stdFile reports whether the checked file is a std module's.
func (c *checker) stdFile() bool {
	_, std := stdlibModuleForPath(c.fa.FilePath)
	return std
}

// partSubject is how a diagnostic about the part a derive demands an
// interface of begins: "`derive Display for H`: field `g`".
func (d *DeriveCtx) partSubject() string {
	return fmt.Sprintf("`derive %s for %s`: field `%s`", d.Iface, d.TypeName, d.FieldName)
}

// deriveVia is the clause naming the derive and its part, for a diagnostic
// made where a derive's demand is recorded, or "" for any other demand.
func deriveVia(rec Recording) string {
	if rec.Derive == nil {
		return ""
	}
	return " (" + requiredClause(rec) + ")"
}

// withDeriveFix is help, the help for a demand of ifaceName that no impl
// can meet, with the fix for a derive's demand of a part added: an impl
// written by hand, which leaves the part out. Any other demand's help is
// help unchanged.
func withDeriveFix(help string, rec Recording, ifaceName string) string {
	if rec.Derive == nil {
		return help
	}
	fix := fmt.Sprintf("write an `impl %s for %s` that leaves the field out", ifaceName, rec.Derive.TypeName)
	if help == "" {
		return fix
	}
	return help + "; " + fix
}
