package irbuild

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/projectgen"
)

// TestLowerFileMatchesTheWholeLowering lowers every file of generated
// projects on its own, from its check against its imports' interfaces and
// the kept objects of the other files (LowerFile), and requires each
// file's module to be the whole lowering's, part by part, and the linked
// program to define everything the whole one does. None may miss: a
// generated project's files are all lowerable on their own.
// NOMI_PERFILE_LINES and NOMI_PERFILE_SEED pick another project.
func TestLowerFileMatchesTheWholeLowering(t *testing.T) {
	lines, seeds := 1000, []int64{1, 2, 3}
	if n, err := strconv.Atoi(os.Getenv("NOMI_PERFILE_LINES")); err == nil {
		lines = n
	}
	if n, err := strconv.Atoi(os.Getenv("NOMI_PERFILE_SEED")); err == nil {
		seeds = []int64{int64(n)}
	}
	for _, seed := range seeds {
		gen := projectgen.Generate(projectgen.Config{Seed: seed, Lines: lines})
		dir := t.TempDir()
		if err := gen.Write(dir); err != nil {
			t.Fatal(err)
		}
		entry := filepath.Join(dir, "main.nomi")
		fe := frontend.New(frontend.Config{SourceBoundProvided: true})
		proj, err := fe.CheckFile(entry, frontend.Mode{})
		if err != nil {
			t.Fatal(err)
		}
		res, refusal, err := GenerateIR(fileProgram(entry, proj, false))
		if err != nil || refusal != nil {
			t.Fatal(err, refusal)
		}
		kept, err := res.Keep()
		if err != nil {
			t.Fatal(err)
		}
		if kept, err = DecodeKeptLowering(kept.Encode()); err != nil {
			t.Fatal(err)
		}
		pi := fe.Interfaces()
		for _, key := range pi.Keys() {
			path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
			got, err := LowerFile(FileProgram(pi.Check(key), key, path, dir, false), kept, false)
			var fm *FileMiss
			if errors.As(err, &fm) {
				t.Errorf("seed %d: %s cannot be lowered on its own: %s", seed, key, fm.Why)
				continue
			}
			if err != nil {
				t.Fatalf("seed %d: %s: %v", seed, key, err)
			}
			if err := sameModule(irModuleNamed(res.IR, path), irModuleNamed(got.IR, path)); err != nil {
				t.Errorf("seed %d: %s lowered on its own differs: %v", seed, key, err)
			}
			if lost := lostDefinitions(res.IRModules(), got.IRModules()); lost != "" {
				t.Errorf("seed %d: the program %s links lacks %s", seed, key, lost)
			}
			if split := splitDefinitions(got.IRModules()); split != "" {
				t.Errorf("seed %d: the program %s links names %s", seed, key, split)
			}
			if _, err := got.Rekeep(kept); err != nil {
				t.Errorf("seed %d: %s: %v", seed, key, err)
			}
		}
	}
}

