package irbuild

// The retained shape's builder. It lowers a function body into an `ir.Func`,
// which the VM runs.
//
// Tail branches write the function result. Branch-valued bindings declare
// a separate typed Slot using the checker's resolved binding type and verify
// both arms against it. Their joins can continue with bindings, effects or
// further branches.

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irScalarSide is what the builder records about one temporary while it
// builds a body: the temporary's kind, and the facts a later lowering in the
// same body reads back to decide what to build. Keyed by temporary.
type irScalarSide struct {
	k kind
	// lambda is the plan of the closure an `ir.FuncValue` builds, which a
	// later call through a local alias reads its defaults from
	// (callablePlan).
	lambda *irLambdaPlan
	// copy is the role of one `ir.Copy`. It decides whether a later reader
	// treats the copied value as already held (irHeldValue), as effect-free
	// (effectFree), or as an alias callablePlan may look through.
	copy irCopyRole
	// copyPure marks an injected pipe value whose source was itself held.
	copyPure bool
	// pureMake marks an `ir.Make` whose operands are all effect-free.
	pureMake bool
	// deferrable marks an `ir.Call` a `defer` may register: a call to a
	// function that answers a value.
	deferrable bool
	// ctl is the signalling form of the iter-sensitive named function an
	// `ir.Ref` names (irNamedCtlForm), or irCtlNone.
	ctl irCtlForm
}

// irScalarBuilder lowers one function body into an `ir.Func` and EMITS
// NOTHING.
//
// A declined build consumes no identifiers from the gen's namespace.
// The builder reads resolved declarations and may intern type/symbol identities;
// consumer-side spelling is recorded for the read-back. In particular,
// consuming gen.uniq during a failed attempt would renumber later temporaries.
type irScalarBuilder struct {
	// placed holds the operands a named-argument qualified call already
	// evaluated, by the synthetic node that stands for each (irnamedarg.go).
	placed      map[ast.Node]irPlacedArg
	inferReturn *resultInference
	returnKind  kind
	// defaultScope excludes caller locals while lowering callee defaults.
	defaultScope bool
	// deferOK admits `defer` and scoped block statements: only a named
	// function's own body retains them. deferScope is the scope the next
	// `leading` run registers deferred calls in, and defers counts the ids
	// handed out. See irdefer.go.
	deferOK    bool
	deferScope *irDeferScope
	defers     int
	// withScope is the scope the next `leading` run lowers its `with`
	// statements in, consumed as deferScope is; testWithScope is a test
	// body's, set for the block or arm being lowered as testDeferScope is.
	// See irdefer.go's irWithScope.
	withScope     *irWithScope
	testWithScope *irWithScope
	// casePrefix is, for the block a `case` tests its arms in, how many of
	// its instructions precede the tests: the scrutinee and its hold. A
	// `case` statement's discarded result slot is inserted there
	// (inferredRegionValue).
	casePrefix map[*ir.Block]int
	parent     *irScalarBuilder
	// synthesized marks a derive-synthesized declaration. Its graph is
	// retained for the VM with synthesized positions and never read back
	// into Go.
	synthesized bool
	// literalCell is the backtick typed literal whose lazy cell
	// initializer this builder lowers: there the literal is its handler's
	// call itself (irliteralcell.go).
	literalCell *ast.TaggedString
	captures    []ir.Temp
	captured    map[string]irLambdaCapture
	g           *gen
	sh          *irFuncShell
	f           *ir.Func
	b           *ir.Block
	sides       []irScalarSide
	// bound is the temporary each name this body BINDS already lives in, and
	// boundK its kind. A read of a bound name is not a fresh `ir.Ref`: after
	// `y := v` the value IS temporary tN, which is exactly what `irbind.go`'s
	// `irName` models. So the read reuses the Bind's destination and the
	// def-use chain crosses the statement boundary.
	bound  map[string]ir.Temp
	boundK map[string]kind
	// inTest marks a body this builder is retaining for a `test` declaration.
	// It gates four widenings; irtestbody.go's header names each and the
	// reason it is safe only here.
	inTest bool
	// testApp is set for a test body whose group chain starts an app: the
	// VM runner boots it before the body, so an app-field read has an app to
	// read. See irtestgroup.go.
	testApp bool
	// testWalked is set for a test body the builder never reads back: one
	// built for the VM only, such as a case under a `tests` group. Gates that
	// that once kept a test body narrow do not apply
	// to it.
	testWalked bool
	// qualRecord is the qualified call recordedQualCall is lowering inside a
	// grouped case's assertion subject, and qualRecordArgs the operands
	// irQualLowerArgs lowered for it, so its `values:` rows can be recorded
	// after it.
	qualRecord     *ast.Call
	qualRecordArgs irQualArgs
	qualRecordSeen bool
	// qualBare types the bare payload-free prelude variants among a
	// qualified call's operands, by position (kindInvalid for any other
	// operand); see containerEqualCall.
	qualBare []kind
	// stdInstPreCall is a qualified call whose operands irQualLowerArgs has
	// already lowered into stdInstPreArgs, so stdInstCall reuses them rather
	// than lowering them a second time.
	stdInstPreCall *ast.Call
	stdInstPreArgs irQualArgs
	// Reduce seeds belong to these exact callback nodes, not ordinary defaults.
	reduceSeeds map[*ast.Lambda]bool
	// partialOpen types and names the parameters of the lambda a partial
	// application lowers to, one per open slot (see partialApplication).
	partialOpen map[*ast.Lambda]partialSlots
	// ctlCallbacks marks the callbacks an enclosing Iter call widens to its
	// signalling form, before its arguments are lowered; ctl is that form on
	// the callback's own builder, and ctlAcc a reduce callback's accumulator.
	ctlCallbacks map[*ast.Lambda]irCtlForm
	// ctlRefs marks the named-function arguments an enclosing Iter call
	// drives in the signalling form their own bodies were lowered under
	// (irNamedCtlForm). A reference to such a function anywhere else
	// declines, because nothing there reads its control answer.
	ctlRefs map[ast.Node]irCtlForm
	ctl     irCtlForm
	ctlAcc  ir.Temp
	// ctlSeed is a signalling reduce's seed, lowered before its callback.
	ctlSeed ir.Temp
	// recording is how deep inside an assertion subject the builder is:
	// non-zero means an operator's operands are the failure report and
	// `bl.record` builds their rows. A counter rather than a bool because a
	// subject nests, a comparison inside an `and` inside a subject, and a
	// lambda boundary saves and restores it.
	recording int
	// pipedCall is the synthetic call node a pipe stage desugared to, or nil.
	// Node IDENTITY
	// rather than a bool, because a different call
	// nested inside a piped stage's argument must still record its rows. See
	// irpipe.go and `bl.call`.
	pipedCall *ast.Call
	// pipedFrom is the pipe pipedCall was spliced from, which carries the
	// checker's type for the stage (a spliced call has none of its own).
	pipedFrom *ast.Binary
	// pipedCase is the spliced `case` a `x |> case { ... }` stage lowered
	// to, whose scrutinee records no row. See irpipe.go.
	pipedCase *ast.Case
	// witnessRow is set when a `values:` row would show a `Type<T>` witness,
	// which renders by the spelling it was written with; the
	// assertion declines rather than print another spelling.
	witnessRow bool
	// testDefs is each ordinary binding a test body introduced, by name: the
	// initializer a later bare-name subject's "defined as:" block renders.
	testDefs map[string]*ast.Binding
	// testStages is the `pipeline values:` block of a pipe-valued test
	// binding a later assertion names bare, by name; testAfter is the test
	// body's statements after the one being lowered, which decides that.
	testStages map[string][]ir.AssertStage
	testAfter  []ast.Node
	// loopsOK admits inline `Iter.loop` in a named function body, and loops
	// counts the ones built. A body whose loop the structured reader cannot
	// spell declines.
	loopsOK bool
	loops   int
	// boot marks the program boot's own body. It admits the Context kind
	// for a parameter read and a struct field, and is never read back.
	boot bool
	// vmOnly marks a std arity wrapper's body, which only the VM reads. It
	// admits a std enum field's Go-stated default as its constant, as every
	// body the test read-back does not spell does (`Backoff.Exponential{}`
	// in a program).
	vmOnly bool
	// body is the named function's own block, the one scope whose app-field
	// writes are retained; stores counts them. See irappboot.go.
	body   *ast.Block
	stores int
	// loopArms is the loop whose case tail is being lowered: its arms may
	// `break v`. Nested regions inside an arm do not see it.
	loopArms *irLoopBuild
	// concTries collects the tries of a `concurrent` body or a lambda, whose
	// boundary is the body's kind once it settles. Nil outside such a body.
	// tryBoundary is the checker's name for that boundary: "concurrent",
	// "lambda", or "fn <name>" for a nested fn lowered as a lambda.
	concTries   *[]irPendingTry
	tryBoundary string
	// nestedFn names the nested fn whose lambda this builder is about to
	// build (nestedFunc), or is empty: that lambda's tries leave the fn,
	// which the checker spells "fn <name>".
	nestedFn string
	// genericNested are the generic fns declared in this body so far, by
	// name, each with the names its instances are bound under; a call
	// reaches one through genericNestedCall.
	genericNested map[string]*irGenericNested
	// testDeferScope is the scope a test-body `defer` registers in: the
	// body's, or the enclosing block's or arm's.
	testDeferScope *irDeferScope
	// testTail is set while a test body lowers its value position: its final
	// statement, and the final statement of each arm or block that final
	// statement is. A value there that is an `Err(AssertionFailure)` is the
	// case's verdict (testVerdict).
	testTail bool
	// setupExit is set while a group's `setup` that holds a `return` is
	// lowered: where its value goes and where control leaves it
	// (irtestsetup.go).
	setupExit *irSetupExit
	// discarded is the expression a statement is lowering only for its
	// effect (`e` as a statement, `_ = e`), or nil. An `Iter.loop` whose value
	// is discarded admits a bare `break`, whose Unit answer nothing reads.
	discarded ast.Node
	// recursive is the recursive nested fn whose lambda this builder is
	// about to build (irnestedfn.go); nil otherwise.
	recursive *irRecursiveFn
}

// irSideSet records one consumer-side fact at t, growing the list.
//
// A FUNCTION OVER THE SLICE rather than a method on the builder, because two
// producers fill one list: `irScalarBuilder` for the body and `irProjExpr`
// for the destructuring prologue, which runs BEFORE a builder exists. One
// growth rule so the two cannot disagree about the indexing.
func irSideSet(sides *[]irScalarSide, t ir.Temp, s irScalarSide) {
	for int(t) >= len(*sides) {
		*sides = append(*sides, irScalarSide{})
	}
	(*sides)[t] = s
}

func (bl *irScalarBuilder) side(t ir.Temp, s irScalarSide) {
	bl.g.irTypeTemp(bl.f, t, s.k)
	irSideSet(&bl.sides, t, s)
}

