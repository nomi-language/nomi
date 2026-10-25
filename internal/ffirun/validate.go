package ffirun

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	goprinter "go/printer"
	gotoken "go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/iancoleman/strcase"
	"golang.org/x/mod/modfile"

	nomiast "github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffitypes"
	"github.com/nomi-language/nomi/internal/modcache"
)

func validateDiscoveredGoBindings(projectRoot string, packages []DiscoveredPackage) error {
	var problems []string
	opaque := map[string]bool{}
	for _, pkg := range packages {
		for _, typ := range pkg.Types {
			localName := typ.LocalName
			if localName == "" {
				localName = localTypeNameFromKey(typ.Key)
			}
			opaque[localName] = true
		}
	}
	for _, pkg := range packages {
		if !isFile(filepath.Join(projectRoot, "go.mod")) && !IsStdPackage(pkg.ImportPath) {
			problems = append(problems, fmt.Sprintf(
				"%s: Go package %q is not in the Go standard library, and no go.mod above the declaring file provides it",
				packageLocation(projectRoot, pkg), pkg.ImportPath))
			continue
		}
		src, readable, err := goSourceOf(projectRoot, pkg.ImportPath)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", packageLocation(projectRoot, pkg), err))
			continue
		}
		if !readable {
			if !goModProvidesImportPath(projectRoot, pkg.ImportPath) {
				problems = append(problems, fmt.Sprintf(
					"%s: Go package %q is not provided by the current Go module, a require, or a replace in go.mod; "+
						"run `nomi add %s` to add its module",
					packageLocation(projectRoot, pkg), pkg.ImportPath, pkg.ImportPath))
			} else {
				// A required module whose source is not on disk: the
				// adapters are generated from that source.
				problems = append(problems, fmt.Sprintf(
					"%s: Go package %q is in a module go.mod requires, which is not in the Go module cache; "+
						"run `nomi fetch` to download it",
					packageLocation(projectRoot, pkg), pkg.ImportPath))
			}
			continue
		}
		if src.std && stdInternal(pkg.ImportPath) {
			problems = append(problems, fmt.Sprintf(
				"%s: Go package %q is internal to the Go standard library, which no other module may import",
				packageLocation(projectRoot, pkg), pkg.ImportPath))
			continue
		}
		if !src.std {
			if why := cgoProblem(projectRoot, pkg.ImportPath); why != "" {
				problems = append(problems, fmt.Sprintf("%s: Go package %q %s",
					packageLocation(projectRoot, pkg), pkg.ImportPath, why))
				continue
			}
		}
		symbols, err := src.symbols()
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", packageLocation(projectRoot, pkg), err))
			continue
		}
		boundTypes := makeGoTypeBindings(symbols)
		check := &projectionCheck{boundTypes: boundTypes, goStructs: symbols.structs, opaque: opaque}
		for _, typ := range pkg.Types {
			localName := typ.LocalName
			if localName == "" {
				localName = localTypeNameFromKey(typ.Key)
			}
			sym, exists := symbols.types[typ.TypeName]
			if !exists {
				problems = append(problems, fmt.Sprintf(
					"%s: Go package %q has no top-level type %q for Nomi binding %q",
					bindingLocation(projectRoot, typ.SourceFile, typ.SourceLine, typ.SourceCol),
					pkg.ImportPath, typ.TypeName, typ.Key))
				continue
			}
			boundTypes[typ.TypeName] = goTypeBinding{
				localName: localName,
				explicit:  true,
				symbol:    sym,
			}
		}
		for _, exp := range pkg.Exports {
			goFunc, ok := symbols.funcs[exp.FuncName]
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"%s: Go package %q has no top-level function %q for Nomi binding %q",
					bindingLocation(projectRoot, exp.SourceFile, exp.SourceLine, exp.SourceCol),
					pkg.ImportPath, exp.FuncName, exp.Key))
				continue
			}
			problems = append(problems, validateFunctionSignature(projectRoot, pkg.ImportPath, exp, goFunc, check)...)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("nomi FFI binding validation failed:\n  - %s", strings.Join(problems, "\n  - "))
}

