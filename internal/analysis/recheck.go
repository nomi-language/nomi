package analysis

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An edit that leaves a file's interface alone (interfaceFingerprint) changes
// only what its bodies say. Recheck then keeps the file's declarations, their
// nodes, symbols and scopes, gives the kept declaration nodes the new bodies
// and the new text's positions (graftEdit), moves every position the kept
// declarations hold to where the new text puts it (textShift), and rebuilds
// the file's bodies alone (rebuildBodies). The program's other files keep
// everything; the program's reductions over every file's bodies run again.

// RecheckPath names the way Recheck answered.
type RecheckPath int

const (
	// RecheckFull is a full check of the edited texts.
	RecheckFull RecheckPath = iota
	// RecheckBodies rebuilt only the edited file's bodies.
	RecheckBodies
	// RecheckUnchanged answered the program as it was: the edit changed no
	// text.
	RecheckUnchanged
	// RecheckDependents checked the edited files and the files that import
	// them afresh and kept every other file's analysis
	// (recheckDependents).
	RecheckDependents
)

func (p RecheckPath) String() string {
	switch p {
	case RecheckBodies:
		return "bodies"
	case RecheckUnchanged:
		return "unchanged"
	case RecheckDependents:
		return "dependents"
	}
	return "full"
}

// RecheckOutcome is how one Recheck answered, and why it checked the whole
// program when it did.
type RecheckOutcome struct {
	Path RecheckPath
	// Reason says why a full check was needed; "" otherwise.
	Reason string
}

// bodyFields are the fields of a declaration that hold what an edit to a
// body may change without changing the file's interface: a function's body,
// an attached test's body, everything a test declaration runs, and a
// `once`'s value. Nothing another file reads is in them but an unannotated
// `once`'s type, which checking its value gives it: a recheck infers it
// again and compares (OnceTable.reinfer). A parameter's default and an
// interface method's default body are not among them: other files lower
// them.
var bodyFields = map[reflect.Type]map[string]bool{
	reflect.TypeFor[ast.FuncDef]():      {"Body": true},
	reflect.TypeFor[ast.AttachedTest](): {"Body": true},
	reflect.TypeFor[ast.TestDecl]():     {"Body": true, "Clock": true, "Boot": true, "Setup": true, "ContextPattern": true},
	reflect.TypeFor[ast.OnceBinding]():  {"Value": true},
}

// triviaTypes are the types that hold comments and the layout around them.
// They are no part of a file's interface, and a graft copies them whole.
var triviaTypes = map[reflect.Type]bool{
	reflect.TypeFor[ast.Trivia]():        true,
	reflect.TypeFor[[]ast.Trivia]():      true,
	reflect.TypeFor[ast.TriviaCarrier](): true,
	reflect.TypeFor[ast.ItemComments]():  true,
	reflect.TypeFor[*ast.ItemComments](): true,
}

// positionedNamePrefix starts the name the parser gives a destructured
// parameter, `__destr_<line>_<col>`, which spells the pattern's position.
// The fingerprint leaves the position out, and a graft takes the new name.
const positionedNamePrefix = "__destr_"

// isPositionField reports whether f is a line or column: an int field whose
// name ends in Line or Col (Line, EndCol, ModuleLine, Span.StartCol).
func isPositionField(f reflect.StructField) bool {
	return f.Type.Kind() == reflect.Int && (strings.HasSuffix(f.Name, "Line") || strings.HasSuffix(f.Name, "Col"))
}

// interfaceFingerprint renders what of a file's syntax another file may
// depend on: every top-level declaration as the parser wrote it, its
// visibility, signature, type parameters and bounds, type body, impl and
// derive headers with their items' signatures, imports, `once` bindings with
// their values, parameter defaults and interface default bodies, and whether
// the file has `main` and `boot`. It leaves out what bodyFields name,
// positions and comments. Two texts of a file with the same fingerprint
// differ only inside bodies, in layout or in comments. nodes are the file's
// nodes as parsed, before derive lowering consumes any.
func interfaceFingerprint(nodes []ast.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		fingerprintValue(&b, reflect.ValueOf(n))
		b.WriteByte('\n')
	}
	return b.String()
}

