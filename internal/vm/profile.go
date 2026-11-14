package vm

// THE PROFILER: where a program's time goes, by Nomi function.
//
// `nomi run --profile` (and NOMI_PROFILE in any run, a built executable's
// included) runs the program on a profiling machine, WithProfiler. A
// goroutine adds one tick to the profiler's pending count at each interval
// (1 ms). The machine takes the pending ticks at its next safe point, and
// charges them to the logical call stack it is on there:
//
//   - every call, tail call and backward branch, which already read the
//     machine's fuel (evaluate.go): a profiling machine's fuel watches
//     nothing, and its burn checks the pending count. A machine that is not
//     profiling and has no limits has no fuel, so it pays nothing;
//   - the return of a crossing into Go (a host function such as `Map.get`
//     or `String.join`, or a bound adapter), whose ticks are charged to the
//     host function on top of the caller's stack. A host call that runs for
//     50 ms takes all 50 ticks when it returns.
//
// So a sample is taken at most an instruction or so late, except across a
// stretch of bytecode with no call and no backward branch, which is short.
//
// # The logical call stack
//
// A bytecode call runs its callee on its caller's register stack, so the
// callers of a frame on one stack are the frames below it. A callback
// started from Go (an `Iter.map` lambda, a task body) runs on a stack of its
// own. A profiling machine links that stack to the frame that was on top of
// the goroutine's previous stack when it crossed into Go (regStack.profParent),
// and names the host function it crossed through (profVia). The goroutine's
// current stack is kept in a profThread, carried on the rt frame lineage in
// the slot a debugger uses for its thread (rt.Frame.DebugThread): a debug
// machine does not profile. A task's frame gets a thread of its own, so a
// walk never leaves its goroutine.
//
// A caller's line is not known: a plain machine does not save a caller's pc.
// Each caller frame is its function, at its declaration; the innermost
// frame has the line of the call or loop where the sample was taken.
//
// A tail call replaces its caller, which is then gone from the stack, as in
// any language with tail calls. One exception keeps a profile readable: a
// function that ends in a pipeline tail-calls into the stdlib
// (`Iter.sum`), and the frame records the program function that call was in
// (frame.stdCaller), which the profile shows under the stdlib function.
//
// # Wall time
//
// Ticks come from a timer, so the profile is of wall time on the threads
// running Nomi code: a host call that waits (a read, a sleep, `Task.await`)
// is charged with its wait. The collector runs on other threads beside the
// program; its CPU time is reported separately, from runtime/metrics.

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// ProfileInterval is how often a profiler samples.
const ProfileInterval = time.Millisecond

// Profiler samples a running program's logical call stack. NewProfiler makes
// one, WithProfiler runs a machine under it, Start and Stop bound the
// sampling, and WriteTable and WritePprof report it.
type Profiler struct {
	interval time.Duration
	pending  atomic.Int64
	stop     chan struct{}
	done     chan struct{}
	started  time.Time
	elapsed  time.Duration
	gcCPU    float64
	gcCycles uint64

	mu     sync.Mutex
	nodes  []profNode
	ofFunc map[*ir.Func]int
	ofHost map[string]int
	stacks map[string]*profStack
	total  int64
}

// profNode is one frame a sample can hold: a Nomi function, at the line its
// body starts, or a host function.
type profNode struct {
	name string
	file string
	line int
	// col tells two lambdas on one line apart.
	col  int
	host bool
	std  bool
}

// lambda reports whether n is a lambda, which has no name of its own.
func (n profNode) lambda() bool { return n.name == "<lambda>" }

// profStack is one distinct stack and how many ticks it took. nodes is
// innermost first; leaf is the line the innermost frame was at.
type profStack struct {
	nodes    []int
	leafFile string
	leafLine int
	ticks    int64
}

// profThread is the stack a goroutine is running Nomi code on, and the host
// function it is crossing through, for the next stack it starts.
type profThread struct {
	stk *regStack
	via string
}

// NewProfiler is a profiler that samples every interval, or every
// ProfileInterval when interval is zero.
func NewProfiler(interval time.Duration) *Profiler {
	if interval <= 0 {
		interval = ProfileInterval
	}
	return &Profiler{
		interval: interval,
		ofFunc:   map[*ir.Func]int{},
		ofHost:   map[string]int{},
		stacks:   map[string]*profStack{},
	}
}

