package ffirun

import (
	"fmt"
	goast "go/ast"
	"go/build"
	goparser "go/parser"
	goprinter "go/printer"
	gotoken "go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/iancoleman/strcase"
	nomiast "github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffitypes"
)

func validateDiscoveredGoBindings(projectRoot string, packages []DiscoveredPackage) error {
	var problems []string
	for _, pkg := range packages {
		if pkg.ImportPath == "" {
			continue
		}
		dir, local, err := resolveLocalImportDir(projectRoot, pkg.ImportPath)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", packageLocation(projectRoot, pkg), err))
			continue
		}
		if !local {
			if !goModProvidesImportPath(projectRoot, pkg.ImportPath) {
				problems = append(problems, fmt.Sprintf(
					"%s: Go package %q is not provided by the current Go module, a require, or a replace in go.mod; "+
						"add it with `go get %s` or add an explicit require/replace",
					packageLocation(projectRoot, pkg), pkg.ImportPath, pkg.ImportPath))
			}
			continue
		}
		symbols, err := readGoPackageSymbols(dir)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", packageLocation(projectRoot, pkg), err))
			continue
		}
		boundTypes := makeGoTypeBindings(symbols)
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
			if exp.GoBody != "" {
				continue
			}
			goFunc, ok := symbols.funcs[exp.FuncName]
			if !ok {
				problems = append(problems, fmt.Sprintf(
					"%s: Go package %q has no top-level function %q for Nomi binding %q",
					bindingLocation(projectRoot, exp.SourceFile, exp.SourceLine, exp.SourceCol),
					pkg.ImportPath, exp.FuncName, exp.Key))
				continue
			}
			problems = append(problems, validateFunctionSignature(projectRoot, pkg.ImportPath, exp, goFunc, boundTypes, symbols.structs)...)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("nomi FFI binding validation failed:\n  - %s", strings.Join(problems, "\n  - "))
}

func validateFunctionSignature(projectRoot, importPath string, exp DiscoveredExport, goFunc goFuncSymbol, boundTypes goTypeBindings, goStructs map[string]goStructSymbol) []string {
	loc := bindingLocation(projectRoot, exp.SourceFile, exp.SourceLine, exp.SourceCol)
	var problems []string
	goParams, unsupported := goFieldListNomiTypes(goFunc.typ.Params, boundTypes, goFunc.imports, true)
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
			got := ""
			if exp.Params[i].TypeAnnotation != nil {
				got = exp.Params[i].TypeAnnotation.TypeString()
			}
			paramLabel := fmt.Sprintf("parameter %d", i+1)
			if exp.Params[i].Name != "" {
				paramLabel = fmt.Sprintf("parameter %d %q", i+1, exp.Params[i].Name)
			}
			if got == "" {
				problems = append(problems, fmt.Sprintf(
					"%s: signature mismatch for Go function %q in package %q: %s projects to %s, but Nomi has no type annotation",
					loc, exp.FuncName, importPath, paramLabel, want))
				continue
			}
			if got != want {
				problems = append(problems, fmt.Sprintf(
					"%s: signature mismatch for Go function %q in package %q: %s projects to %s, but Nomi declares %s",
					loc, exp.FuncName, importPath, paramLabel, want, got))
				continue
			}
			goExpr := goParamExprAt(goFunc.typ.Params, i)
			problems = append(problems, validateProjectedStructShape(
				loc, exp.FuncName, importPath, fmt.Sprintf("%s struct", paramLabel),
				goExpr, exp.Params[i].TypeAnnotation, boundTypes, goStructs, exp.Structs,
			)...)
		}
	}

	wantReturn, returnUnsupported := goResultNomiType(goFunc.typ.Results, boundTypes, goFunc.imports, true)
	for _, msg := range returnUnsupported {
		problems = append(problems, fmt.Sprintf(
			"%s: Go function %q in package %q has unsupported return type: %s",
			loc, exp.FuncName, importPath, msg))
	}
	gotReturn := "Unit"
	if exp.ReturnType != nil {
		gotReturn = exp.ReturnType.TypeString()
	}
	if len(returnUnsupported) > 0 {
		return problems
	}
	if wantReturn != "" && gotReturn != wantReturn {
		problems = append(problems, fmt.Sprintf(
			"%s: signature mismatch for Go function %q in package %q: return projects to %s, but Nomi declares %s",
			loc, exp.FuncName, importPath, wantReturn, gotReturn))
	} else if wantReturn != "" {
		// A tuple return puts one Go result behind each element, so the
		// struct-shape check walks the elements in lockstep with the Go
		// result list. A non-tuple return is the one-element case.
		elems := nomiReturnElems(resultOkType(exp.ReturnType))
		for i, elem := range elems {
			label := "return struct"
			if len(elems) > 1 {
				label = fmt.Sprintf("return element %d struct", i+1)
			}
			problems = append(problems, validateProjectedStructShape(
				loc, exp.FuncName, importPath, label,
				goReturnExprAt(goFunc.typ.Results, i), elem, boundTypes, goStructs, exp.Structs,
			)...)
		}
	}
	return problems
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
func goResultNomiType(results *goast.FieldList, boundTypes goTypeBindings, imports map[string]string, allowTuple bool) (string, []string) {
	goReturns, unsupported := goFieldListNomiTypes(results, boundTypes, imports, false)
	hasErr := len(goReturns) > 0 && goReturns[len(goReturns)-1] == "error"
	values := goReturns
	if hasErr {
		values = values[:len(values)-1]
	}
	for i, v := range values {
		if v == "error" {
			return "", append(unsupported, fmt.Sprintf("return %d projects to error; error is only supported as the final return", i+1))
		}
	}
	if !allowTuple && len(values) > 1 {
		// Callback position keeps the pre-tuple contract: one value plus an
		// optional trailing error. Report it the way it has always read so
		// the Go author sees which slot broke the rule.
		if len(goReturns) > 2 {
			return "", append(unsupported, fmt.Sprintf("%d returns (max 2)", len(goReturns)))
		}
		return "", append(unsupported, fmt.Sprintf("second return projects to %s, expected error", goReturns[1]))
	}
	var projected string
	switch len(values) {
	case 0:
		projected = "Unit"
	case 1:
		projected = values[0]
	default:
		projected = "(" + strings.Join(values, ", ") + ")"
	}
	if hasErr {
		return fmt.Sprintf("Result<%s, String>", projected), unsupported
	}
	return projected, unsupported
}