func fingerprintValue(b *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		fingerprintValue(b, v.Elem())
	case reflect.Struct:
		t := v.Type()
		b.WriteString(t.Name())
		b.WriteByte('{')
		body := bodyFields[t]
		for i := range t.NumField() {
			f := t.Field(i)
			if body[f.Name] || isPositionField(f) || triviaTypes[f.Type] || f.Type == reflect.TypeFor[ast.SpanCarrier]() {
				continue
			}
			b.WriteString(f.Name)
			b.WriteByte(':')
			fingerprintValue(b, v.Field(i))
			b.WriteByte(' ')
		}
		b.WriteByte('}')
	case reflect.Slice, reflect.Array:
		b.WriteByte('[')
		for i := range v.Len() {
			fingerprintValue(b, v.Index(i))
			b.WriteByte(',')
		}
		b.WriteByte(']')
	case reflect.String:
		s := v.String()
		if strings.HasPrefix(s, positionedNamePrefix) {
			s = positionedNamePrefix
		}
		b.WriteString(strconv.Quote(s))
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		b.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	}
}

// textShift is where an edit moves a position: the edit replaced one stretch
// of the old text, which starts at the same position in both texts, so a
// position before it stays, and one after it moves by the difference between
// the stretch's old and new end. A position inside the old stretch has no
// place in the new text.
type textShift struct {
	startLine, startCol   int
	oldEndLine, oldEndCol int
	newEndLine, newEndCol int
}

// shiftBetween is the shift that turns old into new: the stretch between
// their longest common prefix and longest common suffix.
func shiftBetween(old, new string) textShift {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	s := 0
	for s < len(old)-p && s < len(new)-p && old[len(old)-1-s] == new[len(new)-1-s] {
		s++
	}
	var t textShift
	t.startLine, t.startCol = offsetPosition(old, p)
	t.oldEndLine, t.oldEndCol = offsetPosition(old, len(old)-s)
	t.newEndLine, t.newEndCol = offsetPosition(new, len(new)-s)
	return t
}

// offsetPosition is the 1-based line and byte column of offset in text, as
// the lexer counts them.
func offsetPosition(text string, offset int) (int, int) {
	line := 1 + strings.Count(text[:offset], "\n")
	return line, offset - strings.LastIndexByte(text[:offset], '\n')
}

// moves reports whether the shift moves any position.
func (t textShift) moves() bool {
	return t.oldEndLine != t.newEndLine || t.oldEndCol != t.newEndCol
}

// at answers where (line, col) of the old text is in the new one, and false
// when it is inside the stretch the edit replaced. Zero positions and the
// synthesized band (IsSynthesizedLine) do not move.
func (t textShift) at(line, col int) (int, int, bool) {
	if line <= 0 || line >= synthLineBase {
		return line, col, true
	}
	before := func(l, c, bl, bc int) bool { return l < bl || l == bl && c < bc }
	if before(line, col, t.startLine, t.startCol) {
		return line, col, true
	}
	if before(line, col, t.oldEndLine, t.oldEndCol) {
		return 0, 0, false
	}
	if line == t.oldEndLine {
		return t.newEndLine, col - t.oldEndCol + t.newEndCol, true
	}
	return line - t.oldEndLine + t.newEndLine, col, true
}

// grafter walks a file's kept declaration nodes beside the same
// declarations in a new parse whose interface is the same, and notes the
// writes that give the kept nodes the new bodies, positions and comments.
// Nothing is written until commit, so a walk that fails changes nothing.
type grafter struct {
	shift  positionMap
	reason string
	// moves are the kept nodes' positions beside the new ones: old line,
	// old column, new line, new column.
	moves [][4]int
	ops   []func()
	// oldBodies and newBodies are the bodies the graft takes out and puts in.
	oldBodies, newBodies []ast.Node
	// kept is every node of the kept declarations, which a symbol this file
	// defines names as its Node.
	kept map[ast.Node]bool
}

