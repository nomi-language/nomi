package analysis_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/std"
)

// The equivalence harness an incremental check is held to. A recheck
// (DocumentManager.RecheckWithOutcome, or anything with its signature) takes
// a checked program and an edit to one file and answers the program after
// the edit, consuming the program it was given. After every edit, and before
// the next one rewrites anything, the harness checks the edited texts again
// from scratch, with a manager that has checked nothing, and compares the two
// programs' dumps (analysis.DumpProgramForTest) byte for byte.

// recheckFunc is the seam's signature.
type recheckFunc func(prev *analysis.CheckedProgram, edit analysis.ProgramEdit) (*analysis.CheckedProgram, analysis.RecheckOutcome)

// incrementalProject is a multi-file project held in memory: its files' texts
// by module key, and the key of its entry.
type incrementalProject struct {
	name  string
	entry string
	texts map[string]string
}

// root is where the project's files would be. Nothing is read from there:
// every file's text is in texts.
func (p incrementalProject) root() string {
	return "/nomi-incremental/" + strings.ReplaceAll(p.name, "/", "_")
}

// newManager is a document manager over the process's stdlib that has
// checked nothing.
func newManager() *analysis.DocumentManager {
	lib := std.Shared()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	return dm
}

// fullCheck checks the project's texts from scratch.
func fullCheck(p incrementalProject, texts map[string]string) *analysis.CheckedProgram {
	return newManager().CheckProgram(p.root(), p.entry, texts)
}

// editStep is one edit of a sequence: what kind of edit it is, the file, and
// how its text changes.
type editStep struct {
	kind string
	key  string
	// change answers the file's new text from its current one and its
	// original one, or false when it does not apply to the text.
	change func(current, original string) (string, bool)
}

// replace replaces the first old in the file with new.
func replace(kind, key, old, new string) editStep {
	return editStep{kind, key, func(cur, _ string) (string, bool) {
		if !strings.Contains(cur, old) {
			return "", false
		}
		return strings.Replace(cur, old, new, 1), true
	}}
}

// appendText adds text at the end of the file.
func appendText(kind, key, text string) editStep {
	return editStep{kind, key, func(cur, _ string) (string, bool) { return cur + text, true }}
}

// restore puts the file's original text back.
func restore(kind, key string) editStep {
	return editStep{kind, key, func(_, orig string) (string, bool) { return orig, true }}
}

// setText replaces the file's text with text.
func setText(kind, key, text string) editStep {
	return editStep{kind, key, func(string, string) (string, bool) { return text, true }}
}

// runEdits checks the project, then applies steps in order through recheck,
// comparing the program recheck answers after each with a full check of the
// same texts. It answers the first difference as readable text, or "", and
// how recheck answered each step it ran. With mustChange set, a step whose
// full check dumps as the one before it is a broken fixture and fails tb.
func runEdits(tb testing.TB, p incrementalProject, steps []editStep, recheck recheckFunc, mustChange bool) (string, []analysis.RecheckOutcome) {
	tb.Helper()
	prev := fullCheck(p, p.texts)
	lastDump := analysis.DumpProgramForTest(prev)
	var outcomes []analysis.RecheckOutcome
	for i, s := range steps {
		cur, ok := prev.Texts[s.key]
		if !ok {
			tb.Fatalf("%s: step %d (%s) edits %s, which the project does not have", p.name, i+1, s.kind, s.key)
		}
		text, ok := s.change(cur, p.texts[s.key])
		if !ok {
			tb.Fatalf("%s: step %d (%s) does not apply to %s:\n%s", p.name, i+1, s.kind, s.key, cur)
		}
		edit := analysis.ProgramEdit{Key: s.key, Text: text}
		// The texts after the edit, read before the recheck consumes prev.
		texts := map[string]string{}
		for key, t := range prev.Texts {
			texts[key] = t
		}
		texts[s.key] = text
		was := analysis.NodesForTest(prev)
		got, outcome := recheck(prev, edit)
		outcomes = append(outcomes, outcome)
		want := analysis.DumpProgramForTest(fullCheck(p, texts))
		if mustChange && want == lastDump {
			tb.Fatalf("%s: step %d (%s) of %s changes nothing a dump shows", p.name, i+1, s.kind, s.key)
		}
		if gotDump := analysis.DumpProgramForTest(got); gotDump != want {
			return fmt.Sprintf("%s: after step %d (%s, editing %s, recheck %s) the recheck's program differs from a full check:\n%s",
				p.name, i+1, s.kind, s.key, outcome.Path, dumpDiff(want, gotDump)), outcomes
		}
		if stale := analysis.StaleForTest(got, was); stale != "" {
			return fmt.Sprintf("%s: after step %d (%s, editing %s, recheck %s) the recheck's program holds a stale analysis: %s",
				p.name, i+1, s.kind, s.key, outcome.Path, stale), outcomes
		}
		prev, lastDump = got, want
	}
	return "", outcomes
}

