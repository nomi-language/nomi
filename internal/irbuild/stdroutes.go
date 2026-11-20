package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// std's own functions are keyed by their identity, the (module, owner,
// name) triple of analysis.StdFunc that a call's record carries. A
// program's functions are keyed by their declaration node. The std bundle
// keeps no declaration for a `host fn`, so the triple, which the bundle
// builds from names it holds, is the key every std lookup takes.

// stdShortModule is the name the std lowering keys a module by
// (`fs/host`), from the module key a StdFunc carries (`std/fs/host`).
func stdShortModule(module string) string {
	return strings.TrimPrefix(module, "std/")
}

// stdHostKey is the host key of a std function, the form a refusal and a
// host crossing name it by (`fs/host.run_on`).
func stdHostKey(id analysis.StdFunc) string {
	if id.Owner == "" {
		return stdShortModule(id.Module) + "." + id.Name
	}
	return stdShortModule(id.Module) + "." + id.Owner + "." + id.Name
}

// stdFuncByID is the std function id names among those the std index
// files under its owner and name (stdlibIndex.byType, the overload set
// of every declaration of that spelling), told apart by identity: its
// impl's interface and type arguments, which id carries. Nil for the zero
// id and for one the index holds no function of.
func (g *gen) stdFuncByID(id analysis.StdFunc) *stdFunc {
	if g.std == nil || id.IsZero() || id.Owner == "" {
		return nil
	}
	var found *stdFunc
	for _, f := range g.std.byType[id.Owner+"."+id.Name] {
		if isNilNode(f.node) || g.stdFuncIn(id, f.node) != id {
			continue
		}
		if found != nil && found != f {
			return nil
		}
		found = f
	}
	return found
}

// stdInstFuncOf is the std function id names that a program instantiates
// for itself (stdModuleInstFunc), or nil.
func (s *stdInstances) stdInstFuncOf(id analysis.StdFunc) *stdFunc {
	if s == nil || id.IsZero() {
		return nil
	}
	return stdModuleInstFunc(s.std.views[stdShortModule(id.Module)], id.Owner, id.Name)
}

// stdTemplateOf is the generic std template id names, or nil.
func (s *stdInstances) stdTemplateOf(id analysis.StdFunc) *stdFunc {
	if s == nil || id.IsZero() {
		return nil
	}
	return stdModuleGenericTemplate(s.std.views[stdShortModule(id.Module)], id.Owner, id.Name)
}

// The std interfaces whose functions the builder calls in code it writes
// itself (an anonymous struct's or tuple's hash, ToJson, Debug, and an
// assertion subject's Assertable), or whose impls it looks up for an
// operation the language defines over them (`==`, `<`, a range, a `for`
// over a value, a conversion, a typed literal). Each lookup reads the
// impls filed under stdIfaceImplKey of one of these.
var (
	stdHashable       = analysis.StdType{Module: "std/hashable", Name: "Hashable"}
	stdToJson         = analysis.StdType{Module: "std/json", Name: "ToJson"}
	stdFromJson       = analysis.StdType{Module: "std/json", Name: "FromJson"}
	stdDebug          = analysis.StdType{Module: "std/debug", Name: "Debug"}
	stdAssertable     = analysis.StdType{Module: "std/assertions", Name: "Assertable"}
	stdDisplay        = analysis.StdType{Module: "std/display", Name: "Display"}
	stdComparable     = analysis.StdType{Module: "std/comparable", Name: "Comparable"}
	stdEquatable      = analysis.StdType{Module: "std/equatable", Name: "Equatable"}
	stdIter           = analysis.StdType{Module: "std/iter", Name: "Iter"}
	stdDiscrete       = analysis.StdType{Module: "std/discrete", Name: "Discrete"}
	stdSteppable      = analysis.StdType{Module: "std/steppable", Name: "Steppable"}
	stdFrom           = analysis.StdType{Module: "std/convert", Name: "From"}
	stdCheckedLiteral = analysis.StdType{Module: "std/literals", Name: "CheckedLiteral"}
	stdParse          = analysis.StdType{Module: "std/parse", Name: "Parse"}
	stdAdd            = analysis.StdType{Module: "std/add", Name: "Add"}
	stdMultiply       = analysis.StdType{Module: "std/multiply", Name: "Multiply"}
)