func (g *grafter) fail(format string, args ...any) {
	if g.reason == "" {
		g.reason = fmt.Sprintf(format, args...)
	}
}

// set notes the write o = n.
func (g *grafter) set(o, n reflect.Value) {
	if !o.CanSet() {
		g.fail("an unsettable %s", o.Type())
		return
	}
	g.ops = append(g.ops, func() { o.Set(n) })
}

func (g *grafter) commit() {
	for _, op := range g.ops {
		op()
	}
}

// value walks o, from the kept tree, beside n, from the new one.
func (g *grafter) value(o, n reflect.Value) {
	if g.reason != "" {
		return
	}
	if triviaTypes[o.Type()] {
		g.set(o, n)
		return
	}
	switch o.Kind() {
	case reflect.Interface:
		switch {
		case o.IsNil() && n.IsNil():
		case n.IsNil():
			// A field the analysis filled in on the kept node.
		case o.IsNil():
			g.fail("%s is set only in the new text", o.Type())
		case o.Elem().Type() != n.Elem().Type():
			g.fail("a %s became a %s", o.Elem().Type(), n.Elem().Type())
		default:
			g.value(o.Elem(), n.Elem())
		}
	case reflect.Pointer:
		switch {
		case o.IsNil() && n.IsNil():
		case n.IsNil():
		case o.IsNil():
			g.fail("%s is set only in the new text", o.Type())
		case o.Pointer() == n.Pointer():
			// Shared by both trees (a prepended prelude import).
		default:
			if node, ok := o.Interface().(ast.Node); ok {
				g.kept[node] = true
			}
			g.value(o.Elem(), n.Elem())
		}
	case reflect.Slice:
		if o.Len() != n.Len() {
			g.fail("a %s of %d became %d", o.Type(), o.Len(), n.Len())
			return
		}
		for i := range o.Len() {
			g.value(o.Index(i), n.Index(i))
		}
	case reflect.Struct:
		g.fields(o, n)
	}
}

// fields walks a struct's fields: a body field takes the new body, a line or
// column the new position, and the rest is walked.
func (g *grafter) fields(o, n reflect.Value) {
	t := o.Type()
	body := bodyFields[t]
	for i := range t.NumField() {
		f := t.Field(i)
		fo, fn := o.Field(i), n.Field(i)
		switch {
		case body[f.Name]:
			if !fo.IsNil() {
				g.oldBodies = append(g.oldBodies, fo.Interface().(ast.Node))
			}
			if !fn.IsNil() {
				g.newBodies = append(g.newBodies, fn.Interface().(ast.Node))
			}
			g.set(fo, fn)
		case isPositionField(f):
			if fo.Int() != fn.Int() {
				g.set(fo, fn)
			}
			if name, ok := strings.CutSuffix(f.Name, "Line"); ok {
				if col := o.FieldByName(name + "Col"); col.IsValid() && col.Kind() == reflect.Int {
					g.moves = append(g.moves, [4]int{int(fo.Int()), int(col.Int()), int(fn.Int()), int(n.FieldByName(name + "Col").Int())})
				}
			}
		case fo.Kind() == reflect.String:
			if strings.HasPrefix(fo.String(), positionedNamePrefix) && fo.String() != fn.String() {
				g.set(fo, fn)
			}
		case fo.Kind() == reflect.Interface || fo.Kind() == reflect.Pointer || fo.Kind() == reflect.Slice || fo.Kind() == reflect.Struct:
			g.value(fo, fn)
		}
	}
}