// bodyOnly reports whether a step of kind changes only bodies, so that the
// recheck must rebuild the edited file's bodies alone: a function's body, or
// the value of a `once` whose type stays the same (early cutoff).
func bodyOnly(kind string) bool {
	return strings.Contains(kind, "body only") || kind == "body back" || strings.Contains(kind, "once value, same type")
}

// needsFull reports whether a step of kind must check the whole program: it
// leaves a file that does not parse, edits one that did not, or changes the
// impls a file brings into the program, which every file may read.
func needsFull(kind string) bool {
	for _, k := range []string{"unparseable", "parses again", "impl", "derive"} {
		if strings.Contains(kind, k) {
			return true
		}
	}
	return false
}

// dependentsMayBeFull are the reasons a step that changes an interface may
// check the whole program: the files that import the edited one are all of
// them, or a file that does not import it calls one of its impls, or the
// program exports functions, whose names are checked across every file.
var dependentsMayBeFull = map[string]bool{
	"every file of the program depends on the edited files":                true,
	"a file that does not import the edited files holds something of them": true,
	"the program exports functions":                                        true,
}

// checkPaths fails tb for each step whose recheck went the wrong way. A
// body-only step (bodyOnly) must rebuild only bodies; a step that needs a
// full check (needsFull) must take one; any other step changes an interface
// and must check the edited file and the files that import it alone, or the
// whole program for one of the reasons dependentsMayBeFull names. A step
// whose kind says "[dependents]" must check its dependents alone. An
// incremental check that never runs passes every comparison, so the harness
// checks that it ran.
func checkPaths(tb testing.TB, name string, steps []editStep, outcomes []analysis.RecheckOutcome) {
	tb.Helper()
	for i, o := range outcomes {
		s := steps[i]
		at := fmt.Sprintf("%s: step %d (%s, editing %s)", name, i+1, s.kind, s.key)
		switch {
		case bodyOnly(s.kind):
			if o.Path != analysis.RecheckBodies {
				tb.Errorf("%s changes only a body, but the recheck went %s: %s", at, o.Path, o.Reason)
			}
		case needsFull(s.kind):
			if o.Path != analysis.RecheckFull {
				tb.Errorf("%s needs a full check, but the recheck went %s", at, o.Path)
			}
		case o.Path == analysis.RecheckDependents:
		case strings.Contains(s.kind, "[dependents]"):
			tb.Errorf("%s must check only the files that import the edited one, but the recheck went %s: %s", at, o.Path, o.Reason)
		case o.Path != analysis.RecheckFull || !dependentsMayBeFull[o.Reason]:
			tb.Errorf("%s changes an interface, but the recheck went %s: %s", at, o.Path, o.Reason)
		}
	}
}

// runChecked is runEdits through recheck, failing tb on a difference or on
// a step that went the wrong way (checkPaths).
func runChecked(tb testing.TB, p incrementalProject, steps []editStep, recheck recheckFunc) {
	tb.Helper()
	diff, outcomes := runEdits(tb, p, steps, recheck, true)
	if diff != "" {
		tb.Fatal(diff)
	}
	checkPaths(tb, p.name, steps, outcomes)
}

// dumpDiff shows where two dumps first disagree: the section it is in, and
// a few lines around it from each side.
func dumpDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(w) && i < len(g) && w[i] == g[i] {
		i++
	}
	section := "(start)"
	for j := i - 1; j >= 0 && j < len(w); j-- {
		if strings.HasPrefix(w[j], "== ") {
			section = w[j]
			break
		}
	}
	excerpt := func(lines []string) string {
		var b strings.Builder
		for j := max(0, i-3); j < min(len(lines), i+4); j++ {
			mark := "  "
			if j == i {
				mark = "> "
			}
			fmt.Fprintf(&b, "    %s%s\n", mark, lines[j])
		}
		return b.String()
	}
	return fmt.Sprintf("  in %s, line %d of the dump\n  full check:\n%s  recheck:\n%s", section, i+1, excerpt(w), excerpt(g))
}

// orderIncremental is orderProject (program_order_test.go) held in memory.
func orderIncremental() incrementalProject {
	texts := map[string]string{}
	for name, src := range orderProject {
		texts[strings.TrimSuffix(name, ".nomi")] = src
	}
	return incrementalProject{name: "order", entry: "main", texts: texts}
}

