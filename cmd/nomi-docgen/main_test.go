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
	nomiformat "github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// TestTourSidebarListsEveryGeneratedReferencePage pins the one hand-maintained
// list in the docs pipeline against the one thing that generates its targets.
//
// docgen writes <out>/<module>.md for every non-skipped stdlib module with at
// least one public declaration, and NEVER PRUNES, while the tour's navigation
// is a literal array of module names in tour/astro.config.mjs. Nothing
// connected the two, so the list silently rotted: at the time this test was
// written `bytes`, `http`, `random`, `regex` and `supervisors` all had a
// generated page that no navigation entry reached — five unreachable orphans,
// one of them a module the tour had never mentioned at all.
//
// A comment could not have noticed that. The check runs in BOTH directions
// because the two failures are different and both are real:
//
//   - a generated page with no sidebar entry is an ORPHAN: reachable only by
//     typing the URL, so a new stdlib module is invisible by default. This is
//     the direction that actually rotted, and it rots on every module added.
//   - a sidebar entry with no generated page is a DEAD LINK: Starlight
//     validates internal links at build time, so this one already fails
//     `npm run build` — but it fails there in a browser-shaped error message
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

// TestEveryDocumentedInterfaceMethodReachesItsPage pins the one doc-comment
// position whose text was parsed, kept, re-emitted by `nomi fmt -w`, and then
// dropped by every consumer.
//
// A `///` above a method INSIDE an interface body reached nothing. Three
// places had to copy it and none did: the two Symbol constructions in
// analysis/builder.go, both *ast.InterfaceMethod arms in
// internal/hoverdoc, and lsp/signature_help.go's arm, which returned "" for
// the doc where its FuncDef and ExternFunc siblings return n.Doc. So the
// reference page and the editor hover both showed a bare signature. Measured
// population when this was found: FOUR methods, 1019 bytes of authored prose
// — Struct.update (487), Iter.known_count (213), Steppable.step_by (190),
// Discrete.steps_between (129). Three of the four were on pages that already
// existed, so the missing std/structs page was not the whole of it.
//
// The population is DERIVED from the tree, not listed, so a fifth documented
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
	Body    string
	Context string
}

func referenceTestBodies(page, module string) []referenceTest {
	open := fmt.Sprintf(`<pre data-nomi-stdlib-module=%q`, module)
	var tests []referenceTest
	for {
		start := strings.Index(page, open)
		if start < 0 {
			return tests
		}
		start += len(open)
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
			Body:    html.UnescapeString(codeRest[:end]),
			Context: referenceTestContextAttr(attrs),
		})
		page = codeRest[end+len("</code></pre>"):]
	}
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
// way the tour's worker does (vmhost.StdlibReference) and reports how many
// run, fail and are blocked. A reference editor is a `//!` prompt re-hosted
// as a `test` appended to its module, so it may block where the prompt
// itself does; it must never fail.
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
	passed, blocked := 0, 0
	reasons := map[string]int{}
	for _, name := range names {
		page := renderModuleWithLinks(lib, name, links)
		for i, test := range referenceTestBodies(page, name) {
			cases, err := vmhost.StdlibReference(name, test.Body, test.Context, io.Discard)
			if err != nil {
				t.Errorf("%s/reference_%02d: %v", name, i+1, err)
				continue
			}
			if len(cases) != 1 {
				t.Errorf("%s/reference_%02d: %d cases selected, want 1", name, i+1, len(cases))
				continue
			}
			switch c := cases[0]; {
			case c.Blocked != nil:
				blocked++
				reasons[c.Blocked[0]]++
			case c.Err != nil:
				t.Errorf("%s/reference_%02d failed on the VM:\n%s\n\n%v", name, i+1, test.Body, c.Err)
			default:
				passed++
			}
		}
	}
	var lines []string
	for r, n := range reasons {
		lines = append(lines, fmt.Sprintf("%4d %s", n, r))
	}
	sort.Strings(lines)
	t.Logf("reference editors on the VM: %d passed, %d blocked\n%s", passed, blocked, strings.Join(lines, "\n"))
	if passed != referenceVMPin.passed || blocked != referenceVMPin.blocked {
		t.Errorf("reference editors on the VM: %d passed, %d blocked; the pin is %d, %d. "+
			"Raise it in the change that raises passed; never lower it without a named cause",
			passed, blocked, referenceVMPin.passed, referenceVMPin.blocked)
	}
}

// referenceVMPin is what TestReferenceInteractiveTestsOnTheVM reads: the 283
// reference editors the tour renders, run as its worker runs them. A blocked
// editor would block for the reason `nomi test std` reports for the
// same prompt. Every editor runs.
var referenceVMPin = struct{ passed, blocked int }{passed: 283, blocked: 0}

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
