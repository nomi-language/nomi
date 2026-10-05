package main

import (
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/expectation"
	nomiformat "github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// TestTourSidebarListsEveryGeneratedReferencePage pins the one hand-maintained
// list in the docs pipeline against the one thing that generates its targets.
//
// docgen writes <out>/<module>.md for every non-skipped stdlib module with at
// least one public declaration, and never prunes, while the tour's navigation
// is a literal array of module names in tour/astro.config.mjs. Without this
// test the list drifts silently, leaving generated pages no navigation entry
// reaches.
//
// The check runs in both directions
// because the two failures are different and both are real:
//
//   - a generated page with no sidebar entry is an orphan: reachable only by
//     typing the URL, so a new stdlib module is invisible by default. This
//     direction drifts on every module added.
//   - a sidebar entry with no generated page is a dead link: Starlight
//     validates internal links at build time, so this one already fails
//     `npm run build`, but it fails there in a browser-shaped error message
//     long after the edit, and it fails here in Go, in the package that owns
//     the page set.
func TestTourSidebarListsEveryGeneratedReferencePage(t *testing.T) {
	generated := generatedReferenceModules(t)
	sidebar := tourSidebarReferenceModules(t)

	inSidebar := make(map[string]bool, len(sidebar))
	for _, name := range sidebar {
		inSidebar[name] = true
	}
	isGenerated := make(map[string]bool, len(generated))
	for _, name := range generated {
		isGenerated[name] = true
	}

	var orphans, dead []string
	for _, name := range generated {
		if !inSidebar[name] {
			orphans = append(orphans, name)
		}
	}
	for _, name := range sidebar {
		if !isGenerated[name] {
			dead = append(dead, name)
		}
	}
	if len(orphans) > 0 {
		t.Errorf("docgen emits reference/%s.md but %s has no sidebar entry for it: "+
			"the page is an unreachable orphan. Add the module name to referenceItems.",
			strings.Join(orphans, ".md, reference/"), tourConfigPath)
	}
	if len(dead) > 0 {
		t.Errorf("%s lists std/%s in referenceItems but docgen emits no page for it: "+
			"the sidebar link is dead. Remove the entry, or check whether the module "+
			"lost its public declarations or joined docgen's skip set.",
			tourConfigPath, strings.Join(dead, ", std/"))
	}
}

const tourConfigPath = "../../tour/astro.config.mjs"

// generatedReferenceModules answers, by construction rather than by reading a
// directory, which pages `nomi-docgen <dir>` would write: main's own loop,
// with main's own single exclusion — a module whose render is empty because it
// declares nothing public of its own.
func generatedReferenceModules(t *testing.T) []string {
	t.Helper()
	lib := std.Load()
	candidates := make([]string, 0, len(lib.Modules))
	for name := range lib.Modules {
		candidates = append(candidates, name)
	}
	sort.Strings(candidates)
	links := referenceLinksForModules(lib, candidates)
	var written []string
	for _, name := range candidates {
		if renderModuleWithLinks(lib, name, links) == "" {
			continue
		}
		written = append(written, name)
	}
	if len(written) == 0 {
		t.Fatal("no stdlib module rendered a reference page")
	}
	return written
}

// TestEveryDocumentedInterfaceMethodReachesItsPage pins the doc comment on a
// method inside an interface body: the parser keeps it and `nomi fmt -w`
// re-emits it, and every consumer must carry it through.
//
// Three places copy it: the two Symbol constructions in
// analysis/builder.go, both *ast.InterfaceMethod arms in
// internal/hoverdoc, and lsp/signature_help.go's arm, beside its FuncDef and
// ExternFunc siblings that return n.Doc. If any of them drops it, the
// reference page and the editor hover show a bare signature.
//
// The population is derived from the tree, not listed, so a newly documented
// interface method is covered the day it is written. It Fatals on an empty
// population, because "no documented interface methods" would make every
// assertion below vacuous and is exactly what a regression here looks like.
func TestEveryDocumentedInterfaceMethodReachesItsPage(t *testing.T) {
	lib := std.Load()
	names := make([]string, 0, len(lib.Modules))
	for name := range lib.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	links := referenceLinksForModules(lib, names)

	documented := 0
	for _, module := range names {
		var methods []ast.InterfaceMethod
		for _, node := range lib.Nodes[module] {
			iface, ok := node.(*ast.InterfaceDef)
			if !ok || !iface.Public {
				continue
			}
			for i := range iface.Methods {
				if strings.TrimSpace(iface.Methods[i].Doc) != "" {
					methods = append(methods, iface.Methods[i])
				}
			}
		}
		if len(methods) == 0 {
			continue
		}
		page := renderModuleWithLinks(lib, module, links)
		if page == "" {
			t.Errorf("std/%s declares %d documented interface method(s) and renders no reference page at all",
				module, len(methods))
			continue
		}
		for _, m := range methods {
			documented++
			// The first line is enough and is the least brittle
			// anchor: the renderer reflows nothing, but a doc's
			// later lines may carry fenced examples that the page
			// wraps.
			first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(m.Doc), "\n", 2)[0])
			if !strings.Contains(page, first) {
				t.Errorf(`std/%s.%s has a /// doc that does not reach its reference page.

  missing line: %q

Its text is in the source and `+"`nomi fmt -w`"+` keeps it, so the loss is in a
consumer: analysis/builder.go's Symbol construction, internal/hoverdoc's
*ast.InterfaceMethod arms, or lsp/signature_help.go. Each of those dropped it
once.`, module, m.Name, first)
			}
		}
	}
	if documented == 0 {
		t.Fatal("no documented interface method found anywhere in std, so this test asserted nothing — " +
			"either the /// comments were deleted or the AST stopped carrying InterfaceMethod.Doc")
	}
	t.Logf("%d documented interface methods in std, all reaching their reference page", documented)
}