// orderEdits edit orderProject in each way an incremental check could get
// wrong.
var orderEdits = []editStep{
	replace("body only", "report", `"area ${Shape.area(shape)}"`, `"area: ${Shape.area(shape)}"`),
	replace("body only, a once's value reads it", "main", `"hello"`, `"hello there"`),
	replace("body only, positions shift", "shapes", "        value.radius + Iter.count(Settings.tag)\n",
		"        // The tag counts towards the area.\n\n        value.radius + Iter.count(Settings.tag)\n"),
	replace("signature", "settings", "pub fn default_tag(): String {\n    main.greeting()\n}",
		"pub fn default_tag(suffix: String): String {\n    main.greeting() + suffix\n}"),
	replace("body only, a caller follows the new signature", "main", `io.print(report.title())`, `io.print(report.title())
    io.print(settings.default_tag("!"))`),
	replace("import added", "main", "    settings.Settings\n", "    settings.{self, Settings}\n"),
	restore("signature and import back", "settings"),
	restore("caller and import back", "main"),
	appendText("declaration added", "shapes", "\npub fn perimeter(circle: Circle): Int {\n    circle.radius * 6\n}\n"),
	replace("body only, uses a declaration from another file", "report", `"area: ${Shape.area(shape)}"`, `"area: ${Shape.area(shape)} ${shapes.perimeter(Circle{radius: 1})}"`),
	replace("import added", "report", "    settings.Settings\n", "    settings.Settings\n    shapes\n"),
	restore("declaration's use and import removed", "report"),
	restore("declaration removed", "shapes"),
	appendText("impl added", "shapes", "\npub struct Square {\n    side: Int\n}\n\nimpl Shape for Square {\n    fn area(value: Square): Int {\n        value.side * value.side\n    }\n}\n"),
	appendText("derive added", "shapes", "\nderive Display for Square\n"),
	restore("impl and derive removed", "shapes"),
	replace("derive removed", "report", "derive Display for Labelled\n", ""),
	restore("derive back", "report"),
	replace("impl removed", "report", "impl Shape for Circle {\n    fn area(value: Circle): Int {\n        value.radius\n    }\n}\n", ""),
	restore("impl back", "report"),
	replace("once value, same type", "report", "pub once base = Iter.count(main.greeting())", "pub once base = Iter.count(main.greeting()) + 1"),
	replace("once value, new type", "report", "pub once base = Iter.count(main.greeting()) + 1", "pub once base = main.greeting()"),
	restore("once back", "report"),
	replace("import removed", "settings", "import main\n", ""),
	restore("import back", "settings"),
	replace("body only, a type error", "shapes", "value.radius + Iter.count(Settings.tag)", "value.radius + Settings.tag"),
	restore("body only, type error fixed", "shapes"),
	replace("unparseable", "report", "pub fn title(): String {", "pub fn title(: String {"),
	replace("unparseable, typing on", "report", "pub fn title(: String {", "pub fn title(n: String {"),
	restore("parses again", "report"),
	replace("entry unparseable", "main", "fn main() {", "fn main( {"),
	restore("entry parses again", "main"),
	// Two bodies edited at once, as one analysis of a burst of typing sees
	// them: the declaration between them moves by line, not as one stretch.
	editStep{"body only, two places", "report", func(cur, _ string) (string, bool) {
		if !strings.Contains(cur, "    Settings.tag\n") || !strings.Contains(cur, `"area ${Shape.area(shape)}"`) {
			return "", false
		}
		cur = strings.Replace(cur, "    Settings.tag\n", "    // The tag.\n\n    Settings.tag\n", 1)
		return strings.Replace(cur, `"area ${Shape.area(shape)}"`, `"the area ${Shape.area(shape)}"`, 1), true
	}},
	restore("body back", "report"),
}