// WithProfiler is m with every run it starts sampled by p. A debug machine
// is not profiled.
func (m *Machine) WithProfiler(p *Profiler) *Machine {
	if p == nil || m.dbg != nil {
		return m
	}
	run := *m
	run.prof, run.hooked = p, true
	run.fuel = &fuel{watch: true, outer: m.fuel, prof: p}
	return &run
}

// Start begins sampling.
func (p *Profiler) Start() {
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	p.started = time.Now()
	p.gcCPU, p.gcCycles = gcMetrics()
	go func() {
		defer close(p.done)
		t := time.NewTicker(p.interval)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
				p.pending.Add(1)
			}
		}
	}()
}

// Stop ends sampling. Ticks no safe point took are dropped.
func (p *Profiler) Stop() {
	if p.stop == nil {
		return
	}
	close(p.stop)
	<-p.done
	p.stop = nil
	p.elapsed = time.Since(p.started)
	cpu, cycles := gcMetrics()
	p.gcCPU, p.gcCycles = cpu-p.gcCPU, cycles-p.gcCycles
}

func gcMetrics() (cpu float64, cycles uint64) {
	s := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindFloat64 {
		cpu = s[0].Value.Float64()
	}
	if s[1].Value.Kind() == metrics.KindUint64 {
		cycles = s[1].Value.Uint64()
	}
	return cpu, cycles
}

// due reports whether ticks are waiting for a safe point.
func (p *Profiler) due() bool { return p.pending.Load() > 0 }

// take charges the pending ticks to the logical stack fr is on, at pos, or
// at the instruction pc when pos is not valid, or at its function's start
// when pc is -1 too. host, when not empty, is a host function fr called at
// pos, which is the innermost frame.
func (p *Profiler) take(fr *frame, pc int, host string, pos ir.Pos) {
	n := p.pending.Swap(0)
	if n <= 0 || fr == nil || fr.c == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var key strings.Builder
	nodes := make([]int, 0, 16)
	if host != "" {
		nodes = append(nodes, p.hostNode(host))
	}
	switch {
	case pos.IsValid():
	case pc >= 0:
		pos = fr.c.posNear(pc)
	default:
		pos = fr.c.posNear(0)
	}
	for f, depth := fr, 0; f != nil && f.c != nil && depth < 512; depth++ {
		nodes = append(nodes, p.funcNode(f.fn))
		if f.stdEntry != nil && f.stdCaller != nil {
			// A tail call from the program into the stdlib replaced the
			// program's function; the profile keeps it under the stdlib's.
			nodes = append(nodes, p.funcNode(f.stdCaller))
		}
		if f.level > 0 {
			f = f.stk.frames[f.level-1]
			continue
		}
		if f.stk.profVia != "" {
			nodes = append(nodes, p.hostNode(f.stk.profVia))
		}
		f = f.stk.profParent
	}
	for _, id := range nodes {
		key.WriteString(strconv.Itoa(id))
		key.WriteByte(',')
	}
	key.WriteString(pos.File())
	key.WriteByte(':')
	key.WriteString(strconv.Itoa(pos.Line()))
	k := key.String()
	s := p.stacks[k]
	if s == nil {
		s = &profStack{nodes: nodes, leafFile: pos.File(), leafLine: pos.Line()}
		p.stacks[k] = s
	}
	s.ticks += n
	p.total += n
}

func (p *Profiler) funcNode(fn *ir.Func) int {
	if id, ok := p.ofFunc[fn]; ok {
		return id
	}
	pos := fn.Pos()
	id := len(p.nodes)
	p.nodes = append(p.nodes, profNode{name: profName(frameName(fn)), file: pos.File(), line: pos.Line(), col: pos.Col(), std: pos.Stdlib()})
	p.ofFunc[fn] = id
	return id
}

func (p *Profiler) hostNode(name string) int {
	if id, ok := p.ofHost[name]; ok {
		return id
	}
	id := len(p.nodes)
	p.nodes = append(p.nodes, profNode{name: profName(hostadapt.DisplayName(name)), host: true})
	p.ofHost[name] = id
	return id
}

// profName is a function's name as a reader writes it: a std function's
// linkage name starts with its module (`iter.Iter.sum`), which the owner
// after it already names.
func profName(name string) string {
	module, rest, ok := strings.Cut(name, ".")
	if !ok || rest == "" || rest[0] < 'A' || rest[0] > 'Z' {
		return name
	}
	for _, r := range module {
		if (r < 'a' || r > 'z') && r != '_' && (r < '0' || r > '9') {
			return name
		}
	}
	return rest
}

