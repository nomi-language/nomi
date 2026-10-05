// Package hostpair derives one fact from `.nomi` source: which Go symbol
// implements which Nomi host declaration.
//
// That fact is derived in more than one place, and keeping those places
// in agreement is the reason this package and internal/ffitypes exist:
//
//   - internal/ffirun/discovery.go builds it for the generated-wrapper path.
//     Its key rule is `externDeclKey`; its symbol is `pkg.Alias + "." +
//     exp.FuncName`, spelled by codegen.go's wrapper template.
//   - internal/irbuild/stdlib.go keys stdlib declarations with `stdKey`, which
//     is not ffirun's rule (see Pairing.BuilderKey / Pairing.BindingKey).
//   - internal/stdlibbindings holds hand-written rows. Every std declaration
//     is a bare `host fn`, so the symbol half of those rows is in no `.nomi`
//     file and no other derivable place, as symbolsource_test.go checks.
//     Its key half is still derivable, and
//     registered_test.go is the check that holds it.
//
// ffirun reads the receiver's base name and the interface's instantiation.
// Two disagreements remain: the module prefix on an impl-owned declaration
// (live in the shipped internal/stdlibbindings table) and ffirun's
// entry-scoped second key (which stdKey has no analogue for). See
// disagreement_test.go.
//
// This package is the shared derivation those should read. No consumer reads
// it yet. It is referenced only by its own tests: a consumer switch
// must be provable to change nothing, and it cannot be if it lands with the
// thing it is switching to.
//
// # What the derivation does not decide
//
// Registration. A pairing says "declaration D is implemented by Go symbol S";
// whether S is bound through a generated adapter table, spelled into a
// generated wrapper, or linked by one key is the consumer's business. That split
// is why Pairing exposes several key spellings rather than one: the spellings
// are all correct, and which one a consumer wants depends on how it looks up.
package hostpair