// genericEdits are edits any project file takes: a declaration added, its
// body and signature changed, and removed; a body of the file's own changed
// when it has a function whose body starts on its own line; a `once`
// added whose value changes its type and keeps it; an impl and a derive
// added; an import added; a type error; a file left unparseable. Each
// sequence restores the file at the end.
func genericEdits(key, text string) []editStep {
	steps := []editStep{
		appendText("declaration added", key, "\npub fn incremental_added(): Int {\n    1\n}\n"),
		replace("body only", key, "pub fn incremental_added(): Int {\n    1\n}", "pub fn incremental_added(): Int {\n    2\n}"),
		replace("signature", key, "pub fn incremental_added(): Int {\n    2\n}", "pub fn incremental_added(n: Int): Int {\n    n\n}"),
		restore("declaration removed", key),
	}
	if line := firstBodyLine(text); line != "" {
		steps = append(steps,
			replace("body only, positions shift", key, line, line+"    // A comment.\n\n"),
			replace("body only, new binding", key, line+"    // A comment.\n\n", line+"    incremental_probe = 0\n"),
			restore("body back", key),
		)
	}
	return append(steps,
		appendText("once added", key, "\npub once incremental_once = 1\n"),
		replace("once value, same type", key, "pub once incremental_once = 1\n", "pub once incremental_once = 2\n"),
		replace("once value, new type", key, "pub once incremental_once = 2\n", "pub once incremental_once = \"two\"\n"),
		restore("once removed", key),
		appendText("impl added", key, "\npub struct IncrementalProbe {\n    n: Int\n}\n\nimpl Display for IncrementalProbe {\n    fn to_string(value: IncrementalProbe): String {\n        \"probe\"\n    }\n}\n"),
		replace("impl replaced by a derive", key, "impl Display for IncrementalProbe {\n    fn to_string(value: IncrementalProbe): String {\n        \"probe\"\n    }\n}\n", "derive Display for IncrementalProbe\n"),
		restore("derive removed", key),
		setText("import added", key, "import std/strings\n"+text),
		restore("import removed", key),
		appendText("type error", key, "\nfn incremental_bad(): Int {\n    \"text\"\n}\n"),
		restore("type error fixed", key),
		appendText("unparseable", key, "\nfn incremental_broken( {\n"),
		restore("parses again", key),
	)
}

// firstBodyLine is the first line of text that opens a top-level function
// whose body starts on the next line, or "".
func firstBodyLine(text string) string {
	for _, line := range strings.SplitAfter(text, "\n") {
		if (strings.HasPrefix(line, "fn ") || strings.HasPrefix(line, "pub fn ")) && strings.HasSuffix(line, "{\n") {
			return line
		}
	}
	return ""
}

// corpusProjects are the corpus directories with more than one .nomi file
// and no Go bindings, each with its entry: main.nomi, or else its first
// test file.
var corpusProjects = sync.OnceValues(func() ([]incrementalProject, error) {
	var out []incrementalProject
	dirs, err := filepath.Glob(filepath.Join("..", "..", "tests", "*", "*"))
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			continue
		}
		p, err := readProject(dir, strings.TrimPrefix(filepath.ToSlash(dir), "../../tests/"))
		if err != nil {
			return nil, err
		}
		if len(p.texts) > 1 && p.entry != "" {
			out = append(out, p)
		}
	}
	return out, nil
})

// readProject reads every .nomi file under dir into a project.
func readProject(dir, name string) (incrementalProject, error) {
	p := incrementalProject{name: name, texts: map[string]string{}}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".nomi" {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		p.texts[strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")] = string(src)
		return nil
	})
	if _, ok := p.texts["main"]; ok {
		p.entry = "main"
	} else {
		var tests []string
		for key := range p.texts {
			if strings.HasSuffix(key, "_test") {
				tests = append(tests, key)
			}
		}
		sort.Strings(tests)
		if len(tests) > 0 {
			p.entry = tests[0]
		}
	}
	return p, err
}

// dungeonDir is a game of about 500 lines in five files, outside this
// repository. A test that reads it skips when it is not there.
const dungeonDir = "/Users/davidbaldwin/Code/nomi/dungeon"

