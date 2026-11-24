package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// derivedOverLoose declares types whose derived impls hash and compare
// through Loose's hand-written impls, which disagree with a hash and `==`
// over Loose's field: every Loose hashes to 7, and two compare equal when
// their last digits agree. A site that hashed or compared Loose by its
// field would tell Loose{n: 1} from Loose{n: 11}.
const derivedOverLoose = `struct Loose
    n: Int
end

impl Equatable for Loose
    fn equal(a: Loose, b: Loose) -> Bool
        a.n % 10 == b.n % 10
    end
end

impl Hashable for Loose
    fn hash(v: Loose) -> Int
        _ = v
        7
    end
end

struct SPair
    a: Loose
    b: Int
end

derive Equatable for SPair
derive Hashable for SPair

enum EBox
    Two Int
    One Loose
end

derive Equatable for EBox
derive Hashable for EBox

type DLoose Loose

derive Equatable for DLoose
derive Hashable for DLoose

type TPair (Loose, Int)

derive Equatable for TPair
derive Hashable for TPair

struct GBox<T>
    v: T
end

derive Equatable for GBox<T>
derive Hashable for GBox<T>
`

// derivedOverLooseMain prints, for one value of each type and the value
// one Loose digit away, whether the two hash alike called directly,
// through a bound, as Option's Some, as Result's Err and as a one-element
// list's, then whether they are equal by `==`, `Equatable.equal`, a bound,
// `Option.equal` and `List.contains`. A derived hash's Int is not
// specified, so the rows compare.
const derivedOverLooseMain = `fn h<T>(x: T) -> Int where T: Hashable
    Hashable.hash(x)
end

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    Equatable.equal(a, b)
end

fn main()
    s = SPair{a: Loose{n: 1}, b: 2}
    e = EBox.One(Loose{n: 1})
    d = DLoose(Loose{n: 1})
    t = TPair(Loose{n: 1}, 2)
    g = GBox{v: Loose{n: 1}}
    s2 = SPair{a: Loose{n: 11}, b: 2}
    e2 = EBox.One(Loose{n: 11})
    d2 = DLoose(Loose{n: 11})
    t2 = TPair(Loose{n: 11}, 2)
    g2 = GBox{v: Loose{n: 11}}
    io.print("${Hashable.hash(s) == Hashable.hash(s2)} ${h(s) == h(s2)} ${Option.hash(Some(s)) == Option.hash(Some(s2))} ${Result.hash(Err(s)) == Result.hash(Err(s2))} ${Hashable.hash([s]) == Hashable.hash([s2])}")
    io.print("${Hashable.hash(e) == Hashable.hash(e2)} ${h(e) == h(e2)} ${Option.hash(Some(e)) == Option.hash(Some(e2))} ${Result.hash(Err(e)) == Result.hash(Err(e2))} ${Hashable.hash([e]) == Hashable.hash([e2])}")
    io.print("${Hashable.hash(d) == Hashable.hash(d2)} ${h(d) == h(d2)} ${Option.hash(Some(d)) == Option.hash(Some(d2))} ${Result.hash(Err(d)) == Result.hash(Err(d2))} ${Hashable.hash([d]) == Hashable.hash([d2])}")
    io.print("${Hashable.hash(t) == Hashable.hash(t2)} ${h(t) == h(t2)} ${Option.hash(Some(t)) == Option.hash(Some(t2))} ${Result.hash(Err(t)) == Result.hash(Err(t2))} ${Hashable.hash([t]) == Hashable.hash([t2])}")
    io.print("${Hashable.hash(g) == Hashable.hash(g2)} ${h(g) == h(g2)} ${Option.hash(Some(g)) == Option.hash(Some(g2))} ${Result.hash(Err(g)) == Result.hash(Err(g2))} ${Hashable.hash([g]) == Hashable.hash([g2])}")
    io.print("${s == s2} ${Equatable.equal(s, s2)} ${same(s, s2)} ${Option.equal(Some(s), Some(s2))} ${List.contains([s], s2)}")
    io.print("${e == e2} ${Equatable.equal(e, e2)} ${same(e, e2)} ${Option.equal(Some(e), Some(e2))} ${List.contains([e], e2)}")
    io.print("${d == d2} ${Equatable.equal(d, d2)} ${same(d, d2)} ${Option.equal(Some(d), Some(d2))} ${List.contains([d], d2)}")
    io.print("${t == t2} ${Equatable.equal(t, t2)} ${same(t, t2)} ${Option.equal(Some(t), Some(t2))} ${List.contains([t], t2)}")
    io.print("${g == g2} ${Equatable.equal(g, g2)} ${same(g, g2)} ${Option.equal(Some(g), Some(g2))} ${List.contains([g], g2)}")
end
`

// A derived Hashable or Equatable reaches a part's hand-written impl, on a
// struct, an enum, a distinct type, a named tuple and a generic struct:
// called directly, through a generic bound, and inside std's generic
// bodies (Option's and Result's derived hash, List's hash fold,
// `Option.equal`, `List.contains`), which are lowered by std instance gens
// that hold none of the program's impls.
func TestIRDerivedImplWinsOverStructural_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, "import std::io\n\n"+derivedOverLoose+"\n"+derivedOverLooseMain,
		strings.Repeat("True True True True True\n", 10))
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
	main := strings.Replace(derivedOverLooseMain, "fn main()\n", `fn main()
    f = Far{a: Loose{n: 1}}
    f2 = Far{a: Loose{n: 11}}
    io.print("${Hashable.hash(f) == Hashable.hash(f2)} ${h(f) == h(f2)} ${Option.hash(Some(f)) == Option.hash(Some(f2))} ${Result.hash(Err(f)) == Result.hash(Err(f2))} ${Hashable.hash([f]) == Hashable.hash([f2])}")
    io.print("${f == f2} ${Equatable.equal(f, f2)} ${same(f, f2)} ${Option.equal(Some(f), Some(f2))} ${List.contains([f], f2)}")
`, 1)
	files := map[string]string{
		"main.nomi":   "import std::io\nimport shapes.{Loose, SPair, EBox, DLoose, TPair, GBox}\nimport far.{Far}\n\n" + main,
		"shapes.nomi": "pub " + pub.Replace(derivedOverLoose),
		"far.nomi": `import shapes.{Loose}

pub struct Far
    a: Loose
end

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
	want := strings.Repeat("True True True True True\n", 12)
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
