package vm_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
)

// vmReportRecordsMatch runs the named report-shaped records of one population
// on the VM and requires each to be comparable and to equal its committed
// golden record byte for byte.
func vmReportRecordsMatch(t *testing.T, population string, ids []string) {
	t.Helper()
	set, err := expectation.Load(population)
	if err != nil {
		t.Fatal(err)
	}
	_, recorded := reportShapedIDs(set)
	for _, id := range ids {
		if _, ok := recorded[id]; !ok {
			t.Fatalf("%s: %s is not a report-shaped record", population, id)
		}
	}
	got, refused := vmReportSubsetOf(t, population, ids, recorded, vmReportPathResolver(t, population))
	if len(refused) != 0 || len(got.Cases) != len(ids) {
		t.Fatalf("%s: %d of %d record(s) comparable; refusals:\n%v", population, len(got.Cases), len(ids), refused)
	}
	if diffs := set.CompareSubset(got); len(diffs) != 0 {
		t.Fatalf("%s: the VM disagrees with the golden record:\n%v", population, diffs)
	}
}

// `==` and `!=` on a distinct with a hand-written impl call it, one with none
// compares its inner value; `List.equal?` and `Maybe.equal?` compare as `==`
// does; a list of tuples compares structurally. See internal/irbuild's
// irequality.go.
func TestVMEquality_CorpusFilesMatchTheirGoldenRecords(t *testing.T) {
	vmReportRecordsMatch(t, "corpus", []string{
		"07-structs-and-enums/distinct_equatable_impl/distinct_equatable_impl_test.nomi",
		"11-interfaces-and-impls/module_qualified_impl_dispatch/module_qualified_impl_dispatch_test.nomi",
		"04-scalars-and-text/ranges_test.nomi",
		"04-scalars-and-text/floats_test.nomi",
		"13-iterators-and-pipes/string_iterators_test.nomi",
	})
}

// A failing `==` on a std enum renders both operands as its golden record does.
func TestVMEquality_AFailingComparisonReportsLikeItsGoldenRecord(t *testing.T) {
	vmReportRecordsMatch(t, "failure", []string{"std_enum_modes.nomi"})
}

// Struct literals: a field-less struct, a generic struct over a struct
// argument, and container fields.
func TestVMStructLiteral_FilesMatchTheirGoldenRecords(t *testing.T) {
	vmReportRecordsMatch(t, "corpus", []string{
		"09-functions-and-control-flow/field_access_and_predicates_test.nomi",
		"09-functions-and-control-flow/tail_calls_test.nomi",
	})
	vmReportRecordsMatch(t, "failure", []string{"generic_struct.nomi", "tests_operands.nomi"})
}