// lowerNode appends the instructions computing n and answers the temporary
// they write, its kind, and whether the value is MOBILE: whether a later
// operand's instructions can change it, which decides whether a non-final
// operand is forced into a Copy (forceCallOperand).
//
// CALLED THROUGH `bl.lower`, which is one frame in irdecline.go: every decline
// arm below has to be classified for the first-decline census, and a
// hand-placed hook at each of the ten would miss the one nobody remembered.
// Every recursive call in this file goes through the wrapper, which is what
// makes the INNERMOST decline the reason a body is filed under.
func (bl *irScalarBuilder) lowerNode(n ast.Node) (ir.Temp, kind, bool, bool) {
	if isNilNode(n) {
		return ir.NoTemp, kindInvalid, false, false
	}
	if p, placed := bl.placed[n]; placed {
		return p.temp, p.k, true, true
	}
	line, col := nodePos(n)
	if !bl.positionOK(line) {
		// A node on a synthesized line has no source position a consumer
		// can report, and every IR node needs one.
		//
		// No corpus or std node reaches this: derive synthesis produces whole
		// synthesized bodies and `irScalarBuild` checks every statement's
		// line before reaching here. It is a fence.
		//
		// A node's line may differ from its statement's (a triple-quoted
		// literal's hole, a pipe chain written one stage per line). That is
		// recorded as a span rather than declined; see irspan.go and `ir.Pos`.
		return ir.NoTemp, kindInvalid, false, false
	}
	switch t := n.(type) {
	case *ast.TryOp:
		return bl.tryValue(t, t.Expr, t)
	case *ast.Assertion:
		return bl.assertionExpr(t)
	case *ast.If, *ast.Case:
		return bl.inferredRegionValue(n, false)
	case *ast.Block:
		return bl.blockValue(t)
	case *ast.With:
		irDeclineNote("a `with` statement in an operand position")
		return ir.NoTemp, kindInvalid, false, false
	case *ast.DotVariant:
		if t.ResolvedEnum != "" {
			// kindInvalid: no expected type here; the checker's type of
			// the dot is the prelude instance.
			if v, k, mobile, ok := bl.preludeBareValue(t, kindInvalid); ok {
				return v, k, mobile, true
			}
			return bl.variantBare(bl.g.qualifiedDot(t))
		}
		return ir.NoTemp, kindInvalid, false, false
	case *ast.GroupedExpr:
		// It contributes no instruction. Parentheses are grammar.
		return bl.lower(t.Expr)

	case *ast.IntLit:
		c := ir.NewInt(bl.g.irPos(line, col), bl.f.NewTemp(), t.Value)
		bl.b.Append(c)
		return c.Dst(), kindInt, true, true

	case *ast.FloatLit:
		c := ir.NewFloat(bl.g.irPos(line, col), bl.f.NewTemp(), t.Value)
		bl.b.Append(c)
		return c.Dst(), kindFloat, true, true
	case *ast.DecimalLit:
		return bl.decimalLiteral(t)

	case *ast.CodepointLit:
		return bl.codepointLiteral(t)

	case *ast.StringLit:
		// The parser supplies decoded text for every string spelling; both
		// consumers use the same constant node and value.
		c := ir.NewString(bl.g.irPos(line, col), bl.f.NewTemp(), t.Value)
		bl.b.Append(c)
		return c.Dst(), kindString, true, true

	case *ast.TypeIdent:
		// `true` and `false` are not keywords in Nomi — `True` and `False`
		// are the variants of the prelude's Bool enum, resolved by name.
		//
		// Unit is the reserved zero-sized builtin. No nominal guard is
		// needed: the checker RESERVES the name, so no module can bind it to anything
		// else. See irdiscard.go for the statement form that needs it.
		v := false
		switch t.Name {
		case "True":
			v = true
		case "False":
		case "Unit":
			c := ir.NewUnit(bl.g.irPos(line, col), bl.f.NewTemp())
			bl.b.Append(c)
			// MOBILE, from the class rather than from this name:
			// `irConstExpr` answers `pure: true` for every constant,
			// because a constant reads no temporary and runs nothing.
			return c.Dst(), kindUnit, true, true
		default:
			if dst, k, mobile, ok := bl.markerValue(t, t.Name); ok {
				return dst, k, mobile, true
			}
			if d, variant, ok := bl.g.stdEnumVariantAt(t.Name, t.Line, t.Col); ok && variant.kind == "bare" && irRetainedEnumKind(d) {
				return bl.variantValue(t, d, variant, nil, true)
			}
			// A bare payload-free prelude variant with no expected type
			// (`None |> Debug.inspect()`) takes the checker's instance, or
			// Unit's where nothing constrains it; see bareVariantOwnKind.
			if k, ok := bl.bareVariantOwnKind(t); ok {
				return bl.preludeBareValue(t, k)
			}
			// `Some`, `Ok`, or a distinct's `Id`, named as a function
			// value (ownerfuncref.go).
			return bl.ctorFuncRef(t)
		}
		c := ir.NewBool(bl.g.irPos(line, col), bl.f.NewTemp(), v)
		bl.b.Append(c)
		return c.Dst(), kindBool, true, true

	case *ast.Lambda:
		return bl.lambda(t)
	case *ast.FieldAccessor:
		return bl.fieldAccessor(t)
	case *ast.ConcurrentBlock:
		return bl.concurrent(t)

	case *ast.Ident:
		v, k, mobile, ok := bl.identRead(t, line, col)
		if ok {
			v, k, mobile, ok = bl.narrowedRead(t, v, k, mobile)
		}
		return v, k, mobile, ok

	case *ast.StringInterp:
		return bl.interp(t)

	case *ast.Unary:
		if t.Op != "-" && t.Op != "!" {
			return ir.NoTemp, kindInvalid, false, false
		}
		// A unary operator never hoists its operand into a `vN :=` temporary.
		src, k, _, ok := bl.lower(t.Right)
		if !ok {
			return ir.NoTemp, kindInvalid, false, false
		}
		if t.Op == "!" {
			if k != kindBool {
				return ir.NoTemp, kindInvalid, false, false
			}
			n := ir.NewNot(bl.g.irNodePos(t), bl.f.NewTemp(), src)
			bl.b.Append(n)
			return n.Dst(), kindBool, false, true
		}
		ak, modelled := bl.g.irArithKind(ir.OpNeg, k, false)
		if !modelled {
			return ir.NoTemp, kindInvalid, false, false
		}
		a := ir.NewUnaryArith(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), ir.OpNeg, ak, src)
		bl.b.Append(a)
		return a.Dst(), k, false, true

	case *ast.Binary:
		return bl.binary(t)

	case *ast.MapLit:
		return bl.mapMake(t)
	case *ast.VectorLit:
		return bl.vectorMake(t)
	case *ast.SetLit:
		return bl.setMake(t)
	case *ast.RangeLit:
		return bl.rangeMake(t)
	case *ast.TaggedString:
		return bl.taggedLiteral(t)
	case *ast.ListLit:
		if t.TypeName != nil {
			anon := *t
			anon.TypeName = nil
			return bl.attachedVariant(t, t.TypeName, &anon)
		}
		return bl.listMake(t, t.Items, nil)
	case *ast.ListSpreadLit:
		return bl.listMake(t, t.Heads, t.TailSpread)
	case *ast.TupleLit:
		return bl.tupleMake(t)

	case *ast.StructLit:
		// Declared structs and anonymous records share the AST node; the
		// builder selects the matching construction from its TypeName.
		return bl.structMake(t)

	case *ast.FieldAccess:
		if read, ok := bl.g.appRead(t); ok {
			return bl.appFieldRead(t, read)
		}
		if v, k, mobile, ok := bl.qualifiedOnceValue(t); ok {
			return v, k, mobile, true
		}
		if v, k, mobile, ok := bl.ownerOnceValue(t); ok {
			return v, k, mobile, true
		}
		if index, ok := irTupleIndex(t); ok {
			return bl.tupleRead(t, index)
		}
		if _, typeOwner := t.Object.(*ast.TypeIdent); typeOwner {
			if dst, k, mobile, ok := bl.variantBare(t); ok {
				return dst, k, mobile, true
			}
			if dst, k, mobile, ok := bl.stdOnceValue(t); ok {
				return dst, k, mobile, true
			}
			// `Maybe.None` with no expected type, as the bare `None`
			// arm above types it; see bareVariantOwnKind.
			if k, ok := bl.bareVariantOwnKind(t); ok {
				return bl.preludeBareValue(t, k)
			}
			if dst, k, mobile, ok := bl.stdMethodRef(t); ok {
				return dst, k, mobile, true
			}
			// `Todo.render`, `Display.to_string`: any other owner's
			// function as a value (ownerfuncref.go).
			return bl.ownerFuncRef(t)
		}
		if _, dotted := bl.g.dottedTypeQualifier(t.Object); dotted {
			// `Probe.Reading.Steady`: a variant of a namespaced enum, or of
			// one named through a whole-file import.
			if dst, k, mobile, ok := bl.variantBare(t); ok {
				return dst, k, mobile, true
			}
			// `leaf.Box.twice`, `leaf.Shape.Line`: the owner's function or
			// constructor as a value, as in the TypeIdent arm above.
			return bl.ownerFuncRef(t)
		}
		if bl.stdQualifiedOwnerDef(t.Object) != nil {
			// `json.Json.Null`: a std enum's variant through its module's
			// qualifier.
			if dst, k, mobile, ok := bl.variantBare(t); ok {
				return dst, k, mobile, true
			}
			// `json.Json.Str`: a std owner's constructor or function as a
			// value.
			return bl.ownerFuncRef(t)
		}
		if _, mod, ok := bl.qualDottedOwner(t.Object); ok && mod != "" {
			// `lists.List.head`: a function of a std owner the builder
			// holds no def for, through its module's qualifier. The value's
			// body is that qualified call.
			return bl.ownerFuncRef(t)
		}
		if owner, isIdent := t.Object.(*ast.Ident); isIdent {
			// `fakes.Quiet`: a marker named through its file's qualifier.
			// A locally bound name is a value, never a qualifier.
			if _, local := bl.bound[owner.Name]; !local {
				if name, dotted := bl.g.dottedTypeQualifier(t); dotted {
					if dst, k, mobile, ok := bl.markerValue(t, name); ok {
						return dst, k, mobile, true
					}
				}
			}
			// `ids.UserId`: another file's distinct type named as its
			// constructor function (ownerfuncref.go).
			if !irQualIsLocal(bl, owner.Name) && bl.g.distinctCtorRef(t) {
				return bl.callFuncValue(t, owner.Name+"."+t.Field.Name)
			}
			// `span.ident`, `io.print`: another file's function as a value.
			if dst, k, mobile, ok, handled := bl.fileFuncRef(t, owner); handled {
				return dst, k, mobile, ok
			}
		}
		// Value-field projection handles retained structs and records after
		// tuple, variant, method and imported once references are resolved.
		return bl.fieldRead(t)

	case *ast.Call:
		if hasPlaceholder(t.Args) {
			return bl.partialApplication(t)
		}
		if dst, k, mobile, ok := bl.emptyMap(t); ok {
			return dst, k, mobile, true
		}
		if dst, k, mobile, ok := bl.emptyCollection(t); ok {
			return dst, k, mobile, true
		}
		if dst, k, mobile, ok := bl.preludeCall(t); ok {
			return dst, k, mobile, true
		}
		if dot, ok := t.Func.(*ast.DotVariant); ok && dot.ResolvedEnum != "" {
			return bl.variantCall(t, bl.g.qualifiedDot(dot))
		}
		if field, ok := t.Func.(*ast.FieldAccess); ok {
			if dst, k, mobile, ok := bl.variantCall(t, field); ok {
				return dst, k, mobile, true
			}
		}
		// A DISTINCT'S TWO DIRECTIONS COME FIRST, both keyed on a
		// `*ast.TypeIdent` callee, which `bl.call` never resolves: it
		// answers a module-scope `fn` and `io.print`. See
		// irdistinct.go. Each declines silently, so a callee that is not one
		// of them falls through to the call arm unchanged.
		if fa, ok := t.Func.(*ast.FieldAccess); ok && fa.Field != nil {
			// `Day.Hours(48)`: the constructor of a namespaced distinct, whose
			// whole dotted name is the type's.
			// `ids.UserId(3)`: a distinct named through its file's
			// qualifier, which modulequaltype.go resolves by that spelling.
			qualifier := ""
			if owner, isType := fa.Object.(*ast.TypeIdent); isType {
				qualifier = owner.Name
			} else if owner, isIdent := fa.Object.(*ast.Ident); isIdent && !irQualIsLocal(bl, owner.Name) {
				qualifier = owner.Name
			}
			if qualifier != "" {
				name := qualifier + "." + fa.Field.Name
				if d, found := bl.g.namedType(name); found && d.isDistinct {
					ti := &ast.TypeIdent{Name: name, Line: fa.Line, Col: fa.Col}
					if dst, k, mobile, ok := bl.distinctMake(t, ti); ok {
						return dst, k, mobile, true
					}
				}
			}
		}
		if ti, isType := t.Func.(*ast.TypeIdent); isType {
			if dst, k, mobile, ok := bl.distinctMake(t, ti); ok {
				return dst, k, mobile, true
			}
			if dst, k, mobile, ok := bl.distinctInner(t, ti); ok {
				return dst, k, mobile, true
			}
			if dst, k, mobile, ok := bl.structCallForm(t, ti); ok {
				return dst, k, mobile, true
			}
			if dst, k, mobile, ok := bl.stdVariantCall(t, ti); ok {
				return dst, k, mobile, true
			}
		}
		return bl.call(t)

	case *ast.Dbg:
		// `dbg E`, built from existing IR nodes; the operand is fenced to the scalar domain because
		// `debugRendering` can refuse and `irScalarRender` panics rather than
		// routing one. See irdbg.go.
		return bl.dbg(t)

	case *ast.Todo:
		// See irtodo.go.
		return bl.todo(t, kindInvalid)
	}
	return ir.NoTemp, kindInvalid, false, false
}

// binary lowers an arithmetic operator, String concatenation, the PIPE and a
// COMPARISON, plus short-circuit Boolean operators.
func (bl *irScalarBuilder) binary(t *ast.Binary) (ir.Temp, kind, bool, bool) {
	if t.Op == "and" || t.Op == "or" {
		return bl.shortCircuit(t)
	}
	if op, isCompare := irCompareOps[t.Op]; isCompare {
		// `emitCompare` spells it; see ircompare.go.
		//
		// AHEAD of the arithmetic table because no comparison is in it, and
		// ahead of the pipe for the same reason. `bl.compare` lowers both
		// operands itself.
		return bl.compare(t, op)
	}
	if t.Op == "|>" {
		// A pipe is a desugaring onto the ordinary call path and not an
		// operator at all, so it is answered before the arithmetic table,
		// before any operand is lowered. See irpipe.go, and note that `dbg`'s nil-`Expr` guard stops being a
		// fence here: it is how `bl.dbgOf` tells the two spellings apart.
		return bl.pipe(t)
	}
	op, arithmetic := irArithOps[t.Op]
	if !arithmetic {
		return ir.NoTemp, kindInvalid, false, false
	}
	lhs, lk, mobile, ok := bl.lower(t.Left)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	if !mobile {
		// An impure left operand is forced into a temporary
		// so a later operand's statements cannot be hoisted ahead of it. THE
		// COPY IS THE NODE FOR THAT, and
		// its position is the operand's own — the construct being
		// materialized, not the operator.
		c := ir.NewCopy(bl.g.irNodePos(t.Left), bl.f.NewTemp(), lhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: lk, copy: irCopyForce})
		lhs = c.Dst()
	}
	rhs, rk, _, ok := bl.lower(t.Right)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	if f := bl.g.stdlibOperatorFor(t.Op, lk, rk); f != nil {
		return bl.stdOperatorCall(t, f, lhs, rhs)
	}
	if v, k, mobile, ok, handled := bl.stdGenericOperatorCall(t, lhs, rhs, lk, rk); handled {
		return v, k, mobile, ok
	}
	if lk.def != nil && !isDecimalKind(lk) {
		return bl.userOperatorCall(t, lhs, rhs, lk, rk)
	}
	if t.Op == "+" && (lk.tag == tagList || rk.tag == tagList || (lk == kindEmptyList && rk == kindEmptyList)) {
		// `xs + ys` is List.concat's own `rt.ListConcat(xs, ys)` host call,
		// with the untyped side coerced to the typed one.
		call := &ast.Call{Line: t.Line, Col: t.Col, Args: []ast.Node{t.Left, t.Right}}
		args := irQualArgs{temps: []ir.Temp{lhs, rhs}, kinds: []kind{lk, rk}, mobile: []bool{true, true}, ok: true}
		if p := bl.listCallPlan(call, args, "concat"); p != nil {
			return bl.qualEmit(call, args, p)
		}
		return ir.NoTemp, kindInvalid, false, false
	}
	if lk != rk {
		// A mixed-type operator or arithmetic on a named type is declined;
		// the body is stubbed and refused if reachable.
		return ir.NoTemp, kindInvalid, false, false
	}
	if lk == kindString {
		// `"a" + "b"` is concatenation, not arithmetic, and `+` is
		// the only operator String has. The node is
		// `ir.Concat` and the delivery is `irConcatBinary`; every arith
		// shape's `expr` is impure, and so is a join, because its
		// operands' spellings are re-read.
		if t.Op != "+" {
			return ir.NoTemp, kindInvalid, false, false
		}
		c := ir.NewConcat(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), lhs, rhs)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindString})
		return c.Dst(), kindString, false, true
	}
	ak, modelled := bl.g.irArithKind(op, lk, t.Wrapping)
	if !modelled {
		return ir.NoTemp, kindInvalid, false, false
	}
	a := ir.NewArith(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, ak, lhs, rhs)
	bl.b.Append(a)
	// Every arith shape's `expr` is impure — `emitArith` returns a
	// zero `pure` for all seven — so an arith result in a left operand
	// position always hoists. Read off that fact rather than restated.
	return a.Dst(), lk, false, true
}

