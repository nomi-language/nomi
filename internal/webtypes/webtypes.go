// Package webtypes writes nomi.d.ts, the TypeScript declarations `nomi
// build --web` puts beside nomi.js: the loader's API (load, run, mount),
// and a typed `call` overload for each of the program's exported functions.
//
// A Nomi type maps to the TypeScript type of the JSON its ToJson writes and
// its FromJson reads:
//
//	Int, Float              number
//	String                  string
//	Bool                    boolean
//	List, Vector, Set<T>    T[]
//	Map<String, V>          Record<string, V>
//	Maybe<T>                T | null
//	Unit (a result)         void
//	a derived struct        an interface of its fields, by their JSON keys
//	a derived enum          a union: "Bare" | { Payload: ... }
//	a derived distinct      its inner type
//
// A type whose ToJson and FromJson are written by hand, and any other type,
// is `unknown`: the declarations cannot know what its impl writes.
package webtypes

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/irbuild"
)

// Declarations is nomi.d.ts for a program whose entry file is named entry
// (`hangman.nomi`), whose exported functions are exports, and whose user
// files hold nodes.
func Declarations(entry string, exports []irbuild.Export, nodes [][]ast.Node) string {
	w := &writer{decls: map[string][]ast.Node{}, named: map[string]string{}}
	for _, ns := range nodes {
		for _, n := range ns {
			if name := declName(n); name != "" {
				w.decls[name] = append(w.decls[name], n)
			}
		}
	}
	var calls []string
	for _, e := range exports {
		var params []string
		for i, p := range e.Params {
			params = append(params, fmt.Sprintf("%s: %s", paramName(p.Name, i), w.ts(p.Type, 0)))
		}
		result := "void"
		if !analysis.IsUnitType(e.Result) {
			result = w.ts(e.Result, 0)
		}
		args := ""
		if len(params) > 0 {
			args = ", " + strings.Join(params, ", ")
		}
		calls = append(calls, fmt.Sprintf("  call(name: %s%s): Promise<%s>;", quote(e.Name), args, result))
	}
	if len(calls) == 0 {
		calls = append(calls, "  call(name: string, ...args: unknown[]): Promise<unknown>;")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// nomi.d.ts, written by `nomi build --web` for %s: the types of nomi.js's\n", entry)
	b.WriteString("// API, with a typed `call` for each exported function.\n\n")
	for _, d := range w.out {
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	b.WriteString("/** A program `load` started, whose exported functions a page calls. */\n")
	b.WriteString("export interface NomiProgram {\n")
	b.WriteString("  /** Calls an exported function. Rejects with the Nomi diagnostic when it fails; the program stays loaded. */\n")
	for _, c := range calls {
		b.WriteString(c + "\n")
	}
	b.WriteString(api)
	return b.String()
}

// api is the rest of nomi.js's API, after NomiProgram's call overloads.
const api = `  /** The exported functions, with their Nomi signatures. */
  exports(): Promise<NomiExport[]>;
  /** Stops the program's worker. Pending calls reject. */
  close(): void;
}

export interface NomiExport {
  name: string;
  params: { name: string; type: string }[];
  /** The result type as Nomi writes it, or "" for a Unit result. */
  result: string;
  /** The declaration without ` + "`export fn`" + `: "guess(game: Game, letter: String): Game". */
  signature: string;
}

export interface NomiLoadOptions {
  /** Startup.args. */
  args?: string[];
  /** Startup.env; NOMI_COLOR=never unless set here. */
  env?: Record<string, string>;
  /** Output as written; default console.log per line. */
  stdout?: (text: string) => void;
  /** Default console.error per line. */
  stderr?: (text: string) => void;
  /** The folder Locale.load fetches <tag>.nomil from; default locales/ beside the module. */
  locales?: string | URL;
}

export interface NomiRunOptions extends NomiLoadOptions {
  stdin?: string | (() => Promise<string | null> | string | null);
  timeoutMs?: number;
  signal?: AbortSignal;
  worker?: boolean;
}

export interface NomiRunResult {
  exitCode: number;
  fatal?: string;
}

/** Starts the program at url in a worker, runs its main when it has one, and resolves when its exported functions can be called. */
export function load(url: string | URL, options?: NomiLoadOptions): Promise<NomiProgram>;

/** Runs the program at url to its end. */
export function run(url: string | URL, options?: NomiRunOptions): Promise<NomiRunResult>;

/** Mounts a terminal on el and runs the program its data-nomi-program names. */
export function mount(el: Element, url?: string): void;
`

type writer struct {
	decls map[string][]ast.Node
	// named maps a declared type's name to the name it is declared under
	// in out, once it has been (or is being) written.
	named map[string]string
	out   []string
}

// maxDepth bounds the nesting of inlined generic types, which could recur.
const maxDepth = 8

// ts is t as a TypeScript type.
func (w *writer) ts(t analysis.Type, depth int) string {
	if depth > maxDepth {
		return "unknown"
	}
	switch v := t.(type) {
	case *analysis.TypeVar:
		if v.Resolved != nil {
			return w.ts(v.Resolved, depth)
		}
		return "unknown"
	case *analysis.PrimitiveType:
		switch v.Name_ {
		case "Int", "Float":
			return "number"
		case "String":
			return "string"
		case "Bool":
			return "boolean"
		case "Unit":
			return "null"
		}
		return "unknown"
	case *analysis.ListType:
		return array(w.ts(v.Elem, depth+1))
	case *analysis.MapType:
		if k, ok := v.Key.(*analysis.PrimitiveType); ok && k.Name_ == "String" {
			return "Record<string, " + w.ts(v.Val, depth+1) + ">"
		}
		return "unknown"
	case *analysis.StructType:
		if std(v.Origin) {
			if v.Name == "Set" && len(v.TypeArgs) == 1 {
				return array(w.ts(v.TypeArgs[0], depth+1))
			}
			return "unknown"
		}
		return w.declared(v.Name, v.TypeParams, v.TypeArgs, depth)
	case *analysis.EnumType:
		if std(v.Origin) {
			switch {
			case v.Name == "Bool":
				return "boolean"
			case v.Name == "Maybe" && len(v.TypeArgs) == 1:
				return w.ts(v.TypeArgs[0], depth+1) + " | null"
			}
			return "unknown"
		}
		return w.declared(v.Name, v.TypeParams, v.TypeArgs, depth)
	case *analysis.DistinctType:
		if std(v.Origin) {
			if v.Name == "Vector" && len(v.TypeArgs) == 1 {
				return array(w.ts(v.TypeArgs[0], depth+1))
			}
			return "unknown"
		}
		return w.declared(v.Name, v.TypeParams, v.TypeArgs, depth)
	}
	return "unknown"
}

func std(origin string) bool { return strings.HasPrefix(origin, "std/") || origin == "std" }

// declared is a type the program declares: its name, once its declaration
// is written, for a type with no type arguments; its shape written inline
// for a generic one.
func (w *writer) declared(name string, params []string, args []analysis.Type, depth int) string {
	decls := w.decls[name]
	if len(decls) != 1 {
		// Two files declare the name, or none the builder can see.
		return "unknown"
	}
	decl := decls[0]
	key, derived := analysis.DerivedJSONKeys(decl, "ToJson")
	if !derived {
		if key, derived = analysis.DerivedJSONKeys(decl, "FromJson"); !derived {
			return "unknown"
		}
	}
	if len(params) > 0 {
		env := map[string]analysis.Type{}
		for i, p := range params {
			if i < len(args) {
				env[p] = args[i]
			}
		}
		return w.shape(decl, key, env, depth+1)
	}
	if as, ok := w.named[name]; ok {
		return as
	}
	w.named[name] = name
	shape := w.shape(decl, key, nil, depth+1)
	if _, isEnum := decl.(*ast.EnumDef); isEnum || !strings.HasPrefix(shape, "{") {
		w.out = append(w.out, fmt.Sprintf("export type %s = %s;", name, shape))
	} else {
		w.out = append(w.out, fmt.Sprintf("export interface %s %s", name, shape))
	}
	return name
}

// shape is the JSON shape of decl's derived ToJson, with env binding its
// type parameters.
func (w *writer) shape(decl ast.Node, key func(string) string, env map[string]analysis.Type, depth int) string {
	switch d := decl.(type) {
	case *ast.StructDef:
		return w.object(d.Fields, key, env, depth)
	case *ast.EnumDef:
		var arms []string
		for _, v := range d.Variants {
			switch v.Kind {
			case "bare":
				arms = append(arms, quote(v.Name))
			case "positional":
				arms = append(arms, "{ "+field(v.Name)+": "+w.texpr(v.DataTypeExpr, env, depth)+" }")
			case "struct":
				arms = append(arms, "{ "+field(v.Name)+": "+w.object(v.Fields, key, env, depth)+" }")
			case "embedded":
				arms = append(arms, "{ "+field(v.Name)+": "+w.texpr(v.EmbeddedTypeExpr, env, depth)+" }")
			}
		}
		if len(arms) == 0 {
			return "never"
		}
		return strings.Join(arms, " | ")
	case *ast.TypeDef:
		if d.InnerTypeExpr == nil {
			return quote(d.Name)
		}
		return w.texpr(d.InnerTypeExpr, env, depth)
	}
	return "unknown"
}

func (w *writer) object(fields []ast.StructField, key func(string) string, env map[string]analysis.Type, depth int) string {
	if len(fields) == 0 {
		return "{}"
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = field(key(f.Name)) + ": " + w.texpr(f.TypeAnnotation, env, depth)
	}
	return "{ " + strings.Join(parts, "; ") + " }"
}

// texpr is a type expression of a declaration as a TypeScript type. It is
// read by name: the builtin and std names, a type parameter env binds, a
// tuple, and a type the program declares.
func (w *writer) texpr(te ast.TypeExpr, env map[string]analysis.Type, depth int) string {
	if depth > maxDepth || te == nil {
		return "unknown"
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		if bound, ok := env[t.Name]; ok {
			return w.ts(bound, depth+1)
		}
		switch t.Name {
		case "Int", "Float":
			return "number"
		case "String":
			return "string"
		case "Bool":
			return "boolean"
		}
		return w.declared(t.Name, nil, nil, depth)
	case *ast.GenericType:
		arg := func(i int) string {
			if i < len(t.Params) {
				return w.texpr(t.Params[i], env, depth+1)
			}
			return "unknown"
		}
		switch t.Name {
		case "List", "Vector", "Set":
			return array(arg(0))
		case "Maybe":
			return arg(0) + " | null"
		case "Map":
			if k, ok := t.Params[0].(*ast.SimpleType); ok && k.Name == "String" && len(t.Params) == 2 {
				return "Record<string, " + arg(1) + ">"
			}
			return "unknown"
		}
		// A generic type the program declares, at these arguments: its
		// arguments are written in the declaring file's terms, which the
		// checker resolved; written inline as unknown rather than guessed.
		return "unknown"
	case *ast.FuncType:
		if t.Return != nil || len(t.Params) < 2 {
			return "unknown"
		}
		items := make([]string, len(t.Params))
		for i, p := range t.Params {
			items[i] = w.texpr(p, env, depth+1)
		}
		return "[" + strings.Join(items, ", ") + "]"
	}
	return "unknown"
}

func declName(n ast.Node) string {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Name
	case *ast.EnumDef:
		return d.Name
	case *ast.TypeDef:
		return d.Name
	}
	return ""
}

func array(elem string) string {
	if strings.ContainsAny(elem, " |") {
		return "(" + elem + ")[]"
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

// tsReserved are the words a TypeScript parameter cannot be named.
var tsReserved = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true,
	"debugger": true, "default": true, "delete": true, "do": true, "else": true, "enum": true,
	"export": true, "extends": true, "false": true, "finally": true, "for": true, "function": true,
	"if": true, "import": true, "in": true, "instanceof": true, "new": true, "null": true,
	"return": true, "super": true, "switch": true, "this": true, "throw": true, "true": true,
	"try": true, "typeof": true, "var": true, "void": true, "while": true, "with": true,
	"yield": true, "let": true, "static": true, "implements": true, "interface": true,
	"package": true, "private": true, "protected": true, "public": true, "await": true, "name": true,
}

// paramName is a Nomi parameter name as a TypeScript one: `?` and any other
// character an identifier cannot hold become `_`, a reserved word (and
// `name`, the overload's first parameter) gains a trailing `_`, and a
// destructured parameter is `argN`.
func paramName(name string, i int) string {
	if name == "" || name == "_" {
		return fmt.Sprintf("arg%d", i+1)
	}
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if tsReserved[out] {
		out += "_"
	}
	return out
}