func validateFunctionSignature(projectRoot, importPath string, exp DiscoveredExport, goFunc goFuncSymbol, check *projectionCheck) []string {
	loc := bindingLocation(projectRoot, exp.SourceFile, exp.SourceLine, exp.SourceCol)
	if goFunc.typ.TypeParams != nil && goFunc.typ.TypeParams.NumFields() > 0 {
		return []string{fmt.Sprintf(
			"%s: Go function %q in package %q is generic, and a binding needs one concrete signature; "+
				"write a non-generic Go function that calls it and bind that",
			loc, exp.FuncName, importPath)}
	}
	check.begin(loc, exp.FuncName, importPath, exp)
	var problems []string
	goParams, unsupported := goFieldListNomiTypes(goFunc.typ.Params, check.boundTypes, goFunc.imports, true)
	for _, msg := range unsupported {
		problems = append(problems, fmt.Sprintf(
			"%s: Go function %q in package %q has unsupported parameter type: %s",
			loc, exp.FuncName, importPath, msg))
	}
	if len(unsupported) > 0 {
		// Unsupported types already describe the boundary failure. Continuing
		// with arity checks would count only the successfully projected
		// parameters and produce a misleading secondary mismatch.
	} else if len(goParams) != len(exp.Params) {
		problems = append(problems, fmt.Sprintf(
			"%s: signature mismatch for Go function %q in package %q: Go has %d parameter%s, Nomi declares %d",
			loc, exp.FuncName, importPath, len(goParams), plural(len(goParams)), len(exp.Params)))
	} else {
		for i, want := range goParams {
			declared := exp.Params[i].TypeAnnotation
			paramLabel := fmt.Sprintf("parameter %d", i+1)
			if exp.Params[i].Name != "" {
				paramLabel = fmt.Sprintf("parameter %d %q", i+1, exp.Params[i].Name)
			}
			if declared == nil {
				problems = append(problems, fmt.Sprintf(
					"%s: signature mismatch for Go function %q in package %q: %s projects to %s, but Nomi has no type annotation",
					loc, exp.FuncName, importPath, paramLabel, want))
				continue
			}
			if !check.matches(want, declared) {
				problems = append(problems, fmt.Sprintf(
					"%s: signature mismatch for Go function %q in package %q: %s projects to %s, but Nomi declares %s%s",
					loc, exp.FuncName, importPath, paramLabel, want, declared.TypeString(), check.hint(want, declared)))
				continue
			}
			problems = append(problems, check.slotStructs(paramLabel+" struct", want, declared)...)
		}
	}

	wantReturn, returnUnsupported := goResultNomiType(goFunc.typ.Results, check.boundTypes, goFunc.imports, true)
	for _, msg := range returnUnsupported {
		problems = append(problems, fmt.Sprintf(
			"%s: Go function %q in package %q has unsupported return type: %s",
			loc, exp.FuncName, importPath, msg))
	}
	if len(returnUnsupported) > 0 || wantReturn == nil {
		return problems
	}
	declared := exp.ReturnType
	if declared == nil {
		declared = &nomiast.SimpleType{Name: ffitypes.NomiUnit}
	}
	if !check.matches(wantReturn, declared) {
		problems = append(problems, fmt.Sprintf(
			"%s: signature mismatch for Go function %q in package %q: return projects to %s, but Nomi declares %s%s",
			loc, exp.FuncName, importPath, wantReturn, declared.TypeString(), check.hint(wantReturn, declared)))
		return problems
	}
	return append(problems, check.slotStructs("return struct", wantReturn, declared)...)
}

// goProjection is the Nomi type a Go type projects to. It is a tree, so a
// Go struct can stand in it as a shape rather than as a name.
type goProjection struct {
	kind goProjectionKind
	// name is a leaf's Nomi type name (String, Unit, an opaque binding's
	// local name, or "error", this package's sentinel for the trailing
	// result) or a generic's (List, Maybe, Map, Result).
	name string
	// args are a generic's type arguments, a tuple's elements or a
	// callback's parameters.
	args []*goProjection
	// ret is a callback's result.
	ret *goProjection
	// goStruct is a projStruct's Go type name, a struct declared in the
	// bound package.
	goStruct string
}

type goProjectionKind uint8

const (
	projLeaf goProjectionKind = iota
	projGeneric
	projTuple
	projFunc
	// projStruct is a Go struct no `opaque type` binds. It projects to a
	// Nomi struct with the same fields, whatever either is named: the Nomi
	// type's identity comes from the declaration, never from the Go type.
	projStruct
)

func projLeafOf(name string) *goProjection { return &goProjection{kind: projLeaf, name: name} }

func projGenericOf(name string, args ...*goProjection) *goProjection {
	return &goProjection{kind: projGeneric, name: name, args: args}
}

func (p *goProjection) isLeaf(name string) bool { return p.kind == projLeaf && p.name == name }

func (p *goProjection) String() string {
	join := func(ps []*goProjection) string {
		parts := make([]string, len(ps))
		for i, a := range ps {
			parts[i] = a.String()
		}
		return strings.Join(parts, ", ")
	}
	switch p.kind {
	case projGeneric:
		return p.name + "<" + join(p.args) + ">"
	case projTuple:
		return "#(" + join(p.args) + ")"
	case projFunc:
		return "(" + join(p.args) + ") -> " + p.ret.String()
	case projStruct:
		return "a struct (Go " + p.goStruct + ")"
	}
	return p.name
}

// projectionCheck compares the Nomi types a binding declares with the Go
// types their slots hold, for the bindings of one Go package.
//
// Every slot must agree in structure (List with a slice, Maybe with a
// pointer, a callback with a func, a tuple with the Go results), a scalar
// with its row of the projection table, and an opaque binding with the Go
// type it binds. A Go struct that no `opaque type` binds is a shape, not a
// name: it fills a slot whose declared type names a Nomi struct, whatever
// either is called. A struct of the declaring file is then held to the Go
// struct's exported fields, both ways and at every depth. A struct declared
// in another Nomi file is not read here; the adapter generator holds its
// fields to the Go struct's. A Go struct in a slot that declares a projected
// scalar, an opaque binding, a generic, a tuple or a function type is a
// mismatch, and so is a Nomi struct in a slot whose Go type is no struct.
//
// A distinct type crosses as its inner type does, so one the check reads
// (declared in the binding's file, or std's Duration and Instant) is
// compared through its inner type, at every depth. Dynamic is a leaf: a Go
// empty interface or rt.Dynamic projects to it, and nothing else does.
type projectionCheck struct {
	boundTypes goTypeBindings
	goStructs  map[string]goStructSymbol
	// opaque is every Nomi name an `opaque type ... go` binding declares, in
	// any bound package: a type that is no struct.
	opaque map[string]bool

	loc, funcName, importPath string
	nomiStructs               map[string]*nomiast.StructDef
	nomiDistincts             map[string]*nomiast.TypeDef
	// seen is each (Go struct, Nomi struct) pair whose fields the check of
	// one slot has compared, so a recursive struct ends.
	seen map[[2]string]bool
	// unwrapping is each distinct whose inner type the check is comparing,
	// so a distinct that reaches itself ends.
	unwrapping map[string]bool
}

