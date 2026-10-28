package analysis

import (
	"math"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/rt"
)

// The compile-time half of the `infinite?` protocol (rt/seqendless.go).
//
// The runtime traps when a terminal that has to reach the end of its source
// (`Iter.to_list`, `Iter.count`, `String.join`, ...) receives a sequence
// whose Endless answer is set. This check reports the same thing as an
// error, at the terminal, when it can see the whole pipeline in the source:
// a std source that never ends, through std adapters that keep it endless,
// into such a terminal. It follows pipes, nested calls and local bindings
// whose value is such a pipeline, and nothing else: a parameter, a field, a
// `once`, a function's result and a user `impl Iter` keep the runtime trap.
//
// Every rule answers "not infinite" whenever the runtime might, since a
// wrong error rejects a program that finishes. The tables below restate the
// runtime's rules; vmhost's TestInfiniteIterCheckAgreesWithTheRuntime runs
// every entry, and every `Iter` owner function std declares, both ways and
// requires this check to report exactly what the runtime traps on.

// InfiniteIterCode tags the error, so the language server can offer to
// limit the pipeline with `Iter.take`.
const InfiniteIterCode = "infinite-iterator"

// iterArg is one argument of a std function: its position counted with the
// piped value as 0, and its parameter name for a named argument.
type iterArg struct {
	idx  int
	name string
}

// iterDrain is a terminal that has to reach the end of its source. A
// non-nil callback names the argument whose `break` or `continue` lets the
// terminal stop early, in which case it does not have to reach the end.
type iterDrain struct {
	source   iterArg
	callback *iterArg
}

var (
	iterSourceArg   = iterArg{0, "source"}
	iterCallbackArg = &iterArg{1, "f"}
)

// infiniteIterTerminals is every std function the VM refuses to run on an
// endless source (internal/vm's mustReachEnd, and the std functions whose
// bodies end in one of those operations).
var infiniteIterTerminals = map[string]iterDrain{
	"Iter.to_list":         {source: iterSourceArg},
	"Iter.to_vector":       {source: iterSourceArg},
	"Iter.to_set":          {source: iterSourceArg},
	"Iter.to_map":          {source: iterSourceArg},
	"Iter.count":           {source: iterSourceArg},
	"Iter.last":            {source: iterSourceArg},
	"Iter.reverse":         {source: iterSourceArg},
	"Iter.flatten":         {source: iterSourceArg},
	"Iter.transpose":       {source: iterSourceArg},
	"Iter.permutations":    {source: iterArg{0, "items"}},
	"Iter.combinations":    {source: iterArg{0, "items"}},
	"Iter.sort":            {source: iterSourceArg},
	"Iter.sort_by":         {source: iterSourceArg},
	"Iter.sort_with":       {source: iterSourceArg},
	"Iter.partition":       {source: iterSourceArg},
	"Iter.group_by":        {source: iterSourceArg},
	"Iter.frequencies":     {source: iterSourceArg},
	"Iter.min":             {source: iterSourceArg},
	"Iter.max":             {source: iterSourceArg},
	"Iter.min_by":          {source: iterSourceArg},
	"Iter.max_by":          {source: iterSourceArg},
	"Iter.sum":             {source: iterSourceArg},
	"Iter.product":         {source: iterSourceArg},
	"Iter.reduce":          {source: iterSourceArg, callback: iterCallbackArg},
	"Iter.each":            {source: iterSourceArg, callback: iterCallbackArg},
	"Iter.map_reduce":      {source: iterSourceArg, callback: iterCallbackArg},
	"String.join":          {source: iterArg{0, "parts"}},
	"Maybe.values":         {source: iterArg{0, "maybes"}},
	"Result.partition":     {source: iterArg{0, "results"}},
	"Task.spawn_all":       {source: iterSourceArg},
	"Supervisor.spawn_all": {source: iterArg{1, "source"}},
}

// infiniteIterAdapter is how a std adapter answers from its arguments.
type infiniteIterAdapter int

const (
	// iterKeeps is endless when its source is, unless its callback (if
	// any) can stop it.
	iterKeeps infiniteIterAdapter = iota
	// iterChunks is endless when its source is and its size is a literal
	// above zero (chunks, windows).
	iterChunks
	// iterCycles is endless when its source is, or when the source is a
	// literal collection with an element.
	iterCycles
	// iterEither is endless when either argument is (concat).
	iterEither
	// iterBoth is endless when both arguments are (zip).
	iterBoth
)

// iterAdapterRule is one adapter's rule, with the callback argument that
// can stop it, if it takes one.
type iterAdapterRule struct {
	rule     infiniteIterAdapter
	callback *iterArg
}

