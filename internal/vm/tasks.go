package vm

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// A `concurrent` block and its tasks run on rt's structured-concurrency
// runtime, over rt values. A task body is a retained function value run on the
// frame rt gives the task, so cancellation reaches its blocking operations. A
// task handle is this package's value; it renders as rt.TaskInspectText.

// taskValue is a `Task<T>` handle.
type taskValue struct{ t rt.Task[any] }

func (*taskValue) OpaqueText() string { return rt.TaskInspectText() }

// rtFault turns an rt trap raised by f into the VM's fault, and a failing VM
// callback driven by rt into its own error. Other panics, including rt's
// cancellation unwind, continue to the task wrapper that recovers them.
func rtFault(f func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if failure, ok := p.(iterationFailure); ok {
				err = failure.err
				return
			}
			if fault, ok := p.(*rt.Error); ok {
				err = &Fault{err: fault}
				return
			}
			panic(p)
		}
	}()
	f()
	return nil
}

// taskLimit is the first machine limit any task body reached, shared by every
// view of one machine.
//
// A task's error settles the task as Failed, as a trap in a Go task body does,
// and `Task.outcome` observes that without failing. A limit of this machine,
// such as a callee the producer did not retain, is not a Nomi fault, so the
// block and the task operations report it once the tasks have settled rather
// than letting a program observe it as a failed task. The machine spells its
// own errors "vm: ...", and a Nomi fault's text is rt's.
type taskLimit struct {
	mu  sync.Mutex
	err error
}

func (l *taskLimit) note(err error) {
	if !strings.HasPrefix(err.Error(), "vm: ") {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		l.err = err
	}
}

func (l *taskLimit) reached() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// runTask runs a task body's function value on the task's frame. A VM error
// becomes the rt fault the task wrapper classifies.
func (m *Machine) runTask(f *functionValue, fr *rt.Frame, args ...any) any {
	// A task body runs on a goroutine of its own, whose stack starts empty,
	// so its call depth starts at zero rather than at the spawner's.
	root := *m
	root.depth = 0
	v, err := root.apply(f, args, fr)
	if err != nil {
		m.tasks.note(err)
		panic(&rt.Error{Msg: err.Error()})
	}
	return v
}

// settled answers a task operation's result, or the machine limit a task
// reached.
func (m *Machine) settled(v any, err error) (any, error) {
	if limit := m.tasks.reached(); limit != nil {
		return nil, limit
	}
	return v, err
}

func nullary(name string, args []any) (*functionValue, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("vm: %s: expected one operand, got %d", name, len(args))
	}
	f, ok := args[0].(*functionValue)
	if !ok || f.arity != 0 {
		return nil, fmt.Errorf("vm: %s: the operand must be a zero-parameter function", name)
	}
	return f, nil
}

// concurrentHost runs a block's body inside a new scope and exits the scope
// on every path: it cancels what is still running and waits for it.
func concurrentHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	f, err := nullary("concurrent", args)
	if err != nil {
		return nil, err
	}
	scope := rt.EnterScope(m.hostFrame)
	v, err := func() (any, error) {
		defer rt.ScopeExit(scope)
		return m.apply(f, nil, scope)
	}()
	return m.settled(v, err)
}

func taskSpawnHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	f, err := nullary("Task.spawn", args)
	if err != nil {
		return nil, err
	}
	var h rt.Task[any]
	if err := rtFault(func() {
		h = rt.TaskSpawn(m.hostFrame, func(fr *rt.Frame) any { return m.runTask(f, fr) })
	}); err != nil {
		return nil, err
	}
	return &taskValue{t: h}, nil
}

