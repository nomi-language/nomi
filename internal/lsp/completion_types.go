package lsp

import (
	"reflect"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// exprKey identifies an expression across two parses of the same text: the
// snapshot's tree, which the checker typed, and the completion request's
// reparse, which holds the sentinel. Text before the cursor is identical in
// both, so a node there sits at the same position with the same kind.
type exprKey struct {
	line, col int
	kind      string
}

func keyOf(n ast.Node) (exprKey, bool) {
	line, col := nodePos(n)
	if line <= 0 {
		return exprKey{}, false
	}
	return exprKey{line, col, n.NodeType()}, true
}

// nodePos reads a node's Line and Col fields.
func nodePos(n ast.Node) (int, int) {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return 0, 0
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return 0, 0
	}
	l, c := v.FieldByName("Line"), v.FieldByName("Col")
	if !l.IsValid() || !c.IsValid() || l.Kind() != reflect.Int || c.Kind() != reflect.Int {
		return 0, 0
	}
	return int(l.Int()), int(c.Int())
}

// exprIndex is the position index over one FileAnalysis's ExprTypes and
// ExpectedTypes. Built on the first completion request against that
// analysis and reused until the document is analyzed again.
type exprIndex struct {
	fa       *analysis.FileAnalysis
	types    map[exprKey]analysis.Type
	expected map[exprKey]analysis.Type
}

type exprIndexCache struct {
	mu  sync.Mutex
	idx *exprIndex
}

func (c *exprIndexCache) get(fa *analysis.FileAnalysis) *exprIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.idx != nil && c.idx.fa == fa {
		return c.idx
	}
	idx := &exprIndex{
		fa:       fa,
		types:    make(map[exprKey]analysis.Type, len(fa.ExprTypes)),
		expected: make(map[exprKey]analysis.Type, len(fa.ExpectedTypes)),
	}
	for n, t := range fa.ExprTypes {
		if k, ok := keyOf(n); ok {
			idx.types[k] = t
		}
	}
	for n, t := range fa.ExpectedTypes {
		if k, ok := keyOf(n); ok {
			idx.expected[k] = t
		}
	}
	c.idx = idx
	return idx
}

// recordedType is the type the checker gave the snapshot's node at n's
// position, if it checked one there.
func (r *completionRequest) recordedType(n ast.Node) analysis.Type {
	k, ok := r.analyzedKeyOf(n)
	if !ok {
		return nil
	}
	return analysis.ResolveTypeVar(r.s.exprIndex.get(r.fa).types[k])
}

// recordedExpected is the type the checker checked the snapshot's node at
// n's position against, if any.
func (r *completionRequest) recordedExpected(n ast.Node) analysis.Type {
	k, ok := r.analyzedKeyOf(n)
	if !ok {
		return nil
	}
	return analysis.ResolveTypeVar(r.s.exprIndex.get(r.fa).expected[k])
}

// analyzedKeyOf is n's key in the snapshot's analysis, whose text may be
// older than the reparsed text n comes from (completionPositions).
func (r *completionRequest) analyzedKeyOf(n ast.Node) (exprKey, bool) {
	k, ok := keyOf(n)
	if !ok {
		return exprKey{}, false
	}
	return r.positions.analyzedKey(k)
}

// exprType is the type of an expression of the reparsed tree. The checker's
// own answer comes first: the snapshot holds it whenever the statement
// parsed before the user started typing at the cursor. A statement the
// parser could not read has no checked types, so for the shapes a member
// access or a pipe starts from (a name, a field chain, a call, a literal)
// the type is read from the symbols the snapshot does hold: a binding's
// type, a field's declared type, a function's return type, with a generic
// callee's type parameters solved from its arguments (callType).
func (r *completionRequest) exprType(n ast.Node) analysis.Type {
	if n == nil {
		return nil
	}
	if t := r.recordedType(n); t != nil {
		return t
	}
	switch v := n.(type) {
	case *ast.GroupedExpr:
		return r.exprType(v.Expr)
	case *ast.IntLit:
		return analysis.TypeInt
	case *ast.FloatLit:
		return analysis.TypeFloat
	case *ast.StringLit, *ast.StringInterp:
		return analysis.TypeString
	case *ast.CodepointLit:
		return analysis.TypeCodepoint
	case *ast.Ident:
		return r.valueTypeOf(r.scope.Lookup(v.Name))
	case *ast.FieldAccess:
		if v.Field == nil {
			return nil
		}
		if sym := r.memberSymbol(v); sym != nil {
			return r.valueTypeOf(sym)
		}
		if f, ok := fieldOf(r.exprType(v.Object), v.Field.Name); ok {
			return f.Type
		}
	case *ast.Call:
		return r.callType(v, nil)
	case *ast.Binary:
		if v.Op == "|>" {
			return r.pipeType(v)
		}
	}
	return nil
}

