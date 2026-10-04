package irbuild

// The body census reports, per function body the builder is offered, the
// body's position class and whether an IR build was attempted for it.
// Retention and read-back are `irFuncObserved`'s numbers; this hook counts the
// bodies offered, including the ones the builder declines.
//
// It is a hook rather than a counter field for `irFuncObserved`'s reason: a
// caller that wants it runs whole lowerings through `Generate`, which hands
// back no gen to read a field off. It costs one nil check per lowered body.

// irBodyClass is one function-BODY POSITION the builder lowers.
//
// The classes are enumerated from the builder's call sites rather than from
// the AST: the population is the set of places the builder lowers a body.
type irBodyClass uint8

const (
	// irBodyModuleFn is `funcDecl`'s body: a module-scope `fn` of a user
	// program. `irScalarLower`'s first caller.
	irBodyModuleFn irBodyClass = iota
	// irBodyNestedFn is an `fn` declared inside another body. The builder
	// declines the enclosing body.
	irBodyNestedFn
	// irBodyImplFn is `implFunc`'s body: an inherent or interface `impl`
	// member, including a synthesized derive.
	irBodyImplFn
	// irBodyLambda is `lambda`'s body.
	irBodyLambda
	// irBodyTestBody is `testCase`'s body; see irtestbody.go.
	irBodyTestBody
	// irBodyConcurrent is `concurrentBlock`'s body.
	irBodyConcurrent
	// irBodyStdFn is `emitStdFunc`'s body: a Nomi-bodied `std/`
	// declaration. `irScalarLower`'s second caller.
	irBodyStdFn
)

// irBodyClassName is the class's reported name, so a probe and a findings
// table agree on the spelling.
func (c irBodyClass) String() string {
	switch c {
	case irBodyModuleFn:
		return "module fn"
	case irBodyNestedFn:
		return "nested fn"
	case irBodyImplFn:
		return "impl fn"
	case irBodyLambda:
		return "lambda"
	case irBodyTestBody:
		return "test body"
	case irBodyConcurrent:
		return "concurrent block"
	case irBodyStdFn:
		return "std fn"
	}
	return "unknown body class"
}

// irBodyObserved is a test-only hook, nil in production, called once per
// function body the builder lowers: with the body's position class and whether
// an IR build was ATTEMPTED for it.
//
// `attempted` is a property of the call site and not of the outcome: a body
// the builder declines was still attempted.
var irBodyObserved func(class irBodyClass, attempted bool)

// irBodyObserve reports one lowered body to the census.
func (g *gen) irBodyObserve(class irBodyClass, attempted bool) {
	if irBodyObserved == nil {
		return
	}
	irBodyObserved(class, attempted)
}