// stdIfaceCallRecord is the record of a call the builder writes of iface's
// function method, passing its args arguments in order, the receiver a
// value of the checker type recv: what the checker records for
// `Iface.method(x)` written with x of that type
// (analysis.FileAnalysis.InterfaceCallee), the impl block it reaches among
// it. recv is nil where the builder holds the value by its kind alone (a
// box's thunk, a main's failure); the record then names the interface's
// function and no type. Either way the record names the std interface, so
// the call is planned from it and never from what this file binds its
// name to (recordedStdIface).
func (g *gen) stdIfaceCallRecord(iface analysis.StdType, method string, args int, recv analysis.Type) *analysis.ResolvedCall {
	rc := analysis.PositionalCall(args, args)
	rc.Callee = g.instanceFA().InterfaceCallee(iface, method, recv)
	if rc.Callee.Kind == analysis.CalleeUnknown {
		// A program that does not reach iface's module.
		rc.Callee = analysis.Callee{
			Kind:      analysis.CalleeInterface,
			Qualifier: analysis.Qualifier{Kind: analysis.QualInterface, Std: iface},
			Method:    method,
			For:       recv,
		}
	}
	return rc
}

// testingCheck is std/testing's `check`, a keyword form the builder lowers
// itself (testingCheck).
var testingCheck = analysis.StdFunc{Module: "std/testing", Name: "check"}

// stdRoute is a route of the builder's own for a std function, instead of
// a call of its body.
type stdRoute uint8

const (
	// routeRunCaptured runs a function with the console captured
	// (runCaptured); routeRunReplayed also replays recorded input.
	routeRunCaptured stdRoute = iota + 1
	routeRunReplayed
	// routeRunOnFiles runs a function over a host's in-memory files or
	// processes (runOnFiles).
	routeRunOnFiles
	// routeRequestOn, routeAnswerOn and routeRecordedOn drive std/http's
	// host doubles (ircapture.go).
	routeRequestOn
	routeAnswerOn
	routeRecordedOn

	// The routes below are keyed by what a call is qualified with
	// (stdQualRoutes), and typeQualCall lowers each.

	// routeIterSeeded is a seeded `Iter.reduce`, `scan` or `map_reduce`
	// (iterReduceCall); routeIterLoop is `Iter.loop` (iterLoop).
	routeIterSeeded
	routeIterLoop
	// routeStructUpdate is `Struct.update` (structUpdateCall).
	routeStructUpdate
	// routeContextValue is `Context.with_value` and `Context.value`
	// (contextValueCall).
	routeContextValue
	// routeContainerEqual is a container's or prelude enum's `equal`
	// (containerEqualCall); routeContainerCompare a List's or Vector's
	// `compare` (containerCompareAny).
	routeContainerEqual
	routeContainerCompare
	// routeHash is `Hashable.hash`: a tuple's (tupleHashCall), an
	// interface value's (existentialHashCall), or that of a type that
	// meets Hashable by its equality (structuralHashCall).
	routeHash
	// routeToJson is `ToJson.to_json` and `to_json_field` over a record
	// (recordToJsonCall).
	routeToJson
	// routeCompare is `Comparable.compare` over tuples (tupleCompareCall).
	routeCompare
	// routeEquatableEqual is `Equatable.equal`: a container's in a std
	// body (containerEqualCall), and over interface values, tuples,
	// records and types equal by `==` (structuralEqualCall).
	routeEquatableEqual
	// routePreludePredicate is a prelude enum's variant test
	// (preludePredicates, preludePredicateCall).
	routePreludePredicate
	// routeScalarText is a scalar's `to_string` (scalarTextCall), and
	// routeDisplay `Display.to_string`, rendered by the operand's kind.
	routeScalarText
	routeDisplay
	// routeEachWhile is `Bytes.each_while` and `String.each_while`, and
	// routeStringJoin `String.join`: Iter operations over the value's
	// view (iterCall).
	routeEachWhile
	routeStringJoin
	// routeMapErr is `Result.map_err`, routeToResult `Maybe.to_result`,
	// and routeWithDefault `Maybe.with_default` and
	// `Result.with_default`.
	routeMapErr
	routeToResult
	routeWithDefault

	// The routes below are owner-wide: every function of the std type
	// that has no exact route of its own. routeIter is an Iter operation
	// (iterCall), and routeRange a Range one (rangeCall). routeMap,
	// routeList, routeVector and routeSet are a container's function: its
	// intrinsic (mapCallPlan, listCallPlan, vectorCallPlan, setCallPlan),
	// else std's body instantiated at the call's types.
	routeIter
	routeRange
	routeMap
	routeList
	routeVector
	routeSet
)