// placeMoves chooses the map that moves every kept position the walk saw
// where the new text has it: the one stretch between the texts' common
// prefix and suffix (textShift), or failing that, line by line as the
// declarations' own positions moved (lineShifts). With neither it fails the
// graft: a position the declarations' kept analysis holds might not move
// where the new text has it either.
func (g *grafter) placeMoves(old, new string) {
	fits := func(m positionMap) bool {
		for _, mv := range g.moves {
			if l, c, ok := m.at(mv[0], mv[1]); !ok || l != mv[2] || c != mv[3] {
				return false
			}
		}
		return true
	}
	if stretch := shiftBetween(old, new); fits(stretch) {
		g.shift = stretch
		return
	}
	if lines, ok := lineShiftsOf(g.moves); ok && fits(lines) {
		g.shift = lines
		return
	}
	g.fail("the declarations' positions do not move as one edit or line by line")
}

// positionMap answers where a position of a file's old text is in its new
// one, and false when it has no place there.
type positionMap interface {
	at(line, col int) (int, int, bool)
	// moves reports whether any position moves.
	moves() bool
}

// lineShifts moves a position by the move of its line: each line that holds
// a declaration's position moves to the line the new text has it on, its
// columns by one amount. A position on any other line has no place.
type lineShifts struct {
	lines map[int][2]int // old line → new line, column change
	moved bool
}

// lineShiftsOf is the lineShifts of moves (old line, old column, new line,
// new column), or false when two positions on one line move apart.
func lineShiftsOf(moves [][4]int) (lineShifts, bool) {
	s := lineShifts{lines: map[int][2]int{}}
	for _, mv := range moves {
		if mv[0] <= 0 || mv[0] >= synthLineBase {
			continue
		}
		shift := [2]int{mv[2], mv[3] - mv[1]}
		if was, ok := s.lines[mv[0]]; ok && was != shift {
			return s, false
		}
		s.lines[mv[0]] = shift
		if mv[0] != mv[2] || mv[1] != mv[3] {
			s.moved = true
		}
	}
	return s, true
}

func (s lineShifts) moves() bool { return s.moved }

func (s lineShifts) at(line, col int) (int, int, bool) {
	if line <= 0 || line >= synthLineBase {
		return line, col, true
	}
	shift, ok := s.lines[line]
	if !ok {
		return 0, 0, false
	}
	return shift[0], col + shift[1], true
}

// preparedNodes are a file's nodes from a new text as a build prepares
// them: for the entry as CheckProgram does before its build, and for any
// other file as the build does, with the prelude imports the kept nodes
// start with. frontEnd are the entry's derive-lowering and duplicate test
// name errors.
func preparedNodes(nodes []ast.Node, entry bool, kept []ast.Node) (prepared []ast.Node, frontEnd []TypeError) {
	if entry {
		nodes, lowerErrs := LowerDerives(nodes)
		testNameErrs := CheckDuplicateTestNames(nodes)
		prog, _ := SynthesizeDerives(nodes)
		return SynthesizeUniversalDebug(prog), append(lowerErrs, testNameErrs...)
	}
	var prelude []ast.Node
	for _, n := range kept {
		if !isPreludeInjectLine(n.LineNum()) {
			break
		}
		prelude = append(prelude, n)
	}
	lowered, _ := LowerDerives(append(prelude, nodes...))
	extended, _ := SynthesizeDerives(lowered)
	return SynthesizeUniversalDebug(extended), nil
}

// bodyEdit is one edited file's graft, decided and not yet written.
type bodyEdit struct {
	key      string
	fa       *FileAnalysis
	nodes    []ast.Node
	g        *grafter
	frontEnd []TypeError
	// The functions the old and the new bodies declare.
	oldFuncs, newFuncs []*ast.FuncDef
	// importsInOrder reports that the old and the new bodies import the
	// same paths in the same order, so the file's imports are as they were
	// (carryImports).
	importsInOrder bool
}