func (c *projectionCheck) begin(loc, funcName, importPath string, exp DiscoveredExport) {
	c.loc, c.funcName, c.importPath = loc, funcName, importPath
	c.nomiStructs, c.nomiDistincts = exp.Structs, exp.Distincts
	c.unwrapping = map[string]bool{}
}

// stdDistinctInner is the inner type of each std distinct a binding may
// name: Duration and Instant are `opaque type ... Int`, so an int64 slot
// carries either one, as well as time.Duration and time.Time.
var stdDistinctInner = map[string]nomiast.TypeExpr{
	ffitypes.NomiDuration: &nomiast.SimpleType{Name: ffitypes.NomiInt},
	ffitypes.NomiInstant:  &nomiast.SimpleType{Name: ffitypes.NomiInt},
}

// distinct reports whether t names a distinct type the check reads (one
// declared in the binding's file, or std's Duration and Instant), and its
// inner type: nil for a marker, which holds no value and has no Go
// projection.
func (c *projectionCheck) distinct(t nomiast.TypeExpr) (string, nomiast.TypeExpr, bool) {
	v, ok := t.(*nomiast.SimpleType)
	if !ok {
		return "", nil, false
	}
	if d := c.nomiDistincts[v.Name]; d != nil {
		return v.Name, d.InnerTypeExpr, true
	}
	if c.nomiStructs[v.Name] != nil || c.opaque[v.Name] {
		return "", nil, false
	}
	if inner, ok := stdDistinctInner[v.Name]; ok {
		return v.Name, inner, true
	}
	return "", nil, false
}

// unwrapped calls f with the inner type of the distinct t names, and
// reports false for a marker or a distinct that reaches itself.
func (c *projectionCheck) unwrapped(name string, inner nomiast.TypeExpr, f func(nomiast.TypeExpr) bool) bool {
	if inner == nil || c.unwrapping[name] {
		return false
	}
	c.unwrapping[name] = true
	defer delete(c.unwrapping, name)
	return f(inner)
}

// slotStructs is structs for one parameter or result.
func (c *projectionCheck) slotStructs(label string, p *goProjection, t nomiast.TypeExpr) []string {
	c.seen = map[[2]string]bool{}
	return c.structs(label, p, t)
}

// projectedNomiNames are the Nomi types the scalar table projects. A slot
// declaring one holds no struct.
var projectedNomiNames = map[string]bool{
	ffitypes.NomiString: true, ffitypes.NomiBool: true, ffitypes.NomiByte: true,
	ffitypes.NomiInt: true, ffitypes.NomiFloat: true, ffitypes.NomiBytes: true,
	ffitypes.NomiDuration: true, ffitypes.NomiInstant: true, ffitypes.NomiDynamic: true,
	ffitypes.NomiUnit: true,
}

// matches reports whether the declared Nomi type t agrees with the
// projection p of the Go type in its slot.
func (c *projectionCheck) matches(p *goProjection, t nomiast.TypeExpr) bool {
	if t == nil {
		return false
	}
	// A distinct crosses as its inner type does, at any depth; Duration
	// also fills a time.Duration slot, and Instant a time.Time one.
	if name, inner, ok := c.distinct(t); ok && !p.isLeaf(name) {
		return c.unwrapped(name, inner, func(inner nomiast.TypeExpr) bool { return c.matches(p, inner) })
	}
	if p.kind == projStruct {
		return c.namesStruct(t)
	}
	switch v := t.(type) {
	case *nomiast.SimpleType:
		return p.isLeaf(v.Name)
	case *nomiast.GenericType:
		return p.kind == projGeneric && p.name == v.Name && c.allMatch(p.args, v.Params)
	case *nomiast.FuncType:
		if v.Return == nil {
			return p.kind == projTuple && c.allMatch(p.args, v.Params)
		}
		return p.kind == projFunc && c.allMatch(p.args, v.Params) && c.matches(p.ret, v.Return)
	}
	return p.String() == t.TypeString()
}

// hint explains a mismatch the projection table alone does not: a Dynamic
// declared against a Go type that is no empty interface.
func (c *projectionCheck) hint(p *goProjection, t nomiast.TypeExpr) string {
	if c.dynamicAgainstConcrete(p, t) {
		return "; Dynamic crosses only as a Go empty interface (any) or rt.Dynamic, so declare the type the Go value has"
	}
	return ""
}

