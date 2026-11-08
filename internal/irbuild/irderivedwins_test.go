package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// derivedOverLoose declares types whose derived impls hash and compare
// through Loose's hand-written impls, which disagree with the structural
// map-key hash and `==` over Loose's field: every Loose hashes to 7, and two
// compare equal when their last digits agree. Each type's derived hash is
// its formula over 7, so a site that takes the structural fallback (the
// hash a Map key takes) prints an unrelated number.
const derivedOverLoose = `struct Loose {
    n: Int
}

impl Equatable for Loose {
    fn equal(a: Loose, b: Loose) -> Bool {
        a.n % 10 == b.n % 10
    }
}

impl Hashable for Loose {
    fn hash(v: Loose) -> Int {
        _ = v
        7
    }
}

struct SPair {
    a: Loose
    b: Int
}

derive Equatable for SPair
derive Hashable for SPair

enum EBox {
    Two Int
    One Loose
}

derive Equatable for EBox
derive Hashable for EBox

type DLoose Loose

derive Equatable for DLoose
derive Hashable for DLoose

type TPair (Loose, Int)

derive Equatable for TPair
derive Hashable for TPair

struct GBox<T> {
    v: T
}

derive Equatable for GBox<T>
derive Hashable for GBox<T>
`

// derivedOverLooseMain prints, for one value of each type, its hash called
// directly, through a bound, as Maybe's Some, as Result's Err and as a
// one-element list's, then whether it equals the value one Loose digit away
// by `==`, `Equatable.equal`, a bound, `Maybe.equal` and `List.contains`.
const derivedOverLooseMain = `fn h<T>(x: T) -> Int where T: Hashable {
    Hashable.hash(x)
}

fn same<T>(a: T, b: T) -> Bool where T: Equatable {
    Equatable.equal(a, b)
}

fn main() {
    s = SPair{a: Loose{n: 1}, b: 2}
    e = EBox.One(Loose{n: 1})
    d = DLoose(Loose{n: 1})
    t = TPair(Loose{n: 1}, 2)
    g = GBox{v: Loose{n: 1}}
    io.print("${Hashable.hash(s)} ${h(s)} ${Maybe.hash(Some(s))} ${Result.hash(Err(s))} ${Hashable.hash([s])}")
    io.print("${Hashable.hash(e)} ${h(e)} ${Maybe.hash(Some(e))} ${Result.hash(Err(e))} ${Hashable.hash([e])}")
    io.print("${Hashable.hash(d)} ${h(d)} ${Maybe.hash(Some(d))} ${Result.hash(Err(d))} ${Hashable.hash([d])}")
    io.print("${Hashable.hash(t)} ${h(t)} ${Maybe.hash(Some(t))} ${Result.hash(Err(t))} ${Hashable.hash([t])}")
    io.print("${Hashable.hash(g)} ${h(g)} ${Maybe.hash(Some(g))} ${Result.hash(Err(g))} ${Hashable.hash([g])}")
    s2 = SPair{a: Loose{n: 11}, b: 2}
    e2 = EBox.One(Loose{n: 11})
    d2 = DLoose(Loose{n: 11})
    t2 = TPair(Loose{n: 11}, 2)
    g2 = GBox{v: Loose{n: 11}}
    io.print("${s == s2} ${Equatable.equal(s, s2)} ${same(s, s2)} ${Maybe.equal(Some(s), Some(s2))} ${List.contains([s], s2)}")
    io.print("${e == e2} ${Equatable.equal(e, e2)} ${same(e, e2)} ${Maybe.equal(Some(e), Some(e2))} ${List.contains([e], e2)}")
    io.print("${d == d2} ${Equatable.equal(d, d2)} ${same(d, d2)} ${Maybe.equal(Some(d), Some(d2))} ${List.contains([d], d2)}")
    io.print("${t == t2} ${Equatable.equal(t, t2)} ${same(t, t2)} ${Maybe.equal(Some(t), Some(t2))} ${List.contains([t], t2)}")
    io.print("${g == g2} ${Equatable.equal(g, g2)} ${same(g, g2)} ${Maybe.equal(Some(g), Some(g2))} ${List.contains([g], g2)}")
}
`

// derivedOverLooseWant is derivedOverLooseMain's output. SPair and TPair
// hash `7 * 31 + 2`, EBox.One `1 * 31 + 7`, DLoose and GBox `7`; Some adds
// `0 * 31`, Err `1 * 31`, and a one-element list is `17 * 31 + h`.
const derivedOverLooseWant = "219 219 219 250 746\n" +
	"38 38 38 69 565\n" +
	"7 7 7 38 534\n" +
	"219 219 219 250 746\n" +
	"7 7 7 38 534\n"

// A derived Hashable or Equatable wins over the structural fallback a type
// with no impl takes, on a struct, an enum, a distinct type, a named tuple
// and a generic struct: called directly, through a generic bound, and inside
// std's generic bodies (Maybe's and Result's derived hash, List's hash fold,
// `Maybe.equal`, `List.contains`), which are lowered by std instance gens
// that hold none of the program's impls.
func TestIRDerivedImplWinsOverStructural_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, "import std::io\n\n"+derivedOverLoose+"\n"+derivedOverLooseMain,
		derivedOverLooseWant+strings.Repeat("True True True True True\n", 5))
}

// The same with the types and their derives in another file than their
// uses, and one more type, Far, declared and derived in a third file over a
// field type of the second.
func TestIRDerivedImplWinsOverStructural_AnotherFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	pub := strings.NewReplacer("\nstruct ", "\npub struct ", "\nenum ", "\npub enum ", "\ntype ", "\npub type ")
	main := strings.Replace(derivedOverLooseMain, "fn main() {\n", `fn main() {
    f = Far{a: Loose{n: 1}}
    f2 = Far{a: Loose{n: 11}}
    io.print("${Hashable.hash(f)} ${h(f)} ${Maybe.hash(Some(f))} ${Result.hash(Err(f))} ${Hashable.hash([f])}")
    io.print("${f == f2} ${Equatable.equal(f, f2)} ${same(f, f2)} ${Maybe.equal(Some(f), Some(f2))} ${List.contains([f], f2)}")
`, 1)
	files := map[string]string{
		"main.nomi":   "import std::io\nimport shapes.{Loose, SPair, EBox, DLoose, TPair, GBox}\nimport far.{Far}\n\n" + main,
		"shapes.nomi": "pub " + pub.Replace(derivedOverLoose),
		"far.nomi": `import shapes.{Loose}

pub struct Far {
    a: Loose
}

derive Equatable for Far
derive Hashable for Far
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	want := "7 7 7 38 534\nTrue True True True True\n" + derivedOverLooseWant + strings.Repeat("True True True True True\n", 5)
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