import (
	"fmt"
	goparser "go/parser"
	gotoken "go/token"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Kind separates a host FUNCTION binding from a host TYPE binding. They are
// one derivation because they come from one scan and share the key rule, and
// two kinds because a type's Go side is a named type (registered by prototype)
// and a function's is a func value.
type Kind uint8

const (
	// KindFunc is a `host fn name(...) go alias.Symbol` declaration.
	KindFunc Kind = iota + 1
	// KindType is a `host type Name go alias.Symbol` (or `opaque type Name go
	// alias.Symbol`) declaration.
	KindType
)

func (k Kind) String() string {
	switch k {
	case KindFunc:
		return "func"
	case KindType:
		return "type"
	}
	return "invalid"
}

// Pairing is one derived "this Nomi declaration ↔ this Go symbol".
//
// The declaration side is kept DECOMPOSED — module, receiver, interface
// instantiation, name — rather than pre-joined into a key, because the
// existing derivations join those four parts differently and a pre-joined
// string cannot serve them all. Joining is Keys / BuilderKey / BindingKey.
type Pairing struct {
	Kind Kind

	// Module is the module-name segment a non-entry file's declarations are
	// qualified with — "regex" for std/regex, "" for an entry file. This is
	// path.Base of the module path, matching ffirun.moduleNameForNomiFile.
	Module string
	// Receiver is the BASE name of the enclosing `impl` block's receiver type,
	// or "" for a file-top-level declaration. Base name, so `impl Box<T>`
	// gives "Box" — ffirun.implReceiverName's rule, which is what a binding is
	// actually looked up under.
	//
	// The receiver's type parameters are a BINDER, not an instantiation: a
	// module holds at most one `impl Box<T>`, so `<T>` cannot tell two
	// declarations apart, and carrying it produces a key no lookup forms.
	Receiver string
	// Interface is the interface instantiation an `impl Iface<Args> for T`
	// block names, and "" for an inherent block or a non-generic interface.
	// irbuild/stdlib.go's stdImplKey rule, which keys
	// `calendar.NaiveDateTime.Add<Duration, NaiveDateTime>.add` verbatim:
	// eleven `impl Add<X, NaiveDateTime>` blocks each declare `add`, so
	// dropping the instantiation collapses eleven declarations onto one key.
	//
	// This is the discriminating half, and it is why dropping the receiver's
	// binder is safe: what distinguishes two impls of one generic interface
	// for one receiver is the interface's type ARGUMENTS, which are concrete.
	// ffirun.implInterfaceKey reads the same instantiation.
	Interface string
	// Name is the declaration's own name, exactly as written — including a
	// trailing `?` or `!`.
	Name string

	// ImportPath is the Go package the symbol lives in, taken from the
	// `gopkg "path" as alias` handle the selector references. "" for an inline
	// `go { }` body, whose Go text has no package of its own.
	ImportPath string
	// Alias is the `gopkg` handle's alias as the facade wrote it (`go_regex`).
	// Carried because two consumers render a qualified selector and one of
	// them, the wrapper, re-aliases; the alias is the facade's spelling, not
	// the renderer's.
	Alias string
	// Symbol is the Go identifier inside ImportPath (`Compile`), or "" when
	// Inline.
	Symbol string
	// Inline marks a declaration whose implementation is an inline `go { }`
	// body rather than a package symbol. There is nothing to point at: the
	// body is compiled into whatever wrapper the consumer generates, so a
	// pairing cannot name a symbol and consumers must special-case it.
	Inline bool

	SourceFile string
	SourceLine int
	SourceCol  int
}

// GoSymbol is the fully qualified Go symbol, `<import path>.<Symbol>`. Empty
// for an inline body.
//
// This is the spelling runtime.FuncForPC reports for a linked func value and
// the spelling reflect.Type reports for a named type, which is what makes the
// symbol half of a pairing checkable against a real binary rather than only
// against another derivation.
func (p Pairing) GoSymbol() string {
	if p.Inline || p.ImportPath == "" || p.Symbol == "" {
		return ""
	}
	return p.ImportPath + "." + p.Symbol
}

// BindingKey is the spelling ffirun.externDeclKey produces: an impl-owned
// declaration keys on its RECEIVER BASE NAME plus the interface's
// instantiation, and drops the module entirely.
//
// It is what internal/stdlibbindings/bindings.go contains — `Regex.compile`,
// with no `regex.` on it — and it is a member of the candidate list (see
// Keys), the third of four for an impl-owned declaration in a module.
//
// The interface segment appears only for an INSTANTIATED generic interface,
// which is what keeps eleven `impl Add<X, NaiveDateTime>` declarations of
// `add` on eleven keys instead of one. The agreement checks in
// agree_project_test.go hold ffirun to the base name and the interface.
//
// The module is still dropped, and that is disagreement 1 — untouched,
// because it is live in the shipped bindings.go and moving it moves what
// every binding is keyed under.
func (p Pairing) BindingKey() string {
	if p.Receiver != "" {
		if p.Interface != "" {
			return p.Receiver + "." + p.Interface + "." + p.Name
		}
		return p.Receiver + "." + p.Name
	}
	if p.Module != "" {
		return p.Module + "." + p.Name
	}
	return p.Name
}

// BuilderKey is the spelling irbuild.stdKey produces: the module is ALWAYS
// present, and an instantiated generic interface contributes a segment.
//
// It is the FIRST candidate in Keys. The IR builder needs a single name
// because it links a call by key rather than walking a list, and this is the
// one it picked.
//
// irbuild.stdKey joins the module unconditionally because every stdlib
// declaration has one. An ENTRY file's does not, and joining "" would produce
// a leading dot, so the empty module is dropped — which is also what makes
// BuilderKey equal BindingKey for an entry-file top-level declaration, the
// one shape where the two derivations cannot disagree.
func (p Pairing) BuilderKey() string {
	parts := make([]string, 0, 4)
	if p.Module != "" {
		parts = append(parts, p.Module)
	}
	if p.Receiver != "" {
		parts = append(parts, p.Receiver)
		if p.Interface != "" {
			parts = append(parts, p.Interface)
		}
	}
	return strings.Join(append(parts, p.Name), ".")
}

// EntryKey is the extra spelling a declaration answers to when its OWN file is
// loaded as the entry, and "" when that is already BindingKey.
//
// ffirun.entryScopedKey's rule: only a module-qualified TOP-LEVEL declaration
// has a second key, because an impl-owned one keys on a receiver that does not
// change with which file is the entry.
//
// Deliberately NOT a member of Keys. It is not a fallback a lookup tries in
// order; it is the key that REPLACES the qualified one when this file is the
// entry, decided by the wrapper's nomiExternKey helper at run time from the
// path it was handed. Folding it into the candidate list would make a wrapper
// register two names for one declaration.
func (p Pairing) EntryKey() string {
	if p.Receiver != "" || p.Module == "" {
		return ""
	}
	return p.Name
}

// Keys is every registration spelling this declaration answers to, most
// specific first.
//
// For the impl-owned case: module-prefixed before bare, interface-qualified
// before not. A TOP-LEVEL
// declaration has exactly ONE spelling — externDeclKey's — because no impl
// receiver and no interface can vary it; its second name is EntryKey, which is
// a replacement rather than a fallback.
//
// This list is the reason BindingKey and BuilderKey are both correct rather
// than one being a bug: for an impl-owned declaration BuilderKey is its FIRST
// member and BindingKey is a later one (the third of four for a
// module-qualified instantiated-interface impl, the last of two otherwise).
// A consumer that looks a binding up may walk the list; a consumer that links
// by one key must choose one member, and that choice is the disagreement this
// package exists to end.
func (p Pairing) Keys() []string {
	if p.Receiver == "" {
		return []string{p.BindingKey()}
	}
	bare := make([]string, 0, 2)
	if p.Interface != "" {
		bare = append(bare, p.Receiver+"."+p.Interface+"."+p.Name)
	}
	bare = append(bare, p.Receiver+"."+p.Name)
	if p.Module == "" {
		return bare
	}
	keys := make([]string, 0, len(bare)*2)
	for _, k := range bare {
		keys = append(keys, p.Module+"."+k)
	}
	return append(keys, bare...)
}

// String is a stable one-line rendering used by the agreement checks' diff
// output. Not a key; do not parse it.
func (p Pairing) String() string {
	symbol := p.GoSymbol()
	if p.Inline {
		symbol = "<inline go body>"
	}
	if symbol == "" {
		symbol = "<no symbol>"
	}
	return fmt.Sprintf("%s %s -> %s", p.Kind, p.BuilderKey(), symbol)
}

// No `.nomi` file under `std/` names a Go symbol (the first-party adapters
// declare bare `host fn`), so there is nothing in the stdlib for this package
// to derive. Every caller is a USER project reached through DeriveSource or
// DeriveNodes.

// DeriveSource parses src and derives its pairings.
//
// A non-nil error means the source DID NOT PARSE. The pairings returned
// alongside it are what recovery parsing could still see, and they are a lower
// bound rather than a set — so a caller must not read "no error checked, no
// pairings" as "this file declares no bindings". ffirun makes exactly that
// elision (discovery.go:281 discards the parse error and derives from
// recovery), which is defensible there because discovery only decides whether
// the wrapper path is needed and the analyzer reports the syntax error
// afterwards. It is not defensible in a derivation whose output a consumer
// would emit a direct call from, so the error is returned and the recovery
// pairings are returned WITH it rather than instead of it.
//
// Empty source is not an error. A file with no declarations has no pairings,
// which is a fact rather than a failure.
func DeriveSource(file, moduleName string, src []byte) ([]Pairing, error) {
	nodes, err := parser.Parse(lexer.Lex(string(src)))
	if err != nil {
		recovered, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
		return DeriveNodes(file, moduleName, recovered), fmt.Errorf("hostpair: %s: %w", file, err)
	}
	return DeriveNodes(file, moduleName, nodes), nil
}

// DeriveNodes derives the pairings declared by an already-parsed file.
//
// Order is source order: top-level declarations as encountered, and an impl
// block's items where the block appears. Deterministic without sorting, which
// keeps a caller free to sort by whichever key it uses.
func DeriveNodes(file, moduleName string, nodes []ast.Node) []Pairing {
	aliases := collectGopkgAliases(nodes)
	var out []Pairing
	collect(file, moduleName, "", "", nodes, aliases, &out)
	return out
}

// collectGopkgAliases indexes every Go package handle a selector in this file
// may name, by alias.
//
// Three sources, which is ffirun.collectExternPackages plus
// ffirun.collectGoBlocks:
//
//   - a top-level `gopkg "path" as alias` handle;
//   - an `import "path" as alias` written with the extern marker, which
//     ffirun.externPackageFromImport also accepts;
//   - an `import` spec inside a top-level `go { }` prelude, whose alias is the
//     Go one — explicit, or the sanitized last path segment when absent.
//
// A selector naming an alias with no handle is NOT a pairing and is dropped,
// which is ffirun's behaviour (discovery.go:510 `if pkgDecl == nil`) rather
// than a new rule. Dropping is right: without a handle there is no import
// path, so there is no Go symbol to name and any pairing would be invented.
func collectGopkgAliases(nodes []ast.Node) map[string]*ast.ExternPackage {
	aliases := make(map[string]*ast.ExternPackage)
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ExternPackage:
			if v.Alias != "" {
				aliases[v.Alias] = v
			}
		case *ast.ImportStmt:
			if v.Extern && v.ExternAlias != "" && v.ExternAlias != "_" {
				aliases[v.ExternAlias] = &ast.ExternPackage{
					ImportPath: importStmtPath(v),
					Alias:      v.ExternAlias,
				}
			}
		case *ast.ImportBlock:
			for _, entry := range v.Entries {
				if entry.Extern && entry.ExternAlias != "" && entry.ExternAlias != "_" {
					aliases[entry.ExternAlias] = &ast.ExternPackage{
						ImportPath: importStmtPath(entry),
						Alias:      entry.ExternAlias,
					}
				}
			}
		case *ast.GoBlock:
			for _, decl := range goBlockImports(v) {
				aliases[decl.Alias] = decl
			}
		}
	}
	return aliases
}

