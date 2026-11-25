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
	key  declKey
	prog *Program
	node ast.Node
	typ  analysis.Type // the declaration's type, its parameters unbound
	// root reports a declaration AddFile may start from: one the
	// checker records as public or internal to its module, not private
	// to its file. A private type is written only when a root reaches it.
	root  bool
	alias bool
	// jsonKey is the object key the derived impl writes for a field, or nil
	// when the type derives neither ToJson nor FromJson.
	jsonKey func(string) string
	// handWritten reports an `impl ToJson` written by hand, in any file of
	// the program.
	handWritten bool
	// read reports a type that derives FromJson and has no ToJson: one a
	// frontend sends, so its declaration is the JSON its FromJson reads.
	read   bool
	used   bool
	tsName string
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
	// cur is the declaration whose body is being walked, whose type
	// parameters a type argument may name.
	cur *decl
	// presence holds each type parameter some reached instantiation puts a
	// Presence at, and flows each parameter passed on as another
	// declaration's type argument (noteArgs). Seal closes presence over
	// flows.
	presence map[param]bool
	flows    map[param][]param
}

// param is a type parameter of a declaration, by name.
type param struct {
	d    *decl
	name string
}

// NewWriter is a Writer writing Ints as ints says.
func NewWriter(ints Ints) *Writer {
	return &Writer{ints: ints, decls: map[declKey]*decl{}, byFile: map[string][]*decl{}, reserved: map[string]bool{"Record": true},
		presence: map[param]bool{}, flows: map[param][]param{}}
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
	var alias bool
	switch d := n.(type) {
	case *ast.StructDef:
		name = d.Name
	case *ast.EnumDef:
		name = d.Name
	case *ast.TypeDef:
		name = d.Name
	case *ast.ExternType:
		name = d.Name
	case *ast.TypeAlias:
		if d.TargetTypeExpr == nil {
			return nil
		}
		name, alias = d.Name, true
	default:
		return nil
	}
	sym := m.FA.ModuleScope.LookupLocal(name)
	if sym == nil || sym.Type == nil {
		return nil
	}
	d := &decl{key: declKey{m.Path, name}, prog: p, node: n, typ: sym.Type, root: sym.Visibility != ast.Private, alias: alias}
	if !alias {
		// The program's impl record, so an `impl ToJson` in another file of
		// the module counts as one in the declaring file does.
		if key, ok := analysis.DerivedJSONKeys(n, "ToJson"); ok {
			d.jsonKey = key
		} else if analysis.ImplementsToJSON(m.FA, sym.Type) {
			d.handWritten = true
		} else if key, ok := analysis.DerivedJSONKeys(n, "FromJson"); ok {
			d.jsonKey, d.read = key, true
		}
	}
	return d
}

// AddFile adds the file's types that cross as JSON and are not private to
// it, each with every type it reaches, and answers how many it added. A
// type alias counts when the type it names crosses as JSON.
func (w *Writer) AddFile(path string) int {
	n := 0
	for _, d := range w.byFile[path] {
		if !d.root {
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
	var work []param
	for p := range w.presence {
		work = append(work, p)
	}
	for len(work) > 0 {
		p := work[len(work)-1]
		work = work[:len(work)-1]
		for _, to := range w.flows[p] {
			if !w.presence[to] {
				w.presence[to] = true
				work = append(work, to)
			}
		}
	}
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
		var note string
		switch {
		case d.alias:
		case d.handWritten:
			note = d.key.name + "'s ToJson is written by hand, so its JSON is not known here."
		case d.jsonKey == nil:
			note = d.key.name + " has no JSON encoding the declarations can read."
		case d.read:
			note = d.key.name + " has FromJson and no ToJson, so this is the JSON it reads."
		}
		if note != "" {
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
	outer := w.cur
	w.cur = d
	defer func() { w.cur = outer }()
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
// line when sep is "\n", else on one line. A derived impl writes and reads
// a struct's fields, and a struct variant's, with the field type's
// `to_json_field` and `from_missing`, so a `Presence<T>` field is an
// optional property of T: `Absent` leaves its key out, and a missing key
// reads as `Absent`. In a type d.read marks, a field whose missing key
// FromJson fills is optional too: an `Option` (None) or one with a default.
// In any other type, a field whose type is a type parameter is optional
// when some instantiation the roots reach puts a Presence there, since
// ToJson then leaves out its key for Absent: the declaration is written
// once, so it holds for every instantiation. In a type d.read marks it
// stays required, which every instantiation's FromJson accepts.
func (w *Writer) fields(d *decl, fields []analysis.FieldDef, sep string, depth int) string {
	if len(fields) == 0 {
		return "{}"
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		t, optional := f.Type, false
		if inner, ok := stdArg(f.Type, "Presence"); ok {
			t, optional = inner, true
		} else if d.read {
			_, isOption := stdArg(f.Type, "Option")
			optional = isOption || f.HasDefault
		} else if name, ok := typeParamName(f.Type); ok {
			optional = w.presence[param{d, name}]
		}
		key := field(d.jsonKey(f.Name))
		if optional {
			key += "?"
		}
		parts[i] = key + ": " + w.ts(d.prog, t, depth+1)
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

// stdArg is the type argument of t when t is the std enum name of one type
// parameter (Option, Presence).
func stdArg(t analysis.Type, name string) (analysis.Type, bool) {
	switch v := t.(type) {
	case *analysis.TypeVar:
		if v.Resolved != nil {
			return stdArg(v.Resolved, name)
		}
	case *analysis.EnumType:
		if v.Name == name && isStd(v.Origin) && len(v.TypeArgs) == 1 {
			return v.TypeArgs[0], true
		}
	}
	return nil, false
}

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
	case "Option":
		return arg() + " | null", true
	case "Presence":
		// Outside a struct field (fields), Present(v) writes v and Absent
		// writes null.
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
	if !w.sealed {
		w.noteArgs(d, args)
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

// noteArgs records, for fields, which of d's type parameters args puts a
// Presence at, and which it fills with a type parameter of the declaration
// being walked, through which a Presence reaches d once Seal closes over
// them.
func (w *Writer) noteArgs(d *decl, args []analysis.Type) {
	params := typeParams(d.typ)
	for i, a := range args {
		if i >= len(params) {
			break
		}
		to := param{d, params[i]}
		if _, ok := stdArg(a, "Presence"); ok {
			w.presence[to] = true
		} else if name, ok := typeParamName(a); ok && w.cur != nil {
			from := param{w.cur, name}
			w.flows[from] = append(w.flows[from], to)
		}
	}
}

// typeParamName is the name of the type parameter t is.
func typeParamName(t analysis.Type) (string, bool) {
	switch v := t.(type) {
	case *analysis.TypeVar:
		if v.Resolved != nil {
			return typeParamName(v.Resolved)
		}
	case *analysis.TypeParam_:
		return v.Name_, true
	}
	return "", false
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