// stdRoutes are the std functions the builder lowers by its own route,
// by identity. A call reaches an entry only through the identity its
// record carries, so no entry tests how the call spelled its callee.
var stdRoutes = map[analysis.StdFunc]stdRoute{
	{Module: "std/io", Name: "run_captured"}:       routeRunCaptured,
	{Module: "std/io", Name: "run_replayed"}:       routeRunReplayed,
	{Module: "std/fs/host", Name: "run_on"}:        routeRunOnFiles,
	{Module: "std/process/host", Name: "run_on"}:   routeRunOnFiles,
	{Module: "std/http/host", Name: "run_on"}:      routeRunOnFiles,
	{Module: "std/http/host", Name: "request_on"}:  routeRequestOn,
	{Module: "std/http/host", Name: "answer_on"}:   routeAnswerOn,
	{Module: "std/http/host", Name: "recorded_on"}: routeRecordedOn,
}

// stdQualKey names a std function by what a call of it is qualified
// with: the std type or interface (analysis.Callee.RouteOwner) and the
// method. A key with no method names every function of the owner (an
// owner-wide route). It is a different key from the function the call
// reaches (analysis.Callee.Std, stdRoutes' key): `Debug.inspect(s)` is qualified
// with Debug and reaches String's impl.
type stdQualKey struct {
	Owner  analysis.StdType
	Method string
}

// The std types and interfaces stdQualRoutes names.
var (
	stdIterType       = analysis.StdType{Module: "std/iter", Name: "Iter"}
	stdStructType     = analysis.StdType{Module: "std/structs", Name: "Struct"}
	stdContextType    = analysis.StdType{Module: "std/context", Name: "Context"}
	stdComparableType = analysis.StdType{Module: "std/comparable", Name: "Comparable"}
	stdEquatableType  = analysis.StdType{Module: "std/equatable", Name: "Equatable"}
	stdDisplayType    = analysis.StdType{Module: "std/display", Name: "Display"}
	stdIntType        = analysis.StdType{Module: "std/int", Name: "Int"}
	stdFloatType      = analysis.StdType{Module: "std/float", Name: "Float"}
	stdBoolType       = analysis.StdType{Module: "std/bool", Name: "Bool"}
	stdStringType     = analysis.StdType{Module: "std/strings", Name: "String"}
	stdBytesType      = analysis.StdType{Module: "std/bytes", Name: "Bytes"}
	stdMaybeType      = analysis.StdType{Module: "std/maybe", Name: "Maybe"}
	stdResultType     = analysis.StdType{Module: "std/results", Name: "Result"}
	stdListType       = analysis.StdType{Module: "std/lists", Name: "List"}
	stdVectorType     = analysis.StdType{Module: "std/vectors", Name: "Vector"}
	stdMapType        = analysis.StdType{Module: "std/maps", Name: "Map"}
	stdRangeType      = analysis.StdType{Module: "std/ranges", Name: "Range"}
	stdSetType        = analysis.StdType{Module: "std/sets", Name: "Set"}
)

// stdQualRoutes are the std functions typeQualCall lowers by a route of
// its own, keyed by what the call is qualified with (qualRoute): an
// exact key for one function, else the owner's key with no method. A
// program's type or interface of the same name has no std identity, so it
// never reaches an entry.
var stdQualRoutes = stdQualRouteTable()

func stdQualRouteTable() map[stdQualKey]stdRoute {
	routes := map[stdQualKey]stdRoute{
		{stdIterType, "loop"}:           routeIterLoop,
		{stdStructType, "update"}:       routeStructUpdate,
		{stdContextType, "with_value"}:  routeContextValue,
		{stdContextType, "value"}:       routeContextValue,
		{stdListType, "equal"}:          routeContainerEqual,
		{stdMaybeType, "equal"}:         routeContainerEqual,
		{stdResultType, "equal"}:        routeContainerEqual,
		{stdVectorType, "equal"}:        routeContainerEqual,
		{stdMapType, "equal"}:           routeContainerEqual,
		{stdListType, "compare"}:        routeContainerCompare,
		{stdVectorType, "compare"}:      routeContainerCompare,
		{stdHashable, "hash"}:           routeHash,
		{stdToJson, "to_json"}:          routeToJson,
		{stdToJson, "to_json_field"}:    routeToJson,
		{stdComparableType, "compare"}:  routeCompare,
		{stdEquatableType, "equal"}:     routeEquatableEqual,
		{stdIntType, "to_string"}:       routeScalarText,
		{stdFloatType, "to_string"}:     routeScalarText,
		{stdBoolType, "to_string"}:      routeScalarText,
		{stdStringType, "to_string"}:    routeScalarText,
		{stdDisplayType, "to_string"}:   routeDisplay,
		{stdBytesType, "each_while"}:    routeEachWhile,
		{stdStringType, "each_while"}:   routeEachWhile,
		{stdStringType, "join"}:         routeStringJoin,
		{stdResultType, "map_err"}:      routeMapErr,
		{stdMaybeType, "to_result"}:     routeToResult,
		{stdMaybeType, "with_default"}:  routeWithDefault,
		{stdResultType, "with_default"}: routeWithDefault,
		{stdIterType, ""}:               routeIter,
		{stdRangeType, ""}:              routeRange,
		{stdMapType, ""}:                routeMap,
		{stdListType, ""}:               routeList,
		{stdVectorType, ""}:             routeVector,
		{stdSetType, ""}:                routeSet,
	}
	for _, m := range irSeededIterMethods {
		routes[stdQualKey{stdIterType, m}] = routeIterSeeded
	}
	for key := range preludePredicates {
		routes[key] = routePreludePredicate
	}
	return routes
}

