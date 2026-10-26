package stdio

import (
	"bytes"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// Memory is an in-memory file system: the browser's, the tour's, and the
// sandbox `fs_testing.run` installs. It answers the io.Error the operating
// system's backend answers for the same operation, which fs_testing's
// conformance suite checks by running the same rules against both.
//
// Paths are absolute inside it once resolved against its working directory
// (cwd). Parent directories of a seeded file exist. Besides the files it
// keeps what a test asks about: each operation in order (Log), the handles
// still open (OpenHandles), and the failures to inject (Fail).
//
// # Symlinks and modes
//
// A symlink is a node holding its target as written. A path is resolved a
// name at a time, as a kernel resolves it: a symlink met before the last
// name is followed, relative to the directory it is in or from `/` when its
// target is absolute, and `..` goes to the parent of the directory reached,
// not of the path's text. The last name is followed or not as the operation
// says (metadata follows, link_metadata does not). More than loopLimit links
// in one resolution is ELOOP. A node's permission bits are kept and
// answered, and `set_mode` changes them, but nothing is refused because of
// them: the memory file system has one user, who may do everything, so a
// rule about refusal is one the conformance suite does not hold it to.
//
// # Confinement
//
// `fs.within` on memory is a view (memView) whose working directory and
// floor are one directory: a relative path starts there, an absolute one
// must be under it, and resolution refuses to climb above the floor by
// `..` or to follow an absolute symlink, as an *os.Root refuses both.
type Memory struct {
	mu    sync.Mutex
	cwd   string
	nodes map[string]*memNode
	log   []string
	// read is each file whose contents a run opened, by resolved absolute
	// path, in the order first read (Read).
	read []string
	// open is each handle not yet closed, by an id that keeps its order.
	open     map[int]string
	nextOpen int
	failures []*memFailure
	temps    int
}

type memNode struct {
	dir bool
	// link marks a symlink, whose target is target.
	link     bool
	target   string
	data     []byte
	modified int64
	mode     int64
}

func (n *memNode) kind() Kind {
	switch {
	case n.dir:
		return KindDirectory
	case n.link:
		return KindSymlink
	}
	return KindFile
}

// Operation is what a Failure matches: std/fs_testing's `Operation`, by
// position in its declaration, from 1.
type Operation int64

const (
	// OpRead is opening a file to read it.
	OpRead Operation = iota + 1
	// OpWrite is writing data to a file opened for writing.
	OpWrite
	// OpCreate is opening a file to write it, making a directory, and
	// making a symlink.
	OpCreate
	// OpRemove is remove and remove_all.
	OpRemove
	// OpRename matches the path renamed from.
	OpRename
	// OpList is listing a directory.
	OpList
	// OpMetadata is metadata, link_metadata, read_link and set_mode.
	OpMetadata
	// OpClose is closing a file opened to read or write.
	OpClose
)

type memFailure struct {
	path string
	op   Operation
	err  rt.IOError
	// nth fails only the nth matching operation, from 1; 0 fails every one.
	nth  int64
	seen int64
}

// NewMemory is an empty in-memory file system whose working directory is
// cwd, which exists, as does `/tmp`.
func NewMemory(cwd string) *Memory {
	if cwd == "" || cwd[0] != '/' {
		cwd = "/" + cwd
	}
	m := &Memory{cwd: path.Clean(cwd), nodes: map[string]*memNode{"/": {dir: true, mode: 0o755}}, open: map[int]string{}}
	m.mkdirAll(m.cwd)
	m.mkdirAll("/tmp")
	return m
}

// Put writes a file and the directories above it, as a seed or a sandbox
// does before a run. A relative path is under the working directory.
func (m *Memory) Put(p string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	abs := m.view().abs(p)
	m.mkdirAll(path.Dir(abs))
	m.nodes[abs] = &memNode{data: append([]byte(nil), data...), modified: now(), mode: 0o644}
}

// PutDir makes a directory and the directories above it.
func (m *Memory) PutDir(p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirAll(m.view().abs(p))
}

// PutLink makes a symlink at p whose target is target, and the directories
// above it.
func (m *Memory) PutLink(p, target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	abs := m.view().abs(p)
	m.mkdirAll(path.Dir(abs))
	m.nodes[abs] = &memNode{link: true, target: target, modified: now(), mode: 0o777}
}

// Fail makes the nth operation op on p fail with err (every one when nth is
// 0). Failures match in the order they were added.
func (m *Memory) Fail(p string, op Operation, err rt.IOError, nth int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = append(m.failures, &memFailure{path: m.view().abs(p), op: op, err: err, nth: nth})
}

// Files is every file, by absolute path. Directories and symlinks are not
// files.
func (m *Memory) Files() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]byte{}
	for p, n := range m.nodes {
		if !n.dir && !n.link {
			out[p] = append([]byte(nil), n.data...)
		}
	}
	return out
}

