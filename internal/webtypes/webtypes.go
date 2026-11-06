// Package webtypes writes TypeScript declarations for Nomi types: nomi.d.ts,
// which `nomi build --web` puts beside nomi.js, and the file `nomi types`
// writes for a frontend that talks to a Nomi backend over HTTP. Both map a
// Nomi type to the TypeScript type of the JSON its ToJson writes, through
// one Writer:
//
//	Int                              number | bigint (nomi.d.ts), number (nomi types)
//	Float                            number
//	String                           string
//	Bool                             boolean
//	Decimal                          string, its exact digits ("12.50")
//	Uuid                             string, lowercase 8-4-4-4-12 hex
//	Instant                          string, RFC 3339 in UTC
//	Date, Time, NaiveDateTime        string
//	List, Vector, Set<T>             T[]
//	Map<String, V>                   Record<string, V>
//	Maybe<T>                         T | null
//	a tuple payload                  [A, B]
//	Unit (a result)                  void
//	a derived struct                 an interface of its fields, by their JSON keys
//	a derived enum                   a union: "Bare" | { Payload: ... }
//	a derived distinct type          its inner type
//	a generic declaration            a generic interface or type
//
// A declared type is written once, under its Nomi name, and referred to by
// that name. A type whose ToJson is written by hand is declared `unknown`:
// the declarations cannot know what its impl writes. So is any type with
// no JSON encoding at all, such as a Map with non-String keys.
package webtypes

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/irbuild"
)

// Ints is how an Int is written.
type Ints int

const (
	// IntsExact is nomi.js's Int: it hands JavaScript an Int past
	// Number.MAX_SAFE_INTEGER as a BigInt, and takes a BigInt argument as
	// the Int it holds.
	IntsExact Ints = iota
	// IntsNumber is an Int that JSON.parse read: always a number, rounded
	// past 2^53.
	IntsNumber
)

// Module is one checked file of a program: its absolute path, its nodes as
// the front end left them (derives lowered to decorators) and its analysis.
type Module struct {
	Path  string
	Nodes []ast.Node
	FA    *analysis.FileAnalysis
}

// ModulesOf is the modules of an analyzed program.
func ModulesOf(p *irbuild.Program) []Module {
	mods := make([]Module, len(p.Modules))
	for i, m := range p.Modules {
		mods[i] = Module{Path: m.Path, Nodes: m.Nodes, FA: m.FA}
	}
	return mods
}

// Program is a program added to a Writer: the declarations of its files,
// and which file each type origin it names is.
type Program struct {
	pathOf map[string]string // a FileAnalysis origin → its file
}

// declKey identifies a declaration across programs. An origin names a file
// only within one program (every entry is analysis.OriginEntry), so a
// declaration is keyed by its file.
type declKey struct{ path, name string }

// decl is a type declaration a program's file holds.
type decl struct {
	key    declKey
	prog   *Program
	node   ast.Node
	typ    analysis.Type // the declaration's type, its parameters unbound
	public bool
	alias  bool
	// jsonKey is the object key the derived impl writes for a field, or nil
	// when the type derives neither ToJson nor FromJson.
	jsonKey func(string) string
	// handWritten reports an `impl ToJson` the file writes by hand.
	handWritten bool
	used        bool
	tsName      string
}

// Writer collects the declarations a set of root types reaches and writes
// them. Use it in two phases: name every root (AddFile, Visit), then Seal,
// then write references (TS) and the declarations (Declarations).
type Writer struct {
	ints   Ints
	decls  map[declKey]*decl
	byFile map[string][]*decl
	order  []*decl
	sealed bool
	// reserved are the names no declaration may take (Reserve).
	reserved map[string]bool
}

// NewWriter is a Writer writing Ints as ints says.
func NewWriter(ints Ints) *Writer {
	return &Writer{ints: ints, decls: map[declKey]*decl{}, byFile: map[string][]*decl{}, reserved: map[string]bool{"Record": true}}
}

