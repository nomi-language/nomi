package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

func TestCheck_TypeAlias(t *testing.T) {
	_, errs := checkSource(`
typealias Mapper (Int) -> Int
fn apply(f: Mapper, x: Int) -> Int { f(x) }
fn demo() -> Int { apply(fn n -> n + 1, 5) }
`)
	expectNoErrors(t, errs)
}

// An alias is a spelling of its target in type positions; nothing hangs
// functions off it. An impl, a derive and a type-qualified call through one
// are errors that name the target, for a local alias and an imported one.
func TestCheck_ImplThroughTypeAliasIsAnError(t *testing.T) {
	_, errs := checkSource(`
struct Pt {
    x: Int
}
typealias P Pt
impl P {
    fn get(p: P) -> Int {
        p.x
    }
}
impl Display for P {
    fn to_string(p: P) -> String {
        "${p.x}"
    }
}
`)
	expectError(t, errs, "`P` is a typealias for `Pt`; write `impl Pt`")
	expectError(t, errs, "`P` is a typealias for `Pt`; write `impl Display for Pt`")
}

func TestLowerDerives_DeriveThroughTypeAliasIsAnError(t *testing.T) {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(`
struct Pt {
    x: Int
}
typealias P Pt
derive Debug for P
`))
	_, errs := LowerDerives(nodes)
	expectError(t, errs, "`P` is a typealias for `Pt`; write `derive Debug for Pt`")
}

func TestCheck_ImplThroughAnImportedTypeAliasIsAnError(t *testing.T) {
	_, errs := checkSourceWithModules(t, `
import shapes.{P, Pt}
impl Display for P {
    fn to_string(p: P) -> String {
        "${p.x}"
    }
}
`, map[string]string{
		"shapes": `pub struct Pt {
    pub x: Int
}
pub typealias P Pt
`,
	})
	expectError(t, errs, "`P` is a typealias for `Pt`; write `impl Display for Pt`")
}

func TestCheck_CallThroughTypeAliasIsAnError(t *testing.T) {
	_, errs := checkSourceWithModules(t, `
import shapes
struct Pt {
    x: Int
}
impl Pt {
    fn get(p: Pt) -> Int {
        p.x
    }
}
typealias P Pt
fn use_all() -> Int {
    P.get(#P{x: 1}) + shapes.Q.y_of(#shapes.Q{y: 2})
}
`, map[string]string{
		"shapes": `pub struct Qt {
    y: Int
}
pub typealias Q Qt
impl Qt {
    pub fn y_of(q: Qt) -> Int {
        q.y
    }
}
`,
	})
	expectError(t, errs, "`P` is a typealias for `Pt`; call `Pt.get`")
	expectError(t, errs, "`shapes.Q` is a typealias for `Qt`; call `shapes.Qt.y_of`")
}

// A variant through an alias of an enum is a value of the target, and a
// struct literal through an alias builds the target.
func TestCheck_VariantAndLiteralThroughTypeAlias(t *testing.T) {
	_, errs := checkSource(`
enum Color {
    Red
    Green
}
struct Pt {
    x: Int
}
typealias L Color
typealias P Pt
fn red() -> Bool {
    L.Red == Color.Red
}
fn origin() -> Pt {
    #P{x: 0}
}
`)
	expectNoErrors(t, errs)
}
