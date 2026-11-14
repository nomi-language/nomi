package irbuild

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// irInPlaceFixture is the program TestInPlaceUpdates_* lower: each function
// holds struct updates, and its name says which of them, in source order,
// may write in place (`yes`), may when the caller hands over a parameter
// (`own`), and which copy (`no`).
const irInPlaceFixture = `import std::io
import std::channels.{Channel, Sender}

struct P
    x: Int
    y: Int
end

struct Box
    p: P
end

fn keep(p: P) -> Int
    p.x
end

// A literal, then an update of it, then an update of that: each base dies
// at its update.
fn chain_yes_yes(i: Int) -> P
    p = P{x: 0, y: 1}
    q = {..p, x: i}
    {..q, y: i}
end

// A parameter is the activation's only when its caller hands it over.
fn param_own(p: P) -> P
    {..p, x: 1}
end

fn pass_on(p: P) -> P
    p
end

// Handed to a call at its last use, and the callee returns it: the result
// is the caller's again.
fn moved_yes(i: Int) -> P
    p = P{x: i, y: 1}
    q = pass_on(p)
    {..q, x: 2}
end

// Handed to the same call, but read after it: the result may be p.
fn kept_no(i: Int) -> Int
    p = P{x: i, y: 1}
    q = pass_on(p)
    r = {..q, x: 2}
    r.x + p.x
end

// Updated, passed on and returned, round a loop of calls: owned when the
// caller hands it over.
fn threaded_own(p: P, n: Int) -> P
    if n < 1 then p else threaded_own(param_own({..p, y: n}), n - 1)
end

// The base is read after the update.
fn used_after_no(i: Int) -> Int
    p = P{x: 0, y: 1}
    q = {..p, x: i}
    p.x + q.x
end

// A field read before the update and the update's own reads of the base
// leave it eligible.
fn field_reads_yes(i: Int) -> P
    p = P{x: i, y: 1}
    {..p, x: .. + p.y, y: p.x}
end

// Captured by a lambda.
fn captured_no(i: Int) -> Int
    p = P{x: i, y: 1}
    f = fn n: Int -> n + p.x
    q = {..p, x: 2}
    f(q.x)
end

// Passed to a call before the update.
fn passed_no(i: Int) -> P
    p = P{x: i, y: 1}
    _k = keep(p)
    {..p, x: 2}
end

// Passed to a call after the update: the update's result escapes, not the
// base.
fn result_passed_yes(i: Int) -> Int
    p = P{x: i, y: 1}
    keep({..p, x: 2})
end

// Stored in a list.
fn listed_no(i: Int) -> P
    p = P{x: i, y: 1}
    _l = [p]
    {..p, x: 2}
end

// Stored in another struct.
fn boxed_no(i: Int) -> P
    p = P{x: i, y: 1}
    _b = Box{p: p}
    {..p, x: 2}
end

// Sent on a channel.
fn sent_no(i: Int) -> P
    ch = Channel.buffered<P>(1)
    p = P{x: i, y: 1}
    _r = Sender.send(ch.sender, p)
    {..p, x: 2}
end

// One branch updates p and the join reads it: the update is not the last
// use on that path.
fn branch_no(i: Int) -> Int
    p = P{x: i, y: 1}
    q = if i > 0 then {..p, x: 2} else P{x: 3, y: 4}
    q.x + p.x
end

// Each branch's update is the last use of p on its path.
fn branches_yes_yes(i: Int) -> P
    p = P{x: i, y: 1}
    if i > 0 then {..p, x: 2} else {..p, y: 2}
end

// A loop's state written back: the next iteration reads the new value,
// never the old one.
fn loop_yes(n: Int) -> P
    Iter.loop(P{x: 0, y: 0}, fn s ->
        if s.x >= n then break s
        {..s, x: .. + 1}
    end)
end

// A value built before the loop and read by every iteration.
fn loop_reads_old_no(n: Int) -> Int
    start = P{x: 0, y: 0}
    Iter.loop(0, fn i ->
        if i >= n then break i
        q = {..start, x: i}
        i + q.x
    end)
end

// Inside a loop body, the value an earlier statement of the iteration
// built: eligible.
fn loop_body_yes(n: Int) -> Int
    Iter.loop(0, fn i ->
        if i >= n then break i
        p = P{x: i, y: 0}
        q = {..p, y: 1}
        i + q.y
    end)
end

// Rendered (through Debug) before the update.
fn shown_no(i: Int) -> P
    p = P{x: i, y: 1}
    io.inspect(p)
    {..p, x: 2}
end

// An anonymous record.
fn record_yes(i: Int) -> Int
    r = {a: i, b: 2}
    s = {..r, a: 3}
    s.a + s.b
end

// Compared with ==, which may run an impl.
fn compared_no(i: Int) -> Bool
    p = P{x: i, y: 1}
    same = p == P{x: 1, y: 1}
    q = {..p, x: 2}
    same and q.x == 2
end

fn main()
    io.print(chain_yes_yes(1).y)
end
`