// AddProgram indexes the type declarations of a program's modules. A file
// a program shares with one added before keeps its first declarations.
func (w *Writer) AddProgram(mods []Module) *Program {
	p := &Program{pathOf: map[string]string{}}
	for _, m := range mods {
		if m.FA == nil {
			continue
		}
		p.pathOf[m.FA.Origin] = m.Path
		if _, seen := w.byFile[m.Path]; seen {
			continue
		}
		w.byFile[m.Path] = []*decl{}
		for _, n := range m.Nodes {
			d := w.declOf(p, m, n)
			if d == nil || w.decls[d.key] != nil {
				continue
			}
			w.decls[d.key] = d
			w.byFile[m.Path] = append(w.byFile[m.Path], d)
		}
	}
	return p
}

func (w *Writer) declOf(p *Program, m Module, n ast.Node) *decl {
	var name string
	var public, alias bool
	switch d := n.(type) {
	case *ast.StructDef:
		name, public = d.Name, d.Public
	case *ast.EnumDef:
		name, public = d.Name, d.Public
	case *ast.TypeDef:
		name, public = d.Name, d.Public
	case *ast.ExternType:
		name, public = d.Name, d.Public
	case *ast.TypeAlias:
		if d.TargetTypeExpr == nil {
			return nil
		}
		name, public, alias = d.Name, d.Public, true
	default:
		return nil
	}
	sym := m.FA.ModuleScope.LookupLocal(name)
	if sym == nil || sym.Type == nil {
		return nil
	}
	d := &decl{key: declKey{m.Path, name}, prog: p, node: n, typ: sym.Type, public: public, alias: alias}
	if !alias {
		if key, ok := analysis.DerivedJSONKeys(n, "ToJson"); ok {
			d.jsonKey = key
		} else if key, ok := analysis.DerivedJSONKeys(n, "FromJson"); ok {
			d.jsonKey = key
		} else {
			d.handWritten = m.FA.Impls[name]["ToJson"]
		}
	}
	return d
}

// AddFile adds the file's public types that cross as JSON, each with every
// type it reaches, and answers how many it added. A type alias counts when
// the type it names crosses as JSON.
func (w *Writer) AddFile(path string) int {
	n := 0
	for _, d := range w.byFile[path] {
		if !d.public {
			continue
		}
		switch {
		case d.alias:
			if !w.encodes(d.prog, d.typ, 0) {
				continue
			}
		case d.jsonKey == nil && !d.handWritten:
			continue
		}
		w.use(d)
		n++
	}
	return n
}

// Visit adds the declarations t reaches, for a reference TS writes once the
// Writer is sealed.
func (w *Writer) Visit(p *Program, t analysis.Type) { w.ts(p, t, 0) }

// TS is t as a TypeScript type, in p's terms. Call it after Seal.
func (w *Writer) TS(p *Program, t analysis.Type) string { return w.ts(p, t, 0) }

func (w *Writer) use(d *decl) {
	if d.used {
		return
	}
	d.used = true
	w.order = append(w.order, d)
	w.body(d) // reaches what the declaration names
}

// Reserve keeps names from the declarations: names the file declares or
// uses beside them. A Nomi type of one of them is qualified by its file, as
// two Nomi types of one name are. Every Writer reserves Record, which a Map
// is written with.
func (w *Writer) Reserve(names ...string) {
	for _, n := range names {
		w.reserved[n] = true
	}
}

// Seal names each declaration. Types that share a name are each qualified
// by their file's name, then by its directories', as few as tell them apart:
// `User` of models.nomi and of api.nomi are `ModelsUser` and `ApiUser`.
func (w *Writer) Seal() {
	w.sealed = true
	groups := map[string][]*decl{}
	var bases []string
	for _, d := range w.order {
		base := identOf(d.key.name)
		if groups[base] == nil {
			bases = append(bases, base)
		}
		groups[base] = append(groups[base], d)
	}
	taken := map[string]bool{}
	for _, base := range bases {
		if g := groups[base]; len(g) == 1 && !w.reserved[base] {
			g[0].tsName = base
			taken[base] = true
		}
	}
	for _, base := range bases {
		g := groups[base]
		if g[0].tsName != "" {
			continue
		}
		for k := 1; ; k++ {
			names := map[string]bool{}
			ok := true
			for i, d := range g {
				d.tsName = qualified(d.key.path, k) + base
				if k > 16 {
					d.tsName += fmt.Sprint(i + 1)
				}
				ok = ok && !names[d.tsName] && !taken[d.tsName] && !w.reserved[d.tsName]
				names[d.tsName] = true
			}
			if ok {
				break
			}
		}
		for _, d := range g {
			taken[d.tsName] = true
		}
	}
}