// rebuildEditedBodies is Recheck's fast path: it answers the program after
// edits, each of which leaves its file's interface as it was, by rebuilding
// only those files' bodies, and false with the reason when the edits need
// more. parsed holds each edited text parsed; it deletes the entry of a file
// whose nodes it prepared, since the graft takes them.
//
// It writes nothing until it has decided; once it has, prev's analyses are
// the answer's. A `once` value is a body, but an unannotated one's type,
// which other files read, is what checking the value says: the bindings are
// inferred again (OnceTable.reinfer), and when a type may have changed it
// answers false after writing, with prev consumed and the edited files'
// bodies rebuilt, for the caller to check the files that read it.
func (dm *DocumentManager) rebuildEditedBodies(prev *CheckedProgram, edits []ProgramEdit, parsed map[string]*parsedFile) (*CheckedProgram, string) {
	c := prev.checks
	switch {
	case c == nil:
		return nil, "the program was not checked"
	case prev.externs:
		return nil, "the project declares Go bindings"
	}
	var plan []bodyEdit
	moves := false
	for _, edit := range edits {
		key := edit.Key
		fa, nodes := prev.Files[key], prev.Nodes[key]
		switch {
		case fa == nil:
			return nil, "the program does not reach the file"
		case fa.kept == nil:
			return nil, "the file's declarations were not kept"
		case len(prev.Syntax[key]) > 0:
			return nil, "the old text has a syntax error"
		case prev.fingerprints[key] == "":
			return nil, "the old text has no interface fingerprint"
		}
		pf := parsed[key]
		if len(pf.errs) > 0 {
			return nil, "the new text has a syntax error"
		}
		if pf.fingerprint != prev.fingerprints[key] {
			return nil, "the file's interface changed"
		}
		delete(parsed, key)
		prepared, frontEnd := preparedNodes(pf.nodes, key == prev.Entry, nodes)
		if len(prepared) != len(nodes) {
			return nil, "the prepared declarations differ in number"
		}
		g := &grafter{kept: map[ast.Node]bool{}}
		for i := range nodes {
			g.value(reflect.ValueOf(&nodes[i]).Elem(), reflect.ValueOf(&prepared[i]).Elem())
		}
		if g.reason == "" {
			g.placeMoves(prev.Texts[key], edit.Text)
		}
		if g.reason != "" {
			return nil, g.reason
		}
		// An import at the top of a block may load a module the program
		// reaches no other way, so the bodies must import what they imported
		// before.
		oldFuncs, oldImports := bodyContents(g.oldBodies)
		newFuncs, newImports := bodyContents(g.newBodies)
		inOrder := slices.Equal(oldImports, newImports)
		slices.Sort(oldImports)
		slices.Sort(newImports)
		if !slices.Equal(oldImports, newImports) {
			return nil, "a body's imports changed"
		}
		if reason := shiftableKept(fa, g.shift); reason != "" {
			return nil, reason
		}
		moves = moves || g.shift.moves()
		plan = append(plan, bodyEdit{key: key, fa: fa, nodes: nodes, g: g, frontEnd: frontEnd, oldFuncs: oldFuncs, newFuncs: newFuncs, importsInOrder: inOrder})
	}
	prog := prev.Files[prev.Entry].Program()
	if moves && len(prog.Errors) > 0 && prog.programErrors == nil {
		// They name no file, so which of them to move is not known.
		return nil, "the program has errors of its own and positions move"
	}

	// Decided: from here prev's analyses become the answer's.
	prev.consumed = true
	prog.revision++
	for _, e := range plan {
		e.g.commit()
		if e.g.shift.moves() {
			shiftDeclarations(prev, e.fa, e.g.shift, e.g.kept)
		}
		// The functions the old bodies declared leave the program's table
		// of every function's file, and the new bodies' join it.
		if prog.ScopedFunctionFiles != nil {
			for _, fn := range e.oldFuncs {
				delete(prog.ScopedFunctionFiles, fn)
			}
			for _, fn := range e.newFuncs {
				prog.ScopedFunctionFiles[fn] = e.fa
			}
		}
		if prog.results != nil {
			for _, ii := range e.fa.ImplImports {
				delete(prog.results.implImportsUnused, ii)
			}
		}
	}
	if moves && prog.programErrors != nil {
		// They name no file; they are worked out again from the
		// declarations, which now hold the new positions.
		prog.Errors = prog.programErrors()
	}

	edited := make(map[*FileAnalysis]bool, len(plan))
	for _, e := range plan {
		generation := e.fa.generation
		e.fa.walkBodiesAgain(e.nodes)
		if e.importsInOrder {
			e.fa.carryImports(generation)
		}
		edited[e.fa] = true
	}
	if prog.Onces.reinfer(edited) {
		return nil, "an unannotated once's type may have changed"
	}

	next := &CheckedProgram{
		Root: prev.Root, Entry: prev.Entry,
		Texts:            maps.Clone(prev.Texts),
		Files:            prev.Files,
		Nodes:            prev.Nodes,
		Syntax:           prev.Syntax,
		fingerprints:     prev.fingerprints,
		implFingerprints: prev.implFingerprints,
		externs:          prev.externs,
		damaged:          prev.damaged,
		reachesEntry:     prev.reachesEntry,
		unread:           prev.unread,
		entryOwn:         prev.entryOwn,
		checks:           c,
	}
	// Checked in the order a full check checks them: the entry, then the
	// others by key.
	slices.SortFunc(plan, func(a, b bodyEdit) int {
		switch {
		case a.key == prev.Entry:
			return -1
		case b.key == prev.Entry:
			return 1
		}
		return strings.Compare(a.key, b.key)
	})
	entryEdited := false
	for _, e := range plan {
		next.Texts[e.key] = editedText(edits, e.key)
		c.build[e.key] = slices.Clone(e.fa.TypeErrors)
		c.check[e.key] = CheckTypes(e.fa, e.nodes)
		if e.key == prev.Entry {
			c.frontEnd = e.frontEnd
			entryEdited = true
		}
	}
	c.reduce(next)
	if entryEdited {
		c.checkEntry(next)
	}
	for _, e := range plan {
		MarkTailCalls(e.nodes)
	}
	c.assemble(next)
	return next, ""
}

