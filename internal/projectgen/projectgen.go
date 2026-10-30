// Package projectgen generates large multi-file Nomi projects, for tests
// and benchmarks that need a program at the sizes incremental checking is
// meant for. Only tests and benchmarks import it.
//
// A project is deterministic from its Config: the same seed and size give
// the same files byte for byte. Its shape follows real code:
//
//   - three hub files under core/ that most files import: ids.nomi (a
//     distinct type, shared structs and enums, the Describe interface),
//     util.nomi (generic functions, a generic struct, and a struct with a
//     record field beside functions over a record and an `Iter<Int>`,
//     which a unit calls with a list) and text.nomi;
//   - unit files of 50 to 300 lines in a handful of directories, grouped in
//     clusters. A cluster's first unit declares an interface the cluster's
//     other units implement; a unit imports the one before it in its
//     cluster (chains), sometimes a unit of an earlier cluster, and
//     sometimes the one after it (an import cycle);
//   - in each unit: a struct, an enum with data, a distinct type, a generic
//     struct and generic functions, derives (Equatable, Hashable,
//     Comparable, Display, ToJson), an inherent impl block, impls of
//     interfaces declared in other files, an unannotated `once` that other
//     files read, `case`, `?`, pipes and Iter chains;
//   - where two units import each other, types that refer to each other
//     across the two files: one's Link holds the other's Nodes, and a
//     Node's parent is a Link;
//   - main.nomi, the entry, which imports every unit no other unit imports;
//   - a test file beside about one unit in four, with a `group`, tests and
//     `subtests`.
//
// Every project checks with no diagnostic, lowers, runs, and passes its
// tests. The bodies are a fixed set of shapes, not type-directed random
// programs like vmhost's generator (lowering_gen_test.go), so that a large
// project is clean; none of them is a shape vmhost's knownLoweringGaps
// lists (no `case` over a list of functions, no block-local type, no `and`
// or `or` operand that leaves the body). A unit's Rec derives Equatable,
// Hashable and Display over core/ids' Id and Money, so each project
// derives across files; its Entry derives over its own file's types.
//
// Each unit and the util hub hold a probe function that Edit rewrites, so a
// benchmark or test can make the same kind of edit at every size.
package projectgen

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/format"
)

// Config says which project to generate.
type Config struct {
	// Seed picks the project: the same Seed and Lines give the same files.
	Seed int64
	// Lines is about how many lines the entry's program holds, hubs and
	// entry included and test files left out. It is at least about 400.
	Lines int
}

// Project is a generated project.
type Project struct {
	// Files holds each .nomi file's text by module key ("main",
	// "billing/ledger3", "core/util"), test files included.
	Files map[string]string
	// Entry is the entry's module key, "main".
	Entry string
	// Hubs are the hub files' keys, Units the unit files' keys in
	// generation order, and Tests the test files' keys.
	Hubs, Units, Tests []string
	// Imports holds each file's imports of other project files, by key.
	Imports map[string][]string

	units map[string]*unit
}

// Manifest is the project's nomi.toml.
const Manifest = "[module]\nname = \"generated\"\nentry_points = [\"main\"]\n"

// dirs are the directories units go in.
var dirs = []string{"billing", "catalog", "accounts", "shipping", "reports", "search"}

// words name units, so that files read as a project's would.
var words = []string{"ledger", "invoice", "price", "stock", "order", "cart", "refund",
	"tax", "quote", "member", "session", "route", "parcel", "label", "audit",
	"metric", "index", "query", "ranking", "digest"}

// unit is one generated unit file.
type unit struct {
	i    int
	key  string
	name string // the last path segment, its qualifier
	// head is the key of the cluster head whose Rank interface the unit
	// implements; the head implements its own.
	head      string
	headIndex int
	deps      []*unit // units it imports and calls (lower index)
	back      *unit   // the unit after it, imported for its once value (a cycle)
	importers int
	fns       int // f<i>_0 .. f<i>_<fns-1>
	usesUtil  bool
	usesText  bool
	// light leaves out the Summary and Pair types, pick, checked and
	// doubled, for a shorter file.
	light      bool
	shapeSeeds []int
}

