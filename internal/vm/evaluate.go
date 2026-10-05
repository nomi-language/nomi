package vm

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
type Limits struct {
	Steps    int64
	Deadline time.Time
}

// ErrLimit is what Evaluate answers when the evaluation used up its Limits.
var ErrLimit = errors.New("vm: the evaluation reached its step or time limit")

// fuel is a running evaluation's remaining Limits, shared by every view of the
// machine it runs on. Nil on a machine that is not evaluating, which is every
// machine `nomi run` and `nomi test` open: the check is one nil comparison.
type fuel struct {
	left     int64
	deadline time.Time
	ticks    uint32
}

// burn spends one step. Once the steps are gone every later burn fails too,
// so a program that catches the failure and loops fails again at its next
// call or backward branch.
func (f *fuel) burn() error {
	f.left--
	if f.left < 0 {
		return ErrLimit
	}
	if !f.deadline.IsZero() {
		f.ticks++
		if f.ticks&1023 == 0 && time.Now().After(f.deadline) {
			f.left = -1
			return ErrLimit
		}
	}
	return nil
}

func (f *fuel) spent() bool { return f != nil && f.left < 0 }

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
	run.fuel = &fuel{left: lim.Steps, deadline: lim.Deadline}
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
	"List.head": true, "List.tail": true, "List.concat": true, "List.compare": true,
	"Vector.compare": true, "vm.unreachable": true,
	"Range.contains?": true, "Range.step_by": true, "Range.known_count": true,
	"Range.bounded?": true, "Range.contains?Float": true,
	"Set.size": true, "Set.contains?": true, "Set.insert": true, "Set.remove": true,
	"Set.union": true, "Set.intersection": true, "Set.difference": true,
	"Set.subset?":   true,
	"Vector.length": true, "Vector.at": true, "Vector.push": true, "Vector.concat": true,
	"Vector.set": true, "Vector.next_item": true,
	"Map.get": true, "Map.put": true, "Map.size": true,
	"Map.remove": true, "Map.contains_key?": true, "Map.merge": true, "Map.keys": true,
	"Map.values": true, "Map.map_values": true, "Map.map_keys": true, "Map.map_next": true,

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
	"Regex.compile": true, "Regex.find": true, "Regex.find_all": true, "Regex.match?": true,
	"Regex.pattern": true, "Regex.replace_all": true, "Regex.split": true,

	"strings.String.contains?": true, "strings.String.starts_with?": true,
	"strings.String.ends_with?": true, "strings.String.to_upper": true,
	"strings.String.to_lower": true, "strings.String.replace": true, "strings.String.trim": true,
	"strings.String.hash": true, "strings.string_compare": true, "strings.String.to_int": true,
	"strings.String.split": true, "strings.String.length": true,
	"strings.String.slice": true, "strings.String.reverse": true, "strings.String.to_bytes": true,
	"strings.String.to_codepoints": true, "strings.String.normalize": true,
	// strings.String.repeat is not here: its cost is its Int operand.
	"bytes.Byte.from_int": true, "bytes.Byte.to_int": true, "bytes.Bytes.length": true,
	"bytes.Bytes.at": true, "bytes.Bytes.slice": true, "bytes.Bytes.concat": true,
	"bytes.Bytes.to_string": true, "bytes.Bytes.hash": true,
	"bytes.Bytes.to_list": true, "bytes.Bytes.from_list": true,
	"int.Int.to_string": true, "int.Int.to_float": true, "int.Int.wrapping_add": true,
	"int.Int.wrapping_sub": true, "int.Int.wrapping_mul": true, "int.Int.bitwise_and": true,
	"int.Int.bitwise_or": true, "int.Int.bitwise_xor": true, "int.Int.bitwise_not": true,
	"int.Int.shift_left": true, "int.Int.shift_right": true,
	"float.Float.to_string": true, "float.Float.nan": true, "float.Float.positive_infinity": true,
	"float.Float.negative_infinity": true, "float.Float.nan?": true, "float.Float.round": true,
	"float.Float.floor": true, "float.Float.ceil": true, "float.Float.trunc": true,
	"float.Float.to_int": true, "float.float_bits": true,
	"decimal.Decimal.to_string": true, "decimal.Decimal.equal?": true, "decimal.Decimal.hash": true,
	"decimal.Decimal.compare": true, "decimal.Decimal.from_int": true,
	"decimal.Decimal.from_string": true, "decimal.Decimal.to_int": true,
	"decimal.Decimal.to_float": true, "decimal.Decimal.from_float": true,
	"decimal.Decimal.normalize": true, "decimal.Decimal.scale": true,
	// decimal.Decimal.divide and round are not here: their cost is their
	// scale operand.
	"duration.Duration.to_string": true, "codepoints.Codepoint.to_string": true,
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
