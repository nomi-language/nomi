package main

// `nomi effects --tree` prints, for each root the report counts, the
// calls from the root down to each effect, as a tree of the branches that
// lead to one, or with --format mermaid as a Mermaid flowchart. The
// tree is vmhost's (Reach.Trees, from the edges the reach walk recorded);
// this file only lays it out.

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// treeRoots is the trees whose roots are not test cases, and the test
// cases' trees by their file, with the files in the order they come.
func treeRoots(trees []vmhost.CallTree) (roots []vmhost.CallTree, files []string, tests map[string][]vmhost.CallTree) {
	tests = map[string][]vmhost.CallTree{}
	for _, t := range trees {
		if !t.Tests {
			roots = append(roots, t)
			continue
		}
		if _, seen := tests[t.File]; !seen {
			files = append(files, t.File)
		}
		tests[t.File] = append(tests[t.File], t)
	}
	return roots, files, tests
}

// reaches reports whether a tree's root leads to an effect.
func reaches(f *vmhost.TreeFunc) bool { return len(f.Calls) > 0 || len(f.Next) > 0 }

// reachedNames is each effect the calls under f have, sorted, added to
// names.
func reachedNames(f *vmhost.TreeFunc, seen map[*vmhost.TreeFunc]bool, names []string) []string {
	if seen[f] {
		return names
	}
	seen[f] = true
	for _, c := range f.Calls {
		if !slices.Contains(names, c.Name) {
			names = append(names, c.Name)
		}
	}
	for _, s := range f.Next {
		names = reachedNames(s.Func, seen, names)
	}
	slices.Sort(names)
	return names
}

// testsLine is the one line a test file's cases print as when they are not
// expanded: how many there are, and how many of them have which effects.
func testsLine(file string, cases []vmhost.CallTree) string {
	var names []string
	reaching := 0
	seen := map[*vmhost.TreeFunc]bool{}
	for _, t := range cases {
		if reaches(t.Root) {
			reaching++
			names = reachedNames(t.Root, seen, names)
		}
	}
	line := fmt.Sprintf("%s: %s", file, count(len(cases), "test", "tests"))
	if reaching == 0 {
		return line + ", none of which have effects"
	}
	verb := "have"
	if reaching == 1 {
		verb = "has"
	}
	return fmt.Sprintf("%s, %d of which %s %s", line, reaching, verb, effects.EffectList(names))
}

// count is "1 test" or "n tests".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// pureLine is the line that stands for n functions with no effects.
func pureLine(n int) string {
	if n == 1 {
		return "1 function with no effects"
	}
	return fmt.Sprintf("%d functions with no effects", n)
}

// rootLabel is a root as a tree heads it: its name, a test case as
// `test "name"`, a boot with what it is.
func rootLabel(t vmhost.CallTree) string {
	if t.Edge == vm.EdgeTest {
		return fmt.Sprintf("test %q", t.Name)
	}
	if note := vm.StepNote(t.Edge); note != "" {
		return t.Name + " (" + note + ")"
	}
	return t.Name
}

// funcLabel is a function on a tree: its name, with the dependency that
// declares it and how the function above reached it when that is not a
// plain call (vm.StepNote).
func funcLabel(s vmhost.TreeStep) string {
	var notes []string
	if s.Func.Module != "" {
		notes = append(notes, s.Func.Module)
	}
	if note := vm.StepNote(s.Edge); note != "" {
		notes = append(notes, note)
	}
	if len(notes) == 0 {
		return s.Func.Name
	}
	return s.Func.Name + " (" + strings.Join(notes, ", ") + ")"
}

// callLabel is an effect call on a tree: what the code calls, and how
// when it is not a plain call.
func callLabel(c vmhost.TreeCall) string {
	if note := vm.StepNote(c.Edge); note != "" {
		return c.Callee + " (" + note + ")"
	}
	return c.Callee
}

// callName is the effect a call has, as an effects list names it, and
// the list that refuses it, or that no list needs an entry for it (a
// `dbg`).
func callName(c vmhost.TreeCall) string {
	switch {
	case c.Refused != "":
		return c.Name + "  refused: " + c.Refused
	case !effects.Listed(c.Name):
		return c.Name + "  needs no entry"
	}
	return c.Name
}

// treeLine is one line of a tree: the tree on the left, and on a line
// that ends in an effect, its name on the right, in a column.
type treeLine struct{ left, right string }