func goFieldListNomiTypes(fields *goast.FieldList, boundTypes goTypeBindings, imports map[string]string, allowCallbacks bool) ([]string, []string) {
	if fields == nil {
		return nil, nil
	}
	var out []string
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

func goExprNomiType(expr goast.Expr, boundTypes goTypeBindings, imports map[string]string, allowCallbacks bool) (string, error) {
	switch v := expr.(type) {
	case *goast.Ident:
		return goIdentNomiType(v.Name, boundTypes, allowCallbacks)
	case *goast.StarExpr:
		if name, ok := boundGoTypeName(v.X); ok {
			if binding, bound := boundTypes[name]; bound && binding.explicit {
				return binding.localName, nil
			}
		}
		inner, err := goExprNomiType(v.X, boundTypes, imports, allowCallbacks)
		if err != nil {
			return "", fmt.Errorf("*%s: %w", goNodeString(v.X), err)
		}
		return fmt.Sprintf("Maybe<%s>", inner), nil
	case *goast.ArrayType:
		elem, err := goExprNomiType(v.Elt, boundTypes, imports, allowCallbacks)
		if err != nil {
			return "", fmt.Errorf("[]%s: %w", goNodeString(v.Elt), err)
		}
		if elem == ffitypes.NomiByte {
			return ffitypes.NomiBytes, nil
		}
		return fmt.Sprintf("List<%s>", elem), nil
	case *goast.MapType:
		key, err := goExprNomiType(v.Key, boundTypes, imports, allowCallbacks)
		if err != nil {
			return "", fmt.Errorf("map key %s: %w", goNodeString(v.Key), err)
		}
		if !isProjectedNomiMapKeyType(key) {
			return "", fmt.Errorf("map key %s projects to %s, which is not supported as an FFI map key; normalize the map in Go or cross it as Dynamic/opaque", goNodeString(v.Key), key)
		}
		val, err := goExprNomiType(v.Value, boundTypes, imports, allowCallbacks)
		if err != nil {
			return "", fmt.Errorf("map value %s: %w", goNodeString(v.Value), err)
		}
		return fmt.Sprintf("Map<%s, %s>", key, val), nil
	case *goast.FuncType:
		if !allowCallbacks {
			return "", fmt.Errorf("function type %s is only supported in direct callback parameter position", goNodeString(expr))
		}
		params, unsupported := goFieldListNomiTypes(v.Params, boundTypes, imports, false)
		if len(unsupported) > 0 {
			return "", fmt.Errorf("callback %s: %s", goNodeString(expr), strings.Join(unsupported, "; "))
		}
		ret, unsupported := goResultNomiType(v.Results, boundTypes, imports, false)
		if len(unsupported) > 0 {
			return "", fmt.Errorf("callback %s: %s", goNodeString(expr), strings.Join(unsupported, "; "))
		}
		return fmt.Sprintf("(%s) -> %s", strings.Join(params, ", "), ret), nil
	case *goast.InterfaceType:
		if len(v.Methods.List) == 0 {
			return ffitypes.NomiDynamic, nil
		}
		return "", fmt.Errorf("non-empty interface %s", goNodeString(expr))
	case *goast.StructType:
		return "", fmt.Errorf("anonymous struct type %s", goNodeString(expr))
	case *goast.Ellipsis:
		return "", fmt.Errorf("variadic type %s", goNodeString(expr))
	case *goast.ChanType:
		return "", fmt.Errorf("channel type %s is not automatically converted; declare it as an opaque type or normalize it in Go", goNodeString(expr))
	case *goast.SelectorExpr:
		if importPath, ok := selectorImportPath(v, imports); ok {
			if nomi, projected := ffitypes.NomiForGoStdlibType(importPath, v.Sel.Name); projected {
				return nomi, nil
			}
		}
		return "", fmt.Errorf("unbound named type %s", goNodeString(expr))
	}
	return "", fmt.Errorf("unsupported Go type %s", goNodeString(expr))
}

func goParamExprAt(fields *goast.FieldList, idx int) goast.Expr {
	return goFieldExprAt(fields, idx)
}

func goReturnExprAt(fields *goast.FieldList, idx int) goast.Expr {
	return goFieldExprAt(fields, idx)
}

func goFieldExprAt(fields *goast.FieldList, idx int) goast.Expr {
	if fields == nil || idx < 0 {
		return nil
	}
	pos := 0
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		if idx < pos+count {
			return field.Type
		}
		pos += count
	}
	return nil
}

