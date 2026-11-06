package analysis

// BodyChecksForTest runs f with the body-check counter on and answers the
// file analyses CheckTypes walked while it ran, once per walk, in order
// (noteBodyCheck).
func BodyChecksForTest(f func()) []*FileAnalysis {
	bodyChecksMu.Lock()
	bodyChecks = nil
	bodyChecksMu.Unlock()
	countingBodyChecks.Store(true)
	defer countingBodyChecks.Store(false)
	f()
	bodyChecksMu.Lock()
	defer bodyChecksMu.Unlock()
	out := bodyChecks
	bodyChecks = nil
	return out
}

// CountProgramWorkForTest runs f with the program-work counter on and
// answers how many visits to a project file, or to an impl a project file
// declares, the program-wide passes made while it ran (noteProgramWork),
// and how many recordings the impl-index folds looked up to drop
// duplicates (noteRecordingCompares).
func CountProgramWorkForTest(f func()) (visits, scans int64) {
	countingProgramWork.Store(true)
	defer countingProgramWork.Store(false)
	v0, r0 := programWork.Load(), recordingScans.Load()
	f()
	return programWork.Load() - v0, recordingScans.Load() - r0
}

// DiskReadsForTest runs f with the disk-read counter on and answers the
// paths of the project files read from disk while it ran, once per read, in
// order (noteDiskRead).
func DiskReadsForTest(f func()) []string {
	diskReadsMu.Lock()
	diskReads = nil
	diskReadsMu.Unlock()
	countingDiskReads.Store(true)
	defer countingDiskReads.Store(false)
	f()
	diskReadsMu.Lock()
	defer diskReadsMu.Unlock()
	out := diskReads
	diskReads = nil
	return out
}

// ImportsWalksForTest runs f with the imports-walk counter on and answers
// how many files' nodes importsOf walked while it ran (noteImportsWalk).
func ImportsWalksForTest(f func()) int64 {
	countingImportsWalks.Store(true)
	defer countingImportsWalks.Store(false)
	n0 := importsWalks.Load()
	f()
	return importsWalks.Load() - n0
}

// BootWorkForTest runs f with the boot-only check's counters on and
// answers the file of each body it read for its calls, once per read, how
// many of those reads were of a body it had read before, and how many
// program graphs it built (noteBootWalk, noteBootGraph).
func BootWorkForTest(f func()) (walked []*FileAnalysis, rereads int, graphs int64) {
	bootWorkMu.Lock()
	bootWalks = nil
	bootWorkMu.Unlock()
	countingBootWork.Store(true)
	defer countingBootWork.Store(false)
	g0 := bootGraphs.Load()
	f()
	graphs = bootGraphs.Load() - g0
	bootWorkMu.Lock()
	defer bootWorkMu.Unlock()
	seen := map[bootWalk]bool{}
	for _, w := range bootWalks {
		walked = append(walked, w.fa)
		if seen[w] {
			rereads++
		}
		seen[w] = true
	}
	bootWalks = nil
	return walked, rereads, graphs
}

func init() {
	checkKeptImports = true
}

// CheckSuppliesForTest runs f with the supply cross-check on (supplyChecks)
// and answers how many edits took the missing-impl supply from the entry's
// check and how many of those differ from the supply worked out from every
// file's interface.
func CheckSuppliesForTest(f func()) (made, apart int64) {
	supplyChecks.on.Store(true)
	defer supplyChecks.on.Store(false)
	m0, a0 := supplyChecks.made.Load(), supplyChecks.apart.Load()
	f()
	return supplyChecks.made.Load() - m0, supplyChecks.apart.Load() - a0
}