// qualified is the last k elements of a file's path, without .nomi, in
// PascalCase: `ModelsUser` for k = 2 and app/models/user.nomi.
func qualified(path string, k int) string {
	parts := strings.Split(filepath.ToSlash(strings.TrimSuffix(path, ".nomi")), "/")
	if k > len(parts) {
		k = len(parts)
	}
	var b strings.Builder
	for _, p := range parts[len(parts)-k:] {
		b.WriteString(pascal(p))
	}
	return b.String()
}

// Declarations is every declaration the roots reached, in the order they
// were reached, each followed by a blank line.
func (w *Writer) Declarations() string {
	var b strings.Builder
	for _, d := range w.order {
		doc := docOf(d.node)
		if !d.alias && d.jsonKey == nil {
			note := d.key.name + " has no JSON encoding the declarations can read."
			if d.handWritten {
				note = d.key.name + "'s ToJson is written by hand, so its JSON is not known here."
			}
			doc = strings.TrimSpace(doc + "\n\n" + note)
		}
		b.WriteString(jsDoc(doc, ""))
		b.WriteString(w.body(d))
		b.WriteString("\n\n")
	}
	return b.String()
}

// Len is how many declarations the roots reached.
func (w *Writer) Len() int { return len(w.order) }

// body is d's declaration.
func (w *Writer) body(d *decl) string {
	name := d.tsName
	if d.alias {
		return fmt.Sprintf("export type %s = %s;", name, w.ts(d.prog, d.typ, 1))
	}
	if params := typeParams(d.typ); len(params) > 0 {
		name += "<" + strings.Join(params, ", ") + ">"
	}
	if d.jsonKey == nil {
		return fmt.Sprintf("export type %s = unknown;", name)
	}
	switch t := d.typ.(type) {
	case *analysis.StructType:
		return "export interface " + name + " " + w.fields(d, t.Fields, "\n", 1)
	case *analysis.EnumType:
		var arms []string
		for _, v := range t.Variants {
			arms = append(arms, w.variant(d, v))
		}
		if len(arms) == 0 {
			return "export type " + name + " = never;"
		}
		one := "export type " + name + " = " + strings.Join(arms, " | ") + ";"
		if len(one) <= 100 {
			return one
		}
		return "export type " + name + " =\n  | " + strings.Join(arms, "\n  | ") + ";"
	case *analysis.DistinctType:
		if t.Inner == nil {
			return fmt.Sprintf("export type %s = %s;", name, quote(t.Name))
		}
		return fmt.Sprintf("export type %s = %s;", name, w.ts(d.prog, t.Inner, 1))
	case *analysis.PrimitiveType:
		// A host type with a derived ToJson writes its name.
		return fmt.Sprintf("export type %s = %s;", name, quote(t.Name_))
	}
	return fmt.Sprintf("export type %s = unknown;", name)
}

// fields is an object type of fields under d's JSON keys: one field per
// line when sep is "\n", else on one line.
func (w *Writer) fields(d *decl, fields []analysis.FieldDef, sep string, depth int) string {
	if len(fields) == 0 {
		return "{}"
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = field(d.jsonKey(f.Name)) + ": " + w.ts(d.prog, f.Type, depth+1)
	}
	if sep == "\n" {
		return "{\n  " + strings.Join(parts, ";\n  ") + ";\n}"
	}
	return "{ " + strings.Join(parts, "; ") + " }"
}

// variant is the JSON of one variant of a derived enum: a bare variant is
// its name, any other an object keyed by its name.
func (w *Writer) variant(d *decl, v analysis.VariantDef) string {
	var payload string
	switch v.Kind {
	case analysis.VariantBare:
		return quote(v.Name)
	case analysis.VariantStruct:
		payload = w.fields(d, v.Fields, " ", 1)
	case analysis.VariantEmbedded:
		t := v.Embedded
		if t == nil {
			t = v.DataType
		}
		payload = w.ts(d.prog, t, 1)
	default:
		payload = w.ts(d.prog, v.DataType, 1)
	}
	return "{ " + field(v.Name) + ": " + payload + " }"
}