// editedText is the text edits give the file with module key.
func editedText(edits []ProgramEdit, key string) string {
	for _, e := range edits {
		if e.Key == key {
			return e.Text
		}
	}
	return ""
}

// bodyContents are the functions declared in bodies, and the imports in
// them rendered without positions, in the order they appear.
func bodyContents(bodies []ast.Node) (funcs []*ast.FuncDef, imports []string) {
	for _, body := range bodies {
		WalkNodes(body, func(n ast.Node) {
			switch n := n.(type) {
			case *ast.FuncDef:
				funcs = append(funcs, n)
			case *ast.ImportStmt, *ast.ImportBlock:
				var b strings.Builder
				fingerprintValue(&b, reflect.ValueOf(n))
				imports = append(imports, b.String())
			}
		})
	}
	return funcs, imports
}

// shiftableKept answers why a position fa's kept declarations hold cannot
// move with shift, or "" when every one can.
func shiftableKept(fa *FileAnalysis, shift positionMap) string {
	if !shift.moves() {
		return ""
	}
	k := fa.kept
	bad := ""
	check := func(what string, line, col int) {
		if _, _, ok := shift.at(line, col); !ok && bad == "" {
			bad = fmt.Sprintf("the declarations' %s at %d:%d is inside the edit", what, line, col)
		}
	}
	for _, m := range []map[Pos]*Symbol{k.references, k.definitions, k.punned} {
		for pos := range m {
			check("fact", pos.Line, pos.Col)
		}
	}
	for _, r := range k.typeRefs {
		check("type reference", r.pos.Line, r.pos.Col)
	}
	for _, t := range k.typed {
		check("type", t.pos.Line, t.pos.Col)
	}
	for _, errs := range [][]TypeError{k.errors, k.typeErrors, fa.BootErrors} {
		for _, e := range errs {
			check("diagnostic", e.Line, e.Col)
		}
	}
	return bad
}