// dynamicAgainstConcrete reports whether t declares Dynamic somewhere p
// projects another type.
func (c *projectionCheck) dynamicAgainstConcrete(p *goProjection, t nomiast.TypeExpr) bool {
	if name, inner, ok := c.distinct(t); ok && !p.isLeaf(name) {
		return c.unwrapped(name, inner, func(inner nomiast.TypeExpr) bool { return c.dynamicAgainstConcrete(p, inner) })
	}
	switch v := t.(type) {
	case *nomiast.SimpleType:
		return v.Name == ffitypes.NomiDynamic && !p.isLeaf(ffitypes.NomiDynamic) && c.nomiStructs[v.Name] == nil
	case *nomiast.GenericType:
		return p.kind == projGeneric && c.anyDynamicAgainstConcrete(p.args, v.Params)
	case *nomiast.FuncType:
		if c.anyDynamicAgainstConcrete(p.args, v.Params) {
			return true
		}
		return p.kind == projFunc && v.Return != nil && c.dynamicAgainstConcrete(p.ret, v.Return)
	}
	return false
}

func (c *projectionCheck) anyDynamicAgainstConcrete(ps []*goProjection, ts []nomiast.TypeExpr) bool {
	if len(ps) != len(ts) {
		return false
	}
	for i := range ps {
		if c.dynamicAgainstConcrete(ps[i], ts[i]) {
			return true
		}
	}
	return false
}

func (c *projectionCheck) allMatch(ps []*goProjection, ts []nomiast.TypeExpr) bool {
	if len(ps) != len(ts) {
		return false
	}
	for i := range ps {
		if !c.matches(ps[i], ts[i]) {
			return false
		}
	}
	return true
}

// namesStruct reports whether t may name a Nomi struct: a struct of the
// declaring file, or a bare or qualified name of another file's type, which
// this check does not read. A projected scalar and an opaque binding are no
// structs.
func (c *projectionCheck) namesStruct(t nomiast.TypeExpr) bool {
	switch v := t.(type) {
	case *nomiast.SimpleType:
		if c.nomiStructs[v.Name] != nil {
			return true
		}
		return !projectedNomiNames[v.Name] && !c.opaque[v.Name]
	case *nomiast.QualifiedType:
		_, simple := v.Member.(*nomiast.SimpleType)
		return simple
	}
	return false
}

// structs compares the fields of each Go struct in p with the declaring
// file's Nomi struct in the same place of t. t must match p.
func (c *projectionCheck) structs(label string, p *goProjection, t nomiast.TypeExpr) []string {
	if name, inner, ok := c.distinct(t); ok && !p.isLeaf(name) {
		var problems []string
		c.unwrapped(name, inner, func(inner nomiast.TypeExpr) bool {
			problems = c.structs(label, p, inner)
			return true
		})
		return problems
	}
	switch p.kind {
	case projStruct:
		if v, ok := t.(*nomiast.SimpleType); ok {
			return c.structFields(label, p.goStruct, v.Name)
		}
	case projGeneric:
		if v, ok := t.(*nomiast.GenericType); ok {
			return c.eachStructs(label, p.args, v.Params)
		}
	case projTuple, projFunc:
		v, ok := t.(*nomiast.FuncType)
		if !ok {
			return nil
		}
		problems := c.eachStructs(label, p.args, v.Params)
		if p.kind == projFunc {
			problems = append(problems, c.structs(label, p.ret, v.Return)...)
		}
		return problems
	}
	return nil
}

func (c *projectionCheck) eachStructs(label string, ps []*goProjection, ts []nomiast.TypeExpr) []string {
	var problems []string
	for i := range ps {
		problems = append(problems, c.structs(label, ps[i], ts[i])...)
	}
	return problems
}

func (c *projectionCheck) structFields(label, goName, nomiName string) []string {
	goStruct, ok := c.goStructs[goName]
	nomiStruct := c.nomiStructs[nomiName]
	key := [2]string{goName, nomiName}
	if !ok || nomiStruct == nil || c.seen[key] {
		return nil
	}
	c.seen[key] = true
	nomiFields := make(map[string]nomiast.TypeExpr, len(nomiStruct.Fields))
	for _, field := range nomiStruct.Fields {
		nomiFields[field.Name] = field.TypeAnnotation
	}
	var problems []string
	for _, name := range sortedKeys(goStruct.fields) {
		nomiFieldType, ok := nomiFields[name]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: signature mismatch for Go function %q in package %q: %s field %q exists in Go %s but not in Nomi %s",
				c.loc, c.funcName, c.importPath, label, name, goName, nomiName))
			continue
		}
		projected, err := goExprNomiType(goStruct.fields[name], c.boundTypes, goStruct.imports, true)
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s: Go function %q in package %q has unsupported %s field %q type: %s",
				c.loc, c.funcName, c.importPath, label, name, err))
			continue
		}
		if !c.matches(projected, nomiFieldType) {
			declared := ""
			if nomiFieldType != nil {
				declared = nomiFieldType.TypeString()
			}
			problems = append(problems, fmt.Sprintf(
				"%s: signature mismatch for Go function %q in package %q: %s field %q projects to %s, but Nomi %s declares %s%s",
				c.loc, c.funcName, c.importPath, label, name, projected, nomiName, declared, c.hint(projected, nomiFieldType)))
			continue
		}
		problems = append(problems, c.structs(fmt.Sprintf("%s field %q struct", label, name), projected, nomiFieldType)...)
	}
	for _, field := range nomiStruct.Fields {
		if _, ok := goStruct.fields[field.Name]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: signature mismatch for Go function %q in package %q: %s field %q exists in Nomi %s but not in Go %s",
				c.loc, c.funcName, c.importPath, label, field.Name, nomiName, goName))
		}
	}
	return problems
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// goResultNomiType projects a Go result list onto the Nomi return type
// the declaration must spell, by the projection internal/hostgen generates
// adapters for: N non-error results project to Unit (N == 0), that value
// (N == 1) or an N-tuple (N >= 2), and a trailing `error` wraps the lot in
// Result<_, String>.
//
// allowTuple is false in callback position. A callback runs the boundary
// in reverse — Go calls a Nomi closure and needs N Go values back out of
// one Nomi value — and that direction only supports a value plus an
// optional trailing error. Projecting a tuple here would validate a shape
// the adapter generator then refuses.
func goResultNomiType(results *goast.FieldList, boundTypes goTypeBindings, imports map[string]string, allowTuple bool) (*goProjection, []string) {
	goReturns, unsupported := goFieldListNomiTypes(results, boundTypes, imports, false)
	hasErr := len(goReturns) > 0 && goReturns[len(goReturns)-1].isLeaf("error")
	values := goReturns
	if hasErr {
		values = values[:len(values)-1]
	}
	for i, v := range values {
		if v.isLeaf("error") {
			return nil, append(unsupported, fmt.Sprintf("return %d projects to error; error is only supported as the final return", i+1))
		}
	}
	if !allowTuple && len(values) > 1 {
		// Callback position keeps the pre-tuple contract: one value plus an
		// optional trailing error. Report it the way it has always read so
		// the Go author sees which slot broke the rule.
		if len(goReturns) > 2 {
			return nil, append(unsupported, fmt.Sprintf("%d returns (max 2)", len(goReturns)))
		}
		return nil, append(unsupported, fmt.Sprintf("second return projects to %s, expected error", goReturns[1]))
	}
	var projected *goProjection
	switch len(values) {
	case 0:
		projected = projLeafOf(ffitypes.NomiUnit)
	case 1:
		projected = values[0]
	default:
		projected = &goProjection{kind: projTuple, args: values}
	}
	if hasErr {
		return projGenericOf("Result", projected, projLeafOf(ffitypes.NomiString)), unsupported
	}
	return projected, unsupported
}