// valueTypeOf is the type of the value a symbol names: a binding's or a
// parameter's type, a function's type. A type name is no value.
func (r *completionRequest) valueTypeOf(sym *analysis.Symbol) analysis.Type {
	sym = realSymbol(sym)
	if sym == nil || typeSymbol(sym) || sym.Kind == analysis.SymbolModule {
		return nil
	}
	return analysis.ResolveTypeVar(sym.Type)
}

// memberSymbol resolves `file.Name` and `Owner.name` spelled by fa to the
// symbol they name, or nil for a field read off a value.
func (r *completionRequest) memberSymbol(fa *ast.FieldAccess) *analysis.Symbol {
	if at, ok := r.positions.toAnalyzed(analysis.Pos{Line: fa.Field.Line, Col: fa.Field.Col}); ok {
		if sym := realSymbol(r.fa.References[at]); sym != nil {
			return sym
		}
	}
	owner := r.symbolOf(fa.Object)
	if owner == nil {
		return nil
	}
	if owner.Kind == analysis.SymbolModule && owner.ModuleScope != nil {
		return realSymbol(owner.ModuleScope.LookupLocal(fa.Field.Name))
	}
	if typeSymbol(owner) {
		if m := r.ownerFunction(r.ownerNameOf(owner), fa.Field.Name); m != nil {
			return m
		}
		return realSymbol(owner.Members[fa.Field.Name])
	}
	return nil
}

// symbolOf resolves a name or a `file.Name` chain to the symbol it names at
// the cursor; nil for any other expression.
func (r *completionRequest) symbolOf(n ast.Node) *analysis.Symbol {
	switch v := n.(type) {
	case *ast.Ident:
		return realSymbol(r.scope.Lookup(v.Name))
	case *ast.TypeIdent:
		return realSymbol(r.scope.Lookup(v.Name))
	case *ast.FieldAccess:
		if v.Field == nil {
			return nil
		}
		owner := r.symbolOf(v.Object)
		if owner != nil && owner.Kind == analysis.SymbolModule && owner.ModuleScope != nil {
			return realSymbol(owner.ModuleScope.LookupLocal(v.Field.Name))
		}
	}
	return nil
}

// fieldOf returns the field named name of a struct or anonymous struct
// value type, with a generic struct's type arguments substituted.
func fieldOf(t analysis.Type, name string) (analysis.FieldDef, bool) {
	for _, f := range fieldsOf(t) {
		if f.Name == name {
			return f, true
		}
	}
	return analysis.FieldDef{}, false
}

// fieldsOf lists the fields a value of type t has.
func fieldsOf(t analysis.Type) []analysis.FieldDef {
	switch v := analysis.ResolveTypeVar(t).(type) {
	case *analysis.StructType:
		if len(v.TypeArgs) == 0 || len(v.TypeArgs) != len(v.TypeParamDefs) {
			return v.Fields
		}
		subs := make(map[*analysis.TypeParam_]analysis.Type, len(v.TypeArgs))
		for i, p := range v.TypeParamDefs {
			subs[p] = v.TypeArgs[i]
		}
		out := make([]analysis.FieldDef, len(v.Fields))
		for i, f := range v.Fields {
			out[i] = f
			out[i].Type = analysis.Substitute(f.Type, subs)
		}
		return out
	case *analysis.AnonStructType:
		return v.Fields
	}
	return nil
}