var sidebarSpread = regexp.MustCompile(`(?s)const referenceItems = \[.*?\.\.\.\[(.*?)\]\.map\(`)
var sidebarName = regexp.MustCompile(`'([^']+)'`)

// tourSidebarReferenceModules extracts the module names from astro.config.mjs's
// `referenceItems` spread. It fails rather than returning nothing when the
// shape changes, because an empty answer would make the whole test vacuous.
func tourSidebarReferenceModules(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(tourConfigPath))
	if err != nil {
		t.Fatalf("read %s: %v", tourConfigPath, err)
	}
	m := sidebarSpread.FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s: could not find the referenceItems module spread "+
			"(`const referenceItems = [ ... ...[ 'name', ... ].map(`). "+
			"If the sidebar was restructured, update this test with it — it is the "+
			"only thing keeping the list from rotting.", tourConfigPath)
	}
	var names []string
	for _, hit := range sidebarName.FindAllSubmatch(m[1], -1) {
		names = append(names, string(hit[1]))
	}
	if len(names) == 0 {
		t.Fatalf("%s: referenceItems spread has no module names", tourConfigPath)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("%s: referenceItems is not in alphabetical order; got %v",
			tourConfigPath, names)
	}
	return names
}

func TestRenderModuleIncludesPublicNamespaceAndTypeBodyAPIs(t *testing.T) {
	lib := std.Load()
	names := make([]string, 0, len(lib.Files))
	for name := range lib.Files {
		names = append(names, name)
	}
	links := referenceLinksForModules(lib, names)

	iter := renderModuleWithLinks(lib, "iter", nil)
	for _, want := range []string{
		"## Exports",
		`## <span class="nomi-ref-detail">interface </span><span class="nomi-ref-name">Iter</span>`,
		`### <span class="nomi-ref-owner">Iter.</span><span class="nomi-ref-name">reduce</span>`,
		`### <span class="nomi-ref-owner">Iter.</span><span class="nomi-ref-name">map</span>`,
		`### <span class="nomi-ref-owner">Iter.</span><span class="nomi-ref-name">chunks</span>`,
	} {
		if !strings.Contains(iter, want) {
			t.Fatalf("std/iter reference missing %q\n%s", want, iter)
		}
	}
	if !strings.Contains(iter, `<section><p>Interfaces</p><ul><li><a href="#interface-iter"><code>Iter</code></a></li></ul></section>`) {
		t.Fatalf("std/iter reference should list Iter in exports\n%s", iter)
	}
	if strings.Contains(iter, "ChunkByIter") {
		t.Fatalf("std/iter reference leaked private iterator implementation\n%s", iter)
	}

	io := renderModuleWithLinks(lib, "io", nil)
	for _, want := range []string{
		`## <span class="nomi-ref-owner">io.</span><span class="nomi-ref-name">print</span>`,
		`## <span class="nomi-ref-owner">io.</span><span class="nomi-ref-name">inspect</span>`,
		`## <span class="nomi-ref-owner">io.</span><span class="nomi-ref-name">read_line</span>`,
	} {
		if !strings.Contains(io, want) {
			t.Fatalf("std/io reference missing %q\n%s", want, io)
		}
	}

	intPage := renderModuleWithLinks(lib, "int", links)
	for _, want := range []string{
		`## <span class="nomi-ref-detail">type </span><span class="nomi-ref-name">Int</span>`,
		`### <span class="nomi-ref-owner">Int.</span><span class="nomi-ref-name">to_string</span>`,
		"*impl* [`Display.to_string`](/reference/display/#displayto_string)",
		`### <span class="nomi-ref-owner">Int.</span><span class="nomi-ref-name">to_non_zero</span>`,
		`### <span class="nomi-ref-owner">Int.</span><span class="nomi-ref-name">divide</span>`,
	} {
		if !strings.Contains(intPage, want) {
			t.Fatalf("std/int reference missing %q\n%s", want, intPage)
		}
	}
	if strings.Contains(intPage, "## impl Display for Int") {
		t.Fatalf("std/int reference should not render impl groups as headings\n%s", intPage)
	}
	if strings.Contains(intPage, "[`Display.to_string`](#interface-display)") {
		t.Fatalf("std/int reference should not link imported interface impls to missing same-page anchors\n%s", intPage)
	}

	stringsPage := renderModuleWithLinks(lib, "strings", links)
	for _, want := range []string{
		`### <span class="nomi-ref-owner">String.</span><span class="nomi-ref-name">contains?</span>`,
		`### <span class="nomi-ref-owner">String.</span><span class="nomi-ref-name">compare</span>`,
		`### <span class="nomi-ref-owner">String.</span><span class="nomi-ref-name">to_string</span>`,
	} {
		if !strings.Contains(stringsPage, want) {
			t.Fatalf("std/strings reference missing %q\n%s", want, stringsPage)
		}
	}
	for _, want := range []string{
		`<section><p>Types</p><ul><li><a href="#type-int"><code>Int</code></a></li>`,
		`<li><a href="#type-nonzeroint"><code>NonZeroInt</code></a></li>`,
	} {
		if !strings.Contains(intPage, want) {
			t.Fatalf("std/int reference missing export summary %q\n%s", want, intPage)
		}
	}

	calendar := renderModuleWithLinks(lib, "calendar", links)
	for _, want := range []string{
		`## <span class="nomi-ref-detail">interface </span><span class="nomi-ref-name">TimeParts</span>`,
		`## <span class="nomi-ref-detail">interface </span><span class="nomi-ref-name">Anchored</span>`,
		`## <span class="nomi-ref-detail">struct </span><span class="nomi-ref-name">Time</span>`,
		`### <span class="nomi-ref-owner">Time.</span><span class="nomi-ref-name">parse</span>`,
		`### <span class="nomi-ref-owner">Time.</span><span class="nomi-ref-name">hour</span>`,
		"*impl* [`Display.to_string`](/reference/display/#displayto_string)",
		"*impl* [`Debug.inspect`](/reference/debug/#debuginspect)",
		"*interface* `TimeParts`",
		"*interface* `Anchored`",
		"*impl* [`TimeParts.hour`](#timepartshour)",
	} {
		if !strings.Contains(calendar, want) {
			t.Fatalf("std/calendar reference missing %q\n%s", want, calendar)
		}
	}
}