// Relative is every file, by path relative to the working directory for a
// file under it and absolute for any other: what a page's `run` and the
// tour hand back.
func (m *Memory) Relative() map[string][]byte {
	out := map[string][]byte{}
	prefix := strings.TrimSuffix(m.cwd, "/") + "/"
	for p, data := range m.Files() {
		if rel, under := strings.CutPrefix(p, prefix); under {
			p = rel
		}
		out[p] = data
	}
	return out
}

// Cwd is the working directory.
func (m *Memory) Cwd() string { return m.cwd }

// Log is each operation as `<operation> <absolute path>`, in order.
func (m *Memory) Log() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.log...)
}

// Read is the resolved absolute path of each file whose contents a run
// opened, once each, in the order first read: a successful open to read
// (read_text, read, lines, chunks, copy's source) and the first read at a
// position of a file open_file opened. Unlike Log, it leaves out an
// attempt that failed (a missing file, one `fs.within` refuses) and every
// operation that reads no contents (list, metadata, read_link).
func (m *Memory) Read() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.read...)
}

// noteRead adds abs to Read. m.mu is held.
func (m *Memory) noteRead(abs string) {
	for _, p := range m.read {
		if p == abs {
			return
		}
	}
	m.read = append(m.read, abs)
}

// OpenHandles is the absolute path of each handle not yet closed, in the
// order they were opened.
func (m *Memory) OpenHandles() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int, 0, len(m.open))
	for id := range m.open {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = m.open[id]
	}
	return out
}

func now() int64 { return time.Now().UnixNano() }

// mkdirAll makes abs and its parents. m.mu is held, or m is being built.
func (m *Memory) mkdirAll(abs string) {
	if abs == "/" {
		return
	}
	m.mkdirAll(path.Dir(abs))
	if n, ok := m.nodes[abs]; !ok || !n.dir {
		m.nodes[abs] = &memNode{dir: true, modified: now(), mode: 0o755}
	}
}

// failure is the error an injected failure gives operation op on abs, if
// one matches. m.mu is held.
func (m *Memory) failure(op Operation, abs string) *rt.IOError {
	for _, f := range m.failures {
		if f.op != op || f.path != abs {
			continue
		}
		f.seen++
		if f.nth == 0 || f.seen == f.nth {
			e := f.err
			return &e
		}
	}
	return nil
}

func (m *Memory) record(op, abs string) { m.log = append(m.log, op+" "+abs) }

// handle registers an open handle on abs and answers its close, which
// checks an injected Close failure. m.mu is held.
func (m *Memory) handle(abs string) func() error {
	id := m.nextOpen
	m.nextOpen++
	m.open[id] = abs
	closed := false
	return func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if closed {
			return nil
		}
		closed = true
		delete(m.open, id)
		if e := m.failure(OpClose, abs); e != nil {
			return rt.IOFailure{E: *e}
		}
		return nil
	}
}

// memView is the memory file system as one call reaches it: the whole of
// it from its working directory (floor `/`), or inside `fs.within` what is
// under one directory, which is the view's working directory and floor.
type memView struct {
	m     *Memory
	cwd   string
	floor string
}

func (m *Memory) view() memView { return memView{m: m, cwd: m.cwd, floor: "/"} }

// abs is p resolved against the working directory and cleaned, as text: the
// path an operation is logged and a failure matched under. It reads only
// fields that never change.
func (v memView) abs(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = v.cwd + "/" + p
	}
	return path.Clean(p)
}

// confined reports whether v is inside `fs.within`.
func (v memView) confined() bool { return v.floor != "/" }

