package vmhost

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// selfModulesFiles is a project of four modules: main prints what pricing
// totals, pricing calls a helper in net::fetch, which makes an HTTP
// request, and pricing_test, a test file, reads a file. tables is added to
// its nomi.toml after [effects.self] with std.
func selfModulesFiles(std, tables string) map[string]string {
	return map[string]string{
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = " + std + "\n" + tables,
		"main.nomi": `import
    std::io
    pricing
end

fn main()
    io.print(pricing.total())
end
`,
		"pricing.nomi": `import net::fetch

pub fn total() -> String
    fetch.rates()
end
`,
		"net/fetch.nomi": `import
    std::http
    std::url.Url
end

pub fn rates() -> String
    case http.get(Url"https://rates.example.com/today")
        Ok(_) -> "ok"
        Err(_) -> "offline"
    end
end
`,
		"pricing_test.nomi": `import
    std::fs
    pricing
end

test "totals"
    _ = fs.read_text("rates.txt")
    assert pricing.total() != ""
end
`,
	}
}

// selfModulesDiagnostics is effectDiagnostics over selfModulesFiles,
// from main and from the test file.
func selfModulesDiagnostics(t *testing.T, entry, std, tables string) (string, Diagnostics) {
	t.Helper()
	return effectDiagnostics(t, entry, selfModulesFiles(std, tables))
}

// A call is charged to the module whose file holds it: pricing, whose
// entry allows nothing, calls a helper in net::fetch, and the HTTP request
// is net::fetch's. With net::fetch's entry allowing nothing, the one
// refusal is at the call in net/fetch.nomi, naming `net::fetch` and its
// entry, coded with the list self.modules and the entry's key; with it
// allowing std::http, pricing's empty entry passes.
func TestEffectDiagnostic_AModuleOverItsEntry(t *testing.T) {
	both := `["std::io", "std::http"]`
	got, ds := selfModulesDiagnostics(t, "main.nomi", both, "\n[effects.self.modules]\n\"pricing\" = []\n\"net::fetch\" = []\n")
	want := "net/fetch.nomi:7:10: `net::fetch` has the effect std::http, and its entry under [effects.self.modules] allows it no effects\n" +
		"net/fetch.nomi:7:10: help: reached from `main`; `main` calls `total`; `total` calls `rates`\n" +
		"net/fetch.nomi:7:10: help: add \"std::http\" to \"net::fetch\" under [effects.self.modules] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Code != EffectNotAllowedCode || !slices.Equal(ds[0].Args, []string{"std::http", "self.modules", "net::fetch"}) {
		t.Errorf("code %q args %q; want %s with the name, self.modules and the entry's key", ds[0].Code, ds[0].Args, EffectNotAllowedCode)
	}
	if fix := ds[0].Fix; fix == nil || fix.Title != "Allow std::http for net::fetch in nomi.toml" || len(fix.Edits) != 1 ||
		fix.Edits[0].Text != "\"net::fetch\" = [\"std::http\"]\n" {
		t.Errorf("fix %+v; want the edit that adds std::http to net::fetch's entry", fix)
	}

	dir := writeProject(t, selfModulesFiles(both, "\n[effects.self.modules]\n\"pricing\" = []\n\"net::fetch\" = [\"std::http\"]\n"))
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	if err := p.effectError(p.checkedRoots()...); err != nil {
		t.Errorf("pricing's empty entry refuses net::fetch's call: %v", err)
	}
}

// A call over its module's entry and over [effects.self]'s std is
// reported once, over the entry, and the help says std does not allow it
// either and names both lists; the fix adds to both.
func TestEffectDiagnostic_AModuleOverItsEntryAndSelf(t *testing.T) {
	got, ds := selfModulesDiagnostics(t, "main.nomi", `["std::io"]`, "\n[effects.self.modules]\n\"net::fetch\" = []\n")
	want := "net/fetch.nomi:7:10: `net::fetch` has the effect std::http, and its entry under [effects.self.modules] allows it no effects\n" +
		"net/fetch.nomi:7:10: help: reached from `main`; `main` calls `total`; `total` calls `rates`\n" +
		"net/fetch.nomi:7:10: help: std under [effects.self] does not allow std::http either\n" +
		"net/fetch.nomi:7:10: help: add \"std::http\" to \"net::fetch\" under [effects.self.modules] and to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if fix := ds[0].Fix; fix == nil || len(fix.Edits) != 1 || !strings.Contains(fix.Edits[0].Text, `std = ["std::io", "std::http"]`) ||
		!strings.Contains(fix.Edits[0].Text, `"net::fetch" = ["std::http"]`) {
		t.Errorf("fix %+v; want the edit that adds std::http to the entry and to std", fix)
	}
}