func goFieldListNomiTypes(fields *goast.FieldList, boundTypes goTypeBindings, imports map[string]string, allowCallbacks bool) ([]*goProjection, []string) {
	if fields == nil {
		return nil, nil
	}
	var out []*goProjection
	var unsupported []string
	for _, field := range fields.List {
		typ, err := goExprNomiType(field.Type, boundTypes, imports, allowCallbacks)
		if err != nil {
			unsupported = append(unsupported, err.Error())
			continue
		}
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			out = append(out, typ)
		}
	}
	return out, unsupported
}

func goExprNomiType(expr goast.Expr, boundTypes goTypeBindings, imports map[string]string, allowCallbacks bool) (*goProjection, error) {
	switch v := expr.(type) {
	case *goast.Ident:
		return goIdentNomiType(v.Name, boundTypes, allowCallbacks)
	case *goast.StarExpr:
		if name, ok := boundGoTypeName(v.X); ok {
			if binding, bound := boundTypes[name]; bound && binding.explicit {
				return projLeafOf(binding.localName), nil
			}
		}
		inner, err := goExprNomiType(v.X, boundTypes, imports, allowCallbacks)
		if err != nil {
			return nil, fmt.Errorf("*%s: %w", goNodeString(v.X), err)
		}
		return projGenericOf("Maybe", inner), nil
	case *goast.ArrayType:
		elem, err := goExprNomiType(v.Elt, boundTypes, imports, allowCallbacks)
		if err != nil {
			return nil, fmt.Errorf("[]%s: %w", goNodeString(v.Elt), err)
		}
		if elem.isLeaf(ffitypes.NomiByte) {
			return projLeafOf(ffitypes.NomiBytes), nil
		}
		return projGenericOf("List", elem), nil
	case *goast.MapType:
		key, err := goExprNomiType(v.Key, boundTypes, imports, allowCallbacks)
		if err != nil {
			return nil, fmt.Errorf("map key %s: %w", goNodeString(v.Key), err)
		}
		if !isProjectedNomiMapKeyType(key) {
			return nil, fmt.Errorf("map key %s projects to %s, which is not supported as an FFI map key; normalize the map in Go or cross it as Dynamic/opaque", goNodeString(v.Key), key)
		}
		val, err := goExprNomiType(v.Value, boundTypes, imports, allowCallbacks)
		if err != nil {
			return nil, fmt.Errorf("map value %s: %w", goNodeString(v.Value), err)
		}
		return projGenericOf("Map", key, val), nil
	case *goast.FuncType:
		if !allowCallbacks {
			return nil, fmt.Errorf("function type %s is only supported in direct callback parameter position", goNodeString(expr))
		}
		params, unsupported := goFieldListNomiTypes(v.Params, boundTypes, imports, false)
		if len(unsupported) > 0 {
			return nil, fmt.Errorf("callback %s: %s", goNodeString(expr), strings.Join(unsupported, "; "))
		}
		ret, unsupported := goResultNomiType(v.Results, boundTypes, imports, false)
		if len(unsupported) > 0 {
			return nil, fmt.Errorf("callback %s: %s", goNodeString(expr), strings.Join(unsupported, "; "))
		}
		return &goProjection{kind: projFunc, args: params, ret: ret}, nil
	case *goast.InterfaceType:
		if len(v.Methods.List) == 0 {
			return projLeafOf(ffitypes.NomiDynamic), nil
		}
		return nil, fmt.Errorf("non-empty interface %s", goNodeString(expr))
	case *goast.StructType:
		return nil, fmt.Errorf("anonymous struct type %s", goNodeString(expr))
	case *goast.Ellipsis:
		return nil, fmt.Errorf("variadic type %s", goNodeString(expr))
	case *goast.ChanType:
		return nil, fmt.Errorf("channel type %s is not automatically converted; declare it as an opaque type or normalize it in Go", goNodeString(expr))
	case *goast.SelectorExpr:
		if importPath, ok := selectorImportPath(v, imports); ok {
			if nomi, projected := ffitypes.NomiForGoNamedType(importPath, v.Sel.Name); projected {
				return projLeafOf(nomi), nil
			}
		}
		return nil, fmt.Errorf("unbound named type %s", goNodeString(expr))
	}
	return nil, fmt.Errorf("unsupported Go type %s", goNodeString(expr))
}