// resolve is the node path p names: each symlink before the last name
// followed, and the last one when follow is set. A path that reaches
// something missing, or a file where a directory is needed, answers the
// path as far as it resolved with the rest of its names after it, which
// lookup and parentFor then report as NotFound or NotADirectory. Inside a
// confined view a path that leaves the floor is InvalidPath. m.mu is held.
func (v memView) resolve(op, p string, follow bool) (string, *rt.IOError) {
	var names []string
	cur := v.cwd
	if strings.HasPrefix(p, "/") {
		clean := path.Clean(p)
		switch {
		case !v.confined():
			cur = "/"
		case clean == v.floor:
			return v.floor, nil
		case strings.HasPrefix(clean, v.floor+"/"):
			cur, p = v.floor, clean[len(v.floor)+1:]
		default:
			return "", escapes(p)
		}
	}
	names = splitNames(p)
	hops := 0
	for len(names) > 0 {
		name := names[0]
		names = names[1:]
		switch name {
		case ".":
			continue
		case "..":
			if cur == v.floor {
				if v.confined() {
					return "", escapes(p)
				}
				continue
			}
			cur = path.Dir(cur)
			continue
		}
		next := joinPath(cur, name)
		n := v.m.nodes[next]
		switch {
		case n != nil && n.link && (len(names) > 0 || follow):
			hops++
			if hops > loopLimit {
				return "", loop(op, p)
			}
			if strings.HasPrefix(n.target, "/") {
				if v.confined() {
					return "", escapes(p)
				}
				cur = "/"
			}
			names = append(splitNames(n.target), names...)
		case n == nil || !n.dir:
			if len(names) == 0 {
				return next, nil
			}
			return next + "/" + strings.Join(names, "/"), nil
		default:
			cur = next
		}
	}
	return cur, nil
}

