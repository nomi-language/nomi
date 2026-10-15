package vmhost

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A build over the stdlib's base (analysis.StdBase) checks and lowers a
// program exactly as a build that folds every stdlib file itself: for the
// generated programs and the programs the corpus embeds, the front end's
// answer, every decline and its reason, and every untyped expression agree.
// internal/analysis's TestStdBase_ABuildOverTheBaseMatchesOneThatFoldsEveryFile
// compares the analyses themselves over the corpus, fixtures, tour and spec.
func TestStdBase_GeneratedProgramsCheckAsWithoutIt(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	inputs := generatedPrograms()
	for _, s := range corpusSeeds(t) {
		if strings.Contains(s.name, "#") {
			inputs = append(inputs, s)
		}
	}
	for _, in := range inputs {
		dir := t.TempDir()
		with := checkLowers(dir, in.src)
		prev := analysis.SetStdBaseEnabled(false)
		without := checkLowers(dir, in.src)
		analysis.SetStdBaseEnabled(prev)
		if !reflect.DeepEqual(with, without) {
			t.Errorf("%s: over the base\n%s\nfolding every stdlib file\n%s", in.name, with.describe(in.src), without.describe(in.src))
		}
	}
}
