package main

import (
	"html"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// tourDocs is the tour's content directory. The generators write the
// Specification, Writing Nomi, Tooling and reference pages under it; every
// other page is a tour chapter, the examples' pages included.
var tourDocs = filepath.Join("..", "..", "tour", "src", "content", "docs")

// tourGenerated are the folders under tourDocs that a generator writes and
// git ignores. The test builds their pages itself rather than reading
// whatever an earlier build left there.
var tourGenerated = map[string]bool{"spec": true, "writing": true, "tooling": true, "reference": true}

// TestTourLinksResolve reads every tour chapter and holds each link to a
// site page (`/spec/...`, `/writing/...`, `/tooling/...`, `/reference/...`
// or another chapter, `/<chapter>/`) and each `#anchor` within a chapter to
// a page the generators would write, or a chapter, and to a heading id or
// an `id` attribute on it. The generated pages come from the generators'
// own code, so a renamed heading or page fails here before the site breaks.
func TestTourLinksResolve(t *testing.T) {
	anchors := map[string]map[string]bool{} // "/spec/x/" -> its ids
	addSite := func(site string, pages map[string]string, ids func(string) map[string]bool) {
		for name, text := range pages {
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			page := site + strings.TrimSuffix(name, ".md") + "/"
			if name == "index.md" {
				page = site
			}
			anchors[page] = ids(text)
		}
	}
	spec, _, err := specPages(specPath)
	if err != nil {
		t.Fatal(err)
	}
	addSite(specSite, spec, pageAnchors)
	style, _, err := stylePages(stylePath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	addSite(styleSite, style, pageAnchors)
	tooling, _, err := toolingPages(toolingPath, specPath)
	if err != nil {
		t.Fatal(err)
	}
	addSite(toolingSite, tooling, pageAnchors)
	addSite("/reference/", referencePages(), referenceAnchors)

	chapters := map[string]string{} // path from tourDocs -> text
	err = filepath.WalkDir(tourDocs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tourDocs, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if tourGenerated[rel] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".md") {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		chapters[rel] = string(text)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rels := make([]string, 0, len(chapters))
	for rel, text := range chapters {
		anchors[chapterPage(rel)] = pageAnchors(text)
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	filters := examplesFilters(chapters["examples/index.md"])
	link := regexp.MustCompile(`(?:\]\(|href=")(/[^)"#\s]*|#[^)"\s]*)(#[^)"\s]*)?`)
	var broken []string
	checked := map[string]int{}
	for _, rel := range rels {
		for i, l := range fenceLines(strings.Split(chapters[rel], "\n")) {
			if l.fence {
				continue
			}
			for _, m := range link.FindAllStringSubmatch(l.text, -1) {
				page, anchor := m[1], m[2]
				page, query, _ := strings.Cut(page, "?")
				if strings.HasPrefix(page, "#") {
					page, anchor = chapterPage(rel), page
				}
				if !strings.HasSuffix(page, "/") {
					page += "/"
				}
				where := rel + ":" + strconv.Itoa(i+1) + ": " + page + anchor
				ids, ok := anchors[page]
				switch {
				case !ok:
					broken = append(broken, where+" is no page")
				case anchor != "" && anchor != "#_top" && !ids[strings.TrimPrefix(anchor, "#")]:
					broken = append(broken, where+": the page has no such anchor")
				}
				if query != "" {
					values, err := url.ParseQuery(query)
					switch {
					case page != "/examples/" || err != nil:
						broken = append(broken, where+"?"+query+": only the examples table reads a query")
					default:
						for kind, vs := range values {
							for _, v := range vs {
								if !filters[kind][v] {
									broken = append(broken, where+"?"+query+": the examples table has no "+kind+" "+strconv.Quote(v))
								}
							}
						}
					}
				}
				checked[strings.SplitN(strings.Trim(page, "/"), "/", 2)[0]]++
			}
		}
	}
	if len(broken) > 0 {
		t.Errorf("%d tour links name no page or anchor the site has:\n%s", len(broken), strings.Join(broken, "\n"))
	}
	if checked["reference"] < 100 || checked["pipes"] < 20 {
		t.Errorf("checked %d links into the reference and %d into Pipes; the tour holds more", checked["reference"], checked["pipes"])
	}
}

// examplesFilters answers the values each of the examples table's filters
// offers (`subject`, `chapter`), which its script reads from the URL.
func examplesFilters(index string) map[string]map[string]bool {
	filters := map[string]map[string]bool{}
	selects := regexp.MustCompile(`(?s)<select data-filter="([a-z]+)">(.*?)</select>`)
	options := regexp.MustCompile(`<option value="([^"]+)">`)
	for _, s := range selects.FindAllStringSubmatch(index, -1) {
		filters[s[1]] = map[string]bool{}
		for _, o := range options.FindAllStringSubmatch(s[2], -1) {
			filters[s[1]][o[1]] = true
		}
	}
	return filters
}

// chapterPage is the site path of a tour page, from its path under
// tourDocs: `pipes.md` is `/pipes/`, `examples/index.md` is `/examples/`
// and `index.md` is `/`.
func chapterPage(rel string) string {
	slug := strings.TrimSuffix(strings.TrimSuffix(rel, ".md"), "index")
	if slug == "" {
		return "/"
	}
	return "/" + strings.TrimSuffix(slug, "/") + "/"
}

// referencePages renders the standard library reference as `nomi-docgen`
// writes it: a page per module that has one, and the index.
func referencePages() map[string]string {
	lib := std.Load()
	names := referenceCandidates(lib)
	links := referenceLinksForModules(lib, names)
	pages := map[string]string{}
	var written []string
	for _, name := range names {
		if page := renderModuleWithLinks(lib, name, links); page != "" {
			pages[name+".md"] = page
			written = append(written, name)
		}
	}
	pages["index.md"] = renderIndex(written)
	return pages
}

// referenceHeadingTag is the HTML a reference heading wraps its parts in.
var referenceHeadingTag = regexp.MustCompile(`</?span\b[^>]*>`)

// referenceAnchors are a reference page's ids. Its headings hold HTML
// spans, which the site slugs by their text, and its summary and entries
// may carry `id` attributes.
func referenceAnchors(page string) map[string]bool {
	lines := strings.Split(page, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "#") {
			lines[i] = html.UnescapeString(referenceHeadingTag.ReplaceAllString(l, ""))
		}
	}
	ids := pageAnchors(strings.Join(lines, "\n"))
	for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		ids[m[1]] = true
	}
	return ids
}