// irInPlaceVerdicts lowers the fixture and answers, per function whose
// name ends in its expected verdicts, the verdict of each update in source
// order.
func irInPlaceVerdicts(t *testing.T) map[string][]string {
	t.Helper()
	fns := irRetainedFuncs(t, irInPlaceFixture)
	out := map[string][]string{}
	for name, f := range fns {
		base := name
		if i := strings.LastIndex(base, "."); i >= 0 {
			base = base[i+1:]
		}
		asking := map[*ir.Func]*ir.ResultOwnership{}
		eligible := ir.Owns(f, ir.Callees{Result: func(c *ir.Call) ir.ResultOwnership {
			return irFixtureResult(fns, c.Callee(), asking)
		}}).Updates
		var ups []*ir.Make
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if m, isMake := in.(*ir.Make); isMake && m.Kind() == ir.MakeUpdate {
					ups = append(ups, m)
				}
			}
		}
		sort.SliceStable(ups, func(i, j int) bool {
			pi, pj := ups[i].Pos(), ups[j].Pos()
			if pi.Line() != pj.Line() {
				return pi.Line() < pj.Line()
			}
			return pi.Col() < pj.Col()
		})
		for _, u := range ups {
			v := "no"
			if need, ok := eligible[u]; ok {
				v = "yes"
				if need != 0 {
					v = "own"
				}
			}
			out[base] = append(out[base], v)
		}
	}
	return out
}

// TestInPlaceUpdates_Eligibility: each fixture function's updates are
// eligible exactly as its name says. A lambda's updates are its own
// function's (`loop_yes`' body lowers inline into the loop).
func TestInPlaceUpdates_Eligibility(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	got := irInPlaceVerdicts(t)
	want := map[string]string{
		"chain_yes_yes":     "yes yes",
		"param_own":         "own",
		"moved_yes":         "yes",
		"kept_no":           "no",
		"threaded_own":      "own",
		"used_after_no":     "no",
		"field_reads_yes":   "yes",
		"captured_no":       "no",
		"passed_no":         "no",
		"result_passed_yes": "yes",
		"listed_no":         "no",
		"boxed_no":          "no",
		"sent_no":           "no",
		"branch_no":         "no",
		"branches_yes_yes":  "yes yes",
		"loop_yes":          "yes",
		"loop_reads_old_no": "no",
		"loop_body_yes":     "yes",
		"shown_no":          "no",
		"record_yes":        "yes",
		"compared_no":       "no",
	}
	fns := irRetainedFuncs(t, irInPlaceFixture)
	for name, w := range want {
		g := strings.Join(got[name], " ")
		if g != w {
			var dump strings.Builder
			for fname, f := range fns {
				if strings.HasSuffix(fname, name) {
					irDumpFunc(&dump, f)
				}
			}
			t.Errorf("%s: updates eligible %q, want %q\n%s", name, g, w, dump.String())
		}
	}
}

// irFixtureResult is the ResultOwnership of the fixture function sym names,
// as the VM's ownTable answers it: a function's calls to itself assume its
// result until the answer holds. (The fixture has no mutual recursion.)
func irFixtureResult(fns map[string]*ir.Func, sym *ir.Symbol, asking map[*ir.Func]*ir.ResultOwnership) ir.ResultOwnership {
	for _, f := range fns {
		if f.Sym() != sym {
			continue
		}
		if a, ok := asking[f]; ok {
			return *a
		}
		a := &ir.ResultOwnership{Fresh: true}
		asking[f] = a
		defer delete(asking, f)
		for {
			r := ir.Owns(f, ir.Callees{Result: func(c *ir.Call) ir.ResultOwnership {
				return irFixtureResult(fns, c.Callee(), asking)
			}}).Result
			if r == *a {
				return r
			}
			*a = r
		}
	}
	return ir.ResultOwnership{}
}

// irDumpFunc writes f's blocks for a failure message.
func irDumpFunc(w *strings.Builder, f *ir.Func) {
	w.WriteString("fn " + f.Name() + "\n")
	for _, p := range f.Params() {
		w.WriteString("  param " + p.Temp.String() + "\n")
	}
	for _, b := range f.Blocks() {
		w.WriteString(b.ID().String() + ":")
		if h, has := b.Fault(); has {
			w.WriteString(" fault " + h.String())
		}
		w.WriteString("\n")
		for _, in := range b.Instrs() {
			w.WriteString("  " + in.String() + "\n")
		}
		if t := b.Term(); t != nil {
			w.WriteString("  " + t.String() + "\n")
		}
	}
}