// treeWriter lays out trees, expanding each function once (seen, by its
// key) and referring to it as "(above)" after.
type treeWriter struct {
	seen  map[string]bool
	lines []treeLine
}

// branch adds the lines under f, each prefixed by prefix.
func (tw *treeWriter) branch(f *vmhost.TreeFunc, prefix string) {
	type item struct {
		call *vmhost.TreeCall
		step *vmhost.TreeStep
		pure int
	}
	var items []item
	for i := range f.Calls {
		items = append(items, item{call: &f.Calls[i]})
	}
	for i := range f.Next {
		items = append(items, item{step: &f.Next[i]})
	}
	if f.Pure > 0 {
		items = append(items, item{pure: f.Pure})
	}
	for i, it := range items {
		conn, below := "├─ ", "│  "
		if i == len(items)-1 {
			conn, below = "└─ ", "   "
		}
		switch {
		case it.call != nil:
			tw.lines = append(tw.lines, treeLine{prefix + conn + callLabel(*it.call), callName(*it.call)})
		case it.step == nil:
			tw.lines = append(tw.lines, treeLine{left: prefix + conn + pureLine(it.pure)})
		default:
			tw.chain(*it.step, prefix+conn, prefix+below)
		}
	}
}

// chain adds the line of s, joining with arrows each function after it
// that leads on to a single thing, then the lines under the last.
func (tw *treeWriter) chain(s vmhost.TreeStep, head, prefix string) {
	label := funcLabel(s)
	for {
		f := s.Func
		if tw.seen[f.Key] {
			tw.lines = append(tw.lines, treeLine{left: head + label + " (above)"})
			return
		}
		tw.seen[f.Key] = true
		switch {
		case len(f.Calls) == 1 && len(f.Next) == 0 && f.Pure == 0:
			tw.lines = append(tw.lines, treeLine{head + label + " → " + callLabel(f.Calls[0]), callName(f.Calls[0])})
			return
		case len(f.Calls) == 0 && len(f.Next) == 1 && f.Pure == 0:
			s = f.Next[0]
			label += " → " + funcLabel(s)
		default:
			tw.lines = append(tw.lines, treeLine{left: head + label})
			tw.branch(f, prefix)
			return
		}
	}
}

// root adds the lines of a tree: its root, then the branches under it.
func (tw *treeWriter) root(t vmhost.CallTree) {
	tw.seen[t.Root.Key] = true
	if !reaches(t.Root) {
		tw.lines = append(tw.lines, treeLine{left: rootLabel(t) + ": has no effects"})
		return
	}
	tw.lines = append(tw.lines, treeLine{left: rootLabel(t)})
	tw.branch(t.Root, "")
}

// flush prints the lines added since the last flush, the right column
// three spaces past the widest left part that has one.
func (tw *treeWriter) flush(w io.Writer) {
	width := 0
	for _, l := range tw.lines {
		if l.right != "" {
			width = max(width, len([]rune(l.left)))
		}
	}
	for _, l := range tw.lines {
		if l.right == "" {
			fmt.Fprintln(w, l.left)
			continue
		}
		fmt.Fprintf(w, "%s%s   %s\n", l.left, strings.Repeat(" ", width-len([]rune(l.left))), l.right)
	}
	tw.lines = nil
}

// writeTree prints trees as `nomi effects --tree` does: each root
// that is not a test case with its tree, then each test file's cases, one
// line a file unless showTests, which prints the tree of each case that
// has effects and counts the rest.
func writeTree(w io.Writer, trees []vmhost.CallTree, showTests bool) {
	roots, files, tests := treeRoots(trees)
	tw := &treeWriter{seen: map[string]bool{}}
	for _, t := range roots {
		tw.root(t)
		tw.flush(w)
	}
	for _, file := range files {
		cases := tests[file]
		if !showTests {
			fmt.Fprintln(w, testsLine(file, cases))
			continue
		}
		fmt.Fprintf(w, "%s: %s\n", file, count(len(cases), "test", "tests"))
		quiet := 0
		for _, t := range cases {
			if !reaches(t.Root) {
				quiet++
				continue
			}
			tw.root(t)
			tw.flush(w)
		}
		switch {
		case quiet == 1:
			fmt.Fprintln(w, "and 1 test with no effects")
		case quiet > 1:
			fmt.Fprintf(w, "and %d tests with no effects\n", quiet)
		}
	}
	if !showTests && len(files) > 0 {
		fmt.Fprintln(w, "--tests prints the tree of each test")
	}
}

