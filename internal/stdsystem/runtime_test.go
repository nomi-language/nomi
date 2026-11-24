package stdsystem

import (
	"runtime"
	"testing"
)

// A collection the test forces is one more finished cycle, and its pause
// adds to the total, which never falls; no task runs here, so the task
// counts stand still.
func TestRuntime_CountsTheCollectorsCycles(t *testing.T) {
	live, started, _, cycles, pause := Runtime()
	runtime.GC()
	live2, started2, _, cycles2, pause2 := Runtime()
	if cycles2 <= cycles {
		t.Errorf("a forced collection leaves the cycles at %d, from %d", cycles2, cycles)
	}
	if pause2 < pause || pause < 0 {
		t.Errorf("the collector's pause goes from %d to %d nanoseconds", pause, pause2)
	}
	if live2 != live || started2 != started {
		t.Errorf("with no task spawned, the counts move from (%d, %d) to (%d, %d)", live, started, live2, started2)
	}
}
