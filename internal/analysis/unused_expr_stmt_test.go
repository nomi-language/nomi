package analysis_test

import (
	"strings"
	"testing"
)

func TestUnusedExprStmt_NonFinalValueIsRejected(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn hello() -> String
  4
  "hello"
end
`)
	expectDiagnosticContaining(t, errs, "non-final expression has type Int")
}

func TestUnusedExprStmt_ExplicitDiscardIsAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn hello() -> String
  _ = 4
  "hello"
end
`)
	expectNoUnusedBindings(t, errs)
	for _, e := range errs {
		if strings.Contains(e.Message, "non-final expression has type Int") {
			t.Fatalf("unexpected ignored-expression diagnostic: %s", e)
		}
	}
}

func TestUnusedExprStmt_UnitStatementIsAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn side() -> Unit
  Unit
end

fn hello() -> String
  side()
  "hello"
end
`)
	for _, e := range errs {
		if strings.Contains(e.Message, "non-final expression has type Unit") {
			t.Fatalf("unexpected ignored-expression diagnostic: %s", e)
		}
	}
}

func TestUnusedExprStmt_NonUnitCallIsRejected(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn compute() -> Int
  42
end

fn hello() -> String
  compute()
  "hello"
end
`)
	expectDiagnosticContaining(t, errs, "non-final expression has type Int")
}

func TestUnusedExprStmt_IOPrintIsAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
import std::io

fn hello() -> String
  io.print("side effect")
  "hello"
end
`)
	for _, e := range errs {
		if strings.Contains(e.Message, "non-final expression has type Unit") {
			t.Fatalf("unexpected ignored-expression diagnostic for io.print: %s", e)
		}
	}
}

func TestUnusedExprStmt_DebugInspectRequiresDiscard(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
import std::debug: Debug

fn hello() -> String
  Debug.inspect("value")
  "hello"
end
`)
	expectDiagnosticContaining(t, errs, "non-final expression has type String")
}

func TestUnusedExprStmt_DbgStatementIsAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn hello() -> String
  dbg 4
  "hello"
end
`)
	for _, e := range errs {
		if strings.Contains(e.Message, "non-final expression has type Int") {
			t.Fatalf("unexpected ignored-expression diagnostic for dbg: %s", e)
		}
	}
}

func TestUnusedExprStmt_ValueCarryingAssertRefuteStatementsAreAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn ok_value() -> Result<Int, String>
  Ok(42)
end

fn err_value() -> Result<Int, String>
  Err("nope")
end

fn hello() -> Result<String, AssertionFailure>
  assert ok_value()
  refute err_value()
  Ok("hello")
end
`)
	for _, e := range errs {
		if strings.Contains(e.Message, "non-final expression has type Int") ||
			strings.Contains(e.Message, "non-final expression has type String") {
			t.Fatalf("unexpected ignored-expression diagnostic for assert/refute: %s", e)
		}
	}
}

func TestUnusedExprStmt_PipeDbgStatementIsAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn hello() -> String
  [1, 2, 3]
  |> dbg

  "hello"
end
`)
	for _, e := range errs {
		if strings.Contains(e.Message, "non-final expression has type List<Int>") {
			t.Fatalf("unexpected ignored-expression diagnostic for pipe dbg: %s", e)
		}
	}
}

func TestUnusedExprStmt_ReturnStatementIsAccepted(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn hello(flag: Bool) -> String
  if flag then
    return "early"
  end

  "hello"
end
`)
	for _, e := range errs {
		if strings.Contains(e.Message, "non-final expression has type Infallible") {
			t.Fatalf("unexpected ignored-expression diagnostic for return: %s", e)
		}
	}
}
