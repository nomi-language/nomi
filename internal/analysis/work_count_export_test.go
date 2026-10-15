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
// and how many recordings mergeRecordings scanned (noteRecordingCompares).
func CountProgramWorkForTest(f func()) (visits, scans int64) {
	countingProgramWork.Store(true)
	defer countingProgramWork.Store(false)
	v0, r0 := programWork.Load(), recordingScans.Load()
	f()
	return programWork.Load() - v0, recordingScans.Load() - r0
}