// interp lowers `"a${x}b"`: the text runs are String constants, each hole is
// `ir.Render`'s Display discipline, and the terminal is the join.
//
// ONE PART IS THAT PART, which is `ir.NewConcat`'s precondition: a join of one is the identity and a
// join of none is the empty String constant.
//
// `pos` is a span, and this is the one site that produces one. It is the interpolation's whole extent — its start plus the
// furthest line its holes reach — and it is worn by the text-run constants
// and by the join, which are exactly the instructions whose construct spans
// lines. A hole keeps its OWN point position (`irNodePos(part.Expr)`),
// because the hole is a construct in its own right and is on one line. The
// three spanning positions in the whole retained graph are `escape_program`'s
// two text runs and its join. See irspan.go.
func (bl *irScalarBuilder) interp(t *ast.StringInterp) (ir.Temp, kind, bool, bool) {
	pos := bl.g.irNodeSpan(t)
	parts := make([]ir.Temp, 0, len(t.Parts))
	for i, p := range t.Parts {
		switch part := p.(type) {
		case ast.StringText:
			// `ast.StringText` has no position of its own — it is one field
			// and it is the string — so a text run is blamed on the
			// interpolation it belongs to. irinterp.go records that as the
			// one enumeration row asking for something the AST cannot
			// supply.
			c := ir.NewString(pos, bl.f.NewTemp(), part.Value)
			bl.b.Append(c)
			parts = append(parts, c.Dst())
		case ast.StringExpr:
			src, k, mobile, ok := bl.lower(part.Expr)
			if !ok {
				return ir.NoTemp, kindInvalid, false, false
			}
			if never, isNever := bl.neverAs(part.Expr, src, k, kindString); isNever {
				// A hole of Infallible (`Debug.inspect(e)` in an arm no
				// value reaches) renders nothing; it never completes.
				src, k = never, kindString
			}
			// An impure hole is forced into a temporary for every part but the
			// last. Only the last-evaluated operand is safe to leave unforced,
			// the same unforced-tail rule `bl.call` applies. The index is over
			// parts and not over holes, so the hole in `"Method(${x})"` is
			// forced by the trailing `)`.
			if !mobile && i != len(t.Parts)-1 {
				cp := ir.NewCopy(bl.g.irNodePos(part.Expr), bl.f.NewTemp(), src)
				bl.b.Append(cp)
				bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
				src = cp.Dst()
			}
			if k == kindString {
				// The Display discipline is the IDENTITY on a String, which
				// render.go names as "the one discipline whose answer for
				// some kind is the operand unchanged", so a Render here would
				// be a node whose consumer must recognise as a no-op.
				parts = append(parts, src)
				continue
			}
			if !irScalarLeafKind(k) && !isDecimalKind(k) {
				if r, ok := bl.displayHole(part.Expr, src, k, mobile); ok {
					parts = append(parts, r)
					continue
				}
				// A hole outside the scalar domain reaches
				// `displayRendering`'s container, local-impl and stdlib
				// arms, each of which can REFUSE — and a refusal recorded
				// while a build is still deciding whether to decline is a
				// tally row for a lowering that did not happen. Declined
				// here so `irScalarRender` can PANIC on a failed rendering
				// instead of having to route one.
				//
				// Nothing reaches this. `lower` produces Int, Float, Bool,
				// String and, through a Unit-returning call, Unit; the first
				// four are the domain and the fifth cannot reach here,
				// because the checker refuses `"${unit_call()}"` with "no
				// impl of `Display` for `Unit`" before this producer sees
				// it. It is a fence that keeps this builder from producing
				// a wrong answer if the front end ever admits one.
				return ir.NoTemp, kindInvalid, false, false
			}
			r := ir.NewRenderDisplay(bl.g.irNodePos(part.Expr), bl.f.NewTemp(), src)
			bl.b.Append(r)
			bl.side(r.Dst(), irScalarSide{k: kindString})
			parts = append(parts, r.Dst())
		default:
			return ir.NoTemp, kindInvalid, false, false
		}
	}
	switch len(parts) {
	case 0:
		c := ir.NewString(pos, bl.f.NewTemp(), "")
		bl.b.Append(c)
		return c.Dst(), kindString, true, true
	case 1:
		return parts[0], kindString, false, true
	}
	c := ir.NewConcat(pos, bl.f.NewTemp(), parts...)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindString})
	return c.Dst(), kindString, false, true
}

// irCallOperandKind is the value domain `ir.Call`'s operand vector admits:
// the callable value domain (`irCallableValueKind`) plus Unit.
//
// Output rendering has its own domain: Display accepts scalar leaves, while
// Debug also accepts retained lists. Call operands need no rendering. A Unit
// operand (`apply(f, unit_value())`) is a value with no storage.
func irCallOperandKind(k kind) bool { return k == kindUnit || irCallableValueKind(k) }

// stdIntrinsicOwner names the stdlib type whose calls an intrinsic arm of
// call lowers (`Task`, `Channel`, `Sender`, `Receiver`, `Supervisor`) when
// the callee's owner resolves to that type's std declaration: bare, as an
// imported name, or through a whole-module import's qualifier
// (`tasks.Task.spawn`). Identity is the declaration the analyzer resolved the
// owner to, so a user file's own `Task` (nominal identity is declaring file
// and name) takes the ordinary qualified-call route, and that file still
// reaches std's through the qualifier. Anything else answers "".
func (bl *irScalarBuilder) stdIntrinsicOwner(fa *ast.FieldAccess) string {
	var sym *analysis.Symbol
	switch q := fa.Object.(type) {
	case *ast.TypeIdent:
		if bl.g.userTypeNamed(q.Name) {
			return ""
		}
		sym = resolvedTypeSymbol(bl.g.fa, q.Name)
	case *ast.FieldAccess:
		mod, isIdent := q.Object.(*ast.Ident)
		if !isIdent || q.Field == nil || irQualIsLocal(bl, mod.Name) {
			return ""
		}
		sym = resolvedScopeSymbol(qualifierScope(bl.g.fa, mod), q.Field.Name)
	}
	if sym == nil || sym.Node == nil {
		return ""
	}
	for _, s := range stdIntrinsicTypes {
		if d := stdDeclaringSymbol(bl.g.fa, s.origin, s.nomi); d != nil && d.Node == sym.Node {
			return s.nomi
		}
	}
	return ""
}

// stdShortOwner is the owner a defaulted std call may name: a bare type name
// (module ""), or a type named through a std module's qualifier
// (`supervisors.Supervisor`), which names that module's declaration whatever
// this file declares.
func (bl *irScalarBuilder) stdShortOwner(fa *ast.FieldAccess) (owner, module string, ok bool) {
	if fa.Field == nil {
		return "", "", false
	}
	switch q := fa.Object.(type) {
	case *ast.TypeIdent:
		return q.Name, "", true
	case *ast.FieldAccess:
		owner, module, ok := bl.qualDottedOwner(q)
		return owner, module, ok && module != ""
	}
	return "", "", false
}

// stdIntrinsicTypes are the std types stdIntrinsicOwner answers, by declaring
// module.
var stdIntrinsicTypes = []struct{ origin, nomi string }{
	{"std/tasks", "Task"},
	{"std/channels", "Channel"},
	{"std/channels", "Sender"},
	{"std/channels", "Receiver"},
	{"std/supervisors", "Supervisor"},
}

// stdDeclaringSymbol is the symbol a std module declares under name, read from
// that module's own scope as fa's analysis holds it (the analyzer's
// concurrent-scope rule reads `std/tasks` the same way), or nil.
func stdDeclaringSymbol(fa *analysis.FileAnalysis, origin, name string) *analysis.Symbol {
	if fa == nil {
		return nil
	}
	// Inside the declaring module its own scope answers: a std file analyzed
	// on its own (`nomi test std/tasks.nomi`, a reference page's editor) has
	// its own declarations, and the shared stdlib's copy of the module holds
	// different nodes for the same names.
	var scope *analysis.Scope
	if fa.Origin == origin {
		scope = fa.ModuleScope
	}
	if scope == nil {
		scope = fa.StdlibModuleScopes[strings.TrimPrefix(origin, "std/")]
	}
	if scope == nil {
		return nil
	}
	sym := scope.LookupLocal(name)
	for range 8 {
		if sym == nil || sym.Resolved == nil {
			return sym
		}
		sym = sym.Resolved
	}
	return nil
}