func resultOkType(t nomiast.TypeExpr) nomiast.TypeExpr {
	if t == nil {
		return nil
	}
	if q, ok := t.(*nomiast.QualifiedType); ok {
		return resultOkType(q.Member)
	}
	if g, ok := t.(*nomiast.GenericType); ok && g.Name == "Result" && len(g.Params) == 2 {
		return g.Params[0]
	}
	return t
}

// nomiReturnElems splits a declared return type into the per-Go-result
// slots it covers: a tuple contributes one element per Go result, any
// other type is a single slot. The parser spells a tuple as a FuncType
// with no arrow (nil Return); arity < 2 is Unit (`()`) or collapsed
// grouping, neither of which is a tuple.
func nomiReturnElems(t nomiast.TypeExpr) []nomiast.TypeExpr {
	if q, ok := t.(*nomiast.QualifiedType); ok {
		return nomiReturnElems(q.Member)
	}
	if ft, ok := t.(*nomiast.FuncType); ok && ft.Return == nil && len(ft.Params) >= 2 {
		return ft.Params
	}
	return []nomiast.TypeExpr{t}
}

func validateProjectedStructShape(loc, funcName, importPath, label string, goExpr goast.Expr, nomiType nomiast.TypeExpr, boundTypes goTypeBindings, goStructs map[string]goStructSymbol, nomiStructs map[string]*nomiast.StructDef) []string {
	goName, ok := goNamedStructName(goExpr)
	if !ok {
		return nil
	}
	goStruct, ok := goStructs[goName]
	if !ok {
		return nil
	}
	nomiName, ok := nomiStructTypeName(nomiType)
	if !ok {
		return nil
	}
	nomiStruct := nomiStructs[nomiName]
	if nomiStruct == nil {
		return nil
	}
	nomiFields := nomiStructFieldTypes(nomiStruct)
	var problems []string
	for name, goFieldExpr := range goStruct.fields {
		nomiFieldType, ok := nomiFields[name]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: signature mismatch for Go function %q in package %q: %s field %q exists in Go %s but not in Nomi %s",
				loc, funcName, importPath, label, name, goName, nomiName))
			continue
		}
		projected, err := goExprNomiType(goFieldExpr, boundTypes, goStruct.imports, true)
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s: Go function %q in package %q has unsupported %s field %q type: %s",
				loc, funcName, importPath, label, name, err))
			continue
		}
		if projected != nomiFieldType {
			problems = append(problems, fmt.Sprintf(
				"%s: signature mismatch for Go function %q in package %q: %s field %q projects to %s, but Nomi %s declares %s",
				loc, funcName, importPath, label, name, projected, nomiName, nomiFieldType))
		}
	}
	for name := range nomiFields {
		if _, ok := goStruct.fields[name]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: signature mismatch for Go function %q in package %q: %s field %q exists in Nomi %s but not in Go %s",
				loc, funcName, importPath, label, name, nomiName, goName))
		}
	}
	return problems
}

