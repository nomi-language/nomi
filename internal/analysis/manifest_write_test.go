package analysis

import (
	"slices"
	"strings"
	"testing"
)

// writeTestModule is the [module] table the tables are written after.
const writeTestModule = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"

// WriteCapabilityTables keeps each comment that belongs to a key it
// keeps, and drops one that belongs to a key it removes: a comment line
// directly above a key, one on its line, and in a list written one name
// per line, one above or on a name's line. A comment no key owns stays,
// and so does a run of blank lines, unless removing a key leaves two runs
// together. A key the table lacks is added after the keys it has, with no
// comment. A flag keeps its line while its value stays, has its value
// written again when it turns true, and goes when it turns false. A table
// the file lacks goes after the one before it, or before the first the
// file has.
func TestWriteCapabilityTables_RulesForComments(t *testing.T) {
	app := t.TempDir()
	tight := func() *Capabilities {
		return &Capabilities{
			Self: &SelfCapabilities{Std: []string{"std::fs", "std::io"}, StdInTests: []string{"std::instant"}},
			Deps: &DepsCapabilities{Std: []string{"std::fs"}, Modules: map[string][]string{"example.com/pricing": {"std::fs"}, "example.com/pure": {}}},
		}
	}
	withoutPure := tight()
	delete(withoutPure.Deps.Modules, "example.com/pure")
	withKit := tight()
	withKit.Deps.Modules = map[string][]string{"example.com/kit": {}, "example.com/pricing": {"std::fs"}}
	withAll := tight()
	withAll.Deps.Modules["example.com/kit"] = []string{}
	withProcess := tight()
	withProcess.Self.Std = []string{"std::fs", "std::io", "std::process"}
	// self, deps and mods are the three tables as tight writes them.
	self := "[capabilities.self]\nstd = [\"std::fs\", \"std::io\"]\nstd_in_tests = [\"std::instant\"]\n"
	deps := "\n[capabilities.deps]\nstd = [\"std::fs\"]\n"
	mods := "\n[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n\"example.com/pure\" = []\n"
	cases := []struct {
		name, before, after string
		c                   *Capabilities
	}{{
		name: "a list one name per line",
		before: "[capabilities.self]\nstd = [\n    # through pricing\n    \"std::fs\",\n    \"std::io\",  # printing\n\n    # to remove\n    \"std::net\", # gone\n]\n" +
			"std_in_tests = [\"std::instant\"]  # the clock\n" + deps + mods,
		after: "[capabilities.self]\nstd = [\n    # through pricing\n    \"std::fs\",\n    \"std::io\",  # printing\n    \"std::process\",\n]\n" +
			"std_in_tests = [\"std::instant\"]  # the clock\n" + deps + mods,
		c: withProcess,
	}, {
		name: "a list that no longer holds a name, and a flag turned false",
		before: "[capabilities.self]\nstd = [\"std::fs\", \"std::io\"]\n# the clock\nstd_in_tests = [\"std::instant\"]\n\n# ours\ngo = true\n" +
			"\n[capabilities.deps]\nentries_required = false\nstd = [\"std::fs\"]\n# theirs\ngo = [\"example.com/pg\"]\n" + mods,
		after: "[capabilities.self]\nstd = [\"std::fs\", \"std::io\"]\n# the clock\nstd_in_tests = [\"std::instant\"]\n" +
			"\n[capabilities.deps]\nentries_required = false\nstd = [\"std::fs\"]\n" + mods,
		c: tight(),
	}, {
		name:   "a comment on a key's line",
		before: "[capabilities.self]\nstd = [\"std::io\"]  # what it does\nstd_in_tests = [\"std::instant\", \"std::net\"] # tests\n" + deps + mods,
		after:  "[capabilities.self]\nstd = [\"std::io\", \"std::fs\"]  # what it does\nstd_in_tests = [\"std::instant\"] # tests\n" + deps + mods,
		c:      tight(),
	}, {
		name: "a comment no key owns",
		before: self + deps + "\n[capabilities.deps.modules]\n# Dependencies, by path.\n\n" +
			"# computes\n\"example.com/pricing\" = []\n# reaches nothing\n\"example.com/pure\" = []\n",
		after: self + deps + "\n[capabilities.deps.modules]\n# Dependencies, by path.\n\n" +
			"# computes\n\"example.com/pricing\" = [\"std::fs\"]\n",
		c: withoutPure,
	}, {
		name:   "blank lines a removed key leaves together",
		before: self + deps + "\n[capabilities.deps.modules]\n\"example.com/kit\" = []\n\n\"example.com/pure\" = []\n\n\"example.com/pricing\" = [\"std::fs\"]\n",
		after:  self + deps + "\n[capabilities.deps.modules]\n\"example.com/kit\" = []\n\n\"example.com/pricing\" = [\"std::fs\"]\n",
		c:      withKit,
	}, {
		name:   "a key added",
		before: self + deps + "\n[capabilities.deps.modules]\n# a kit\n\"example.com/kit\" = []\n\n# computes\n\"example.com/pricing\" = [\"std::fs\"]\n",
		after:  self + deps + "\n[capabilities.deps.modules]\n# a kit\n\"example.com/kit\" = []\n\n# computes\n\"example.com/pricing\" = [\"std::fs\"]\n\n\"example.com/pure\" = []\n",
		c:      withAll,
	}, {
		name:   "a list grown past the width",
		before: "[capabilities.self]\nstd = [\"std::fs\", \"std::io\"] # all of it\n",
		after: "[capabilities.self]\nstd = [ # all of it\n    \"std::fs\",\n    \"std::io\",\n    \"std::http\",\n    \"std::instant\",\n" +
			"    \"std::process\",\n    \"std::random\",\n]\n",
		c: &Capabilities{Self: &SelfCapabilities{Std: []string{"std::fs", "std::http", "std::instant", "std::io", "std::process", "std::random"}}},
	}, {
		name:   "flags",
		before: "[capabilities.self]\nstd = []\ngo = false # none of ours\ngo_in_tests = false\n",
		after:  "[capabilities.self]\nstd = []\ngo = false # none of ours\ngo_in_tests = true\n",
		c:      &Capabilities{Self: &SelfCapabilities{Std: []string{}, GoInTests: true}},
	}, {
		name:   "a table added before the first the file has",
		before: "[capabilities.deps]\n# the ceiling\nstd = [\"std::fs\"]\n",
		after:  self + "\n[capabilities.deps]\n# the ceiling\nstd = [\"std::fs\"]\n" + mods,
		c:      tight(),
	}, {
		name:   "a table the tables written lack",
		before: self + deps + mods,
		after:  self,
		c:      &Capabilities{Self: tight().Self},
	}, {
		name:   "CRLF line endings",
		before: "# app\r\n[module]\r\nname = \"app\"\r\n\r\n[capabilities.self]\r\n# all\r\nstd = [\"std::io\"]\r\n\r\n[capabilities.deps.modules]\r\n\"example.com/pure\" = [] # none\r\n",
		after: "# app\r\n[module]\r\nname = \"app\"\r\n\r\n[capabilities.self]\r\n# all\r\nstd = [\"std::io\", \"std::fs\"]\r\nstd_in_tests = [\"std::instant\"]\r\n\r\n" +
			"[capabilities.deps]\r\nstd = [\"std::fs\"]\r\n\r\n" +
			"[capabilities.deps.modules]\r\n\"example.com/pure\" = [] # none\r\n\"example.com/pricing\" = [\"std::fs\"]\r\n",
		c: tight(),
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := c.before
			if !strings.HasPrefix(before, "# app") {
				before = writeTestModule + "\n" + before
			}
			got, _, err := WriteCapabilityTables(app, []byte(before), c.c)
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

// CapabilityChanges names a table one side has and the other lacks, a
// ceiling of no names one side has, and a flag that turns true or false,
// besides the names each list gains and loses.
func TestCapabilityChanges_TablesFlagsAndCeilings(t *testing.T) {
	to := &Capabilities{
		Self: &SelfCapabilities{Std: []string{"std::io"}, Go: true},
		Deps: &DepsCapabilities{Std: []string{}, Modules: map[string][]string{"example.com/pure": {}}},
	}
	got := CapabilityChanges(&Capabilities{Deps: &DepsCapabilities{Modules: map[string][]string{}}}, to)
	want := []CapabilityChange{
		{Change: "add", List: ListSelf},
		{Change: "add", List: ListSelfStd, Name: "std::io"},
		{Change: "add", List: ListSelfGo, Name: "true"},
		{Change: "add", List: ListDepsStd},
		{Change: "add", List: ListDepsModules, Module: "example.com/pure"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes %+v\nwant %+v", got, want)
	}
	if got := CapabilityChanges(to, to.Clone()); len(got) != 0 {
		t.Errorf("a table and its clone differ by %+v", got)
	}
}