type referenceTest struct {
	// Label names the editor on its page: the heading of the entry it sits
	// under, that entry's impl context when it has one, and `#n` for the
	// entry's second and later editors.
	Label   string
	Body    string
	Context string
}

func referenceTestBodies(page, module string) []referenceTest {
	open := fmt.Sprintf(`<pre data-nomi-stdlib-module=%q`, module)
	var tests []referenceTest
	seen := map[string]int{}
	offset := 0
	for {
		at := strings.Index(page[offset:], open)
		if at < 0 {
			return tests
		}
		entry := referenceEntryLabel(page[:offset+at])
		seen[entry]++
		label := entry
		if n := seen[entry]; n > 1 {
			label = fmt.Sprintf("%s #%d", entry, n)
		}
		start := offset + at + len(open)
		rest := page[start:]
		preEnd := strings.Index(rest, ">")
		if preEnd < 0 {
			return tests
		}
		attrs := rest[:preEnd]
		codeOpen := `<code class="language-nomi-test">`
		codeStart := strings.Index(rest[preEnd+1:], codeOpen)
		if codeStart < 0 {
			return tests
		}
		codeStart += preEnd + 1 + len(codeOpen)
		codeRest := rest[codeStart:]
		end := strings.Index(codeRest, "</code></pre>")
		if end < 0 {
			return tests
		}
		tests = append(tests, referenceTest{
			Label:   label,
			Body:    html.UnescapeString(codeRest[:end]),
			Context: referenceTestContextAttr(attrs),
		})
		offset = start + codeStart + end + len("</code></pre>")
	}
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// referenceEntryLabel names the API entry the page text before an editor ends
// in: its last `##`/`###` heading with the markup stripped (`Byte.from_int`,
// `type Bool`), plus the impl context line that directly follows that heading
// (`Date.add (impl Add<Duration> for Date)`), which is what tells one owner's
// several impls of one method apart.
func referenceEntryLabel(before string) string {
	lines := strings.Split(before, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		heading, ok := strings.CutPrefix(lines[i], "### ")
		if !ok {
			heading, ok = strings.CutPrefix(lines[i], "## ")
		}
		if !ok {
			continue
		}
		label := html.UnescapeString(htmlTag.ReplaceAllString(heading, ""))
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			if ctx, ok := strings.CutPrefix(next, `<p class="nomi-ref-context">`); ok {
				label += " (" + html.UnescapeString(strings.TrimSuffix(ctx, "</p>")) + ")"
			}
			break
		}
		return label
	}
	return "(no heading)"
}