func isProjectedNomiMapKeyType(p *goProjection) bool {
	switch p.kind {
	case projFunc:
		return false
	case projGeneric:
		return p.name != "List" && p.name != "Map"
	}
	return true
}

// goIdentNomiType projects one Go predeclared type name onto the Nomi
// type it crosses as. The projection itself lives in
// internal/ffitypes — the same table the runtime's reflect-based
// preflight reads — so the two sides of the boundary cannot disagree
// about what `int64` means.
//
// `error` is handled here rather than in the table because it is not a
// Nomi type at all: it is this package's sentinel for the trailing-result
// slot, which goResultNomiType consumes into the Result layer.
func goIdentNomiType(name string, boundTypes goTypeBindings, allowCallbacks bool) (*goProjection, error) {
	if name == "error" {
		return projLeafOf("error"), nil
	}
	if nomi, projected := ffitypes.NomiForGoIdent(name); projected {
		return projLeafOf(nomi), nil
	}
	if binding, ok := boundTypes[name]; ok {
		return goNamedTypeNomiType(name, binding, boundTypes, allowCallbacks)
	}
	return nil, fmt.Errorf("unbound named type %s", name)
}

func goNamedTypeNomiType(name string, binding goTypeBinding, boundTypes goTypeBindings, allowCallbacks bool) (*goProjection, error) {
	if binding.explicit {
		return projLeafOf(binding.localName), nil
	}
	sym := binding.symbol
	switch sym.kind {
	case goTypeStruct:
		return &goProjection{kind: projStruct, goStruct: name}, nil
	case goTypeInterface:
		if sym.emptyInterface {
			return projLeafOf(ffitypes.NomiDynamic), nil
		}
		return nil, fmt.Errorf("named Go interface type %s is not automatically converted; declare it as an opaque type or normalize it in Go", name)
	case goTypeFunc:
		return nil, fmt.Errorf("named Go function type %s is not automatically converted; use an unnamed function parameter in the Go adapter or declare it as an opaque type", name)
	case goTypeChan:
		return nil, fmt.Errorf("named Go channel type %s is not automatically converted; declare it as an opaque type or normalize it in Go", name)
	case goTypeInvalid:
		return nil, fmt.Errorf("unsupported named Go type %s", name)
	}
	projected, err := goExprNomiType(sym.expr, boundTypes, sym.imports, allowCallbacks)
	if err != nil {
		return nil, fmt.Errorf("named Go %s type %s: %w", sym.kind, name, err)
	}
	return projected, nil
}

func boundGoTypeName(expr goast.Expr) (string, bool) {
	if ident, ok := expr.(*goast.Ident); ok {
		return ident.Name, true
	}
	return "", false
}

func localTypeNameFromKey(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		return key[i+1:]
	}
	return key
}

func goNodeString(n goast.Node) string {
	if n == nil {
		return "<nil>"
	}
	var b strings.Builder
	if err := goprinter.Fprint(&b, gotoken.NewFileSet(), n); err != nil {
		return fmt.Sprintf("%T", n)
	}
	return b.String()
}

// goSource is a Go package whose source ffirun reads: a package of the
// project's own module or of a local `replace`, or a standard library package
// in the toolchain's GOROOT (gostd.go).
type goSource struct {
	dir string
	std bool
}

// goSourceOf is the source of importPath, and whether ffirun reads it. A
// package of any other module is not read here; a named type from one is
// type-checked by go/types (gotypesimport.go). A standard library package is
// answered first and needs no go.mod.
func goSourceOf(projectRoot, importPath string) (goSource, bool, error) {
	if dir, ok := StdPackageDir(importPath); ok {
		return goSource{dir: dir, std: true}, true, nil
	}
	dir, local, err := resolveLocalImportDir(projectRoot, importPath)
	return goSource{dir: dir}, local, err
}

// symbols reads the package's top-level declarations. A standard library
// package is read through the toolchain's build constraints (stdGoFiles).
func (s goSource) symbols() (goPackageSymbols, error) {
	if !s.std {
		return readGoPackageSymbols(s.dir)
	}
	files, err := stdGoFiles(s.dir)
	if err != nil {
		return goPackageSymbols{}, fmt.Errorf("reading Go package directory %s: %w", s.dir, err)
	}
	return readGoPackageFiles(s.dir, files)
}