// dungeonProject reads the dungeon into memory, or skips tb.
func dungeonProject(tb testing.TB) incrementalProject {
	tb.Helper()
	if _, err := os.Stat(dungeonDir); err != nil {
		tb.Skipf("no dungeon project at %s", dungeonDir)
	}
	p, err := readProject(dungeonDir, "dungeon")
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

// dungeonEdits edit the dungeon's files where another file reads them.
var dungeonEdits = []editStep{
	replace("body only", "world", `.North -> "north"`, `.North -> "the north"`),
	replace("signature", "world", "pub fn parse(word: String): Maybe<Direction> {", "pub fn parse(word: String, strict: Bool): Maybe<Direction> {"),
	restore("signature back", "world"),
	replace("once annotation dropped, same type", "world", "pub once directions: List<Direction> = [.North, .South, .East, .West]", "pub once directions = [Direction.North, Direction.South, Direction.East, Direction.West]"),
	replace("once value, new type", "world", "pub once directions = [Direction.North, Direction.South, Direction.East, Direction.West]", "pub once directions = Direction.North"),
	restore("once back", "world"),
	replace("impl removed", "world", "impl Display for Item {", "impl Item {\n    fn unused_display(item: Item): String {\n        \"\"\n    }\n}\n\nimpl Item {"),
	restore("impl back", "world"),
	replace("entry body only", "main", `io.print("You wake up in the dark. Type 'help' for commands.")`, `io.print("You wake up. Type 'help' for commands.")`),
	replace("entry unparseable", "main", "fn turn(state: Game, line: String): Game {", "fn turn(state: Game, line: String: Game {"),
	restore("entry parses again", "main"),
}

// layeredProject is a project whose imports form layers, so that an edit to
// one file has importers that are not every file: util, which model, view
// and the entry import; model, which view and the entry import; view and
// palette, which only the entry imports.
func layeredProject() incrementalProject {
	return incrementalProject{name: "layered", entry: "main", texts: map[string]string{
		"main": `import {
    std/io
    model
    view
    util
    palette
}

fn main() {
    item = model.make("anvil", model.default_weight)
    io.print(view.show(item))
    io.print("${util.clamp(-1)} ${model.heavy(item)} ${palette.name()}")
}
`,
		"util": `pub fn clamp(n: Int): Int {
    if n < 0 { 0 } else { n }
}

pub struct Span {
    from: Int
    to: Int
}
`,
		"model": `import util

pub struct Item {
    name: String
    weight: Int
}

pub fn make(name: String, weight: Int): Item {
    Item{name: name, weight: util.clamp(weight)}
}

pub fn heavy(item: Item): Bool {
    item.weight > 10
}

pub once default_weight = util.clamp(3)

pub once label = "weight ${default_weight}"
`,
		"view": `import {
    model.Item
    util
}

pub fn show(item: Item): String {
    "${item.name}: ${util.clamp(item.weight)}"
}
`,
		"palette": `pub fn name(): String {
    "red"
}

pub fn unused(n: Int): Int {
    n
}
`,
	}}
}

// layeredEdits change signatures with one importer, none and many, add and
// remove a public declaration, add an impl to the leaf, and change a
// `once`'s value keeping its type and changing it.
var layeredEdits = []editStep{
	replace("signature used by one importer [dependents]", "view", "pub fn show(item: Item): String {", "pub fn show(item: Item, prefix: String = \"\"): String {"),
	replace("body only, the new parameter used", "view", `"${item.name}: `, `"${prefix}${item.name}: `),
	restore("signature back [dependents]", "view"),
	replace("signature used by none [dependents]", "palette", "pub fn unused(n: Int): Int {", "pub fn unused(n: Int, m: Int = 0): Int {"),
	restore("signature back [dependents]", "palette"),
	replace("signature used by many [dependents]", "util", "pub fn clamp(n: Int): Int {", "pub fn clamp(n: Int, low: Int = 0): Int {"),
	replace("body only, the new parameter used", "util", "if n < 0 { 0 } else { n }", "if n < low { low } else { n }"),
	replace("signature breaks its callers [dependents]", "util", "pub fn clamp(n: Int, low: Int = 0): Int {", "pub fn clamp(n: String, low: Int = 0): Int {"),
	restore("signature back [dependents]", "util"),
	appendText("public declaration added [dependents]", "model", "\npub fn light(item: Item): Bool {\n    item.weight < 2\n}\n"),
	replace("body only, the new declaration used", "main", "${model.heavy(item)}", "${model.heavy(item)} ${model.light(item)}"),
	restore("body only, the new declaration's use removed", "main"),
	restore("public declaration removed [dependents]", "model"),
	appendText("impl added in the leaf", "util", "\nimpl Display for Span {\n    fn to_string(value: Span): String {\n        \"${value.from}..${value.to}\"\n    }\n}\n"),
	restore("impl removed from the leaf", "util"),
	replace("once value, same type", "model", "pub once default_weight = util.clamp(3)", "pub once default_weight = util.clamp(4)"),
	replace("once value, same type, a once of the file reads it", "model", "pub once label = \"weight ${default_weight}\"", "pub once label = \"weight: ${default_weight}\""),
	replace("once value, new type [dependents]", "model", "pub once default_weight = util.clamp(4)", "pub once default_weight = \"four\""),
	restore("once back [dependents]", "model"),
}

// dispatchProject is a project where a file the entry imports calls an impl
// the entry declares: shade does not import main, but its call to
// Shade.shade dispatches to main's impl.
func dispatchProject() incrementalProject {
	return incrementalProject{name: "dispatch", entry: "main", texts: map[string]string{
		"main": `import {
    std/io
    shade.{self, Shade, Tint}
}

impl Shade for Tint {
    fn shade(value: Tint): String {
        "tint ${value.level}"
    }
}

fn main() {
    io.print(shade.describe())
}
`,
		"shade": `pub interface Shade {
    fn shade(value: self): String
}

pub struct Tint {
    level: Int
}

pub fn describe(): String {
    Shade.shade(Tint{level: 2})
}
`,
	}}
}

// dispatchEdits change the entry's interface while shade holds its impl.
var dispatchEdits = []editStep{
	appendText("declaration added, a file the entry imports calls its impl", "main", "\npub fn extra(): Int {\n    1\n}\n"),
	restore("declaration removed, a file the entry imports calls its impl", "main"),
}

func TestIncrementalMatchesFull(t *testing.T) {
	dm := newManager()
	recheck := dm.RecheckWithOutcome
	t.Run("layered", func(t *testing.T) {
		runChecked(t, layeredProject(), layeredEdits, recheck)
	})
	t.Run("dispatch", func(t *testing.T) {
		p := dispatchProject()
		diff, outcomes := runEdits(t, p, dispatchEdits, recheck, true)
		if diff != "" {
			t.Fatal(diff)
		}
		for i, o := range outcomes {
			if o.Path != analysis.RecheckFull || o.Reason != "a file that does not import the edited files holds something of them" {
				t.Errorf("step %d (%s) went %s (%s), want a full check because shade holds main's impl", i+1, dispatchEdits[i].kind, o.Path, o.Reason)
			}
		}
		if errs := fullCheck(p, p.texts).Errors; len(errs["main"])+len(errs["shade"]) > 0 {
			t.Errorf("the project does not check: %v", errs)
		}
	})
	t.Run("order", func(t *testing.T) {
		p := orderIncremental()
		runChecked(t, p, orderEdits, recheck)
		for _, key := range []string{"main", "shapes"} {
			runChecked(t, p, genericEdits(key, p.texts[key]), recheck)
		}
	})
	t.Run("dungeon", func(t *testing.T) {
		p := dungeonProject(t)
		cur := map[string]string{}
		for key, text := range p.texts {
			cur[key] = text
		}
		for _, s := range dungeonEdits {
			text, ok := s.change(cur[s.key], p.texts[s.key])
			if !ok {
				t.Skipf("the dungeon at %s has changed: %s no longer applies to %s", dungeonDir, s.kind, s.key)
			}
			cur[s.key] = text
		}
		runChecked(t, p, dungeonEdits, recheck)
		for _, key := range []string{"game", "command"} {
			runChecked(t, p, genericEdits(key, p.texts[key]), recheck)
		}
	})
	projects, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) < 20 {
		t.Fatalf("found %d multi-file corpus projects, want at least 20", len(projects))
	}
	for i, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			// The entry, and in every fourth project the first other file
			// the entry reaches too. Each check costs a few milliseconds;
			// FuzzIncrementalMatchesFull edits every file.
			keys := []string{p.entry}
			if other := fullCheck(p, p.texts).Keys(); len(other) > 1 && i%4 == 0 {
				keys = append(keys, other[1])
			}
			for _, key := range keys {
				runChecked(t, p, genericEdits(key, p.texts[key]), recheck)
			}
		})
	}
}