// A test file is a module of its own, with its own entry, which holds its
// calls from the tests too: std_in_tests allows the test's read under
// [effects.self], and pricing_test's empty entry refuses it.
func TestEffectDiagnostic_ATestFilesEntry(t *testing.T) {
	got, ds := selfModulesDiagnostics(t, "pricing_test.nomi", `["std::io", "std::http"]`,
		"std_in_tests = [\"std::fs\"]\n\n[effects.self.modules]\n\"pricing_test\" = []\n")
	want := "pricing_test.nomi:7:9: `pricing_test` has the effect std::fs, and its entry under [effects.self.modules] allows it no effects\n" +
		"pricing_test.nomi:7:9: help: reached from test \"totals\"\n" +
		"pricing_test.nomi:7:9: help: add \"std::fs\" to \"pricing_test\" under [effects.self.modules] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || !slices.Equal(ds[0].Args, []string{"std::fs", "self.modules", "pricing_test"}) {
		t.Errorf("args %q, want the name, self.modules and pricing_test", ds[0].Args)
	}
}

// selfListProblems loads entry from a project of files and answers its
// lists' problems, with the project's directory left out of each path,
// and the title of each problem's fix.
func selfListProblems(t *testing.T, entry string, files map[string]string) (string, []string) {
	t.Helper()
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, filepath.FromSlash(entry)))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	ds := p.effectListProblems()
	if len(ds) == 0 {
		return "", nil
	}
	var fixes []string
	for _, d := range ds {
		if d.Fix != nil {
			fixes = append(fixes, d.Fix.Title)
		}
	}
	return inProject(ds.Error(), dir), fixes
}

// A key under [effects.self.modules] that names no module of the
// project is an error at the key, naming the nearest module. A module of
// the project the program does not load is a key, and so is one in a
// directory; a file in another module's directory is not.
func TestEffectLists_AnUnknownSelfModule(t *testing.T) {
	files := selfModulesFiles(`["std::io", "std::http"]`, "\n[effects.self.modules]\n\"pricng\" = []\n\"net::fetch\" = [\"std::http\"]\n\"unused\" = []\n\"plugin::calc\" = []\n")
	files["unused.nomi"] = "pub fn nothing() -> Int\n    1\nend\n"
	files["plugin/nomi.toml"] = "[module]\nname = \"plugin\"\n"
	files["plugin/calc.nomi"] = "pub fn one() -> Int\n    1\nend\n"
	got, _ := selfListProblems(t, "main.nomi", files)
	want := "nomi.toml:12: [effects.self.modules] names plugin::calc, which is no module of this project\n" +
		"nomi.toml:12: help: a key is a module of this project as its own code imports it: main, net::fetch, pricing, pricing_test, unused\n" +
		"nomi.toml:9: [effects.self.modules] names pricng, which is no module of this project\n" +
		"nomi.toml:9: help: did you mean \"pricing\"?"
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
	files = selfModulesFiles(`["std::io"]`, "\n[effects.self.modules]\n\"pricing\" = [\"std::fss\"]\n")
	got, _ = selfListProblems(t, "main.nomi", files)
	want = "nomi.toml:9: [effects.self.modules] \"pricing\" names std::fss, which is not a std module\n" +
		"nomi.toml:9: help: did you mean \"std::fs\"?"
	if got != want {
		t.Errorf("problems with a misspelled name:\n%s\nwant:\n%s", got, want)
	}
}

