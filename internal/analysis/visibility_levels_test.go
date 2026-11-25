package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The three levels of visibility over a program of three files in two
// modules (spec §14 *Visibility*): app/main.nomi names declarations of
// app/engine.nomi, a file of its own module, and of kit/kit.nomi, the one
// file of the module kit, which app's nomi.toml names by its directory.
// Each file declares one function and one type at each level.

// levelDecls declares, in a file whose functions are prefixed fn and types
// Ty, a function and a type at each level.
func levelDecls(fn, ty string) string {
	return fmt.Sprintf(`pub fn %[1]s_public() -> Int
    1
end

fn %[1]s_internal() -> Int
    2
end

private fn %[1]s_private() -> Int
    3
end

pub struct %[2]sPublic
    n: Int
end

struct %[2]sInternal
    n: Int
end

private struct %[2]sPrivate
    n: Int
end

pub fn %[1]s_uses() -> Int
    %[1]s_internal() + %[1]s_private() + %[2]sPrivate{n: 1}.n + %[2]sInternal{n: 1}.n
end
`, fn, ty)
}

// levelProgram is the program whose entry is main, beside engine and kit.
func levelProgram(main string) map[string]string {
	files := appModule(main, "kit")
	files["app/engine.nomi"] = levelDecls("engine", "Engine")
	return library(files, "kit", "kit", levelDecls("kit", "Kit"))
}

// Every pair of a level and a place a use stands in, by qualified use and
// by import: what is seen compiles, and what is not is the error that says
// the level, with its Code, and fails if the program starts to compile.
func TestVisibilityLevels_EveryLevelFromEveryPlace(t *testing.T) {
	type place struct {
		file, module string // the declaring file's qualifier and its module, "" for app
		fn, ty       string
	}
	places := []place{{"engine", "", "engine", "Engine"}, {"kit", "kit", "kit", "Kit"}}
	levels := []struct {
		name, suffix string
	}{{"public", "public"}, {"internal", "internal"}, {"private", "private"}}
	for _, p := range places {
		for _, l := range levels {
			fn := p.fn + "_" + l.suffix
			ty := p.ty + strings.ToUpper(l.suffix[:1]) + l.suffix[1:]
			var code, want, wantTy string
			switch {
			case l.name == "private":
				code = analysis.PrivateDeclarationCode
				want = fmt.Sprintf("`%s` is private to `%s.nomi`", fn, p.file)
				wantTy = fmt.Sprintf("`%s` is private to `%s.nomi`", ty, p.file)
			case l.name == "internal" && p.module != "":
				code = analysis.InternalDeclarationCode
				want = fmt.Sprintf("`%s` is internal to package `%s`", fn, p.module)
				wantTy = fmt.Sprintf("`%s` is internal to package `%s`", ty, p.module)
			}
			uses := map[string]struct{ src, want string }{
				"qualified call": {fmt.Sprintf("import %s\n\nfn main()\n    _ = %s.%s()\nend\n", p.file, p.file, fn), want},
				"qualified type": {fmt.Sprintf("import %s\n\nfn size(x: %s.%s) -> Int\n    x.n\nend\n\nfn main()\n    _ = size\nend\n", p.file, p.file, ty), wantTy},
				"import":         {fmt.Sprintf("import %s.{%s}\n\nfn main()\n    _ = %s()\nend\n", p.file, fn, fn), want},
				"imported type":  {fmt.Sprintf("import %s.{%s}\n\nfn size(x: %s) -> Int\n    x.n\nend\n\nfn main()\n    _ = size\nend\n", p.file, ty, ty), wantTy},
			}
			for use, u := range uses {
				t.Run(l.name+" in "+p.file+" by "+use, func(t *testing.T) {
					errs := buildModules(t, levelProgram(u.src))
					if code == "" {
						if len(errs) != 0 {
							t.Fatalf("a %s declaration of %s is refused:\n%s", l.name, p.file, levelErrors(errs))
						}
						return
					}
					for _, e := range errs {
						if e.Message == u.want && e.Code == code {
							return
						}
					}
					t.Fatalf("want %q (%s), got:\n%s", u.want, code, levelErrors(errs))
				})
			}
		}
	}
}

// A file names its own declarations at every level.
func TestVisibilityLevels_SameFileSeesEveryLevel(t *testing.T) {
	main := levelDecls("own", "Own") + "\nfn main()\n    _ = own_public() + own_internal() + own_private() + own_uses()\nend\n"
	if errs := buildModules(t, levelProgram(main)); len(errs) != 0 {
		t.Fatalf("a file is refused its own declarations:\n%s", levelErrors(errs))
	}
}

// `pub import` re-exports public items only: an internal item, which a
// plain import of the same module takes, is refused as a re-export, so a
// facade cannot widen it.
func TestVisibilityLevels_ReexportOfAnInternalItemIsRefused(t *testing.T) {
	errs := buildModules(t, levelProgram("pub import engine.{engine_internal}\n\nfn main()\n    _ = engine_internal()\nend\n"))
	want := "`engine_internal` is internal, and `pub import` re-exports public items only"
	for _, e := range errs {
		if e.Message == want && e.Code == analysis.ReexportNotPublicCode {
			return
		}
	}
	t.Fatalf("want %q, got:\n%s", want, levelErrors(errs))
}

// The same plain import compiles, and a re-export of a public item does.
func TestVisibilityLevels_PlainImportAndPublicReexportCompile(t *testing.T) {
	for _, main := range []string{
		"import engine.{engine_internal}\n\nfn main()\n    _ = engine_internal()\nend\n",
		"pub import engine.{engine_public}\n\nfn main()\n    _ = engine_public()\nend\n",
	} {
		if errs := buildModules(t, levelProgram(main)); len(errs) != 0 {
			t.Fatalf("%s is refused:\n%s", main, levelErrors(errs))
		}
	}
}

