package irbuild

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
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
		{Key: "util", Path: "/p/util.nomi"},
	}, Listed: []string{"http"}, Dangling: []string{"util.f"}, Declines: map[string][]string{"/p/util.nomi": {"f: why"}}}
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