// call lowers module functions, callable values and qualified calls.
//
// Direct calls place arguments with argSlotPlan. Explicit operands run in
// its order; omitted defaults follow in parameter order, in callee scope.
func (bl *irScalarBuilder) call(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	// Anywhere: `check` answers a value, so
	// no boundary decides its delivery.
	if bl.testingCheckCall(t) {
		return bl.testingCheck(t)
	}
	if fa, qualified := t.Func.(*ast.FieldAccess); qualified && fa.Field != nil {
		if owner := bl.stdIntrinsicOwner(fa); owner == "Channel" || owner == "Sender" || owner == "Receiver" {
			// Channel calls take the checker's solved signature, including a
			// constructor's explicit type argument. Inside a grouped case's
			// assertion subject its rows are recorded after it, as for any
			// other qualified call.
			if bl.recording > 0 {
				return bl.recordedQualCall(t, func() (ir.Temp, kind, bool, bool) {
					return bl.channelCall(t, owner, fa.Field.Name)
				})
			}
			return bl.channelCall(t, owner, fa.Field.Name)
		}
	}
	if v, k, mobile, ok, handled := bl.returnSelfDispatch(t); handled {
		return v, k, mobile, ok
	}
	if len(t.TypeArgs) != 0 {
		if _, qualified := t.Func.(*ast.FieldAccess); qualified {
			return bl.turbofishDispatch(t)
		}
		id, direct := t.Func.(*ast.Ident)
		if !direct || bl.g.irMonoTemplate(bl.g.funcs[id.Name]) == nil {
			return no()
		}
		if _, bound := bl.g.lookup(id.Name); bound {
			return no()
		}
		for scope := bl; scope != nil; scope = scope.parent {
			if _, bound := scope.boundK[id.Name]; bound {
				return no()
			}
		}
	}
	// TAIL POSITION IS A FACT AND NOT A SHAPE, which is `ir/call.go`'s own
	// division: `analysis/tail_position.go` computes it, `ast.Call.IsTailCall`
	// carries it, and both consumers read it. So the site rides on the node
	// through `irCallSite` rather than declining the call. A tail plan's
	// REWRITE is `tail`'s code shape and cannot arise here: a tail-plan
	// member is built as an ordinary body with its calls marked
	// (internal/vm/tail.go).
	hasNamed := false
	for _, a := range t.Args {
		if _, named := a.(*ast.NamedArg); named {
			hasNamed = true
		}
	}
	if fa, qualified := t.Func.(*ast.FieldAccess); qualified {
		if fa.Field != nil {
			switch owner := bl.stdIntrinsicOwner(fa); {
			case owner == "Task" && bl.recording == 0:
				// Task calls place their own named arguments.
				return bl.taskCall(t, fa.Field.Name)
			case owner == "Task":
				// Inside a grouped case's assertion subject, recorded after the
				// call as any other qualified call is.
				return bl.recordedQualCall(t, func() (ir.Temp, kind, bool, bool) {
					return bl.taskCall(t, fa.Field.Name)
				})
			case owner == "Supervisor" && fa.Field.Name == "spawn":
				return bl.supervisorSpawn(t)
			case owner == "Supervisor" && fa.Field.Name == "spawn_all":
				return bl.supervisorSpawnAll(t)
			}
		}
		if owner, module, isStdOwner := bl.stdShortOwner(fa); isStdOwner {
			if bl.recording > 0 && namedArgNode(t.Args) == nil && bl.stdShortCallShape(t, owner, fa.Field.Name, module) {
				// Inside a VM-only assertion subject, its rows recorded after
				// the call as any other qualified call's are.
				return bl.recordedQualCall(t, func() (ir.Temp, kind, bool, bool) {
					v, k, mobile, ok, _ := bl.stdShortCall(t, owner, fa.Field.Name, module)
					return v, k, mobile, ok
				})
			}
			if v, k, mobile, ok, handled := bl.stdShortCall(t, owner, fa.Field.Name, module); handled {
				return v, k, mobile, ok
			}
		}
		if site, ok := bl.siblingQualSite(fa); ok && (hasNamed || site.fn.defaults) {
			// `lib.add(1)` or `lib.add(1, b: 5)`: placed and filled as a
			// bare call to the same function is (siblingdefault.go).
			return bl.siblingCall(t, site)
		}
		if hasNamed {
			return bl.namedQualCall(t, fa)
		}
		// Output primitives take rendered text, so insert Render before resolving
		// ordinary qualified calls. Their signatures describe source values.
		if dst, k, mobile, ok := bl.hostOutput(t, fa); ok {
			return dst, k, mobile, true
		}
		return bl.qualCall(t, fa)
	}
	if id, ok := t.Func.(*ast.Ident); ok && !hasNamed && bl.bareIterLoop(id) {
		// `import std/iter.Iter.{loop}` then `loop(...)`: Iter.loop.
		return bl.iterLoop(t)
	}
	if id, ok := t.Func.(*ast.Ident); ok && !hasNamed && len(t.TypeArgs) == 0 {
		if v, k, mobile, lowered, handled := bl.genericNestedCall(t, id); handled {
			return v, k, mobile, lowered
		}
	}
	if id, ok := t.Func.(*ast.Ident); ok {
		for parent := bl.parent; parent != nil; parent = parent.parent {
			if parent.boundK[id.Name].tag == tagFunc {
				return bl.indirectCall(t)
			}
		}
		if k, bound := bl.boundK[id.Name]; bound && (k.tag == tagFunc || irCallableDistinct(k)) {
			return bl.indirectCall(t)
		}
		if l, bound := bl.g.lookup(id.Name); bound && (l.k.tag == tagFunc || irCallableDistinct(l.k)) {
			return bl.indirectCall(t)
		}
		if _, bound := bl.g.lookup(id.Name); !bound && !hasNamed {
			// `add_op(1)` over `once add_op: (Int) -> Int`: a call through
			// the function value the cell holds. A local of the name was
			// tried above, so this is the file's `once`.
			if d := bl.g.onces[id.Name]; d != nil && d.k.tag == tagFunc {
				return bl.indirectCall(t)
			}
		}
	} else {
		return bl.indirectCall(t)
	}
	callee, direct := t.Func.(*ast.Ident)
	if !direct {
		return no()
	}
	if _, shadowed := bl.g.lookup(callee.Name); shadowed {
		// A local name shadowing a function is a call THROUGH A VALUE, which
		// is `CalleeIndirect` and a different resolution.
		return no()
	}
	if f := bl.g.stdlibSibling(callee.Name); f != nil {
		if !hasNamed && !bl.inTest && irScalarHost(f) {
			args := bl.irQualLowerArgs(t)
			if bl.qualSignature(t, args, f.params, f.result) {
				return bl.qualEmit(t, args, &irQualPlan{token: f, name: f.key, result: f.result, host: true})
			}
		}
		// A Nomi-bodied sibling links to its retained declaration in this
		// module, and stdFuncPlan answers only when that body already exists;
		// an extern-backed host sibling is a crossing by binding name.
		// A stdlib module's own test body calls a sibling the cached lowering
		// retained, outside an assertion subject or piped (a piped call
		// records no rows).
		stdTest := bl.stdTestOwnType() && (bl.recording == 0 || t == bl.pipedCall)
		if !hasNamed && (!bl.inTest || stdTest) {
			args := bl.irQualLowerArgs(t)
			if p := bl.stdFuncPlan(t, args, f); p != nil {
				return bl.qualEmit(t, args, p)
			}
		}
		// A stdlib sibling resolves before `g.funcs`, so any other sibling
		// call is declined here rather than lowered as a direct call to a
		// same-named user declaration.
		return no()
	}
	if !hasNamed {
		if v, k, mobile, ok, handled := bl.stdGenericSiblingCall(t, callee.Name); handled {
			return v, k, mobile, ok
		}
	}
	// `import std/calendar.Date.parse` puts a stdlib type's method in this
	// file's scope; the bare call reaches the same impl plan the qualified
	// spelling reaches.
	if _, local := bl.g.funcs[callee.Name]; !local && !hasNamed && bl.g.std != nil {
		if sym := resolvedBareSymbolAt(bl.g.fa, callee); sym != nil && sym.OwningType != "" && sym.Name != "" &&
			len(bl.g.std.byType[sym.OwningType+"."+sym.Name]) != 0 {
			_, localType := bl.g.types[sym.OwningType]
			_, localIface := bl.g.ifaces[sym.OwningType]
			if localType || localIface {
				return no()
			}
			lower := func() (ir.Temp, kind, bool, bool) {
				// A generic member (`import std/strings.String.{split}`) is
				// instantiated per program, as its qualified spelling is.
				if v, k, mobile, ok, handled := bl.stdGenericQualCall(t, sym.OwningType, sym.Name); handled {
					return v, k, mobile, ok
				}
				args := bl.irQualLowerArgs(t)
				if p := bl.qualImplPlan(t, args, sym.OwningType, sym.Name); p != nil {
					return bl.qualEmit(t, args, p)
				}
				return no()
			}
			if bl.recording > 0 {
				// Inside a VM-only assertion subject the call's rows are
				// recorded after it, as a qualified call's are.
				return bl.recordedQualCall(t, lower)
			}
			return lower()
		}
	}
	sig := bl.g.funcs[callee.Name]
	if sig == nil && !hasNamed {
		if key, output := stdBareOutputKey(bl.g.fa, callee); output || bl.inStdIO() && bl.ownOutputKey(callee, &key) {
			// `import std/io.print` then `print(x)`: io.print itself.
			return bl.hostOutputKey(t, key)
		}
		if f := bl.stdBareGenericFileFunc(callee); f != nil {
			// `import std/io.capture` then `capture(...)`: the generic
			// stdlib function, instantiated as `io.capture(...)` is.
			if v, k, mobile, ok, handled := bl.stdInstCallAt(t, f, kindInvalid); handled {
				return v, k, mobile, ok
			}
		}
		if module, name, isStd := stdBareFileFunc(bl.g.fa, callee); isStd {
			// `import std/io.read_line` then `read_line()`: the call
			// `io.read_line()` plans, under any alias the import gave it.
			lower := func() (ir.Temp, kind, bool, bool) {
				if bl.qualBare == nil {
					bl.qualBare = bl.checkedBareOperands(t)
				}
				args := bl.irQualLowerArgs(t)
				if p := bl.stdFilePlan(t, args, module, name); p != nil {
					return bl.qualEmit(t, args, p)
				}
				return no()
			}
			if bl.recording > 0 {
				return bl.recordedQualCall(t, lower)
			}
			return lower()
		}
	}
	if sig == nil && bl.g.files != nil {
		if site, ok := bl.g.files.lookupBare(bl.g.fa, callee); ok && site.fn != nil && site.unit != bl.g.fileUnit {
			return bl.siblingCall(t, site)
		}
	}
	if sig == nil && !hasNamed && bl.recording == 0 && bl.sameImplName(callee) {
		if p := bl.sameImplPlan(callee.Name); p != nil {
			args := bl.irQualLowerArgs(t)
			it := p.token.(*implItem)
			if bl.qualSignature(t, args, it.params, it.result) {
				return bl.qualEmit(t, args, p)
			}
			return no()
		}
	}
	if tpl := bl.g.irMonoTemplate(sig); tpl != nil {
		// A walk-only test body is built after the module's instances are
		// flushed; irRetryWalkOnlyTestBodies flushes again after it.
		// Named arguments and omitted defaults are placed below against the
		// instance's signature; the checker's instantiated signature has
		// every parameter either way.
		if len(t.Args) > len(tpl.decl.Params) {
			return no()
		}
		typeArgs, ok := bl.g.checkedMonoTypeArgs(t, tpl)
		if !ok {
			typeArgs, ok = bl.g.checkedMonoTypeArgsFilled(t, tpl)
		}
		if !ok {
			return no()
		}
		inst, why, _ := bl.g.resolveMonoInstance(tpl, typeArgs, callee.Name)
		if why != "" || inst == nil {
			return no()
		}
		sig = inst.sig
	}
	if sig == nil || !sig.lowerable || sig.decl == nil || sig.dict != nil ||
		sig.tps != nil {
		// `sig.tps != nil` and not `len(sig.tps) != 0`: a generic callee,
		// even with an empty type-parameter slice, can need a Go type
		// assertion on its result, which this producer does not build.
		return no()
	}
	if !irCallableValueKind(sig.result) && sig.result != kindUnit {
		// The result domain is the operand domain. See irCallOperandKind.
		return no()
	}
	if irNamedCtlForm(sig.decl) != irCtlNone {
		// Its `break` or `continue` would have to leave this caller too.
		irDeclineNote("a direct call to a function that uses break or continue: " + callee.Name)
		return no()
	}
	plan, planned := argSlotPlan(t.Args, sig.params, sigParamNames(sig))
	// Inside an assertion subject the rows follow the argument slot
	// mapping (recordCallSlots).
	if !planned {
		return no()
	}
	args := make([]ir.Temp, len(sig.params))
	for i := range args {
		args[i] = ir.NoTemp
	}
	for _, i := range plan.order {
		a, slot := argExpr(t.Args[i]), plan.slots[i]
		src, k, _, ok := bl.lowerTypedOperand(a, sig.params[slot])
		if !ok || !irCallOperandKind(k) {
			return no()
		}
		// Only the final operand may stay unforced. Inside an assertion
		// subject it is also forced, because it is named twice, in its row
		// and in the call.
		src, k, ok = bl.coerceEmpty(a, src, k, sig.params[slot])
		if !ok {
			return no()
		}
		args[slot] = src
	}
	if len(t.Args) != len(sig.params) {
		if !bl.callDefaults(sig, args) {
			return no()
		}
	}
	// A call inside an assertion subject shows its arguments. The rows are
	// recorded after every operand is lowered and before the call. A no-op
	// outside a subject, so a `fn` body's call is unchanged.
	//
	// Except for a piped call, matched by node identity against the
	// synthetic node the splice built: a piped call records no call
	// arguments, and `testdata/pipe_assert_report.nomi` compares the
	// failure report text.
	if t != bl.pipedCall {
		bl.recordCallSlots(t.Args, plan, sigParamNames(sig), args, sig.params, sig.result)
	}
	c := ir.NewCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t),
		bl.g.irFunctionCallee(sig), args...)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: sig.result, deferrable: true})
	return c.Dst(), sig.result, false, true
}

// irMonoTemplate is the template a call to sig instantiates for the IR: its
// own when erasure declined it, and for an erased generic (a bare type
// parameter, bounded or not) the template monoTemplateFor derives, since the
// VM runs an instance rather than an erased body.
func (g *gen) irMonoTemplate(sig *fnSig) *monoTemplate {
	if sig == nil {
		return nil
	}
	if sig.mono != nil {
		return sig.mono
	}
	if sig.decl == nil || len(sig.decl.TypeParams) == 0 {
		return nil
	}
	if (sig.tps == nil && sig.dict == nil) && sig.lowerable {
		return nil
	}
	if !sig.irMonoAsked {
		sig.irMono, sig.irMonoAsked = g.monoTemplateFor(sig.decl), true
	}
	return sig.irMono
}

// structCallForm builds the struct call form `Foo({a: 1})` as the brace
// literal `Foo{a: 1}`: the argument's fields in written order, then the
// omitted fields' defaults. A record VALUE argument is read field by field
// (structFromRecord). A generic struct's call form is built at the instance
// its fields solve, as its brace literal is.
func (bl *irScalarBuilder) structCallForm(t *ast.Call, ti *ast.TypeIdent) (ir.Temp, kind, bool, bool) {
	if len(t.Args) != 1 {
		return ir.NoTemp, kindInvalid, false, false
	}
	if tpl, instantiate, isTemplate := bl.g.genericTemplateNamed(ti.Name); isTemplate && tpl.structDecl() != nil {
		if lit, isLit := anonArgAsLiteral(t.Args[0]); isLit {
			return bl.genericStructMake(lit, tpl, instantiate)
		}
		return bl.structFromRecord(t, func(rec kind) *typeDef {
			return bl.g.recordInstance(tpl, instantiate, rec)
		})
	}
	d, found := bl.g.namedType(ti.Name)
	if !found || !irRetainedStructKind(d) {
		return ir.NoTemp, kindInvalid, false, false
	}
	lit, isLit := anonArgAsLiteral(t.Args[0])
	if !isLit {
		return bl.structFromRecord(t, func(kind) *typeDef { return d })
	}
	return bl.structMakeOf(lit, d)
}

// recordInstance is the instance of the generic struct template tpl that a
// record of kind rec fills: each declared field's annotation unified with
// the record's field of that name. A type parameter only an omitted field
// names is unsolved, and the answer is nil.
func (g *gen) recordInstance(tpl *genericTemplate, instantiate func([]kind) (kind, bool), rec kind) *typeDef {
	params := templateParamSet(tpl)
	solved := map[string]kind{}
	for _, f := range tpl.structDecl().Fields {
		for i, name := range rec.comp.names {
			if name == f.Name {
				g.unifyTypeParams(f.TypeAnnotation, rec.comp.parts[i], params, solved)
			}
		}
	}
	args, ok := templateArgs(tpl, solved)
	if !ok {
		return nil
	}
	k, ok := instantiate(args)
	if !ok || k.tag != tagNamed {
		return nil
	}
	return k.def
}

