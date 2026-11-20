package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maxDebugImplSites is how many distinct source sites of each class of
// nested Debug body not chosen from the checker's record (noteDebugImpl,
// debugImplClasses) the programs TestIRGolden_RecordsMatchTheVM runs
// reach. Each count only goes down. The counts were the same with the
// artifact cache empty, warm and off. Over `nomi test tests` and `nomi
// test std` the held classes had 45, 0, 11 and 8 sites: the 45 are std's
// own `//!` lines whose typed literal's failure renders an error another
// std module declares (literalRender).
//
//   - "no checker type": a part whose checker type the renderer does not
//     have where it names the part's body, chosen by its kind.
//   - "a type declared in a body": a part whose type no impl can name,
//     whose universal Debug the builder writes.
//   - "an interface value's implementors": a part held as an interface
//     value, which renders through any implementor's body.
//   - "an Iter's sources": an `Iter<T>` part, which renders as any source
//     it may view.
var maxDebugImplSites = map[string]int{
	string(debugImplNoType):      0,
	string(debugImplBodyLocal):   0,
	string(debugImplExistential): 0,
	string(debugImplIterSources): 0,
}

// debugImplRatchet holds the nested Debug body census to
// maxDebugImplSites: every class debugImplClasses lists that is not the
// record's.
func debugImplRatchet() censusRatchet {
	r := censusRatchet{what: "nested Debug body sites", held: map[string]bool{}, max: maxDebugImplSites,
		fix: "give the part its checker type"}
	for class, recorded := range debugImplClasses {
		if !recorded {
			r.held[string(class)] = true
		}
	}
	return r
}

// A type declared in a body renders through the universal Debug the
// builder writes for it, which no record names, inside a tuple and as a
// Maybe's payload.
func TestNoteDebugImpl_ATypeDeclaredInABody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `fn main()
    enum Shape
        Circle Int
        Dot
    end

    dbg (Shape.Dot, Shape.Circle(2))
    dbg Some(Shape.Dot)
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatalf("the program does not lower: %v", err)
	}
}

// An `embeds` variant's value renders through its own type's body, a
// distinct's too, inside a value held as an interface.
func TestNoteDebugImpl_AnEmbeddedDistinct(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import std::io

type Tag String

enum Shape
    embeds Tag
    Dot
end

interface Says
    fn says(value: self) -> String
end

impl Says for Shape
    fn says(_: Shape) -> String
        "shape"
    end
end

struct Holder
    v: Says
end

fn main()
    h = Holder{v: Shape.Dot}
    io.print(Debug.inspect(h.v))
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatalf("the program does not lower: %v", err)
	}
}

// A nested Debug body chosen in a way debugImplClasses does not list
// fails the lowering, naming the site and the class. With every class
// listed, the same program lowers.
func TestNoteDebugImpl_AnUnlistedClassFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `struct Widget
    weight: Int
end

impl Debug for Widget
    fn inspect(w: Widget) -> String
        "w${w.weight}"
    end
end

fn main()
    dbg [Widget{weight: 1}]
end
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the test checks nothing: %v", err)
	}
	lower := func() (failure string) {
		defer func() {
			if r := recover(); r != nil {
				failure = fmt.Sprint(r)
			}
		}()
		if _, _, err := GenerateIR(p); err != nil {
			return err.Error()
		}
		return ""
	}
	if failure := lower(); failure != "" {
		t.Fatalf("the program does not lower with every class listed: %s", failure)
	}
	recorded := debugImplClasses[debugImplFromRecord]
	delete(debugImplClasses, debugImplFromRecord)
	defer func() { debugImplClasses[debugImplFromRecord] = recorded }()
	want := `main.nomi:12:5: a nested Debug body is chosen "from the record", which no class lists`
	if failure := lower(); !strings.Contains(failure, want) {
		t.Fatalf("lowering with the class unlisted failed with %q, want it to name %q", failure, want)
	}
}
