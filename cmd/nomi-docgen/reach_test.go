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
// the effect its line under the signature names, "" for none.
type referenceEntry struct {
	owner, name, effect string
}

var (
	refHeading = regexp.MustCompile(`^#{2,3} (?:<span class="nomi-ref-owner">([^<]*)\.</span>)?<span class="nomi-ref-name">([^<]*)</span>`)
	refReach   = regexp.MustCompile(`<p class="nomi-ref-reach">Has effects: <code>([^<]*)</code>\.</p>`)
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
			out[len(out)-1].effect = m[1]
		}
	}
	return out
}

// A function a call to which has an effect carries a line under its
// signature naming the std module an effects list must allow for it,
// the name a refusal gives. A host function with an effect is
// one; so is a function written in Nomi whose walk reaches one
// (`fs.exists` calls fs's host `metadata`, `Seed.from_os` random's
// `os_state`), and a `once` whose initializer's walk reaches one
// (`io.stdout`); a function or `once` whose walk reaches none carries no
// line.
func TestReferenceMarksFunctionsWithEffects(t *testing.T) {
	lib := std.Load()
	for _, tc := range []struct {
		module, owner, name, effect string
	}{
		{"fs", "fs", "read_text", "std::fs"},
		{"fs", "fs", "exists", "std::fs"},
		{"random", "Seed", "from_os", "std::random"},
		{"random", "Seed", "from_int", ""},
		{"io", "io", "print", "std::io"},
		{"io", "io", "stdout", "std::io"},
		{"float", "Float", "pi", ""},
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
			if e.effect != tc.effect {
				t.Errorf("std::%s %s.%s: has the effect %q, want %q", tc.module, tc.owner, tc.name, e.effect, tc.effect)
			}
		}
		if !found {
			t.Errorf("std::%s: no entry for %s.%s on its page", tc.module, tc.owner, tc.name)
		}
		overview := strings.Contains(page, "[effects lists]("+effectListsLink+")")
		if want := tc.effect != ""; want && !overview {
			t.Errorf("std::%s: its overview does not say its functions have effects", tc.module)
		}
	}
	if page := renderModuleWithLinks(lib, "strings", nil); strings.Contains(page, "nomi-ref-reach") || strings.Contains(page, effectListsLink) {
		t.Errorf("std::strings: a function is marked as having effects, but none of strings' functions has")
	}
}

// Every effect crossing the VM's intrinsics, the std binding rows and
// std/compiler's hosts mark as an effect (vm.EffectCrossings) is marked
// on its module's page when the page documents it (a module's private
// host functions, such as random's `os_state`, are not on it), and the
// page's overview says its functions have effects. A crossing a `host`
// module declares is its parent module's.
func TestReferenceMarksEveryEffectCrossing(t *testing.T) {
	keys := vm.EffectCrossings()
	for _, key := range compilerhosts.Names() {
		if effect, _ := compilerhosts.Effect(key); effect {
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
			if !strings.Contains(page, "[effects lists]("+effectListsLink+")") {
				t.Errorf("std::%s: its overview does not say its functions have effects", module)
			}
			entries = referenceEntries(page)
			pages[module] = entries
		}
		for _, e := range entries {
			if e.owner != owner || e.name != name {
				continue
			}
			if e.effect != "std::"+module {
				t.Errorf("std::%s %s.%s (%s): has the effect %q, want %q", module, owner, name, key, e.effect, "std::"+module)
			} else {
				marked++
			}
		}
	}
	if marked == 0 {
		t.Fatal("no effect crossing is on a page")
	}
	t.Logf("%d modules, %d of the %d effect crossings marked on their pages", len(pages), marked, len(keys))
}