// structFromRecord builds `Config(defaults())`, the struct call form over a
// record VALUE: the record is evaluated, its
// fields are matched to the declaration by name, and an omitted field takes
// its declared default. The record is bound to a name no program can spell
// and the struct is built from a literal reading its fields, so the
// defaults are filled exactly as the literal form fills them. of names the
// struct to build from the record's kind, which a generic struct's instance
// depends on.
func (bl *irScalarBuilder) structFromRecord(t *ast.Call, of func(rec kind) *typeDef) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	src, k, _, ok := bl.lower(t.Args[0])
	if !ok || !irRetainedRecordKind(k) {
		return no()
	}
	d := of(k)
	if d == nil {
		return no()
	}
	line, col := nodePos(t.Args[0])
	name := "%record" + strconv.Itoa(line) + "." + strconv.Itoa(col)
	bl.patternBinding(t.Args[0], name, src, k)
	lit := &ast.StructLit{Line: t.Line, Col: t.Col}
	for _, f := range k.comp.names {
		lit.Fields = append(lit.Fields, ast.StructFieldVal{Name: f, Line: line, Col: col,
			Value: &ast.FieldAccess{Object: &ast.Ident{Name: name, Line: line, Col: col},
				Field: &ast.Ident{Name: f, Line: line, Col: col}, Line: line, Col: col}})
	}
	return bl.structMakeOf(lit, d)
}

// bareIterLoop reports whether a bare callee name is std's Iter.loop brought
// into scope by a selective import, and not a local or a file function.
func (bl *irScalarBuilder) bareIterLoop(id *ast.Ident) bool {
	name := id.Name
	if _, local := bl.g.funcs[name]; local {
		return false
	}
	for scope := bl; scope != nil; scope = scope.parent {
		if _, bound := scope.boundK[name]; bound {
			return false
		}
	}
	sym := resolvedBareSymbolAt(bl.g.fa, id)
	return sym != nil && sym.OwningType == "Iter" && sym.Name == "loop" && bl.g.iterOwns("Iter")
}

// hostOutput keeps rendering in the IR and passes its String to the host.
// Print and write use Display; inspect uses Debug and holds effectful operands once.
func (bl *irScalarBuilder) hostOutput(t *ast.Call, fa *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	owner, isIdent := fa.Object.(*ast.Ident)
	if !isIdent || fa.Field == nil || len(t.Args) != 1 {
		return no()
	}
	if _, shadowed := bl.g.lookup(owner.Name); shadowed {
		return no()
	}
	std, isStd := stdFileQualifier(bl.g.fa, owner)
	if !isStd && bl.inStdIO() && owner.Name == "io" && moduleScopeOf(bl.g.fa, owner.Name) == bl.g.fa.ModuleScope {
		// std/io naming itself, as its own `//!` tests do.
		std, isStd = "io", true
	}
	key := std + "." + fa.Field.Name
	if !isStd || !isOutputKey(key) {
		return no()
	}
	return bl.hostOutputKey(t, key)
}

// stdBareOutputKey is the host key of a bare call to std/io's print, write
// or inspect brought into scope by a selective import (`import std/io.print`).
func stdBareOutputKey(fa *analysis.FileAnalysis, id *ast.Ident) (string, bool) {
	sym := resolvedBareSymbolAt(fa, id)
	if sym == nil || fa.StdlibModuleScopes == nil {
		return "", false
	}
	io := fa.StdlibModuleScopes["io"]
	if io == nil {
		return "", false
	}
	for _, key := range []string{printKey, writeKey, inspectKey} {
		if resolveSymbol(io.Lookup(strings.TrimPrefix(key, "io."))) == sym {
			return key, true
		}
	}
	return "", false
}

// inStdIO reports whether this body belongs to std/io itself, whose own
// tests and reference examples call print, write and inspect bare or as
// `io.print`.
func (bl *irScalarBuilder) inStdIO() bool {
	return bl.g.stdModule == "io" && bl.g.fa != nil && bl.g.fa.ModuleScope != nil
}

// ownOutputKey reports, inside std/io, whether a bare name is the module's
// own print, write or inspect, and sets key to its host key.
func (bl *irScalarBuilder) ownOutputKey(id *ast.Ident, key *string) bool {
	sym := resolvedBareSymbolAt(bl.g.fa, id)
	if sym == nil {
		return false
	}
	for _, k := range []string{printKey, writeKey, inspectKey} {
		if resolveSymbol(bl.g.fa.ModuleScope.Lookup(strings.TrimPrefix(k, "io."))) == sym {
			*key = k
			return true
		}
	}
	return false
}

// hostOutputKey is hostOutput once the call is known to be key's.
func (bl *irScalarBuilder) hostOutputKey(t *ast.Call, key string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Args) != 1 {
		return no()
	}
	src, k, mobile, ok := bl.lower(t.Args[0])
	if ok {
		src, k, ok = bl.typeOpenEmpty(t.Args[0], src, k)
	}
	debug := key == inspectKey
	if ok && k.tag == tagNamed {
		plan := bl.namedRenderPlan(k, debug)
		if plan == nil && debug {
			// A std type whose Debug impl is a `host fn` (Dynamic).
			plan = bl.stdHostDebugPlan(t.Args[0], src, k)
		}
		if plan != nil {
			// A std body's plan carries the cached declaration's own symbol,
			// which is what links across modules; a local impl's is interned
			// here.
			callee := plan.sym
			if callee == nil {
				callee = bl.g.irCalleeSym(plan.token, plan.name)
			}
			constructor := ir.NewCall
			if plan.host {
				constructor = ir.NewHostCall
			}
			r := constructor(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OrdinaryCall, callee, src)
			bl.b.Append(r)
			bl.side(r.Dst(), irScalarSide{k: kindString, deferrable: true})
			c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(key, key), r.Dst())
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: kindUnit, deferrable: true})
			return c.Dst(), kindUnit, false, true
		}
	}
	if ok && !debug && !irScalarLeafKind(k) && !isDecimalKind(k) {
		// `io.print(xs)` over a container, a record or a tuple renders as
		// its interpolation `"${xs}"` does.
		if r, rendered := bl.displayHole(t.Args[0], src, k, mobile); rendered {
			c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(key, key), r)
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: kindUnit, deferrable: true})
			return c.Dst(), kindUnit, false, true
		}
	}
	var impls []ir.DebugImpl
	nested := false
	if ok && debug && !irDebugValueKind(k) {
		impls, nested = bl.nestedDebugImpls(k)
	}
	if ok && !irScalarLeafKind(k) && !isDecimalKind(k) && !(debug && irDebugValueKind(k)) && !nested {
		// Any other value renders as `Debug.inspect(x)` or `"${x}"` would:
		// through the value's own impl, std's instance for a container, or
		// structurally. The operand is already lowered, so the rendering is
		// built over it rather than over a second evaluation.
		var text ir.Temp
		var rendered bool
		if debug {
			text, rendered = bl.debugOver(t.Args[0], src, k)
		} else {
			text, rendered = bl.displayHole(t.Args[0], src, k, true)
		}
		if !rendered {
			return no()
		}
		c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(key, key), text)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindUnit, deferrable: true})
		return c.Dst(), kindUnit, false, true
	}
	if !ok || (!irScalarLeafKind(k) && !isDecimalKind(k) && !(debug && irDebugValueKind(k)) && !nested) {
		return no()
	}
	if debug || k != kindString {
		var r *ir.Render
		at := ast.Node(t.Args[0])
		if debug && len(impls) > 0 {
			at = t
			r = ir.NewRenderDebugWith(bl.g.irNodePos(t), bl.f.NewTemp(), src, impls)
		} else if debug {
			at = t
			r = ir.NewRenderDebug(bl.g.irNodePos(t), bl.f.NewTemp(), src)
		} else {
			r = ir.NewRenderDisplay(bl.g.irNodePos(at), bl.f.NewTemp(), src)
		}
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		src = r.Dst()
	}
	c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(key, key), src)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindUnit, deferrable: true})
	return c.Dst(), kindUnit, false, true
}

// debugOver renders an already-lowered value as `Debug.inspect(at)` does:
// the synthesized structural render, std's instance for a container, or the
// value's own impl.
func (bl *irScalarBuilder) debugOver(at ast.Node, src ir.Temp, k kind) (ir.Temp, bool) {
	line, col := nodePos(at)
	call := &ast.Call{
		Func: &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: "Debug", Line: line, Col: col},
			Field:  &ast.Ident{Name: "inspect", Line: line, Col: col},
			Line:   line, Col: col,
		},
		Args: []ast.Node{at},
		Line: line,
		Col:  col,
	}
	args := irQualArgs{temps: []ir.Temp{src}, kinds: []kind{k}, mobile: []bool{true}, ok: true}
	if v, rk, _, ok := bl.synthDebugRender(call, args, "Debug", "inspect"); ok {
		return v, rk == kindString
	}
	if v, rk, _, ok, handled := bl.ifaceContainerCall(call, args, "Debug", "inspect"); handled {
		return v, ok && rk == kindString
	}
	plan := bl.qualImplPlan(call, args, "Debug", "inspect")
	if plan == nil || plan.result != kindString {
		return ir.NoTemp, false
	}
	v, _, _, ok := bl.qualEmit(call, args, plan)
	return v, ok
}

// namedRenderPlan calls a named value's local Display impl, as displayText
// does, or a retained struct's or enum's Debug impl through the native named
// inspector.
func (bl *irScalarBuilder) namedRenderPlan(k kind, debug bool) *irQualPlan {
	if debug {
		if p := bl.stdDebugPlan(k); p != nil {
			return p
		}
		if !irRetainedStructKind(k.def) && !irRetainedEnumKind(k.def) && !irCompositeDistinct(k.def) && !irWrappingDistinct(k.def) {
			return nil
		}
		plan, _ := bl.structDebugPlan(k)
		return plan
	}
	impl := bl.g.implsByIface["Display"][k]
	if impl == nil || !impl.lowerable {
		return nil
	}
	it := impl.items["to_string"]
	if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != 1 || it.params[0] != k || it.result != kindString {
		return nil
	}
	return &irQualPlan{token: it, name: k.nomi() + ".to_string", result: kindString}
}

// irScalarLeafKind is the scalar domain this shape admits: Int, Float, Bool
// and String. Float sits beside Int because `irArithKind` answers for both
// and `rt` has a shape for each operator in each.
//
// Decimal is outside it deliberately — a third arithmetic domain and a
// literal carried as exact text — and so is every named, container and
// existential kind.
func irScalarLeafKind(k kind) bool {
	switch k {
	case kindInt, kindFloat, kindBool, kindString:
		return true
	}
	return false
}

// irScalarPlan is one retained function.
type irScalarPlan struct {
	fn *ir.Func
	// result is the kind the body produced.
	result kind
}

// irScalarBody is the statement list this shape admits, or nil.
//
// N-1 statements and then one expression. The leading run admits bindings,
// destructures, `defer`, block statements, app-field writes and expression
// statements.
//
// The final statement is unwrapped: an ExprStmt's expression is the node both
// the cursor and the assignment are taken from.
func (g *gen) irScalarBody(fd *ast.FuncDef) ([]ast.Node, ast.Node) {
	return g.irScalarBlock(fd.Body, irDeclineSynthMask(fd))
}

func (g *gen) irScalarBlock(block *ast.Block, synthMask string) ([]ast.Node, ast.Node) {
	irDeclineBodyWhy = "the statement list"
	if block == nil {
		irDeclineBodyWhy = "no body"
		return nil, nil
	}
	if len(block.Stmts) == 0 {
		// `fn main() {}`: an empty block answers Unit.
		return nil, &ast.TypeIdent{Name: "Unit", Line: block.Line, Col: block.Col}
	}
	// A block also has a scope, its own type scope and scope exits. The
	// type scope emits nothing: the caller that lowers the statements makes
	// the block's types visible while it does (enterBlockTypes), and a name
	// lowered without them declines where it is used. A `defer`'s exit is an
	// `ir.RunDefer` its block's lowering appends. See irdefer.go.
	last := len(block.Stmts) - 1
	lead := make([]ast.Node, 0, last)
	for _, s := range block.Stmts[:last] {
		switch st := s.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias:
			// A block-local type declaration runs nothing: its values' layout
			// and identity come from the typeDef blocklocaltype.go resolved,
			// visible while the block's type scope is pushed.
			continue
		case *ast.DistinctDestructure, *ast.TupleDestructure, *ast.StructDestructure, *ast.MapDestructure, *ast.Defer, *ast.With, *ast.Block, *ast.FuncDef, *ast.ImportStmt,
			// An assertion in an ordinary `fn`, whose failure is the
			// function's `Err(AssertionFailure)`; `leading` checks the
			// checker's boundary.
			*ast.Assertion, *ast.PatternDestructure,
			// A pattern binding, whose else branches off the block.
			*ast.PatternBinding:
			lead = append(lead, st)
		case *ast.Binding:
			// A discard name binds nothing and never enters scope, which is
			// why the rebind check in `irScalarBuild` does not apply to it.
			// See irdiscard.go.
			lead = append(lead, st)
		case *ast.ExprStmt:
			if isNilNode(st.Expr) {
				irDeclineBodyWhy = "a nil leading expression statement"
				return nil, nil
			}
			lead = append(lead, st.Expr)
		default:
			irDeclineBodyWhy = fmt.Sprintf("a leading statement form outside the shape: %T", s) + synthMask
			return nil, nil
		}
	}
	n := block.Stmts[last]
	if es, isExpr := n.(*ast.ExprStmt); isExpr {
		n = es.Expr
	}
	if isNilNode(n) {
		irDeclineBodyWhy = "a nil final statement"
		return nil, nil
	}
	if w, isWith := n.(*ast.With); isWith {
		// A `with` ending a block: the override holds to the block's end,
		// which is right after it, and the block answers Unit.
		return append(lead, w), &ast.TypeIdent{Name: "Unit", Line: w.Line, Col: w.Col}
	}
	if b, isBinding := n.(*ast.Binding); isBinding && ast.IsDiscardName(b.Name) && b.TypeAnnotation == nil {
		// `_ = Sender.send(ch.sender, 1)` ending a block: the value is
		// evaluated and dropped, and the block answers Unit, as a block
		// does after a binding statement.
		return append(lead, b), &ast.TypeIdent{Name: "Unit", Line: b.Line, Col: b.Col}
	}
	switch st := n.(type) {
	case *ast.DistinctDestructure, *ast.TupleDestructure, *ast.StructDestructure, *ast.MapDestructure, *ast.PatternBinding:
		// A destructuring binding ending a block, `(_, _) = pair`: the value
		// is evaluated and matched, and the block answers Unit, as it does
		// after any binding statement.
		line, col := nodePos(st)
		return append(lead, st), &ast.TypeIdent{Name: "Unit", Line: line, Col: col}
	}
	return lead, n
}

