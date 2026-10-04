package analysis

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Application fields.
//
// An entry's boot, `fn boot(): App` or `fn boot(startup: Startup): App` in a
// file that defines `fn main`, returns the application value: a struct with
// at most one field of std/context's Context type. Code reads one of its
// fields by naming the type,
// `MyApp.logger`, and replaces one from a statement to the end of its block
// with `with MyApp.logger = value`.
//
// An application type is a struct some entry boot of the project returns
// (FileAnalysis.AppTypes). `T.field` on an application type is always the
// field: such a type may not declare an inherent member with a field's name
// (checkAppTypeMembers). On any other type `T.member` is owner access.
//
// Whether the right application is booted is checked at each execution root:
// an entry's `main`, each test, each group's setup, and each boot body
// (checkAppRoots). Every application-field read reachable from a root must
// name the type its boot returns.

// AppRead is one application-field read, or the field a `with` override
// replaces: App is the application type and Field its field.
type AppRead struct {
	App          *StructType
	Field        FieldDef
	ContextField string
}

// SchemaFields answers the top-level fields of a boot's result type.
func SchemaFields(schema Type) []FieldDef {
	switch s := schema.(type) {
	case *StructType:
		return s.Fields
	case *AnonStructType:
		return s.Fields
	}
	return nil
}

func isScopedSchema(schema Type) bool {
	switch schema.(type) {
	case *StructType, *AnonStructType:
		return true
	}
	return false
}

// ScopedContextField returns the unique top-level canonical Context field.
func ScopedContextField(schema Type, context Type) string {
	name := ""
	for _, field := range SchemaFields(schema) {
		if SameScopedType(field.Type, context) {
			if name != "" {
				return ""
			}
			name = field.Name
		}
	}
	return name
}

func scopedField(schema Type, name string) (FieldDef, bool) {
	for _, f := range SchemaFields(schema) {
		if f.Name == name {
			return f, true
		}
	}
	return FieldDef{}, false
}

