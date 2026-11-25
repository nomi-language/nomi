package analysis

import (
	"slices"
	"strings"
	"testing"
)

// writeTestModule is the [package] table the tables are written after.
const writeTestModule = "[package]\nname = \"app\"\nentry_points = [\"main\"]\n"

// WriteEffectTables keeps each comment that belongs to a key it
// keeps, and drops one that belongs to a key it removes: a comment line
// directly above a key, one on its line, and in a list written one name
// per line, one above or on a name's line. A comment no key owns stays,
// and so does a run of blank lines, unless removing a key leaves two runs
// together. A key the table lacks is added after the keys it has, with no
// comment. A table the file lacks goes after the one before it, or before
// the first the file has.
func TestWriteEffectTables_RulesForComments(t *testing.T) {
	app := t.TempDir()
	tight := func() *Effects {
		return &Effects{
			Self: &SelfEffects{Std: []string{"std::fs", "std::io"}, StdInTests: []string{"std::instant"}},
			Deps: &DepsEffects{Std: []string{"std::fs"}, Packages: map[string][]string{"pricing": {"std::fs"}, "pure": {}}},
		}
	}
	withoutPure := tight()
	delete(withoutPure.Deps.Packages, "pure")
	withKit := tight()
	withKit.Deps.Packages = map[string][]string{"kit": {}, "pricing": {"std::fs"}}
	withAll := tight()
	withAll.Deps.Packages["kit"] = []string{}
	withProcess := tight()
	withProcess.Self.Std = []string{"std::fs", "std::io", "std::process"}
	// self, deps and mods are the three tables as tight writes them.
	self := "[effects.self]\nstd = [\"std::fs\", \"std::io\"]\nstd_in_tests = [\"std::instant\"]\n"
	deps := "\n[effects.deps]\nstd = [\"std::fs\"]\n"
	mods := "\n[effects.deps.packages]\n\"pricing\" = [\"std::fs\"]\n\"pure\" = []\n"
	cases := []struct {
		name, before, after string
		c                   *Effects
	}{{
		name: "a list one name per line",
		before: "[effects.self]\nstd = [\n    # through pricing\n    \"std::fs\",\n    \"std::io\",  # printing\n\n    # to remove\n    \"std::net\", # gone\n]\n" +
			"std_in_tests = [\"std::instant\"]  # the clock\n" + deps + mods,
		after: "[effects.self]\nstd = [\n    # through pricing\n    \"std::fs\",\n    \"std::io\",  # printing\n    \"std::process\",\n]\n" +
			"std_in_tests = [\"std::instant\"]  # the clock\n" + deps + mods,
		c: withProcess,
	}, {
		name: "a list that no longer holds a name, and a list that goes",
		before: "[effects.self]\nstd = [\"std::fs\", \"std::io\", \"std::net\"]\n# the clock\nstd_in_tests = [\"std::instant\"]\n" +
			"\n[effects.deps]\nentries_required = false\nstd = [\"std::fs\"]\n# theirs\nstd_in_tests = [\"std::net\"]\n" + mods,
		after: "[effects.self]\nstd = [\"std::fs\", \"std::io\"]\n# the clock\nstd_in_tests = [\"std::instant\"]\n" +
			"\n[effects.deps]\nentries_required = false\nstd = [\"std::fs\"]\n" + mods,
		c: tight(),
	}, {
		name:   "a comment on a key's line",
		before: "[effects.self]\nstd = [\"std::io\"]  # what it does\nstd_in_tests = [\"std::instant\", \"std::net\"] # tests\n" + deps + mods,
		after:  "[effects.self]\nstd = [\"std::io\", \"std::fs\"]  # what it does\nstd_in_tests = [\"std::instant\"] # tests\n" + deps + mods,
		c:      tight(),
	}, {
		name: "a comment no key owns",
		before: self + deps + "\n[effects.deps.packages]\n# Dependencies, by path.\n\n" +
			"# computes\n\"pricing\" = []\n# reaches nothing\n\"pure\" = []\n",
		after: self + deps + "\n[effects.deps.packages]\n# Dependencies, by path.\n\n" +
			"# computes\n\"pricing\" = [\"std::fs\"]\n",
		c: withoutPure,
	}, {
		name:   "blank lines a removed key leaves together",
		before: self + deps + "\n[effects.deps.packages]\n\"kit\" = []\n\n\"pure\" = []\n\n\"pricing\" = [\"std::fs\"]\n",
		after:  self + deps + "\n[effects.deps.packages]\n\"kit\" = []\n\n\"pricing\" = [\"std::fs\"]\n",
		c:      withKit,
	}, {
		name:   "a key added",
		before: self + deps + "\n[effects.deps.packages]\n# a kit\n\"kit\" = []\n\n# computes\n\"pricing\" = [\"std::fs\"]\n",
		after:  self + deps + "\n[effects.deps.packages]\n# a kit\n\"kit\" = []\n\n# computes\n\"pricing\" = [\"std::fs\"]\n\n\"pure\" = []\n",
		c:      withAll,
	}, {
		name:   "a list grown past the width",
		before: "[effects.self]\nstd = [\"std::fs\", \"std::io\"] # all of it\n",
		after: "[effects.self]\nstd = [ # all of it\n    \"std::fs\",\n    \"std::io\",\n    \"std::http\",\n    \"std::instant\",\n" +
			"    \"std::process\",\n    \"std::random\",\n]\n",
		c: &Effects{Self: &SelfEffects{Std: []string{"std::fs", "std::http", "std::instant", "std::io", "std::process", "std::random"}}},
	}, {
		name:   "a table added before the first the file has",
		before: "[effects.deps]\n# the ceiling\nstd = [\"std::fs\"]\n",
		after:  self + "\n[effects.deps]\n# the ceiling\nstd = [\"std::fs\"]\n" + mods,
		c:      tight(),
	}, {
		name:   "a table the tables written lack",
		before: self + deps + mods,
		after:  self,
		c:      &Effects{Self: tight().Self},
	}, {
		name:   "CRLF line endings",
		before: "# app\r\n[package]\r\nname = \"app\"\r\n\r\n[effects.self]\r\n# all\r\nstd = [\"std::io\"]\r\n\r\n[effects.deps.packages]\r\n\"pure\" = [] # none\r\n",
		after: "# app\r\n[package]\r\nname = \"app\"\r\n\r\n[effects.self]\r\n# all\r\nstd = [\"std::io\", \"std::fs\"]\r\nstd_in_tests = [\"std::instant\"]\r\n\r\n" +
			"[effects.deps]\r\nstd = [\"std::fs\"]\r\n\r\n" +
			"[effects.deps.packages]\r\n\"pure\" = [] # none\r\n\"pricing\" = [\"std::fs\"]\r\n",
		c: tight(),
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := c.before
			if !strings.HasPrefix(before, "# app") {
				before = writeTestModule + "\n" + before
			}
			got, _, err := WriteEffectTables(app, []byte(before), c.c)
			if err != nil {
				t.Fatal(err)
			}
			want := c.after
			if !strings.HasPrefix(want, "# app") {
				want = writeTestModule + "\n" + want
			}
			if string(got) != want {
				t.Errorf("written:\n%q\nwant:\n%q", got, want)
			}
		})
	}
}