// profThreadOf is the profiler's record of the goroutine runtime runs on.
func profThreadOf(runtime *rt.Frame) *profThread {
	if runtime == nil {
		return nil
	}
	th, _ := runtime.DebugThread().(*profThread)
	if th == nil {
		th = &profThread{}
		runtime.SetDebugThread(th)
	}
	return th
}

// profActivation runs an activation activateSlot opened on a profiling
// machine. One that arrived from Go runs on a stack of its own, which it
// links to the frame on top of the goroutine's previous stack.
//
// Ticks that came due while the activation ran and that no call or
// backward branch in it took are its own: a function whose body neither
// calls nor loops takes them as it returns.
func (m *Machine) profActivation(fr *frame, pooled bool) error {
	if !pooled {
		return m.exec(fr)
	}
	th := profThreadOf(fr.runtime)
	if th == nil {
		return m.exec(fr)
	}
	stk, prev := fr.stk, th.stk
	stk.profParent, stk.profVia = nil, th.via
	if prev != nil && prev.nf > 0 {
		stk.profParent = prev.frames[prev.nf-1]
	}
	th.stk, th.via = stk, ""
	defer func() {
		th.stk, th.via = prev, stk.profVia
		stk.profParent, stk.profVia = nil, ""
	}()
	return m.exec(fr)
}

// profReturn takes the ticks due at fr's return, which execProfiled calls
// before it closes fr's window: every activation, a bytecode call's
// included, passes through exec.
func (m *Machine) profReturn(fr *frame) {
	if m.prof.due() {
		m.prof.take(fr, -1, "", ir.Pos{})
	}
}

// profCross runs a crossing into Go from fr on a profiling machine: a
// callback the host function starts names it, and ticks that came due
// while it ran are its own.
func (m *Machine) profCross(fr *frame, n *ir.Call, name string, cross func() (any, error)) (any, error) {
	if m.prof.due() {
		// Ticks that came due in fr before the call are fr's, at the call.
		m.prof.take(fr, -1, "", n.Pos())
	}
	th := profThreadOf(fr.runtime)
	var via string
	if th != nil {
		via, th.via = th.via, name
	}
	v, err := cross()
	if th != nil {
		th.via = via
	}
	if m.prof.due() {
		m.prof.take(fr, -1, name, n.Pos())
	}
	return v, err
}

// hookedCallHost is opHost's call on a debug or profiling machine: one
// test in the loop for both.
func (m *Machine) hookedCallHost(fr *frame, h *hostCall, args []any) (any, error) {
	if m.dbg != nil {
		return m.debugCallHost(fr, h, args)
	}
	return m.profCallHost(fr, h, args)
}

// profCallHost is opHost's call on a profiling machine.
func (m *Machine) profCallHost(fr *frame, h *hostCall, args []any) (any, error) {
	return m.profCross(fr, h.call, h.name, func() (any, error) {
		if h.inPlace != nil && fr.owned&h.need == h.need {
			// A profiling machine reads no values: it writes an owned
			// collection in place as a plain one does (inplace.go).
			return h.inPlace(m.boundAt(fr, h.name), h.call.Pos(), args)
		}
		if h.intr != nil {
			return h.intr(m.boundAt(fr, h.name), h.call.Pos(), args)
		}
		return callAdapter(h.fn, fr.runtime, args)
	})
}

// profRow is one function's share of a profile.
type profRow struct {
	node      profNode
	self, cum int64
}

// rows is each node's self and cumulative ticks, most self first.
func (p *Profiler) rows() []profRow {
	rows := make([]profRow, len(p.nodes))
	for i, n := range p.nodes {
		rows[i].node = n
	}
	seen := make([]int, len(p.nodes))
	mark := 0
	for _, s := range p.stacks {
		mark++
		if len(s.nodes) > 0 {
			rows[s.nodes[0]].self += s.ticks
		}
		for _, id := range s.nodes {
			if seen[id] != mark {
				seen[id] = mark
				rows[id].cum += s.ticks
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].self != rows[j].self {
			return rows[i].self > rows[j].self
		}
		return rows[i].cum > rows[j].cum
	})
	return rows
}

