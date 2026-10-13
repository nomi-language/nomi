package main

// The file watching `nomi run --web`, `nomi run --watch` and `nomi test
// --watch` share: it polls modification times and sizes rather than asking
// the system for events, so it needs no platform code and sees a file an
// editor replaces by renaming another over it.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"
)

const (
	// watchPoll is how often the watched files' modification times are read.
	watchPoll = 250 * time.Millisecond
	// watchSettle is how long the files must stay unchanged after a change
	// before it is acted on, so an editor's burst of writes acts once.
	watchSettle = 100 * time.Millisecond
)

// watchSet is files whose change runs one action.
type watchSet struct {
	// files is the files to watch, read at every poll; a set whose listing
	// is costly answers one it keeps and refreshes in changed.
	files func() []string
	// changed is the action, given the changed files' display paths joined
	// by ", ". It runs on the watching goroutine, so the next poll waits for
	// it.
	changed func(names string)
}

// watchFiles polls each set's files until ctx is done. Once a set's files
// have changed and then stayed unchanged for watchSettle, its action runs,
// sets in the order given. A file that changes while an action runs keeps
// the stamp it had before, so the next poll sees it.
func watchFiles(ctx context.Context, sets ...watchSet) {
	last := make([]map[string]fileStamp, len(sets))
	for i, s := range sets {
		last[i] = stamps(s.files())
	}
	read := func() []map[string]fileStamp {
		now := make([]map[string]fileStamp, len(sets))
		for i, s := range sets {
			now[i] = stamps(s.files())
		}
		return now
	}
	same := func(a, b []map[string]fileStamp) bool {
		for i := range a {
			if !sameStamps(a[i], b[i]) {
				return false
			}
		}
		return true
	}
	ticker := time.NewTicker(watchPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := read()
		if same(now, last) {
			continue
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(watchSettle):
			}
			next := read()
			if same(next, now) {
				break
			}
			now = next
		}
		for i, s := range sets {
			if sameStamps(now[i], last[i]) {
				continue
			}
			s.changed(changedFiles(last[i], now[i]))
			// The set may hold other files now.
			last[i] = stamps(s.files())
			for f := range last[i] {
				if st, ok := now[i][f]; ok {
					last[i][f] = st
				}
			}
		}
	}
}

// programFiles is the files a program is built from: the entry, every user
// file it loads (not the stdlib's), and the nomi.toml of its project,
// present or not.
func programFiles(entry string) []string {
	files := []string{entry, filepath.Join(analysis.ProjectRoot(entry, ""), "nomi.toml")}
	// An unreadable import graph still leaves the files it found; the build
	// reports why.
	imported, _ := frontend.ImportedFiles(entry)
	return append(files, imported...)
}

// fileStamp is what a poll compares: a file's modification time and size,
// or that it is missing.
type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

func stamps(files []string) map[string]fileStamp {
	m := make(map[string]fileStamp, len(files))
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			m[f] = fileStamp{info.ModTime(), info.Size(), true}
		} else {
			m[f] = fileStamp{}
		}
	}
	return m
}

func sameStamps(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !v.mod.Equal(w.mod) || v.size != w.size || v.ok != w.ok {
			return false
		}
	}
	return true
}

// changedFiles names the files whose stamps differ between last and now.
func changedFiles(last, now map[string]fileStamp) string {
	changed := []string{}
	for f, s := range now {
		if p := last[f]; !p.mod.Equal(s.mod) || p.size != s.size || p.ok != s.ok {
			changed = append(changed, vmhost.DisplayPath(f))
		}
	}
	for f := range last {
		if _, ok := now[f]; !ok {
			changed = append(changed, vmhost.DisplayPath(f))
		}
	}
	slices.Sort(changed)
	return strings.Join(changed, ", ")
}
