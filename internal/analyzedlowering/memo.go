package analyzedlowering

import (
	"slices"
	"sync"
)

// Memo keeps the last lowering of each path from an analysis, so a run
// whose input is the same as the last one's answers what that run found
// without lowering again. The language server lowers an edited document
// once its edits pause and again when it is saved: the same text, from the
// same program, both times.
//
// A lowering from an analysis reads nothing but the texts the analysis was
// built from (the entry's, each other project file's and nomi.toml's;
// Check refuses an analysis whose files do not hold those texts on disk)
// and the stdlib, which a process does not change. Two analyses of the same
// texts are the same program (analysis.DocumentManager.Recheck answers what
// a full check answers), so the memo keys a lowering by those texts and
// the root, never by the analysis objects, which a recheck replaces or
// rewrites in place.
type Memo struct {
	mu   sync.Mutex
	last map[string]memoEntry
}

type memoEntry struct {
	key      memoKey
	problems error
	err      error
}

// memoKey is what a lowering of an analysis reads.
type memoKey struct {
	src, root, manifest string
	reachesEntry        bool
	files               []memoFile
}

type memoFile struct {
	key, path, text string
}

func keyOf(src string, a *Analyzed) memoKey {
	k := memoKey{src: src, root: a.Root, manifest: a.Manifest, reachesEntry: a.ReachesEntry}
	k.files = make([]memoFile, len(a.Files))
	for i, f := range a.Files {
		k.files[i] = memoFile{f.Key, f.Path, f.Text}
	}
	return k
}

func (k memoKey) equal(o memoKey) bool {
	return k.src == o.src && k.root == o.root && k.manifest == o.manifest &&
		k.reachesEntry == o.reachesEntry && slices.Equal(k.files, o.files)
}

// Lookup answers what Check answered for path's last lowering that Store
// kept, when that lowering read the texts a was built from and a is still
// offered (Offered): its files hold those texts on disk. ok is false
// otherwise, and for a nil a.
func (m *Memo) Lookup(path, src string, a *Analyzed) (problems, err error, ok bool) {
	if m == nil || a == nil {
		return nil, nil, false
	}
	m.mu.Lock()
	e, found := m.last[path]
	m.mu.Unlock()
	if !found || !e.key.equal(keyOf(src, a)) || !Offered(path, src, a) {
		return nil, nil, false
	}
	return e.problems, e.err, true
}

// Store keeps problems and err, what Check answered with ok true for path,
// src and a, as path's last lowering. A nil Memo keeps nothing.
func (m *Memo) Store(path, src string, a *Analyzed, problems, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		m.last = map[string]memoEntry{}
	}
	m.last[path] = memoEntry{key: keyOf(src, a), problems: problems, err: err}
}

// Forget drops path's last lowering.
func (m *Memo) Forget(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.last, path)
}