// goBlockImports reads the `import` specs out of a `go { }` prelude.
//
// ffirun.parseGoBlockPrelude's import half, minus the non-import declarations
// it also lifts: those are Go text the wrapper emits, not pairings. An
// unparseable body yields nothing, matching ffirun — a malformed prelude is
// the analyzer's error to report, and discovery must not invent a pairing out
// of text it could not read.
func goBlockImports(block *ast.GoBlock) []*ast.ExternPackage {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "inline_go.nomi.go", "package main\n"+strings.TrimSpace(block.Body)+"\n", goparser.ImportsOnly)
	if err != nil {
		return nil
	}
	var out []*ast.ExternPackage
	for _, spec := range file.Imports {
		if spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		alias := ""
		if spec.Name != nil {
			alias = spec.Name.Name
		} else {
			alias = defaultGoImportAlias(importPath)
		}
		if alias == "" || alias == "_" {
			continue
		}
		out = append(out, &ast.ExternPackage{ImportPath: importPath, Alias: alias})
	}
	return out
}

func collect(file, moduleName, receiver, iface string, nodes []ast.Node, aliases map[string]*ast.ExternPackage, out *[]Pairing) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ImplBlock:
			recv := receiverBaseName(v.Receiver)
			if recv == "" {
				continue
			}
			collect(file, moduleName, recv, interfaceInstantiation(v.Interface), v.Items, aliases, out)
		case *ast.ExternType:
			// A host TYPE is never impl-owned: `host type` and
			// `opaque type ... go ...` are file-top-level forms. Carrying the
			// receiver through anyway costs nothing and keeps one rule.
			if p, ok := typePairing(file, moduleName, receiver, v, aliases); ok {
				*out = append(*out, p)
			}
		case *ast.ExternFunc:
			if p, ok := funcPairing(file, moduleName, receiver, iface, v, aliases); ok {
				*out = append(*out, p)
			}
		}
	}
}