// writeMermaid prints trees as a Mermaid flowchart: each function once as a
// node, an edge to each function and effect call it reaches, labelled
// when it is not a plain call, the effect calls as leaves grouped by what
// an effects list names them, a refused one marked, and the functions a
// function reaches that have no effects as one node under it. Test cases
// are expanded with showTests, and otherwise one node a test file, with an
// edge to each effect its cases have.
func writeMermaid(w io.Writer, trees []vmhost.CallTree, showTests bool) {
	roots, files, tests := treeRoots(trees)
	var nodes, edges []string
	ids := map[string]string{}
	label := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, "#quot;") + `"` }
	arrow := func(from, to string, kind vm.EdgeKind) {
		if note := vm.StepNote(kind); note != "" {
			edges = append(edges, fmt.Sprintf("  %s -- %s --> %s", from, label(note), to))
			return
		}
		edges = append(edges, fmt.Sprintf("  %s --> %s", from, to))
	}
	// Leaves, by name, then callee and standing.
	type leaf struct{ callee, refused string }
	var names []string
	leaves := map[string][]leaf{}
	leafID := map[[3]string]string{}
	groupID := map[string]string{}
	group := func(name string) string {
		if id, ok := groupID[name]; ok {
			return id
		}
		id := fmt.Sprintf("m%d", len(groupID)+1)
		groupID[name] = id
		names = append(names, name)
		return id
	}
	call := func(c vmhost.TreeCall) string {
		group(c.Name)
		k := [3]string{c.Name, c.Callee, c.Refused}
		if id, ok := leafID[k]; ok {
			return id
		}
		id := fmt.Sprintf("c%d", len(leafID)+1)
		leafID[k] = id
		leaves[c.Name] = append(leaves[c.Name], leaf{c.Callee, c.Refused})
		return id
	}
	pure := 0
	var visit func(f *vmhost.TreeFunc, name string) string
	visit = func(f *vmhost.TreeFunc, name string) string {
		if id, ok := ids[f.Key]; ok {
			return id
		}
		id := fmt.Sprintf("f%d", len(ids)+1)
		ids[f.Key] = id
		nodes = append(nodes, fmt.Sprintf("  %s[%s]", id, label(name)))
		for _, c := range f.Calls {
			arrow(id, call(c), c.Edge)
		}
		for _, s := range f.Next {
			arrow(id, visit(s.Func, funcLabel(vmhost.TreeStep{Func: s.Func})), s.Edge)
		}
		if f.Pure > 0 {
			pure++
			pid := fmt.Sprintf("p%d", pure)
			nodes = append(nodes, fmt.Sprintf("  %s[%s]", pid, label(pureLine(f.Pure))))
			edges = append(edges, fmt.Sprintf("  %s -.-> %s", id, pid))
		}
		return id
	}
	for _, t := range roots {
		visit(t.Root, rootLabel(t))
	}
	for i, file := range files {
		if showTests {
			for _, t := range tests[file] {
				if reaches(t.Root) {
					visit(t.Root, rootLabel(t))
				}
			}
			continue
		}
		id := fmt.Sprintf("t%d", i+1)
		nodes = append(nodes, fmt.Sprintf("  %s[%s]", id, label(fmt.Sprintf("%s: %s", file, count(len(tests[file]), "test", "tests")))))
		var reached []string
		seen := map[*vmhost.TreeFunc]bool{}
		for _, t := range tests[file] {
			reached = reachedNames(t.Root, seen, reached)
		}
		for _, name := range reached {
			edges = append(edges, fmt.Sprintf("  %s --> %s", id, group(name)))
		}
	}
	fmt.Fprintln(w, "flowchart LR")
	for _, n := range nodes {
		fmt.Fprintln(w, n)
	}
	var refused []string
	for _, name := range names {
		fmt.Fprintf(w, "  subgraph %s[%s]\n", groupID[name], label(name))
		for _, l := range leaves[name] {
			id := leafID[[3]string{name, l.callee, l.refused}]
			text := l.callee
			if l.refused != "" {
				text += " (refused: " + l.refused + ")"
				refused = append(refused, id)
			}
			fmt.Fprintf(w, "    %s[%s]\n", id, label(text))
		}
		fmt.Fprintln(w, "  end")
	}
	for _, e := range edges {
		fmt.Fprintln(w, e)
	}
	if len(refused) > 0 {
		fmt.Fprintln(w, "  classDef refused stroke:#d33,stroke-width:2px")
		fmt.Fprintf(w, "  class %s refused\n", strings.Join(refused, ","))
	}
}
