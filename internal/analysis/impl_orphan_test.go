package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// allModule returns an implModuleOf func that maps every FuncDef to
// the given module name — the common case for these tests, where one
// impl lives in one module.
func allModule(mod string) func(*ast.FuncDef) string {
	return func(*ast.FuncDef) string { return mod }
}

func TestDetectOrphanImpls_AllowsLocalIface(t *testing.T) {
	// Iface in myapp + T in myapp + impl in myapp → no error.
	fn := implFuncDef("greet", "greeter", "Dog")
	index := map[string]map[string][]*ast.FuncDef{
		"greeter": {"greet": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"greeter": "myapp", "Dog": "myapp"})

	if errs := detectOrphanImpls(index, declModule, allModule("myapp"), nil); len(errs) != 0 {
		t.Fatalf("expected no errors, got: %+v", errs)
	}
}

func TestDetectOrphanImpls_AllowsLocalTypeForeignIface(t *testing.T) {
	// Iface in std + T in myapp + impl in myapp → no error (T-local OK).
	fn := implFuncDef("to_string", "Display", "Dog")
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"Display": "std", "Dog": "myapp"})

	if errs := detectOrphanImpls(index, declModule, allModule("myapp"), nil); len(errs) != 0 {
		t.Fatalf("expected no errors, got: %+v", errs)
	}
}

func TestDetectOrphanImpls_AllowsForeignTypeLocalIface(t *testing.T) {
	// Iface in myapp + T in std + impl in myapp → no error (Iface-local OK).
	fn := implFuncDef("speak", "Speakable", "Int")
	index := map[string]map[string][]*ast.FuncDef{
		"Speakable": {"speak": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"Speakable": "myapp", "Int": "std"})

	if errs := detectOrphanImpls(index, declModule, allModule("myapp"), nil); len(errs) != 0 {
		t.Fatalf("expected no errors, got: %+v", errs)
	}
}

func TestDetectOrphanImpls_RejectsForeignIfaceAndForeignType(t *testing.T) {
	// Iface in std + T in stringkit + impl in tools → exactly one error,
	// mentioning both foreign names and the offending impl module.
	fn := implFuncDef("to_string", "Display", "Pad")
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"Display": "std", "Pad": "stringkit"})

	errs := detectOrphanImpls(index, declModule, allModule("tools"), nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 orphan error, got %d: %+v", len(errs), errs)
	}
	msg := errs[0].Message
	for _, want := range []string{"orphan", "Display", "Pad", "std", "stringkit", "tools"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

func TestDetectOrphanImpls_SkipsUnresolvedNames(t *testing.T) {
	// Iface unknown (not in declModule) AND T unknown → orphan check
	// skips; analyzer's other passes report the unknown-name diagnostic.
	fn := implFuncDef("to_string", "MysteryIface", "MysteryType")
	index := map[string]map[string][]*ast.FuncDef{
		"MysteryIface": {"to_string": {fn}},
	}
	declModule := singleDeclModules(map[string]string{}) // both unresolved

	if errs := detectOrphanImpls(index, declModule, allModule("tools"), nil); len(errs) != 0 {
		t.Fatalf("expected no errors for unresolved names, got: %+v", errs)
	}
}

func TestDetectOrphanImpls_SkipsWhenRecvUnresolved(t *testing.T) {
	// Iface known, receiver unresolved → skip. Emitting an orphan error
	// in this state would produce a confusing `for X (declared in
	// module "")` message that doubles up on the unknown-name
	// diagnostic the analyzer's other passes will already report.
	fn := implFuncDef("to_string", "Display", "MysteryType")
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"Display": "std"}) // recv unresolved

	if errs := detectOrphanImpls(index, declModule, allModule("tools"), nil); len(errs) != 0 {
		t.Fatalf("expected no errors when receiver is unresolved, got: %+v", errs)
	}
}