func typePairing(file, moduleName, receiver string, v *ast.ExternType, aliases map[string]*ast.ExternPackage) (Pairing, bool) {
	p := Pairing{
		Kind:       KindType,
		Module:     moduleName,
		Receiver:   receiver,
		Name:       v.Name,
		SourceFile: file,
	}
	if v.GoBody != "" {
		alias, name, ok := parseInlineTypeSelector(v.GoBody)
		if !ok {
			return Pairing{}, false
		}
		p.Symbol = name
		p.SourceLine, p.SourceCol = v.GoBodyLine, v.GoBodyCol
		if alias == "" {
			// A local `go { type X ... }` body: the type exists only in the
			// generated wrapper, so there is no import path to name.
			p.Inline = true
			return p, true
		}
		decl := aliases[alias]
		if decl == nil {
			return Pairing{}, false
		}
		p.Alias, p.ImportPath = decl.Alias, decl.ImportPath
		return p, true
	}
	if v.ForeignAlias == "" || v.ForeignName == "" {
		return Pairing{}, false
	}
	decl := aliases[v.ForeignAlias]
	if decl == nil {
		return Pairing{}, false
	}
	p.Alias, p.ImportPath = decl.Alias, decl.ImportPath
	p.Symbol = v.ForeignName
	p.SourceLine, p.SourceCol = v.ForeignNameLine, v.ForeignNameCol
	return p, true
}

func funcPairing(file, moduleName, receiver, iface string, v *ast.ExternFunc, aliases map[string]*ast.ExternPackage) (Pairing, bool) {
	p := Pairing{
		Kind:       KindFunc,
		Module:     moduleName,
		Receiver:   receiver,
		Interface:  iface,
		Name:       v.Name,
		SourceFile: file,
	}
	if v.GoBody != "" {
		p.Inline = true
		p.SourceLine, p.SourceCol = v.GoBodyLine, v.GoBodyCol
		return p, true
	}
	if v.ForeignAlias == "" || v.ForeignName == "" {
		return Pairing{}, false
	}
	decl := aliases[v.ForeignAlias]
	if decl == nil {
		return Pairing{}, false
	}
	p.Alias, p.ImportPath = decl.Alias, decl.ImportPath
	p.Symbol = v.ForeignName
	p.SourceLine, p.SourceCol = v.ForeignNameLine, v.ForeignNameCol
	return p, true
}

