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
	"reflect"
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

// The VM REPL, which is `nomi` with no arguments.
//
// A session keeps one live VM machine (vmhost.Session). Each input is a file
// of the session's program, `repl_in<N>`, the way the Scala REPL, GHCi and
// the OCaml toplevel compile each input as a new unit that sees the earlier
// ones. The file is checked against the interfaces of the earlier inputs'
// files, lowered on its own against what they lowered, and linked into the
// machine (vmhost.Session.LoadFile). Earlier inputs are never checked,
// lowered or run again, so an input costs what its own text and the files
// it reads cost, however long the session.
//
// # Lexical scope across inputs
//
// Each input's definitions are new entities, as GHCi's `Ghci1.x` and
// `Ghci2.x` are. A function, a session value and a `once` is a replEntity,
// and a definition refers to the entities that were in scope when it was
// entered, never to a name:
//
//	x = 5
//	fn add(a: Int) -> Int { a + x }
//	x = 100
//	add(1)        // 6: add reads the x it was written against
//
// and after `fn f` is redefined, an earlier `fn g` keeps calling the old f
// while later inputs call the new one. The mechanism is files: an input's
// file imports each earlier definition its text names from the file that
// made it, the one in scope when the input was entered. A redefinition is
// declared in its own input's file, and an earlier file keeps importing
// what it imported. An entity is kept while anything in scope refers to
// it, directly or through other entities, and dropped from the session
// when nothing does.
//
// # A file
//
// An input's file holds:
//
//   - the session's imports (every `import` earlier inputs made, the
//     latest of each key), the input's own, and `import repl_in<K>.{...}`
//     naming each function, once, type, interface or embed of earlier
//     input K that the input's text names and that is in scope. A type's
//     earlier inputs with an inherent impl of it are imported whole, since
//     its members are found through them;
//   - for each session value the input names, `host fn
//     nomi_repl_get_<id>() -> T` and `once <name>: T =
//     nomi_repl_get_<id>()`, which the session answers from its store;
//   - the input's own declarations, each made `pub` (a generated `pub` line
//     above it) so a later input can import it, and its onces, as `pub
//     once <name>: T = nomi_repl_once_<id>(fn -> { <initializer> })`. The
//     session calls init on the first force in the whole session and
//     answers the stored value after that, so an initializer runs at most
//     once, on first use, whichever input forces it;
//   - `fn main` holding its statements. A final expression is printed with
//     `io.inspect` unless it is Unit, and each name the input binds at top
//     level (a binding or a destructure) is stored with
//     `nomi_repl_put_<id>(name)` as main's last step.
//
// T is the checker's type for the value, spelled back as source: the input
// is checked once with probes to learn it (and to resolve its references)
// and then checked and lowered with the reads and writes declared. A value
// whose type cannot be spelled (an unsolved type variable, a type from a
// module the session imported whole) or whose write the builder cannot
// lower is not kept, and the REPL says so.
//
// An input that fails to check, faults or is BLOCKED prints its error and
// changes nothing: its file leaves the session's program. Its effects
// before a fault have happened.
//
// Limits:
//
//   - Types are versioned, as GHCi versions them. A type redeclared with a
//     different declaration is a new type in the input's file; earlier
//     values, functions, onces, types and impls that name the old one keep
//     it, and later inputs that say the name get the new one. A value's or
//     once's type is spelled through the file that declares each session
//     type in it (`repl_in1.Point`, replSessionModule), so the spelling
//     keeps naming the old type, and a file that reads it imports that file
//     whole. A diagnostic that names two versions qualifies each
//     (analysis's same-name rule), which the REPL writes as `Point (input
//     1)`. Redeclaring a type with the same declaration keeps the earlier
//     type, and the input declares nothing.
//   - Impls are not versioned. A redefined `impl` replaces the old one for
//     everything, earlier definitions included: it is taken out of its
//     earlier input's file, so no later check sees it, the machine
//     dispatches the type's interface calls to the newest impl, and each of
//     the old impl's functions is redirected to the new one's
//     (vmhost.Session.Supersede), so code an earlier input compiled calls
//     it too. An impl is for the version of its type in force when it was
//     written: it stays with that version, and replaces only an impl of
//     the same version (replDecl.typeFile).
//   - An input may extend an earlier input's type: an inherent impl or a
//     `derive` for it is the input's (frontend.Checker.SessionSource). A
//     derive and a written impl of one interface for one type share a key
//     (replIfaceImplKey), so either replaces the other, and an impl for an
//     earlier input's type replaces the Debug the front end synthesized in
//     the type's file for code earlier inputs compiled too.
//   - A "did you mean" hint offers every name in scope, not only the ones
//     the input's file imports (vmhost.Session.SetScopeNames).
//   - A function declared in the same input as a binding sees the binding's
//     earlier value, since the binding is a local of that input's main.
//   - `fn main` cannot be declared. A `fn boot` runs again on every input
//     after it: each later input's file imports its input's file whole, its
//     `main` is checked as running under that boot, and the machine keeps
//     the boot it ran (vm.Machine.Extend).