// infiniteIterAdapters is every std adapter that carries its source's
// answer (rt's Seq constructors that set Endless from their upstream's).
// Every other function, `Iter.take` and `Iter.take_while` among them, ends
// the analysis.
var infiniteIterAdapters = map[string]iterAdapterRule{
	"Iter.map":        {iterKeeps, iterCallbackArg},
	"Iter.filter":     {iterKeeps, &iterArg{1, "pred"}},
	"Iter.drop":       {iterKeeps, nil},
	"Iter.drop_while": {iterKeeps, &iterArg{1, "pred"}},
	"Iter.with_index": {iterKeeps, nil},
	"Iter.scan":       {iterKeeps, iterCallbackArg},
	"Iter.chunk_by":   {iterKeeps, &iterArg{1, "key_fn"}},
	"Iter.flat_map":   {iterKeeps, iterCallbackArg},
	"Iter.chunks":     {iterChunks, nil},
	"Iter.windows":    {iterChunks, nil},
	"Iter.cycle":      {iterCycles, nil},
	"Iter.concat":     {iterEither, nil},
	"Iter.zip":        {iterBoth, nil},
}

// InfiniteIterTables lists the names of the std functions the check knows,
// for the test that holds each to the runtime: the terminals that must
// reach the end, the adapters that carry endlessness, and the sources that
// never end.
func InfiniteIterTables() (terminals, adapters, sources []string) {
	for name := range infiniteIterTerminals {
		terminals = append(terminals, name)
	}
	for name := range infiniteIterAdapters {
		adapters = append(adapters, name)
	}
	sources = []string{"Iter.repeat", "Iter.from", "Iter.iterate", "Range.from", "Range.naturals", "Range.step_by"}
	return terminals, adapters, sources
}

// rangeEndlessMin is rt's: an unbounded Int range that starts within this
// many steps of `Int.max_value` ends.
const rangeEndlessMin = 1 << 48

// infiniteIterCheck walks a file's bodies for terminals that drain a source
// it can see never ends.
type infiniteIterCheck struct {
	s *iterSensitivity
	// piped marks each call that is the right side of a `|>`, checked with
	// its piped value when the pipe is reached.
	piped  map[*ast.Call]bool
	errors []TypeError
}

// endlessAt is a sequence known to never end: the name the runtime gives
// its source and where that source is written.
type endlessAt struct {
	name string
	at   ast.Node
}

func (s *iterSensitivity) checkInfiniteIters(nodes []ast.Node) {
	c := &infiniteIterCheck{s: s, piped: map[*ast.Call]bool{}}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Binary:
				if call, ok := node.Right.(*ast.Call); ok && node.Op == "|>" {
					c.piped[call] = true
					c.terminal(call, node.Left)
				}
			case *ast.Call:
				if !c.piped[node] {
					c.terminal(node, nil)
				}
			}
			return true
		})
	}
	s.errors = append(s.errors, c.errors...)
}

// terminal reports call when it is a terminal that must reach the end and
// its source never ends.
func (c *infiniteIterCheck) terminal(call *ast.Call, piped ast.Node) {
	key := c.s.stdFunctionKey(call)
	drain, ok := infiniteIterTerminals[key]
	if !ok {
		return
	}
	args := iterCallArgs(call, piped)
	if drain.callback != nil && c.mayStop(args.at(*drain.callback)) {
		return
	}
	src := c.endless(args.at(drain.source), 0)
	if src == nil {
		return
	}
	line, col, endCol := calleeSpan(call)
	srcLine, srcCol := positionOfCall(src.at)
	c.errors = append(c.errors, TypeError{
		Line:    line,
		Col:     col,
		EndLine: line,
		EndCol:  endCol,
		Message: rt.InfiniteIterationText(key, src.name),
		Related: []RelatedInfo{{Line: srcLine, Col: srcCol, Message: "`" + src.name + "` never ends"}},
		Code:    InfiniteIterCode,
	})
}

// iterArgs is a call's arguments with the piped value first.
type iterArgs []ast.Node

func iterCallArgs(call *ast.Call, piped ast.Node) iterArgs {
	if piped == nil {
		return call.Args
	}
	return append(iterArgs{piped}, call.Args...)
}

// at is the argument a names: the named argument of its name, else the
// positional argument at its index. nil when there is none.
func (args iterArgs) at(a iterArg) ast.Node {
	pos := 0
	for _, arg := range args {
		if named, ok := arg.(*ast.NamedArg); ok {
			if named.Name == a.name {
				return named.Value
			}
			continue
		}
		if pos == a.idx {
			return arg
		}
		pos++
	}
	return nil
}