// Generate builds the project cfg names.
func Generate(cfg Config) *Project {
	r := rand.New(rand.NewSource(cfg.Seed))
	p := &Project{
		Files:   map[string]string{},
		Entry:   "main",
		Hubs:    []string{"core/ids", "core/util", "core/text"},
		Imports: map[string][]string{},
		units:   map[string]*unit{},
	}
	p.Files["core/ids"] = idsHub
	p.Files["core/util"] = utilHub
	p.Files["core/text"] = textHub
	p.Imports["core/util"] = []string{"core/ids"}
	p.Imports["core/text"] = []string{"core/util"}
	lines := countLines(idsHub) + countLines(utilHub) + countLines(textHub) + 20

	var all []*unit
	var clusterStart int
	cluster := 0
	clusterSize := 4 + r.Intn(7)
	for i := 0; lines < cfg.Lines || len(all) < 2; i++ {
		if i-clusterStart >= clusterSize {
			clusterStart, cluster, clusterSize = i, cluster+1, 4+r.Intn(7)
		}
		dir := dirs[cluster%len(dirs)]
		name := fmt.Sprintf("%s%d", words[r.Intn(len(words))], i)
		u := &unit{i: i, key: dir + "/" + name, name: name, fns: 1 + r.Intn(20)}
		u.usesUtil = r.Float64() < 0.6
		u.usesText = r.Float64() < 0.3
		u.light = r.Float64() < 0.3
		if i > clusterStart {
			u.head, u.headIndex = all[clusterStart].key, clusterStart
			prev := all[i-1]
			if r.Float64() < 0.6 && prev.key != u.head {
				u.deps = append(u.deps, prev)
			}
			if r.Float64() < 0.15 && prev.back == nil {
				prev.back = u
			}
		} else {
			u.head, u.headIndex = u.key, i
		}
		if clusterStart > 0 && r.Float64() < 0.25 {
			// A unit of an earlier cluster.
			d := all[r.Intn(clusterStart)]
			if !u.hasDep(d) {
				u.deps = append(u.deps, d)
			}
		}
		for k := 0; k < u.fns; k++ {
			u.shapeSeeds = append(u.shapeSeeds, r.Intn(1<<30))
		}
		all = append(all, u)
		p.units[u.key] = u
		p.Units = append(p.Units, u.key)
		lines += unitLines(u)
	}
	for _, u := range all {
		for _, d := range u.deps {
			d.importers++
		}
		if u.back != nil {
			u.back.importers++
		}
		if u.head != u.key {
			all[u.headIndex].importers++
		}
	}
	for _, u := range all {
		text, imports := renderUnit(u)
		p.Files[u.key] = text
		p.Imports[u.key] = imports
	}
	var roots []*unit
	for _, u := range all {
		if u.importers == 0 {
			roots = append(roots, u)
		}
	}
	// Units that only import each other (a cycle no other unit imports)
	// are not reached from the roots: the entry imports one of each such
	// group too.
	reached := map[string]bool{}
	var reach func(key string)
	reach = func(key string) {
		if reached[key] {
			return
		}
		reached[key] = true
		for _, to := range p.Imports[key] {
			reach(to)
		}
	}
	for _, u := range roots {
		reach(u.key)
	}
	for _, u := range all {
		if !reached[u.key] {
			roots = append(roots, u)
			reach(u.key)
		}
	}
	p.Files["main"], p.Imports["main"] = renderMain(roots)
	for _, u := range all {
		if u.i%4 == 1 || len(all) < 4 && u.i == 0 {
			key := u.key + "_test"
			p.Files[key] = renderTest(u)
			p.Imports[key] = []string{u.key}
			p.Tests = append(p.Tests, key)
		}
	}
	// The files are written as `nomi fmt` would write them.
	for key, text := range p.Files {
		formatted, err := format.Format(text)
		if err != nil {
			panic(fmt.Sprintf("projectgen: %s does not format: %v\n%s", key, err, text))
		}
		p.Files[key] = formatted
	}
	return p
}

func (u *unit) hasDep(d *unit) bool {
	for _, x := range u.deps {
		if x == d {
			return true
		}
	}
	return false
}

// cycleFront is the unit whose back u is when each imports the other, so the
// two declare types that refer to each other; nil otherwise.
func (u *unit) cycleFront() *unit {
	for _, d := range u.deps {
		if d.back == u {
			return d
		}
	}
	return nil
}