const replVMBanner = "REPL on the VM: bindings, functions and types carry between inputs."

func runReplVM() {
	if !readline.IsTerminal(int(os.Stdin.Fd())) {
		// A piped stdin is read as a script, without the line editor:
		// on Windows the editor reads only a console, and nowhere does a
		// pipe need its prompts.
		fmt.Println(currentVersionLine())
		fmt.Println(replVMBanner)
		replScript(os.Stdin, os.Stdout, os.Stderr)
		return
	}
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
// stdin that is not a terminal.
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
	// byDef is the entity a file declares under a name, by the file's path.
	byDef    map[replDef]*replEntity
	nextEnt  int
	nextFile int
}

// replDef is a declaration by its file's path and its name.
type replDef struct{ path, name string }

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
	// file is the key of the file that declares a function or a once.
	file string
	// vid is a value's or a once's session store id, and typ its type as
	// source.
	vid int
	typ string
	// refs are the entities its declaration refers to.
	refs []*replEntity
}

type replDecl struct {
	// key names what the declaration declares; two with one key cannot both
	// be in force.
	key  string
	text string
	// flat is the text with its whitespace collapsed: a type redeclared with
	// the same flat text is not a new type.
	flat string
	// fnName is the function a `fn` or `host fn` declares, and typeName the
	// type a type declaration declares. name is what a later input imports
	// the declaration by: a type's name or an embed's.
	fnName   string
	typeName string
	name     string
	// inherent is the type an inherent impl extends, and recvType the type
	// an impl, inherent or of an interface, written or derived, is for when
	// its receiver is a plain type name. typeFile is the file that declares
	// the version of recvType the impl is for, "" for a type no input
	// declares.
	inherent string
	recvType string
	typeFile string
	// refs are the entities the declaration refers to.
	refs []*replEntity
	// isImport places the declaration with the imports, which a file must
	// hold before anything else; every later input's file holds it again.
	isImport bool
	// line is the declaration's first line in the current input.
	line int
	// file is the key of the file that declares it and fileLine its first
	// line there, once its input ran.
	file     string
	fileLine int
	// pub says the file declares it with a `pub` line above it.
	pub bool
	// same marks a type redeclared with the declaration in force: the
	// input does not declare it again.
	same bool
	// ent is the entity a function declaration of the current input
	// becomes.
	ent *replEntity
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
	refs            []*replEntity
	ent             *replEntity
	// typ is the once's type as source, empty when it cannot be spelled.
	typ string
}

func newReplSession(out, errOut io.Writer) *replSession {
	return &replSession{vm: vmhost.NewSession(out, errOut), visible: map[string]*replEntity{}, byDef: map[replDef]*replEntity{}}
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

// replFileKey is the module key of the session's n-th file, and
// replFilePath its path.
func replFileKey(n int) string       { return fmt.Sprintf("repl_in%d", n) }
func replFilePath(key string) string { return key + ".nomi" }

// replImportPath is how a file imports the file with key.
func replImportPath(key string) string { return key }

// replSessionKey matches a session file's module key, and replQualified a
// type spelled through one (`repl_in3.Point`).
var (
	replSessionKey = regexp.MustCompile(`^repl_in[0-9]+$`)
	replQualified  = regexp.MustCompile(`\brepl_in([0-9]+)\.([A-Z][A-Za-z0-9_]*)`)
)

// replSessionModule reports whether module is a session file's: a value's
// type names each type an input declared through that input's file, so the
// spelling keeps meaning that type after a later input declares another of
// its name.
func replSessionModule(module string) bool { return replSessionKey.MatchString(module) }

// replTypeUses are the files a type spelling names a type through, and the
// type names it writes bare.
func replTypeUses(typ string) (files []string, bare map[string]bool) {
	for _, m := range replQualified.FindAllStringSubmatch(typ, -1) {
		files = append(files, "repl_in"+m[1])
	}
	return files, replTypeWords(replQualified.ReplaceAllString(typ, ""))
}

// replInput is one input, split for placing in a file.
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
	// words are the names the input's text holds: every identifier, and
	// every string a node carries, which over-approximates the names it
	// uses.
	words map[string]bool
}

