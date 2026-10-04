package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Cross-module tests for opaque distinct types (spec §15.3).
//
// Single-file tests of opacity bookkeeping live in opaque_test.go;
// these tests need a real two-file project to exercise the boundary.

func opaqueProject(t *testing.T, files map[string]string) (*analysis.FileAnalysis, []analysis.TypeError) {
	t.Helper()
	tmp := t.TempDir()

	for rel, content := range files {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mainData, err := os.ReadFile(filepath.Join(tmp, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := lexer.Lex(string(mainData))
	entryNodes, _ := parser.ParseWithRecovery(tokens)

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		fp := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(fp)
		if err != nil {
			return nil, err
		}
		fileTokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(fileTokens)
		return nodes, nil
	}

	lib := std.Load()
	fa, cache, siblingNodes := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis from BuildProject")
	}
	// BuildProject does NOT run CheckTypes — run it explicitly so the
	// checker's enforcement passes (constructor opacity, etc.) actually fire.
	checkErrs := analysis.CheckTypes(fa, entryNodes)
	all := append([]analysis.TypeError{}, fa.TypeErrors...)
	all = append(all, checkErrs...)
	for key, siblingFA := range cache {
		if analysis.IsStdlibKey(key) {
			continue
		}
		nodes := siblingNodes[key]
		all = append(all, siblingFA.TypeErrors...)
		all = append(all, analysis.CheckTypes(siblingFA, nodes)...)
	}
	return fa, all
}

func opaqueExpectErrorContains(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message
	}
	t.Fatalf("expected an error containing %q, got %d errors:\n  %s",
		substr, len(errs), strings.Join(msgs, "\n  "))
}

func opaqueExpectNoErrors(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	errs = withoutUnusedBindingErrors(errs)
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message
		}
		t.Fatalf("expected no errors, got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
	}
}

// Round 2.2 — constructor calls outside the owning module are blocked.