// GoPackageDir is the directory of the Go package importPath as the project
// whose go.mod is at projectRoot sees it, when its source is on disk: a
// package of a module the go.mod replaces with a local directory, of the
// project's own module, or of a module the go.mod requires whose files are in
// the Go module cache (put there by `nomi fetch`, or by any go command). A
// Go standard library package is not answered here (StdPackageDir).
func GoPackageDir(projectRoot, importPath string) (string, bool) {
	dir, ok, err := resolveLocalImportDir(projectRoot, importPath)
	return dir, ok && err == nil
}

// resolveLocalImportDir is GoPackageDir with go.mod's read error.
func resolveLocalImportDir(projectRoot, importPath string) (string, bool, error) {
	f, err := parseProjectGoMod(projectRoot)
	if err != nil {
		return "", false, err
	}
	bestPrefix := ""
	bestDir := ""
	for _, r := range f.Replace {
		if r.New.Version != "" || r.New.Path == "" {
			continue
		}
		if importPath != r.Old.Path && !strings.HasPrefix(importPath, r.Old.Path+"/") {
			continue
		}
		target := r.New.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(projectRoot, target)
		}
		suffix := strings.TrimPrefix(importPath, r.Old.Path)
		if len(r.Old.Path) > len(bestPrefix) {
			bestPrefix = r.Old.Path
			bestDir = filepath.Join(target, filepath.FromSlash(strings.TrimPrefix(suffix, "/")))
		}
	}
	if bestDir != "" {
		return filepath.Clean(bestDir), true, nil
	}
	if f.Module != nil {
		modPath := f.Module.Mod.Path
		if importPath == modPath || strings.HasPrefix(importPath, modPath+"/") {
			suffix := strings.TrimPrefix(importPath, modPath)
			return filepath.Clean(filepath.Join(projectRoot, filepath.FromSlash(strings.TrimPrefix(suffix, "/")))), true, nil
		}
	}
	if dir, ok := cachedImportDir(f, importPath); ok {
		return dir, true, nil
	}
	return "", false, nil
}

// cachedImportDir is the directory of importPath in the module cache's copy
// of the module f requires that provides it (the longest module path that
// prefixes it), at the version f requires, or false when the cache does not
// hold that module.
func cachedImportDir(f *modfile.File, importPath string) (string, bool) {
	var best *modfile.Require
	for _, r := range f.Require {
		if importPathMatchesModule(importPath, r.Mod.Path) && (best == nil || len(r.Mod.Path) > len(best.Mod.Path)) {
			best = r
		}
	}
	if best == nil {
		return "", false
	}
	cache, err := modcache.Root()
	if err != nil {
		return "", false
	}
	src := best.Mod
	for _, r := range f.Replace {
		if r.Old.Path == src.Path && (r.Old.Version == "" || r.Old.Version == src.Version) && r.New.Version != "" {
			src = r.New
		}
	}
	root := modcache.Cached(cache, src)
	if root == "" {
		return "", false
	}
	suffix := strings.TrimPrefix(strings.TrimPrefix(importPath, best.Mod.Path), "/")
	return filepath.Join(root, filepath.FromSlash(suffix)), true
}

func goModProvidesImportPath(projectRoot, importPath string) bool {
	if IsStdPackage(importPath) {
		return true
	}
	f, err := parseProjectGoMod(projectRoot)
	if err != nil {
		return false
	}
	if f.Module != nil && importPathMatchesModule(importPath, f.Module.Mod.Path) {
		return true
	}
	for _, r := range f.Require {
		if importPathMatchesModule(importPath, r.Mod.Path) {
			return true
		}
	}
	for _, r := range f.Replace {
		if importPathMatchesModule(importPath, r.Old.Path) {
			return true
		}
	}
	return false
}

func importPathMatchesModule(importPath, modulePath string) bool {
	return importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

type goPackageSymbols struct {
	funcs   map[string]goFuncSymbol
	types   map[string]goTypeSymbol
	structs map[string]goStructSymbol
}

type goTypeKind string

const (
	goTypeInvalid   goTypeKind = "unsupported"
	goTypeStruct    goTypeKind = "struct"
	goTypeInterface goTypeKind = "interface"
	goTypeFunc      goTypeKind = "function"
	goTypeChan      goTypeKind = "channel"
	goTypeScalar    goTypeKind = "scalar"
	goTypeSlice     goTypeKind = "slice"
	goTypeArray     goTypeKind = "array"
	goTypeMap       goTypeKind = "map"
	goTypePointer   goTypeKind = "pointer"
	goTypeNamed     goTypeKind = "named"
)

type goTypeSymbol struct {
	kind           goTypeKind
	alias          bool
	expr           goast.Expr
	imports        map[string]string
	emptyInterface bool
}

type goTypeBinding struct {
	localName string
	explicit  bool
	symbol    goTypeSymbol
}

type goTypeBindings map[string]goTypeBinding

func makeGoTypeBindings(symbols goPackageSymbols) goTypeBindings {
	out := make(goTypeBindings, len(symbols.types))
	for name, sym := range symbols.types {
		out[name] = goTypeBinding{
			localName: name,
			symbol:    sym,
		}
	}
	return out
}

type goFuncSymbol struct {
	typ     *goast.FuncType
	imports map[string]string
}

type goStructSymbol struct {
	fields  map[string]goast.Expr
	imports map[string]string
}

func readGoPackageSymbols(dir string) (goPackageSymbols, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return goPackageSymbols{}, fmt.Errorf("reading Go package directory %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		names = append(names, name)
	}
	return readGoPackageFiles(dir, names)
}

// readGoPackageFiles reads the top-level declarations of the named files in
// dir.
func readGoPackageFiles(dir string, names []string) (goPackageSymbols, error) {
	symbols := goPackageSymbols{
		funcs:   make(map[string]goFuncSymbol),
		types:   make(map[string]goTypeSymbol),
		structs: make(map[string]goStructSymbol),
	}
	parsed := 0
	fset := gotoken.NewFileSet()
	for _, name := range names {
		file, err := goparser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return goPackageSymbols{}, fmt.Errorf("parsing Go package file %s: %w", filepath.Join(dir, name), err)
		}
		imports := goFileImports(file)
		parsed++
		for _, decl := range file.Decls {
			switch v := decl.(type) {
			case *goast.FuncDecl:
				if v.Recv == nil && v.Name != nil {
					symbols.funcs[v.Name.Name] = goFuncSymbol{typ: v.Type, imports: imports}
				}
			case *goast.GenDecl:
				for _, spec := range v.Specs {
					if ts, ok := spec.(*goast.TypeSpec); ok && ts.Name != nil {
						symbols.types[ts.Name.Name] = goTypeSymbolFor(ts, imports)
						if st, ok := ts.Type.(*goast.StructType); ok {
							symbols.structs[ts.Name.Name] = goStructSymbol{
								fields:  goStructFields(st),
								imports: imports,
							}
						}
					}
				}
			}
		}
	}
	if parsed == 0 {
		return goPackageSymbols{}, fmt.Errorf("Go package directory %s contains no non-test .go files", dir)
	}
	return symbols, nil
}