// TestKeptLoweringRefusesACorruptEncoding requires a kept lowering that is
// truncated, has a byte changed or is another format to be refused.
func TestKeptLoweringRefusesACorruptEncoding(t *testing.T) {
	k := &KeptLowering{EntryPath: "/p/main.nomi", HasMain: true, Units: []KeptUnit{
		{Key: "main", Path: "/p/main.nomi", Object: []byte("object"), Decls: map[string][32]byte{"main": {1}}},
		{Key: "util", Path: "/p/util.nomi", Rules: []RefinementRuleSite{{Path: "/p/util.nomi", Line: 2, Type: "Small", Key: "k"}, {Type: "Plain"}}, RuleChecks: []int{0}},
	}, Listed: []string{"http"}, Dangling: []string{"util.f"}, Declines: map[string][]string{"/p/util.nomi": {"f: why"}},
		DebugRenderings: []KeptDebugRendering{{Iface: "8:std/display.Display", Renderers: []string{"", "/p/util.nomi"}, Implementers: []string{"/p/main.nomi"}}}}
	data := k.Encode()
	back, err := DecodeKeptLowering(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(back.Encode()) != string(data) {
		t.Fatal("a kept lowering does not read back as it was written")
	}
	for name, bad := range map[string][]byte{
		"truncated":   data[:len(data)-3],
		"empty":       nil,
		"another":     []byte("NOMILOAD\x01"),
		"byte change": append(append([]byte(nil), data[:len(data)-1]...), data[len(data)-1]^0xff),
	} {
		if _, err := DecodeKeptLowering(bad); err == nil {
			t.Errorf("%s: a corrupt kept lowering reads back", name)
		}
	}
}

// TestTemplateDeclNamesTheDeclarationAnInstanceIsMadeFrom holds the
// longest-match rule over declaration names that hold `<`, `.` and `/`.
func TestTemplateDeclNamesTheDeclarationAnInstanceIsMadeFrom(t *testing.T) {
	decls := map[string][32]byte{"first_or": {}, "impl Equatable for Tree<T>": {}, "Tree": {}, "helper": {}, "Rec#2": {}}
	for link, want := range map[string]string{
		"core/util.first_or<2>": "first_or",
		"sat.impl Equatable for Tree<T>[6@sat.Tree<4>].equal<{T=4}>": "impl Equatable for Tree<T>",
		"type 6@sat.Tree..children default":                          "Tree",
		"sat.helper/step":                                            "helper",
		"sat.Rec#2[6@sat.Rec].new":                                   "Rec#2",
		"other.first_or<2>":                                          "",
		"sat.missing<2>":                                             "",
	} {
		key := "sat"
		if link == "core/util.first_or<2>" {
			key = "core/util"
		}
		got, _ := templateDecl(link, key, decls)
		if got != want {
			t.Errorf("templateDecl(%q) = %q, want %q", link, got, want)
		}
	}
}

// TestLowerFileMissesARenderingOfAnotherFilesImplementers lowers on its
// own each file of a program whose named.nomi renders a struct holding
// Display values through Debug, which main.nomi implements and boxes
// into. The Render named.nomi's Debug body builds names main.nomi's impl,
// which named.nomi's check never reads, and main.nomi's implementers are
// what named.nomi's kept object names: each file must miss (debugMiss),
// rather than lower a Render that names none or link a stale one.
func TestLowerFileMissesARenderingOfAnotherFilesImplementers(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"named.nomi": `pub struct Holder
    held: List<Display>
end
`,
		"main.nomi": `import
    std::io
    named.{Holder}
end

struct Mark
    k: Int
end

impl Display for Mark
    fn to_string(m: Mark) -> String
        "m${m.k}"
    end
end

fn main()
    h = Holder{held: [Mark{k: 1}]}
    io.print(Debug.inspect(h))
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	fe := frontend.New(frontend.Config{SourceBoundProvided: true})
	proj, err := fe.CheckFile(entry, frontend.Mode{})
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	res, refusal, err := GenerateIR(fileProgram(entry, proj, false))
	if err != nil || refusal != nil {
		t.Fatalf("the program does not lower: %v %v", err, refusal)
	}
	kept, err := res.Keep()
	if err != nil {
		t.Fatal(err)
	}
	if kept, err = DecodeKeptLowering(kept.Encode()); err != nil {
		t.Fatal(err)
	}
	if len(kept.DebugRenderings) == 0 {
		t.Fatal("the kept lowering records no rendering through every implementer's Debug")
	}
	pi := fe.Interfaces()
	for _, key := range pi.Keys() {
		path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
		_, err := LowerFile(FileProgram(pi.Check(key), key, path, dir, false), kept, false)
		var fm *FileMiss
		if !errors.As(err, &fm) {
			t.Errorf("%s lowers on its own (%v); want a miss", key, err)
			continue
		}
		if !strings.Contains(fm.Why, "through every implementer's Debug") {
			t.Errorf("%s misses for another reason: %s", key, fm.Why)
		}
	}
}

// TestRelowerEachFileLinksTheRefinementChecks lowers a file whose literals
// are checked as a generic std subtype (a range literal as a ClosedRange,
// whose step calls NonEmptyRange's try_from<Int>) on its own and links it
// (RelowerEachFile), and requires every function vmhost runs to check
// them to name the linked program's symbols. A check the whole lowering
// made calls the try_from instance by a symbol the linked program does
// not define, which the VM reports as not retained.
func TestRelowerEachFileLinksTheRefinementChecks(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	src := `import std::io

fn main()
    r: ClosedRange<Int> = 0..=10
    io.print(Int.clamp(15, r))
    io.print(Int.clamp(-3, 0..=10))
end
`
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fe := frontend.New(frontend.Config{SourceBoundProvided: true})
	proj, err := fe.CheckFile(entry, frontend.Mode{})
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	p := fileProgram(entry, proj, false)
	p.interfaces = fe.Interfaces
	defer RelowerEachFile()()
	res, refusal, err := GenerateIR(p)
	if err != nil || refusal != nil {
		t.Fatalf("the program does not lower: %v %v", err, refusal)
	}
	checks := ir.NewModule("refinement checks")
	for _, s := range res.Refinements {
		if s.Build != nil {
			checks.AddFunc(s.Build)
		}
		for _, st := range s.Steps {
			if st.Check != nil {
				checks.AddFunc(st.Check)
			}
		}
	}
	if checks.Empty() {
		t.Fatal("no refinement literal has a generic step's check; the fixture is wrong")
	}
	if split := splitDefinitions(append(res.IRModules(), checks)); split != "" {
		t.Fatalf("a refinement check of the program linked from the file lowered on its own names %s", split)
	}
}

// TestLowerFileKeepsAnotherFilesGenericRuleCheck lowers main.nomi on its
// own against the kept object of many.nomi, which declares a generic
// subtype. The rule's Check (analysis.RefinementRuleCheck), which vmhost
// walks for effects, must come through the kept unit (KeptUnit.RuleChecks)
// as the whole lowering made it, decoded against the linked program.
func TestLowerFileKeepsAnotherFilesGenericRuleCheck(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"many.nomi": "pub subtype Many<T> List<T> when fn xs -> not Iter.is_empty(xs)\n",
		"main.nomi": `import
    std::io
    many.{Many}
end

fn main()
    xs: Many<Int> = [1, 2]
    io.print(List.length(xs))
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	fe := frontend.New(frontend.Config{SourceBoundProvided: true})
	proj, err := fe.CheckFile(entry, frontend.Mode{})
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	res, refusal, err := GenerateIR(fileProgram(entry, proj, false))
	if err != nil || refusal != nil {
		t.Fatalf("the program does not lower: %v %v", err, refusal)
	}
	checked := func(r *Result) int {
		n := 0
		for _, rule := range r.RefinementRules {
			if rule.Type == "Many" && rule.Check != nil {
				n++
			}
		}
		return n
	}
	if checked(res) == 0 {
		t.Fatal("the whole lowering makes no Check for Many's rule; the fixture is wrong")
	}
	kept, err := res.Keep()
	if err != nil {
		t.Fatal(err)
	}
	if kept, err = DecodeKeptLowering(kept.Encode()); err != nil {
		t.Fatal(err)
	}
	pi := fe.Interfaces()
	got, err := LowerFile(FileProgram(pi.Check("main"), "main", entry, dir, false), kept, false)
	if err != nil {
		t.Fatalf("main.nomi does not lower on its own: %v", err)
	}
	if checked(got) != checked(res) {
		t.Fatalf("main.nomi lowered on its own links %d checks of Many's rule, the whole lowering %d", checked(got), checked(res))
	}
	checks := ir.NewModule("rule checks")
	for _, rule := range got.RefinementRules {
		if rule.Check != nil {
			checks.AddFunc(rule.Check)
		}
	}
	if split := splitDefinitions(append(got.IRModules(), checks)); split != "" {
		t.Fatalf("a rule check of the program main.nomi lowered on its own links names %s", split)
	}
}
