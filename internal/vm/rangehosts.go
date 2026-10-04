package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

func rangeBacking(v any) (rt.Range[any], error) {
	s, ok := structRecord(v, "ranges.Range")
	if !ok {
		return rt.Range[any]{}, fmt.Errorf("vm: range receiver is %T, want Range", v)
	}
	rawEnd, _ := s.FieldNamed("end")
	endValue, endSome, ok := maybeParts(rawEnd)
	if !ok {
		return rt.Range[any]{}, fmt.Errorf("vm: Range.end must be Maybe")
	}
	rawInclusive, _ := s.FieldNamed("inclusive")
	inclusive, ok := asBool(rawInclusive)
	start, hasStart := s.FieldNamed("start")
	if !ok || !hasStart || start == nil {
		return rt.Range[any]{}, fmt.Errorf("vm: invalid Range fields")
	}
	r := rt.Range[any]{Start: start, Inclusive: inclusive, End: rt.Maybe[any]{Tag: rt.TagNone}}
	if endSome {
		r.End = rt.Maybe[any]{Tag: rt.TagSome, Some: endValue}
	}
	return r, nil
}

func rangeHost(contains bool) hostFn {
	return func(m *Machine, _ ir.Pos, args []any) (any, error) {
		arity := 1
		if contains {
			arity = 3
		}
		if len(args) != arity {
			return nil, fmt.Errorf("vm: range query expected %d operands, got %d", arity, len(args))
		}
		r, err := rangeBacking(args[0])
		if err != nil {
			return nil, err
		}
		if !contains {
			return boolValue(rt.RangeBounded(r)), nil
		}
		cmp, ok := args[2].(*functionValue)
		if !ok || cmp == nil || cmp.body == nil || cmp.arity != 2 {
			return nil, fmt.Errorf("vm: Range.contains? requires a binary comparator")
		}
		var compareErr error
		held := rt.RangeContains(nil, r, args[1], func(_ *rt.Frame, a, b any) rt.Ordering {
			if compareErr != nil {
				return rt.Ordering{Tag: rt.TagEqual}
			}
			v, err := m.activate(cmp.body, []any{a, b}, cmp.captures, m.hostFrame, false, m.depth, nil)
			if err != nil {
				compareErr = err
				return rt.Ordering{Tag: rt.TagEqual}
			}
			if r, isEnumRec := enumRecord(v); !isEnumRec || !isEnum(r, "comparable.Ordering") {
				compareErr = fmt.Errorf("vm: range comparator returned %T, want Ordering", v)
				return rt.Ordering{Tag: rt.TagEqual}
			}
			o, ok := orderingOf(v)
			if !ok {
				compareErr = fmt.Errorf("vm: range comparator returned invalid Ordering")
				return rt.Ordering{Tag: rt.TagEqual}
			}
			return o
		})
		return boolValue(held), compareErr
	}
}

func floatRangeHost(_ *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: Float range query expected 2 operands, got %d", len(args))
	}
	r, err := rangeBacking(args[0])
	if err != nil {
		return nil, err
	}
	start, startOK := r.Start.(float64)
	n, nOK := args[1].(float64)
	if !startOK || !nOK {
		return nil, fmt.Errorf("vm: Float range requires Float operands")
	}
	fr := rt.Range[float64]{Start: start, Inclusive: r.Inclusive, End: rt.Maybe[float64]{Tag: rt.TagNone}}
	if r.End.Tag == rt.TagSome {
		end, ok := r.End.Some.(float64)
		if !ok {
			return nil, fmt.Errorf("vm: Float range requires a Float end")
		}
		fr.End = rt.Maybe[float64]{Tag: rt.TagSome, Some: end}
	}
	return boolValue(rt.RangeContainsFloat(fr, n)), nil
}

// rangeStepBy is `Range.step_by(r, by)`: a lazy sequence the runtime's own
// StepByRangeSeq drives, calling the linked comparator and step body.
func rangeStepBy(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("vm: Range.step_by expected 4 operands, got %d", len(args))
	}
	r, err := rangeBacking(args[0])
	if err != nil {
		return nil, err
	}
	cmp, ok := args[2].(*functionValue)
	if !ok || cmp == nil || cmp.arity != 2 {
		return nil, fmt.Errorf("vm: Range.step_by requires a binary comparator")
	}
	step, ok := args[3].(*functionValue)
	if !ok || step == nil || step.arity != 2 {
		return nil, fmt.Errorf("vm: Range.step_by requires a binary step function")
	}
	return rt.StepByRangeSeq(r, args[1], func(runtime *rt.Frame, a, b any) rt.Ordering {
		return iterOrdering(m.iterInvoke(cmp, runtime, a, b), "range comparator")
	}, func(runtime *rt.Frame, v, by any) rt.Maybe[any] {
		res := m.iterInvoke(step, runtime, v, by)
		payload, isSome, ok := maybeParts(res)
		if !ok {
			panic(iterationFailure{fmt.Errorf("vm: range step returned %T, want Maybe", res)})
		}
		if isSome {
			return rt.Maybe[any]{Tag: rt.TagSome, Some: payload}
		}
		return rt.Maybe[any]{Tag: rt.TagNone}
	}), nil
}

// rangeKnownCount is `Iter.known_count(r)` over a Range: the runtime's own
// RangeKnownCount over the linked `Comparable.compare` and
// `Discrete.steps_between` bodies, as a compiled program calls it.
func rangeKnownCount(m *Machine, _ ir.Pos, args []any) (out any, err error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: Range.known_count expected 3 operands, got %d", len(args))
	}
	r, err := rangeBacking(args[0])
	if err != nil {
		return nil, err
	}
	cmp, ok := args[1].(*functionValue)
	if !ok || cmp == nil || cmp.arity != 2 {
		return nil, fmt.Errorf("vm: Range.known_count requires a binary comparator")
	}
	steps, ok := args[2].(*functionValue)
	if !ok || steps == nil || steps.arity != 2 {
		return nil, fmt.Errorf("vm: Range.known_count requires a binary distance function")
	}
	defer func() {
		if p := recover(); p != nil {
			failure, ok := p.(iterationFailure)
			if !ok {
				panic(p)
			}
			out, err = nil, failure.err
		}
	}()
	return countValue(rt.RangeKnownCount(m.hostFrame, r, func(runtime *rt.Frame, a, b any) rt.Ordering {
		return iterOrdering(m.iterInvoke(cmp, runtime, a, b), "range comparator")
	}, func(runtime *rt.Frame, a, b any) rt.Maybe[int64] {
		return rangeSteps(m.iterInvoke(steps, runtime, a, b))
	})), nil
}

// rangeSteps reads `Discrete.steps_between`'s Maybe<Int> answer.
func rangeSteps(res any) rt.Maybe[int64] {
	payload, isSome, ok := maybeParts(res)
	if !ok {
		panic(iterationFailure{fmt.Errorf("vm: range distance returned %T, want Maybe", res)})
	}
	if !isSome {
		return rt.None[int64]()
	}
	n, isInt := payload.(int64)
	if !isInt {
		panic(iterationFailure{fmt.Errorf("vm: range distance returned Some(%T), want Int", payload)})
	}
	return rt.Some(n)
}