// irScalarBuild builds one function body into a retained `ir.Func`, or
// declines.
//
// It declines rather than refusing. The caller records the decline reason, and
// the VM reports the body as BLOCKED if a program reaches it (irnative.go).
func (g *gen) irScalarBuild(fd *ast.FuncDef, sig irFuncSig, plan *tailPlan, sh *irFuncShell) (*irScalarPlan, bool) {
	g.irDeclineOpen(fd.Name)
	switch {
	case sh == nil:
		irDeclineNote("no function shell")
		return nil, false
	// A discarded lowering (`g.probing`, `g.stdSettling`) is handled by
	// `irScalarLower` ahead of the observation hook rather than here.
	case plan != nil:
		// A body with a tail plan is `tail`'s shape, not this one.
		irDeclineNote("a tail plan")
		return nil, false
	case sig.dicts != 0:
		// A bounded generic takes dictionaries, and a type parameter has no
		// representation in this IR at all.
		irDeclineNote("a bounded generic's dictionaries")
		return nil, false
	case !sh.patternOK:
		// A destructuring parameter that did not lower leaves the prologue
		// PARTIAL — some names bound at an invalid kind, some projections
		// never built — so the graph would have a hole in the prologue.
		// `funcDecl` reports the pattern and sets the
		// function's produced kind invalid; this declines the retention.
		irDeclineNote("a destructuring parameter that did not lower")
		return nil, false
	}
	// Types the body declares resolve by name for the extent of the body,
	// as they do for the front end (blocklocaltype.go).
	defer g.enterBlockTypes(fd.Body)()
	lead, body := g.irScalarBody(fd)
	if body == nil {
		irDeclineNote(irDeclineBodyWhy)
		return nil, false
	}
	if block, isBlock := body.(*ast.Block); isBlock && sig.result == kindUnit {
		// A Unit function ending in a block statement: the block runs in
		// its own scope (scopedBlock, which restores a `with`'s overrides
		// at its exit) and the function answers Unit.
		lead = append(lead, block)
		body = &ast.TypeIdent{Name: "Unit", Line: block.Line, Col: block.Col}
	}
	if sig.result == kindUnit && analysis.EndsInDbg(body) {
		// A Unit function ending in a `dbg` observation: the observation
		// runs as a statement, its value is discarded, and the function
		// answers Unit, as the checker types it.
		line, col := nodePos(body)
		lead = append(lead, body)
		body = &ast.TypeIdent{Name: "Unit", Line: line, Col: col}
	}
	declLine, _ := nodePos(fd)
	bl := &irScalarBuilder{
		g: g, sh: sh, f: sh.fn, b: sh.entry,
		returnKind: sig.result,
		sides:      sh.sides,
		bound:      map[string]ir.Temp{}, boundK: map[string]kind{},
		synthesized: analysis.IsSynthesizedLine(declLine),
		deferOK:     true,
		loopsOK:     !g.irBoot,
		boot:        g.irBoot,
		body:        fd.Body,
	}
	if form := irNamedCtlForm(fd); form != irCtlNone && len(sh.paramTemps) == len(fd.Params) {
		// An iter-sensitive function signals as a callback lambda does:
		// `continue` and `break` leave it with rt's control answer.
		bl.ctl = form
		if form == irCtlReduce {
			bl.ctlAcc = sh.paramTemps[0]
		}
	}
	// A DESTRUCTURED NAME IS A NAME THIS FUNCTION BOUND, which is what makes
	// the body's read of it the Bind's own temporary rather than an
	// unresolvable `ir.Ref`. The prologue's `ir.Bind` nodes are the record
	// of which names a destructuring parameter introduced, so this reads
	// them off the graph instead of keeping a second list. See irparam.go,
	// and `ir.Lint`'s RuleLocalDeclared for how a missed one is reported.
	for _, b := range sh.irPrologueBinds() {
		k := sh.frame.kindOf(b.Dst())
		if !irRetainedValueKind(k) {
			// A destructured name outside the retained value domain is
			// outside the shape, exactly as a parameter of that kind is.
			irDeclineNote("a destructured name's kind outside the domain: " + k.nomi())
			return nil, false
		}
		bl.bound[b.Sym().Name()], bl.boundK[b.Sym().Name()] = b.Dst(), k
	}
	scope := bl.openDeferScope(fd.Body)
	bl.openWithScope(true)
	if !bl.leading(lead) {
		return nil, false
	}
	line, _ := nodePos(body)
	if !bl.positionOK(line) {
		// A synthesized body would be `ir.AtSynthesized` throughout, which
		// is representable, but `gen.at` ignores such a line so the cursor
		// claim the whole shape rests on would not hold. Declined by name.
		irDeclineNote("the final expression is on a synthesized line")
		return nil, false
	}
	if ret, isReturn := body.(*ast.Return); isReturn && scope != nil && len(scope.ids) != 0 {
		// `defer close(conn); return 5` ending the body: a final `return v`
		// is the body's value v, so the deferred calls run after it is
		// computed and before the function answers, as for a tail `v`.
		if isNilNode(ret.Value) {
			body = &ast.TypeIdent{Name: "Unit", Line: ret.Line, Col: ret.Col}
		} else {
			body = ret.Value
		}
	}
	val, k, ok := bl.tail(body, sig)
	if !ok {
		return nil, false
	}
	// A branching or returning tail leaves through Returns the tail's
	// region set; the VM runs the pending deferred calls at every exit of
	// the activation, after the returned value is computed.
	if val != ir.NoTemp {
		// The body's answer goes into the function's own result slot, which
		// `irFuncShellFor` declared, and the exit returns the slot.
		bl.resultCopy(body, val)
		if !bl.boot {
			// A boot's own deferred calls are the app's cleanup: the VM hands
			// the ones still pending when boot answers to rt's boot cleanup,
			// which runs them when the app ends (vm.Machine.finish).
			bl.closeDefers(scope, body)
		}
		bl.side(sh.result, irScalarSide{k: k})
		bl.b.SetTerm(ir.NewReturn(g.irNodePos(body), sh.result))
	}
	// An app-field write is admitted only in the body's own statements or a
	// scoped block's (appFieldWrite), whose extents the VM gives it: the
	// activation, or the block up to its StoreScope. A deferred call runs at
	// its block's exit with the writes made before it in force, and a branch or a return after a top-level write leaves the write
	// in force as the rest of the body does.
	return &irScalarPlan{fn: sh.fn, result: k}, true
}

// inSynthesized reports whether this builder belongs to a derive-synthesized
// declaration.
func (bl *irScalarBuilder) inSynthesized() bool {
	for b := bl; b != nil; b = b.parent {
		if b.synthesized {
			return true
		}
	}
	return false
}

// positionOK reports whether a node's line can carry a retained position:
// an emittable source line, or a synthesized line inside a synthesized
// declaration.
func (bl *irScalarBuilder) positionOK(line int) bool {
	if emittableLine(line) {
		return true
	}
	return bl.inSynthesized() && analysis.IsSynthesizedLine(line)
}

// resultCopy records delivery on the write, not its shared destination or reused
// source. The IR position remains the expression's actual source position.
func (bl *irScalarBuilder) resultCopy(n ast.Node, val ir.Temp) {
	cp := ir.NewCopy(bl.g.irNodePos(n), bl.sh.result, val)
	bl.b.Append(cp)
}

// tail lowers the body's final expression.
//
// It answers `ir.NoTemp` for a construct that has already written the result
// slot and terminated every block it opened — an `if` or a `case` in tail
// position — and a temporary for an ordinary expression the caller copies.
func (bl *irScalarBuilder) tail(body ast.Node, sig irFuncSig) (ir.Temp, kind, bool) {
	switch t := body.(type) {
	case *ast.Return:
		if bl.inTest {
			return ir.NoTemp, kindInvalid, false
		}
		k, ok := bl.explicitReturn(t)
		return ir.NoTemp, k, ok
	case *ast.If:
		k, ok := bl.tailIf(t, sig)
		return ir.NoTemp, k, ok
	case *ast.Case:
		k, ok := bl.tailCase(t, sig)
		return ir.NoTemp, k, ok
	}
	val, k, _, ok := bl.lowerWant(body, sig.result)
	if !ok || k != sig.result {
		// The declared result must be what the body produces. `gen.assign`
		// otherwise emits `_ = <expr>` and the caller reports a return-type
		// mismatch, which is a refusal path and not this shape.
		if ok {
			irDeclineNote("the tail's kind is not the declared result: " +
				k.nomi() + " vs " + sig.result.nomi())
		}
		return ir.NoTemp, kindInvalid, false
	}
	return val, k, true
}

// tailIf lowers a terminal Boolean conditional. Explicit returning arms exit
// independently; ordinary value arms write the result slot and join at an exit.
//
// BOTH ARMS ARE REQUIRED. An `if` with no `else` produces Unit whatever the
// then-arm produces, and a Unit-producing tail
// `if` is a shape this builder would have to reconcile against a declared
// result — two answers for one construct. Declined.
func (bl *irScalarBuilder) tailIf(t *ast.If, sig irFuncSig) (kind, bool) {
	if then, els := irReturningArms(t); then != nil && els != nil {
		return bl.returnBranches(t, then, els)
	}
	k, ok := bl.ifRegion(t, sig)
	if ok {
		bl.exitReturn(t)
	}
	return k, ok
}

// exitReturn terminates a tail region's exit, which returns the result slot
// its arms wrote. When every arm left the activation (an `else if` chain or a
// `case` whose arms all end in `return`), no arm reaches the exit and none
// wrote the slot, so a `return` of it would read a temporary nothing defines.
// That exit is unreachable, and it jumps to itself: a terminator that reads
// nothing.
func (bl *irScalarBuilder) exitReturn(n ast.Node) {
	pos := bl.g.irNodePos(n)
	if !ir.Reachable(bl.f)[bl.b.ID()] {
		bl.b.SetTerm(ir.NewJump(pos, bl.b.ID()))
		return
	}
	bl.b.SetTerm(ir.NewReturn(pos, bl.sh.result))
}

// ifRegion leaves its join open for the caller's return or enclosing jump.
func (bl *irScalarBuilder) ifRegion(t *ast.If, sig irFuncSig) (kind, bool) {
	if t.CondPattern != nil {
		return bl.patternIfRegion(t, sig)
	}
	// An `if` with no `else` is Unit whichever way it goes, so a
	// Unit-valued region may omit it: the else edge writes Unit.
	unitElse := t.Else == nil && !sig.testArms && (sig.inferResult || sig.result == kindUnit)
	if isNilNode(t.Cond) || (t.Else == nil && !sig.testArms && !unitElse) {
		irDeclineNote("a tail `if` without a condition or `else`")
		return kindInvalid, false
	}
	cond, ck, _, ok := bl.lower(t.Cond)
	if !ok || ck != kindBool {
		if ok {
			irDeclineNote("a tail `if` condition that is not Bool: " + ck.nomi())
		}
		return kindInvalid, false
	}
	then := bl.f.NewBlock(bl.g.irNodePos(t.Then), "then")
	elsAt := t.Else
	if elsAt == nil {
		elsAt = t
	}
	els := bl.f.NewBlock(bl.g.irNodePos(elsAt), "else")
	exit := bl.f.NewBlock(bl.g.irNodePos(t), "exit")
	br := ir.NewBranch(bl.g.irPos(t.Line, t.Col), cond, then.ID(), els.ID())
	bl.b.SetTerm(br)
	// The Go `if` is mapped to where the condition left the cursor, which is
	// not the `if`'s own line when the condition is a piped value from an
	// earlier line or spans lines.

	tk, ok := bl.armInto(then, exit, t.Then, sig)
	if !ok {
		return kindInvalid, false
	}
	ek, ok := bl.testElse(els, exit, t, tk, bl.laterArmSig(sig, tk))
	if !ok {
		return kindInvalid, false
	}
	k, agree := irJoinArms(tk, ek, sig)
	if !agree {
		irDeclineNote("a tail `if` whose arms disagree: " + tk.nomi() + " vs " + ek.nomi())
		return kindInvalid, false
	}
	bl.b = exit
	return k, true
}

// kindDiverged is the kind armInto answers for an arm that leaves the
// activation (`return v`), or whose nested region's arms all do, and so
// writes no value to its region. irJoinArms resolves it against the other
// arms. It escapes a region only when every arm diverged and the region has
// no declared result: the region's own caller then decides what that means
// (a lambda or `concurrent` body takes the kind its returns settled).
var kindDiverged = kind{tag: tagNamed, def: &typeDef{nomi: "<diverged>"}}

