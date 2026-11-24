package rt

import "sync/atomic"

// taskCounts counts the process's Nomi tasks, for std/system's `runtime`:
// started is every task ever spawned, by `Task.spawn`, `Task.spawn_all`
// or a supervisor, and live those not yet settled, a task waiting for a
// `max_running` slot included. Both task paths (spawnScopeTask and
// supervisorEnrol) count through taskBegan and taskEnded, so an atomic add
// is all a spawn pays.
var taskCounts struct {
	started, live atomic.Int64
}

// taskBegan counts a task its spawn has started; its goroutine defers
// taskEnded.
func taskBegan() {
	taskCounts.started.Add(1)
	taskCounts.live.Add(1)
}

func taskEnded() { taskCounts.live.Add(-1) }

// RuntimeCounts is the process's live tasks, tasks ever started and
// supervisors, read without stopping anything. A supervisor is made in a
// program's boot and kept for the life of the process, so the count is of
// those made.
func RuntimeCounts() (live, started, supervisorCount int64) {
	reg := supervisors()
	reg.mu.Lock()
	n := int64(len(reg.groups))
	reg.mu.Unlock()
	return taskCounts.live.Load(), taskCounts.started.Load(), n
}
