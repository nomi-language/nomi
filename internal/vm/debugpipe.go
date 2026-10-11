package vm

// THE PIPELINE VIEW: each stage of the `|>` chain a frame is in, or has just
// finished, with the value it produced, as `dbg` would print it after each
// stage.
//
// The builder records each pipeline's stages and the temporary holding each
// one's value (ir.Func.Pipes). A stage that has run holds its value there.
//
// A LAZY STAGE (`|> Iter.map(f)`) produces a sequence that has not run: its
// elements pass through it only when a later stage forces the pipeline. A
// debug machine compiles an opTap after the write of each stage's
// temporary; when the value is a lazy sequence, the tap wraps the
// sequence's run so that it records the elements it passes on, up to
// tapLimit of them, as they pass. Nothing runs that would not have run, in
// no other order: the wrapper yields each element on, and records it, and
// does nothing else. The view shows a lazy stage's elements once the
// pipeline has been forced.

import (
	"errors"
	"strconv"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// tapLimit is how many elements a lazy stage records.
const tapLimit = 100

// stageTap is what one lazy stage passed on in its latest run.
type stageTap struct {
	mu    sync.Mutex
	ran   bool
	items []any
	more  int
}

func (t *stageTap) start() {
	t.mu.Lock()
	t.ran, t.items, t.more = true, nil, 0
	t.mu.Unlock()
}

func (t *stageTap) add(item any) {
	t.mu.Lock()
	if len(t.items) < tapLimit {
		t.items = append(t.items, item)
	} else {
		t.more++
	}
	t.mu.Unlock()
}

// stageTemps are the temporaries of f's pipeline stages.
func stageTemps(f *ir.Func) map[ir.Temp]bool {
	var out map[ir.Temp]bool
	for _, p := range f.Pipes() {
		for _, s := range p.Stages {
			if out == nil {
				out = map[ir.Temp]bool{}
			}
			out[s.Temp] = true
		}
	}
	return out
}

// debugAfter emits an opTap after an instruction that writes a pipeline
// stage's value into a reference register, where a sequence is held.
func (cp *compiler) debugAfter(in ir.Instr) {
	t := in.Dst()
	if t == ir.NoTemp || !cp.c.dbg.stageTemps[t] || int(t) >= len(cp.c.locs) || cp.c.locs[t].bank() != bankR {
		return
	}
	cp.at(in.Pos(), false, -1)
	cp.emit(cp.op(opTap, uint32(t)))
}

// tap wraps the lazy sequence fr's temporary t holds so that it records
// what it passes on.
func (d *Debugger) tap(fr *frame, t ir.Temp) {
	v, err := fr.read(t)
	if err != nil {
		return
	}
	seq, isSeq := v.(rt.Seq[any])
	if !isSeq || seq.Src != nil {
		// A collection's view shows its collection; nothing to record.
		return
	}
	tp := &stageTap{}
	run := seq.Run
	seq.Run = func(rf *rt.Frame, yield func(*rt.Frame, any) bool) bool {
		tp.start()
		return run(rf, func(rf *rt.Frame, item any) bool {
			tp.add(item)
			return yield(rf, item)
		})
	}
	fr.write(t, seq)
	if fr.taps == nil {
		fr.taps = map[ir.Temp]*stageTap{}
	}
	fr.taps[t] = tp
}

// Pipeline is the pipeline a frame Stack handed out is in, or the one that
// finished last before its position: each stage by its source, with its
// value once it has run. Nil when there is none.
func (d *Debugger) Pipeline(frameID int) ([]Variable, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if frameID < 1 || frameID > len(d.frames) {
		return nil, errors.New("vm: no frame " + strconv.Itoa(frameID) + "; the program resumed")
	}
	ref := d.frames[frameID-1]
	if ref.via || ref.fr == nil || ref.fr.c == nil || ref.fr.c.dbg == nil {
		return nil, nil
	}
	fr := ref.fr
	p, found := pipeAt(fr.c.fn, ref.pos)
	if !found {
		return nil, nil
	}
	defined := fr.c.dbg.defined(fr.c.fn)
	var out []Variable
	for _, s := range p.Stages {
		v := Variable{Name: s.Text, Decl: s.Pos}
		val, err := fr.read(s.Temp)
		if err != nil || !defined.Before(ref.point.block, ref.point.index, s.Temp) {
			v.Text = "not run yet"
			v.Value = rt.Inspect(rt.Unit{})
			out = append(out, v)
			continue
		}
		v.Value = rt.Inspect(val)
		if seq, isSeq := val.(rt.Seq[any]); isSeq && seq.Src == nil {
			v.Type = "Iter"
			tp := fr.taps[s.Temp]
			if tp == nil {
				v.Text = "lazy"
			} else {
				tp.mu.Lock()
				ran, items, more := tp.ran, append([]any(nil), tp.items...), tp.more
				tp.mu.Unlock()
				if !ran {
					v.Text = "lazy; not run yet"
				} else {
					v.Value = rt.Inspect(listOf(items))
					v.Text = v.Value.Text()
					if more > 0 {
						v.Text += " and " + strconv.Itoa(more) + " more"
					}
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// pipeAt is the innermost pipeline of f whose span holds pos, or else the
// one that ends last before pos.
func pipeAt(f *ir.Func, pos ir.Pos) (ir.Pipe, bool) {
	var best ir.Pipe
	found := false
	for _, p := range f.Pipes() {
		if p.Holds(pos.Line(), pos.Col()) && (!found || startsAfter(p.Pos, best.Pos)) {
			best, found = p, true
		}
	}
	if found {
		return best, true
	}
	for _, p := range f.Pipes() {
		end := p.Pos
		before := end.EndLine() < pos.Line() || (end.EndLine() == pos.Line() && end.EndCol() < pos.Col())
		if !before {
			continue
		}
		if !found || end.EndLine() > best.Pos.EndLine() ||
			(end.EndLine() == best.Pos.EndLine() && end.EndCol() > best.Pos.EndCol()) {
			best, found = p, true
		}
	}
	return best, found
}