// laterArmSig is the signature an inferred region's later arm is lowered
// with once an earlier arm settled the region's kind as prior: that kind is
// the arm's expected type, as the checker gives `None` in `if c { Some(1) }
// else { None }` the then-arm's `Maybe<Int>`.
func (bl *irScalarBuilder) laterArmSig(sig irFuncSig, prior kind) irFuncSig {
	// kindInvalid: sentinel — an earlier arm that settled no kind gives no expected type.
	unsettled := prior == kindInvalid
	if !sig.inferResult || bl.ctl != irCtlNone || prior == kindDiverged || prior == kindEmptyList ||
		unsettled || !irCallableValueKind(prior) {
		return sig
	}
	sig.inferResult, sig.result = false, prior
	return sig
}

// irJoinArms is the kind of a region with arms of kinds a and b, where an
// arm that diverged takes the other's. When every arm diverged the region's
// exit is unreachable and it answers the declared result, or kindDiverged
// when the result is inferred.
func irJoinArms(a, b kind, sig irFuncSig) (kind, bool) {
	switch {
	case a == kindDiverged && b == kindDiverged:
		if sig.inferResult {
			return kindDiverged, true
		}
		return sig.result, true
	case a == kindDiverged:
		return b, true
	case b == kindDiverged:
		return a, true
	}
	return a, a == b
}

// testElse lowers an `if`'s else arm. A test-body statement `if` may have
// none, and its else edge then joins the exit directly.
func (bl *irScalarBuilder) testElse(els, exit *ir.Block, t *ast.If, then kind, sig irFuncSig) (kind, bool) {
	if t.Else == nil && sig.testArms {
		els.SetTerm(ir.NewJump(bl.g.irNodePos(t), exit.ID()))
		return then, true
	}
	if t.Else == nil {
		// A Unit-valued `if` without `else`: the missing arm answers Unit.
		prev := bl.b
		bl.b = els
		u := ir.NewUnit(bl.g.irNodePos(t), bl.f.NewTemp())
		bl.b.Append(u)
		bl.resultCopy(t, u.Dst())
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(t), exit.ID()))
		bl.b = prev
		return kindUnit, true
	}
	return bl.armInto(els, exit, t.Else, sig)
}

// armInto lowers an arm into the result slot and jumps to exit. Ordinary
// function and lambda arms carry leading statements in isolated lexical scopes.
func (bl *irScalarBuilder) armInto(arm, exit *ir.Block, body ast.Node, sig irFuncSig) (kind, bool) {
	if isNilNode(body) {
		irDeclineNote("an arm body that is not one expression")
		return kindInvalid, false
	}
	line, _ := nodePos(body)
	if !bl.positionOK(line) {
		irDeclineNote("an arm body on a synthesized line")
		return kindInvalid, false
	}
	prevB := bl.b
	bl.b = arm
	defer func() { bl.b = prevB }()
	lp := bl.loopArms
	bl.loopArms = nil
	defer func() { bl.loopArms = lp }()
	if sig.testArms {
		return bl.testArm(exit, body)
	}
	// Arms are distinct lexical scopes. Copies of the symbol table preserve
	// outer identities while giving same-spelled sibling bindings new symbols.
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	if block, ok := body.(*ast.Block); ok && len(block.Stmts) == 0 && bl.g.blockTypes[block] == nil &&
		(sig.inferResult || sig.result == kindUnit) {
		// An empty arm, `if False {}`: the block's value is Unit.
		u := ir.NewUnit(bl.g.irNodePos(block), bl.f.NewTemp())
		bl.b.Append(u)
		bl.resultCopy(block, u.Dst())
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(block), exit.ID()))
		return kindUnit, true
	}
	// An arm block is a lexical scope: its deferred calls run at its exit,
	// after its value is written, or at whatever exit the activation takes
	// from inside it.
	var scope *irDeferScope
	var ws *irWithScope
	if block, ok := body.(*ast.Block); ok {
		defer bl.g.enterBlockTypes(block)()
		lead, tail := bl.g.irScalarBlock(block, "")
		if tail == nil {
			return kindInvalid, false
		}
		scope = bl.openDeferScope(block)
		ws = bl.openWithScope(false)
		if !bl.leading(lead) {
			return kindInvalid, false
		}
		body = tail
	}
	if brk, isBreak := body.(*ast.Break); isBreak && lp != nil {
		if !bl.loopBreakArm(brk, lp, arm) {
			return kindInvalid, false
		}
		return lp.k, true
	}
	// kindInvalid: sentinel — only ordinary lambdas may infer an unset result.
	returnKnown := bl.returnKind != kindInvalid || bl.inferReturn != nil
	if ret, isReturn := body.(*ast.Return); isReturn && !isNilNode(ret.Value) && !bl.inTest && returnKnown {
		// `return v` ending an arm leaves the activation (the enclosing
		// lambda or function, the innermost one) with v. The arm writes nothing and never reaches
		// the region's exit, so its kind is the other arms'.
		if _, ok := bl.explicitReturn(ret); !ok {
			return kindInvalid, false
		}
		return kindDiverged, true
	}
	if ret, isReturn := body.(*ast.Return); isReturn && isNilNode(ret.Value) && bl.inferReturn != nil && !bl.inTest {
		// A bare `return` ending an arm of a lambda leaves the lambda with
		// Unit, which is the arm's value when the lambda's result is Unit.
		if !sig.inferResult && sig.result != kindUnit {
			return kindInvalid, false
		}
		if _, ok := bl.explicitReturn(ret); !ok {
			return kindInvalid, false
		}
		return kindUnit, true
	}
	if ret, isReturn := body.(*ast.Return); isReturn && isNilNode(ret.Value) && bl.inferReturn == nil &&
		bl.returnKind == kindUnit && !bl.inTest {
		// A bare `return` ending an arm of a Unit function leaves it, as a
		// guard's does; the arm reaches no exit.
		if _, ok := bl.explicitReturn(ret); !ok {
			return kindInvalid, false
		}
		return kindDiverged, true
	}
	if _, isBreak := body.(*ast.Break); (isBreak || isContinueNode(body)) && bl.ctl != irCtlNone && !sig.inferResult {
		// A signalling callback's `break v` or `continue` ending an arm
		// leaves the callback, as one ending a guard arm does (ctlReturn).
		if !bl.ctlReturn(body) {
			return kindInvalid, false
		}
		return sig.result, true
	}
	// A nested branch writes the enclosing arm's destination directly. Only
	// a branch used as an operand needs an additional result slot.
	var k kind
	var ok, region bool
	// A nested branch in a loop's tail position is still the tail: its
	// arms' `break` leaves the loop.
	switch nested := body.(type) {
	case *ast.If:
		bl.loopArms = lp
		k, ok = bl.ifRegion(nested, sig)
		bl.loopArms = nil
		region = true
	case *ast.Case:
		bl.loopArms = lp
		k, ok = bl.caseRegion(nested, sig)
		bl.loopArms = nil
		region = true
	}
	if region {
		if !ok {
			return kindInvalid, false
		}
		if k == kindDiverged || !ir.Reachable(bl.f)[bl.b.ID()] {
			// Every arm of the nested region left the activation, so its
			// exit is unreachable and this arm, like a `return` arm, writes
			// no value: `else if c { return 1 } else { return 2 }`.
			bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(body), bl.b.ID()))
			return kindDiverged, true
		}
		bl.closeDefers(scope, body)
		bl.closeWithScope(ws, body)
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(body), exit.ID()))
		return k, true
	}
	var val ir.Temp
	if sig.inferResult {
		val, k, _, ok = bl.lower(body)
	} else {
		val, k, _, ok = bl.lowerWant(body, sig.result)
	}
	if !ok || (!sig.inferResult && k != sig.result) {
		if ok {
			irDeclineNote("an arm's kind is not the declared result: " +
				k.nomi() + " vs " + sig.result.nomi())
		}
		return kindInvalid, false
	}
	bl.resultCopy(body, val)
	bl.closeDefers(scope, body)
	bl.closeWithScope(ws, body)
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(body), exit.ID()))
	return k, true
}

// tailCase lowers `case subj { lit -> e ... _ -> e }` in tail position.
//
// Answering MatchLit instructions supply each Branch condition. Tuple arms
// chain their component tests, with every failed test selecting the next arm.
// Each successful body writes the result slot and jumps to one exit. A final
// wildcard or flat irrefutable tuple supplies the exhaustive arm; other pattern
// forms remain outside this retained region.
//
// The subject is evaluated once and held when its Go spelling is not a bare
// identifier. The existing hold delivery preserves this for calls, arithmetic
// and concatenation as well as field reads.
func (bl *irScalarBuilder) tailCase(t *ast.Case, sig irFuncSig) (kind, bool) {
	k, ok := bl.caseRegion(t, sig)
	if ok {
		bl.exitReturn(t)
	}
	return k, ok
}

// caseRegion leaves its join open for the caller's continuation.
func (bl *irScalarBuilder) caseRegion(t *ast.Case, sig irFuncSig) (kind, bool) {
	if len(t.Branches) < 1 {
		irDeclineNote("a tail `case` with no branches")
		return kindInvalid, false
	}
	adHoc := isNilNode(t.Value)
	if !adHoc && bl.isNeverOperand(t.Value) {
		return bl.neverCaseRegion(t, sig)
	}
	var subj ir.Temp
	var sk kind
	if !adHoc {
		var ok bool
		subj, sk, _, ok = bl.lower(t.Value)
		if !ok || (sk != kindInt && sk != kindString && sk != kindBool && !irLiteralCaseKind(sk, bl) &&
			(!irRetainedTupleKind(sk) && !irRetainedListKind(sk) && !irListTransportKind(sk) && !(sk.tag == tagNamed && irRetainedEnumKind(sk.def)) && !(sk.tag == tagMap && irRetainedMapKind(sk)) && !irNominalCaseKind(sk))) {
			if ok {
				irDeclineNote("a tail `case` scrutinee outside retained literal-pattern kinds: " + sk.nomi())
			}
			return kindInvalid, false
		}
		if !irHeldValue(bl.f, subj, bl.sides) {
			// Every non-identifier subject is held in a name once, so repeated
			// arm comparisons read the same value.
			c := ir.NewCopy(bl.g.irNodePos(t.Value), bl.f.NewTemp(), subj)
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: sk, copy: irCopyHold})
			subj = c.Dst()
		}
		// Inside an assertion subject the scrutinee is a `values:` row,
		// recorded before any arm runs.
		if t != bl.pipedCase {
			bl.record(t.Value, subj, sk, true)
		}
	}
	if bl.casePrefix == nil {
		bl.casePrefix = map[*ir.Block]int{}
	}
	bl.casePrefix[bl.b] = len(bl.b.Instrs())
	exit := bl.f.NewBlock(bl.g.irNodePos(t), "exit")
	// Every case owns a NoMatch block at the case's source position. A final
	// tested arm reaches it on failure; an unconditional fallback leaves it
	// unreachable. Its syntactic jump records the region's join, but NoMatch
	// diverges and contributes no normal-flow predecessor to that join.
	nomatch := bl.f.NewBlock(bl.g.irNodePos(t), "nomatch")
	nomatch.Append(ir.NewNoMatch(bl.g.irNodePos(t)))
	nomatch.SetTerm(ir.NewJump(bl.g.irNodePos(t), exit.ID()))
	var result kind
	have := false
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	for i := range t.Branches {
		bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
		br := &t.Branches[i]
		last := i == len(t.Branches)-1
		arm, next, ok := bl.caseArmTest(t, br, adHoc, subj, sk, last, nomatch)
		if !ok {
			return kindInvalid, false
		}
		armSig := sig
		if have {
			armSig = bl.laterArmSig(sig, result)
		}
		k, aok := bl.armInto(arm, exit, br.Body, armSig)
		if !aok {
			return kindInvalid, false
		}
		if k != kindDiverged {
			if have && sig.inferResult && k != result {
				// An inferred result whose arms are `[]` and a list: the empty
				// list is a value of the list's kind.
				if k == kindEmptyList && result.tag == tagList {
					k = result
				} else if result == kindEmptyList && k.tag == tagList {
					result = k
				}
			}
			if have && k != result {
				irDeclineNote("two `case` arms disagree: " + k.nomi() + " vs " + result.nomi())
				return kindInvalid, false
			}
			result, have = k, true
		}
		if next == nil {
			// An unguarded irrefutable arm matches every value, so the arms
			// after it never run: the first match wins.
			break
		}
	}
	bl.b = exit
	if !have {
		// Every arm left the activation.
		return irJoinArms(kindDiverged, kindDiverged, sig)
	}
	return result, true
}

// litFor lowers one literal pattern as the operand a MatchLit compares
// against. A literal is its own instruction writing a temporary, which is
// ir.go's rule for every operand shape.
func (bl *irScalarBuilder) litFor(pat ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	line, _ := nodePos(pat)
	if !bl.positionOK(line) {
		return ir.NoTemp, kindInvalid, false, false
	}
	switch pat.(type) {
	case *ast.IntLit, *ast.StringLit, *ast.CodepointLit:
		return bl.lower(pat)
	case *ast.FloatLit, *ast.DecimalLit:
		// The VM compares with rt.Equal, whose structural equality is
		// EqFloat and EqDecimal here.
		if irLiteralCaseKind(want, bl) {
			return bl.lower(pat)
		}
	}
	return ir.NoTemp, kindInvalid, false, false
}

// colOf is a node's column, for a position taken beside a line already read.
func colOf(n ast.Node) int {
	_, col := nodePos(n)
	return col
}