// receiverBaseName is ffirun.implReceiverName: the base name of an impl
// block's receiver, so `impl Generator<T>` gives "Generator".
//
// The base name and not TypeString(), because the base name is what a binding
// is looked up under. agree_project_test.go's generic-receiver fixture holds
// ffirun to it.
func receiverBaseName(recv ast.TypeExpr) string {
	switch t := recv.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	case *ast.QualifiedType:
		return receiverBaseName(t.Member)
	}
	return ""
}

// interfaceInstantiation is irbuild.stdImplKey and ffirun.implInterfaceKey: the interface's full instantiation when it takes
// type arguments, and "" otherwise.
//
// "" for a non-generic interface is not a shortcut. A non-generic interface can
// be implemented for a receiver at most once, so the instantiation would add no
// information; a generic one can be implemented once per instantiation, which
// is where the collapse lived.
func interfaceInstantiation(iface ast.TypeExpr) string {
	switch t := iface.(type) {
	case *ast.GenericType:
		if len(t.Params) == 0 {
			return ""
		}
		return t.TypeString()
	case *ast.QualifiedType:
		if interfaceInstantiation(t.Member) == "" {
			return ""
		}
		return t.TypeString()
	}
	return ""
}

// parseInlineTypeSelector reads the Go type expression an `ExternType`'s
// inline body carries — `ffi.Box`, `*ffi.Box`, or a bare `Box` naming a type
// declared in the same inline Go text.
//
// The grammar is ffirun.parseInlineGoTypeSelector's, arm for arm: trim, drop a
// leading `*`, split on ".", accept one segment (bare) or two (alias-qualified)
// and nothing else. Written out rather than called, because ffirun must not
// become a dependency of the package meant to replace its derivation — but any
// difference here would be a difference in the DERIVED PAIRING, so it is the
// grammar that is shared, not the code, and the agreement check is what holds
// them together.
func parseInlineTypeSelector(body string) (alias, name string, ok bool) {
	expr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), "*"))
	parts := strings.Split(expr, ".")
	if len(parts) == 1 && parts[0] != "" {
		return "", parts[0], true
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ModuleNameForFile is the module-name segment a project file's declarations
// are qualified with: "" for the entry `main.nomi` and for any `_test.nomi`,
// and the file's base name otherwise.
//
// ffirun.moduleNameForNomiFile's rule, and it is part of the DERIVATION rather
// than of the file walk: the segment it returns is a component of every key
// the file's declarations answer to. Which files to walk is deliberately NOT
// here — that is a project-layout question (nested nomi.toml scopes, local
// replace targets) with no bearing on what a given file's pairings are.
func ModuleNameForFile(projectRoot, file string) string {
	base := filepath.Base(file)
	if base == "main.nomi" || strings.HasSuffix(base, "_test.nomi") {
		return ""
	}
	rel, err := filepath.Rel(projectRoot, file)
	if err != nil {
		return strings.TrimSuffix(base, ".nomi")
	}
	withoutExt := strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
	return path.Base(withoutExt)
}

// importStmtPath is the Go import path an extern-marked `import` statement
// names. ffirun.externPackageFromImport reads the same field.
func importStmtPath(n *ast.ImportStmt) string {
	if n == nil {
		return ""
	}
	return n.ExternPath
}

// defaultGoImportAlias is the alias a `go { }` prelude import answers to when
// it declares none: the sanitized last path segment, which is
// ffirun.defaultGoImportAlias.
//
// Not Go's real rule — that is the imported package's own `package` clause,
// which discovery cannot see without compiling. The last segment is the
// approximation both sides make, so both make the same mistake on a package
// whose name differs from its directory, and the agreement check cannot
// distinguish them. Recorded rather than fixed: changing it here would break
// agreement with the consumer that ships.
func defaultGoImportAlias(importPath string) string {
	return sanitizeIdent(lastPathSegment(strings.TrimSuffix(importPath, "/")))
}

func lastPathSegment(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

// sanitizeIdent maps any rune illegal in a Go identifier to "_", and prefixes
// "_" when the result would start with a digit. ffirun.sanitizeIdent's rule.
func sanitizeIdent(s string) string {
	if s == "" {
		return ""
	}
	out := make([]rune, 0, len(s))
	for i, r := range s {
		ok := r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			r = '_'
		}
		out = append(out, r)
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = append([]rune{'_'}, out...)
	}
	return string(out)
}