// WriteTable writes the profile as text: the functions by their own time,
// with their time including callees, then the lines the innermost frames
// were at. name shortens a file path for display; nil leaves it.
func (p *Profiler) WriteTable(w io.Writer, name func(string) string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == nil {
		name = filepath.Base
	}
	tick := p.interval
	dur := func(n int64) string { return fmt.Sprintf("%.2fs", (time.Duration(n) * tick).Seconds()) }
	pct := func(n int64) string {
		if p.total == 0 {
			return "0.0%"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(p.total))
	}
	where := func(n profNode) string {
		at := strconv.Itoa(n.line)
		if n.lambda() {
			at += ":" + strconv.Itoa(n.col)
		}
		switch {
		case n.host:
			return "(host)"
		case n.file == "":
			return ""
		case n.std:
			return "std " + filepath.Base(n.file) + ":" + at
		}
		return name(n.file) + ":" + at
	}

	fmt.Fprintf(w, "nomi profile: %s sampled over %.2fs, one sample every %s\n", dur(p.total), p.elapsed.Seconds(), p.interval)
	fmt.Fprintf(w, "garbage collector: %.2fs of CPU over %d cycles, on threads beside the program\n\n", p.gcCPU, p.gcCycles)
	if p.total == 0 {
		fmt.Fprintln(w, "no samples: the program finished before the first one")
		return
	}

	fmt.Fprintf(w, "%8s %7s %8s %7s  %s\n", "self", "", "total", "", "function")
	shown := 0
	for _, r := range p.rows() {
		// A row under 0.5% of its own and under 5% with its callees says
		// nothing a reader acts on.
		if shown == 25 {
			break
		}
		if r.self*200 < p.total && r.cum*20 < p.total {
			continue
		}
		shown++
		fmt.Fprintf(w, "%8s %7s %8s %7s  %s  %s\n", dur(r.self), pct(r.self), dur(r.cum), pct(r.cum), r.node.name, where(r.node))
	}

	type lineRow struct {
		file  string
		line  int
		fn    string
		ticks int64
	}
	lines := map[string]*lineRow{}
	for _, s := range p.stacks {
		if s.leafFile == "" || len(s.nodes) == 0 {
			continue
		}
		fn := p.nodes[s.nodes[0]].name
		k := s.leafFile + ":" + strconv.Itoa(s.leafLine) + " " + fn
		r := lines[k]
		if r == nil {
			r = &lineRow{file: s.leafFile, line: s.leafLine, fn: fn}
			lines[k] = r
		}
		r.ticks += s.ticks
	}
	sorted := make([]*lineRow, 0, len(lines))
	for _, r := range lines {
		sorted = append(sorted, r)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ticks != sorted[j].ticks {
			return sorted[i].ticks > sorted[j].ticks
		}
		return sorted[i].file+strconv.Itoa(sorted[i].line) < sorted[j].file+strconv.Itoa(sorted[j].line)
	})
	fmt.Fprintf(w, "\n%8s %7s  %s\n", "self", "", "line")
	for i, r := range sorted {
		if i == 15 {
			break
		}
		fmt.Fprintf(w, "%8s %7s  %s:%d  %s\n", dur(r.ticks), pct(r.ticks), name(r.file), r.line, r.fn)
	}
}

