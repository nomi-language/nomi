package vm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"runtime/metrics"
	"sort"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// EVALUATING A FUNCTION FOR AN EDITOR.
//
// The language server runs a typed literal's `from_fragments` while the user
// types, to report a literal its handler rejects. It must not run anything
// that acts on the world, and it must not hang. Effects answers the first
// question before anything runs; Evaluate runs under Limits, which answer the
// second.

// Limits bounds one Evaluate. Steps counts the activations the evaluation
// starts (calls, tail transfers, callbacks from Go), the backward branches it
// takes and the elements its iterations pass along, which are the ways a
// program repeats work. Deadline is a wall-clock backstop, read every 1024
// steps; zero means none. A single crossing into Go is not interrupted, so
// pureCrossings admits only crossings whose cost follows from the size of
// their operands, which the steps bound.
//
// Memory, when not zero, bounds how far the heap grows while the evaluation
// runs, in bytes: the process's heap objects (runtime/metrics), measured
// against the heap when it starts. It is read every few steps, more often
// as the growth nears the bound: a pure crossing at most doubles what its
// operands hold, so between two reads the heap cannot pass twice the bound.
// The process's other goroutines grow the same heap, so the bound is
// approximate; a macro's run sets it as a backstop against one that
// would take the language server down.
type Limits struct {
	Steps    int64
	Deadline time.Time
	Memory   int64
	// Spent, when set, receives the steps the evaluation took.
	Spent *int64
}

// ErrLimit is what Evaluate answers when the evaluation used up its Limits.
var ErrLimit = errors.New("vm: the evaluation reached its step or time limit")

// ErrMemoryLimit is Evaluate's answer when the evaluation grew the heap past
// Limits.Memory. It is an ErrLimit.
var ErrMemoryLimit = fmt.Errorf("%w: memory", ErrLimit)

// ErrDeadline is Evaluate's answer when the evaluation was still running at
// Limits.Deadline. It is an ErrLimit. Unlike the steps, the deadline depends
// on how fast the machine is, so a caller that must answer the same on every
// machine tells it from the step limit.
var ErrDeadline = fmt.Errorf("%w: deadline", ErrLimit)

// heapObjects is the bytes of heap objects now.
func heapObjects() int64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int64(s[0].Value.Uint64())
}

// fuel is a running evaluation's remaining Limits, shared by every view of the
// machine it runs on, a task's included, so its counters are atomic. Nil on a
// machine that is not evaluating, which is every machine `nomi run` and `nomi
// test` open: the check is one nil comparison.
type fuel struct {
	left     atomic.Int64
	deadline time.Time
	// stop is StopOn's context's Done channel, or nil, read with the
	// deadline every 1024 steps. stopped records that it closed.
	stop    <-chan struct{}
	stopped atomic.Bool
	ticks   atomic.Uint32
	// watch says the fuel counts nothing and only answers whether stopped
	// was set: a test case's time limit (caselimit.go), whose timer sets it.
	// A step then costs one atomic load, which no task contends for. outer
	// is the fuel of the machine the case runs on, StopOn's, still burned.
	watch bool
	outer *fuel
	// memory is Limits.Memory, heapBase the heap when the run started,
	// memoryCheck the steps until the heap is read again, and overMemory
	// records that the run passed the bound.
	memory, heapBase int64
	memoryCheck      atomic.Int32
	overMemory       atomic.Bool
	// overDeadline records that the run was stopped at its deadline.
	overDeadline atomic.Bool
}

func newFuel(lim Limits) *fuel {
	f := &fuel{deadline: lim.Deadline, memory: lim.Memory}
	f.left.Store(lim.Steps)
	if f.memory > 0 {
		f.heapBase = heapObjects()
		f.memoryCheck.Store(1)
	}
	return f
}

// checkMemory reads the heap when its countdown is done, and answers
// whether the run is past its bound. The next read is fewer steps away the
// nearer the growth is to the bound: as many as the times the growth could
// double before it passes the bound, less one.
func (f *fuel) checkMemory() bool {
	if f.memoryCheck.Add(-1) > 0 {
		return false
	}
	grown := max(heapObjects()-f.heapBase, 1)
	if grown > f.memory {
		f.overMemory.Store(true)
		return true
	}
	f.memoryCheck.Store(int32(min(max(bits.Len64(uint64(f.memory/grown))-1, 1), 64)))
	return false
}