func referenceTestContextAttr(attrs string) string {
	const key = `data-nomi-stdlib-context="`
	start := strings.Index(attrs, key)
	if start < 0 {
		return ""
	}
	start += len(key)
	rest := attrs[start:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return html.UnescapeString(rest[:end])
}

// TestReferenceInteractiveTestsOnTheVM runs every stdlib reference editor the
// way the tour's worker does (vmhost.StdlibReference) and checks the run
// against testdata/expectations/reference.expect. A reference editor is a
// `//!` prompt re-hosted as a `test` appended to its module; it must never
// fail or block.
//
// The golden file is what records which editors exist. The editors are not
// stdlib.expect's `//!` cases: a reference page renders only the attached
// tests of the public API entries it documents, so a prompt on a private
// function or on an `impl` block itself runs under `nomi test std` and has no
// editor, and an editor runs re-hosted with its page's context rather than in
// place. So a dropped, added, renamed or newly failing editor is a diff here,
// and adding a `//!` example needs only
//
//	NOMI_REGENERATE_EXPECTATIONS=1 go test ./cmd/nomi-docgen -run TestReferenceInteractiveTestsOnTheVM -count=1
func TestReferenceInteractiveTestsOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs every reference editor on the VM; -short")
	}
	lib := std.Load()
	names := make([]string, 0, len(lib.Files))
	for name := range lib.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	links := referenceLinksForModules(lib, names)
	got := &expectation.Set{
		Population: "reference",
		What: "one record per stdlib reference page carrying interactive tests, each editor " +
			"run as the tour's worker runs it (vmhost.StdlibReference). A `T` line is an " +
			"editor's outcome and its label: the entry heading, its impl context, and #n " +
			"for an entry's later editors. N is the page's editor count.",
	}
	bad := 0
	for _, name := range names {
		page := renderModuleWithLinks(lib, name, links)
		tests := referenceTestBodies(page, name)
		if len(tests) == 0 {
			continue
		}
		var lines []string
		exit := 0
		for _, test := range tests {
			outcome := "ok"
			cases, err := vmhost.StdlibReference(name, test.Body, test.Context, io.Discard)
			switch {
			case err != nil:
				outcome = "FAIL"
				t.Errorf("%s :: %s does not load:\n%s\n\n%v", name, test.Label, test.Body, err)
			case len(cases) != 1:
				outcome = "FAIL"
				t.Errorf("%s :: %s: %d cases selected, want 1", name, test.Label, len(cases))
			case cases[0].Blocked != nil:
				outcome = "BLOCKED"
				t.Errorf("%s :: %s is blocked on the VM: %s", name, test.Label, cases[0].Blocked[0])
			case cases[0].Err != nil:
				outcome = "FAIL"
				t.Errorf("%s :: %s failed on the VM:\n%s\n\n%v", name, test.Label, test.Body, cases[0].Err)
			}
			if outcome != "ok" {
				bad++
				exit = 1
			}
			lines = append(lines, outcome+" "+test.Label)
		}
		got.Add(expectation.NewCase(name, exit, len(tests), strings.Join(lines, "\n")))
	}
	got.Sort()
	checkOrRecordReference(t, got, bad)
}