func goNamedStructName(expr goast.Expr) (string, bool) {
	if ident, ok := expr.(*goast.Ident); ok {
		return ident.Name, true
	}
	return "", false
}

func nomiStructTypeName(t nomiast.TypeExpr) (string, bool) {
	switch v := t.(type) {
	case *nomiast.SimpleType:
		return v.Name, true
	case *nomiast.QualifiedType:
		return nomiStructTypeName(v.Member)
	default:
		return "", false
	}
}

func nomiStructFieldTypes(def *nomiast.StructDef) map[string]string {
	out := make(map[string]string, len(def.Fields))
	for _, field := range def.Fields {
		if field.TypeAnnotation == nil {
			out[field.Name] = ""
			continue
		}
		out[field.Name] = field.TypeAnnotation.TypeString()
	}
	return out
}

func isProjectedNomiMapKeyType(t string) bool {
	if t == "" {
		return false
	}
	return !strings.HasPrefix(t, "List<") &&
		!strings.HasPrefix(t, "Map<") &&
		!strings.HasPrefix(t, "(")
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
func goIdentNomiType(name string, boundTypes goTypeBindings, allowCallbacks bool) (string, error) {
	if name == "error" {
		return "error", nil
	}
	if nomi, projected := ffitypes.NomiForGoIdent(name); projected {
		return nomi, nil
	}
	if binding, ok := boundTypes[name]; ok {
		return goNamedTypeNomiType(name, binding, boundTypes, allowCallbacks)
	}
	return "", fmt.Errorf("unbound named type %s", name)
}

func goNamedTypeNomiType(name string, binding goTypeBinding, boundTypes goTypeBindings, allowCallbacks bool) (string, error) {
	if binding.explicit {
		return binding.localName, nil
	}
	sym := binding.symbol
	switch sym.kind {
	case goTypeStruct:
		return binding.localName, nil
	case goTypeInterface:
		if sym.emptyInterface {
			return ffitypes.NomiDynamic, nil
		}
		return "", fmt.Errorf("named Go interface type %s is not automatically converted; declare it as an opaque type or normalize it in Go", name)
	case goTypeFunc:
		return "", fmt.Errorf("named Go function type %s is not automatically converted; use an unnamed function parameter in the Go adapter or declare it as an opaque type", name)
	case goTypeChan:
		return "", fmt.Errorf("named Go channel type %s is not automatically converted; declare it as an opaque type or normalize it in Go", name)
	case goTypeInvalid:
		return "", fmt.Errorf("unsupported named Go type %s", name)
	}
	projected, err := goExprNomiType(sym.expr, boundTypes, sym.imports, allowCallbacks)
	if err != nil {
		return "", fmt.Errorf("named Go %s type %s: %w", sym.kind, name, err)
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
	return "", false, nil
}

func goModProvidesImportPath(projectRoot, importPath string) bool {
	if isStandardLibraryImport(importPath) {
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

func isStandardLibraryImport(importPath string) bool {
	pkg, err := build.Default.Import(importPath, "", build.FindOnly)
	return err == nil && pkg.Goroot
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
	symbols := goPackageSymbols{
		funcs:   make(map[string]goFuncSymbol),
		types:   make(map[string]goTypeSymbol),
		structs: make(map[string]goStructSymbol),
	}
	parsed := 0
	fset := gotoken.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
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
