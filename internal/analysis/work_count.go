package analysis

import (
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ast"
)

// countingBodyChecks turns on bodyChecks, which records each file whose
// bodies CheckTypes walks, once per walk. Only a test counts: it holds an
// edit's recheck to the files the edit can change
// (TestEditWorkStaysFlatAsAProjectGrows).
var (
	countingBodyChecks atomic.Bool
	bodyChecksMu       sync.Mutex
	bodyChecks         []*FileAnalysis
)

// noteBodyCheck records that CheckTypes walks fa's bodies, when counting
// is on.
func noteBodyCheck(fa *FileAnalysis) {
	if !countingBodyChecks.Load() {
		return
	}
	bodyChecksMu.Lock()
	bodyChecks = append(bodyChecks, fa)
	bodyChecksMu.Unlock()
}

// countingProgramWork turns on programWork, which counts each visit a
// program-wide pass makes to a project file, or to an impl a project file
// declares: the visits noteStdlibWork sees whose file is not the
// stdlib's. Only a test counts.
var (
	countingProgramWork atomic.Bool
	programWork         atomic.Int64
	// recordingScans counts the recordings mergeRecordings and
	// buildProjectImplIndex look up to drop duplicates.
	recordingScans atomic.Int64
)

// noteRecordingCompares counts n recordings looked up to drop duplicates,
// when counting is on.
func noteRecordingCompares(n int) {
	if countingProgramWork.Load() {
		recordingScans.Add(int64(n))
	}
}

// noteProgramWork counts a visit to the file with module key or path key,
// or to an impl it declares, when counting is on and the file is not the
// stdlib's.
func noteProgramWork(key string) {
	if countingProgramWork.Load() && !IsStdlibKey(key) {
		programWork.Add(1)
	}
}

// countingDiskReads turns on diskReads, which records the path of each
// project file readProjectFile reads from disk rather than answering from
// what it read before. Only a test counts.
var (
	countingDiskReads atomic.Bool
	diskReadsMu       sync.Mutex
	diskReads         []string
)

// noteDiskRead records that readProjectFile reads path, when counting is on.
func noteDiskRead(path string) {
	if !countingDiskReads.Load() {
		return
	}
	diskReadsMu.Lock()
	diskReads = append(diskReads, path)
	diskReadsMu.Unlock()
}

// countingImportsWalks turns on importsWalks, which counts the files whose
// nodes importsOf walks. Only a test counts.
var (
	countingImportsWalks atomic.Bool
	importsWalks         atomic.Int64
)

// noteImportsWalk counts one file's nodes walked for imports, when
// counting is on.
func noteImportsWalk() {
	if countingImportsWalks.Load() {
		importsWalks.Add(1)
	}
}

// countingBootWork turns on bootWalks and bootGraphs, which count the work
// the boot-only check does (CheckBootScope): each function or test body it
// reads for its calls, with the file it is in, and each program graph it
// builds. Only a test counts (TestEditWorkStaysFlatAsAProjectGrows).
var (
	countingBootWork atomic.Bool
	bootWorkMu       sync.Mutex
	bootWalks        []bootWalk
	bootGraphs       atomic.Int64
)

// bootWalk is one body the boot-only check read, and the file it is in.
type bootWalk struct {
	fa   *FileAnalysis
	body ast.Node
}

// noteBootWalk records that the boot-only check read body, in fa, when
// counting is on.
func noteBootWalk(fa *FileAnalysis, body ast.Node) {
	if !countingBootWork.Load() {
		return
	}
	bootWorkMu.Lock()
	bootWalks = append(bootWalks, bootWalk{fa: fa, body: body})
	bootWorkMu.Unlock()
}

// noteBootGraph counts a program graph the boot-only check built, when
// counting is on.
func noteBootGraph() {
	if countingBootWork.Load() {
		bootGraphs.Add(1)
	}
}