// qualRoute is the route typeQualCall lowers t, `ti.method(...)`, by: the
// entry of stdQualRoutes for what the call's record says it is qualified
// with and the method, else the owner-wide entry, or 0. The key answered
// names the method either way.
//
// A copy the builder respelled with a concrete owner (stdKindQualCall's
// `Int.to_string` for `T.to_string`) carries a record qualified with that
// owner (respelledCallOf). Code the checker never checked
// (markUnchecked) has no record, and is reached by the owner the builder
// wrote, resolved in this file's scope.
func (bl *irScalarBuilder) qualRoute(t *ast.Call, ti *ast.TypeIdent, method string) (stdRoute, stdQualKey) {
	g := bl.g
	owner := g.routeCallee(t).RouteOwner()
	if g.isUnchecked(t) {
		if _, local := g.blockLocalNamed(ti.Name); local {
			return 0, stdQualKey{}
		}
		fa := g.srcFA()
		owner = fa.QualifierOfSymbol(resolvedTypeSymbol(fa, ti.Name))
	}
	if owner.Std.IsZero() {
		return 0, stdQualKey{}
	}
	key := stdQualKey{owner.Std, method}
	if route, ok := stdQualRoutes[key]; ok {
		return route, key
	}
	return stdQualRoutes[stdQualKey{Owner: owner.Std}], key
}

// kindQualifier is the qualifier of the std type k is an instance of, as
// the checker names it (analysis.FileAnalysis.StdTypeQualifier): std's
// Map for a `Map<String, Int>`, Int for an Int, std/calendar's Date for a
// Date. A call the builder respells with k's owner is qualified with it
// (respelledCallOf). The zero Qualifier for a kind whose type no std
// module declares: a program's type, a function, a tuple.
func (g *gen) kindQualifier(k kind) analysis.Qualifier {
	fa := g.srcFA()
	if fa == nil {
		return analysis.Qualifier{}
	}
	module, name := "", irContainerBaseName(k)
	switch {
	case k == kindInt, k == kindFloat, k == kindString, k == kindBool:
		name = k.nomi()
	case name != "":
	case k.tag == tagNamed && k.def != nil:
		if origin, nomi, ok := stdSpecOf(k.def); ok {
			module, name = origin, nomi
			break
		}
		if k.def.decl == nil {
			return analysis.Qualifier{}
		}
		module = k.def.foreign
		if dn, ok := g.declNameOf(k.def.decl); ok {
			module = dn.module
		}
		if module == "" {
			module = fa.Origin
		}
		if !analysis.IsStdlibKey(module) {
			return analysis.Qualifier{}
		}
		name = k.def.nomi
	}
	if name == "" {
		return analysis.Qualifier{}
	}
	return fa.StdTypeQualifier(module, name)
}

// stdRouteCall lowers t, a call of id, by id's route. handled is false
// for a function with none.
func (bl *irScalarBuilder) stdRouteCall(t *ast.Call, id analysis.StdFunc) (ir.Temp, kind, bool, bool, bool) {
	key := stdHostKey(id)
	switch stdRoutes[id] {
	case routeRunCaptured:
		v, k, mobile, ok := bl.runCaptured(t, key, kindString, kindString, kindString)
		return v, k, mobile, ok, true
	case routeRunReplayed:
		v, k, mobile, ok := bl.runCaptured(t, key, kindString, kindString, kindString, kindInt)
		return v, k, mobile, ok, true
	case routeRunOnFiles:
		v, k, mobile, ok := bl.runOnFiles(t, key)
		return v, k, mobile, ok, true
	case routeRequestOn:
		v, k, mobile, ok := bl.requestOn(t, key)
		return v, k, mobile, ok, true
	case routeAnswerOn:
		v, k, mobile, ok := bl.answerOn(t, key)
		return v, k, mobile, ok, true
	case routeRecordedOn:
		v, k, mobile, ok := bl.recordedOn(t, key)
		return v, k, mobile, ok, true
	}
	return ir.NoTemp, kindInvalid, false, false, false
}