// appTypeNamedBy resolves the owner of `Owner.field` to an application type:
// a struct an entry boot of the project returns. The owner is a type name, or
// a file-qualified one (`app.MyApp`). Resolution uses the builder's
// references, so it answers for a file whose checker has not run.
func appTypeNamedBy(fa *FileAnalysis, owner ast.Node) (*StructType, *Symbol) {
	if fa == nil || fa.ContextType == nil {
		return nil, nil
	}
	var pos Pos
	switch o := owner.(type) {
	case *ast.TypeIdent:
		pos = Pos{Line: o.Line, Col: o.Col}
	case *ast.FieldAccess:
		if o.Field == nil {
			return nil, nil
		}
		pos = Pos{Line: o.Field.Line, Col: o.Field.Col}
	default:
		return nil, nil
	}
	sym := fa.References[pos]
	if sym == nil {
		return nil, nil
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	st := appStructOfSymbol(fa, sym)
	if st == nil {
		return nil, nil
	}
	return st, sym
}

// appStructOfSymbol answers the application type sym declares, or nil.
func appStructOfSymbol(fa *FileAnalysis, sym *Symbol) *StructType {
	if fa == nil || fa.ContextType == nil || sym == nil {
		return nil
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym.Kind != SymbolStruct {
		return nil
	}
	st, ok := sym.Type.(*StructType)
	if !ok || st == nil || len(st.TypeParams) > 0 || len(st.TypeArgs) > 0 {
		return nil
	}
	for _, app := range fa.AppTypes {
		if SameScopedType(st, app) {
			return st
		}
	}
	return nil
}

// TestGroupBoot answers the entry boot a `tests` group's `boot` line calls,
// or nil when the group has no `boot` line or its call names no entry boot.
func TestGroupBoot(fa *FileAnalysis, t *ast.TestDecl) *ast.FuncDef {
	if fa == nil || t == nil {
		return nil
	}
	call, ok := t.Boot.(*ast.Call)
	if !ok {
		return nil
	}
	pos, ok := calleePos(call.Func)
	if !ok {
		return nil
	}
	sym := fa.References[pos]
	if sym == nil {
		return nil
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	fn, ok := sym.Node.(*ast.FuncDef)
	if !ok || sym.Kind != SymbolFunction || !fa.BootFunctions[fn] {
		return nil
	}
	return fn
}

// calleePos is where the analyzer records the reference a call's callee
// makes: the name, or a qualified name's last segment.
func calleePos(n ast.Node) (Pos, bool) {
	switch v := n.(type) {
	case *ast.Ident:
		return Pos{Line: v.Line, Col: v.Col}, true
	case *ast.FieldAccess:
		if v.Field == nil {
			return Pos{}, false
		}
		return Pos{Line: v.Field.Line, Col: v.Field.Col}, true
	}
	return Pos{}, false
}

// appReadOf reports whether n reads an application field.
func appReadOf(fa *FileAnalysis, n *ast.FieldAccess) (AppRead, bool) {
	if n == nil || n.Field == nil {
		return AppRead{}, false
	}
	st, _ := appTypeNamedBy(fa, n.Object)
	if st == nil {
		return AppRead{}, false
	}
	field, ok := scopedField(st, n.Field.Name)
	if !ok {
		return AppRead{}, false
	}
	return AppRead{App: st, Field: field, ContextField: ScopedContextField(st, fa.ContextType)}, true
}

// appReadSite is a read found from an execution root, with where it is.
type appReadSite struct {
	read AppRead
	file *FileAnalysis
	line int
	node ast.Node
}

// spelling is the read as written: `MyApp.logger`.
func (s appReadSite) spelling() string {
	return "`" + s.read.App.Name + "." + s.read.Field.Name + "`"
}

// where is the read's location: `server.nomi:2`, or `line 2` for a source
// with no file name.
func (s appReadSite) where() string {
	if s.file == nil || s.file.FilePath == "" {
		return fmt.Sprintf("line %d", s.line)
	}
	return fmt.Sprintf("%s:%d", filepath.Base(s.file.FilePath), s.line)
}

// checkAppRoots checks that each execution root in this file boots the
// application type every read reachable from it names, and that an entry
// point's boot is called only on a `tests` group's `boot` line.
//
// Roots are an entry's `main` (under its file's boot), each test (under its
// group's boot, or none), each group's setup, and each boot body and group
// `boot` line's argument, which run before any field is published.
func checkAppRoots(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	var errors []TypeError
	// The one place an entry boot may be called: a group's `boot` line.
	bootLines := map[Pos]bool{}
	for _, n := range nodes {
		if t, ok := n.(*ast.TestDecl); ok && t.Group {
			if call, ok := t.Boot.(*ast.Call); ok {
				if pos, ok := calleePos(call.Func); ok {
					bootLines[pos] = true
				}
			}
		}
	}
	for pos, sym := range fa.References {
		for sym.Resolved != nil {
			sym = sym.Resolved
		}
		if fn, ok := sym.Node.(*ast.FuncDef); ok && sym.Kind == SymbolFunction && fa.BootFunctions[fn] && !bootLines[pos] {
			errors = append(errors, TypeError{Line: pos.Line, Col: pos.Col, Message: "entry-point boot cannot be called or captured as an ordinary function; a `tests` group calls it on its `boot` line"})
		}
	}
	w := newAppReadWalker(fa)
	reported := map[string]bool{}
	report := func(line, col int, message string) {
		key := fmt.Sprintf("%d:%d:%s", line, col, message)
		if reported[key] {
			return
		}
		reported[key] = true
		errors = append(errors, TypeError{Line: line, Col: col, Message: message})
	}

	// bootFrame is the boot a root runs under: the boot function, the type
	// it returns, and where an error about it is reported. A nil fn is no
	// boot.
	type bootFrame struct {
		fn        *ast.FuncDef
		app       Type
		line, col int
	}
	frameOf := func(fn *ast.FuncDef, line, col int) bootFrame {
		if fn == nil {
			return bootFrame{}
		}
		owner := fa
		if declaring := fa.ScopedFunctionFiles[fn]; declaring != nil {
			owner = declaring
		}
		st, _ := inferAppStruct(fn, []*FileAnalysis{owner, fa})
		return bootFrame{fn: fn, app: st, line: line, col: col}
	}
	// check reports the first mismatch between a root's reads and its boot.
	// subject is how the message names the root ("this test", "this
	// program"); anchorLine/anchorCol is where an error goes when there is no
	// boot to blame.
	check := func(sites []appReadSite, frame bootFrame, subject string, anchorLine, anchorCol int) {
		if len(sites) == 0 {
			return
		}
		line, col := anchorLine, anchorCol
		if frame.fn != nil {
			line, col = frame.line, frame.col
		}
		first := sites[0]
		for _, s := range sites[1:] {
			if !SameScopedType(s.read.App, first.read.App) {
				report(line, col, fmt.Sprintf(
					"code %s runs reads %s (%s) and %s (%s); one boot publishes one application type",
					subject, first.spelling(), first.where(), s.spelling(), s.where()))
				return
			}
		}
		switch {
		case frame.fn == nil:
			report(line, col, fmt.Sprintf("%s has no boot, but code it runs reads %s (%s)", subject, first.spelling(), first.where()))
		case frame.app == nil || !SameScopedType(frame.app, first.read.App):
			report(line, col, fmt.Sprintf("%s boots `%s`, but code it runs reads %s (%s)", subject, typeLabel(frame.app), first.spelling(), first.where()))
		}
	}
	// beforeBoot checks code that runs before any application field is
	// published: a boot body, or a group `boot` line's argument.
	beforeBoot := func(body ast.Node, line, col int) {
		sites := w.reachable(fa, body)
		if len(sites) == 0 {
			return
		}
		report(line, col, fmt.Sprintf(
			"boot runs before any application field is published, but code it runs reads %s (%s)",
			sites[0].spelling(), sites[0].where()))
	}

	group := func(t *ast.TestDecl) {
		var frame bootFrame
		if call, ok := t.Boot.(*ast.Call); ok {
			for _, arg := range call.Args {
				beforeBoot(arg, t.BootLine, t.BootCol)
			}
			frame = frameOf(TestGroupBoot(fa, t), t.BootLine, t.BootCol)
		}
		if t.Setup != nil {
			check(w.reachable(fa, t.Setup), frame, "this group's setup", t.Line, t.Col)
		}
		if t.Body == nil {
			return
		}
		for _, n := range t.Body.Stmts {
			if child, ok := n.(*ast.TestDecl); ok && !child.Group {
				check(w.reachable(fa, child.Body), frame, "this test", child.Line, child.Col)
			}
		}
	}

	var roots func([]ast.Node)
	roots = func(items []ast.Node) {
		for _, n := range items {
			for _, attached := range ast.AttachedTestsOf(n) {
				check(w.reachable(fa, attached.Body), bootFrame{}, "this test", attached.Line, attached.Col)
			}
			switch v := n.(type) {
			case *ast.FuncDef:
				if fa.BootFunctions[v] && v.Name == "boot" {
					beforeBoot(v.Body, v.Line, v.Col)
				}
				if v.Name == "main" && !v.ImplFunction {
					frame := bootFrame{}
					if boot := findBootFunc(nodes); boot != nil && fa.BootFunctions[boot] {
						frame = frameOf(boot, boot.Line, boot.Col)
					}
					check(w.reachable(fa, v.Body), frame, "this program", v.Line, v.Col)
				}
			case *ast.TestDecl:
				if v.Group {
					group(v)
				} else {
					check(w.reachable(fa, v.Body), bootFrame{}, "this test", v.Line, v.Col)
				}
			case *ast.StructDef:
				roots(v.Items)
			case *ast.EnumDef:
				roots(v.Items)
			case *ast.TypeDef:
				roots(v.Items)
			case *ast.ExternType:
				roots(v.Items)
			case *ast.ImplBlock:
				roots(v.Items)
			}
		}
	}
	roots(nodes)
	return errors
}

// typeLabel names a boot's result type in a diagnostic.
func typeLabel(t Type) string {
	if t == nil {
		return "?"
	}
	return t.String()
}

// appReadWalker finds the application-field reads reachable from a body:
// written in it, in a function it calls (across files), in a callback it
// passes to a call, or in a lambda it binds and then calls.
//
// A body's own walk yields its reads and the functions it calls; a
// function's walk is taken once and reused. A root's reads are its body's,
// then those of every function reachable through the calls, breadth first,
// each function once.
type appReadWalker struct {
	fa      *FileAnalysis
	summary map[*ast.FuncDef]*appWalk
	impls   map[string][]appCallee
}

// appCallee is a function a walk calls, and the file declaring it.
type appCallee struct {
	owner *FileAnalysis
	fn    *ast.FuncDef
}

// appWalk is one body's walk: the reads written in it, in walk order, and
// the functions it calls.
type appWalk struct {
	sites []appReadSite
	calls []appCallee
	seen  map[ast.Node]bool
	// called de-duplicates calls.
	called map[*ast.FuncDef]bool
	// inspected holds every body this walk has inspected. Inspecting one
	// again adds no read and no call, and a lambda whose body calls the
	// function that holds it (`fn f(x) { |n| f(x)(n) }`) reaches its own
	// body again through that function's returned lambdas, without end.
	inspected map[ast.Node]bool
}

func newAppWalk() *appWalk {
	return &appWalk{seen: map[ast.Node]bool{}, called: map[*ast.FuncDef]bool{}, inspected: map[ast.Node]bool{}}
}

func (aw *appWalk) read(site appReadSite) {
	if aw.seen[site.node] {
		return
	}
	aw.seen[site.node] = true
	aw.sites = append(aw.sites, site)
}

func (aw *appWalk) call(owner *FileAnalysis, fn *ast.FuncDef) {
	if fn == nil || aw.called[fn] {
		return
	}
	aw.called[fn] = true
	aw.calls = append(aw.calls, appCallee{owner: owner, fn: fn})
}

func newAppReadWalker(fa *FileAnalysis) *appReadWalker {
	return &appReadWalker{fa: fa, summary: map[*ast.FuncDef]*appWalk{}, impls: map[string][]appCallee{}}
}

// reachable answers the reads reachable from body, which owner holds.
func (w *appReadWalker) reachable(owner *FileAnalysis, body ast.Node) []appReadSite {
	if body == nil {
		return nil
	}
	root := newAppWalk()
	w.inspect(root, owner, body)
	sites := append([]appReadSite(nil), root.sites...)
	seen := map[ast.Node]bool{}
	for _, s := range sites {
		seen[s.node] = true
	}
	visited := map[*ast.FuncDef]bool{}
	queue := append([]appCallee(nil), root.calls...)
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if visited[next.fn] {
			continue
		}
		visited[next.fn] = true
		walk := w.function(next)
		for _, s := range walk.sites {
			if !seen[s.node] {
				seen[s.node] = true
				sites = append(sites, s)
			}
		}
		queue = append(queue, walk.calls...)
	}
	return sites
}

// function answers a called function's own walk, taken once.
func (w *appReadWalker) function(c appCallee) *appWalk {
	if walk, ok := w.summary[c.fn]; ok {
		return walk
	}
	owner := c.owner
	if actual := w.fa.ScopedFunctionFiles[c.fn]; actual != nil {
		owner = actual
	}
	walk := newAppWalk()
	w.summary[c.fn] = walk
	w.inspect(walk, owner, c.fn.Body)
	return walk
}

// inspect walks the code a body executes. A closure or nested function
// declaration does not execute its body; calls enter those bodies through the
// function-reference graph.
func (w *appReadWalker) inspect(aw *appWalk, owner *FileAnalysis, node ast.Node) {
	if node == nil || aw.inspected[node] {
		return
	}
	aw.inspected[node] = true
	walkScopedExecution(node, func(n ast.Node) {
		if access, ok := n.(*ast.FieldAccess); ok {
			if read, ok := appReadOf(owner, access); ok {
				aw.read(appReadSite{read: read, file: owner, line: access.Field.Line, node: access})
			}
		}
		if invocation, ok := n.(*ast.Call); ok {
			w.invoke(aw, owner, invocation.Func, map[*Symbol]bool{})
			// A callback supplied to an executing function may be invoked
			// before it returns. Its requirements belong to this scope.
			for _, arg := range invocation.Args {
				w.invoke(aw, owner, arg, map[*Symbol]bool{})
			}
		}
	})
}

func (w *appReadWalker) resolve(owner *FileAnalysis, n ast.Node) *Symbol {
	var pos Pos
	switch v := n.(type) {
	case *ast.Ident:
		pos = Pos{Line: v.Line, Col: v.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: v.Line, Col: v.Col}
	case *ast.FieldAccess:
		if v.Field == nil {
			return nil
		}
		pos = Pos{Line: v.Field.Line, Col: v.Field.Col}
	default:
		return nil
	}
	return owner.References[pos]
}

// contained walks the lambdas, names and calls inside a node a call may hand
// back for its caller to run (a factory's body).
func (w *appReadWalker) contained(aw *appWalk, owner *FileAnalysis, node ast.Node, aliases map[*Symbol]bool) {
	WalkNodes(node, func(n ast.Node) {
		switch n.(type) {
		case *ast.Lambda, *ast.Ident, *ast.TypeIdent, *ast.Call:
			w.invoke(aw, owner, n, aliases)
		}
	})
}

// invoke follows what running expr may execute.
func (w *appReadWalker) invoke(aw *appWalk, owner *FileAnalysis, expr ast.Node, aliases map[*Symbol]bool) {
	if lambda, ok := expr.(*ast.Lambda); ok {
		w.inspect(aw, owner, lambda.Body)
		return
	}
	switch v := expr.(type) {
	case *ast.Call:
		factory := w.resolve(owner, v.Func)
		if factory != nil {
			for factory.Resolved != nil {
				factory = factory.Resolved
			}
			if fn, ok := factory.Node.(*ast.FuncDef); ok && factory.Kind == SymbolFunction && !aliases[factory] && !w.fa.BootFunctions[fn] {
				aliases[factory] = true
				if source := w.fa.ScopedFunctionFiles[fn]; source != nil {
					owner = source
				}
				w.contained(aw, owner, fn.Body, aliases)
			}
		}
		return
	case *ast.StructLit, *ast.ListLit, *ast.TupleLit:
		w.contained(aw, owner, expr, aliases)
		return
	}
	sym := w.resolve(owner, expr)
	if sym == nil || sym.Kind == SymbolField {
		if field, ok := expr.(*ast.FieldAccess); ok {
			w.invoke(aw, owner, field.Object, aliases)
		}
		return
	}
	if impl, ok := sym.DispatchImpl.(*ast.FuncDef); ok {
		if !w.fa.BootFunctions[impl] {
			aw.call(owner, impl)
		}
		return
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	if aliases[sym] {
		return
	}
	aliases[sym] = true
	if fn, ok := sym.Node.(*ast.FuncDef); ok && sym.Kind == SymbolFunction {
		if !w.fa.BootFunctions[fn] {
			aw.call(owner, fn)
		}
		return
	}
	if binding, ok := sym.Node.(*ast.Binding); ok {
		w.invoke(aw, owner, binding.Value, aliases)
		return
	}
	if sym.Kind == SymbolInterfaceMethod {
		if method, ok := sym.Node.(*ast.InterfaceMethod); ok && method.Body != nil {
			w.inspect(aw, owner, method.Body)
		}
		// Dynamic dispatch may choose any implementation of this method.
		for _, impl := range w.implementations(sym.Name, sym.OwningInterface) {
			aw.call(impl.owner, impl.fn)
		}
	}
}

// implementations answers every impl of an interface method in the project,
// ordered by file and position, found once per method.
func (w *appReadWalker) implementations(method, iface string) []appCallee {
	key := iface + "." + method
	if impls, ok := w.impls[key]; ok {
		return impls
	}
	var impls []appCallee
	for fn, file := range w.fa.ScopedFunctionFiles {
		k := strings.Split(file.ImplBlockInterfaceKey[fn], "<")[0]
		if fn.Name == method && (k == iface || strings.HasSuffix(k, "."+iface)) {
			impls = append(impls, appCallee{owner: file, fn: fn})
		}
	}
	sort.Slice(impls, func(i, j int) bool {
		a, b := impls[i], impls[j]
		if a.owner.FilePath != b.owner.FilePath {
			return a.owner.FilePath < b.owner.FilePath
		}
		if a.fn.Line != b.fn.Line {
			return a.fn.Line < b.fn.Line
		}
		return a.fn.Col < b.fn.Col
	})
	w.impls[key] = impls
	return impls
}

// walkScopedExecution visits the nodes a body executes. A closure or nested
// function declaration does not execute its body.
func walkScopedExecution(node ast.Node, visit func(ast.Node)) {
	seen := map[uintptr]bool{}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		if v.Kind() == reflect.Interface {
			if !v.IsNil() {
				walk(v.Elem())
			}
			return
		}
		if v.Kind() == reflect.Ptr {
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if v.CanInterface() {
				if n, ok := v.Interface().(ast.Node); ok {
					switch n.(type) {
					case *ast.FuncDef, *ast.Lambda:
						return
					}
					visit(n)
				}
			}
			walk(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(node))
}

// SameScopedType reports exact nominal identity; assignment compatibility
// (Any, bottom types, and enum embedding) does not make two application types
// one.
func SameScopedType(a, b Type) bool {
	if a == nil || b == nil {
		return false
	}
	switch x := a.(type) {
	case *PrimitiveType:
		y, ok := b.(*PrimitiveType)
		return ok && samePrimitive(x, y)
	case *StructType:
		y, ok := b.(*StructType)
		if !ok || x == nil || y == nil || x.Origin != y.Origin || x.Name != y.Name || len(x.TypeArgs) != len(y.TypeArgs) {
			return false
		}
		for i, t := range x.TypeArgs {
			if t.String() != y.TypeArgs[i].String() || !TypesEqual(t, y.TypeArgs[i]) {
				return false
			}
		}
		return true
	case *AnonStructType:
		_, ok := b.(*AnonStructType)
		return ok && TypesEqual(a, b)
	case *DistinctType:
		y, ok := b.(*DistinctType)
		return ok && x != nil && y != nil && x.Origin == y.Origin && x.Name == y.Name && x.String() == y.String()
	}
	return false
}
