package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// TestEngineBench times each program under benchmarks/engines on the VM, with
// loading timed separately from execution, and reports the median of five
// runs. It is a measurement, not a check, so it runs only
// when NOMI_ENGINE_BENCH is set:
//
//	NOMI_ENGINE_BENCH=1 go test ./vmhost -run TestEngineBench -v -count=1
//
// Native Go is measured outside Go test: `nomi build` each program and a hello
// world, time each binary, and subtract hello's time.
func TestEngineBench(t *testing.T) {
	if os.Getenv("NOMI_ENGINE_BENCH") == "" {
		t.Skip("set NOMI_ENGINE_BENCH=1 to run the engine benchmark")
	}
	const reps = 5
	files, err := filepath.Glob(filepath.Join("..", "benchmarks", "engines", "*.nomi"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no benchmark programs found: %v", err)
	}
	start := time.Now()
	vmhost.Warm()
	t.Logf("stdlib lowering (once per process): %v", time.Since(start))
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".nomi")
		var vmLoad, vmRun []time.Duration
		for range reps {
			t0 := time.Now()
			p, err := vmhost.Load(path)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			t1 := time.Now()
			var buf bytes.Buffer
			if err := p.Run(context.Background(), &buf, nil, false); err != nil {
				t.Fatalf("%s on the VM: %v", name, err)
			}
			vmLoad, vmRun = append(vmLoad, t1.Sub(t0)), append(vmRun, time.Since(t1))
		}
		t.Logf("%-9s VM load %8v run %8v", name, median(vmLoad), median(vmRun))
	}
}

func median(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2].Round(100 * time.Microsecond)
}