// burn spends one step. Once the steps are gone every later burn fails too,
// so a program that catches the failure and loops fails again at its next
// call or backward branch.
func (f *fuel) burn() error {
	if f.watch {
		if f.stopped.Load() {
			return ErrStopped
		}
		if f.outer != nil {
			return f.outer.burn()
		}
		return nil
	}
	if f.left.Add(-1) < 0 {
		if f.stopped.Load() {
			return ErrStopped
		}
		if f.overMemory.Load() {
			return ErrMemoryLimit
		}
		if f.overDeadline.Load() {
			return ErrDeadline
		}
		return ErrLimit
	}
	if f.memory > 0 && f.checkMemory() {
		f.left.Store(-1)
		return ErrMemoryLimit
	}
	if (!f.deadline.IsZero() || f.stop != nil) && f.ticks.Add(1)&1023 == 0 {
		if !f.deadline.IsZero() && time.Now().After(f.deadline) {
			f.overDeadline.Store(true)
			f.left.Store(-1)
			return ErrDeadline
		}
		select {
		case <-f.stop:
			f.stopped.Store(true)
			f.left.Store(-1)
			return ErrStopped
		default:
		}
	}
	return nil
}

func (f *fuel) spent() bool { return f != nil && f.left.Load() < 0 }

// WithLimits is m with every run it starts (Main, Call, RunCases) bounded by
// lim as Evaluate's run is: a run that uses them up stops with ErrLimit, and
// Exhausted reports that it did. The limits are shared by every view of the
// returned machine, so it bounds one run. vmhost's run harness
// (vmhost/run_fuzz_test.go) runs generated programs this way so that none
// hangs.
func (m *Machine) WithLimits(lim Limits) *Machine {
	run := *m
	run.fuel = newFuel(lim)
	return &run
}

// ErrStopped is what a run on a machine StopOn gave answers when the
// context ended while it ran.
var ErrStopped = errors.New("vm: the run was stopped")

// StopOn is m with every run it starts (Main, Call, RunCases) ending when
// ctx does. A run blocked in a wait (a timer, a channel, a task) wakes at
// once, because the frames it builds from Go are over ctx; a run that is
// computing stops at its next call, backward branch or iteration element,
// checked as WithLimits' deadline is, with ErrStopped. It replaces any
// limits WithLimits gave. `nomi run --watch` stops a run this way when a
// source file changes.
func (m *Machine) StopOn(ctx context.Context) *Machine {
	run := *m
	run.stopCtx = ctx
	f := newFuel(Limits{Steps: math.MaxInt64})
	f.stop = ctx.Done()
	run.fuel = f
	return &run
}

// Exhausted reports whether a run on m used up the limits WithLimits gave it.
func (m *Machine) Exhausted() bool { return m.fuel.spent() }

// metered is seq burning one step per element it passes along, so an
// iteration driven in Go (`Iter.count(Iter.repeat(x))`) is bounded too. It
// keeps seq's source and count.
func (m *Machine) metered(seq rt.Seq[any]) rt.Seq[any] {
	f, run := m.fuel, seq.Run
	seq.Run = func(fr *rt.Frame, yield func(*rt.Frame, any) bool) bool {
		return run(fr, func(fr *rt.Frame, item any) bool {
			if err := f.burn(); err != nil {
				panic(iterationFailure{err})
			}
			return yield(fr, item)
		})
	}
	return seq
}

// Evaluate calls f with args under lim, on a fresh rt frame of ctx and
// without booting the program: there is no app, so a function that reads an
// app field cannot be evaluated (Effects reports the read). It answers f's
// result, ErrLimit when lim ran out, or the program's own failure.
func (m *Machine) Evaluate(ctx context.Context, f *ir.Func, args []any, lim Limits) (result any, err error) {
	run := *m
	run.fuel = newFuel(lim)
	fr := rt.NewFrame(run.withDiagnostics(ctx))
	run.hostFrame = fr
	defer func() {
		if r := recover(); r != nil {
			e, isFault := r.(*rt.Error)
			if !isFault {
				panic(r)
			}
			result, err = nil, &Fault{err: e}
		}
		if run.fuel.spent() {
			result, err = nil, ErrLimit
			if run.fuel.overMemory.Load() {
				err = ErrMemoryLimit
			} else if run.fuel.overDeadline.Load() {
				err = ErrDeadline
			}
		}
		if lim.Spent != nil {
			*lim.Spent = lim.Steps - max(run.fuel.left.Load(), 0)
		}
	}()
	return run.callWithFrame(f, args, fr)
}