func parseReplInput(input string) (*replInput, error) {
	nodes, err := parser.Parse(lexer.Lex(input))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(input, "\n")
	in := &replInput{typeNames: map[string]bool{}, fnNames: map[string]bool{}, words: replWords(input, nodes)}
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

// replWords is every identifier of text and every identifier-shaped string
// its nodes carry: the names an input may use, interpolations included.
func replWords(text string, nodes []ast.Node) map[string]bool {
	words := map[string]bool{}
	add := func(s string) {
		if replIdentifier(s) {
			words[s] = true
		}
	}
	for _, tok := range lexer.Lex(text) {
		if tok.Type == token.IDENT || tok.Type == token.TYPE_IDENT {
			words[tok.Lexeme] = true
		}
	}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.String:
			add(v.String())
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if f := v.Field(i); f.CanInterface() && f.Kind() == reflect.String {
					add(f.String())
				} else if f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String {
					walk(f)
				}
			}
		}
	}
	for _, n := range nodes {
		ast.Inspect(n, func(m ast.Node) bool {
			v := reflect.ValueOf(m)
			for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
				if v.IsNil() {
					return true
				}
				v = v.Elem()
			}
			walk(v)
			return true
		})
	}
	return words
}

// replIdentifier reports whether s is shaped as a name, possibly qualified
// (`Color.Red`, whose head is the name).
func replIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// replDeclOf is n as a declaration, keyed by what it declares.
func replDeclOf(n ast.Node, text string) (*replDecl, bool) {
	flat := strings.Join(strings.Fields(text), " ")
	d := &replDecl{text: text, flat: flat, pub: replNeedsPub(n)}
	switch t := n.(type) {
	case *ast.FuncDef:
		d.key, d.fnName = "fn "+t.Name, t.Name
	case *ast.ExternFunc:
		d.key, d.fnName = "fn "+t.Name, t.Name
	case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias, *ast.InterfaceDef, *ast.ExternType:
		name, _ := replTypeName(n)
		d.key, d.typeName, d.name = "type "+name, name, name
	case *ast.ImplBlock:
		d.key = "impl " + replImplHeader(flat)
		if t.Interface != nil {
			d.key, d.recvType = replIfaceImplKey(t.Interface, t.Receiver)
		} else if st, ok := t.Receiver.(*ast.SimpleType); ok {
			d.inherent, d.recvType = st.Name, st.Name
		}
	case *ast.ImplConformance:
		d.key, d.recvType = replIfaceImplKey(t.Interface, t.Receiver)
	case *ast.ImportStmt, *ast.ImportBlock:
		d.key, d.isImport = "import "+flat, true
	case *ast.ExternPackage, *ast.TestDecl, *ast.Decorator:
		d.key = "text " + flat
	case *ast.EmbedDecl:
		// The files it names are read from the working directory when its
		// input is checked.
		d.key, d.name = "embed "+t.Name, t.Name
	default:
		return nil, false
	}
	return d, true
}

// replIfaceImplKey is the key of an impl of iface for recv, written or
// derived: `impl impl Display for P`, the declaration name its functions'
// linkage names carry (analysis.DeclarationNames) after `impl `. A derive
// and a written impl of one interface for one type share it, so a later
// one replaces an earlier one. recvType is the receiver's name when it is a
// plain type name.
func replIfaceImplKey(iface, recv ast.TypeExpr) (key, recvType string) {
	if st, ok := recv.(*ast.SimpleType); ok {
		recvType = st.Name
	}
	return "impl impl " + iface.TypeString() + " for " + recv.TypeString(), recvType
}

// replImplHeader is an impl's header: what it implements for what.
func replImplHeader(flat string) string {
	if i := strings.Index(flat, "{"); i >= 0 {
		return strings.TrimSpace(flat[:i])
	}
	return flat
}

// replNeedsPub reports whether n is a declaration a later file imports
// that is not written `pub`.
func replNeedsPub(n ast.Node) bool {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return false
	}
	switch n.(type) {
	case *ast.ImplBlock, *ast.ImplConformance:
		return false
	}
	f := v.Elem().FieldByName("Public")
	return f.IsValid() && f.Kind() == reflect.Bool && !f.Bool()
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
	case *ast.AsPattern:
		names = append(replPatternNames(q.Pattern), q.Name)
	case *ast.OrPattern:
		// Every alternative binds the same names.
		if len(q.Alternatives) > 0 {
			names = replPatternNames(q.Alternatives[0])
		}
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
		for _, e := range q.Tails {
			names = append(names, replPatternNames(e)...)
		}
	case *ast.MapPattern:
		for _, e := range q.Entries {
			names = append(names, replPatternNames(e.Pattern)...)
		}
	}
	return names
}

