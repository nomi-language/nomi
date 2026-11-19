package rotation

import (
	"os"
	"testing"
)

// Walk is a corpus-wide walk's choice of inputs: every input under
// NOMI_CORPUS_WALKS=full, and otherwise a fixed sample, every input whose
// index in its list is a multiple of the stride. Index 0 is always in the
// sample, so a list a walk samples on its own (one kind of input) is never
// left out as the corpus grows.
type Walk struct {
	// Full reports that NOMI_CORPUS_WALKS=full asked for the whole walk.
	Full   bool
	stride int
}

// CorpusWalk is tb's walk, sampling one input in stride unless
// NOMI_CORPUS_WALKS=full. It logs which it runs and how to run the other.
func CorpusWalk(tb testing.TB, stride int) Walk {
	tb.Helper()
	w := Walk{Full: os.Getenv("NOMI_CORPUS_WALKS") == "full", stride: stride}
	if w.Full {
		tb.Logf("the whole walk (NOMI_CORPUS_WALKS=full)")
	} else {
		tb.Logf("a sample of the walk; NOMI_CORPUS_WALKS=full runs the whole walk")
	}
	return w
}

// Takes reports whether the walk checks the input at index i of its list.
func (w Walk) Takes(i int) bool { return w.Full || i%w.stride == 0 }