// [effects.self.modules] is written by the rules of
// [effects.deps.packages]: after [effects.self] and before
// [effects.deps], keeping its comments and entries_required, an entry
// that goes taking its comment, and a new one going last. It is written
// when the tables have it, empty or not, and when the file has it, and
// left out otherwise. AllowEffects adds a name to an entry, giving the
// file the table when it has none.
func TestWriteEffectTables_SelfModules(t *testing.T) {
	app := t.TempDir()
	tight := func(entries map[string][]string) *Effects {
		return &Effects{
			Self: &SelfEffects{Std: []string{"std::fs", "std::io"}, Modules: entries},
			Deps: &DepsEffects{Std: []string{}},
		}
	}
	self := "[effects.self]\nstd = [\"std::fs\", \"std::io\"]\n"
	deps := "\n[effects.deps]\nstd = []\n"
	cases := []struct {
		name, before, after string
		c                   *Effects
	}{{
		name:   "a table the file lacks",
		before: self,
		after:  self + "\n[effects.self.modules]\n\"main\" = [\"std::io\"]\n\"pricing\" = []\n" + deps,
		c:      tight(map[string][]string{"pricing": {}, "main": {"std::io"}}),
	}, {
		name: "comments and entries_required kept, an entry gone and one added",
		before: "[effects.self]\n# every module\nentries_required = true\nstd = [\"std::fs\", \"std::io\"]\n" +
			"\n[effects.self.modules]\n# prints\n\"main\" = [\"std::io\"] # only\n# gone\n\"old\" = []\n" + deps,
		after: "[effects.self]\n# every module\nentries_required = true\nstd = [\"std::fs\", \"std::io\"]\n" +
			"\n[effects.self.modules]\n# prints\n\"main\" = [\"std::io\"] # only\n\"storage\" = [\"std::fs\"]\n" + deps,
		c: tight(map[string][]string{"main": {"std::io"}, "storage": {"std::fs"}}),
	}, {
		name:   "an empty table",
		before: self + deps,
		after:  self + "\n[effects.self.modules]\n" + deps,
		c:      tight(map[string][]string{}),
	}, {
		name:   "no table, none written",
		before: self + deps,
		after:  self + deps,
		c:      tight(nil),
	}, {
		name:   "the file's table, no entries left",
		before: self + "\n[effects.self.modules]\n# none yet\n" + deps,
		after:  self + "\n[effects.self.modules]\n# none yet\n" + deps,
		c:      tight(nil),
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := WriteEffectTables(app, []byte(writeTestModule+"\n"+c.before), c.c)
			if err != nil {
				t.Fatal(err)
			}
			if want := writeTestModule + "\n" + c.after; string(got) != want {
				t.Errorf("written:\n%q\nwant:\n%q", got, want)
			}
		})
	}
	before := writeTestModule + "\n" + self
	was, err := ParseManifestData([]byte(before), "nomi.toml")
	if err != nil {
		t.Fatal(err)
	}
	fix, err := AllowEffects(app+"/nomi.toml", []byte(before), was.Effects, "Allow std::fs for storage in nomi.toml",
		[]EffectAdd{{List: ListSelfModules, Module: "storage", Name: "std::fs"}})
	if err != nil || fix == nil || len(fix.Edits) != 1 || fix.Edits[0].Text != "\n[effects.self.modules]\n\"storage\" = [\"std::fs\"]\n" {
		t.Errorf("the fix that adds to an entry: %+v, %v", fix, err)
	}
}

