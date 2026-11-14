package analysis_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// partnerDeriveProject is the corpus project whose partner test derives
// FromJson and Equatable for its partner's private type, with the test as
// its entry.
func partnerDeriveProject(t *testing.T) incrementalProject {
	t.Helper()
	p, err := readProject(filepath.Join("..", "..", "tests", "14-modules-and-packaging", "partner_test_derive"), "partner_test_derive")
	if err != nil {
		t.Fatal(err)
	}
	if p.entry != "todos_test" || len(p.texts) != 2 {
		t.Fatalf("the project is not the partner test and its partner: entry %q, %d files", p.entry, len(p.texts))
	}
	if dump := analysis.DumpProgramForTest(fullCheck(p, p.texts)); strings.Contains(dump, "diag ") {
		t.Fatalf("the project has diagnostics, so the harness compares two refusals:\n%s", dump)
	}
	return p
}

// partnerDeriveSteps edit the partner's declaration the test derives from
// and the test's derives: a field added (of a type the partner declares
// and of one it imports), a field's type changed, the derive dropped and
// written again, and a derive the partner already makes.
func partnerDeriveSteps() []editStep {
	return []editStep{
		replace("partner field added", "todos", "    note:  Maybe<String>\n", "    note:  Maybe<String>\n    extra: Tag\n"),
		replace("partner field of an imported type", "todos", "    extra: Tag\n", "    extra: Maybe<Uuid>\n"),
		restore("partner restored", "todos"),
		replace("test derive dropped", "todos_test", "derive Equatable for TodoJson\n", ""),
		restore("test derive back", "todos_test"),
		replace("partner derives it too", "todos", "derive ToJson for TodoJson\n", "derive ToJson for TodoJson\nderive Equatable for TodoJson\n"),
		restore("partner restored again", "todos"),
		replace("test body edited", "todos_test", "    assert todo_json.title == \"milk\"\n", "    assert todo_json.title == \"milk\"\n    assert todo_json.title != \"eggs\"\n"),
		restore("test body back", "todos_test"),
	}
}

// An incremental check of the partner-test project after each edit matches
// a full check, whichever file the edit is in.
func TestPartnerDeriveIncrementalMatchesFull(t *testing.T) {
	p := partnerDeriveProject(t)
	if diff, _ := runEdits(t, p, partnerDeriveSteps(), fuzzRecheck(), false); diff != "" {
		t.Fatal(diff)
	}
	for _, key := range []string{"todos", "todos_test"} {
		if diff := fuzzEdits(t, p, key, p.texts[key], int64(hashText("partner/"+key))); diff != "" {
			t.Fatal(diff)
		}
	}
}

// Each file of the partner-test project checked against the others'
// interfaces matches a full check after each edit: the test's derived
// impls read the partner's declaration from its interface as from its
// source, and the partner's check sees them as the program's does.
func TestPartnerDeriveFromInterfacesMatchesFull(t *testing.T) {
	p := partnerDeriveProject(t)
	texts := map[string]string{}
	for k, v := range p.texts {
		texts[k] = v
	}
	if diff := fromInterfacesDiff(p, texts); diff != "" {
		t.Fatalf("checked against interfaces differs from a full check:\n%s", diff)
	}
	for i, s := range partnerDeriveSteps() {
		text, ok := s.change(texts[s.key], p.texts[s.key])
		if !ok {
			t.Fatalf("step %d (%s) does not apply", i+1, s.kind)
		}
		texts[s.key] = text
		if diff := fromInterfacesDiff(p, texts); diff != "" {
			t.Fatalf("after step %d (%s) the program checked against interfaces differs from a full check:\n%s", i+1, s.kind, diff)
		}
	}
	for _, key := range []string{"todos", "todos_test"} {
		if diff := interfaceEdits(t, p, key, p.texts[key], int64(hashText("partner/"+key))); diff != "" {
			t.Fatal(diff)
		}
	}
}