// WritePprof writes the profile in the format `go tool pprof` reads: a
// gzipped profile.proto whose functions are the program's, with samples of
// wall time.
func (p *Profiler) WritePprof(w io.Writer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var e protoEnc
	strs := map[string]int{"": 0}
	strtab := []string{""}
	str := func(s string) int {
		if i, ok := strs[s]; ok {
			return i
		}
		strs[s] = len(strtab)
		strtab = append(strtab, s)
		return len(strtab) - 1
	}
	valueType := func(field int, typ, unit string) {
		var vt protoEnc
		vt.int(1, int64(str(typ)))
		vt.int(2, int64(str(unit)))
		e.bytes(field, vt.b.Bytes())
	}
	valueType(1, "samples", "count")
	valueType(1, "wall", "nanoseconds")

	// A location per function, at the line its body starts, and one per
	// innermost line a sample was taken at.
	type locKey struct {
		node int
		line int
	}
	locs := map[locKey]int{}
	var locOrder []locKey
	loc := func(node, line int) uint64 {
		k := locKey{node, line}
		if id, ok := locs[k]; ok {
			return uint64(id)
		}
		locs[k] = len(locOrder) + 1
		locOrder = append(locOrder, k)
		return uint64(len(locOrder))
	}
	keys := make([]string, 0, len(p.stacks))
	for k := range p.stacks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := p.stacks[k]
		var sample protoEnc
		ids := make([]uint64, len(s.nodes))
		for i, id := range s.nodes {
			line := p.nodes[id].line
			if i == 0 && s.leafLine > 0 && !p.nodes[id].host {
				line = s.leafLine
			}
			ids[i] = loc(id, line)
		}
		sample.packed(1, ids)
		sample.packedInt(2, []int64{s.ticks, s.ticks * int64(p.interval)})
		e.bytes(2, sample.b.Bytes())
	}
	for i, k := range locOrder {
		var l, line protoEnc
		line.int(1, int64(k.node+1))
		line.int(2, int64(k.line))
		l.int(1, int64(i+1))
		l.bytes(4, line.b.Bytes())
		e.bytes(4, l.b.Bytes())
	}
	for i, n := range p.nodes {
		var f protoEnc
		fname := n.name
		if n.lambda() {
			// Lambdas have no names; their positions tell them apart.
			fname = fmt.Sprintf("<lambda %s:%d:%d>", filepath.Base(n.file), n.line, n.col)
		}
		f.int(1, int64(i+1))
		f.int(2, int64(str(fname)))
		f.int(3, int64(str(fname)))
		f.int(4, int64(str(n.file)))
		f.int(5, int64(n.line))
		e.bytes(5, f.b.Bytes())
	}
	e.int(9, p.started.UnixNano())
	e.int(10, int64(p.elapsed))
	var period protoEnc
	period.int(1, int64(str("wall")))
	period.int(2, int64(str("nanoseconds")))
	e.bytes(11, period.b.Bytes())
	e.int(12, int64(p.interval))
	// The string table is field 6; its order is the indices above.
	for _, s := range strtab {
		e.bytes(6, []byte(s))
	}
	zw := gzip.NewWriter(w)
	if _, err := zw.Write(e.b.Bytes()); err != nil {
		return err
	}
	return zw.Close()
}

// protoEnc writes protocol buffer fields: just the varint and
// length-delimited kinds a profile.proto uses.
type protoEnc struct{ b bytes.Buffer }

func (e *protoEnc) varint(v uint64) {
	for v >= 0x80 {
		e.b.WriteByte(byte(v) | 0x80)
		v >>= 7
	}
	e.b.WriteByte(byte(v))
}

func (e *protoEnc) int(field int, v int64) {
	if v == 0 {
		return
	}
	e.varint(uint64(field) << 3)
	e.varint(uint64(v))
}

func (e *protoEnc) bytes(field int, b []byte) {
	e.varint(uint64(field)<<3 | 2)
	e.varint(uint64(len(b)))
	e.b.Write(b)
}

func (e *protoEnc) packed(field int, vs []uint64) {
	var inner protoEnc
	for _, v := range vs {
		inner.varint(v)
	}
	e.bytes(field, inner.b.Bytes())
}

func (e *protoEnc) packedInt(field int, vs []int64) {
	us := make([]uint64, len(vs))
	for i, v := range vs {
		us[i] = uint64(v)
	}
	e.packed(field, us)
}

// StartProfile runs m under a profiler when spec, the value of NOMI_PROFILE,
// asks for one. "1" prints the profile's table to errOut when the run ends;
// any other value names a file the profile is also written to, in pprof's
// format. finish stops the sampling and reports; with spec empty, m is
// unchanged and finish does nothing.
func StartProfile(m *Machine, spec string, errOut io.Writer) (run *Machine, finish func()) {
	if spec == "" || spec == "0" || m.dbg != nil {
		return m, func() {}
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	p := NewProfiler(0)
	run = m.WithProfiler(p)
	p.Start()
	return run, func() {
		p.Stop()
		fmt.Fprintln(errOut)
		p.WriteTable(errOut, relativeToWorkingDir)
		if spec == "1" {
			return
		}
		f, err := os.Create(spec)
		if err == nil {
			err = p.WritePprof(f)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			fmt.Fprintf(errOut, "nomi: NOMI_PROFILE: %v\n", err)
			return
		}
		fmt.Fprintf(errOut, "\nwrote %s; go tool pprof -http=: %s shows it as a graph and a flame graph\n", spec, spec)
	}
}

// relativeToWorkingDir is path relative to the working directory when it is
// under it, and its base name otherwise.
func relativeToWorkingDir(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(path)
}