// A type's functions follow the same rule as a file's: an internal one is
// called from another file of its module, and a private one is the
// private error, with its owner in the name.
func TestVisibilityLevels_InherentFunctions(t *testing.T) {
	gear := "pub struct Gear\n    teeth: Int\nend\n\nimpl Gear\n    fn spin(g: Gear) -> Int\n        g.teeth\n    end\n\n" +
		"    private fn hidden(g: Gear) -> Int\n        g.teeth\n    end\nend\n\npub fn made() -> Int\n    Gear.hidden(Gear{teeth: 1})\nend\n"
	program := func(main string) map[string]string {
		files := levelProgram(main)
		files["app/gear.nomi"] = gear
		return files
	}
	if errs := buildModules(t, program("import gear.{Gear}\n\nfn main()\n    _ = Gear.spin(Gear{teeth: 2})\nend\n")); len(errs) != 0 {
		t.Fatalf("an internal inherent function is refused to its module:\n%s", levelErrors(errs))
	}
	errs := buildModules(t, program("import gear.{Gear}\n\nfn main()\n    _ = Gear.hidden(Gear{teeth: 2})\nend\n"))
	want := "`Gear.hidden` is private to `gear.nomi`"
	for _, e := range errs {
		if e.Message == want && e.Code == analysis.PrivateDeclarationCode {
			return
		}
	}
	t.Fatalf("want %q, got:\n%s", want, levelErrors(errs))
}

func levelErrors(errs []analysis.TypeError) string {
	var lines []string
	for _, e := range errs {
		lines = append(lines, "  "+e.Error())
	}
	return strings.Join(lines, "\n")
}

// Visibility consistency gains the middle level (spec §14 *Visibility
// consistency*): an internal declaration may name no private type, a public
// one no internal type, whether its own file declares the type or it comes
// through an import or a file's qualifier, and a private declaration names
// any type. Each error has its Code and fails if the program compiles.
func TestVisibilityLevels_ConsistencyAtEachLevel(t *testing.T) {
	const own = "private struct Hidden\n    n: Int\nend\n\nstruct Inner\n    n: Int\nend\n\n"
	for _, tc := range []struct{ name, main, want string }{
		{"an internal fn returning a private type", own + "fn leak() -> Hidden\n    Hidden{n: 1}\nend\n",
			"function return exposes private type Hidden"},
		{"an internal struct's private field", own + "struct Box\n    h: Hidden\nend\n",
			"internal struct field exposes private type Hidden"},
		{"an internal once's inferred private type", own + "once kept = Hidden{n: 1}\n",
			"internal once binding exposes private type Hidden"},
		{"a pub fn taking its own internal type", own + "pub fn take(i: Inner) -> Int\n    i.n\nend\n",
			"function parameter exposes internal type Inner"},
		{"a pub fn returning an imported internal type", "import engine.{EngineInternal}\n\npub fn make() -> EngineInternal\n    EngineInternal{n: 1}\nend\n",
			"function return exposes internal type EngineInternal"},
		{"a pub fn returning a qualified internal type", "import engine\n\npub fn make() -> engine.EngineInternal\n    engine.EngineInternal{n: 1}\nend\n",
			"function return exposes internal type EngineInternal"},
		{"a pub once of an imported internal type", "import engine\n\npub once made = engine.EngineInternal{n: 1}\n",
			"public once binding exposes internal type EngineInternal"},
		{"a pub inherent function returning an internal type", own + "pub struct Open\n    n: Int\nend\n\nimpl Open\n    pub fn inner(o: Open) -> Inner\n        Inner{n: o.n}\n    end\nend\n",
			"function return exposes internal type Inner"},
		{"an internal inherent function taking a private type", own + "impl Inner\n    fn of(h: Hidden) -> Inner\n        Inner{n: h.n}\n    end\nend\n",
			"function parameter exposes private type Hidden"},
		{"a pub inherent once of an internal type", own + "pub struct Open\n    n: Int\nend\n\nimpl Open\n    pub once zero: Inner = Inner{n: 0}\nend\n",
			"public once binding exposes internal type Inner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := buildModules(t, levelProgram(tc.main+"\nfn main()\nend\n"))
			for _, e := range errs {
				if e.Message == tc.want && e.Code == analysis.ExposesTypeCode {
					return
				}
			}
			t.Fatalf("want %q, got:\n%s", tc.want, levelErrors(errs))
		})
	}
}

// What each level may name compiles: a private declaration names a private
// type, an internal one an internal type of its own file or another file
// of its module, and a public one a public type.
func TestVisibilityLevels_ConsistentSignaturesCompile(t *testing.T) {
	main := "import engine\nimport engine.{EngineInternal, EnginePublic}\n\nprivate struct Hidden\n    n: Int\nend\n\nstruct Inner\n    n: Int\nend\n\n" +
		"private fn hidden(h: Hidden) -> Hidden\n    h\nend\n\nfn inner(i: Inner, e: EngineInternal) -> engine.EngineInternal\n    _ = i\n    e\nend\n\n" +
		"pub fn open(p: EnginePublic) -> EnginePublic\n    p\nend\n\n" +
		"fn main()\n    _ = hidden(Hidden{n: 1})\n    _ = inner(Inner{n: 1}, EngineInternal{n: 1})\n    _ = open(EnginePublic{n: 1})\nend\n"
	if errs := buildModules(t, levelProgram(main)); len(errs) != 0 {
		t.Fatalf("a consistent signature is refused:\n%s", levelErrors(errs))
	}
}