// EffectChanges names a table one side has and the other lacks, and a
// ceiling of no names one side has, besides the names each list gains and
// loses.
func TestEffectChanges_TablesAndCeilings(t *testing.T) {
	to := &Effects{
		Self: &SelfEffects{Std: []string{"std::io"}},
		Deps: &DepsEffects{Std: []string{}, Packages: map[string][]string{"pure": {}}},
	}
	got := EffectChanges(&Effects{Deps: &DepsEffects{Packages: map[string][]string{}}}, to)
	want := []EffectChange{
		{Change: "add", List: ListSelf},
		{Change: "add", List: ListSelfStd, Name: "std::io"},
		{Change: "add", List: ListDepsStd},
		{Change: "add", List: ListDepsPackages, Key: "pure"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes %+v\nwant %+v", got, want)
	}
	if got := EffectChanges(to, to.Clone()); len(got) != 0 {
		t.Errorf("a table and its clone differ by %+v", got)
	}
	// An entry under [effects.self.modules] changes as one under
	// [effects.deps.packages] does, after self's lists and before deps'.
	from := &Effects{Self: &SelfEffects{Modules: map[string][]string{"main": {"std::io", "std::net"}, "old": {}}}}
	into := &Effects{Self: &SelfEffects{Modules: map[string][]string{"main": {"std::io"}, "pricing": {}}}, Deps: &DepsEffects{}}
	got = EffectChanges(from, into)
	want = []EffectChange{
		{Change: "add", List: ListSelfModules, Key: "pricing"},
		{Change: "add", List: ListDeps},
		{Change: "remove", List: ListSelfModules, Key: "main", Name: "std::net"},
		{Change: "remove", List: ListSelfModules, Key: "old"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes %+v\nwant %+v", got, want)
	}
	if label := want[0].Label(); label != `self.modules "pricing"` {
		t.Errorf("label %q", label)
	}
	if got := EffectChanges(from, from.Clone()); len(got) != 0 {
		t.Errorf("self entries and their clone differ by %+v", got)
	}
}