// TestIncrementalMatchesFullForSeveralFiles: edits to several files at once
// (RecheckEdits), as a save of every file or a checkout makes them, answer
// what a full check of the edited texts does. Edits that each change only
// bodies rebuild only those bodies; any other set checks the edited files
// and the files that import them.
func TestIncrementalMatchesFullForSeveralFiles(t *testing.T) {
	p := layeredProject()
	type multi struct {
		kind  string
		path  analysis.RecheckPath
		steps []editStep
	}
	viewBody := replace("", "view", `"${item.name}: `, `"item ${item.name}: `)
	paletteBody := replace("", "palette", `"red"`, `"blue"`)
	viewSignature := replace("", "view", "pub fn show(item: Item): String {", "pub fn show(item: Item, prefix: String = \"\"): String {")
	utilSignature := replace("", "util", "pub fn clamp(n: Int): Int {", "pub fn clamp(n: Int, low: Int = 0): Int {")
	modelBody := replace("", "model", "item.weight > 10", "item.weight > 12")
	sets := []multi{
		{"two bodies", analysis.RecheckBodies, []editStep{viewBody, paletteBody}},
		{"bodies back", analysis.RecheckBodies, []editStep{restore("", "view"), restore("", "palette")}},
		{"a signature and a body", analysis.RecheckDependents, []editStep{viewSignature, paletteBody}},
		{"a signature and a body back", analysis.RecheckDependents, []editStep{restore("", "view"), restore("", "palette")}},
		{"a leaf's signature and its importer's body", analysis.RecheckDependents, []editStep{utilSignature, modelBody}},
		{"both back", analysis.RecheckDependents, []editStep{restore("", "util"), restore("", "model")}},
	}
	dm := newManager()
	prev := fullCheck(p, p.texts)
	for i, set := range sets {
		texts := maps.Clone(prev.Texts)
		var edits []analysis.ProgramEdit
		for _, s := range set.steps {
			text, ok := s.change(texts[s.key], p.texts[s.key])
			if !ok {
				t.Fatalf("set %d (%s) does not apply to %s", i+1, set.kind, s.key)
			}
			texts[s.key] = text
			edits = append(edits, analysis.ProgramEdit{Key: s.key, Text: text})
		}
		was := analysis.NodesForTest(prev)
		got, outcome := dm.RecheckEdits(prev, edits)
		if outcome.Path != set.path {
			t.Errorf("set %d (%s) went %s (%s), want %s", i+1, set.kind, outcome.Path, outcome.Reason, set.path)
		}
		want := analysis.DumpProgramForTest(fullCheck(p, texts))
		if gotDump := analysis.DumpProgramForTest(got); gotDump != want {
			t.Fatalf("after set %d (%s) the recheck's program differs from a full check:\n%s", i+1, set.kind, dumpDiff(want, gotDump))
		}
		if stale := analysis.StaleForTest(got, was); stale != "" {
			t.Fatalf("after set %d (%s) the recheck's program holds a stale analysis: %s", i+1, set.kind, stale)
		}
		prev = got
	}
}

