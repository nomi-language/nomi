package declinesites

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// listPath is the committed inventory: one line per decline site with its
// class, where it is, which suites execute it, and a note.
const listPath = "../../testdata/lowering-declines.txt"

const listHeader = `# Every place internal/irbuild can decline a body, with whether anything
# reaches it. internal/declinesites says what counts as a site (its package
# comment) and TestDeclineSitesAreListed fails when a site is added or removed
# without this file. Columns, tab-separated:
#
#   class   a  reached: a suite below executes the site
#           b  unreached, and the checker rejects every program that would reach it
#           c  unreached, and the checker accepts such a program: a gap, pinned in
#              vmhost's knownLoweringGaps (it moves to a once the coverage reruns)
#           d  unreached, and this inventory could not decide which
#           -  unreached, not classified yet
#           ?  added since the coverage last ran
#   key     function | kind | description#n (kinds: note, false, computed)
#   pos     file:line in internal/irbuild, refreshed by regeneration
#   reached the suites whose coverage executes the site, or -
#   note    the reason for a b or d, the reproducer for a c
#
# Suites: expect (corpus, stdlib, tour and failure records), vmhost (generated
# programs, known gaps, the ported reproducers of fixed gaps, lowered-program
# runs, spec programs, and the two lowering fuzz targets' corpora), projectgen
# (generated projects at about 1k and 10k lines), irbuild (the builder's own
# tests). A site a suite executes need not decline there: a caller may try
# another lowering when it fails.
#
# Regenerate the list, keeping classes and notes:
#   NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/declinesites -run TestDeclineSitesAreListed -count=1
# Rerun the coverage too (about ten minutes; runs the whole irbuild package):
#   NOMI_DECLINE_COVERAGE=1 go test ./internal/declinesites -run TestDeclineSitesAreListed -count=1 -timeout 0 -v
# With NOMI_DECLINE_PROFILES=<dir> as well, the profiles stay in <dir>; with
# it alone, the list is rewritten from the profiles already there.
`

type entry struct {
	class, key, pos, reached, note string
}

func readList(path string) (map[string]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]entry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			return nil, fmt.Errorf("%s:%d: want 5 tab-separated fields, have %d", path, n, len(f))
		}
		e := entry{f[0], f[1], f[2], f[3], f[4]}
		if _, dup := out[e.key]; dup {
			return nil, fmt.Errorf("%s:%d: %s is listed twice", path, n, e.key)
		}
		out[e.key] = e
	}
	return out, sc.Err()
}

func writeList(path string, sites []Site, entries map[string]entry) error {
	var b strings.Builder
	b.WriteString(listHeader)
	for _, s := range sites {
		e := entries[s.Key()]
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", e.class, s.Key(), s.Pos(), e.reached, e.note)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// TestDeclineSitesAreListed holds testdata/lowering-declines.txt to the
// builder: every decline site is listed, by a key without its line, and
// nothing else is. NOMI_REGENERATE_EXPECTATIONS=1 rewrites the list, keeping
// each site's class and note; NOMI_DECLINE_COVERAGE=1 also runs the suites
// with coverage and records which execute each site, and
// NOMI_DECLINE_PROFILES=<dir> reads their profiles from a directory a
// previous run left instead.
func TestDeclineSitesAreListed(t *testing.T) {
	sites, err := Find("../irbuild")
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) < 100 {
		t.Fatalf("found %d decline sites; the analysis is broken", len(sites))
	}
	entries, err := readList(listPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	coverage, profilesDir := os.Getenv("NOMI_DECLINE_COVERAGE") != "", os.Getenv("NOMI_DECLINE_PROFILES")
	if os.Getenv("NOMI_REGENERATE_EXPECTATIONS") != "" || coverage || profilesDir != "" {
		regenerate(t, sites, entries, coverage, profilesDir)
		return
	}
	var added, removed []string
	keys := map[string]bool{}
	for _, s := range sites {
		keys[s.Key()] = true
		if _, ok := entries[s.Key()]; !ok {
			added = append(added, s.Pos()+"\t"+s.Key())
		}
	}
	for k := range entries {
		if !keys[k] {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)
	if len(added)+len(removed) > 0 {
		t.Errorf("testdata/lowering-declines.txt is out of date: %d decline sites are not listed and %d listed are gone.\n"+
			"Regenerate it with NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/declinesites -run TestDeclineSitesAreListed -count=1,\n"+
			"then classify each new site (see the file's header).\nnot listed:\n  %s\ngone:\n  %s",
			len(added), len(removed), strings.Join(added, "\n  "), strings.Join(removed, "\n  "))
	}
}

func regenerate(t *testing.T, sites []Site, old map[string]entry, coverage bool, profilesDir string) {
	var reached map[string][]string
	if coverage || profilesDir != "" {
		profiles := map[string]string{}
		if !coverage {
			for _, s := range Suites {
				profiles[s.Name] = filepath.Join(profilesDir, s.Name+".out")
			}
		} else {
			root, err := filepath.Abs("../..")
			if err != nil {
				t.Fatal(err)
			}
			// With both set, the profiles and logs are kept in the directory.
			dir := profilesDir
			if dir == "" {
				dir = t.TempDir()
			}
			profiles, err = RunSuites(root, dir, func(s string) { t.Log(s) })
			if err != nil {
				t.Fatal(err)
			}
		}
		var err error
		reached, err = Reached(sites, profiles)
		if err != nil {
			t.Fatal(err)
		}
	}
	entries := map[string]entry{}
	counts := map[string]int{}
	for _, s := range sites {
		e, ok := old[s.Key()]
		if !ok {
			e = entry{class: "?", reached: "?"}
		}
		if reached != nil {
			if r := reached[s.Key()]; len(r) > 0 {
				e.class, e.reached = "a", strings.Join(r, ",")
			} else {
				e.reached = "-"
				if e.class == "a" || e.class == "?" {
					e.class = "-"
				}
			}
		}
		entries[s.Key()] = e
		counts[e.class]++
	}
	if err := writeList(listPath, sites, entries); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d sites to %s: %v", len(sites), listPath, counts)
}
