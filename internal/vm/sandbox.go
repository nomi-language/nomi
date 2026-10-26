package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// sandboxKey is std/fs_host's private `run_on`, which `fs_testing.run`
// and `fs.within` call through `fs_host.run_in`.
const sandboxKey = "fs_host.run_on"

// sandboxHost is `run_on(files, body, finish)`: call body on a frame whose
// file system is files (rt.EnterFiles), a sandbox or a directory `fs.within`
// confines it to, so every std/fs call in it, and in the tasks it starts,
// reaches files, then hand body's value to finish on the caller's frame. A
// trap in body propagates.
func sandboxHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: %s: expected three operands, got %d", sandboxKey, len(args))
	}
	files, ok := args[0].(rt.FileSystem)
	if !ok {
		return nil, fmt.Errorf("vm: %s: the file system is %T, not a FileSystem", sandboxKey, args[0])
	}
	body, ok := args[1].(*functionValue)
	if !ok || body.arity != 0 {
		return nil, fmt.Errorf("vm: %s: body must be a zero-parameter function", sandboxKey)
	}
	finish, ok := args[2].(*functionValue)
	if !ok || finish.arity != 1 {
		return nil, fmt.Errorf("vm: %s: finish must take one parameter", sandboxKey)
	}
	if m.hostFrame == nil {
		return nil, fmt.Errorf("vm: %s: called with no frame", sandboxKey)
	}
	v, err := m.apply(body, nil, rt.EnterFiles(m.hostFrame, files))
	if err != nil {
		return m.settled(nil, err)
	}
	return m.settled(m.apply(finish, []any{v}, m.hostFrame))
}