// staleDumps is the dump of each project's first check in this process, so
// that a second run (-count=2) is compared with the first.
var staleDumps sync.Map

// TestDumpProgramIsStable: two independent checks of the same texts dump
// alike, and so does every later check in the process, after the stdlib
// state every check shares has served others.
func TestDumpProgramIsStable(t *testing.T) {
	projects := []incrementalProject{orderIncremental()}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	projects = append(projects, corpus[0], corpus[len(corpus)/2], corpus[len(corpus)-1])
	if _, err := os.Stat(dungeonDir); err == nil {
		projects = append(projects, dungeonProject(t))
	}
	for _, p := range projects {
		first := analysis.DumpProgramForTest(fullCheck(p, p.texts))
		if second := analysis.DumpProgramForTest(fullCheck(p, p.texts)); second != first {
			t.Errorf("%s: two checks of the same texts dump differently:\n%s", p.name, dumpDiff(first, second))
		}
		if earlier, loaded := staleDumps.LoadOrStore(p.name, first); loaded && earlier.(string) != first {
			t.Errorf("%s: a check dumps differently from this process's first check of it:\n%s", p.name, dumpDiff(earlier.(string), first))
		}
		if !strings.Contains(first, "== file "+p.entry) || !strings.Contains(first, "syntax tree") {
			t.Errorf("%s: the dump does not show its entry:\n%s", p.name, first)
		}
	}
}

// TestIncrementalHarnessCatchesAStaleResult: a recheck that keeps part of
// the program before the edit fails the harness, with a diff that names the
// file and shows both sides. One keeps the previous program for an edit to a
// function's body, as a body-only fast path that installed nothing would; the
// other keeps the previous type of every unannotated `once`, as an early
// cutoff that never re-inferred one would.
func TestIncrementalHarnessCatchesAStaleResult(t *testing.T) {
	dm := newManager()
	p := orderIncremental()

	ignoresBodies := func(prev *analysis.CheckedProgram, edit analysis.ProgramEdit) (*analysis.CheckedProgram, analysis.RecheckOutcome) {
		stale := *prev
		stale.Texts = map[string]string{}
		for key, text := range prev.Texts {
			stale.Texts[key] = text
		}
		stale.Texts[edit.Key] = edit.Text
		return &stale, analysis.RecheckOutcome{Path: analysis.RecheckBodies}
	}
	diff, _ := runEdits(t, p, orderEdits[:1], ignoresBodies, true)
	for _, want := range []string{"step 1 (body only, editing report, recheck bodies)", "== file report", "full check:", "recheck:", "> expr 22:14", "> expr 22:13"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the harness's report on a recheck that ignores a body edit has no %q:\n%s", want, diff)
		}
	}

	keepsOnceTypes := func(prev *analysis.CheckedProgram, edit analysis.ProgramEdit) (*analysis.CheckedProgram, analysis.RecheckOutcome) {
		// The types before the recheck, which consumes prev.
		was := map[string]map[analysis.Pos]analysis.Type{}
		for key, fa := range prev.Files {
			was[key] = map[analysis.Pos]analysis.Type{}
			for pos, sym := range fa.Definitions {
				if sym != nil && sym.Name == "base" {
					was[key][pos] = sym.Type
				}
			}
		}
		next, outcome := dm.RecheckWithOutcome(prev, edit)
		for key, fa := range next.Files {
			for pos, sym := range fa.Definitions {
				if typ, ok := was[key][pos]; ok && sym != nil && sym.Name == "base" {
					sym.Type = typ
				}
			}
		}
		return next, outcome
	}
	onceEdit := []editStep{replace("once value, new type", "report", "pub once base = Iter.count(main.greeting())", "pub once base = main.greeting()")}
	diff, _ = runEdits(t, p, onceEdit, keepsOnceTypes, true)
	for _, want := range []string{"step 1 (once value, new type, editing report, recheck full)", "full check:", "recheck:", "base", "= Int", "= String"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the harness's report on a recheck that keeps a stale once type has no %q:\n%s", want, diff)
		}
	}
}