func TestDetectOrphanImpls_SkipsWhenIfaceUnresolved(t *testing.T) {
	// Receiver known, iface unresolved → skip. Symmetric to the
	// recv-unresolved case; same rationale (avoid `(declared in module
	// "")` doubling up on the analyzer's unknown-name diagnostic). This
	// case is the more interesting half of the `||`-deviation in
	// detectOrphanImpls — a plan-faithful `&&` would emit a half-formed
	// orphan error here.
	fn := implFuncDef("to_string", "MysteryIface", "Pad")
	index := map[string]map[string][]*ast.FuncDef{
		"MysteryIface": {"to_string": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"Pad": "stringkit"}) // iface unresolved

	if errs := detectOrphanImpls(index, declModule, allModule("tools"), nil); len(errs) != 0 {
		t.Fatalf("expected no errors when interface is unresolved, got: %+v", errs)
	}
}

func TestDetectOrphanImpls_SkipsUnkeyableImpl(t *testing.T) {
	// implModuleOf returns "" → conservative skip (can't decide if impl
	// is local). Mirrors funcDefReceiverBaseName-""-skip in the
	// collision check.
	fn := implFuncDef("to_string", "Display", "Pad")
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"Display": "std", "Pad": "stringkit"})

	unkeyable := func(*ast.FuncDef) string { return "" }
	if errs := detectOrphanImpls(index, declModule, unkeyable, nil); len(errs) != 0 {
		t.Fatalf("expected no errors when implModuleOf returns \"\", got: %+v", errs)
	}
}

func TestDetectOrphanImpls_SkipsUnkeyableReceiver(t *testing.T) {
	// FuncDef with no first-param type annotation — funcDefReceiverBaseName
	// returns "" → conservative skip (mirrors collision check).
	fn := implFuncDef("to_string", "Display", "Pad")
	fn.Params[0].TypeAnnotation = nil
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fn}},
	}
	declModule := singleDeclModules(map[string]string{"Display": "std", "Pad": "stringkit"})

	if errs := detectOrphanImpls(index, declModule, allModule("tools"), nil); len(errs) != 0 {
		t.Fatalf("expected no errors for receiverless FuncDef, got: %+v", errs)
	}
}

func TestDetectOrphanImpls_MultipleImplsOneOrphan(t *testing.T) {
	// Three impls of Display — two local-on-one-side (OK) and one orphan.
	// The orphan must be the only error, anchored at its own position,
	// and the legitimate impls must pass quietly.
	good1 := implFuncDef("to_string", "Display", "Dog")  // T-local
	good2 := implFuncDef("to_string", "Display", "Cat")  // T-local
	orphan := implFuncDef("to_string", "Display", "Pad") // both foreign
	orphan.Line = 42
	orphan.Col = 7

	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {good1, good2, orphan}},
	}
	declModule := singleDeclModules(map[string]string{
		"Display": "std",
		"Dog":     "myapp",
		"Cat":     "myapp",
		"Pad":     "stringkit",
	})

	errs := detectOrphanImpls(index, declModule, allModule("myapp"), nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 orphan error, got %d: %+v", len(errs), errs)
	}
	if errs[0].Line != 42 || errs[0].Col != 7 {
		t.Errorf("error not anchored at orphan FuncDef: got line=%d col=%d, want 42/7",
			errs[0].Line, errs[0].Col)
	}
	if !strings.Contains(errs[0].Message, "Pad") {
		t.Errorf("error message should name the orphan receiver Pad: %s", errs[0].Message)
	}
}

// A base name declared by two modules is local to EITHER of them. The
// single-valued index this replaced had to pick one, so an impl in the
// module that lost read as an orphan even though it owned the receiver.
func TestDetectOrphanImpls_NameDeclaredInTwoModulesIsLocalToBoth(t *testing.T) {
	dogInApp := implFuncDef("speak", "Speakable", "Dog")
	index := map[string]map[string][]*ast.FuncDef{
		"Speakable": {"speak": {dogInApp}},
	}
	declModule := map[string][]string{
		"Speakable": {"std"},
		// `Dog` is declared by std AND by the project.
		"Dog": {"std", "myapp"},
	}

	if errs := detectOrphanImpls(index, declModule, allModule("myapp"), nil); len(errs) != 0 {
		t.Fatalf("expected no orphan error for a receiver the impl's module declares, got %+v", errs)
	}
	// The same impl in a third module owns neither side, so it is an orphan.
	if errs := detectOrphanImpls(index, declModule, allModule("other"), nil); len(errs) != 1 {
		t.Fatalf("expected 1 orphan error from a module owning neither side, got %d: %+v", len(errs), errs)
	}
}