// shiftDeclarations moves every position fa's kept declarations hold, and
// every copy of one the program's other files hold, to where shift puts it.
// The graft has already moved the nodes', and nodes are the kept
// declarations' nodes.
//
// A symbol is this file's when it names the file as its source, when its
// Node is one of the kept declarations' (an import's path segment, which
// names no source), or when the file defines it at its own position (an
// import's binding, which names the imported file).
func shiftDeclarations(p *CheckedProgram, fa *FileAnalysis, shift positionMap, nodes map[ast.Node]bool) {
	k := fa.kept
	path := fa.FilePath
	at := func(pos Pos) Pos {
		pos.Line, pos.Col, _ = shift.at(pos.Line, pos.Col)
		return pos
	}
	seen := map[*Symbol]bool{}
	// others is set while the other files' facts are visited: a symbol a
	// checker made for a site there may name one of these nodes and still
	// stand at the site.
	others := false
	var symbol func(sym *Symbol, owned bool)
	symbol = func(sym *Symbol, owned bool) {
		if sym == nil || seen[sym] {
			return
		}
		if !owned && !(path != "" && sym.SourceFile == path) && (others || sym.Node == nil || !nodes[sym.Node]) {
			return
		}
		seen[sym] = true
		sym.Pos = at(sym.Pos)
		if sym.Resolved != nil {
			// An import's binding: its members are the imported file's.
			return
		}
		for _, m := range sym.Members {
			symbol(m, m != nil && (m.SourceFile == path || m.SourceFile == ""))
		}
	}
	keys := func(m map[Pos]*Symbol, defs bool) map[Pos]*Symbol {
		out := make(map[Pos]*Symbol, len(m))
		for pos, sym := range m {
			// A symbol defined here at its own position is this file's,
			// whatever file it names as its source (an import's binding).
			symbol(sym, defs && sym != nil && sym.Pos == pos)
			out[at(pos)] = sym
		}
		return out
	}
	k.definitions = keys(k.definitions, true)
	k.references = keys(k.references, false)
	k.punned = keys(k.punned, false)
	for _, sym := range k.symbols {
		symbol(sym, false)
	}
	for _, byName := range fa.TypeMethods {
		for _, sym := range byName {
			symbol(sym, false)
		}
	}
	// The fields of an anonymous struct type written in a declaration are
	// the type references BuildTypes recorded at their names, and the types
	// built from them hold the same positions (FieldDef).
	anonFields := map[Pos]string{}
	for _, r := range k.typeRefs {
		if r.sym != nil && r.sym.Kind == SymbolField {
			anonFields[r.pos] = r.sym.Name
		}
	}
	for i := range k.typeRefs {
		r := &k.typeRefs[i]
		// A field's symbol is recorded at its own position, with no source.
		symbol(r.sym, r.sym != nil && r.sym.Pos == r.pos)
		r.pos = at(r.pos)
	}
	for i := range k.typed {
		k.typed[i].pos = at(k.typed[i].pos)
	}
	for _, byType := range k.manifest {
		for _, recs := range byType {
			for i := range recs {
				shiftRecording(&recs[i], path, at)
			}
		}
	}
	shiftErrors(k.errors, path, shift, true)
	shiftErrors(k.typeErrors, path, shift, true)
	shiftErrors(fa.BootErrors, path, shift, true)
	var scope func(s *Scope)
	scope = func(s *Scope) {
		s.Start, s.End = at(s.Start), at(s.End)
		for _, sym := range s.Symbols {
			symbol(sym, false)
		}
		for _, child := range s.Children {
			scope(child)
		}
	}
	fa.ModuleScope.Start, fa.ModuleScope.End = at(fa.ModuleScope.Start), at(fa.ModuleScope.End)
	for _, child := range fa.ModuleScope.Children[:k.scopes] {
		scope(child)
	}
	if len(anonFields) > 0 {
		types := map[Type]bool{}
		move := func(f *FieldDef) {
			if name, ok := anonFields[Pos{Line: f.Line, Col: f.Col}]; ok && name == f.Name {
				f.Line, f.Col, _ = shift.at(f.Line, f.Col)
			}
		}
		for sym := range seen {
			moveAnonFields(sym.Type, types, move)
		}
		for _, t := range k.typed {
			moveAnonFields(t.typ, types, move)
		}
	}

	// The other files' copies: a checker's per-site copy of one of this
	// file's symbols, and a diagnostic that points here.
	c := p.checks
	others = true
	for key, other := range p.Files {
		if other == fa || other == nil {
			continue
		}
		for _, m := range []map[Pos]*Symbol{other.References, other.Definitions, other.PunnedFieldLabels} {
			for _, sym := range m {
				symbol(sym, false)
			}
		}
		for _, errs := range [][]TypeError{c.build[key], c.check[key], c.appRoots[key], c.implImports[key]} {
			shiftErrors(errs, path, shift, false)
		}
	}
	shiftErrors(c.coherence, path, shift, false)
	shiftErrors(c.entryChecks, path, shift, false)
	if p.Entry != "" && p.Files[p.Entry] != fa {
		shiftErrors(c.frontEnd, path, shift, false)
	}
}

