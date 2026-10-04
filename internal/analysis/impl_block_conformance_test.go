package analysis

import (
	"testing"
)

// Block-form impls validate each method's signature against the
// interface — name / param-count / param-type / param-name / return
// checks (validateImplBlockMethodSignatures).
// Applies to `fn` and `host fn` items alike. (hasErrorContaining lives in
// shadowing_test.go.)

// A block `fn` whose return type disagrees with the interface is rejected.
func TestImplBlock_FnReturnTypeMismatch_Rejected(t *testing.T) {
	src := `pub interface Speech {
    fn speak(value: self): String
}

pub struct Dog { name: String }

impl Speech for Dog {
    fn speak(_d: Dog): Int { 0 }
}`
	_, errs := checkSource(src)
	if !hasErrorContaining(errs, "speak") {
		t.Fatalf("expected a return-type conformance error for speak, got: %v", errs)
	}
}

// A block `fn` whose parameter type disagrees with the interface is rejected.
func TestImplBlock_FnParamTypeMismatch_Rejected(t *testing.T) {
	src := `pub interface Greeter {
    fn greet(value: self, name: String): String
}

pub struct Bot { id: Int }

impl Greeter for Bot {
    fn greet(_b: Bot, _name: Int): String { "hi" }
}`
	_, errs := checkSource(src)
	if !hasErrorContaining(errs, "greet") {
		t.Fatalf("expected a param-type conformance error for greet, got: %v", errs)
	}
}

// A block `host fn` whose return type disagrees with the interface is
// rejected — same as the fn case, since the extern IS the method.
func TestImplBlock_ExternReturnTypeMismatch_Rejected(t *testing.T) {
	src := `pub interface Speech {
    fn speak(value: self): String
}

pub struct Dog { name: String }

impl Speech for Dog {
    host fn speak(d: Dog): Int
}`
	_, errs := checkSource(src)
	if !hasErrorContaining(errs, "speak") {
		t.Fatalf("expected a return-type conformance error for extern speak, got: %v", errs)
	}
}

// A block impl that overrides a final (non-`open`) interface default is
// rejected. Without this, a block could silently break the interface's
// default contract.
func TestImplBlock_OverridesFinalDefault_Rejected(t *testing.T) {
	src := `pub interface Greeter {
    fn greet(_value: self): String { "hi" }
}

pub struct Bot { id: Int }

impl Greeter for Bot {
    fn greet(_b: Bot): String { "yo" }
}`
	_, errs := checkSource(src)
	if !hasErrorContaining(errs, "greet") {
		t.Fatalf("expected a final-default override error for greet, got: %v", errs)
	}
}

// Overriding an `open` interface default IS allowed — no error.
func TestImplBlock_OverridesOpenDefault_Allowed(t *testing.T) {
	src := `pub interface Greeter {
    open fn greet(_value: self): String { "hi" }
}

pub struct Bot { id: Int }

impl Greeter for Bot {
    fn greet(_b: Bot): String { "yo" }
}`
	_, errs := checkSource(src)
	if hasErrorContaining(errs, "greet") {
		t.Fatalf("overriding an open default should be allowed, got: %v", errs)
	}
}

// A block impl whose non-self parameter name disagrees with the interface
// declaration is rejected (Swift-style names). The
// self-position parameter name stays free.
func TestImplBlock_ParamNameMismatch_Rejected(t *testing.T) {
	src := `pub interface Adder {
    fn add(value: self, amount: Int): Int
}

pub struct counter { n: Int }

impl Adder for counter {
    fn add(c: counter, _delta: Int): Int { c.n }
}`
	_, errs := checkSource(src)
	if !hasErrorContaining(errs, "delta") {
		t.Fatalf("expected a param-name conformance error for 'delta' vs 'amount', got: %v", errs)
	}
}

// The self/receiver parameter name is free — using a different name than the
// interface's `value` is allowed.
func TestImplBlock_SelfParamNameFree_Allowed(t *testing.T) {
	src := `pub interface Adder {
    fn add(value: self, amount: Int): Int
}

pub struct counter { n: Int }

impl Adder for counter {
    fn add(counter: counter, amount: Int): Int { _ = amount; counter.n }
}`
	_, errs := checkSource(src)
	if hasErrorContaining(errs, "parameter name") {
		t.Fatalf("self-position param name should be free, got: %v", errs)
	}
}

func TestImplBlock_DestructuredParamNameFree_Allowed(t *testing.T) {
	src := `pub interface Adder {
    fn add(value: self, rhs: Step): counter
}

pub type Step Int
pub type counter Int

impl Adder for counter {
    fn add(lhs: counter, Step(n)): counter { lhs }
}`
	_, errs := checkSource(src)
	if hasErrorContaining(errs, "parameter name") {
		t.Fatalf("destructured parameter should not be name-checked, got: %v", errs)
	}
}

// A correct block impl (fn + extern) produces no conformance error.
func TestImplBlock_CorrectSignatures_NoError(t *testing.T) {
	src := `pub interface Speech {
    fn speak(value: self): String
}

pub struct Dog { name: String }
pub struct Cat { name: String }

impl Speech for Dog {
    fn speak(d: Dog): String { d.name }
}

impl Speech for Cat {
    host fn speak(c: Cat): String
}`
	_, errs := checkSource(src)
	if hasErrorContaining(errs, "speak") {
		t.Fatalf("correct impls should produce no conformance error, got: %v", errs)
	}
}
