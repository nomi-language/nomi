package irbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// Context.with_value / Context.value over `Type<T>` witnesses, markers'
// equality, and `if`/`case` assertion subjects, run on the VM against the
// golden record of `nomi test`, failing reports included.

const irContextValueSource = `type UserId Int

type RequestId Int

type Flag

struct Tag {
  name: String
}

fn user_of(c: Context): Maybe<UserId> {
  Context.value(c, UserId)
}

fn find_in(c: Context, witness: Type<Tag>): Maybe<Tag> {
  Context.value(c, witness)
}

test "two distincts over one scalar keep two slots" {
  c = Context.with_value(Context.root(), UserId(1))
  both = Context.with_value(c, RequestId(2))
  assert user_of(both) == Some(UserId(1))
  assert Context.value(both, RequestId) == Some(RequestId(2))
}

test "a primitive is keyed by its own type" {
  c = Context.with_value(Context.root(), 5)
  assert Context.value(c, Int) == Some(5)
  assert Context.value(c, String) == None
}

test "a child shadows and the parent is unchanged" {
  parent = Context.with_value(Context.root(), Tag{name: "a"})
  child = Context.with_value(parent, Tag{name: "b"})
  assert find_in(child, Tag) == Some(Tag{name: "b"})
  assert find_in(parent, Tag) == Some(Tag{name: "a"})
}

test "markers compare equal and are values" {
  c = Context.with_value(Context.root(), Flag)
  assert Flag == Flag
  refute Flag != Flag
  assert Context.value(c, Flag) == Some(Flag)
}

test "a failing lookup reports its operands" {
  c = Context.with_value(Context.root(), UserId(3))
  assert Context.value(c, UserId) == Some(UserId(4))
}

test "a case subject records its scrutinee and the arm that ran" {
  c = Context.with_value(Context.root(), UserId(3))
  assert case Context.value(c, UserId) {
    Some(UserId(id)) -> id == 4
    None -> False
  }
}
`

// TestIRContextValue_WitnessesKeyTheStore runs the program above whole.
func TestIRContextValue_WitnessesKeyTheStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context_value_test.nomi")
	if err := os.WriteFile(path, []byte(irContextValueSource), 0600); err != nil {
		t.Fatal(err)
	}
	module := irAssertFixtureVM(t, path)
	// A type name where a `Type<T>` is expected lowers to a witness, never
	// to the marker's value.
	witnesses := 0
	for _, f := range module.Funcs() {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if r, ok := in.(*ir.Ref); ok && r.Kind() == ir.RefTypeWitness {
					witnesses++
				}
			}
		}
	}
	for _, c := range module.Tests() {
		for _, b := range c.Fn().Blocks() {
			for _, in := range b.Instrs() {
				if r, ok := in.(*ir.Ref); ok && r.Kind() == ir.RefTypeWitness {
					witnesses++
				}
			}
		}
	}
	if witnesses == 0 {
		t.Fatal("no retained body reads a Type witness")
	}
}

// TestIRContextValue_RepositoryFixturesRunWhole runs the red fixtures for a
// Context operand's rows and for `if`/`case` assertion subjects.
func TestIRContextValue_RepositoryFixturesRunWhole(t *testing.T) {
	for _, name := range []string{
		"context_values_report.nomi",
		"tests_case_subject.nomi",
		"pipe_stage_keyword_report.nomi", // a piped case records no scrutinee row
	} {
		t.Run(name, func(t *testing.T) {
			abs, err := filepath.Abs(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			irAssertFixtureVM(t, abs)
		})
	}
}
