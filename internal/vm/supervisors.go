package vm

import (
	"context"
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Supervisors run on rt's supervisor runtime. A Supervisor is rt.Supervisor
// itself: std's `new_exact` and `flush_bounded` are generated adapters
// (internal/stdlibadapters) that build and read it, so a supervisor made in
// boot has rt's registry, semaphore, restart policy and drain. A supervised task body is a retained function
// value run on the frame rt gives the task; its handle is a `Task<Unit>`.

// unitTaskValue is the `Task<Unit>` handle a supervisor spawn answers. rt
// types a supervised task's result as rt.Unit, so it is a handle of its own
// beside taskValue.
type unitTaskValue struct{ t rt.Task[rt.Unit] }

func (*unitTaskValue) OpaqueText() string { return rt.TaskInspectText() }

// supervisorSpawnHost is `Supervisor.spawn(sup, body)`.
func supervisorSpawnHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("vm: Supervisor.spawn: expected two operands, got %d", len(args))
	}
	sup, ok := args[0].(rt.Supervisor)
	if !ok {
		return nil, fmt.Errorf("vm: Supervisor.spawn: the supervisor is %T", args[0])
	}
	f, err := nullary("Supervisor.spawn", args[1:])
	if err != nil {
		return nil, err
	}
	var h rt.Task[rt.Unit]
	if err := rtFault(func() {
		h = rt.SupervisorSpawn(m.hostFrame, sup, func(fr *rt.Frame) rt.Unit {
			m.runTask(f, fr)
			return rt.Unit{}
		})
	}); err != nil {
		return nil, err
	}
	return &unitTaskValue{t: h}, nil
}

// unitTaskHost answers the one-handle task operations on a supervised task.
func (m *Machine) unitTaskHost(name string, h *unitTaskValue) (any, error) {
	var out any
	err := rtFault(func() {
		switch name {
		case "Task.await":
			rt.TaskAwait(m.hostFrame, h.t)
			out = rt.Unit{}
		case "Task.cancel":
			rt.TaskCancel(h.t)
			out = rt.Unit{}
		case "Task.outcome":
			o := rt.TaskOutcomeOf(m.hostFrame, h.t)
			out = outcomeValue(rt.Outcome[any]{Tag: o.Tag, Completed: rt.Unit{}, Failed: o.Failed})
		}
	})
	if err == nil && out == nil {
		err = fmt.Errorf("vm: %s is not a task operation", name)
	}
	return m.settled(out, err)
}

// unitAwaitAll is `Task.await_all` over supervised handles, whose rt type is
// Task[rt.Unit]; a list's handles share their static type, so every element
// is one.
func (m *Machine) unitAwaitAll(items []any) (any, error) {
	var tasks *rt.List[rt.Task[rt.Unit]]
	for i := len(items) - 1; i >= 0; i-- {
		h, ok := items[i].(*unitTaskValue)
		if !ok {
			return nil, fmt.Errorf("vm: Task.await_all: element %d is %T, not a supervised task", i, items[i])
		}
		tasks = rt.Cons(h.t, tasks)
	}
	var results *rt.List[rt.Unit]
	if err := rtFault(func() { results = rt.TaskAwaitAll(m.hostFrame, tasks) }); err != nil {
		return m.settled(nil, err)
	}
	var out []any
	for c := results; c != nil; c = c.Tail {
		out = append(out, rt.Unit{})
	}
	return m.settled(listOf(out), nil)
}

// withDefaultHost is `Maybe.with_default` and `Result.with_default`: the
// payload of a Some or an Ok, else the default.
func withDefaultHost(present string) hostFn {
	return func(_ *Machine, _ ir.Pos, args []any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("vm: with_default: expected two operands, got %d", len(args))
		}
		v, ok := enumRecord(args[0])
		if !ok {
			return nil, fmt.Errorf("vm: with_default: the receiver is %T", args[0])
		}
		if variantName(v) == present {
			if p, has := payloadOf(v); has {
				return p, nil
			}
		}
		return args[1], nil
	}
}

// IdleSupervisor is a `Supervisor` with no work, made on a boot frame as
// boot's own call makes one: a flush answers Flushed without blocking. A host
// exercising retained functions offers it as an argument, as it offers
// ClosedChannel's halves.
func IdleSupervisor() any {
	fr := rt.EnterBoot(rt.NewFrame(context.Background()))
	s := rt.SupervisorNewExact(fr, 1, rt.DefaultShutdownTimeout, rt.Restart{Tag: 1},
		rt.Backoff{Tag: 1, MaxRestarts: rt.BackoffDefaultMaxRestarts, MaxElapsed: rt.BackoffDefaultMaxElapsed}, rt.GiveUp{Tag: 1})
	return s
}

// supervisorSpawnAllHost is `Supervisor.spawn_all(sup, source, f)`: one
// supervised task per item of the list, through rt, which enrolls each under
// the supervisor's own limit.
func supervisorSpawnAllHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	const name = "Supervisor.spawn_all"
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: %s: expected three operands, got %d", name, len(args))
	}
	sup, ok := args[0].(rt.Supervisor)
	if !ok {
		return nil, fmt.Errorf("vm: %s: the supervisor is %T", name, args[0])
	}
	xs, ok := args[1].(*list)
	if !ok {
		return nil, fmt.Errorf("vm: %s: the source is %T, not a list", name, args[1])
	}
	f, ok := args[2].(*functionValue)
	if !ok || f.arity != 1 {
		return nil, fmt.Errorf("vm: %s: the body must be a unary function", name)
	}
	items := listSlice(xs)
	var source *rt.List[any]
	for i := len(items) - 1; i >= 0; i-- {
		source = rt.Cons(items[i], source)
	}
	var handles *rt.List[rt.Task[rt.Unit]]
	if err := rtFault(func() {
		handles = rt.SupervisorSpawnAll(m.hostFrame, sup, source, func(fr *rt.Frame, item any) rt.Unit {
			m.runTask(f, fr, item)
			return rt.Unit{}
		})
	}); err != nil {
		return nil, err
	}
	var out []any
	for c := handles; c != nil; c = c.Tail {
		out = append(out, &unitTaskValue{t: c.Head})
	}
	return listOf(out), nil
}