// replStore is one value the input's file stores: the input's top-level
// name and its type.
type replStore struct {
	name string
	id   int
	typ  string
}

// replPlan is what one file for an input holds beyond the input itself.
type replPlan struct {
	key string
	// decls are the earlier declarations still in force, and ents the
	// earlier entities still live.
	decls []*replDecl
	ents  []*replEntity
	// visible is the earlier entity each name means to the input.
	visible map[string]*replEntity
	// replaced are the earlier impls the input redeclares, which leave
	// their files.
	replaced []*replDecl
	// show prints the final expression; unit runs it as a statement.
	show bool
	// probe binds each bound name and once to a probe local, to learn its
	// type, and emits the input's onces as written; otherwise stores writes
	// the kept values to the session and each once whose type is known is
	// forced through the session.
	probe  bool
	stores []replStore
	// values are the session values the file reads, by name.
	values map[string]*replEntity
	// bootFile is the earlier input whose `fn boot` the input runs under,
	// which its file imports whole, "" for none.
	bootFile string
}

// spell is typ, a type a value or once has, as the plan's file writes it:
// a type the file itself declares needs no qualifier.
func (plan *replPlan) spell(typ string) string {
	return strings.ReplaceAll(typ, plan.key+".", "")
}

// scopeNames are the names the input has in scope, though its file imports
// only the ones its text names: every earlier function, value, once, type
// and embed in force, which a "did you mean" hint offers.
func (plan *replPlan) scopeNames() []string {
	var names []string
	for name := range plan.visible {
		names = append(names, name)
	}
	for _, d := range plan.decls {
		if d.name != "" {
			names = append(names, d.name)
		}
	}
	sort.Strings(names)
	return names
}

// replSource is a file's text and, for each of its lines, where the line
// came from: its line in the current input, replEarlier for an earlier
// input's declaration, or replGenerated; and what declared it.
type replSource struct {
	key    string
	b      strings.Builder
	origin []int
	owner  []replOwner
}