// maxDepth bounds the nesting of a written type.
const maxDepth = 32

func (w *Writer) intTS() string {
	if w.ints == IntsNumber {
		return "number"
	}
	return "number | bigint"
}

// ts is t as a TypeScript type, in p's terms.
func (w *Writer) ts(p *Program, t analysis.Type, depth int) string {
	if depth > maxDepth || t == nil {
		return "unknown"
	}
	switch v := t.(type) {
	case *analysis.TypeVar:
		if v.Resolved != nil {
			return w.ts(p, v.Resolved, depth)
		}
		return "unknown"
	case *analysis.TypeParam_:
		return v.Name_
	case *analysis.PrimitiveType:
		if v.Origin == "" {
			switch v.Name_ {
			case "Int":
				return w.intTS()
			case "Float":
				return "number"
			case "String", "Decimal":
				return "string"
			case "Unit":
				return "null"
			}
			return "unknown"
		}
		return w.declared(p, v.Origin, v.Name_, nil, depth)
	case *analysis.ListType:
		return array(w.ts(p, v.Elem, depth+1))
	case *analysis.MapType:
		if k, ok := v.Key.(*analysis.PrimitiveType); ok && k == analysis.TypeString {
			return "Record<string, " + w.ts(p, v.Val, depth+1) + ">"
		}
		return "unknown"
	case *analysis.TupleType:
		items := make([]string, len(v.Elems))
		for i, e := range v.Elems {
			items[i] = w.ts(p, e, depth+1)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case *analysis.StructType:
		if s, ok := w.std(p, v.Origin, v.Name, v.TypeArgs, depth); ok {
			return s
		}
		return w.declared(p, v.Origin, v.Name, v.TypeArgs, depth)
	case *analysis.EnumType:
		if s, ok := w.std(p, v.Origin, v.Name, v.TypeArgs, depth); ok {
			return s
		}
		return w.declared(p, v.Origin, v.Name, v.TypeArgs, depth)
	case *analysis.DistinctType:
		if s, ok := w.std(p, v.Origin, v.Name, v.TypeArgs, depth); ok {
			return s
		}
		return w.declared(p, v.Origin, v.Name, v.TypeArgs, depth)
	}
	return "unknown"
}

func isStd(origin string) bool { return strings.HasPrefix(origin, "std/") || origin == "std" }

// std is a std type as the JSON its std impl writes, or unknown for a std
// type with no ToJson; ok is false for a type std does not declare.
func (w *Writer) std(p *Program, origin, name string, args []analysis.Type, depth int) (string, bool) {
	if !isStd(origin) {
		return "", false
	}
	arg := func() string {
		if len(args) == 1 {
			return w.ts(p, args[0], depth+1)
		}
		return "unknown"
	}
	switch name {
	case "Bool":
		return "boolean", true
	case "Maybe":
		return arg() + " | null", true
	case "Set", "Vector":
		return array(arg()), true
	case "Instant", "Date", "Time", "NaiveDateTime", "Uuid":
		return "string", true
	}
	return "unknown", true
}

// declared is a type a program file declares: its name, with its type
// arguments.
func (w *Writer) declared(p *Program, origin, name string, args []analysis.Type, depth int) string {
	d := w.decls[declKey{p.pathOf[origin], name}]
	if d == nil {
		return "unknown"
	}
	w.use(d)
	ref := d.tsName
	if !w.sealed {
		ref = name
	}
	params := typeParams(d.typ)
	if len(params) == 0 {
		return ref
	}
	items := make([]string, len(params))
	for i := range params {
		items[i] = "unknown"
		if i < len(args) {
			items[i] = w.ts(p, args[i], depth+1)
		}
	}
	return ref + "<" + strings.Join(items, ", ") + ">"
}

// encodes reports whether t crosses as JSON through a std or derived impl,
// or a hand-written one.
func (w *Writer) encodes(p *Program, t analysis.Type, depth int) bool {
	if depth > maxDepth {
		return false
	}
	all := func(ts ...analysis.Type) bool {
		for _, e := range ts {
			if !w.encodes(p, e, depth+1) {
				return false
			}
		}
		return true
	}
	switch v := t.(type) {
	case *analysis.TypeVar:
		return v.Resolved != nil && w.encodes(p, v.Resolved, depth)
	case *analysis.PrimitiveType:
		switch v {
		case analysis.TypeInt, analysis.TypeFloat, analysis.TypeString, analysis.TypeDecimal:
			return true
		}
		return w.derivedOrHandWritten(p, v.Origin, v.Name_)
	case *analysis.ListType:
		return all(v.Elem)
	case *analysis.MapType:
		return v.Key == analysis.TypeString && all(v.Val)
	case *analysis.StructType:
		if s, ok := w.std(p, v.Origin, v.Name, nil, maxDepth); ok {
			return s != "unknown" && all(v.TypeArgs...)
		}
		return w.derivedOrHandWritten(p, v.Origin, v.Name) && all(v.TypeArgs...)
	case *analysis.EnumType:
		if s, ok := w.std(p, v.Origin, v.Name, nil, maxDepth); ok {
			return s != "unknown" && all(v.TypeArgs...)
		}
		return w.derivedOrHandWritten(p, v.Origin, v.Name) && all(v.TypeArgs...)
	case *analysis.DistinctType:
		if s, ok := w.std(p, v.Origin, v.Name, nil, maxDepth); ok {
			return s != "unknown" && all(v.TypeArgs...)
		}
		return w.derivedOrHandWritten(p, v.Origin, v.Name) && all(v.TypeArgs...)
	}
	return false
}

func (w *Writer) derivedOrHandWritten(p *Program, origin, name string) bool {
	d := w.decls[declKey{p.pathOf[origin], name}]
	return d != nil && (d.jsonKey != nil || d.handWritten)
}

// typeParams are a declaration's type parameter names.
func typeParams(t analysis.Type) []string {
	switch v := t.(type) {
	case *analysis.StructType:
		return v.TypeParams
	case *analysis.EnumType:
		return v.TypeParams
	case *analysis.DistinctType:
		return v.TypeParams
	}
	return nil
}

func docOf(n ast.Node) string {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Doc
	case *ast.EnumDef:
		return d.Doc
	case *ast.TypeDef:
		return d.Doc
	case *ast.ExternType:
		return d.Doc
	case *ast.TypeAlias:
		return d.Doc
	}
	return ""
}