func goTypeSymbolFor(ts *goast.TypeSpec, imports map[string]string) goTypeSymbol {
	sym := goTypeSymbol{
		kind:    goTypeKindForExpr(ts.Type),
		alias:   ts.Assign.IsValid(),
		expr:    ts.Type,
		imports: imports,
	}
	if iface, ok := ts.Type.(*goast.InterfaceType); ok {
		sym.emptyInterface = iface.Methods == nil || len(iface.Methods.List) == 0
	}
	return sym
}

func goTypeKindForExpr(expr goast.Expr) goTypeKind {
	switch v := expr.(type) {
	case *goast.StructType:
		return goTypeStruct
	case *goast.InterfaceType:
		return goTypeInterface
	case *goast.FuncType:
		return goTypeFunc
	case *goast.ChanType:
		return goTypeChan
	case *goast.ArrayType:
		if v.Len == nil {
			return goTypeSlice
		}
		return goTypeArray
	case *goast.MapType:
		return goTypeMap
	case *goast.StarExpr:
		return goTypePointer
	case *goast.Ident:
		// Every predeclared spelling the projection table names crosses
		// without a binding, and so does `error` — the trailing-result
		// sentinel, which is not a Nomi type and so is not in the table.
		if _, projected := ffitypes.NomiForGoIdent(v.Name); projected || v.Name == "error" {
			return goTypeScalar
		}
		return goTypeNamed
	case *goast.SelectorExpr:
		return goTypeNamed
	default:
		return goTypeInvalid
	}
}

func goStructFields(st *goast.StructType) map[string]goast.Expr {
	out := make(map[string]goast.Expr)
	if st == nil || st.Fields == nil {
		return out
	}
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		for _, name := range field.Names {
			if name == nil || !name.IsExported() {
				continue
			}
			nomiName, ok := nomiFieldNameFromGoField(name.Name, field.Tag)
			if !ok {
				continue
			}
			out[nomiName] = field.Type
		}
	}
	return out
}

func nomiFieldNameFromGoField(name string, tag *goast.BasicLit) (string, bool) {
	if tag != nil && tag.Value != "" {
		raw, err := strconv.Unquote(tag.Value)
		if err == nil {
			if tagValue, ok := reflect.StructTag(raw).Lookup("nomi"); ok {
				switch tagValue {
				case "-":
					return "", false
				case "":
					return strcase.ToSnake(name), true
				default:
					return tagValue, true
				}
			}
		}
	}
	return strcase.ToSnake(name), true
}

func goFileImports(file *goast.File) map[string]string {
	imports := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		if spec.Path == nil {
			continue
		}
		importPath := strings.Trim(spec.Path.Value, `"`)
		if importPath == "" {
			continue
		}
		name := defaultImportName(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." || name == "" {
			continue
		}
		imports[name] = importPath
	}
	return imports
}

func defaultImportName(importPath string) string {
	if i := strings.LastIndexByte(importPath, '/'); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

func selectorImportPath(expr *goast.SelectorExpr, imports map[string]string) (string, bool) {
	ident, ok := expr.X.(*goast.Ident)
	if !ok {
		return "", false
	}
	importPath, ok := imports[ident.Name]
	return importPath, ok
}

func packageLocation(projectRoot string, pkg DiscoveredPackage) string {
	return bindingLocation(projectRoot, pkg.OwnerFile, pkg.OwnerLine, pkg.OwnerCol)
}

func bindingLocation(projectRoot, file string, line, col int) string {
	if file == "" {
		return "unknown"
	}
	display := file
	if rel, err := filepath.Rel(projectRoot, file); err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		display = filepath.ToSlash(rel)
	}
	if line > 0 && col > 0 {
		return fmt.Sprintf("%s:%d:%d", display, line, col)
	}
	return display
}