// moveAnonFields moves the field positions of the anonymous struct types t
// is built from (FieldDef.Line and Col), where move says, once each.
func moveAnonFields(t Type, seen map[Type]bool, move func(*FieldDef)) {
	if t == nil || isNilType(t) || seen[t] {
		return
	}
	seen[t] = true
	walk := func(ts ...Type) {
		for _, x := range ts {
			moveAnonFields(x, seen, move)
		}
	}
	fields := func(fs []FieldDef) {
		for i := range fs {
			move(&fs[i])
			walk(fs[i].Type)
		}
	}
	switch tt := t.(type) {
	case *AnonStructType:
		fields(tt.Fields)
	case *StructType:
		fields(tt.Fields)
		walk(tt.TypeArgs...)
	case *EnumType:
		for i := range tt.Variants {
			v := &tt.Variants[i]
			walk(v.DataType, v.Embedded)
			fields(v.Fields)
		}
		walk(tt.TypeArgs...)
	case *DistinctType:
		walk(tt.Inner)
		walk(tt.TypeArgs...)
	case *FuncType:
		walk(tt.Params...)
		walk(tt.Return)
	case *TupleType:
		walk(tt.Elems...)
	case *ListType:
		walk(tt.Elem)
	case *MapType:
		walk(tt.Key, tt.Val)
	case *PartialType:
		walk(tt.Inner)
	case *TypeVar:
		walk(tt.Resolved)
	case *InterfaceType:
		for _, m := range tt.Methods {
			walk(m.Params...)
			walk(m.Return)
		}
		walk(tt.TypeArgs...)
	}
}

// shiftRecording moves a demand recorded in the file at path.
func shiftRecording(r *Recording, path string, at func(Pos) Pos) {
	if r.Pos.File == path {
		r.Pos = at(r.Pos)
	}
	if d := r.Derive; d != nil {
		moved := *d
		if moved.DerivePos.File == path || moved.DerivePos.File == "" {
			moved.DerivePos = at(moved.DerivePos)
		}
		if moved.FieldPos.File == path || moved.FieldPos.File == "" {
			moved.FieldPos = at(moved.FieldPos)
		}
		r.Derive = &moved
	}
}

// shiftErrors moves the positions in errs that are in the file at path: the
// errors' own when own (errs are that file's), and the related locations
// that name it.
func shiftErrors(errs []TypeError, path string, shift positionMap, own bool) {
	for i := range errs {
		e := &errs[i]
		if own {
			e.Line, e.Col, _ = shift.at(e.Line, e.Col)
			if e.EndLine > 0 {
				e.EndLine, e.EndCol, _ = shift.at(e.EndLine, e.EndCol)
			}
		}
		if len(e.Related) == 0 {
			continue
		}
		related := slices.Clone(e.Related)
		for j := range related {
			r := &related[j]
			if r.File == path && path != "" || r.File == "" && own {
				r.Line, r.Col, _ = shift.at(r.Line, r.Col)
				if r.EndLine > 0 {
					r.EndLine, r.EndCol, _ = shift.at(r.EndLine, r.EndCol)
				}
			}
		}
		e.Related = related
	}
}
