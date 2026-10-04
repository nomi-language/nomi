package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An interface-typed struct field admitted by the struct literal must also be
// admitted by a `Struct.update` patch. Before `deepPartialMatches` reached
// `ifaceParamAdmits`, the literal accepted `FakeClock` where the field is typed
// `Clock` and the patch refused the same value with
// `argument 2: expected Partial<Cfg>, got {clock: FakeClock}` — two admission
// paths disagreeing about one rule.
const ifaceFieldSrc = `pub interface Clock {
  fn at(c: self): Int
}

pub struct FakeClock {
  t: Int
}

impl Clock for FakeClock {
  fn at(c: FakeClock): Int { c.t }
}

pub struct Cfg {
  clock: Clock
  port: Int
}

fn patch(): Cfg {
  base = Cfg{clock: FakeClock{t: 1}, port: 80}
  Struct.update(base, {clock: FakeClock{t: 4242}})
}
`

func TestPartialIfaceField_UpdateAdmitsImplementer(t *testing.T) {
	_, errs := checkSourceWithStdlib(ifaceFieldSrc)
	if len(errs) != 0 {
		t.Fatalf("Struct.update must admit an interface impl where the struct literal does;\ngot %d diagnostic(s):\n  %s",
			len(errs), pifMsgs(errs))
	}
}

// The control for the test above: the literal form must be accepted too. If
// this ever fails, the test above is not measuring a disagreement between the
// two paths — it is measuring a program that is broken for some other reason.
func TestPartialIfaceField_LiteralControlStillAccepted(t *testing.T) {
	src := strings.Replace(ifaceFieldSrc,
		"Struct.update(base, {clock: FakeClock{t: 4242}})",
		"Cfg{clock: FakeClock{t: 4242}, port: base.port}", 1)
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("the struct-literal control must be clean; got:\n  %s", pifMsgs(errs))
	}
}

// A NON-implementer must still be refused through the patch path. This is the
// planted positive behind the zero above: without it, a change that made
// `deepPartialMatches` return true unconditionally would pass the admission
// test and delete the check entirely.
func TestPartialIfaceField_UpdateRejectsNonImplementer(t *testing.T) {
	src := strings.Replace(ifaceFieldSrc,
		"Struct.update(base, {clock: FakeClock{t: 4242}})",
		"Struct.update(base, {clock: NoClock{t: 4242}})", 1)
	src = strings.Replace(src, "pub struct Cfg {",
		"pub struct NoClock {\n  t: Int\n}\n\npub struct Cfg {", 1)
	_, errs := checkSourceWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("a patch field whose value does not implement the field's interface must be refused")
	}
}

// The patch walk is deep, so the widening must apply at depth too: a nested
// patch reaching an interface-typed field one level down is the same rule.
func TestPartialIfaceField_UpdateAdmitsImplementerAtDepth(t *testing.T) {
	src := `pub interface Clock {
  fn at(c: self): Int
}

pub struct FakeClock {
  t: Int
}

impl Clock for FakeClock {
  fn at(c: FakeClock): Int { c.t }
}

pub struct Inner {
  clock: Clock
  tag: String
}

pub struct Outer {
  inner: Inner
  port: Int
}

fn patch(): Outer {
  base = Outer{inner: Inner{clock: FakeClock{t: 1}, tag: "a"}, port: 80}
  Struct.update(base, {inner: {clock: FakeClock{t: 4242}}})
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("a nested patch must admit an interface impl one level down;\ngot %d diagnostic(s):\n  %s",
			len(errs), pifMsgs(errs))
	}
}

// A non-implementer at depth must still be refused, so the depth test above is
// not passing because the recursion stopped checking.
func TestPartialIfaceField_UpdateRejectsNonImplementerAtDepth(t *testing.T) {
	src := `pub interface Clock {
  fn at(c: self): Int
}

pub struct FakeClock {
  t: Int
}

impl Clock for FakeClock {
  fn at(c: FakeClock): Int { c.t }
}

pub struct NoClock {
  t: Int
}

pub struct Inner {
  clock: Clock
  tag: String
}

pub struct Outer {
  inner: Inner
  port: Int
}

fn patch(): Outer {
  base = Outer{inner: Inner{clock: FakeClock{t: 1}, tag: "a"}, port: 80}
  Struct.update(base, {inner: {clock: NoClock{t: 4242}}})
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("a non-implementer one level down in a patch must be refused")
	}
}

// Admission must also RECORD the conformance, and the implementer under test
// must appear ONLY in the patch. That is the whole point: if the same concrete
// type also appears in a struct literal in the same program, the literal path
// records the pair and this test passes no matter what the patch path does.
// A first version of this test used `FakeClock`, which the literal in
// `ifaceFieldSrc` also constructs, and a planted mutant — a hand-written
// interface check inside `deepPartialMatches` that returned the right verdict
// and skipped the recording — PASSED it. `RealClock` below is constructed
// nowhere but the patch, so the manifest entry can only come from the patch.
//
// This is the guard on SHARING rather than on the verdict: a reimplementation
// that forgets the recording type checks clean and then fails to dispatch.
func TestPartialIfaceField_UpdateRecordsConformance(t *testing.T) {
	const src = `pub interface Clock {
  fn at(c: self): Int
}

pub struct FakeClock {
  t: Int
}

impl Clock for FakeClock {
  fn at(c: FakeClock): Int { c.t }
}

pub struct RealClock {
  base: Int
}

impl Clock for RealClock {
  fn at(c: RealClock): Int { c.base }
}

pub struct Cfg {
  clock: Clock
  port: Int
}

fn patch(): Cfg {
  base = Cfg{clock: FakeClock{t: 1}, port: 80}
  Struct.update(base, {clock: RealClock{base: 42}})
}
`
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %s", pifMsgs(errs))
	}
	if fa == nil || fa.ImplManifest == nil {
		t.Fatal("no impl manifest produced")
	}
	// ImplManifest is keyed [ifaceName][typeName] (see RecordManifest).
	types, ok := fa.ImplManifest["Clock"]
	if !ok {
		t.Fatalf("patch admission did not record any Clock conformance; manifest: %v", fa.ImplManifest)
	}
	if _, ok := types["RealClock"]; !ok {
		t.Fatalf("the patch is the only place RealClock is constructed, so a missing "+
			"(RealClock, Clock) entry means the patch path admitted without recording; got %v", types)
	}
}

// pifMsgs renders diagnostics for the failure messages above. Local to this
// file because the shared `joinMessages` lives in the external `analysis_test`
// package and takes `[]analysis.TypeError`.
func pifMsgs(errs []analysis.TypeError) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Message
	}
	return strings.Join(parts, "\n  ")
}
