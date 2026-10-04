package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
	"github.com/nomi-language/nomi/vmhost"

	"github.com/chzyer/readline"
)

// THE VM REPL, which is `nomi` with no arguments.
//
// A session keeps ONE live VM machine (vmhost.Session). Each input is checked
// and lowered as a new program and linked into that machine, the way the
// Scala REPL, GHCi and the OCaml toplevel compile each input as a new unit
// that sees the earlier ones. Earlier inputs are never run again.
//
// # Lexical scope across inputs
//
// Each input's definitions are new entities, as GHCi's `Ghci1.x` and
// `Ghci2.x` are. A function, a session value and a `once` is a replEntity,
// and a definition refers to the entities that were in scope when it was
// entered, never to a name:
//
//	x = 5
//	fn add(a: Int): Int { a + x }
//	x = 100
//	add(1)        // 6: add reads the x it was written against
//
// and after `fn f` is redefined, an earlier `fn g` keeps calling the old f
// while later inputs call the new one. The mechanism is versioned names. A
// declaration is kept as a replTemplate: its source text and, for every
// identifier the checker resolved to one of the entry's top-level functions
// or onces (vmhost.Checked.TopLevelRefs), which entity it names. When a
// later program re-emits the declaration, each such identifier is spelled
// with the entity's name in that program: the user's own name while the
// entity is the one in scope, and `nomi_repl_<id>_<name>` once a newer
// definition has taken the name. An entity is emitted while anything in
// scope refers to it, directly or through other entities, and dropped from
// the session when nothing does. Error text has the versioned spelling
// rewritten back to the user's name (replSource.explainText); function
// values inspect as `<function>`, so no program output shows one.
//
// # A program
//
// An input's program is built from source:
//
//   - every other declaration earlier inputs made (struct, enum, type,
//     typealias, interface, impl, derive, import), the latest of each key;
//   - every live entity: a function's declaration; for a session value,
//     `host fn nomi_repl_get_<id>(): T` and `once <name>: T =
//     nomi_repl_get_<id>()`, which the session answers from its store; for a
//     `once`, `host fn nomi_repl_once_<id>(init: () -> T): T` and `once
//     <name>: T = nomi_repl_once_<id>(|| { <initializer> })`. The session
//     calls init on the first force in the whole session and answers the
//     stored value after that, so an initializer runs at most once, on first
//     use, whichever input forces it;
//   - the input's own declarations and onces, then `fn main` holding its
//     statements. A final expression is printed with `io.inspect` unless it
//     is Unit, and each name the input binds at top level (a binding or a
//     destructure) is stored with `nomi_repl_put_<id>(name)` as main's last
//     step.
//
// T is the checker's type for the value, spelled back as source: the input
// is checked once to learn it (and to resolve its references) and then
// checked and lowered with the reads and writes declared. A value whose type
// cannot be spelled (an unsolved type variable, a type from a module the
// session imported whole) or whose write the builder cannot lower is not kept,
// and the REPL says so.
//
// An input that fails to check, faults or is BLOCKED prints its error and
// changes nothing. Its effects before a fault have happened.
//
// Limits:
//
//   - Types, impls and imports are not versioned. Redeclaring a type with a
//     different declaration drops every value whose type names it, every
//     type, impl, function and once whose declaration names it, and every
//     definition that refers to one of those, with a note. GHCi instead
//     keeps them, typed by the old type (`Ghci1.T`); here the old type's
//     name is taken by the new one, so its values are unreachable anyway. An
//     impl's methods are reached through their type, so a redefined `impl`
//     replaces the old one for everything, earlier definitions included.
//   - A function declared in the same input as a binding sees the binding's
//     earlier value, since the binding is a local of that input's main.
//   - `fn main` cannot be declared, and a `fn boot` runs again on every
//     input.
//   - Each input re-checks and re-lowers every live declaration, so an input
//     costs more as the session grows.

const replVMBanner = "REPL on the VM: bindings, functions and types carry between inputs."

func runReplVM() {
	homeDir, _ := os.UserHomeDir()
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          ">> ",
		HistoryFile:     filepath.Join(homeDir, ".nomi_history"),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer rl.Close()
	fmt.Println(currentVersionLine())
	fmt.Println(replVMBanner)
	s := newReplSession(os.Stdout, os.Stderr)
	replLoop(func(continuation bool) (string, error) {
		if continuation {
			rl.SetPrompt(".. ")
		} else {
			rl.SetPrompt(">> ")
		}
		return rl.Readline()
	}, s, os.Stdout, os.Stderr)
	fmt.Println()
}

