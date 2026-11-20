package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// referenceEntry is one function's section of a rendered reference page:
// its heading's owner (`Seed`, or `fs` for a module function) and name, and
// the outside call its line under the signature names, "" for none.
type referenceEntry struct {
	owner, name, outside string
}

var (
	refHeading = regexp.MustCompile(`^#{2,3} (?:<span class="nomi-ref-owner">([^<]*)\.</span>)?<span class="nomi-ref-name">([^<]*)</span>`)
	refReach   = regexp.MustCompile(`<p class="nomi-ref-reach">Reaches outside the program: <code>([^<]*)</code>\.</p>`)
)

// referenceEntries is every function section of page, in order.
func referenceEntries(page string) []referenceEntry {
	var out []referenceEntry
	for _, line := range strings.Split(page, "\n") {
		if m := refHeading.FindStringSubmatch(line); m != nil {
			out = append(out, referenceEntry{owner: m[1], name: m[2]})
			continue
		}
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			out = append(out, referenceEntry{})
			continue
		}
		if m := refReach.FindStringSubmatch(line); m != nil && len(out) > 0 {
			out[len(out)-1].outside = m[1]
		}
	}
	return out
}

// A function a call to which reaches outside the program carries a line
// under its signature naming the std module a capability list must allow
// for it, the name a capability error gives. A host function marked
// outside is one; so is a function written in Nomi whose walk reaches one
// (`fs.exists` calls fs's host `metadata`, `Seed.from_os` random's
// `os_state`); a function whose walk reaches none carries no line.
func TestReferenceMarksFunctionsThatReachOutside(t *testing.T) {
	lib := std.Load()
	for _, tc := range []struct {
		module, owner, name, outside string
	}{
		{"fs", "fs", "read_text", "std::fs"},
		{"fs", "fs", "exists", "std::fs"},
		{"random", "Seed", "from_os", "std::random"},
		{"random", "Seed", "from_int", ""},
		{"io", "io", "print", "std::io"},
		{"tasks", "Task", "spawn", "std::tasks"},
		{"strings", "String", "uppercase", ""},
		{"http", "http", "get", "std::http"},
		{"net", "net", "dial", "std::net"},
	} {
		page := renderModuleWithLinks(lib, tc.module, nil)
		found := false
		for _, e := range referenceEntries(page) {
			if e.owner != tc.owner || e.name != tc.name {
				continue
			}
			found = true
			if e.outside != tc.outside {
				t.Errorf("std::%s %s.%s: reaches outside by %q, want %q", tc.module, tc.owner, tc.name, e.outside, tc.outside)
			}
		}
		if !found {
			t.Errorf("std::%s: no entry for %s.%s on its page", tc.module, tc.owner, tc.name)
		}
		overview := strings.Contains(page, "[capability lists]("+capabilityListsLink+")")
		if want := tc.outside != ""; want && !overview {
			t.Errorf("std::%s: its overview does not say its functions reach outside", tc.module)
		}
	}
	if page := renderModuleWithLinks(lib, "strings", nil); strings.Contains(page, "nomi-ref-reach") || strings.Contains(page, capabilityListsLink) {
		t.Errorf("std::strings: a function is marked as reaching outside, but none of strings' functions does")
	}
}

// Every function the tooling reference's table of outside calls lists is
// marked on its module's page when the page documents it (a module's
// private host functions, such as random's `os_state`, are not on it), and
// the page's overview says its functions reach outside. The table is
// written from the crossings the VM's intrinsics, the std binding rows and
// std/compiler's hosts mark outside (internal/docscheck
// TestOutsideCallTableMatchesTheBindings), with a crossing a `host` module
// declares listed under its parent module. This reads those, which name
// each function's owner type that the table leaves out (`Task.spawn`).
func TestReferenceMarksEveryOutsideFunctionOfTheToolingTable(t *testing.T) {
	keys := vm.OutsideCrossings()
	for _, key := range compilerhosts.Names() {
		if outside, _ := compilerhosts.Outside(key); outside {
			keys[key] = "compiler"
		}
	}
	lib := std.Load()
	pages := map[string][]referenceEntry{}
	marked := 0
	for key, declaredIn := range keys {
		module := strings.TrimSuffix(declaredIn, "::host")
		qualified := strings.TrimPrefix(key, strings.ReplaceAll(declaredIn, "::", "/")+".")
		owner, name, typed := strings.Cut(qualified, ".")
		if !typed {
			owner, name = module[strings.LastIndex(module, ":")+1:], qualified
		}
		entries, ok := pages[module]
		if !ok {
			page := renderModuleWithLinks(lib, strings.ReplaceAll(module, "::", "/"), nil)
			if !strings.Contains(page, "[capability lists]("+capabilityListsLink+")") {
				t.Errorf("std::%s: its overview does not say its functions reach outside", module)
			}
			entries = referenceEntries(page)
			pages[module] = entries
		}
		for _, e := range entries {
			if e.owner != owner || e.name != name {
				continue
			}
			if e.outside != "std::"+module {
				t.Errorf("std::%s %s.%s (%s): reaches outside by %q, want %q", module, owner, name, key, e.outside, "std::"+module)
			} else {
				marked++
			}
		}
	}
	if marked == 0 {
		t.Fatal("no function the table lists is on a page")
	}
	t.Logf("%d modules, %d of the table's %d functions marked on their pages", len(pages), marked, len(keys))
}