func splitNames(p string) []string {
	var out []string
	for _, name := range strings.Split(p, "/") {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// lookup finds what is at abs. When nothing is, it answers NotADirectory
// for p when a directory above it is a file, else NotFound for p. m.mu is
// held.
func (v memView) lookup(p, abs string) (*memNode, *rt.IOError) {
	if n, ok := v.m.nodes[abs]; ok {
		return n, nil
	}
	return nil, v.absent(p, abs)
}

// absent is why nothing is at abs. m.mu is held.
func (v memView) absent(p, abs string) *rt.IOError {
	for dir := path.Dir(abs); dir != "/"; dir = path.Dir(dir) {
		if n, ok := v.m.nodes[dir]; ok {
			if !n.dir {
				return errOf(rt.TagIONotADirectory, p)
			}
			break
		}
	}
	return errOf(rt.TagIONotFound, p)
}

// parentFor checks that the directory abs would be made in exists, and
// answers NotFound naming it, as p's directory is written, when it does not.
// m.mu is held.
func (v memView) parentFor(p, abs string) *rt.IOError {
	dir := path.Dir(abs)
	n, ok := v.m.nodes[dir]
	if ok && n.dir {
		return nil
	}
	if ok {
		return errOf(rt.TagIONotADirectory, p)
	}
	if e := v.absent(p, dir); e.Tag == rt.TagIONotADirectory {
		return e
	}
	named := parentOf(p)
	if named == "" {
		named = "."
	}
	return errOf(rt.TagIONotFound, named)
}

// begin takes the lock, logs operation word on p and checks an injected
// failure of op. The caller unlocks.
func (v memView) begin(word string, op Operation, p string) (lexical string, e *rt.IOError) {
	v.m.mu.Lock()
	lexical = v.abs(p)
	v.m.record(word, lexical)
	return lexical, v.m.failure(op, lexical)
}

func (v memView) Open(p string) (rt.ReadSource, *rt.IOError) {
	lexical, e := v.begin("read", OpRead, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return rt.ReadSource{}, e
	}
	abs, e := v.resolve("open", p, true)
	if e != nil {
		return rt.ReadSource{}, e
	}
	n, e := v.lookup(p, abs)
	if e != nil {
		return rt.ReadSource{}, e
	}
	if n.dir {
		return rt.ReadSource{}, errOf(rt.TagIOIsADirectory, p)
	}
	v.m.noteRead(abs)
	return rt.ReadSource{Name: p, R: bytes.NewReader(append([]byte(nil), n.data...)), Close: v.m.handle(lexical)}, nil
}

// memSink writes a file of m: into the node as each write is made, or, for
// an atomic write, into a buffer the close puts in place.
type memSink struct {
	m       *Memory
	lexical string
	node    *memNode
	atomic  bool
	pending []byte
}

func (s *memSink) Write(b []byte) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if e := s.m.failure(OpWrite, s.lexical); e != nil {
		return 0, rt.IOFailure{E: *e}
	}
	if s.atomic {
		s.pending = append(s.pending, b...)
		return len(b), nil
	}
	// A node removed while open keeps the bytes and nobody sees them, as
	// on a disk.
	s.node.data = append(s.node.data, b...)
	s.node.modified = now()
	return len(b), nil
}

func (v memView) Create(p string, mode WriteMode) (rt.WriteSink, *rt.IOError) {
	lexical, e := v.begin("write", OpCreate, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return rt.WriteSink{}, e
	}
	if mode == ReplaceAtomically {
		return v.createAtomically(p, lexical)
	}
	if mode == New {
		// O_EXCL refuses any node at the path, a symlink's own included,
		// whether or not it leads anywhere.
		at, e := v.resolve("open", p, false)
		if e != nil {
			return rt.WriteSink{}, e
		}
		if _, exists := v.m.nodes[at]; exists {
			return rt.WriteSink{}, errOf(rt.TagIOAlreadyExists, p)
		}
	}
	abs, e := v.resolve("open", p, true)
	if e != nil {
		return rt.WriteSink{}, e
	}
	if e := v.parentFor(p, abs); e != nil {
		return rt.WriteSink{}, e
	}
	n, exists := v.m.nodes[abs]
	if exists && n.dir {
		return rt.WriteSink{}, errOf(rt.TagIOIsADirectory, p)
	}
	switch {
	case !exists:
		n = &memNode{modified: now(), mode: 0o644}
		v.m.nodes[abs] = n
	case mode == Replace:
		n.data, n.modified = nil, now()
	}
	release := v.m.handle(lexical)
	sink := &memSink{m: v.m, lexical: lexical, node: n}
	return rt.WriteSink{Name: p, W: sink, Close: release, Abort: func() { _ = release() }, Unbuffered: true}, nil
}

// createAtomically is Create's ReplaceAtomically: the bytes go to a buffer,
// and the close puts a new file at the path, replacing what is there (a
// symlink included, which it does not follow), with the permissions of what
// the path led to. m.mu is held.
func (v memView) createAtomically(p, lexical string) (rt.WriteSink, *rt.IOError) {
	if followed, e := v.resolve("open", p, true); e == nil {
		if n, ok := v.m.nodes[followed]; ok && n.dir {
			return rt.WriteSink{}, errOf(rt.TagIOIsADirectory, p)
		}
	}
	abs, e := v.resolve("open", p, false)
	if e != nil {
		return rt.WriteSink{}, e
	}
	if e := v.parentFor(p, abs); e != nil {
		return rt.WriteSink{}, e
	}
	sink := &memSink{m: v.m, lexical: lexical, atomic: true}
	release := v.m.handle(lexical)
	commit := func() error {
		if err := release(); err != nil {
			return err
		}
		v.m.mu.Lock()
		defer v.m.mu.Unlock()
		if e := v.parentFor(p, abs); e != nil {
			return rt.IOFailure{E: *e}
		}
		mode := int64(0o644)
		if followed, e := v.resolve("open", p, true); e == nil {
			if old, ok := v.m.nodes[followed]; ok {
				if old.dir {
					return rt.IOFailure{E: *errOf(rt.TagIOIsADirectory, p)}
				}
				mode = old.mode
			}
		}
		v.m.nodes[abs] = &memNode{data: sink.pending, modified: now(), mode: mode}
		return nil
	}
	return rt.WriteSink{Name: p, W: sink, Close: commit, Abort: func() { _ = release() }, Unbuffered: true}, nil
}

// memFile is a File's access to a node: reads and writes at a position,
// straight into the node.
type memFile struct {
	m       *Memory
	lexical string
	// abs is the resolved path, which Read names.
	abs  string
	node *memNode
}

func (f *memFile) ReadAt(b []byte, off int64) (int, error) {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	f.m.noteRead(f.abs)
	if off >= int64(len(f.node.data)) {
		return 0, io.EOF
	}
	n := copy(b, f.node.data[off:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) WriteAt(b []byte, off int64) (int, error) {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if e := f.m.failure(OpWrite, f.lexical); e != nil {
		return 0, rt.IOFailure{E: *e}
	}
	if end := off + int64(len(b)); end > int64(len(f.node.data)) {
		f.node.data = append(f.node.data, make([]byte, end-int64(len(f.node.data)))...)
	}
	copy(f.node.data[off:], b)
	f.node.modified = now()
	return len(b), nil
}

func (f *memFile) Truncate(size int64) error {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if e := f.m.failure(OpWrite, f.lexical); e != nil {
		return rt.IOFailure{E: *e}
	}
	if size <= int64(len(f.node.data)) {
		f.node.data = f.node.data[:size]
	} else {
		f.node.data = append(f.node.data, make([]byte, size-int64(len(f.node.data)))...)
	}
	f.node.modified = now()
	return nil
}

func (f *memFile) size() (int64, error) {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	return int64(len(f.node.data)), nil
}

func (v memView) OpenFile(p string, access Access) (rt.FileSource, *rt.IOError) {
	op, word := OpCreate, "open"
	if access == AccessRead {
		op = OpRead
	}
	lexical, e := v.begin(word, op, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return rt.FileSource{}, e
	}
	abs, e := v.resolve("open", p, true)
	if e != nil {
		return rt.FileSource{}, e
	}
	n, exists := v.m.nodes[abs]
	switch {
	case exists && n.dir:
		return rt.FileSource{}, errOf(rt.TagIOIsADirectory, p)
	case !exists && access == AccessCreate:
		if e := v.parentFor(p, abs); e != nil {
			return rt.FileSource{}, e
		}
		n = &memNode{modified: now(), mode: 0o644}
		v.m.nodes[abs] = n
	case !exists:
		_, e := v.lookup(p, abs)
		return rt.FileSource{}, e
	}
	f := &memFile{m: v.m, lexical: lexical, abs: abs, node: n}
	return rt.FileSource{Name: p, F: f, Size: f.size, Writable: access != AccessRead, Close: v.m.handle(lexical)}, nil
}

func (v memView) List(dir string) ([]DirEntry, *rt.IOError) {
	_, e := v.begin("list", OpList, dir)
	defer v.m.mu.Unlock()
	if e != nil {
		return nil, e
	}
	abs, e := v.resolve("open", dir, true)
	if e != nil {
		return nil, e
	}
	n, e := v.lookup(dir, abs)
	if e != nil {
		return nil, e
	}
	if !n.dir {
		return nil, errOf(rt.TagIONotADirectory, dir)
	}
	var out []DirEntry
	for p, child := range v.m.nodes {
		if p == abs || path.Dir(p) != abs {
			continue
		}
		out = append(out, DirEntry{Name: path.Base(p), Kind: child.kind()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (v memView) CreateDir(p string, parents bool) *rt.IOError {
	_, e := v.begin("create_dir", OpCreate, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return e
	}
	if parents {
		abs, e := v.resolve("mkdir", p, true)
		if e != nil {
			return e
		}
		if n, ok := v.m.nodes[abs]; ok {
			if n.dir {
				return nil
			}
			return errOf(rt.TagIOAlreadyExists, p)
		}
		for dir := path.Dir(abs); dir != "/"; dir = path.Dir(dir) {
			if n, ok := v.m.nodes[dir]; ok {
				if !n.dir {
					return errOf(rt.TagIONotADirectory, p)
				}
				break
			}
		}
		v.m.mkdirAll(path.Clean(abs))
		return nil
	}
	abs, e := v.resolve("mkdir", p, false)
	if e != nil {
		return e
	}
	if _, ok := v.m.nodes[abs]; ok {
		return errOf(rt.TagIOAlreadyExists, p)
	}
	if e := v.parentFor(p, abs); e != nil {
		return e
	}
	v.m.nodes[abs] = &memNode{dir: true, modified: now(), mode: 0o755}
	return nil
}

// children reports whether something is under the directory abs. m.mu is
// held.
func (v memView) children(abs string) bool {
	prefix := strings.TrimSuffix(abs, "/") + "/"
	for p := range v.m.nodes {
		if p != abs && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func (v memView) Remove(p string) *rt.IOError {
	_, e := v.begin("remove", OpRemove, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return e
	}
	abs, e := v.resolve("remove", p, false)
	if e != nil {
		return e
	}
	n, e := v.lookup(p, abs)
	if e != nil {
		return e
	}
	if n.dir && v.children(abs) {
		return errOf(rt.TagIODirectoryNotEmpty, p)
	}
	if abs == "/" || abs == v.floor {
		return &rt.IOError{Tag: rt.TagIOOther, Operation: "remove", Path: p, Code: "EBUSY"}
	}
	delete(v.m.nodes, abs)
	return nil
}

func (v memView) RemoveAll(p string) *rt.IOError {
	_, e := v.begin("remove_all", OpRemove, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return e
	}
	abs, e := v.resolve("remove", p, false)
	if e != nil {
		return e
	}
	n, ok := v.m.nodes[abs]
	if !ok {
		return nil
	}
	if !n.dir {
		delete(v.m.nodes, abs)
		return nil
	}
	prefix := strings.TrimSuffix(abs, "/") + "/"
	for q := range v.m.nodes {
		if q != "/" && q != v.floor && (q == abs || strings.HasPrefix(q, prefix)) {
			delete(v.m.nodes, q)
		}
	}
	return nil
}

func (v memView) Rename(from, to string) *rt.IOError {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	lexFrom, lexTo := v.abs(from), v.abs(to)
	v.m.record("rename", lexFrom+" to "+lexTo)
	if e := v.m.failure(OpRename, lexFrom); e != nil {
		return e
	}
	src, e := v.resolve("rename", from, false)
	if e != nil {
		return e
	}
	dst, e := v.resolve("rename", to, false)
	if e != nil {
		return e
	}
	n, e := v.lookup(from, src)
	if e != nil {
		return e
	}
	if e := v.parentFor(to, dst); e != nil {
		return e
	}
	if src == dst {
		return nil
	}
	if n.dir && strings.HasPrefix(dst, src+"/") {
		return &rt.IOError{Tag: rt.TagIOOther, Operation: "rename", Path: to, Code: "EINVAL"}
	}
	if old, ok := v.m.nodes[dst]; ok {
		switch {
		case old.dir && !n.dir:
			return errOf(rt.TagIOIsADirectory, to)
		case !old.dir && n.dir:
			return errOf(rt.TagIONotADirectory, to)
		case old.dir && v.children(dst):
			return errOf(rt.TagIODirectoryNotEmpty, to)
		}
	}
	moved := map[string]*memNode{}
	prefix := src + "/"
	for q, node := range v.m.nodes {
		if q == src || (n.dir && strings.HasPrefix(q, prefix)) {
			moved[dst+strings.TrimPrefix(q, src)] = node
			delete(v.m.nodes, q)
		}
	}
	for q, node := range moved {
		v.m.nodes[q] = node
	}
	return nil
}

func infoOfNode(n *memNode) Info {
	info := Info{Kind: n.kind(), Size: int64(len(n.data)), Modified: n.modified, Mode: n.mode}
	switch {
	case n.dir:
		info.Size = 0
	case n.link:
		info.Size = int64(len(n.target))
	}
	return info
}

func (v memView) Stat(p string) (Info, *rt.IOError) {
	_, e := v.begin("metadata", OpMetadata, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return Info{}, e
	}
	abs, e := v.resolve("stat", p, true)
	if e != nil {
		return Info{}, e
	}
	n, e := v.lookup(p, abs)
	if e != nil {
		return Info{}, e
	}
	return infoOfNode(n), nil
}

func (v memView) Lstat(p string) (Info, *rt.IOError) {
	_, e := v.begin("link_metadata", OpMetadata, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return Info{}, e
	}
	abs, e := v.resolve("lstat", p, false)
	if e != nil {
		return Info{}, e
	}
	n, e := v.lookup(p, abs)
	if e != nil {
		return Info{}, e
	}
	return infoOfNode(n), nil
}

func (v memView) Symlink(p, target string) *rt.IOError {
	_, e := v.begin("create_symlink", OpCreate, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return e
	}
	abs, e := v.resolve("symlink", p, false)
	if e != nil {
		return e
	}
	if _, ok := v.m.nodes[abs]; ok {
		return errOf(rt.TagIOAlreadyExists, p)
	}
	if e := v.parentFor(p, abs); e != nil {
		return e
	}
	v.m.nodes[abs] = &memNode{link: true, target: target, modified: now(), mode: 0o777}
	return nil
}

func (v memView) ReadLink(p string) (string, *rt.IOError) {
	_, e := v.begin("read_link", OpMetadata, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return "", e
	}
	abs, e := v.resolve("readlink", p, false)
	if e != nil {
		return "", e
	}
	n, e := v.lookup(p, abs)
	if e != nil {
		return "", e
	}
	if !n.link {
		return "", &rt.IOError{Tag: rt.TagIOOther, Operation: "readlink", Path: p, Code: "EINVAL"}
	}
	return n.target, nil
}

func (v memView) Chmod(p string, mode int64) *rt.IOError {
	_, e := v.begin("set_mode", OpMetadata, p)
	defer v.m.mu.Unlock()
	if e != nil {
		return e
	}
	abs, e := v.resolve("chmod", p, true)
	if e != nil {
		return e
	}
	n, e := v.lookup(p, abs)
	if e != nil {
		return e
	}
	n.mode = mode
	return nil
}

func (v memView) Within(dir string) (Backend, func(), *rt.IOError) {
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	abs, e := v.resolve("open", dir, true)
	if e != nil {
		return nil, nil, e
	}
	n, e := v.lookup(dir, abs)
	if e != nil {
		return nil, nil, e
	}
	if !n.dir {
		return nil, nil, errOf(rt.TagIONotADirectory, dir)
	}
	return memView{m: v.m, cwd: abs, floor: abs}, func() {}, nil
}

func (v memView) TempDir() string { return "/tmp" }

func (v memView) MakeTempDir() (string, *rt.IOError) {
	if v.confined() {
		return "", withinTempDir()
	}
	v.m.mu.Lock()
	defer v.m.mu.Unlock()
	v.m.mkdirAll("/tmp")
	for {
		v.m.temps++
		dir := "/tmp/nomi-" + strconv.Itoa(v.m.temps)
		if _, ok := v.m.nodes[dir]; !ok {
			v.m.record("create_dir", dir)
			v.m.nodes[dir] = &memNode{dir: true, modified: now(), mode: 0o700}
			return dir, nil
		}
	}
}

func (v memView) CurrentDir() (string, *rt.IOError) { return v.cwd, nil }

// The Backend methods of the whole memory file system, from its working
// directory.

func (m *Memory) Open(p string) (rt.ReadSource, *rt.IOError) { return m.view().Open(p) }
func (m *Memory) Create(p string, mode WriteMode) (rt.WriteSink, *rt.IOError) {
	return m.view().Create(p, mode)
}
func (m *Memory) OpenFile(p string, access Access) (rt.FileSource, *rt.IOError) {
	return m.view().OpenFile(p, access)
}
func (m *Memory) List(dir string) ([]DirEntry, *rt.IOError)        { return m.view().List(dir) }
func (m *Memory) CreateDir(p string, parents bool) *rt.IOError     { return m.view().CreateDir(p, parents) }
func (m *Memory) Remove(p string) *rt.IOError                      { return m.view().Remove(p) }
func (m *Memory) RemoveAll(p string) *rt.IOError                   { return m.view().RemoveAll(p) }
func (m *Memory) Rename(from, to string) *rt.IOError               { return m.view().Rename(from, to) }
func (m *Memory) Stat(p string) (Info, *rt.IOError)                { return m.view().Stat(p) }
func (m *Memory) Lstat(p string) (Info, *rt.IOError)               { return m.view().Lstat(p) }
func (m *Memory) Symlink(p, target string) *rt.IOError             { return m.view().Symlink(p, target) }
func (m *Memory) ReadLink(p string) (string, *rt.IOError)          { return m.view().ReadLink(p) }
func (m *Memory) Chmod(p string, mode int64) *rt.IOError           { return m.view().Chmod(p, mode) }
func (m *Memory) Within(dir string) (Backend, func(), *rt.IOError) { return m.view().Within(dir) }
func (m *Memory) TempDir() string                                  { return "/tmp" }
func (m *Memory) MakeTempDir() (string, *rt.IOError)               { return m.view().MakeTempDir() }
func (m *Memory) CurrentDir() (string, *rt.IOError)                { return m.cwd, nil }