// maxIterBindingDepth bounds how many local bindings a source is followed
// through.
const maxIterBindingDepth = 64

// endless is the source n never ends by, or nil when n may end or the
// check cannot tell.
func (c *infiniteIterCheck) endless(n ast.Node, depth int) *endlessAt {
	if depth > maxIterBindingDepth {
		return nil
	}
	switch node := n.(type) {
	case *ast.GroupedExpr:
		return c.endless(node.Expr, depth)
	case *ast.Ident:
		sym := c.s.fa.References[Pos{Line: node.Line, Col: node.Col}]
		if sym == nil || sym.Kind != SymbolBinding {
			return nil
		}
		if b, ok := sym.Node.(*ast.Binding); ok && b.Value != nil {
			return c.endless(b.Value, depth+1)
		}
	case *ast.Binary:
		if call, ok := node.Right.(*ast.Call); ok && node.Op == "|>" {
			return c.endlessCall(call, node.Left, depth)
		}
	case *ast.Call:
		return c.endlessCall(node, nil, depth)
	}
	return nil
}

func (c *infiniteIterCheck) endlessCall(call *ast.Call, piped ast.Node, depth int) *endlessAt {
	key := c.s.stdFunctionKey(call)
	args := iterCallArgs(call, piped)
	switch key {
	case "Iter.repeat", "Iter.from":
		return &endlessAt{key, call}
	case "Iter.iterate":
		if c.mayStop(args.at(iterArg{1, "step"})) {
			return nil
		}
		return &endlessAt{key, call}
	case "Range.from":
		// rt's rangeEndless: a range that starts within rangeEndlessMin of
		// `Int.max_value` ends, and any other, a negative start included,
		// never does.
		if n, ok := intLiteral(args.at(iterArg{0, "n"})); ok && uint64(math.MaxInt64)-uint64(n) >= rangeEndlessMin {
			return &endlessAt{"Range.from", call}
		}
		return nil
	case "Range.naturals":
		return &endlessAt{"Range.naturals", call}
	case "Range.step_by":
		return c.endlessStepBy(args, depth)
	}
	rule, ok := infiniteIterAdapters[key]
	if !ok {
		return nil
	}
	if rule.callback != nil && c.mayStop(args.at(*rule.callback)) {
		return nil
	}
	switch rule.rule {
	case iterKeeps:
		return c.endless(args.at(iterSourceArg), depth)
	case iterChunks:
		if n, ok := intLiteral(args.at(iterArg{1, "size"})); ok && n > 0 {
			return c.endless(args.at(iterSourceArg), depth)
		}
	case iterCycles:
		src := args.at(iterSourceArg)
		if e := c.endless(src, depth); e != nil {
			return e
		}
		if nonEmptyCountedLiteral(src) {
			return &endlessAt{"Iter.cycle", call}
		}
	case iterEither:
		if e := c.endless(args.at(iterArg{0, "a"}), depth); e != nil {
			return e
		}
		return c.endless(args.at(iterArg{1, "b"}), depth)
	case iterBoth:
		a := c.endless(args.at(iterArg{0, "a"}), depth)
		if a != nil && c.endless(args.at(iterArg{1, "b"}), depth) != nil {
			return a
		}
	}
	return nil
}

// endlessStepBy is `Range.step_by(r, by)` over an unbounded Range the check
// can see, with a literal step. rt's stepByEndless calls it endless unless
// the step does not move or the range reaches the end of Int within
// rangeEndlessMin steps; a start and a step this small leave more room than
// that in either direction.
func (c *infiniteIterCheck) endlessStepBy(args iterArgs, depth int) *endlessAt {
	r := c.endless(args.at(iterArg{0, "r"}), depth)
	if r == nil || (r.name != "Range.from" && r.name != "Range.naturals") {
		return nil
	}
	start := int64(0)
	if call, ok := r.at.(*ast.Call); ok && c.s.stdFunctionKey(call) == "Range.from" {
		if n, ok := intLiteral(iterArgs(call.Args).at(iterArg{0, "n"})); ok {
			start = n
		} else {
			// Piped into Range.from: its start is the piped value.
			return nil
		}
	}
	by, ok := intLiteral(args.at(iterArg{1, "by"}))
	if !ok || by == 0 || by > 1<<10 || by < -(1<<10) || start > 1<<60 || start < -(1<<60) {
		return nil
	}
	return &endlessAt{"Range.step_by", r.at}
}