// entries_required under [effects.self] makes each module of the
// project whose files the program loads have an entry: a module with none
// is an error at the key, one per module, whose fix writes the entry with
// what the module reaches. The key false or absent requires nothing.
func TestEffectLists_SelfEntriesRequired(t *testing.T) {
	tables := "entries_required = true\n\n[effects.self.modules]\n\"main\" = [\"std::io\"]\n"
	got, fixes := selfListProblems(t, "main.nomi", selfModulesFiles(`["std::io", "std::http"]`, tables))
	want := "nomi.toml:7: [effects.self] entries_required: net::fetch has no entry\n" +
		"nomi.toml:7: help: add \"net::fetch\" under [effects.self.modules] in nomi.toml\n" +
		"nomi.toml:7: [effects.self] entries_required: pricing has no entry\n" +
		"nomi.toml:7: help: add \"pricing\" under [effects.self.modules] in nomi.toml"
	if got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
	wantFixes := []string{`Write "net::fetch" = ["std::http"] under [effects.self.modules]`, `Write "pricing" = [] under [effects.self.modules]`}
	if !slices.Equal(fixes, wantFixes) {
		t.Errorf("fixes %q, want %q", fixes, wantFixes)
	}
	for _, c := range []struct{ name, tables string }{
		{"every entry", tables + "\"pricing\" = []\n\"net::fetch\" = [\"std::http\"]\n"},
		{"the key false", strings.Replace(tables, "true", "false", 1)},
		{"the key absent", strings.Replace(tables, "entries_required = true\n", "", 1)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := selfListProblems(t, "main.nomi", selfModulesFiles(`["std::io", "std::http"]`, c.tables)); got != "" {
				t.Errorf("problems:\n%s\nwant none", got)
			}
		})
	}
}

// A dependency whose own nomi.toml declares [effects.self.modules] is
// held to its entries when the project has an [effects.deps]: a call
// over the entry for its file's module is reported coded
// effect-not-declared with the list self.modules, naming the module and
// the entry, and the help says the fix is in that module. A name in the
// dependency's own entry that is no std module is an error at its line in
// the dependency's nomi.toml.
func TestEffectDiagnostic_ADependencyOverItsOwnModuleEntry(t *testing.T) {
	own := "\n[effects.self]\nstd = [\"std::fs\"]\n\n[effects.self.modules]\n\"rules\" = []\n"
	deps := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.deps]\nstd = [\"std::io\", \"std::fs\"]\n"
	got, ds := effectDiagnostics(t, "app/main.nomi", dependencyFiles(deps, own))
	want := "pricing/rules.nomi:4:5: pricing's `rules` has the effect std::fs, and its entry under the module's own [effects.self.modules] allows it no effects\n" +
		"pricing/rules.nomi:4:5: help: reached from `main`; `main` calls `load`\n" +
		"pricing/rules.nomi:4:5: help: pricing declares fewer effects than its code has, and the fix is in that module: add \"std::fs\" to \"rules\" under [effects.self.modules] in pricing/nomi.toml, or remove the call" +
		"\napp/main.nomi:7:14: note: your code has the effect std::fs in pricing here: `main` calls `load`"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Code != EffectNotDeclaredCode || !slices.Equal(ds[0].Args, []string{"std::fs", "self.modules", "pricing"}) {
		t.Errorf("code %q args %q; want %s with the name, self.modules and the module", ds[0].Code, ds[0].Args, EffectNotDeclaredCode)
	}

	dir := writeProject(t, dependencyFiles(deps, "\n[effects.self]\nstd = [\"std::fs\"]\n\n[effects.self.modules]\n\"rules\" = [\"std::fss\"]\n"))
	p, err := Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	err = p.effectError(p.checkedRoots()...)
	var list Diagnostics
	if !errors.As(err, &list) {
		t.Fatalf("the dependency's misspelled entry is not a list problem: %v", err)
	}
	want = "pricing/nomi.toml:8: [effects.self.modules] \"rules\" names std::fss, which is not a std module\n" +
		`pricing/nomi.toml:8: help: did you mean "std::fs"?`
	if got := inProject(list.Error(), dir); got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
}