func TestOpaque_BlocksConstructorOutsideOwningModule(t *testing.T) {
	// Unqualified-import form — selective import lifts PositiveInt into
	// local scope; calling `PositiveInt(5)` directly is the common case.
	_, errs := opaqueProject(t, map[string]string{
		"positive_int.nomi": `

pub opaque type PositiveInt Int

pub fn from_int(n: Int): Maybe<PositiveInt> {
  if n > 0 { Some(PositiveInt(n)) } else { None }
}
`,
		"main.nomi": `import positive_int.{PositiveInt}

fn main() {
  p = PositiveInt(5)
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_AllowsConstructorInsideOwningModule(t *testing.T) {
	// Smart constructor inside the owning module uses PositiveInt(n)
	// freely. This must not error. Calling from main uses the exported
	// `from_int`.
	_, errs := opaqueProject(t, map[string]string{
		"positive_int.nomi": `

pub opaque type PositiveInt Int

pub fn from_int(n: Int): Maybe<PositiveInt> {
  if n > 0 { Some(PositiveInt(n)) } else { None }
}
`,
		"main.nomi": `import positive_int

fn main() {
  p = positive_int.from_int(5)
}
`,
	})
	opaqueExpectNoErrors(t, errs)
}

func TestOpaque_NonOpaqueConstructorAllowedOutside(t *testing.T) {
	// Sanity check: a non-opaque distinct type's constructor IS callable
	// from outside (existing behavior that opaque carves out from). Test
	// fails only if the new opacity check rejects non-opaque types too.
	_, errs := opaqueProject(t, map[string]string{
		"open_int.nomi": `pub type OpenInt Int
`,
		"main.nomi": `import open_int.{OpenInt}

fn main() {
  p = OpenInt(5)
}
`,
	})
	opaqueExpectNoErrors(t, errs)
}

// Round 2.3 — unwrap calls outside the owning module are blocked.

func TestOpaque_BlocksUnwrapOutsideOwningModule(t *testing.T) {
	// Outside the owning module, `Int(p)` for an opaque PositiveInt
	// would expose the private representation. Block it.
	_, errs := opaqueProject(t, map[string]string{
		"positive_int.nomi": `

pub opaque type PositiveInt Int

pub fn from_int(n: Int): Maybe<PositiveInt> {
  if n > 0 { Some(PositiveInt(n)) } else { None }
}
`,
		"main.nomi": `import positive_int

fn main() {
  p = positive_int.from_int(5)
  case p {
    Some(v) -> Int(v)
    None -> 0
  }
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_AllowsUnwrapInsideOwningModule(t *testing.T) {
	// Inside the owning module, unwrap is fine — it's how the owner
	// impl its own accessors.
	_, errs := opaqueProject(t, map[string]string{
		"positive_int.nomi": `

pub opaque type PositiveInt Int

pub fn from_int(n: Int): Maybe<PositiveInt> {
  if n > 0 { Some(PositiveInt(n)) } else { None }
}

pub fn to_int(p: PositiveInt): Int {
  Int(p)
}
`,
		"main.nomi": `import positive_int

fn main() {
  p = positive_int.from_int(5)
  Unit
}
`,
	})
	opaqueExpectNoErrors(t, errs)
}

func TestOpaque_AllowsUnwrapOnNonOpaqueOutside(t *testing.T) {
	// Sanity check: a non-opaque distinct type can still be unwrapped
	// from outside. The new check must not over-fire.
	_, errs := opaqueProject(t, map[string]string{
		"open_int.nomi": `pub type OpenInt Int
`,
		"main.nomi": `import open_int.{OpenInt}

fn main(): Int {
  v = OpenInt(5)
  Int(v)
}
`,
	})
	opaqueExpectNoErrors(t, errs)
}

// Round 2.4 — pattern destructuring outside owning module is blocked.

func TestOpaque_BlocksDestructuringOutsideOwningModule(t *testing.T) {
	// Outside the owning module, `case p { PositiveInt(n) -> ... }`
	// destructures the opaque type and exposes the wrapped value.
	// Block it.
	_, errs := opaqueProject(t, map[string]string{
		"positive_int.nomi": `

pub opaque type PositiveInt Int

pub fn from_int(n: Int): Maybe<PositiveInt> {
  if n > 0 { Some(PositiveInt(n)) } else { None }
}
`,
		"main.nomi": `import positive_int.{PositiveInt}

fn unwrap(p: PositiveInt): Int {
  case p {
    PositiveInt(n) -> n
  }
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_AllowsDestructuringInsideOwningModule(t *testing.T) {
	// Inside the owning module, `case p { PositiveInt(n) -> ... }` is
	// fine — that's how the owner accesses its own representation.
	_, errs := opaqueProject(t, map[string]string{
		"positive_int.nomi": `

pub opaque type PositiveInt Int

pub fn from_int(n: Int): Maybe<PositiveInt> {
  if n > 0 { Some(PositiveInt(n)) } else { None }
}

pub fn to_int(p: PositiveInt): Int {
  case p {
    PositiveInt(n) -> n
  }
}
`,
		"main.nomi": `import positive_int

fn main() {
  p = positive_int.from_int(5)
  Unit
}
`,
	})
	opaqueExpectNoErrors(t, errs)
}

// Round 2.5 — opacity semantics on inline struct-bodied opaque types.
// `pub opaque struct Date { year: Int; month: Int; day: Int }` exposes
// the type name but keeps the constructor and field access private to
// the owning module. Mirrors the primitive-distinct cases above.

func TestOpaque_StructBody_BlocksConstructorOutsideOwningModule(t *testing.T) {
	_, errs := opaqueProject(t, map[string]string{
		"date.nomi": `pub opaque struct Date {
  year: Int
  month: Int
  day: Int
}

pub fn make(year: Int, month: Int, day: Int): Date {
  Date{year: year, month: month, day: day}
}

`,
		"main.nomi": `import date.{Date}

fn main() {
  d = Date{year: 2026, month: 5, day: 4}
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_StructBody_BlocksFieldAccessOutsideOwningModule(t *testing.T) {
	// Field access on an opaque struct from outside its module is
	// rejected. Same-module accessors still see fields directly.
	_, errs := opaqueProject(t, map[string]string{
		"date.nomi": `pub opaque struct Date {
  year: Int
  month: Int
  day: Int
}

pub fn make(year: Int, month: Int, day: Int): Date {
  Date{year: year, month: month, day: day}
}

pub fn year(d: Date): Int { d.year }
pub fn month(d: Date): Int { d.month }
pub fn day(d: Date): Int { d.day }

`,
		"main.nomi": `import date.{Date}

fn report(d: Date): Int {
  d.year
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_StructBody_AllowsAccessorsInsideOwningModule(t *testing.T) {
	// Inside the module, both constructor calls and field access work.
	// Outside callers go through the exported accessors. Mirrors the
	// canonical Date shape from std/calendar but uses `cal.nomi` so the
	// short-name doesn't collide with stdlib's `std/calendar` — the
	// post-stdlib-as-package-cutover collision check rejects two
	// reachable modules sharing a last-path segment.
	_, errs := opaqueProject(t, map[string]string{
		"cal.nomi": `pub opaque struct Date {
  year: Int
  month: Int
  day: Int
}

pub fn make(year: Int, month: Int, day: Int): Date {
  Date{year: year, month: month, day: day}
}

pub fn year(d: Date): Int { d.year }
pub fn month(d: Date): Int { d.month }
pub fn day(d: Date): Int { d.day }

`,
		"main.nomi": `import cal

fn main() {
  d = cal.make(2026, 5, 4)
  y = cal.year(d)
}
`,
	})
	opaqueExpectNoErrors(t, errs)
}

func TestOpaque_StructBody_BlocksDestructuringOutsideOwningModule(t *testing.T) {
	// Struct destructuring (`case d { Date{year, ...} -> ... }`) on an
	// opaque struct from outside its module is also blocked.
	_, errs := opaqueProject(t, map[string]string{
		"date.nomi": `pub opaque struct Date {
  year: Int
  month: Int
  day: Int
}

pub fn make(year: Int, month: Int, day: Int): Date {
  Date{year: year, month: month, day: day}
}

`,
		"main.nomi": `import date.{Date}

fn report(d: Date): Int {
  case d {
    Date{year: y, month: m, day: dd} -> y
  }
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

// Round 2.6 — opacity semantics on inline enum-bodied opaque types.

func TestOpaque_EnumBody_BlocksVariantConstructorOutsideOwningModule(t *testing.T) {
	// Constructing a variant of an opaque enum from outside its module
	// is blocked — outside callers go through exported smart constructors.
	_, errs := opaqueProject(t, map[string]string{
		"status.nomi": `pub opaque enum Status {
  Active
  Inactive String
}

pub fn active(): Status { Status.Active }
pub fn inactive(reason: String): Status { Status.Inactive(reason) }
`,
		"main.nomi": `import status.{Status}

fn main() {
  s = Status.Active
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_EnumBody_BlocksDataVariantConstructorOutsideOwningModule(t *testing.T) {
	_, errs := opaqueProject(t, map[string]string{
		"status.nomi": `pub opaque enum Status {
  Active
  Inactive String
}

pub fn active(): Status { Status.Active }
pub fn inactive(reason: String): Status { Status.Inactive(reason) }
`,
		"main.nomi": `import status.{Status}

fn main() {
  s = Status.Inactive("reason")
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_EnumBody_BlocksDestructuringOutsideOwningModule(t *testing.T) {
	_, errs := opaqueProject(t, map[string]string{
		"status.nomi": `pub opaque enum Status {
  Active
  Inactive String
}

pub fn active(): Status { Status.Active }
pub fn inactive(reason: String): Status { Status.Inactive(reason) }
`,
		"main.nomi": `import status.{Status}

fn describe(s: Status): String {
  case s {
    Active -> "active"
    Inactive(r) -> r
  }
}
`,
	})
	opaqueExpectErrorContains(t, errs, "opaque")
}

func TestOpaque_EnumBody_AllowsConstructorsInsideOwningModule(t *testing.T) {
	_, errs := opaqueProject(t, map[string]string{
		"status.nomi": `pub opaque enum Status {
  Active
  Inactive String
}

pub fn active(): Status { Status.Active }
pub fn inactive(reason: String): Status { Status.Inactive(reason) }

`,
		"main.nomi": `import status

fn main() {
  s = status.active()
}
`,
	})
	opaqueExpectNoErrors(t, errs)
}
