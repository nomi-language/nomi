package analysis

import (
	"strings"
	"testing"
)

func TestRedeclaration_FileScope_DuplicateOnce_Rejected(t *testing.T) {
	_, errs := checkSource(`
once x = 1
once x = 2

fn main() {}
`)
	expectError(t, errs, "'x' is already defined in this scope")
}

func TestRedeclaration_FileScope_DuplicateFn_Rejected(t *testing.T) {
	_, errs := checkSource(`
fn helper(): Int {
  1
}

fn helper(): Int {
  2
}

fn main() {}
`)
	expectError(t, errs, "'helper' is already defined in this scope")
}

func TestRedeclaration_FileScope_DuplicateStruct_Rejected(t *testing.T) {
	_, errs := checkSource(`
struct User {
  name: String
}

struct User {
  email: String
}

fn main() {}
`)
	expectError(t, errs, "'User' is already defined in this scope")
}

func TestOnce_NestedInFunctionBody_Rejected(t *testing.T) {
	_, errs := checkSource(`
fn main() {
  once x = 1
  x
}
`)
	expectError(t, errs, "once")
}

func TestShadowing_SameScopeBinding_Allowed(t *testing.T) {
	_, errs := checkSource(`
fn main(): Int {
  x = 1
  x = 2
  x
}
`)
	expectNoErrors(t, errs)
}

func TestShadowing_Parameter_Allowed(t *testing.T) {
	_, errs := checkSource(`
fn double(x: Int): Int {
  x = x + x
  x
}
`)
	expectNoErrors(t, errs)
}

func TestShadowing_TypeChanging_Allowed(t *testing.T) {
	_, errs := checkSource(`
fn need_string(s: String): String {
  s
}

fn use_it(): String {
  x = 5
  x = "hi"
  need_string(x)
}
`)
	expectNoErrors(t, errs)
}

func TestShadowing_ModuleOnce_Allowed(t *testing.T) {
	_, errs := checkSource(`
once x = 1

fn main(): Int {
  x = 2
  x
}
`)
	expectNoErrors(t, errs)
}

func TestShadowing_LambdaParameter_Allowed(t *testing.T) {
	_, errs := checkSource(`
fn apply(): Int {
  f = |x: Int| {
    x = x + 1
    x
  }
  f(5)
}
`)
	expectNoErrors(t, errs)
}

func TestShadowing_NestedFunctionBinding_Allowed(t *testing.T) {
	_, errs := checkSource(`
fn main(): Int {
  x = 1

  fn helper(): Int {
    x = 2
    x
  }

  helper()
}
`)
	expectNoErrors(t, errs)
}

func TestShadowing_DoesNotHideRealTypeErrors(t *testing.T) {
	_, errs := checkSource(`
fn need_string(s: String): String {
  s
}

fn use_it(): String {
  x = 5
  need_string(x)
}
`)
	if !hasErrorContaining(errs, "expected String", "got Int") {
		t.Fatalf("expected String/Int type mismatch, got %v", errs)
	}
}

func hasErrorContaining(errs []TypeError, needles ...string) bool {
	for _, err := range errs {
		found := true
		for _, needle := range needles {
			if !strings.Contains(err.Message, needle) {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}