// taskHost adapts the task operations that take one handle.
func taskHost(name string) hostFn {
	return func(m *Machine, _ ir.Pos, args []any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("vm: %s: expected one operand, got %d", name, len(args))
		}
		if unit, supervised := args[0].(*unitTaskValue); supervised {
			return m.unitTaskHost(name, unit)
		}
		h, ok := args[0].(*taskValue)
		if !ok {
			return nil, fmt.Errorf("vm: %s: the operand is %T, not a task", name, args[0])
		}
		var out any
		err := rtFault(func() {
			switch name {
			case "Task.await":
				out = rt.TaskAwait(m.hostFrame, h.t)
			case "Task.cancel":
				rt.TaskCancel(h.t)
				out = rt.Unit{}
			case "Task.outcome":
				out = outcomeValue(rt.TaskOutcomeOf(m.hostFrame, h.t))
			}
		})
		if err == nil && out == nil {
			err = fmt.Errorf("vm: %s is not a task operation", name)
		}
		return m.settled(out, err)
	}
}

// outcomeValue builds std/tasks' `Outcome<T>` from an rt outcome.
func outcomeValue(o rt.Outcome[any]) any {
	switch o.Tag {
	case rt.TagCompleted:
		return outcomeDesc.MakeVariant(0, o.Completed)
	case rt.TagCancelled:
		return outcomeDesc.NewVariant(1)
	}
	kind := "Errored"
	if o.Failed.Tag == rt.TagPanicked {
		kind = "Panicked"
	}
	tag := 1 // Errored
	if kind == "Panicked" {
		tag = 0
	}
	return outcomeDesc.MakeVariant(2, failureDesc.MakeVariant(tag, o.Failed.Msg))
}

// taskSpawnAllHost drains the source on the caller's frame and spawns one task
// per item through rt, which bounds how many run at once.
func taskSpawnAllHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: Task.spawn_all: expected three operands, got %d", len(args))
	}
	src, ok := args[0].(rt.Seq[any])
	if !ok {
		return nil, fmt.Errorf("vm: Task.spawn_all: the source is %T, not a sequence", args[0])
	}
	f, ok := args[1].(*functionValue)
	if !ok || f.arity != 1 {
		return nil, fmt.Errorf("vm: Task.spawn_all: the body must be a unary function")
	}
	limit, ok := args[2].(int64)
	if !ok {
		return nil, fmt.Errorf("vm: Task.spawn_all: max_running is %T, not an Int", args[2])
	}
	var handles *rt.List[rt.Task[any]]
	if err := rtFault(func() {
		handles = rt.TaskSpawnAll(m.hostFrame, src, func(fr *rt.Frame, item any) any {
			return m.runTask(f, fr, item)
		}, limit)
	}); err != nil {
		return nil, err
	}
	var out []any
	for c := handles; c != nil; c = c.Tail {
		out = append(out, &taskValue{t: c.Head})
	}
	return listOf(out), nil
}

// taskAwaitAllHost waits for a list of tasks through rt, in spawn order.
func taskAwaitAllHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("vm: Task.await_all: expected one operand, got %d", len(args))
	}
	xs, ok := args[0].(*list)
	if !ok {
		return nil, fmt.Errorf("vm: Task.await_all: the operand is %T, not a list", args[0])
	}
	items := listSlice(xs)
	if len(items) > 0 {
		if _, supervised := items[0].(*unitTaskValue); supervised {
			return m.unitAwaitAll(items)
		}
	}
	var tasks *rt.List[rt.Task[any]]
	for i := len(items) - 1; i >= 0; i-- {
		h, ok := items[i].(*taskValue)
		if !ok {
			return nil, fmt.Errorf("vm: Task.await_all: element %d is %T, not a task", i, items[i])
		}
		tasks = rt.Cons(h.t, tasks)
	}
	var results *rt.List[any]
	if err := rtFault(func() { results = rt.TaskAwaitAll(m.hostFrame, tasks) }); err != nil {
		return m.settled(nil, err)
	}
	var out []any
	for c := results; c != nil; c = c.Tail {
		out = append(out, c.Head)
	}
	return listOf(out), nil
}