// replOwner is the declaration a file line belongs to: the current input's
// decls[decl-1] or onces[once-1]. start is the file line the declaration's
// text begins on.
type replOwner struct {
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

// explain rewrites the file's line numbers in an error to the input's.
func (s *replSource) explain(err error) string {
	var ds vmhost.Diagnostics
	if !errors.As(err, &ds) {
		return s.explainText(err.Error())
	}
	lines := make([]string, len(ds))
	for i, d := range ds {
		switch {
		case d.Path != "" && d.Path != replFilePath(s.key):
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
	// Two types of one name from different inputs: each by its input.
	text = replQualified.ReplaceAllString(text, "$2 (input $1)")
	// A position in an earlier input's file is that file's line.
	text = replFileRef.ReplaceAllStringFunc(text, func(ref string) string {
		m := replFileRef.FindStringSubmatch(ref)
		if m[1] == replFilePath(s.key) {
			return "repl.nomi:" + m[2]
		}
		return "repl.nomi:" + m[2] + " (an earlier input's declaration)"
	})
	return replLineRef.ReplaceAllStringFunc(text, func(ref string) string {
		m := replLineRef.FindStringSubmatch(ref)
		if m[3] != "" {
			return ref
		}
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

// replLineRef is a line of the input's file in a report: `line N` in a
// diagnostic or a fault, or `repl.nomi:N` in a `todo` trap, which names its
// file (replFileRef has rewritten the file's path), and not one an earlier
// input's file holds.
var replLineRef = regexp.MustCompile(`(line |repl\.nomi:)(\d+)( \(an earlier input's declaration\))?`)

// replFileRef is a position in one of the session's files, as a fault
// names it.
var replFileRef = regexp.MustCompile(`(repl_in\d+\.nomi):(\d+)`)

// replVersionedName is a definition's name behind a session prefix
// (`nomi_repl_3_f`); error text shows the user's name.
var replVersionedName = regexp.MustCompile(`\bnomi_repl_\d+_`)

// file is the source of in's file under plan.
func (s *replSession) file(in *replInput, plan *replPlan) *replSource {
	src := &replSource{key: plan.key}
	// Imports first: a file holds them before anything else.
	if plan.show && in.lastIsExpr {
		src.gen(fmt.Sprintf("import std::io as %s", replValueImport))
	}
	own := map[string]bool{}
	for _, d := range in.decls {
		own[d.key] = true
	}
	for _, d := range plan.decls {
		if d.isImport && !own[d.key] {
			src.add(d.text, replEarlier, replOwner{})
		}
	}
	for i, d := range in.decls {
		if d.isImport {
			src.add(d.text, d.line, replOwner{decl: i + 1})
		}
	}
	named, whole, values := s.imports(in, plan)
	files := make([]string, 0, len(named))
	for f := range named {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		src.gen(fmt.Sprintf("import %s.{%s}", replImportPath(f), strings.Join(named[f], ", ")))
	}
	for _, f := range whole {
		src.gen(fmt.Sprintf("import %s", replImportPath(f)))
	}
	plan.values = map[string]*replEntity{}
	for _, e := range values {
		plan.values[e.name] = e
		get := vmhost.SessionGetName(e.vid)
		src.gen(fmt.Sprintf("host fn %s() -> %s", get, plan.spell(e.typ)))
		src.gen(fmt.Sprintf("once %s: %s = %s()", e.name, plan.spell(e.typ), get))
	}
	for _, st := range plan.stores {
		src.gen(fmt.Sprintf("host fn %s(value: %s) -> Unit", vmhost.SessionPutName(st.id), plan.spell(st.typ)))
	}
	for i, d := range in.decls {
		if d.isImport || d.same {
			continue
		}
		if d.pub {
			src.gen("pub")
		}
		src.add(d.text, d.line, replOwner{decl: i + 1})
	}
	for i, o := range in.onces {
		if plan.probe || o.typ == "" {
			src.gen("pub")
			src.add(o.text, o.line, replOwner{once: i + 1})
			continue
		}
		force := vmhost.SessionOnceName(o.ent.vid)
		typ := plan.spell(o.typ)
		src.gen(fmt.Sprintf("host fn %s(init: () -> %s) -> %s", force, typ, typ))
		src.gen("pub")
		src.add(fmt.Sprintf("once %s: %s = %s(fn -> {", o.name, typ, force), replGenerated, replOwner{once: i + 1})
		src.add(o.rhs(), o.line+o.rhsLine-1, replOwner{once: i + 1})
		src.gen("})")
	}
	src.gen("fn main() {")
	for i, st := range in.stmts {
		if i == len(in.stmts)-1 && in.lastIsExpr {
			switch {
			case plan.probe:
				src.add(fmt.Sprintf("%s = %s", replValueName, st.text), st.line, replOwner{})
				src.gen(fmt.Sprintf("_ = %s", replValueName))
			case plan.show:
				src.add(fmt.Sprintf("%s = %s", replValueName, st.text), st.line, replOwner{})
				src.gen(fmt.Sprintf("%s.inspect(%s)", replValueImport, replValueName))
			default:
				src.add(st.text, st.line, replOwner{})
			}
			continue
		}
		src.add(st.text, st.line, replOwner{})
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

// imports is what in's file imports under plan: the names it imports from
// each earlier file, the earlier files it imports whole, and the session
// values it reads. A name is the input's when the input declares it;
// otherwise it is the entity in scope, or the type, interface or embed in
// force, that the name means. The types of the values the file reads and
// writes are names it uses too.
func (s *replSession) imports(in *replInput, plan *replPlan) (map[string][]string, []string, []*replEntity) {
	words := map[string]bool{}
	for w := range in.words {
		words[w] = true
	}
	wholeSet := map[string]bool{}
	addTypes := func(typ string) {
		files, bare := replTypeUses(typ)
		for _, f := range files {
			if f != plan.key {
				wholeSet[f] = true
			}
		}
		for w := range bare {
			words[w] = true
		}
	}
	for _, st := range plan.stores {
		addTypes(st.typ)
	}
	for _, o := range in.onces {
		addTypes(o.typ)
	}
	for w := range in.words {
		if e := plan.visible[w]; e != nil && e.kind == replValue && !in.fnNames[w] {
			addTypes(e.typ)
		}
	}
	declares := func(name string) bool {
		if in.fnNames[name] {
			return true
		}
		for _, d := range in.decls {
			if d.name == name && !d.same {
				return true
			}
		}
		return false
	}
	byName := map[string]*replDecl{}
	for _, d := range plan.decls {
		if d.name != "" && d.file != "" {
			byName[d.name] = d
		}
	}
	// inherent are the files with an inherent impl of each type in force.
	inherent := map[string][]string{}
	for _, d := range plan.decls {
		if t := byName[d.inherent]; d.inherent != "" && d.file != "" && t != nil && t.file == d.typeFile {
			inherent[d.inherent] = append(inherent[d.inherent], d.file)
		}
	}
	named := map[string][]string{}
	var values []*replEntity
	sorted := make([]string, 0, len(words))
	for w := range words {
		sorted = append(sorted, w)
	}
	sort.Strings(sorted)
	for _, w := range sorted {
		if declares(w) {
			continue
		}
		if e := plan.visible[w]; e != nil {
			switch e.kind {
			case replValue:
				values = append(values, e)
			default:
				named[e.file] = append(named[e.file], w)
			}
			continue
		}
		if d := byName[w]; d != nil {
			named[d.file] = append(named[d.file], w)
			for _, f := range inherent[w] {
				wholeSet[f] = true
			}
		}
	}
	if plan.bootFile != "" {
		wholeSet[plan.bootFile] = true
	}
	whole := make([]string, 0, len(wholeSet))
	for f := range wholeSet {
		whole = append(whole, f)
	}
	sort.Strings(whole)
	return named, whole, values
}

// rhs is o's initializer: its text from after the `=`.
func (o *replOnce) rhs() string {
	lines := strings.Split(o.text, "\n")[o.rhsLine-1:]
	lines[0] = lines[0][o.rhsCol-1:]
	return strings.Join(lines, "\n")
}

// prepare is what of the session is in force for in, whose file has key:
// the earlier declarations and live entities, the entity each name means,
// and the impls in redeclares.
//
// A type the input declares with a declaration other than the one in force
// is a new type, in the input's file; earlier values, functions, onces,
// types and impls that name the type in force keep it, since each names it
// through the file that declares it. A type redeclared with the
// declaration in force is that type: the input does not declare it again.
func (s *replSession) prepare(in *replInput, key string) *replPlan {
	byKey := map[string]*replDecl{}
	for _, d := range s.decls {
		byKey[d.key] = d
	}
	for _, d := range in.decls {
		if old := byKey[d.key]; d.typeName != "" && old != nil && old.flat == d.flat {
			d.same = true
		}
	}
	// typeFile is the file that declares the version of a type the input
	// means by its name.
	typeFile := func(name string) string {
		for _, d := range in.decls {
			if d.typeName == name && !d.same {
				return key
			}
		}
		if d := byKey["type "+name]; d != nil {
			return d.file
		}
		return ""
	}
	for _, d := range in.decls {
		if d.recvType != "" {
			d.typeFile = typeFile(d.recvType)
		}
	}
	// An impl replaces an earlier one of its key for the same version of
	// its type; one for an earlier version stays with that version.
	redeclared := map[string]bool{}
	for _, d := range in.decls {
		if !d.same {
			redeclared[d.key+"\x00"+d.typeFile] = true
		}
	}
	plan := &replPlan{key: key, visible: map[string]*replEntity{}}
	for _, d := range s.decls {
		switch {
		case redeclared[d.key+"\x00"+d.typeFile] && d.typeName == "":
			if strings.HasPrefix(d.key, "impl ") && d.file != "" {
				plan.replaced = append(plan.replaced, d)
			}
		case d.typeName != "" && redeclared[d.key+"\x00"]:
			// A type the input redeclares: its file keeps it for what
			// names it there, and the name means the input's.
		default:
			plan.decls = append(plan.decls, d)
		}
	}

	// The entities in scope for the input: every visible one whose name the
	// input does not declare.
	for name, e := range s.visible {
		if !in.fnNames[name] {
			plan.visible[name] = e
		}
	}
	// Live: what is in scope and what a live entity or a kept declaration
	// refers to.
	live := map[*replEntity]bool{}
	var mark func(e *replEntity)
	mark = func(e *replEntity) {
		if live[e] {
			return
		}
		live[e] = true
		for _, r := range e.refs {
			mark(r)
		}
	}
	for _, e := range plan.visible {
		mark(e)
	}
	for _, d := range plan.decls {
		for _, r := range d.refs {
			mark(r)
		}
	}
	for _, e := range s.ents {
		if live[e] {
			plan.ents = append(plan.ents, e)
		}
	}
	return plan
}

func replContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// resolve records for in's declarations and onces which entities each
// refers to, from the checked probe file src.
func (s *replSession) resolve(in *replInput, checked *vmhost.Checked, src *replSource, plan *replPlan) {
	path := replFilePath(plan.key)
	own := map[string]*replEntity{}
	for _, d := range in.decls {
		if d.ent != nil {
			own[d.fnName] = d.ent
		}
	}
	for _, o := range in.onces {
		own[o.name] = o.ent
	}
	type use struct {
		owner  replOwner
		target *replEntity
	}
	seen := map[use]bool{}
	for _, r := range checked.Refs() {
		if r.Line < 1 || r.Line > len(src.owner) {
			continue
		}
		var target *replEntity
		switch {
		case r.Path == path && own[r.Name] != nil:
			target = own[r.Name]
		case r.Path == path:
			target = plan.values[r.Name]
		default:
			target = s.byDef[replDef{r.Path, r.Name}]
		}
		owner := src.owner[r.Line-1]
		if target == nil || seen[use{owner, target}] {
			continue
		}
		seen[use{owner, target}] = true
		switch {
		case owner.decl > 0:
			d := in.decls[owner.decl-1]
			d.refs = append(d.refs, target)
		case owner.once > 0:
			o := in.onces[owner.once-1]
			o.refs = append(o.refs, target)
		}
	}
}

// replace takes each impl plan replaces out of its earlier input's file,
// for the checks of the input and the inputs after it, and answers what
// puts them back.
func (s *replSession) replace(plan *replPlan) func() {
	saved := map[string]string{}
	for _, d := range plan.replaced {
		text, ok := s.vm.FileText(d.file)
		if !ok {
			continue
		}
		if _, held := saved[d.file]; !held {
			saved[d.file] = text
		}
		lines := strings.Split(text, "\n")
		for i := d.fileLine - 1; i < d.fileLine-1+strings.Count(d.text, "\n")+1 && i < len(lines); i++ {
			if i >= 0 {
				lines[i] = ""
			}
		}
		s.vm.ReplaceFile(d.file, strings.Join(lines, "\n"))
	}
	return func() {
		for file, text := range saved {
			s.vm.ReplaceFile(file, text)
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
	// Every input is numbered, so `input N` in a diagnostic counts the
	// inputs as the reader typed them.
	s.nextFile++
	key := replFileKey(s.nextFile)
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
	plan := s.prepare(in, key)
	// An input that declares no boot runs under the boot in scope, as
	// the machine runs it.
	if e := plan.visible["boot"]; e != nil && e.kind == replFn && !in.fnNames["boot"] {
		plan.bootFile = e.file
	}
	s.vm.SetBoot(plan.bootFile)
	s.vm.SetScopeNames(plan.scopeNames())
	restore := s.replace(plan)
	ran := false
	defer func() {
		if !ran {
			s.vm.Discard(key)
			restore()
		}
	}()

	// Check once with probes to learn each bound name's and once's type and
	// whether the final expression is Unit, and to resolve the input's
	// references.
	plan.probe = true
	src := s.file(in, plan)
	checked, err := s.vm.CheckFile(key, src.b.String())
	if err != nil {
		fmt.Fprintln(errOut, src.explain(err))
		return
	}
	s.resolve(in, checked, src, plan)
	plan.probe = false
	plan.show = in.lastIsExpr && !checked.IsUnit(replValueName)
	// notKept is each name the session cannot keep, and why.
	var notKept [][2]string
	const unwritable, uncarried = "its type cannot be written in source", "the VM cannot carry a value of its type between inputs"
	for i, name := range in.bound {
		typ, ok := checked.LocalType(fmt.Sprintf("%s%d", replProbePrefix, i), replSessionModule)
		if !ok {
			notKept = append(notKept, [2]string{name, unwritable})
			continue
		}
		plan.stores = append(plan.stores, replStore{name: name, id: s.vm.NewID(), typ: typ})
	}
	for i, o := range in.onces {
		if typ, ok := checked.LocalType(fmt.Sprintf("%sonce_%d", replProbePrefix, i), replSessionModule); ok {
			o.typ = typ
			o.ent.vid = s.vm.NewID()
		} else {
			notKept = append(notKept, [2]string{o.name, unwritable})
		}
	}

	load := func() (*vmhost.Program, error) {
		src = s.file(in, plan)
		p, err := s.vm.LoadFile(key, src.b.String())
		if err != nil {
			s.vm.Discard(key)
		}
		return p, err
	}
	check := func() error {
		_, err := s.vm.CheckFile(key, s.file(in, plan).b.String())
		return err
	}
	p, err := load()
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
			if check() == nil {
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
			if check() != nil {
				o.typ = ""
				notKept = append(notKept, [2]string{o.name, unwritable})
			}
		}
		plan.stores = kept
		p, err = load()
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
			s.vm.Discard(key)
			p, err = load()
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
	ran = true
	s.vm.Commit(p)
	for _, d := range plan.replaced {
		// Code earlier inputs compiled calls the new impl too.
		header := strings.TrimPrefix(d.key, "impl ")
		decl := header
		if d.inherent != "" {
			decl = d.inherent
		}
		s.vm.Supersede(p, key, d.file, decl)
	}
	for _, d := range in.decls {
		// An impl for an earlier input's type replaces the impl the front
		// end synthesized in the type's file (the universal Debug) for code
		// earlier inputs compiled too.
		if d.inherent == "" && d.typeFile != "" && d.typeFile != key {
			s.vm.Supersede(p, key, d.typeFile, strings.TrimPrefix(d.key, "impl "))
		}
	}
	for _, n := range s.commit(in, plan, src) {
		notKept = append(notKept, n)
	}
	for _, n := range notKept {
		fmt.Fprintf(errOut, "note: %s is not kept for later inputs: %s\n", n[0], n[1])
	}
}

// commit makes the input's definitions the session's after its file src
// ran, and answers each definition that cannot be kept because it refers
// to one that is not.
func (s *replSession) commit(in *replInput, plan *replPlan, src *replSource) [][2]string {
	var notKept [][2]string
	path := replFilePath(plan.key)
	// fileLine is the first line of each of the input's declarations and
	// onces in its file.
	declLine, onceLine := map[int]int{}, map[int]int{}
	for _, o := range src.owner {
		if o.decl > 0 && declLine[o.decl] == 0 {
			declLine[o.decl] = o.start
		}
		if o.once > 0 && onceLine[o.once] == 0 {
			onceLine[o.once] = o.start
		}
	}
	// made is every entity this input made that is kept.
	made := map[*replEntity]bool{}
	var fns []*replEntity
	var decls []*replDecl
	for i, d := range in.decls {
		if d.same {
			continue
		}
		d.file, d.fileLine = plan.key, declLine[i+1]
		if d.ent == nil {
			decls = append(decls, d)
			continue
		}
		e := d.ent
		e.file, e.refs = plan.key, d.refs
		fns = append(fns, e)
		made[e] = true
	}
	var onces []*replEntity
	for _, o := range in.onces {
		if o.typ == "" {
			continue
		}
		e := o.ent
		e.file, e.typ, e.refs = plan.key, o.typ, o.refs
		onces = append(onces, e)
		made[e] = true
	}
	// A definition that refers to one of this input's that is not kept (a
	// once of an unwritable type) is not kept either.
	earlier := map[*replEntity]bool{}
	for _, e := range s.ents {
		earlier[e] = true
	}
	resolved := func(refs []*replEntity) (string, bool) {
		for _, r := range refs {
			if !made[r] && !earlier[r] {
				return r.name, false
			}
		}
		return "", true
	}
	for grew := true; grew; {
		grew = false
		for e := range made {
			if missing, ok := resolved(e.refs); !ok {
				delete(made, e)
				notKept = append(notKept, [2]string{e.name, "it uses " + missing + ", which is not kept"})
				grew = true
			}
		}
	}

	s.decls = plan.decls
	for _, d := range decls {
		if missing, ok := resolved(d.refs); ok {
			s.decls = append(s.decls, d)
		} else {
			notKept = append(notKept, [2]string{"`" + d.key + "`", "it uses " + missing + ", which is not kept"})
		}
	}
	s.ents = plan.ents
	visible := map[string]*replEntity{}
	for name, e := range plan.visible {
		visible[name] = e
	}
	for _, e := range append(fns, onces...) {
		if made[e] {
			s.ents = append(s.ents, e)
			s.byDef[replDef{path, e.name}] = e
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
		for _, r := range e.refs {
			mark(r)
		}
	}
	for _, e := range s.visible {
		mark(e)
	}
	for _, d := range s.decls {
		for _, r := range d.refs {
			mark(r)
		}
	}
	ents := s.ents[:0:0]
	for _, e := range s.ents {
		if live[e] {
			ents = append(ents, e)
		} else if e.kind != replValue {
			delete(s.byDef, replDef{replFilePath(e.file), e.name})
		}
	}
	s.ents = ents
}
