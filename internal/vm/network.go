package vm

import (
	"fmt"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// The crossings behind `http_testing.run`, std/http_host's private
// run_on, answer_on and recorded_on: the fake is a Nomi function, so the
// machine installs it and calls it, as it calls a sandbox's body.
const (
	networkFakeKey     = "http_host.run_on"
	networkAnswerKey   = "http_host.answer_on"
	networkRecordedKey = "http_host.recorded_on"
	networkRequestKey  = "http_host.request_on"
)

// networkMu guards each fake's list of answered requests: tasks inside one
// `http_testing.run` may send at once.
var networkMu sync.Mutex

// networkFakeHost is `run_on(answer, body, finish)`: call body on a frame
// whose std/http requests answer calls (rt.EnterNetwork), and in the tasks
// it starts, then hand body's value to finish on the caller's frame.
func networkFakeHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: %s: expected three operands, got %d", networkFakeKey, len(args))
	}
	answer, ok := args[0].(*functionValue)
	if !ok || answer.arity != 1 {
		return nil, fmt.Errorf("vm: %s: the answer must take one parameter", networkFakeKey)
	}
	body, ok := args[1].(*functionValue)
	if !ok || body.arity != 0 {
		return nil, fmt.Errorf("vm: %s: body must be a zero-parameter function", networkFakeKey)
	}
	finish, ok := args[2].(*functionValue)
	if !ok || finish.arity != 1 {
		return nil, fmt.Errorf("vm: %s: finish must take one parameter", networkFakeKey)
	}
	if m.hostFrame == nil {
		return nil, fmt.Errorf("vm: %s: called with no frame", networkFakeKey)
	}
	fake := rt.NetworkFake{Answer: answer, Answered: new([]any)}
	v, err := m.apply(body, nil, rt.EnterNetwork(m.hostFrame, fake))
	if err != nil {
		return m.settled(nil, err)
	}
	return m.settled(m.apply(finish, []any{v}, m.hostFrame))
}

// networkAnswerHost is `answer_on(request, found, missing)`: with a fake
// installed, record request, call the fake with it and hand its answer to
// found; with none, call missing, which sends on the network.
func networkAnswerHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: %s: expected three operands, got %d", networkAnswerKey, len(args))
	}
	found, ok := args[1].(*functionValue)
	if !ok || found.arity != 1 {
		return nil, fmt.Errorf("vm: %s: found must take one parameter", networkAnswerKey)
	}
	missing, ok := args[2].(*functionValue)
	if !ok || missing.arity != 0 {
		return nil, fmt.Errorf("vm: %s: missing must take no parameter", networkAnswerKey)
	}
	if m.hostFrame == nil {
		return nil, fmt.Errorf("vm: %s: called with no frame", networkAnswerKey)
	}
	fake := rt.NetworkOf(m.hostFrame)
	if fake == nil {
		return m.settled(m.apply(missing, nil, m.hostFrame))
	}
	networkMu.Lock()
	*fake.Answered = append(*fake.Answered, args[0])
	networkMu.Unlock()
	answered, err := m.apply(fake.Answer.(*functionValue), []any{args[0]}, m.hostFrame)
	if err != nil {
		return m.settled(nil, err)
	}
	return m.settled(m.apply(found, []any{answered}, m.hostFrame))
}

// networkRecordedHost is `recorded_on(found)`: hand found the requests the
// installed fake has answered so far, in order (none with no fake).
func networkRecordedHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("vm: %s: expected one operand, got %d", networkRecordedKey, len(args))
	}
	found, ok := args[0].(*functionValue)
	if !ok || found.arity != 1 {
		return nil, fmt.Errorf("vm: %s: found must take one parameter", networkRecordedKey)
	}
	if m.hostFrame == nil {
		return nil, fmt.Errorf("vm: %s: called with no frame", networkRecordedKey)
	}
	var list *rt.List[any]
	if fake := rt.NetworkOf(m.hostFrame); fake != nil {
		networkMu.Lock()
		answered := append([]any(nil), *fake.Answered...)
		networkMu.Unlock()
		for i := len(answered) - 1; i >= 0; i-- {
			list = rt.ConsCell[any, rt.List[any]](answered[i], list)
		}
	}
	return m.settled(m.apply(found, []any{list}, m.hostFrame))
}

// networkRequestHost is `request_on(incoming, timeout, body, finish)`: call
// body with the request's context (the one in force, with a deadline timeout
// nanoseconds away unless timeout is negative) on a frame whose app Context
// field reads it and which stops when the client goes away
// (rt.EnterRequest), then hand its value to finish on the caller's frame.
func networkRequestHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("vm: %s: expected four operands, got %d", networkRequestKey, len(args))
	}
	in, ok := args[0].(rt.HTTPIncoming)
	if !ok {
		return nil, fmt.Errorf("vm: %s: the request is %T, not an Incoming", networkRequestKey, args[0])
	}
	timeout, ok := args[1].(int64)
	if !ok {
		return nil, fmt.Errorf("vm: %s: the timeout is %T, not an Int", networkRequestKey, args[1])
	}
	body, ok := args[2].(*functionValue)
	if !ok || body.arity != 1 {
		return nil, fmt.Errorf("vm: %s: body must take one parameter", networkRequestKey)
	}
	finish, ok := args[3].(*functionValue)
	if !ok || finish.arity != 1 {
		return nil, fmt.Errorf("vm: %s: finish must take one parameter", networkRequestKey)
	}
	if m.hostFrame == nil {
		return nil, fmt.Errorf("vm: %s: called with no frame", networkRequestKey)
	}
	c := rt.ActiveContext(m.hostFrame)
	if timeout >= 0 {
		c = rt.ContextWithTimeout(c, rt.Duration(timeout))
	}
	fr, release := rt.EnterRequest(m.hostFrame, in.Done(), c)
	v, err := m.apply(body, []any{c}, fr)
	release()
	if err != nil {
		return m.settled(nil, err)
	}
	return m.settled(m.apply(finish, []any{v}, m.hostFrame))
}