// TestCheckProgramPrintsWhatTheFrontEndPrints: the diagnostics a checked
// program's dump lists as printed are the ones `nomi check` prints for the
// same files, in its order, for programs with no syntax error.
func TestCheckProgramPrintsWhatTheFrontEndPrints(t *testing.T) {
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	order := orderIncremental()
	withTypeError := orderIncremental()
	withTypeError.name = "order with a type error"
	withTypeError.texts = map[string]string{}
	for key, text := range order.texts {
		withTypeError.texts[key] = text
	}
	withTypeError.texts["shapes"] = strings.Replace(withTypeError.texts["shapes"], "value.radius + Iter.count(Settings.tag)", "value.radius + Settings.tag", 1)
	withTypeError.texts["report"] = strings.Replace(withTypeError.texts["report"], "pub once base = Iter.count(main.greeting())", "pub once base = main.greeting()", 1)
	for _, p := range append([]incrementalProject{order, withTypeError}, corpus...) {
		dir := t.TempDir()
		for key, text := range p.texts {
			path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		prog := newManager().CheckProgram(dir, p.entry, p.texts)
		var want []string
		for _, line := range strings.Split(analysis.DumpProgramForTest(prog), "\n== printed\n")[1:] {
			for _, l := range strings.Split(line, "\n") {
				if l != "" && !strings.HasPrefix(l, "  ") {
					want = append(want, l)
				}
			}
		}
		var got []string
		_, err := frontend.New(frontend.Config{}).CheckFileSource(filepath.Join(dir, p.entry+".nomi"), p.texts[p.entry], frontend.Mode{Tests: true})
		if ds, ok := err.(frontend.Diagnostics); ok {
			for _, d := range ds {
				key := strings.TrimSuffix(strings.TrimPrefix(d.Path, dir+string(filepath.Separator)), ".nomi")
				got = append(got, fmt.Sprintf("%s:%d:%d", key, d.Line, d.Col)+" "+d.Message)
			}
		} else if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		// The dump's printed lines are key:line:col-end message [code].
		for i, l := range want {
			key, rest, _ := strings.Cut(l, ":")
			pos, msg, _ := strings.Cut(rest, " ")
			pos, _, _ = strings.Cut(pos, "-")
			if j := strings.LastIndex(msg, " ["); j >= 0 && strings.HasSuffix(msg, "]") {
				msg = msg[:j]
			}
			want[i] = key + ":" + pos + " " + msg
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: the front end prints\n%s\nand the checked program's dump lists\n%s", p.name, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if p.name == withTypeError.name && len(got) < 2 || p.name == order.name && len(got) == 0 {
			t.Errorf("%s: the front end prints %d diagnostics, want the program's errors", p.name, len(got))
		}
	}
}

// BenchmarkRecheckDungeon times a recheck of the dungeon (dungeonDir), copied
// to a temporary directory, after an edit that alternates between two texts
// of one file: a body-only edit to game.nomi, which rebuilds that file's
// bodies; a signature edit to game.nomi, which the entry imports; a
// signature edit to world.nomi, which every other file imports; a signature
// edit to the entry; and a body-only edit to the entry.
func BenchmarkRecheckDungeon(b *testing.B) {
	p := dungeonProject(b)
	dir := writeProject(b, p)
	for _, bench := range []struct {
		name, key, old, new string
	}{
		{"body", "game", "{ 3 } else { 1 }", "{ 4 } else { 1 }"},
		{"signature", "game", "fn strength(game: Game): Int {", "fn strength(game: Game, _bonus: Int = 0): Int {"},
		{"leaf signature", "world", "pub fn room(place: Place): Room {", "pub fn room(place: Place, _lit: Bool = True): Room {"},
		{"entry signature", "main", "fn turn(state: Game, line: String): Game {", "fn turn(state: Game, line: String, _echo: Bool = False): Game {"},
		{"entry body", "main", "You wake up in the dark.", "You wake up."},
	} {
		b.Run(bench.name, func(b *testing.B) {
			edited := strings.Replace(p.texts[bench.key], bench.old, bench.new, 1)
			if edited == p.texts[bench.key] {
				b.Skipf("the dungeon has changed: %q is not in %s", bench.old, bench.key)
			}
			dm := newManager()
			prog := dm.CheckProgram(dir, p.entry, p.texts)
			texts := []string{edited, p.texts[bench.key]}
			b.ResetTimer()
			for i := range b.N {
				prog = dm.Recheck(prog, analysis.ProgramEdit{Key: bench.key, Text: texts[i%2]})
			}
		})
	}
}
