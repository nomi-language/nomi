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