// checkOrRecordReference writes got over the committed reference golden file
// under NOMI_REGENERATE_EXPECTATIONS=1, unless an editor failed or blocked, and
// otherwise requires the committed file to be byte-identical to it.
func checkOrRecordReference(t *testing.T, got *expectation.Set, bad int) {
	t.Helper()
	t.Logf("%s: %d pages, %d editors", got.Population, len(got.Cases), got.TotalCases())
	if expectation.RegenerateRequested() {
		if bad > 0 {
			t.Fatalf("%s: not rewriting the golden file over %d editor(s) that failed or blocked",
				got.Population, bad)
		}
		if err := expectation.Store(got); err != nil {
			t.Fatalf("storing %s: %v", got.Population, err)
		}
		t.Logf("%s: REWROTE the committed golden file because NOMI_REGENERATE_EXPECTATIONS=1. "+
			"Review the diff and name the reason for every moved record in the commit message.",
			got.Population)
		return
	}
	path, err := expectation.Path(got.Population)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the committed golden file for %s: %v", got.Population, err)
	}
	if string(committed) == string(got.Render()) {
		return
	}
	want, err := expectation.Parse(committed)
	if err != nil {
		t.Fatalf("the committed golden file for %s does not parse: %v", got.Population, err)
	}
	diffs := want.Compare(got)
	if len(diffs) == 0 {
		diffs = []string{"no record moved, but the rendered file differs from the committed " +
			"one (header, ordering or format); regenerate it"}
	}
	t.Errorf("%s: the reference editors do not match the committed golden file; %d difference(s):\n%s\n"+
		"If the change is intended, regenerate with NOMI_REGENERATE_EXPECTATIONS=1 and name the reason.",
		got.Population, len(diffs), strings.Join(diffs, "\n"))
}

