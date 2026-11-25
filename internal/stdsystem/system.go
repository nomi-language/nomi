package stdsystem

import (
	"math"
	"runtime"
	"runtime/metrics"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/rt"
)

// NomiRelease is std/system/host's `nomi_release`: the release the binary
// running the program was built as (buildversion.Release, the version
// std/http's client names in its User-Agent), or None for a build from
// source, which is no release.
func NomiRelease() rt.Option[string] {
	if v := buildversion.Release(); v != "" {
		return rt.Some(v)
	}
	return rt.None[string]()
}

// OS is std/system/host's `os`: Go's name for the operating system the
// program runs on (`linux`, `darwin`, `windows`, `js`, `wasip1`).
func OS() string { return runtime.GOOS }

// Arch is std/system/host's `arch`: Go's name for the processor
// architecture (`amd64`, `arm64`, `wasm`).
func Arch() string { return runtime.GOARCH }

// CPUCount is std/system/host's `cpu_count`: the logical CPUs the process
// may use, which a CPU affinity mask or a container's limit narrows, as
// Go's runtime counts them. A js/wasm or wasip1 module answers 1.
func CPUCount() int64 { return int64(max(runtime.NumCPU(), 1)) }

// memorySamples are the two runtime/metrics readings `memory` answers. A
// metrics.Read does not stop the world, where runtime.ReadMemStats does.
var memorySamples = [...]string{
	"/memory/classes/heap/objects:bytes",
	"/memory/classes/total:bytes",
}

// Memory is std/system/host's `memory`: the bytes the heap's values
// occupy, live or not yet swept, and the bytes the runtime has obtained
// from the operating system for every purpose.
func Memory() (int64, int64) {
	samples := make([]metrics.Sample, len(memorySamples))
	for i, name := range memorySamples {
		samples[i].Name = name
	}
	metrics.Read(samples)
	read := func(s metrics.Sample) int64 {
		if s.Value.Kind() != metrics.KindUint64 {
			return 0
		}
		return int64(min(s.Value.Uint64(), uint64(1)<<62))
	}
	return read(samples[0]), read(samples[1])
}

// gcSamples are the runtime/metrics readings `runtime` answers for the
// garbage collector: its finished cycles, and the histogram of the pauses
// it stopped the program for.
var gcSamples = [...]string{
	"/gc/cycles/total:gc-cycles",
	"/sched/pauses/total/gc:seconds",
}

// Runtime is std/system/host's `runtime`: the process's live tasks, the
// tasks it ever started and its supervisors (rt.RuntimeCounts), the
// garbage collector's finished cycles, and the nanoseconds its pauses
// took. The runtime keeps each pause in a histogram bucket, not its exact
// length, so the total sums each bucket's count times its midpoint (its
// lower bound for the last, open one): an estimate within a bucket's
// width per pause. Nothing here stops the program, as runtime.ReadMemStats
// would.
func Runtime() (live, started, supervisorCount, cycles, pauseNanos int64) {
	live, started, supervisorCount = rt.RuntimeCounts()
	samples := make([]metrics.Sample, len(gcSamples))
	for i, name := range gcSamples {
		samples[i].Name = name
	}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindUint64 {
		cycles = int64(min(samples[0].Value.Uint64(), uint64(1)<<62))
	}
	if samples[1].Value.Kind() == metrics.KindFloat64Histogram {
		h := samples[1].Value.Float64Histogram()
		var seconds float64
		for i, n := range h.Counts {
			lo, hi := h.Buckets[i], h.Buckets[i+1]
			mid := lo
			if !math.IsInf(lo, -1) && !math.IsInf(hi, 1) {
				mid = (lo + hi) / 2
			} else if math.IsInf(lo, -1) {
				mid = 0
			}
			seconds += float64(n) * mid
		}
		pauseNanos = int64(min(seconds*1e9, float64(int64(1)<<62)))
	}
	return live, started, supervisorCount, cycles, pauseNanos
}

// unsupported is the parts of std/system's `Error.Unsupported` for
// operation on this target.
func unsupported(operation string) *rt.List[string] {
	return rt.Cons("Unsupported", rt.Cons(operation, rt.Cons(runtime.GOOS+"/"+runtime.GOARCH, nil)))
}

// unavailable is the parts of std/system's `Error.Unavailable`: the system
// could not answer operation, for the reason err gives.
func unavailable(operation string, err error) *rt.List[string] {
	return rt.Cons("Unavailable", rt.Cons(operation, rt.Cons(err.Error(), nil)))
}

// text answers s, or the parts of the error err is for operation.
func text(operation, s string, err error) rt.Result[string, *rt.List[string]] {
	if err != nil {
		return rt.Err[string](unavailable(operation, err))
	}
	return rt.Ok[string, *rt.List[string]](s)
}