// pureCrossings are the crossings into Go an evaluation may make: each
// computes its answer from its operands alone and acts on nothing outside
// the machine. It is the one list of them; a crossing it does not name is an
// effect, so a host function added later is refused until someone decides it
// belongs here. Intrinsics come first, then the generated stdlib adapters
// (internal/stdlibbindings).
var pureCrossings = map[string]bool{
	"Result.map_err": true, "Maybe.to_result": true,
	"Maybe.with_default": true, "Result.with_default": true,
	"List.head": true, "List.tail": true, "List.concat": true, "List.compare": true, "List.contains?": true,
	"Vector.compare": true, "vm.unreachable": true, "vm.hash_value": true,
	"Range.contains?": true, "Range.step_by": true, "Range.known_count": true,
	"Range.bounded?": true, "Range.contains?Float": true,
	"Set.size": true, "Set.contains?": true, "Set.insert": true, "Set.remove": true,
	"Set.union": true, "Set.intersection": true, "Set.difference": true,
	"Set.subset?":   true,
	"Vector.length": true, "Vector.get": true, "Vector.at": true, "Vector.push": true, "Vector.concat": true,
	"Vector.set": true, "Vector.next_item": true, "Vector.contains?": true,
	"Map.get": true, "Map.put": true, "Map.size": true,
	"Map.remove": true, "Map.contains?": true, "Map.merge": true, "Map.keys": true,
	"Map.values": true, "Map.map_values": true, "Map.map_keys": true, "Map.map_next": true,

	"locale.locale_fallbacks": true, "locale.locale_from_posix_raw": true, "locale.locale_language": true,
	"locale.locale_negotiate": true, "locale.locale_parse_raw": true, "locale.locale_region": true,
	"locale.locale_script": true, "locale.locale_available": true,
	"plural.plural_cardinal_tag": true, "plural.plural_cardinal_tags": true,
	"plural.plural_ordinal_tag": true, "plural.plural_ordinal_tags": true,
	// Locale-aware formatting reads the machine's own CLDR data.
	"int.int_format": true, "int.int_parse_in": true,
	"float.float_format": true, "float.float_format_percent": true, "float.float_parse_in": true, "float.float_parse_percent_in": true,
	"decimal.decimal_format": true, "decimal.decimal_format_percent": true,
	"decimal.decimal_parse_in": true, "decimal.decimal_parse_percent_in": true,
	"strings.join_list_raw":      true,
	"collation.collator_new_raw": true, "collation.collator_compare_raw": true, "collation.collator_sort_key_raw": true,
	"collation.collator_inspect_raw": true,
	"calendar.civil_format_style":    true, "calendar.civil_format_fields": true, "calendar.civil_format_pattern": true,
	"calendar.date_parse_style": true, "calendar.time_parse_style": true, "calendar.date_parse_pattern": true,
	"calendar.time_parse_pattern": true, "calendar.naive_parse_pattern": true, "calendar.fields_check": true,
	"calendar.pattern_check": true, "calendar.relative_format_raw": true, "duration.duration_format_raw": true,
	"currency.currency_check": true, "currency.currency_code_of": true, "currency.currency_digits_of": true,
	"money_format.money_format_raw": true, "money_format.money_parse_raw": true,
	// A message's formatting, and the intrinsic that names an enum
	// value's variant.
	"message_format.format_raw": true, "message_format.variant_name": true,

	"Date.days_between": true, "Date.to_string": true,
	"DateTime.day": true, "DateTime.hour": true, "DateTime.minute": true, "DateTime.month": true,
	"DateTime.nanosecond": true, "DateTime.second": true, "DateTime.to_offset": true,
	"DateTime.to_string": true, "DateTime.year": true,
	"NaiveDateTime.to_date": true, "NaiveDateTime.to_string": true, "NaiveDateTime.to_time": true,
	"OffsetDateTime.day": true, "OffsetDateTime.hour": true, "OffsetDateTime.minute": true,
	"OffsetDateTime.month": true, "OffsetDateTime.nanosecond": true, "OffsetDateTime.second": true,
	"OffsetDateTime.to_string": true, "OffsetDateTime.year": true,
	"Time.to_string":         true,
	"calendar.date_add_days": true, "calendar.date_add_months": true, "calendar.date_add_years": true,
	"calendar.date_new_raw": true, "calendar.date_parse_raw": true,
	"calendar.naive_add_days": true, "calendar.naive_add_months": true, "calendar.naive_add_nanos": true,
	"calendar.naive_add_years": true, "calendar.naive_between_nanos": true,
	"calendar.naive_new_exact_raw": true, "calendar.naive_parse_raw": true,
	"calendar.offset_add_months": true, "calendar.offset_add_years": true,
	"calendar.offset_from_instant_nanos": true, "calendar.offset_parse_raw": true,
	"calendar.offset_with_offset_raw": true, "calendar.time_add_nanos": true,
	"calendar.time_parse_raw": true,
	"calendar.zoned_add_days": true, "calendar.zoned_add_hours": true,
	"calendar.zoned_add_microseconds": true, "calendar.zoned_add_milliseconds": true,
	"calendar.zoned_add_minutes": true, "calendar.zoned_add_months": true,
	"calendar.zoned_add_nanoseconds": true, "calendar.zoned_add_seconds": true,
	"calendar.zoned_add_years": true, "calendar.zoned_from_instant_in_raw": true,
	"calendar.zoned_in_zone_raw": true, "calendar.zoned_offset_nanos": true,
	"calendar.zoned_parse_raw": true, "calendar.zoned_with_zone_raw": true,
	"Regex.compile": true, "Regex.find_in": true,
	"Regex.pattern": true, "Regex.replace_all": true,
	"Regex.contained_in?": true, "Regex.prefix_of?": true, "Regex.suffix_of?": true,
	"Regex.split_in": true, "Regex.replace_in": true, "Regex.find_all_in": true,
	"String.captures": true, "String.captures_all": true,
	"String.to_upper_in": true, "String.to_lower_in": true, "String.fold_case": true,

	"strings.String.contained_in?": true, "strings.String.prefix_of?": true,
	"strings.String.suffix_of?": true, "strings.String.find_all_in": true,
	"strings.String.to_upper": true,
	"strings.String.to_lower": true, "strings.String.replace_in": true, "strings.String.trim": true,
	"strings.String.hash": true, "strings.string_compare": true, "strings.String.to_int": true,
	"strings.String.split_in": true, "strings.String.length": true,
	"strings.String.slice": true, "strings.String.reverse": true, "strings.String.to_bytes": true,
	"strings.String.to_codepoints": true, "strings.String.normalize": true, "strings.String.to_literal": true,
	"strings.String.words": true, "strings.String.lines": true,
	"assertions.AssertionFailure.format": true,
	// strings.String.repeat is not here: its cost is its Int operand.
	"int.Int.to_byte": true, "bytes.Byte.to_int": true, "bytes.Bytes.length": true,
	"bytes.Bytes.get": true, "bytes.Bytes.at": true, "bytes.Bytes.slice": true, "bytes.Bytes.concat": true,
	"bytes.Bytes.to_string": true, "bytes.Bytes.hash": true, "bytes.Bytes.contains?": true,
	"bytes.Bytes.to_list": true, "bytes.Bytes.from_list": true, "bytes.bytes_compare": true,
	"int.Int.to_string": true, "int.Int.to_float": true, "int.Int.wrapping_add": true,
	"int.Int.wrapping_sub": true, "int.Int.wrapping_mul": true, "int.Int.bitwise_and": true,
	"int.Int.bitwise_or": true, "int.Int.bitwise_xor": true, "int.Int.bitwise_not": true,
	"int.Int.shift_left": true, "int.Int.shift_right": true,
	"int.Int.pow": true, "int.Int.sqrt": true, "int.Int.gcd": true, "int.Int.lcm": true,
	"int.int_to_digits": true, "int.int_from_digits": true,
	"float.Float.to_string": true, "float.Float.nan": true, "float.Float.positive_infinity": true,
	"float.Float.negative_infinity": true, "float.Float.nan?": true, "float.float_round": true,
	"float.Float.floor": true, "float.Float.ceil": true, "float.Float.trunc": true,
	"float.Float.to_int": true, "float.float_bits": true,
	"float.Float.pow": true, "float.Float.sqrt": true, "float.Float.exp": true, "float.Float.log": true,
	"float.Float.log2": true, "float.Float.log10": true, "float.Float.sin": true, "float.Float.cos": true,
	"float.Float.tan": true, "float.Float.atan2": true,
	"decimal.Decimal.to_string": true, "decimal.Decimal.equal?": true, "decimal.Decimal.hash": true,
	"decimal.Decimal.compare": true, "int.Int.to_decimal": true,
	"strings.String.to_decimal": true, "decimal.Decimal.to_int": true,
	"decimal.Decimal.to_float": true, "float.Float.to_decimal": true,
	"decimal.Decimal.normalize": true, "decimal.Decimal.scale": true, "decimal.Decimal.divide_exact": true,
	// decimal.Decimal.divide, round and sqrt are not here: their cost is their
	// scale operand. decimal.Decimal.pow is not either: its cost is its
	// exponent.
	"duration.Duration.to_string": true, "code_points.Codepoint.to_string": true,
	"code_points.Codepoint.letter?": true, "code_points.Codepoint.whitespace?": true,
	"code_points.Codepoint.upper?": true, "code_points.Codepoint.lower?": true, "int.Int.to_codepoint": true,
	"json.Json.decode": true, "json.Json.encode": true, "json.Json.to_dynamic": true,
	"dynamic.Dynamic.field": true, "dynamic.Dynamic.index": true, "dynamic.Dynamic.path": true,
	"dynamic.Dynamic.as_string": true, "dynamic.Dynamic.as_int": true,
	"dynamic.Dynamic.as_float": true, "dynamic.Dynamic.as_bool": true,
	"dynamic.Dynamic.as_list": true, "dynamic.Dynamic.as_dict": true,
	"dynamic.Dynamic.null?": true, "dynamic.Dynamic.has?": true, "dynamic.Dynamic.inspect": true,
}