// jsDoc is doc as a JSDoc comment indented by indent, or "" for no doc.
func jsDoc(doc, indent string) string {
	doc = strings.TrimSpace(strings.ReplaceAll(doc, "*/", "*\\/"))
	if doc == "" {
		return ""
	}
	lines := strings.Split(doc, "\n")
	if len(lines) == 1 {
		return indent + "/** " + lines[0] + " */\n"
	}
	var b strings.Builder
	b.WriteString(indent + "/**\n")
	for _, l := range lines {
		b.WriteString(strings.TrimRight(indent+" * "+l, " ") + "\n")
	}
	b.WriteString(indent + " */\n")
	return b.String()
}

// identOf is a Nomi type name as a TypeScript identifier: `Json.Case` is
// `JsonCase`.
func identOf(name string) string {
	return strings.ReplaceAll(name, ".", "")
}

// pascal is a file or directory name in PascalCase: `user_api` is `UserApi`.
func pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '_' || r == '-' || r == '.' || r == ' ' {
			up = true
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// array is an array of elem, parenthesized when elem is a union.
func array(elem string) string {
	nest := 0
	for _, r := range elem {
		switch r {
		case '<', '[', '{', '(':
			nest++
		case '>', ']', '}', ')':
			nest--
		case '|':
			if nest == 0 {
				return "(" + elem + ")[]"
			}
		}
	}
	return elem + "[]"
}

var tsIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// field is a property name, quoted when it is not an identifier.
func field(name string) string {
	if tsIdent.MatchString(name) {
		return name
	}
	return quote(name)
}

func quote(s string) string {
	return fmt.Sprintf("%q", s)
}