// TestEveryImplOfOneMethodReachesItsPage pins the impls that the checker's
// TypeMethods table collapses. It keys methods by (owner, name), so an owner
// with several impls of one interface method kept only one there, and the
// page documented only that one: Instant showed one of its two `subtract`s,
// Date one of its four `add`s and one of its four `subtract`s, and the
// others' docs and `//!` editors never reached the page.
//
// The population is read from the AST: every `impl Add<…>`/`impl
// Subtract<…>` block for Instant and Date. Each must render under its own
// heading, labelled by its impl header, carrying its own doc and editors.
func TestEveryImplOfOneMethodReachesItsPage(t *testing.T) {
	lib := std.Load()
	for _, tc := range []struct {
		module, owner string
		want          map[string]int // method name -> impl blocks
	}{
		{"instant", "Instant", map[string]int{"add": 1, "subtract": 2}},
		{"calendar", "Date", map[string]int{"add": 4, "subtract": 4}},
	} {
		page := renderModuleWithLinks(lib, tc.module, nil)
		sections := strings.Split(page, "\n### ")
		got := map[string]int{}
		editors := 0
		for _, node := range lib.Nodes[tc.module] {
			block, ok := node.(*ast.ImplBlock)
			if !ok || block.Interface == nil || block.Receiver.TypeString() != tc.owner {
				continue
			}
			iface := block.Interface.TypeString()
			if !strings.HasPrefix(iface, "Add<") && !strings.HasPrefix(iface, "Subtract<") {
				continue
			}
			header := "impl " + iface + " for " + tc.owner
			context := `<p class="nomi-ref-context">` + html.EscapeString(header) + `</p>`
			var section string
			for _, s := range sections {
				if strings.Contains(s, context) {
					if section != "" {
						t.Errorf("std/%s: %q labels two sections", tc.module, header)
					}
					section = s
				}
			}
			if section == "" {
				t.Errorf("std/%s: no section is labelled %q\n%s", tc.module, header, page)
				continue
			}
			fn := block.Items[0].(*ast.FuncDef)
			got[fn.Name]++
			// A heading names the interface only when the owner's
			// method name alone would head two sections.
			heading := fmt.Sprintf(
				`<span class="nomi-ref-owner">%s.</span><span class="nomi-ref-name">%s</span>`,
				tc.owner, fn.Name)
			if tc.want[fn.Name] > 1 {
				heading += fmt.Sprintf(` <span class="nomi-ref-detail">%s</span>`, html.EscapeString(iface))
			}
			heading += "\n"
			if !strings.HasPrefix(section, heading) {
				t.Errorf("std/%s: %q's heading should be %q; section:\n%s",
					tc.module, header, heading, section)
			}
			first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(fn.Doc), "\n", 2)[0])
			if first == "" || !strings.Contains(section, first) {
				t.Errorf("std/%s: %q's section lacks its doc %q", tc.module, header, first)
			}
			for _, test := range ast.AttachedTestsOf(fn) {
				body := html.EscapeString(nomiformat.RenderAttachedTestBody(test))
				if !strings.Contains(section, `<code class="language-nomi-test">`+body+`</code>`) {
					t.Errorf("std/%s: %q's section lacks its editor:\n%s\nsection:\n%s",
						tc.module, header, body, section)
				}
				editors++
			}
		}
		for name, n := range tc.want {
			if got[name] != n {
				t.Errorf("std/%s: %s.%s has %d impl sections, want %d", tc.module, tc.owner, name, got[name], n)
			}
		}
		if editors == 0 {
			t.Errorf("std/%s: none of %s's Add/Subtract impls carried an editor; the editor check is vacuous",
				tc.module, tc.owner)
		}
	}
}

// A `//!` test's own leading imports stay in its editor, spelled as a user
// program spells them, so the example can be copied out as it stands.
// Maybe.collect's case imports std/ranges.Range, which std/maybe does not.
func TestReferenceEditorShowsTheTestsOwnImports(t *testing.T) {
	lib := std.Load()
	page := renderModuleWithLinks(lib, "maybe", nil)
	var collect *referenceTest
	tests := referenceTestBodies(page, "maybe")
	for i := range tests {
		if strings.Contains(tests[i].Body, "Maybe.collect(") {
			collect = &tests[i]
			break
		}
	}
	if collect == nil {
		t.Fatalf("std/maybe reference has no Maybe.collect editor\n%s", page)
	}
	if !strings.HasPrefix(collect.Body, "import std/ranges.Range\n") {
		t.Errorf("Maybe.collect editor does not open with `import std/ranges.Range`:\n%s", collect.Body)
	}
	if strings.Contains(collect.Context, "ranges.Range") {
		t.Errorf("the editor's hidden context repeats the import it shows: %q", collect.Context)
	}
	names := make([]string, 0, len(lib.Files))
	for name := range lib.Files {
		names = append(names, name)
	}
	for _, name := range names {
		for _, tc := range referenceTestBodies(renderModuleWithLinks(lib, name, nil), name) {
			for _, line := range strings.Split(tc.Body, "\n") {
				if imp, ok := strings.CutPrefix(line, "import "); ok && !strings.HasPrefix(imp, "std/") {
					t.Errorf("std/%s %s: an editor opens with a stdlib import a user program cannot write: %q", name, tc.Label, line)
				}
			}
		}
	}
}
