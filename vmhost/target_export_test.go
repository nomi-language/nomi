package vmhost

import "slices"

// LackingTargetForTest makes goos/goarch a target that lacks classes, for
// the test that calls it, and answers the function that removes it.
func LackingTargetForTest(goos, goarch string, classes ...string) (restore func()) {
	key := goos + "/" + goarch
	if testTargets == nil {
		testTargets = map[string][]string{}
	}
	sorted := slices.Clone(classes)
	slices.Sort(sorted)
	testTargets[key] = sorted
	return func() { delete(testTargets, key) }
}