// replLoop reads inputs until read fails, gathering continuation lines until
// the lexer calls an input complete, and evaluates each in s.
func replLoop(read func(continuation bool) (string, error), s *replSession, out, errOut io.Writer) {
	for {
		line, err := read(false)
		if err != nil { // io.EOF or interrupt
			return
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		input := line
		for !lexer.IsComplete(input) {
			continuation, err := read(true)
			if err != nil {
				return
			}
			input += "\n" + continuation
		}
		s.eval(input, out, errOut)
	}
}

// replScript runs a session over the lines of in: what `nomi` does with a
// piped stdin, without the terminal.
func replScript(in io.Reader, out, errOut io.Writer) {
	sc := bufio.NewScanner(in)
	s := newReplSession(out, errOut)
	replLoop(func(bool) (string, error) {
		if !sc.Scan() {
			return "", io.EOF
		}
		return sc.Text(), nil
	}, s, out, errOut)
}

// replSession is what carries between inputs.
type replSession struct {
	vm *vmhost.Session
	// decls are the declarations earlier inputs made that are not entities,
	// in the order they were made; a redeclaration replaces its predecessor.
	decls []*replDecl
	// ents are the live entities, in the order they were made.
	ents []*replEntity
	// visible is the entity each name means to the next input.
	visible map[string]*replEntity
	nextEnt int
}

// replEntityKind is what an entity is.
type replEntityKind int

const (
	replFn    replEntityKind = iota // a `fn` or `host fn` declaration
	replValue                       // a value a binding stored
	replLazy                        // a `once`, forced through the session
)

// replEntity is one definition an input made: a function, a stored value or
// a once. An entity never changes; a redefinition is a new entity.
type replEntity struct {
	id   int
	name string
	kind replEntityKind
	// tmpl is a function's declaration or a once's initializer.
	tmpl replTemplate
	// vid is a value's or a once's session store id, and typ its type as
	// source.
	vid int
	typ string
	// types are the type names its declaration or type mentions.
	types map[string]bool
}

// replTemplate is declaration text whose references to entities are spelled
// per program.
type replTemplate struct {
	text string
	refs []replRef
}

// replRef is one identifier in a template's text naming an entity: its line
// in the text and byte column (both from 1), the width of its spelling in
// the text, and the entity.
type replRef struct {
	line, col, width int
	target           *replEntity
}

// render is t's text with each reference spelled name(target).
func (t replTemplate) render(name func(*replEntity) string) string {
	if len(t.refs) == 0 {
		return t.text
	}
	lines := strings.Split(t.text, "\n")
	byLine := map[int][]replRef{}
	for _, r := range t.refs {
		byLine[r.line] = append(byLine[r.line], r)
	}
	for ln, rs := range byLine {
		sort.Slice(rs, func(i, j int) bool { return rs[i].col > rs[j].col })
		l := lines[ln-1]
		for _, r := range rs {
			at := r.col - 1
			l = l[:at] + name(r.target) + l[at+r.width:]
		}
		lines[ln-1] = l
	}
	return strings.Join(lines, "\n")
}

// replVersioned is the name entity e has in a program where a newer
// definition holds its own name. replVersionedName undoes it in error text.
func replVersioned(e *replEntity) string { return fmt.Sprintf("nomi_repl_%d_%s", e.id, e.name) }

var replVersionedName = regexp.MustCompile(`\bnomi_repl_\d+_`)

type replDecl struct {
	// key names what the declaration declares; two with one key cannot both
	// be in a program.
	key  string
	tmpl replTemplate
	// flat is the text with its whitespace collapsed: a type redeclared with
	// the same flat text is not a new type.
	flat string
	// fnName is the function a `fn` or `host fn` declares, and typeName the
	// type a type declaration declares.
	fnName   string
	typeName string
	// types are the type names the declaration mentions.
	types map[string]bool
	// isImport places the declaration with the imports, which a program
	// must hold before anything else.
	isImport bool
	// line is the declaration's first line in the current input, or 0 for
	// one an earlier input made.
	line int
	// ent is the entity a function declaration of the current input
	// becomes, and header the position of its name in its text.
	ent                   *replEntity
	headerLine, headerCol int
}

// replText is a piece of the current input and its first line there.
type replText struct {
	text string
	line int
}

// replOnce is one `once` of the current input.
type replOnce struct {
	name string
	text string
	line int
	// rhsLine and rhsCol are where the initializer starts in text.
	rhsLine, rhsCol int
	refs            []replRef
	ent             *replEntity
	// typ is the once's type as source, empty when it cannot be spelled.
	typ string
}

func newReplSession(out, errOut io.Writer) *replSession {
	return &replSession{vm: vmhost.NewSession(out, errOut), visible: map[string]*replEntity{}}
}

func (s *replSession) newEntity(name string, kind replEntityKind) *replEntity {
	e := &replEntity{id: s.nextEnt, name: name, kind: kind}
	s.nextEnt++
	return e
}

// The names the REPL's generated code uses. The prefix keeps them apart from
// an input's own names.
const (
	replValueImport = "nomi_repl_io"
	replValueName   = "nomi_repl_value"
	replProbePrefix = "nomi_repl_probe_"
)

// replInput is one input, split for placing in a program.
type replInput struct {
	decls      []*replDecl
	onces      []*replOnce
	stmts      []replText
	lastIsExpr bool
	// bound is every name the input's statements bind at top level, in
	// first-bound order: a binding or a destructure's names.
	bound []string
	// typeNames are the types the input declares.
	typeNames map[string]bool
	// fnNames are the functions and onces the input declares.
	fnNames map[string]bool
}

func parseReplInput(input string) (*replInput, error) {
	nodes, err := parser.Parse(lexer.Lex(input))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(input, "\n")
	in := &replInput{typeNames: map[string]bool{}, fnNames: map[string]bool{}}
	seen := map[string]bool{}
	bind := func(name string) {
		if name == "" || name == "_" || seen[name] {
			return
		}
		seen[name] = true
		in.bound = append(in.bound, name)
	}
	for i, n := range nodes {
		start := n.LineNum()
		if i == 0 {
			start = 1
		}
		end := len(lines) + 1
		if i+1 < len(nodes) {
			end = nodes[i+1].LineNum()
		}
		if start < 1 || start > end || end > len(lines)+1 {
			return nil, fmt.Errorf("repl: cannot place input line %d", start)
		}
		text := strings.Join(lines[start-1:end-1], "\n")
		if o, isOnce := n.(*ast.OnceBinding); isOnce {
			rhsLine, rhsCol, ok := replOnceRHS(text)
			if !ok {
				return nil, fmt.Errorf("repl: cannot find the initializer of once %s", o.Name)
			}
			in.onces = append(in.onces, &replOnce{name: o.Name, text: text, line: start, rhsLine: rhsLine, rhsCol: rhsCol})
			in.fnNames[o.Name] = true
			continue
		}
		if d, isDecl := replDeclOf(n, text); isDecl {
			d.line = start
			in.decls = append(in.decls, d)
			if d.fnName != "" {
				in.fnNames[d.fnName] = true
				line, col, ok := replHeader(text, d.fnName)
				if !ok {
					return nil, fmt.Errorf("repl: cannot find the name of fn %s", d.fnName)
				}
				d.headerLine, d.headerCol = line, col
			}
			if d.typeName != "" {
				in.typeNames[d.typeName] = true
			}
			continue
		}
		in.stmts = append(in.stmts, replText{text, start})
		_, in.lastIsExpr = n.(*ast.ExprStmt)
		for _, name := range replBoundNames(n) {
			bind(name)
		}
	}
	return in, nil
}

// replDeclOf is n as a declaration, keyed by what it declares.
func replDeclOf(n ast.Node, text string) (*replDecl, bool) {
	flat := strings.Join(strings.Fields(text), " ")
	d := &replDecl{tmpl: replTemplate{text: text}, flat: flat, types: replTypeWords(text)}
	switch t := n.(type) {
	case *ast.FuncDef:
		d.key, d.fnName = "fn "+t.Name, t.Name
	case *ast.ExternFunc:
		d.key, d.fnName = "fn "+t.Name, t.Name
	case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias, *ast.InterfaceDef, *ast.ExternType:
		name, _ := replTypeName(n)
		d.key, d.typeName = "type "+name, name
	case *ast.ImplBlock, *ast.ImplConformance:
		// An impl's header is what it implements for what.
		header := flat
		if i := strings.Index(header, "{"); i >= 0 {
			header = strings.TrimSpace(header[:i])
		}
		d.key = "impl " + header
	case *ast.ImportStmt, *ast.ImportBlock:
		d.key, d.isImport = "import "+flat, true
	case *ast.GoBlock, *ast.ExternPackage, *ast.TestDecl, *ast.Decorator:
		d.key = "text " + flat
	default:
		return nil, false
	}
	return d, true
}

// replTypeWords is the type-position words of text: every capitalized
// identifier outside strings and comments.
func replTypeWords(text string) map[string]bool {
	words := map[string]bool{}
	for _, tok := range lexer.Lex(text) {
		if tok.Type == token.TYPE_IDENT {
			words[tok.Lexeme] = true
		}
	}
	return words
}

// replHeader is the position of name after the `fn` of a function
// declaration's text.
func replHeader(text, name string) (int, int, bool) {
	afterFn := false
	for _, tok := range lexer.Lex(text) {
		switch {
		case tok.Type == token.FN:
			afterFn = true
		case afterFn && tok.Type == token.IDENT && tok.Lexeme == name:
			return tok.Line, tok.Col, true
		case afterFn:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// replOnceRHS is where the initializer of a `once` declaration's text
// starts: the first byte after its `=` that is not a space.
func replOnceRHS(text string) (int, int, bool) {
	afterOnce := false
	lines := strings.Split(text, "\n")
	for _, tok := range lexer.Lex(text) {
		switch {
		case tok.Type == token.ONCE:
			afterOnce = true
		case afterOnce && tok.Type == token.EQ:
			line, col := tok.Line, tok.Col+1
			for col <= len(lines[line-1]) && lines[line-1][col-1] == ' ' {
				col++
			}
			return line, col, true
		}
	}
	return 0, 0, false
}

func replTypeName(n ast.Node) (string, bool) {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Name, true
	case *ast.EnumDef:
		return d.Name, true
	case *ast.TypeDef:
		return d.Name, true
	case *ast.TypeAlias:
		return d.Name, true
	case *ast.InterfaceDef:
		return d.Name, true
	case *ast.ExternType:
		return d.Name, true
	}
	return "", false
}

// replBoundNames is the names a top-level statement binds.
func replBoundNames(n ast.Node) []string {
	var names []string
	switch s := n.(type) {
	case *ast.Binding:
		names = append(names, s.Name)
	case *ast.TupleDestructure:
		for _, id := range s.Bindings {
			if id != nil {
				names = append(names, id.Name)
			}
		}
	case *ast.StructDestructure:
		for _, f := range s.Fields {
			names = append(names, replStructFieldNames(f)...)
		}
	case *ast.MapDestructure:
		for _, e := range s.Entries {
			names = append(names, replPatternNames(e.Pattern)...)
		}
	case *ast.DistinctDestructure:
		if s.Binding != nil {
			names = append(names, s.Binding.Name)
		}
	case *ast.PatternDestructure:
		names = append(names, replPatternNames(s.Pattern)...)
	case *ast.PatternBinding:
		names = append(names, replPatternNames(s.Pattern)...)
	}
	return names
}

func replStructFieldNames(f ast.StructPatternField) []string {
	if f.Pattern != nil {
		return replPatternNames(f.Pattern)
	}
	return []string{f.Binding}
}

func replPatternNames(p ast.Node) []string {
	var names []string
	switch q := p.(type) {
	case *ast.IdentPattern:
		names = append(names, q.Name)
	case *ast.EnumPattern:
		if q.Binding != "" {
			names = append(names, q.Binding)
		}
		if q.Payload != nil {
			names = append(names, replPatternNames(q.Payload)...)
		}
	case *ast.StructPattern:
		for _, f := range q.Fields {
			names = append(names, replStructFieldNames(f)...)
		}
	case *ast.TuplePattern:
		for _, e := range q.Patterns {
			names = append(names, replPatternNames(e)...)
		}
	case *ast.ListPattern:
		for _, e := range q.Heads {
			names = append(names, replPatternNames(e)...)
		}
		if q.TailSpread != nil {
			names = append(names, replPatternNames(q.TailSpread)...)
		}
	case *ast.MapPattern:
		for _, e := range q.Entries {
			names = append(names, replPatternNames(e.Pattern)...)
		}
	}
	return names
}

// replStore is one value the input's program stores: the input's top-level
// name and its type.
type replStore struct {
	name string
	id   int
	typ  string
}

// replPlan is what one program for an input holds beyond the input itself.
type replPlan struct {
	decls []*replDecl   // earlier declarations still in force
	ents  []*replEntity // earlier entities still live
	// names is each entity's name in the program.
	names map[*replEntity]string
	// show prints the final expression; unit runs it as a statement.
	show bool
	// probe binds each bound name and once to a probe local, to learn its
	// type, and emits the input's onces as written; otherwise stores writes
	// the kept values to the session and each once whose type is known is
	// forced through the session.
	probe  bool
	stores []replStore
}

func (p *replPlan) name(e *replEntity) string {
	if n, ok := p.names[e]; ok {
		return n
	}
	return replVersioned(e)
}

// replSource is a program's text and, for each of its lines, where the line
// came from: its line in the current input, replEarlier for an earlier
// input's declaration, or replGenerated; and what declared it.
type replSource struct {
	b      strings.Builder
	origin []int
	owner  []replOwner
}

// replOwner is the declaration a program line belongs to: an earlier
// entity's, or the current input's decls[decl-1] or onces[once-1]. start is
// the program line the declaration's text begins on.
type replOwner struct {
	ent   *replEntity
	decl  int
	once  int
	start int
}

const (
	replEarlier   = 0
	replGenerated = -1
)

// add appends text, whose first line came from line (or replEarlier or
// replGenerated), owned by owner.
func (s *replSource) add(text string, line int, owner replOwner) {
	owner.start = len(s.origin) + 1
	for i, l := range strings.Split(text, "\n") {
		s.b.WriteString(l)
		s.b.WriteByte('\n')
		switch {
		case line > 0:
			s.origin = append(s.origin, line+i)
		default:
			s.origin = append(s.origin, line)
		}
		s.owner = append(s.owner, owner)
	}
}

func (s *replSource) gen(text string) { s.add(text, replGenerated, replOwner{}) }

// explain rewrites the program's line numbers in an error to the input's.
// The session's program has no file, so a diagnostic in it is spelled by
// line and column alone.
func (s *replSource) explain(err error) string {
	var ds vmhost.Diagnostics
	if !errors.As(err, &ds) {
		return s.explainText(err.Error())
	}
	lines := make([]string, len(ds))
	for i, d := range ds {
		switch {
		case filepath.IsAbs(d.Path):
			lines[i] = d.String()
		default:
			lines[i] = fmt.Sprintf("line %d, col %d: %s", d.Line, d.Col, d.Message)
			if !strings.Contains(d.Message, replProbePrefix) {
				for _, h := range d.Hints {
					lines[i] += "\n  help: " + h
				}
			}
		}
	}
	return s.explainText(strings.Join(lines, "\n"))
}

func (s *replSource) explainText(text string) string {
	// A diagnostic about a probe repeats one about the input's own binding.
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if !strings.Contains(l, replProbePrefix) {
			kept = append(kept, l)
		}
	}
	text = replVersionedName.ReplaceAllString(strings.Join(kept, "\n"), "")
	return replLineRef.ReplaceAllStringFunc(text, func(ref string) string {
		m := replLineRef.FindStringSubmatch(ref)
		n, _ := strconv.Atoi(m[2])
		if n < 1 || n > len(s.origin) {
			return ref
		}
		switch o := s.origin[n-1]; {
		case o > 0:
			return m[1] + strconv.Itoa(o)
		case o == replEarlier:
			return m[1] + m[2] + " (an earlier input's declaration)"
		}
		return ref
	})
}

// replLineRef is a line of the session's program in a report: `line N` in a
// diagnostic or a fault, or `repl.nomi:N` in a `todo` trap, which names its
// file.
var replLineRef = regexp.MustCompile(`(line |repl\.nomi:)(\d+)`)

// program is the source of in's program under plan.
func (in *replInput) program(plan *replPlan) *replSource {
	src := &replSource{}
	// Imports first: a program holds them before anything else.
	if plan.show && in.lastIsExpr {
		src.gen(fmt.Sprintf("import std/io as %s", replValueImport))
	}
	for _, d := range plan.decls {
		if d.isImport {
			src.add(d.tmpl.render(plan.name), replEarlier, replOwner{})
		}
	}
	for i, d := range in.decls {
		if d.isImport {
			src.add(d.tmpl.text, d.line, replOwner{decl: i + 1})
		}
	}
	for _, d := range plan.decls {
		if !d.isImport {
			src.add(d.tmpl.render(plan.name), replEarlier, replOwner{})
		}
	}
	for _, e := range plan.ents {
		name := plan.name(e)
		switch e.kind {
		case replFn:
			src.add(e.tmpl.render(plan.name), replEarlier, replOwner{ent: e})
		case replValue:
			get := vmhost.SessionGetName(e.vid)
			src.gen(fmt.Sprintf("host fn %s(): %s", get, e.typ))
			src.add(fmt.Sprintf("once %s: %s = %s()", name, e.typ, get), replGenerated, replOwner{ent: e})
		case replLazy:
			force := vmhost.SessionOnceName(e.vid)
			src.gen(fmt.Sprintf("host fn %s(init: () -> %s): %s", force, e.typ, e.typ))
			src.add(fmt.Sprintf("once %s: %s = %s(|| {", name, e.typ, force), replGenerated, replOwner{ent: e})
			src.add(e.tmpl.render(plan.name), replEarlier, replOwner{ent: e})
			src.gen("})")
		}
	}
	for _, st := range plan.stores {
		src.gen(fmt.Sprintf("host fn %s(value: %s): Unit", vmhost.SessionPutName(st.id), st.typ))
	}
	for i, d := range in.decls {
		if !d.isImport {
			src.add(d.tmpl.text, d.line, replOwner{decl: i + 1})
		}
	}
	for i, o := range in.onces {
		if plan.probe || o.typ == "" {
			src.add(o.text, o.line, replOwner{once: i + 1})
			continue
		}
		force := vmhost.SessionOnceName(o.ent.vid)
		src.gen(fmt.Sprintf("host fn %s(init: () -> %s): %s", force, o.typ, o.typ))
		src.add(fmt.Sprintf("once %s: %s = %s(|| {", o.name, o.typ, force), replGenerated, replOwner{once: i + 1})
		src.add(o.rhs().text, o.line+o.rhsLine-1, replOwner{once: i + 1})
		src.gen("})")
	}
	src.gen("fn main() {")
	for i, s := range in.stmts {
		if i == len(in.stmts)-1 && in.lastIsExpr {
			switch {
			case plan.probe:
				src.add(fmt.Sprintf("%s = %s", replValueName, s.text), s.line, replOwner{})
				src.gen(fmt.Sprintf("_ = %s", replValueName))
			case plan.show:
				src.add(fmt.Sprintf("%s = %s", replValueName, s.text), s.line, replOwner{})
				src.gen(fmt.Sprintf("%s.inspect(%s)", replValueImport, replValueName))
			default:
				src.add(s.text, s.line, replOwner{})
			}
			continue
		}
		src.add(s.text, s.line, replOwner{})
	}
	if plan.probe {
		for i, name := range in.bound {
			src.gen(fmt.Sprintf("%s%d = %s", replProbePrefix, i, name))
			src.gen(fmt.Sprintf("_ = %s%d", replProbePrefix, i))
		}
		for i, o := range in.onces {
			src.gen(fmt.Sprintf("%sonce_%d = %s", replProbePrefix, i, o.name))
			src.gen(fmt.Sprintf("_ = %sonce_%d", replProbePrefix, i))
		}
	}
	stored := map[string]bool{}
	for _, st := range plan.stores {
		stored[st.name] = true
		src.gen(fmt.Sprintf("%s(%s)", vmhost.SessionPutName(st.id), st.name))
	}
	if !plan.probe {
		// A name the input binds and nothing reads is a program error; a
		// REPL binding is usually read only by later inputs.
		for _, name := range in.bound {
			if !stored[name] {
				src.gen(fmt.Sprintf("_ = %s", name))
			}
		}
	}
	src.gen("Unit\n}")
	return src
}

// rhs is o's initializer as a template: its text from after the `=`, with
// the references inside it.
func (o *replOnce) rhs() replTemplate {
	lines := strings.Split(o.text, "\n")[o.rhsLine-1:]
	lines[0] = lines[0][o.rhsCol-1:]
	t := replTemplate{text: strings.Join(lines, "\n")}
	for _, r := range o.refs {
		switch {
		case r.line < o.rhsLine || r.line == o.rhsLine && r.col < o.rhsCol:
			continue
		case r.line == o.rhsLine:
			r.col -= o.rhsCol - 1
		}
		r.line -= o.rhsLine - 1
		t.refs = append(t.refs, r)
	}
	return t
}

// prepare is what of the session is in force for in: the earlier
// declarations and live entities its program holds and their names there,
// and a note for each definition in scope that in's type redeclarations
// drop.
func (s *replSession) prepare(in *replInput) (*replPlan, []string) {
	// A type the input declares with a different declaration is a new type.
	// So is every earlier type whose declaration names one.
	byKey := map[string]*replDecl{}
	for _, d := range s.decls {
		byKey[d.key] = d
	}
	changed := map[string]bool{}
	for _, d := range in.decls {
		if old := byKey[d.key]; d.typeName != "" && old != nil && old.flat != d.flat {
			changed[d.typeName] = true
		}
	}
	mentions := func(types map[string]bool) bool {
		for t := range types {
			if changed[t] {
				return true
			}
		}
		return false
	}
	for grew := len(changed) > 0; grew; {
		grew = false
		for _, d := range s.decls {
			if d.typeName != "" && !changed[d.typeName] && mentions(d.types) {
				changed[d.typeName], grew = true, true
			}
		}
	}

	// Dropped: every entity whose declaration or type names a changed type,
	// and everything that refers to a dropped entity.
	dropped := map[*replEntity]bool{}
	for _, e := range s.ents {
		if mentions(e.types) || e.kind != replFn && replMentions(e.typ, changed) {
			dropped[e] = true
		}
	}
	refersToDropped := func(refs []replRef) bool {
		for _, r := range refs {
			if dropped[r.target] {
				return true
			}
		}
		return false
	}
	for grew := len(dropped) > 0; grew; {
		grew = false
		for _, e := range s.ents {
			if !dropped[e] && refersToDropped(e.tmpl.refs) {
				dropped[e], grew = true, true
			}
		}
	}

	var notes []string
	redeclared := map[string]bool{}
	for _, d := range in.decls {
		redeclared[d.key] = true
	}
	plan := &replPlan{names: map[*replEntity]string{}}
	for _, d := range s.decls {
		switch {
		case redeclared[d.key]:
		case mentions(d.types) || refersToDropped(d.tmpl.refs):
			notes = append(notes, fmt.Sprintf("note: `%s` is no longer in scope: it uses a redeclared type", d.key))
		default:
			plan.decls = append(plan.decls, d)
		}
	}

	// The entities in scope for the input: every visible one whose name the
	// input does not declare. They keep their names; every other live
	// entity is versioned.
	visible := map[*replEntity]bool{}
	for name, e := range s.visible {
		switch {
		case in.fnNames[name]:
		case dropped[e]:
			if !replContains(in.bound, name) {
				if e.kind == replValue {
					notes = append(notes, fmt.Sprintf("note: %s is no longer in scope: its type %s was redeclared", name, e.typ))
				} else {
					notes = append(notes, fmt.Sprintf("note: %s is no longer in scope: it uses a redeclared type", name))
				}
			}
		default:
			visible[e] = true
		}
	}
	// Live: what is in scope and what a live entity or a kept declaration
	// refers to.
	live := map[*replEntity]bool{}
	var mark func(e *replEntity)
	mark = func(e *replEntity) {
		if live[e] || dropped[e] {
			return
		}
		live[e] = true
		for _, r := range e.tmpl.refs {
			mark(r.target)
		}
	}
	for e := range visible {
		mark(e)
	}
	for _, d := range plan.decls {
		for _, r := range d.tmpl.refs {
			mark(r.target)
		}
	}
	for _, e := range s.ents {
		if !live[e] {
			continue
		}
		plan.ents = append(plan.ents, e)
		if visible[e] {
			plan.names[e] = e.name
		} else {
			plan.names[e] = replVersioned(e)
		}
	}
	sort.Strings(notes)
	return plan, notes
}

func replContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// resolve records in the current input's declarations and onces which
// entity each of their top-level references names, from the checked probe
// program src.
func (in *replInput) resolve(checked *vmhost.Checked, src *replSource, plan *replPlan) {
	for _, r := range checked.TopLevelRefs() {
		if r.DefLine < 1 || r.DefLine > len(src.owner) || r.Line < 1 || r.Line > len(src.owner) {
			continue
		}
		var target *replEntity
		switch def := src.owner[r.DefLine-1]; {
		case def.ent != nil && plan.names[def.ent] == r.Name:
			target = def.ent
		case def.decl > 0 && in.decls[def.decl-1].fnName == r.Name:
			target = in.decls[def.decl-1].ent
		case def.once > 0 && in.onces[def.once-1].name == r.Name:
			target = in.onces[def.once-1].ent
		}
		if target == nil {
			continue
		}
		use := src.owner[r.Line-1]
		ref := replRef{line: r.Line - use.start + 1, col: r.Col, width: len(r.Name), target: target}
		switch {
		case use.decl > 0:
			d := in.decls[use.decl-1]
			d.tmpl.refs = append(d.tmpl.refs, ref)
		case use.once > 0:
			o := in.onces[use.once-1]
			o.refs = append(o.refs, ref)
		}
	}
}

// eval runs one input. Every failure is printed to errOut, and a failed
// input leaves the session as it was.
func (s *replSession) eval(input string, out, errOut io.Writer) {
	defer func() {
		// A panic here is a bug in the REPL or the toolchain, never the
		// input's own failure (those arrive as errors), so it is labelled as
		// one rather than printed as if the program had raised it.
		if r := recover(); r != nil {
			fmt.Fprintf(errOut, "repl: internal error, not a fault in the input: %v\n", r)
		}
	}()
	in, err := parseReplInput(input)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return
	}
	if in.fnNames["main"] {
		fmt.Fprintln(errOut, "repl: an input cannot declare `fn main`; the REPL runs each input as main")
		return
	}
	for _, d := range in.decls {
		if d.fnName != "" {
			d.ent = s.newEntity(d.fnName, replFn)
		}
	}
	for _, o := range in.onces {
		o.ent = s.newEntity(o.name, replLazy)
	}
	plan, dropNotes := s.prepare(in)

	// Check once with probes to learn each bound name's and once's type and
	// whether the final expression is Unit, and to resolve the input's
	// references.
	plan.probe = true
	src := in.program(plan)
	checked, err := s.vm.Check(src.b.String())
	if err != nil {
		fmt.Fprintln(errOut, src.explain(err))
		return
	}
	in.resolve(checked, src, plan)
	plan.probe = false
	plan.show = in.lastIsExpr && !checked.IsUnit(replValueName)
	// notKept is each name the session cannot keep, and why.
	var notKept [][2]string
	const unwritable, uncarried = "its type cannot be written in source", "the VM cannot carry a value of its type between inputs"
	for i, name := range in.bound {
		typ, ok := checked.LocalType(fmt.Sprintf("%s%d", replProbePrefix, i))
		if !ok {
			notKept = append(notKept, [2]string{name, unwritable})
			continue
		}
		plan.stores = append(plan.stores, replStore{name: name, id: s.vm.NewID(), typ: typ})
	}
	for i, o := range in.onces {
		if typ, ok := checked.LocalType(fmt.Sprintf("%sonce_%d", replProbePrefix, i)); ok {
			o.typ = typ
			o.ent.vid = s.vm.NewID()
		} else {
			notKept = append(notKept, [2]string{o.name, unwritable})
		}
	}

	src = in.program(plan)
	p, err := s.vm.Load(src.b.String())
	if err != nil && (len(plan.stores) > 0 || len(in.onces) > 0) {
		// The probe check passed, so a spelled type is what failed. Keep the
		// stores and onces whose type checks on its own.
		stores, onces := plan.stores, map[*replOnce]string{}
		for _, o := range in.onces {
			onces[o] = o.typ
			o.typ = ""
		}
		var kept []replStore
		for _, st := range stores {
			plan.stores = []replStore{st}
			if _, err := s.vm.Check(in.program(plan).b.String()); err == nil {
				kept = append(kept, st)
			} else {
				notKept = append(notKept, [2]string{st.name, unwritable})
			}
		}
		plan.stores = nil
		for _, o := range in.onces {
			if onces[o] == "" {
				continue
			}
			o.typ = onces[o]
			if _, err := s.vm.Check(in.program(plan).b.String()); err != nil {
				o.typ = ""
				notKept = append(notKept, [2]string{o.name, unwritable})
			}
		}
		plan.stores = kept
		src = in.program(plan)
		p, err = s.vm.Load(src.b.String())
	}
	if err == nil {
		// A store the builder could not lower (its value's type does not
		// cross into Go) would block the whole input; the value is not kept
		// instead.
		var kept []replStore
		for _, st := range plan.stores {
			if p.Retains(vmhost.SessionPutName(st.id)) {
				kept = append(kept, st)
			} else {
				notKept = append(notKept, [2]string{st.name, uncarried})
			}
		}
		if len(kept) < len(plan.stores) {
			plan.stores = kept
			src = in.program(plan)
			p, err = s.vm.Load(src.b.String())
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, src.explain(err))
		return
	}
	if err := s.vm.Run(context.Background(), p); err != nil {
		var report bytes.Buffer
		if blocked, ok := vmhost.IsBlocked(err); ok {
			blocked.Write(&report, "<repl>")
		} else {
			vmhost.WriteFailure(&report, err)
		}
		io.WriteString(errOut, src.explainText(report.String()))
		return
	}
	for _, n := range s.commit(in, plan) {
		notKept = append(notKept, n)
	}
	for _, n := range dropNotes {
		fmt.Fprintln(errOut, n)
	}
	for _, n := range notKept {
		fmt.Fprintf(errOut, "note: %s is not kept for later inputs: %s\n", n[0], n[1])
	}
}

// commit makes the input's definitions the session's after it ran, and
// answers each definition that cannot be kept because it refers to one that
// is not.
func (s *replSession) commit(in *replInput, plan *replPlan) [][2]string {
	var notKept [][2]string
	// made is every entity this input made that is kept.
	made := map[*replEntity]bool{}
	var fns []*replEntity
	var decls []*replDecl
	for _, d := range in.decls {
		d.line = replEarlier
		if d.ent == nil {
			decls = append(decls, d)
			continue
		}
		e := d.ent
		e.tmpl = d.tmpl
		e.tmpl.refs = append(e.tmpl.refs, replRef{line: d.headerLine, col: d.headerCol, width: len(e.name), target: e})
		e.types = d.types
		fns = append(fns, e)
		made[e] = true
	}
	var onces []*replEntity
	for _, o := range in.onces {
		if o.typ == "" {
			continue
		}
		e := o.ent
		e.typ, e.tmpl = o.typ, o.rhs()
		e.types = replTypeWords(o.text)
		for t := range replTypeWords(e.typ) {
			e.types[t] = true
		}
		onces = append(onces, e)
		made[e] = true
	}
	// A definition that refers to one of this input's that is not kept (a
	// once of an unwritable type) is not kept either.
	earlier := map[*replEntity]bool{}
	for _, e := range plan.ents {
		earlier[e] = true
	}
	resolved := func(refs []replRef) (string, bool) {
		for _, r := range refs {
			if !made[r.target] && !earlier[r.target] {
				return r.target.name, false
			}
		}
		return "", true
	}
	for grew := true; grew; {
		grew = false
		for e := range made {
			if missing, ok := resolved(e.tmpl.refs); !ok {
				delete(made, e)
				notKept = append(notKept, [2]string{e.name, "it uses " + missing + ", which is not kept"})
				grew = true
			}
		}
	}

	s.decls = plan.decls
	for _, d := range decls {
		if missing, ok := resolved(d.tmpl.refs); ok {
			s.decls = append(s.decls, d)
		} else {
			notKept = append(notKept, [2]string{"`" + d.key + "`", "it uses " + missing + ", which is not kept"})
		}
	}
	s.ents = plan.ents
	visible := map[string]*replEntity{}
	for e := range plan.names {
		if plan.names[e] == e.name {
			visible[e.name] = e
		}
	}
	for _, e := range append(fns, onces...) {
		if made[e] {
			s.ents = append(s.ents, e)
			visible[e.name] = e
		} else if visible[e.name] != nil && visible[e.name] == e {
			delete(visible, e.name)
		}
	}
	// A value shadows a function or once of its name for later inputs, and
	// a bound name that is not kept is unbound.
	kept := map[string]bool{}
	for _, st := range plan.stores {
		if !s.vm.Stored(st.id) {
			continue
		}
		e := s.newEntity(st.name, replValue)
		e.vid, e.typ = st.id, st.typ
		s.ents = append(s.ents, e)
		visible[st.name] = e
		kept[st.name] = true
	}
	for _, name := range in.bound {
		if !kept[name] {
			delete(visible, name)
		}
	}
	s.visible = visible
	s.collect()
	sort.Slice(notKept, func(i, j int) bool { return notKept[i][0] < notKept[j][0] })
	return notKept
}

// collect drops every entity nothing in scope reaches.
func (s *replSession) collect() {
	live := map[*replEntity]bool{}
	var mark func(e *replEntity)
	mark = func(e *replEntity) {
		if live[e] {
			return
		}
		live[e] = true
		for _, r := range e.tmpl.refs {
			mark(r.target)
		}
	}
	for _, e := range s.visible {
		mark(e)
	}
	for _, d := range s.decls {
		for _, r := range d.tmpl.refs {
			mark(r.target)
		}
	}
	ents := s.ents[:0:0]
	for _, e := range s.ents {
		if live[e] {
			ents = append(ents, e)
		}
	}
	s.ents = ents
}

// replMentions reports whether the type text typ names any of names.
func replMentions(typ string, names map[string]bool) bool {
	if len(names) == 0 {
		return false
	}
	for _, word := range strings.FieldsFunc(typ, func(r rune) bool {
		return !(r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) {
		if i := strings.LastIndexByte(word, '.'); i >= 0 {
			word = word[i+1:]
		}
		if names[word] {
			return true
		}
	}
	return false
}