func countLines(s string) int { return strings.Count(s, "\n") }

// unitLines is about how many lines renderUnit writes for u.
func unitLines(u *unit) int {
	if u.light {
		return 75 + 12*u.fns
	}
	return 120 + 12*u.fns
}

// Lines answers how many lines the entry's program holds: every file but
// the test files.
func (p *Project) Lines() int {
	n := 0
	for key, text := range p.Files {
		if !strings.HasSuffix(key, "_test") {
			n += countLines(text)
		}
	}
	return n
}

// TestLines answers how many lines the test files hold.
func (p *Project) TestLines() int {
	n := 0
	for _, key := range p.Tests {
		n += countLines(p.Files[key])
	}
	return n
}

// ProgramTexts answers the texts of the entry's program, by module key.
func (p *Project) ProgramTexts() map[string]string {
	out := map[string]string{}
	for key, text := range p.Files {
		if !strings.HasSuffix(key, "_test") {
			out[key] = text
		}
	}
	return out
}

// Write writes the project, nomi.toml included, under dir.
func (p *Project) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "nomi.toml"), []byte(Manifest), 0o644); err != nil {
		return err
	}
	for key, text := range p.Files {
		path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Dependents answers the program files that import key, directly or
// through others, key itself left out, in key order. Test files are not in
// the entry's program and are left out.
func (p *Project) Dependents(key string) []string {
	importers := map[string][]string{}
	for from, tos := range p.Imports {
		if strings.HasSuffix(from, "_test") {
			continue
		}
		for _, to := range tos {
			importers[to] = append(importers[to], from)
		}
	}
	seen := map[string]bool{key: true}
	work := []string{key}
	for len(work) > 0 {
		k := work[len(work)-1]
		work = work[:len(work)-1]
		for _, from := range importers[k] {
			if !seen[from] {
				seen[from] = true
				work = append(work, from)
			}
		}
	}
	delete(seen, key)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Leaf answers a unit only the entry imports, the last such one generated.
func (p *Project) Leaf() string {
	for i := len(p.Units) - 1; i >= 0; i-- {
		if p.units[p.Units[i]].importers == 0 {
			return p.Units[i]
		}
	}
	return p.Units[len(p.Units)-1]
}

// Hub answers the hub Edit's hub edits rewrite, core/util.
func (p *Project) Hub() string { return "core/util" }

// EditKind is a kind of edit Edit makes.
type EditKind string

const (
	// BodyLeaf changes a function body in Leaf().
	BodyLeaf EditKind = "body edit, leaf"
	// BodyHub changes a function body in Hub().
	BodyHub EditKind = "body edit, hub"
	// SignatureLeaf adds a defaulted parameter to a function in Leaf(), or
	// takes it away again.
	SignatureLeaf EditKind = "signature edit, leaf"
	// SignatureHub does the same in Hub().
	SignatureHub EditKind = "signature edit, hub"
	// ImplLeaf adds an impl of Display for Leaf()'s enum, or takes it away.
	ImplLeaf EditKind = "impl added, leaf"
	// BodyEntry changes a value in main's body.
	BodyEntry EditKind = "body edit, entry"
)

// EditKinds lists every kind Edit makes, in the order reports list them.
var EditKinds = []EditKind{BodyLeaf, BodyHub, SignatureLeaf, SignatureHub, ImplLeaf, BodyEntry}

// Edit answers the file kind's n-th edit rewrites and the file's text after
// it. It rewrites the generated text, so edit n differs from edit n-1 and
// edit n+2 is edit n again: a sequence of edits, each applied to the text
// the one before left, changes the file every time.
func (p *Project) Edit(kind EditKind, n int) (key, text string) {
	switch kind {
	case BodyLeaf, SignatureLeaf, ImplLeaf:
		key = p.Leaf()
	case BodyHub, SignatureHub:
		key = p.Hub()
	case BodyEntry:
		key = p.Entry
	}
	orig := p.Files[key]
	switch kind {
	case BodyLeaf, BodyHub:
		return key, strings.Replace(orig, "    n + 1\n", fmt.Sprintf("    n + %d\n", 2+n%2), 1)
	case SignatureLeaf, SignatureHub:
		if n%2 == 1 {
			return key, orig
		}
		return key, strings.Replace(orig, "(n: Int) -> Int {\n    n + 1\n", "(n: Int, extra: Int = 0) -> Int {\n    n + extra + 1\n", 1)
	case ImplLeaf:
		if n%2 == 1 {
			return key, orig
		}
		u := p.units[key]
		return key, orig + fmt.Sprintf("\nimpl Display for Kind%d {\n    fn to_string(kind: Kind%d) -> String {\n        case kind {\n            .Plain -> \"plain\"\n            .Sized(n) -> \"sized ${n}\"\n            .Tagged(t) -> t\n        }\n    }\n}\n", u.i, u.i)
	case BodyEntry:
		return key, strings.Replace(orig, "total = 0\n", fmt.Sprintf("total = %d\n", 1+n%2), 1)
	}
	panic("projectgen: unknown edit kind " + string(kind))
}

const idsHub = `//# Identifiers, money and levels every module shares.

import std/json.ToJson

/// A record's identifier.
pub type Id Int

derive Equatable for Id
derive Hashable for Id
derive Comparable for Id

impl Display for Id {
    fn to_string(id: Id) -> String {
        Id(n) = id
        "#${n}"
    }
}

/// An amount of money in one currency.
pub struct Money {
    cents: Int
    currency: String = "EUR"
}

derive Equatable for Money
derive Hashable for Money
derive ToJson for Money

impl Money {
    /// Euros, from cents.
    pub fn of(cents: Int) -> Money {
        #Money{cents}
    }

    /// The sum of two amounts, in the first one's currency.
    pub fn add(a: Money, b: Money) -> Money {
        #Money{cents: a.cents + b.cents, currency: a.currency}
    }

    /// Whether the amount is zero.
    pub fn is_zero(m: Money) -> Bool {
        m.cents == 0
    }
}

impl Display for Money {
    fn to_string(m: Money) -> String {
        "${m.cents} ${m.currency}"
    }
}

/// How large something is.
pub enum Level {
    Low
    Mid
    High Int
}

derive Equatable for Level
derive Hashable for Level

impl Display for Level {
    fn to_string(level: Level) -> String {
        case level {
            .Low -> "low"
            .Mid -> "mid"
            .High(n) -> "high ${n}"
        }
    }
}

/// Something that can say what it is and how much it weighs.
pub interface Describe {
    fn describe(value: self) -> String
    fn weight(value: self) -> Int
}

/// The scale every weight is measured against.
pub once scale = 100
`

const utilHub = `//# Generic helpers every module uses.

import core/ids.{Level, Money}

/// A page of results and how many there were.
pub struct Page<T> {
    items: List<T>
    total: Int
}

/// The whole of items as one page.
pub fn page<T>(items: List<T>) -> Page<T> {
    #Page{items, total: Iter.count(items)}
}

/// The first item, or fallback when there is none.
pub fn first_or<T>(items: List<T>, fallback: T) -> T {
    case List.head(items) {
        Some(x) -> x
        None -> fallback
    }
}

/// The items for which keep holds, as a list.
pub fn keep<T>(items: List<T>, keep: (T) -> Bool) -> List<T> {
    items
    |> Iter.filter(keep)
    |> Iter.to_list()
}

/// n, moved into lo..=hi.
pub fn clamp(n: Int, lo: Int, hi: Int) -> Int {
    case {
        n < lo -> lo
        n > hi -> hi
        _ -> n
    }
}

/// The sum of xs.
pub fn total(xs: List<Int>) -> Int {
    Iter.reduce(xs, fn acc = 0, x -> acc + x)
}

/// The level n falls in.
pub fn level_of(n: Int) -> Level {
    case {
        n < 10 -> .Low
        n < 100 -> .Mid
        _ -> .High(n)
    }
}

/// The sum of amounts.
pub fn sum_money(amounts: List<Money>) -> Money {
    Iter.reduce(amounts, fn acc = Money.of(0), m -> Money.add(acc, m))
}

/// A place on a page, held as a record.
pub struct Spot {
    at: #{x: Int, y: Int}
}

/// The spot at n.
pub fn spot(n: Int) -> Spot {
    #Spot{at: #{x: n, y: n + 1}}
}

/// Where a spot is.
pub fn place(s: Spot) -> #{x: Int, y: Int} {
    s.at
}

/// The sum of any numbers.
pub fn sum_all(xs: Iter<Int>) -> Int {
    Iter.reduce(xs, fn acc = 0, x -> acc + x)
}

/// What a change to the util hub changes.
pub fn probe(n: Int) -> Int {
    n + 1
}

/// The ratio the hub settles on.
pub once ratio = clamp(probe(41), 0, 100)
`

const textHub = `//# Text helpers.

import core/util

/// words joined by sep, each trimmed.
pub fn joined(words: List<String>, sep: String) -> String {
    words
    |> Iter.map(String.trim)
    |> String.join(sep)
}

/// s repeated n times, at most ten.
pub fn repeated(s: String, n: Int) -> String {
    1..=util.clamp(n, 0, 10)
    |> Iter.map(fn _ -> s)
    |> String.join()
}

/// How many characters the words hold together.
pub fn width(words: List<String>) -> Int {
    words
    |> Iter.map(String.length)
    |> Iter.to_list()
    |> util.total()
}
`

// renderUnit writes u's file and answers its imports of project files.
func renderUnit(u *unit) (string, []string) {
	var b strings.Builder
	i := u.i
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	imports := []string{"core/ids"}
	w("//# Unit %d of the generated project.\n\n", i)
	w("import {\n")
	if !u.light {
		w("    std/json.ToJson\n")
	}
	w("    core/ids.{Describe, Id, Money}\n")
	if u.usesText {
		w("    core/text\n")
		imports = append(imports, "core/text")
	}
	if u.usesUtil {
		w("    core/util\n")
		imports = append(imports, "core/util")
	}
	// Each imported unit, as a file (self) when u reads it through its
	// qualifier, and with the cluster's Rank interface when it is the head.
	mods := map[string][]string{}
	for _, d := range u.deps {
		mods[d.key] = []string{"self"}
	}
	if u.back != nil {
		mods[u.back.key] = []string{"self"}
	}
	if u.head != u.key {
		mods[u.head] = append(mods[u.head], fmt.Sprintf("Rank%d", u.headIndex))
	}
	keys := make([]string, 0, len(mods))
	for m := range mods {
		keys = append(keys, m)
	}
	sort.Strings(keys)
	for _, m := range keys {
		switch items := mods[m]; {
		case len(items) == 1 && items[0] == "self":
			w("    %s\n", m)
		default:
			w("    %s.{%s}\n", m, strings.Join(items, ", "))
		}
		imports = append(imports, m)
	}
	w("}\n\n")

	if u.head == u.key {
		w("/// Something with a rank, implemented across cluster %d.\n", i)
		w("pub interface Rank%d {\n    fn rank(value: self) -> Int\n}\n\n", i)
	}

	w(`/// A record of unit %[1]d.
pub struct Rec%[1]d {
    id: Id
    name: String
    amount: Money
    tags: List<String> = []
}

derive Equatable for Rec%[1]d
derive Hashable for Rec%[1]d
derive Display for Rec%[1]d

/// The kinds unit %[1]d tells apart.
pub enum Kind%[1]d {
    Plain
    Sized Int
    Tagged String
}

derive Equatable for Kind%[1]d
derive Hashable for Kind%[1]d

/// A score, kept apart from other numbers.
pub type Score%[1]d Int

derive Equatable for Score%[1]d
derive Hashable for Score%[1]d
derive Comparable for Score%[1]d

/// An entry of unit %[1]d: a kind and its score.
pub struct Entry%[1]d {
    kind: Kind%[1]d
    score: Score%[1]d
    label: String
}

derive Equatable for Entry%[1]d
derive Hashable for Entry%[1]d

`, i)
	if !u.light {
		w(`/// A summary of unit %[1]d's records.
pub struct Summary%[1]d {
    name: String
    count: Int
}

derive Equatable for Summary%[1]d
derive Display for Summary%[1]d
derive ToJson for Summary%[1]d

/// Two values of any types.
pub struct Pair%[1]d<A, B> {
    left: A
    right: B
}

/// The pair the other way round.
pub fn swap%[1]d<A, B>(pair: Pair%[1]d<A, B>) -> Pair%[1]d<B, A> {
    #Pair%[1]d{left: pair.right, right: pair.left}
}

/// The item at k, if there is one.
pub fn pick%[1]d<T>(items: List<T>, k: Int) -> Maybe<T> {
    items
    |> Iter.drop(k)
    |> Iter.first()
}

`, i)
	}

	backRead := ""
	if u.back != nil {
		backRead = fmt.Sprintf(" + %s.base%d", u.back.name, u.back.i)
	}
	w(`impl Describe for Rec%[1]d {
    fn describe(value: Rec%[1]d) -> String {
        "${value.id} ${value.name}"
    }

    fn weight(value: Rec%[1]d) -> Int {
        value.amount.cents + base%[1]d%[2]s
    }
}

impl Rank%[3]d for Rec%[1]d {
    fn rank(value: Rec%[1]d) -> Int {
        Iter.count(value.tags) + %[1]d
    }
}

`, i, backRead, u.headIndex)

	if u.usesUtil {
		w("/// The unit's base value, read by the files that import it.\npub once base%d = util.clamp(%d, 0, 1000)\n\n", i, (i*37)%1500)
	} else {
		w("/// The unit's base value, read by the files that import it.\npub once base%d = %d * 3 + 1\n\n", i, i%500)
	}

	w(`impl Rec%[1]d {
    /// A record numbered n.
    pub fn new(n: Int) -> Rec%[1]d {
        #Rec%[1]d{id: Id(n), name: "r${n}", amount: Money.of(n * 10), tags: ["a", "bb"]}
    }

    /// The record's score: its cents and the length of its tags.
    pub fn score(r: Rec%[1]d) -> Int {
        r.tags
        |> Iter.map(String.length)
        |> Iter.filter(fn n -> n > 0)
        |> Iter.reduce(fn acc = r.amount.cents, n -> acc + n)
    }
}

/// The kind n stands for.
pub fn kind%[1]d(n: Int) -> Kind%[1]d {
    case n %% 3 {
        0 -> .Plain
        1 -> .Sized(n)
        _ -> .Tagged("t${n}")
    }
}

`, i)
	if !u.light {
		w(`/// n doubled, or why not.
pub fn checked%[1]d(n: Int) -> Result<Int, String> {
    if n < 0 { Err("negative: ${n}") } else { Ok(n * 2) }
}

/// n doubled twice over.
pub fn doubled%[1]d(n: Int) -> Result<Int, String> {
    a = checked%[1]d(n)?
    b = checked%[1]d(a)?
    Ok(a + b)
}

`, i)
	}
	// Types that refer to each other across the import cycle between u and
	// u.back: u's Link holds back's Nodes and a Node's parent is a Link.
	if u.back != nil && u.back.hasDep(u) {
		w(`/// A link of unit %[1]d, whose nodes unit %[2]d declares.
pub struct Link%[1]d {
    label: String
    nodes: List<%[3]s.Node%[2]d>
}

derive Equatable for Link%[1]d

`, i, u.back.i, u.back.name)
	}
	if front := u.cycleFront(); front != nil {
		w(`/// A node of unit %[1]d, whose parent unit %[2]d declares.
pub struct Node%[1]d {
    rec: Rec%[1]d
    parent: Maybe<%[3]s.Link%[2]d>
}

derive Equatable for Node%[1]d

/// A link of unit %[2]d over nodes of this unit, read back.
pub fn linked%[1]d(n: Int) -> Int {
    leaf = #Node%[1]d{rec: Rec%[1]d.new(n), parent: None}
    link = #%[3]s.Link%[2]d{label: "l${n}", nodes: [leaf, leaf]}
    top = #Node%[1]d{rec: Rec%[1]d.new(n + 1), parent: Some(link)}
    same = top == leaf
    case top.parent {
        Some(l) -> if same { 0 } else { Iter.count(l.nodes) + String.length(l.label) }
        None -> 1
    }
}

`, i, front.i, front.name)
	}
	w(`/// What a change to this unit changes.
pub fn probe%[1]d(n: Int) -> Int {
    n + 1
}

`, i)

	for k := 0; k < u.fns; k++ {
		w("%s", renderFn(u, k, rand.New(rand.NewSource(int64(u.shapeSeeds[k])))))
	}
	return strings.TrimRight(b.String(), "\n") + "\n", imports
}

// renderFn writes u's function f<i>_<k>, from(n: Int): Int. f<i>_0 calls
// the units u depends on; every later one may call the one before it, so
// the call graph has no cycle even where the imports do.
func renderFn(u *unit, k int, r *rand.Rand) string {
	i := u.i
	c := 2 + r.Intn(9)
	var body string
	prev := fmt.Sprintf("probe%d(n)", i)
	if k > 0 {
		prev = fmt.Sprintf("f%d_%d(n %% %d)", i, k-1, c+3)
	}
	shape := r.Intn(10)
	if u.light && (shape == 3 || shape == 4 || shape == 7 || shape == 9) {
		// A light unit has no Pair, Summary, pick or doubled.
		shape = []int{0, 1, 5, 2}[shape%4]
	}
	switch shape {
	case 0:
		body = fmt.Sprintf(`    kind = kind%[1]d(n + %[2]d)
    case kind {
        .Plain -> n + base%[1]d
        .Sized(s) -> s * 2 + %[3]s
        .Tagged(t) -> String.length(t)
    }`, i, c, prev)
	case 1:
		body = fmt.Sprintf(`    start = %[3]s
    m = n %% %[2]d + 1
    1..=m
    |> Iter.map(fn x -> x * %[2]d)
    |> Iter.filter(fn x -> x %% 2 == 0)
    |> Iter.reduce(fn acc = start, x -> acc + x)`, i, c, prev)
	case 2:
		body = fmt.Sprintf(`    r = Rec%[1]d.new(n)
    s = Rec%[1]d.score(r)
    if r == Rec%[1]d.new(n + %[2]d) {
        s + String.length(Display.to_string(r)) + Hashable.hash(r) - Hashable.hash(Rec%[1]d.new(n + %[2]d))
    } else {
        s + Describe.weight(r) + Rank%[4]d.rank(r) + %[3]s
    }`, i, c, prev, u.headIndex)
	case 3:
		fallback := "(items |> Iter.first() |> Maybe.with_default(0))"
		if u.usesUtil {
			fallback = "util.first_or(items, 0)"
		}
		body = fmt.Sprintf(`    items = [n, n + %[2]d, n * 2, %[3]s]
    case pick%[1]d(items, n %% 3) {
        Some(x) -> x + %[4]s
        None -> 0
    }`, i, c, prev, fallback)
	case 4:
		body = fmt.Sprintf(`    case doubled%[1]d(n - %[2]d) {
        Ok(v) -> v + %[3]s
        Err(message) -> String.length(message)
    }`, i, c, prev)
	case 5:
		body = fmt.Sprintf(`    table = %%{"a" => n, "b" => n + %[2]d, "c" => %[3]s}
    Map.get(table, "b")
    |> Maybe.with_default(0)`, i, c, prev)
	case 6:
		if u.usesText {
			body = fmt.Sprintf(`    words = ["unit", "%[1]d", "${n}", " x "]
    text.width(words) + String.length(text.joined(words, "-")) + %[2]s`, i, prev)
		} else {
			body = fmt.Sprintf(`    words = ["unit", "%[1]d", "${n}"]
    words
    |> Iter.map(String.length)
    |> Iter.reduce(fn acc = %[2]s, x -> acc + x)`, i, prev)
		}
	case 7:
		body = fmt.Sprintf(`    summary = #Summary%[1]d{name: "s${n}", count: n}
    shown = Display.to_string(summary)
    String.length(shown) + summary.count + %[2]s`, i, prev)
	case 8:
		body = fmt.Sprintf(`    a = Score%[1]d(n)
    b = Score%[1]d(n + %[2]d)
    entry = #Entry%[1]d{kind: kind%[1]d(n), score: a, label: "x"}
    same = entry == #Entry%[1]d{kind: kind%[1]d(n), score: b, label: "x"}
    case Comparable.compare(a, b) {
        .Less -> 1 + %[3]s
        .Equal -> if same { 0 } else { 3 }
        .Greater -> 2
    }`, i, c, prev)
	default:
		body = fmt.Sprintf(`    pair = swap%[1]d(#Pair%[1]d{left: n, right: "p%[2]d"})
    String.length(pair.left) + pair.right + %[3]s`, i, c, prev)
	}
	if k == 0 {
		// Only the first dependency is called, so that a call of f<i>_0
		// costs as many calls as the chain below it is long; the others
		// are read for their once values.
		calls := ""
		for j, d := range u.deps {
			if j == 0 {
				calls += fmt.Sprintf(" + %s.f%d_0(n %% %d)", d.name, d.i, c+5)
			}
			calls += fmt.Sprintf(" + %s.base%d", d.name, d.i)
		}
		if u.usesUtil {
			// A record and an Iter<Int> in another file's signatures and
			// fields, the Iter fed a list.
			calls += " + util.ratio + util.sum_all([n, 1]) + util.place(util.spot(n)).y"
		}
		if u.usesText {
			calls += " + text.width([\"${n}\"])"
		}
		if u.cycleFront() != nil {
			calls += fmt.Sprintf(" + linked%d(n)", i)
		}
		if calls != "" {
			return fmt.Sprintf("/// Step 0 of unit %d.\npub fn f%d_0(n: Int) -> Int {\n    value = {\n%s\n    }\n    value%s\n}\n\n",
				i, i, indent(body), calls)
		}
	}
	return fmt.Sprintf("/// Step %d of unit %d.\npub fn f%d_%d(n: Int) -> Int {\n%s\n}\n\n", k, i, i, k, body)
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// renderMain writes the entry, which imports every root unit.
func renderMain(roots []*unit) (string, []string) {
	var b strings.Builder
	b.WriteString("//# The generated project's entry: it runs every unit no other unit imports.\n\nimport {\n    std/io\n")
	var imports []string
	sorted := append([]*unit(nil), roots...)
	sort.Slice(sorted, func(a, c int) bool { return sorted[a].key < sorted[c].key })
	for _, u := range sorted {
		fmt.Fprintf(&b, "    %s\n", u.key)
		imports = append(imports, u.key)
	}
	b.WriteString("}\n\nfn main() {\n    total = 0\n")
	for _, u := range roots {
		fmt.Fprintf(&b, "    total = total + %s.f%d_%d(%d)\n", u.name, u.i, u.fns-1, 3+u.i%7)
	}
	b.WriteString("    io.print(\"checksum ${total}\")\n}\n")
	return b.String(), imports
}

// renderTest writes the test file beside u.
func renderTest(u *unit) string {
	doubling := ""
	if !u.light {
		doubling = fmt.Sprintf(`
    test "doubling" {
        assert %[1]s.doubled%[2]d(2) == Ok(12)
        assert Err(_) = %[1]s.doubled%[2]d(-1)
    }
`, u.name, u.i)
	}
	return fmt.Sprintf(`import %[1]s.{self, Entry%[2]d, Kind%[2]d, Rec%[2]d, Score%[2]d}

group "unit %[2]d" {
    test "steps give the same answer twice" {
        subtests [0, 1, 2, 5, 9], n {
            assert %[3]s.f%[2]d_%[4]d(n) == %[3]s.f%[2]d_%[4]d(n)
        }
    }

    test "records compare by value" {
        entry = #Entry%[2]d{kind: Kind%[2]d.Sized(3), score: Score%[2]d(3), label: "e"}
        assert entry == #Entry%[2]d{kind: .Sized(3), score: Score%[2]d(3), label: "e"}
        refute entry == #Entry%[2]d{kind: .Plain, score: Score%[2]d(3), label: "e"}
        assert Hashable.hash(entry) == Hashable.hash(entry)
    }

    test "records derive over another file's types" {
        assert Rec%[2]d.new(2) == Rec%[2]d.new(2)
        refute Rec%[2]d.new(2) == Rec%[2]d.new(3)
        assert Hashable.hash(Rec%[2]d.new(2)) == Hashable.hash(Rec%[2]d.new(2))
        assert Display.to_string(Rec%[2]d.new(2)) == "#Rec%[2]d{id: #2, name: r2, amount: 20 EUR, tags: [a, bb]}"
    }

    test "kinds" {
        assert %[3]s.kind%[2]d(0) == Kind%[2]d.Plain
        assert %[3]s.kind%[2]d(4) == Kind%[2]d.Sized(4)
    }

    test "scores order" {
        assert Comparable.compare(Score%[2]d(1), Score%[2]d(2)) == .Less
    }
%[5]s}
`, u.key, u.i, u.name, u.fns-1, doubling)
}