// Effects walks everything f can reach, as Unretained does, and answers what
// stops it being evaluated for an editor, sorted: each crossing into Go that
// pureCrossings does not name (output, files, the network, the clock,
// randomness, tasks, channels, the Context), each read or write of the running
// app's fields, Context or scope, and each declaration this machine could not
// run. Nil means f computes its result from its operands alone.
//
// It over-approximates as the reachability walk does: a dispatched call
// reaches every implementation of its method, and a function value reaches
// every body the evaluation creates a value of.
func (m *Machine) Effects(f *ir.Func) []string {
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{}, effects: map[string]bool{}}
	w.push(f)
	for len(w.queue) > 0 {
		next := w.queue[len(w.queue)-1]
		w.queue = w.queue[:len(w.queue)-1]
		w.visit(next)
	}
	for _, u := range w.found {
		w.effects[fmt.Sprintf("%s cannot run here (reached from %s)", u.Name, u.From)] = true
	}
	if len(w.effects) == 0 {
		return nil
	}
	out := make([]string, 0, len(w.effects))
	for e := range w.effects {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// effectOf is the effect one instruction has, or "".
func effectOf(in ir.Instr, from string) string {
	switch n := in.(type) {
	case *ir.Ref:
		switch n.Kind() {
		case ir.RefAppField, ir.RefContext, ir.RefScope:
			name := ""
			if n.Sym() != nil {
				name = " " + n.Sym().Name()
			}
			return fmt.Sprintf("%s reads the running app (%s%s)", from, n.Kind(), name)
		}
	case *ir.Store:
		return fmt.Sprintf("%s writes the running app", from)
	case *ir.Call:
		if n.Crosses() && !pureCrossings[n.Callee().Name()] {
			return fmt.Sprintf("%s calls %s", from, n.Callee().Name())
		}
	case *ir.Defer:
		if c := n.Call(); c != nil && c.Crosses() && !pureCrossings[c.Callee().Name()] {
			return fmt.Sprintf("%s calls %s", from, c.Callee().Name())
		}
	}
	return ""
}