// leading shares fresh bindings and effects between named and anonymous bodies.
//
// A statement that declines where `lower` gave the decline no position (a
// nested `fn`, a rebinding in a test) is where the decline is reported.
func (bl *irScalarBuilder) leading(lead []ast.Node) (lowered bool) {
	var cur ast.Node
	defer func() {
		if !lowered && cur != nil {
			irDeclineAtNode(cur)
		}
	}()
	// The deferred calls of THIS run's block. A nested block's statements
	// register in their own scope, which that block's lowering opens.
	scope := bl.deferScope
	bl.deferScope = nil
	ws := bl.withScope
	bl.withScope = nil
	for _, s := range lead {
		cur = s
		line, _ := nodePos(s)
		if !bl.positionOK(line) {
			irDeclineNote("a lead statement on a synthesized line")
			return false
		}
		if conditional, ok := s.(*ast.If); ok {
			if bl.guardShape(conditional) {
				if !bl.guardReturn(conditional) {
					irDeclineNote("a leading conditional outside a straight return guard")
					return false
				}
				continue
			}
			// Any other `if` statement is a region whose value is
			// dropped, as a `case` statement is.
			val, k, _, ok := bl.inferredRegionValue(conditional, true)
			if !ok {
				return false
			}
			bl.irStatementDrop(conditional, val, k)
			continue
		}
		if match, ok := s.(*ast.Case); ok {
			val, k, _, ok := bl.inferredRegionValue(match, true)
			if !ok {
				return false
			}
			bl.irStatementDrop(match, val, k)
			continue
		}
		if imp, ok := s.(*ast.ImportStmt); ok {
			if !bl.testImport(imp) {
				return false
			}
			continue
		}
		if d, ok := s.(*ast.Defer); ok {
			if !bl.deferCall(d, scope) {
				return false
			}
			continue
		}
		if w, ok := s.(*ast.With); ok {
			if !bl.withStmt(w, ws) {
				return false
			}
			continue
		}
		if block, ok := s.(*ast.Block); ok {
			if !bl.scopedBlock(block) {
				return false
			}
			continue
		}
		if distinct, ok := s.(*ast.DistinctDestructure); ok {
			if !bl.distinctBinding(distinct) {
				return false
			}
			continue
		}
		if tuple, ok := s.(*ast.TupleDestructure); ok {
			if !bl.tupleBinding(tuple) {
				return false
			}
			continue
		}
		if destructure, ok := s.(*ast.StructDestructure); ok {
			if !bl.structBinding(destructure) {
				return false
			}
			continue
		}
		if destructure, ok := s.(*ast.MapDestructure); ok {
			if !bl.mapBinding(destructure) {
				return false
			}
			continue
		}
		if fd, ok := s.(*ast.FuncDef); ok {
			if !bl.nestedFunc(fd) {
				return false
			}
			continue
		}
		if a, ok := s.(*ast.Assertion); ok {
			if !bl.fnAssertion(a.Line, a.Col) || !bl.assertion(a) {
				return false
			}
			continue
		}
		if pd, ok := s.(*ast.PatternDestructure); ok {
			if !bl.fnAssertion(pd.AssertLine, pd.AssertCol) || !bl.patternAssert(pd) {
				return false
			}
			continue
		}
		if pb, ok := s.(*ast.PatternBinding); ok {
			if !bl.patternBindingStmt(pb, nil) {
				return false
			}
			continue
		}
		b, isBinding := s.(*ast.Binding)
		if !isBinding {
			// AN EXPRESSION STATEMENT, lowered for its effect. A Unit-valued
			// one drops nothing: the IR records no discard, an unread
			// temporary is legal here, and the `_ = <call>` a Go statement
			// needs rides on the `ir.Call` itself (`irCallDiscarded`).
			//
			// A non-Unit member: `dbg n` is transparent, so it answers the
			// operand's kind and Go needs `_ = n2_n` after the print.
			// `irStatementDrop` is that statement, as a recorded `ir.Copy`
			// delivery; see irdiscard.go and ircopydelivery.go.
			prevDiscarded := bl.discarded
			bl.discarded = s
			val, k, _, ok := bl.lower(s)
			bl.discarded = prevDiscarded
			if !ok {
				return false
			}
			bl.irStatementDrop(s, val, k)
			continue
		}
		if ast.IsDiscardName(b.Name) {
			// AN EXPLICITLY DISCARDED STATEMENT — `_ = expr`, `_name = expr`.
			// It declares nothing, so there is no `ir.Bind` and no interned
			// symbol; the whole of it is the value and the drop. See
			// irdiscard.go.
			prevDiscarded := bl.discarded
			bl.discarded = b.Value
			src, k, ok := bl.bindingExpression(b)
			bl.discarded = prevDiscarded
			if !ok {
				return false
			}
			bl.irStatementDrop(b, src, k)
			// And the statement's own Unit value, discarded after the
			// binding, as `irScalarBindLocal` does for a named binding.
			bl.irDiscardStmtUnit(b)
			continue
		}
		_, shadows := bl.g.lookup(b.Name)
		_, bound := bl.bound[b.Name]
		// A rebinding is a fresh identity, and reads in its initializer see
		// the previous one. A closure made before it captured the previous
		// value, which it keeps (spec §23), so nothing here depends on
		// whether one did.
		if (shadows || bound) && bl.inTest {
			irDeclineNote("a test-body rebind: " + b.Name)
			return false
		}
		src, k, ok := bl.bindingValue(b)
		if !ok {
			return false
		}
		// Reads in the initializer retain the previous identity. Only later
		// reads see the new value, even when Go reuses its storage.
		sym := ir.NewSymbol(b.Name)
		bl.sh.syms[b.Name] = sym
		bind := ir.NewBind(bl.g.irPos(line, colOf(b)), bl.f.NewTemp(), src, sym)
		bl.b.Append(bind)
		bl.side(bind.Dst(), irScalarSide{k: k})
		bl.bound[b.Name], bl.boundK[b.Name] = bind.Dst(), k
		// An assertion in this body that names the binding bare prints its
		// "defined as:" block.
		if bl.testDefs == nil {
			bl.testDefs = map[string]*ast.Binding{}
		}
		bl.testDefs[b.Name] = b
	}
	return true
}

// bindingValue declares a typed result before lowering a branch or block.
// Every path must produce exactly the checker's resolved binding kind.
func (bl *irScalarBuilder) bindingValue(b *ast.Binding) (ir.Temp, kind, bool) {
	_, conditional := b.Value.(*ast.If)
	_, match := b.Value.(*ast.Case)
	_, block := b.Value.(*ast.Block)
	if !conditional && !match && !block {
		return bl.bindingExpression(b)
	}
	var sym *analysis.Symbol
	if bl.g.fa != nil {
		sym = bl.g.fa.Definitions[analysis.Pos{Line: b.Line, Col: b.Col}]
	}
	if sym == nil {
		irDeclineNote("a branch or block binding with no checked definition")
		return ir.NoTemp, kindInvalid, false
	}
	k := bl.g.project(sym.Type)
	if seq, ok := bl.g.irSeqKindOf(sym.Type); ok {
		k = seq
	}
	return bl.typedRegionValue(b.Value, k)
}

// typedRegionValue gives a branch or block one immutable typed destination.
// Callers supply a checked binding type or an expected operand type.
func (bl *irScalarBuilder) typedRegionValue(value ast.Node, k kind) (ir.Temp, kind, bool) {
	ty := bl.g.irTypeOf(k)
	if ty == nil || (!irCallableValueKind(k) && k != kindUnit) {
		// The checked kind has no retained IR type, so the region has no
		// slot to deliver into.
		irDeclineNote("a branch or block value kind with no IR type: " + k.nomi())
		return ir.NoTemp, kindInvalid, false
	}
	result := bl.f.NewTemp()
	slot := ir.NewSlot(bl.g.irNodePos(value), result, ty)
	bl.b.Append(slot)
	bl.side(result, irScalarSide{k: k})
	outer := bl.sh.result
	bl.sh.result = result
	defer func() { bl.sh.result = outer }()
	var actual kind
	var ok bool
	switch v := value.(type) {
	case *ast.Block:
		actual, ok = bl.blockBinding(v, k)
	case *ast.If:
		actual, ok = bl.ifRegion(v, irFuncSig{result: k})
	case *ast.Case:
		actual, ok = bl.caseRegion(v, irFuncSig{result: k})
	}
	if !ok {
		irDeclineAtNode(value)
		bl.abandonRegion(value)
	}
	return result, actual, ok
}

// bindingExpression checks a straight initializer against its written annotation.
// The existing empty-list transfer is the only widening in this retained domain.
func (bl *irScalarBuilder) bindingExpression(b *ast.Binding) (ir.Temp, kind, bool) {
	if b.TypeAnnotation == nil {
		if lam, isLambda := b.Value.(*ast.Lambda); isLambda {
			// The lambda's result is the checker's, so an `if` arm's bare
			// `Maybe.None` takes the other arm's type (lambdaResultWant).
			defer bl.g.lambdaResultWant(lam, b.Line, b.Col)()
		}
		v, k, _, ok := bl.lower(b.Value)
		return v, k, ok
	}
	want := bl.g.typeOf(b.TypeAnnotation)
	// A local interface's existential (`widened: Clock = FakeClock{...}`)
	// holds the concrete value, as an interface-typed field does.
	existential := irExistentialKind(want)
	if !irCallableValueKind(want) && want != kindUnit && !existential {
		irDeclineNote("a binding annotation outside the retained value domain: " + want.nomi())
		return ir.NoTemp, kindInvalid, false
	}
	if list, isList := b.Value.(*ast.ListLit); isList && irAnnotatedNominalList(list, want) {
		v, k, _, ok := bl.listMakeOf(list, list.Items, nil, want.comp.parts[0])
		return v, k, ok
	}
	if existential {
		// The VM's existential is the concrete value, so the binding keeps
		// the concrete implementer's kind; every later use erases it where
		// the interface is expected, as the value itself would be.
		v, k, _, ok := bl.lowerTypedOperand(b.Value, want)
		if !ok || (k != want && !bl.g.irErases(want, k)) {
			return ir.NoTemp, kindInvalid, false
		}
		return v, k, true
	}
	v, k, _, ok := bl.lowerWant(b.Value, want)
	return v, k, ok
}

func isContinueNode(n ast.Node) bool {
	_, ok := n.(*ast.Continue)
	return ok
}

// irLiteralCaseKind is a `case` scrutinee whose only patterns are literals
// the VM tests with rt.Equal beside the Int, String and Bool ones: Float and
// Decimal, whose equality is EqFloat's and EqDecimal's.
func irLiteralCaseKind(k kind, bl *irScalarBuilder) bool {
	return (k == kindFloat || isDecimalKind(k))
}

// lambdaResultWant records, for the length of one lowering, the result type
// the checker solved for a lambda bound at (line, col). A lambda whose body
// ends in an `if` or `case` takes it as the arms' destination. The answer is
// the undo.
func (g *gen) lambdaResultWant(lam *ast.Lambda, line, col int) func() {
	if g.fa == nil {
		return func() {}
	}
	sym := g.fa.Definitions[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		return func() {}
	}
	ty := sym.Type
	for {
		tv, isVar := ty.(*analysis.TypeVar)
		if !isVar || tv.Resolved == nil {
			break
		}
		ty = tv.Resolved
	}
	ft, isFunc := ty.(*analysis.FuncType)
	if !isFunc || ft.Return == nil || irUnsolvedType(ft.Return) {
		return func() {}
	}
	k := g.project(ft.Return)
	if !irCallableValueKind(k) {
		return func() {}
	}
	prevFor, prev := g.lambdaWantFor, g.lambdaWant
	g.lambdaWantFor, g.lambdaWant = lam, k
	return func() { g.lambdaWantFor, g.lambdaWant = prevFor, prev }
}

// identRead reads the value t names, at the type its binding holds.
func (bl *irScalarBuilder) identRead(t *ast.Ident, line, col int) (ir.Temp, kind, bool, bool) {
	if held, isBound := bl.bound[t.Name]; isBound {
		// A NAME THIS BODY BOUND. The value is already in the Bind's
		// destination temporary, so the read is that temporary rather
		// than a fresh `ir.Ref`. Mobile, which is the answer
		// `ir.Ref.Stable()` gives for RefLocal.
		return held, bl.boundK[t.Name], true, true
	}
	if inst, isNested := bl.genericNestedRef(t); isNested {
		// A generic nested fn named as a value: the instance the checker
		// instantiated the reference at.
		if inst == nil {
			return ir.NoTemp, kindInvalid, false, false
		}
		return bl.lower(inst)
	}
	if bl.defaultScope {
		if v, k, mobile, ok := bl.onceValue(t); ok {
			return v, k, mobile, true
		}
		return bl.funcRef(t)
	}
	if v, k, mobile, ok := bl.onceValue(t); ok {
		return v, k, mobile, true
	}
	if bl.parent != nil {
		return bl.capture(t)
	}
	l, bound := bl.g.lookup(t.Name)
	if !bound {
		return bl.funcRef(t)
	}
	if !irCallableValueKind(l.k) && l.k != kindUnit && !(bl.boot && irContextKind(l.k)) {
		// Bound values need a retained representation, including callable
		// signatures over the retained value domain.
		return ir.NoTemp, kindInvalid, false, false
	}
	r := ir.NewRefLocal(bl.g.irPos(line, col), bl.f.NewTemp(), bl.sh.localSym(t.Name))
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: l.k})
	// `ir.Ref.Stable()` is the mobility answer and for RefLocal it is
	// true: a local read forces nothing and two reads agree. It is taken
	// from the node rather than asserted.
	return r.Dst(), l.k, r.Stable(), true
}
