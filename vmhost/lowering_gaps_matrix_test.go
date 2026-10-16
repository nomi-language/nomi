package vmhost

// matrixLoweringGaps are the gaps the feature × context matrix found
// (feature_matrix_test.go): programs the front end accepts and the builder
// declines or panics on, each pinned by a minimal reproducer. They join
// knownLoweringGaps, so TestKnownLoweringGaps holds each reproducer to its
// decline. A gap whose reason the builder derives from the node alone
// (noSkip) excuses no matrix cell by that reason; mxKnown names the cells
// it fails by their shape.
var matrixLoweringGaps = []loweringGap{}

// matrixCheckerGaps are the expressions the matrix found the checker
// leaving without a type. They join knownCheckerGaps.
var matrixCheckerGaps = []checkerGap{}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, matrixLoweringGaps...)
	knownCheckerGaps = append(knownCheckerGaps, matrixCheckerGaps...)
}
