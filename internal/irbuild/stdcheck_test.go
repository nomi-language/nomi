package irbuild

import (
	"testing"
)

// TestStdStruct_AssertionFailureFieldsDoNotCollapse is the non-collapse claim
// the std struct representation rests on.
//
// `AssertionFailure` carries THREE `List<…>` fields at three different element
// types — two directly and one through `AssertionValue.pipeline` — plus two
// `Maybe<…>` at two different payloads. Every one of those goes through an
// intern table, and a table keyed on the CONSTRUCTOR's name rather than on the
// full instantiation would hand back one entry for all three, putting a
// `*rt.List[rt.NomiAssertionDetail]` where the code reads
// `rt.NomiAssertionValue` fields.
//
// Asserted as PAIRWISE DISTINCT kinds and as distinct renderings, because a
// text-keyed table could not separate two that rendered alike however carefully
// it was written — the argument sharedcomp.go's own guard rests on.
func TestStdStruct_AssertionFailureFieldsDoNotCollapse(t *testing.T) {
	defs := stdStructDefs()
	failure := defs[stdSpecAssertionFailure]
	byName := map[string]kind{}
	for _, f := range failure.fields {
		byName[f.nomi] = f.k
	}
	value := defs[stdSpecAssertionValue]
	var pipeline kind
	for _, f := range value.fields {
		if f.nomi == "pipeline" {
			pipeline = f.k
		}
	}
	named := map[string]kind{
		"AssertionFailure.values":  byName["values"],
		"AssertionFailure.details": byName["details"],
		"AssertionValue.pipeline":  pipeline,
		"AssertionFailure.actual":  byName["actual"],
		"AssertionFailure.binding": byName["binding"],
	}
	want := map[string]string{
		"AssertionFailure.values":  "List<AssertionValue>",
		"AssertionFailure.details": "List<AssertionDetail>",
		"AssertionValue.pipeline":  "List<AssertionPipelineStage>",
		"AssertionFailure.actual":  "Maybe<String>",
		"AssertionFailure.binding": "Maybe<AssertionBinding>",
	}
	for at, k := range named {
		if k == kindInvalid {
			t.Fatalf("%s has no kind at all", at)
		}
		if got := k.nomi(); got != want[at] {
			t.Errorf("%s renders %q, want %q", at, got, want[at])
		}
	}
	for a, ka := range named {
		for b, kb := range named {
			if a >= b {
				continue
			}
			if ka == kb {
				t.Errorf("%s and %s are ONE kind; an intern table collapsed two instantiations", a, b)
			}
			if ka.nomi() == kb.nomi() {
				t.Errorf("%s and %s render alike (%s); a text-keyed table could not separate them",
					a, b, ka.nomi())
			}
		}
	}
}
