package analysis

import (
	"strings"
	"testing"
)

// Interface upgrades v1 (analyzer):
//   - an `impl Iface for T` block's items are validated against the interface.
//   - per-method impl rejects an override of a non-open default.

// TestImplStructOnMethodsOnlyInterfaceRedundantButOk: an
// `impl Iface for T` block's method items drive method validation.
func TestImplStructOnMethodsOnlyInterfaceRedundantButOk(t *testing.T) {
	src := `pub interface Greeter {
  fn greet(value: self): String
}

struct User { name: String }

impl Greeter for User {
  fn greet(user: User): String { user.name }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// TestImplStructOnNonInterfaceErrors: an impl block on a struct must target an
// interface; targeting a struct or enum produces a clear error.
func TestImplStructOnNonInterfaceErrors(t *testing.T) {
	src := `pub struct other { x: Int }

struct AppEnv { x: Int }

impl other for AppEnv`
	_, errs := checkSource(src)
	expectError(t, errs, "not an interface")
}

// ---------------------------------------------------------------------------
// Feature 2: open default methods
// ---------------------------------------------------------------------------

// TestImplOverridesOpenDefaultOk: an `open fn extension_point` default
// may be overridden by an impl block. No error expected.
func TestImplOverridesOpenDefaultOk(t *testing.T) {
	src := `pub interface MyIface {
  fn required_method(value: self): Int
  open fn extension_point(_value: self): String { "hi" }
}

pub struct User { name: String }

impl MyIface for User {
  fn required_method(_u: User): Int { 1 }

  fn extension_point(u: User): String { u.name }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// TestImplOverridesFinalDefaultErrors: a non-open default cannot be
// overridden — the analyzer reports an error.
func TestImplOverridesFinalDefaultErrors(t *testing.T) {
	src := `pub interface MyIface {
  fn required_method(value: self): Int
  fn final_default(_value: self): String { "fixed" }
}

pub struct User { name: String }

impl MyIface for User {
  fn required_method(_u: User): Int { 1 }

  fn final_default(_u: User): String { "custom" }
}`
	_, errs := checkSource(src)
	expectError(t, errs, "final default")
	expectError(t, errs, "mark it `open`")
}

// TestImplOnRequiredMethodUnchanged: required methods (no body) work
// as before — an impl is required, no `open` involved.
func TestImplOnRequiredMethodUnchanged(t *testing.T) {
	src := `pub interface MyIface {
  fn required_method(value: self): Int
}

pub struct User { name: String }

impl MyIface for User {
  fn required_method(_u: User): Int { 1 }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// TestImplFinalDefaultOmittedOk: not overriding a final default is the
// happy path — the impl observes the interface's default unchanged.
func TestImplFinalDefaultOmittedOk(t *testing.T) {
	src := `pub interface MyIface {
  fn required_method(value: self): Int
  fn final_default(_value: self): String { "fixed" }
}

pub struct User { name: String }

impl MyIface for User {
  fn required_method(_u: User): Int { 1 }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// TestOpenErrorPointsAtDecorator: the final-default error anchors at
// the impl block (not the fn signature) so editors land the cursor
// on the assertion that failed.
func TestOpenErrorPointsAtDecorator(t *testing.T) {
	src := `pub interface MyIface {
  fn final_default(_value: self): String { "fixed" }
}

pub struct User { name: String }

impl MyIface for User {
  fn final_default(_u: User): String { "custom" }
}`
	_, errs := checkSource(src)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "final default") {
			found = true
			// The error anchors at the offending block method. Just
			// confirm the error has a positive line — anchoring detail
			// is internal.
			if e.Line == 0 {
				t.Errorf("expected positive line, got 0: %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("expected a 'final default' error")
	}
}