// intLiteral is n's value when n is an Int literal, negated or not.
func intLiteral(n ast.Node) (int64, bool) {
	switch node := n.(type) {
	case *ast.IntLit:
		return node.Value, true
	case *ast.GroupedExpr:
		return intLiteral(node.Expr)
	case *ast.Unary:
		if node.Op == "-" {
			if v, ok := node.Right.(*ast.IntLit); ok && v.Value != math.MinInt64 {
				return -v.Value, true
			}
		}
	}
	return 0, false
}

// nonEmptyCountedLiteral reports whether n is a collection literal with an
// element whose iterator knows its count, so its cycle never ends.
func nonEmptyCountedLiteral(n ast.Node) bool {
	switch node := n.(type) {
	case *ast.GroupedExpr:
		return nonEmptyCountedLiteral(node.Expr)
	case *ast.ListLit:
		return node.TypeName == nil && len(node.Items) > 0
	case *ast.VectorLit:
		return node.TypeName == nil && node.Spread == nil && len(node.Items) > 0
	case *ast.SetLit:
		return node.TypeName == nil && node.Spread == nil && len(node.Items) > 0
	case *ast.MapLit:
		return node.TypeName == nil && node.Spread == nil && len(node.Entries) > 0
	}
	return false
}

// mayStop reports whether a callback can stop the sequence that runs it: a
// lambda or capture with `break` or `continue` in it, or one that calls an
// iter-sensitive function, or such a function itself. Anything
// the check cannot see into may stop it. A missing argument cannot.
func (c *infiniteIterCheck) mayStop(cb ast.Node) bool {
	switch node := cb.(type) {
	case nil:
		return false
	case *ast.GroupedExpr:
		return c.mayStop(node.Expr)
	case *ast.Lambda, *ast.Capture:
		stops := false
		ast.Inspect(node, func(n ast.Node) bool {
			switch inner := n.(type) {
			case *ast.Break, *ast.Continue:
				stops = true
			case *ast.FuncDef:
				return false
			case *ast.Ident, *ast.FieldAccess:
				if c.s.sensitiveFunc(inner) != nil {
					stops = true
				}
			}
			return !stops
		})
		return stops
	case *ast.Ident, *ast.FieldAccess:
		sym := c.s.resolveIterCallbackSymbol(node)
		if sym == nil || sym.Kind != SymbolFunction {
			return true
		}
		return c.s.sensitiveFunc(node) != nil
	}
	return true
}

// stdFunctionKey is "Owner.name" when call's callee is one of the std
// functions this check knows, written owner-qualified or reached through
// an alias or a selective import, and "" otherwise.
func (s *iterSensitivity) stdFunctionKey(call *ast.Call) string {
	sym := s.resolveIterCallbackSymbol(call.Func)
	if sym == nil || sym.Kind != SymbolFunction {
		return ""
	}
	key := sym.OwningType + "." + sym.Name
	module, ok := infiniteIterOwners[sym.OwningType]
	if !ok {
		return ""
	}
	if _, known := infiniteIterTerminals[key]; !known {
		if _, known := infiniteIterAdapters[key]; !known {
			switch key {
			case "Iter.repeat", "Iter.from", "Iter.iterate", "Range.from", "Range.naturals", "Range.step_by":
			default:
				return ""
			}
		}
	}
	if !isStdOwnerFunction(s.fa, sym, sym.OwningType, module) {
		return ""
	}
	return key
}

// infiniteIterOwners maps each owner whose functions the check knows to the
// std module that declares it.
var infiniteIterOwners = map[string]string{
	"Iter":       "iter",
	"Range":      "ranges",
	"String":     "strings",
	"Maybe":      "maybe",
	"Result":     "results",
	"Task":       "tasks",
	"Supervisor": "supervisors",
}

// calleeSpan is where call's callee is written: its line, its first column
// and the column one past its last.
func calleeSpan(call *ast.Call) (line, col, endCol int) {
	switch fn := call.Func.(type) {
	case *ast.FieldAccess:
		// A FieldAccess's own position is its dot; the callee starts at
		// its owner.
		line, col := fn.Line, fn.Col
		switch obj := fn.Object.(type) {
		case *ast.Ident:
			line, col = obj.Line, obj.Col
		case *ast.TypeIdent:
			line, col = obj.Line, obj.Col
		}
		if fn.Field != nil {
			return line, col, fn.Field.Col + len(fn.Field.Name)
		}
		return line, col, 0
	case *ast.Ident:
		return fn.Line, fn.Col, fn.Col + len(fn.Name)
	}
	return call.Line, call.Col, 0
}

// positionOfCall is where n's callee is written when n is a call.
func positionOfCall(n ast.Node) (int, int) {
	if call, ok := n.(*ast.Call); ok {
		line, col, _ := calleeSpan(call)
		return line, col
	}
	return positionOf(n)
}
